// anvilctl prepares and inspects reviewed Kubernetes executions.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	api "anvil.dev/operator/api/v1alpha1"
	"anvil.dev/operator/internal/controller"
	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

func main() {
	if err := execute(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func read(path string, value any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.UnmarshalStrict(b, value)
}
func execute(args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: anvilctl template-hash|recipe-hash|prepare|validate|doctor|inspect")
	}
	if args[0] == "template-hash" || args[0] == "recipe-hash" {
		if len(args) != 2 {
			return fmt.Errorf("usage: anvilctl %s FILE", args[0])
		}
		var value any
		if args[0] == "template-hash" {
			var t map[string]any
			if err := read(args[1], &t); err != nil {
				return err
			}
			value = t["spec"]
			if value == nil {
				return fmt.Errorf("template spec is missing")
			}
		} else {
			var r api.TrainingRecipe
			if err := read(args[1], &r); err != nil {
				return err
			}
			value = r.Spec.Binding
		}
		hash, err := controller.Hash(value)
		if err == nil {
			_, err = fmt.Fprintln(out, hash)
		}
		return err
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	recipePath := flags.String("recipe", "", "Reviewed TrainingRecipe file")
	templatePath := flags.String("template", "", "Reviewed WorkflowTemplate file")
	runPath := flags.String("run", "", "TrainingRun file for validation")
	namespace := flags.String("namespace", "", "Explicit namespace")
	kubeconfig := flags.String("kubeconfig", "", "Kubeconfig; defaults to normal Kubernetes loading rules")
	name := flags.String("name", "", "Resource name for inspection")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	switch args[0] {
	case "prepare", "validate":
		var recipe api.TrainingRecipe
		var raw map[string]any
		if err := read(*recipePath, &recipe); err != nil {
			return err
		}
		if err := read(*templatePath, &raw); err != nil {
			return err
		}
		template := &unstructured.Unstructured{Object: raw}
		if recipe.APIVersion != api.GroupVersion.String() || recipe.Kind != "TrainingRecipe" || template.GroupVersionKind() != controller.TemplateGVK {
			return fmt.Errorf("expected TrainingRecipe and WorkflowTemplate")
		}
		if *namespace != "" {
			recipe.Namespace = *namespace
			template.SetNamespace(*namespace)
		}
		if recipe.Namespace == "" || template.GetNamespace() != recipe.Namespace {
			return fmt.Errorf("recipe and template need the same explicit namespace")
		}
		if args[0] == "prepare" {
			spec, exists, err := unstructured.NestedMap(template.Object, "spec")
			if err != nil || !exists {
				return fmt.Errorf("template spec is missing")
			}
			recipe.Spec.Binding.TemplateSpecSHA256, err = controller.Hash(spec)
			if err != nil {
				return err
			}
			if template.GetAnnotations() == nil {
				template.SetAnnotations(map[string]string{})
			}
			annotations := template.GetAnnotations()
			annotations["anvil.dev/recipe-revision"] = recipe.Spec.Binding.Revision
			template.SetAnnotations(annotations)
			recipe.Spec.Enabled = false
			a, err := yaml.Marshal(template.Object)
			if err != nil {
				return err
			}
			b, err := yaml.Marshal(recipe)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(out, "%s---\n%s", a, b)
			return err
		}
		var run api.TrainingRun
		if err := read(*runPath, &run); err != nil {
			return err
		}
		if run.APIVersion != api.GroupVersion.String() || run.Kind != "TrainingRun" {
			return fmt.Errorf("expected TrainingRun")
		}
		if *namespace != "" {
			run.Namespace = *namespace
		}
		run.UID = "00000000-0000-0000-0000-000000000000"
		// Offline validation checks a held recipe without enabling it in Kubernetes.
		recipe.Spec.Enabled = true
		workflow, err := controller.BuildWorkflow(&run, &recipe, template)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(map[string]any{"valid": true, "namespace": run.Namespace, "workflowSpecSHA256": workflow.GetAnnotations()[controller.WorkflowHash]})
	case "inspect", "doctor":
		if *namespace == "" {
			return fmt.Errorf("--namespace is required")
		}
		rules := clientcmd.NewDefaultClientConfigLoadingRules()
		rules.ExplicitPath = *kubeconfig
		config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{}).ClientConfig()
		if err != nil {
			return err
		}
		config.Timeout = 10 * time.Second
		scheme := runtime.NewScheme()
		if err = api.AddToScheme(scheme); err != nil {
			return err
		}
		if err = corev1.AddToScheme(scheme); err != nil {
			return err
		}
		if err = authv1.AddToScheme(scheme); err != nil {
			return err
		}
		kubernetes, err := client.New(config, client.Options{Scheme: scheme})
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if args[0] == "inspect" {
			if *name == "" {
				return fmt.Errorf("--name is required")
			}
			var run api.TrainingRun
			if err = kubernetes.Get(ctx, types.NamespacedName{Namespace: *namespace, Name: *name}, &run); err != nil {
				return err
			}
			return json.NewEncoder(out).Encode(&run)
		}
		failed := false
		apiDiscovery, err := discovery.NewDiscoveryClientForConfig(config)
		if err != nil {
			return err
		}
		if err := controller.CheckArgoAPI(apiDiscovery); err != nil {
			failed = true
			fmt.Fprintln(out, err)
		} else {
			fmt.Fprintln(out, "Argo Workflow, WorkflowTemplate, and WorkflowTaskResult APIs: true")
		}
		for _, gvk := range []struct{ version, kind string }{{api.GroupVersion.String(), "TrainingRun"}, {api.GroupVersion.String(), "TrainingRecipe"}, {"argoproj.io/v1alpha1", "Workflow"}, {"argoproj.io/v1alpha1", "WorkflowTemplate"}} {
			list := &unstructured.UnstructuredList{}
			list.SetAPIVersion(gvk.version)
			list.SetKind(gvk.kind + "List")
			err = kubernetes.List(ctx, list, client.InNamespace(*namespace), client.Limit(1))
			fmt.Fprintf(out, "%s API/list: %v\n", gvk.kind, err == nil)
			if err != nil {
				failed = true
				fmt.Fprintln(out, err)
			}
		}
		for _, verb := range []string{"create", "patch"} {
			review := &authv1.SelfSubjectAccessReview{Spec: authv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &authv1.ResourceAttributes{Namespace: *namespace, Group: api.Group, Resource: "trainingruns", Verb: verb}}}
			err = kubernetes.Create(ctx, review)
			allowed := err == nil && review.Status.Allowed
			fmt.Fprintf(out, "TrainingRun %s: %v\n", verb, allowed)
			failed = failed || !allowed
		}
		fmt.Fprintln(out, "Review Argo workflowDefaults, executor credentials, storage, admission, and network policies before enabling recipes.")
		if failed {
			return fmt.Errorf("prerequisite checks failed")
		}
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}
