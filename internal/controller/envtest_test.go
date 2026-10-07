package controller

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	api "anvil.dev/operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	kyaml "k8s.io/apimachinery/pkg/util/yaml"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metrics "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func testArgoCRDs() []*apiextensions.CustomResourceDefinition {
	preserve := true
	crds := []*apiextensions.CustomResourceDefinition{}
	for _, kind := range []string{"Workflow", "WorkflowTemplate", "WorkflowTaskResult"} {
		plural := strings.ToLower(kind) + "s"
		crds = append(crds, &apiextensions.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{Name: plural + ".argoproj.io"},
			Spec: apiextensions.CustomResourceDefinitionSpec{Group: "argoproj.io", Scope: apiextensions.NamespaceScoped,
				Names: apiextensions.CustomResourceDefinitionNames{Plural: plural, Kind: kind},
				Versions: []apiextensions.CustomResourceDefinitionVersion{{Name: "v1alpha1", Served: true, Storage: true,
					Schema: &apiextensions.CustomResourceValidation{OpenAPIV3Schema: &apiextensions.JSONSchemaProps{Type: "object", Properties: map[string]apiextensions.JSONSchemaProps{
						"spec": {Type: "object", XPreserveUnknownFields: &preserve}, "status": {Type: "object", XPreserveUnknownFields: &preserve},
					}}}, Subresources: &apiextensions.CustomResourceSubresources{Status: &apiextensions.CustomResourceSubresourceStatus{}},
				}},
			},
		})
	}
	return crds
}

// Envtest runs a real API server and etcd. Argo status and Pod exit are simulated;
// this suite proves Anvil's API/reconciliation contract, not an Argo training execution.
func TestKubernetesAPI(t *testing.T) {
	if os.Getenv("ANVIL_ENVTEST") != "1" {
		t.Skip("set ANVIL_ENVTEST=1 and KUBEBUILDER_ASSETS for a real local API server")
	}
	crds := testArgoCRDs()
	environment := &envtest.Environment{CRDDirectoryPaths: []string{"../../config/crd"}, ErrorIfCRDPathMissing: true, CRDs: crds}
	config, err := environment.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := environment.Stop(); err != nil {
			t.Error(err)
		}
	})
	scheme := runtime.NewScheme()
	if err := api.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := rbacv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	kubernetes, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := kubernetes.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "training"}}); err != nil {
		t.Fatal(err)
	}
	reconciler := &Reconciler{Client: kubernetes, Reader: kubernetes}
	step := func(run *api.TrainingRun) {
		t.Helper()
		_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Namespace: run.Namespace, Name: run.Name}})
		if err != nil {
			t.Fatal(err)
		}
	}
	readRun := func(run *api.TrainingRun) {
		t.Helper()
		if err := kubernetes.Get(ctx, client.ObjectKeyFromObject(run), run); err != nil {
			t.Fatal(err)
		}
	}
	readWorkflow := func(run *api.TrainingRun) *unstructured.Unstructured {
		t.Helper()
		wf := &unstructured.Unstructured{}
		wf.SetGroupVersionKind(WorkflowGVK)
		if err := kubernetes.Get(ctx, types.NamespacedName{Namespace: run.Namespace, Name: WorkflowName(run)}, wf); err != nil {
			t.Fatal(err)
		}
		return wf
	}
	makeRun := func(t *testing.T, name string) *api.TrainingRun {
		t.Helper()
		run, recipe, template := fixtures(t)
		recipe.Name = "recipe-" + name
		recipe.ResourceVersion = ""
		recipe.UID = ""
		template.SetName("template-" + name)
		template.SetResourceVersion("")
		template.SetUID("")
		recipe.Spec.Binding.WorkflowTemplate = template.GetName()
		run.Name = name
		run.UID = ""
		run.ResourceVersion = ""
		run.Generation = 0
		run.Spec.Request.RecipeRef.Name = recipe.Name
		run.Spec.Request.RecipeRef.BindingSHA256, _ = Hash(recipe.Spec.Binding)
		if err := kubernetes.Create(ctx, template); err != nil {
			t.Fatal(err)
		}
		if err := kubernetes.Create(ctx, recipe); err != nil {
			t.Fatal(err)
		}
		if err := kubernetes.Create(ctx, run); err != nil {
			t.Fatal(err)
		}
		return run
	}

	t.Run("measured-progress-schema-and-identity", func(t *testing.T) {
		run := makeRun(t, "progress")
		for i := 0; i < 4; i++ {
			step(run)
		}
		workflow := readWorkflow(run)
		workflow.Object["status"] = map[string]any{"phase": "Running", "progress": "3/10"}
		if err := kubernetes.Status().Update(ctx, workflow); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 4; i++ {
			step(run)
		}
		readRun(run)
		if run.Status.Progress == nil || run.Status.Progress.Completed != 3 || run.Status.Progress.Total != 10 || run.Status.WorkflowRef.UID != string(workflow.GetUID()) || run.Status.Phase != "Running" {
			t.Fatalf("progress did not persist on original execution: %+v", run.Status)
		}
		original := run.DeepCopy()
		run.Status.Progress.Completed = 11
		if err := kubernetes.Status().Update(ctx, run); !apierrors.IsInvalid(err) {
			t.Fatalf("API accepted completed greater than total: %v", err)
		}
		run = original.DeepCopy()
		run.Status.Progress.Total = -1
		if err := kubernetes.Status().Update(ctx, run); !apierrors.IsInvalid(err) {
			t.Fatalf("API accepted negative total: %v", err)
		}
		workflow.Object["status"] = map[string]any{"phase": "Running", "progress": "10/10"}
		if err := kubernetes.Status().Update(ctx, workflow); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			step(original)
		}
		readRun(original)
		if original.Status.Phase != "Running" {
			t.Fatal("progress completed an active execution")
		}
	})

	t.Run("schema-and-immutability", func(t *testing.T) {
		run := makeRun(t, "immutable")
		before := run.DeepCopy()
		run.Spec.Request.DurationSeconds++
		if err := kubernetes.Update(ctx, run); !apierrors.IsInvalid(err) {
			t.Fatalf("mutable request accepted: %v", err)
		}
		run = before.DeepCopy()
		run.Spec.Cancel = true
		if err := kubernetes.Update(ctx, run); err != nil {
			t.Fatal(err)
		}
		readRun(run)
		run.Spec.Cancel = false
		if err := kubernetes.Update(ctx, run); !apierrors.IsInvalid(err) {
			t.Fatalf("cancellation reset accepted: %v", err)
		}
		recipe := &api.TrainingRecipe{}
		if err := kubernetes.Get(ctx, types.NamespacedName{Namespace: run.Namespace, Name: run.Spec.Request.RecipeRef.Name}, recipe); err != nil {
			t.Fatal(err)
		}
		recipe.Spec.Binding.ServiceAccountName = "other"
		if err := kubernetes.Update(ctx, recipe); !apierrors.IsInvalid(err) {
			t.Fatalf("mutable binding accepted: %v", err)
		}
		if err := kubernetes.Get(ctx, client.ObjectKeyFromObject(recipe), recipe); err != nil {
			t.Fatal(err)
		}
		recipe.Spec.Enabled = false
		if err := kubernetes.Update(ctx, recipe); err != nil {
			t.Fatal(err)
		}
		invalid := before.DeepCopy()
		invalid.Name = "bad-hash"
		invalid.UID = ""
		invalid.ResourceVersion = ""
		invalid.Spec.Request.RecipeRef.BindingSHA256 = "bad"
		if err := kubernetes.Create(ctx, invalid); !apierrors.IsInvalid(err) {
			t.Fatalf("malformed hash accepted: %v", err)
		}
		invalid.Name = "bad-key"
		invalid.Spec.Request.RecipeRef.BindingSHA256 = strings.Repeat("a", 64)
		invalid.Spec.Request.Parameters = map[string]string{"INVALID": "x"}
		if err := kubernetes.Create(ctx, invalid); !apierrors.IsInvalid(err) {
			t.Fatalf("invalid parameter key accepted: %v", err)
		}
	})
	t.Run("success-and-terminal-idempotency", func(t *testing.T) {
		run := makeRun(t, "success")
		for i := 0; i < 5; i++ {
			step(run)
		}
		readRun(run)
		if run.Status.Phase != "Running" || run.Status.WorkflowRef == nil || run.Status.WorkflowRef.UID == "" {
			t.Fatalf("run not running: %+v", run.Status)
		}
		workflow := readWorkflow(run)
		if _, exists, _ := unstructured.NestedFieldNoCopy(workflow.Object, "spec", "workflowTemplateRef"); exists {
			t.Fatal("mutable reference submitted")
		}
		status := map[string]any{"phase": "Succeeded", "outputs": map[string]any{"parameters": []any{map[string]any{"name": "model-sha256", "value": strings.Repeat("d", 64)}}}}
		workflow.Object["status"] = status
		if err := kubernetes.Status().Update(ctx, workflow); err != nil {
			t.Fatal(err)
		}
		step(run)
		readRun(run)
		if run.Status.Phase != "Succeeded" || run.Status.Outputs["model-sha256"] != strings.Repeat("d", 64) {
			t.Fatalf("output not preserved: %+v", run.Status)
		}
		rv := run.ResourceVersion
		step(run)
		readRun(run)
		if run.ResourceVersion != rv {
			t.Fatal("terminal run mutated")
		}
	})
	t.Run("cancellation-waits-for-worker-exit", func(t *testing.T) {
		run := makeRun(t, "cancel")
		for i := 0; i < 5; i++ {
			step(run)
		}
		readRun(run)
		workflow := readWorkflow(run)
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "cancel-worker", Namespace: run.Namespace, Labels: map[string]string{"workflows.argoproj.io/workflow": workflow.GetName()}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "train", Image: "test:unused"}}, RestartPolicy: corev1.RestartPolicyNever}}
		if err := kubernetes.Create(ctx, pod); err != nil {
			t.Fatal(err)
		}
		run.Spec.Cancel = true
		if err := kubernetes.Update(ctx, run); err != nil {
			t.Fatal(err)
		}
		step(run)
		workflow = readWorkflow(run)
		shutdown, _, _ := unstructured.NestedString(workflow.Object, "spec", "shutdown")
		if shutdown != "Terminate" {
			t.Fatal("termination not requested")
		}
		workflow.Object["status"] = map[string]any{"phase": "Failed"}
		if err := kubernetes.Status().Update(ctx, workflow); err != nil {
			t.Fatal(err)
		}
		step(run)
		readRun(run)
		if run.Status.Phase != "Cancelling" {
			t.Fatalf("released worker too early: %s", run.Status.Phase)
		}
		pod.Status.Phase = corev1.PodFailed
		if err := kubernetes.Status().Update(ctx, pod); err != nil {
			t.Fatal(err)
		}
		step(run)
		readRun(run)
		if run.Status.Phase != "Cancelled" {
			t.Fatalf("not cancelled: %+v", run.Status)
		}
	})
	t.Run("delete-keeps-finalizer-until-workers-stop", func(t *testing.T) {
		run := makeRun(t, "delete")
		for i := 0; i < 5; i++ {
			step(run)
		}
		readRun(run)
		if err := kubernetes.Delete(ctx, run); err != nil {
			t.Fatal(err)
		}
		step(run)
		readRun(run)
		if len(run.Finalizers) == 0 {
			t.Fatal("removed finalizer before Argo terminal")
		}
		workflow := readWorkflow(run)
		workflow.Object["status"] = map[string]any{"phase": "Failed"}
		if err := kubernetes.Status().Update(ctx, workflow); err != nil {
			t.Fatal(err)
		}
		step(run)
		if err := kubernetes.Get(ctx, client.ObjectKeyFromObject(run), run); !apierrors.IsNotFound(err) {
			t.Fatalf("deletion did not finish: %v", err)
		}
	})
	t.Run("restart-and-lost-workflow", func(t *testing.T) {
		run := makeRun(t, "restart")
		for i := 0; i < 3; i++ {
			step(run)
		}
		workflow := readWorkflow(run)
		// Create returned before status observation. A new reconciler must recover the same UID.
		restarted := &Reconciler{Client: kubernetes, Reader: kubernetes}
		for i := 0; i < 2; i++ {
			if _, err := restarted.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(run)}); err != nil {
				t.Fatal(err)
			}
		}
		readRun(run)
		if run.Status.WorkflowRef.UID != string(workflow.GetUID()) {
			t.Fatal("restart did not recover original workflow")
		}
		// Simulate an administrator forcibly removing the observation guard.
		controllerutil.RemoveFinalizer(workflow, WorkflowFinalizer)
		if err := kubernetes.Update(ctx, workflow); err != nil {
			t.Fatal(err)
		}
		if err := kubernetes.Delete(ctx, workflow); err != nil {
			t.Fatal(err)
		}
		step(run)
		readRun(run)
		if run.Status.Phase != "Failed" {
			t.Fatal("missing recorded workflow did not fail")
		}
		step(run)
		wf := &unstructured.Unstructured{}
		wf.SetGroupVersionKind(WorkflowGVK)
		if err := kubernetes.Get(ctx, types.NamespacedName{Namespace: run.Namespace, Name: WorkflowName(run)}, wf); !apierrors.IsNotFound(err) {
			t.Fatal("lost workflow restarted")
		}
	})
	t.Run("workflow-deletion-retains-result-until-durable-observation", func(t *testing.T) {
		run := makeRun(t, "ttl-retention")
		for i := 0; i < 5; i++ {
			step(run)
		}
		readRun(run)
		workflow := readWorkflow(run)
		uid := workflow.GetUID()
		if !controllerutil.ContainsFinalizer(workflow, WorkflowFinalizer) {
			t.Fatal("created Workflow lacks its observation guard")
		}
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "ttl-worker", Namespace: run.Namespace, Labels: map[string]string{"workflows.argoproj.io/workflow": workflow.GetName()}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "train", Image: "test:unused"}}, RestartPolicy: corev1.RestartPolicyNever}}
		if err := kubernetes.Create(ctx, pod); err != nil {
			t.Fatal(err)
		}
		workflow.Object["status"] = map[string]any{"phase": "Succeeded", "outputs": map[string]any{"parameters": []any{map[string]any{"name": "model-sha256", "value": strings.Repeat("e", 64)}}}}
		if err := kubernetes.Status().Update(ctx, workflow); err != nil {
			t.Fatal(err)
		}
		// This is the deletion request that Argo TTL would make during an operator outage.
		if err := kubernetes.Delete(ctx, workflow); err != nil {
			t.Fatal(err)
		}
		step(run)
		readRun(run)
		if terminal(run.Status.Phase) || readWorkflow(run).GetDeletionTimestamp() == nil {
			t.Fatal("released a deleted Workflow before worker exit")
		}
		pod.Status.Phase = corev1.PodSucceeded
		if err := kubernetes.Status().Update(ctx, pod); err != nil {
			t.Fatal(err)
		}
		step(run)
		readRun(run)
		if run.Status.Phase != "Succeeded" || run.Status.Outputs["model-sha256"] != strings.Repeat("e", 64) || run.Status.WorkflowRef.UID != string(uid) {
			t.Fatalf("completion did not preserve the original execution: %+v", run.Status)
		}
		if !controllerutil.ContainsFinalizer(readWorkflow(run), WorkflowFinalizer) {
			t.Fatal("released observation before its completion patch")
		}
		// A restarted controller can finish the durable completion and release the pending deletion.
		restarted := &Reconciler{Client: kubernetes, Reader: kubernetes}
		if _, err := restarted.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(run)}); err != nil {
			t.Fatal(err)
		}
		wf := &unstructured.Unstructured{}
		wf.SetGroupVersionKind(WorkflowGVK)
		if err := kubernetes.Get(ctx, types.NamespacedName{Namespace: run.Namespace, Name: WorkflowName(run)}, wf); !apierrors.IsNotFound(err) {
			t.Fatalf("observed Workflow deletion did not finish: %v", err)
		}
		step(run)
		readRun(run)
		if run.Status.Phase != "Succeeded" || run.Status.Outputs["model-sha256"] != strings.Repeat("e", 64) {
			t.Fatal("Workflow cleanup changed the durable run result")
		}
	})
	t.Run("shipped-rbac", func(t *testing.T) {
		apply := func(path string, user string) {
			t.Helper()
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			decoder := kyaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
			for {
				var object unstructured.Unstructured
				err := decoder.Decode(&object.Object)
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				object.SetNamespace("training")
				if object.GetKind() == "RoleBinding" {
					object.Object["subjects"] = []any{map[string]any{"kind": "User", "apiGroup": "rbac.authorization.k8s.io", "name": user}}
				}
				if err := kubernetes.Create(ctx, &object); err != nil {
					t.Fatal(err)
				}
			}
		}
		apply("../../config/rbac/role.yaml", "operator-user")
		apply("../../config/rbac/run-submitter.yaml", "submitter-user")
		bind := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "submitter-test", Namespace: "training"}, Subjects: []rbacv1.Subject{{Kind: "User", APIGroup: "rbac.authorization.k8s.io", Name: "submitter-user"}}, RoleRef: rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: "anvil-run-submitter"}}
		if err := kubernetes.Create(ctx, bind); err != nil {
			t.Fatal(err)
		}
		operatorUser, err := environment.AddUser(envtest.User{Name: "operator-user"}, config)
		if err != nil {
			t.Fatal(err)
		}
		submitterUser, err := environment.AddUser(envtest.User{Name: "submitter-user"}, config)
		if err != nil {
			t.Fatal(err)
		}
		operatorClient, err := client.New(operatorUser.Config(), client.Options{Scheme: scheme})
		if err != nil {
			t.Fatal(err)
		}
		submitterClient, err := client.New(submitterUser.Config(), client.Options{Scheme: scheme})
		if err != nil {
			t.Fatal(err)
		}
		var runs api.TrainingRunList
		if err := wait.PollUntilContextTimeout(ctx, 50*time.Millisecond, 5*time.Second, true, func(ctx context.Context) (bool, error) {
			return submitterClient.List(ctx, &runs, client.InNamespace("training")) == nil, nil
		}); err != nil {
			t.Fatal(err)
		}
		original := makeRun(t, "rbac-source")
		allowed := original.DeepCopy()
		allowed.Name = "rbac-submitted"
		allowed.UID = ""
		allowed.ResourceVersion = ""
		if err := submitterClient.Create(ctx, allowed); err != nil {
			t.Fatal("submitter cannot submit", err)
		}
		allowed.Spec.Cancel = true
		if err := submitterClient.Update(ctx, allowed); !apierrors.IsForbidden(err) {
			t.Fatalf("submitter unexpectedly has update: %v", err)
		}
		patchBase := allowed.DeepCopy()
		allowed.Spec.Cancel = true
		if err := submitterClient.Patch(ctx, allowed, client.MergeFrom(patchBase)); err != nil {
			t.Fatal("submitter cannot patch", err)
		}
		allowed.Status.Phase = "Succeeded"
		if err := submitterClient.Status().Update(ctx, allowed); !apierrors.IsForbidden(err) {
			t.Fatalf("submitter can forge status: %v", err)
		}
		recipe := &api.TrainingRecipe{}
		if err := operatorClient.Get(ctx, types.NamespacedName{Namespace: "training", Name: original.Spec.Request.RecipeRef.Name}, recipe); err != nil {
			t.Fatal("operator cannot read recipe", err)
		}
		forbiddenRecipe := recipe.DeepCopy()
		forbiddenRecipe.Name = "rbac-forbidden"
		forbiddenRecipe.UID = ""
		forbiddenRecipe.ResourceVersion = ""
		if err := submitterClient.Create(ctx, forbiddenRecipe); !apierrors.IsForbidden(err) {
			t.Fatalf("submitter can admit recipe: %v", err)
		}
		if err := operatorClient.Get(ctx, types.NamespacedName{Namespace: "training", Name: "anything"}, &corev1.Secret{}); !apierrors.IsForbidden(err) {
			t.Fatalf("operator can read Secrets: %v", err)
		}
		promotion := &api.ModelPromotion{ObjectMeta: metav1.ObjectMeta{Name: "forbidden-promotion", Namespace: "training"}}
		if err := submitterClient.Create(ctx, promotion); !apierrors.IsForbidden(err) {
			t.Fatalf("run submitter can approve promotion: %v", err)
		}
		if err := operatorClient.Create(ctx, promotion); !apierrors.IsForbidden(err) {
			t.Fatalf("operator can create approval decisions: %v", err)
		}
		if err := operatorClient.Update(ctx, recipe); !apierrors.IsForbidden(err) {
			t.Fatalf("operator can change recipe: %v", err)
		}
		scopedReconciler := &Reconciler{Client: operatorClient, Reader: operatorClient}
		for i := 0; i < 5; i++ {
			if _, err := scopedReconciler.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(original)}); err != nil {
				t.Fatal("shipped operator Role cannot reconcile", err)
			}
		}
		readRun(original)
		if original.Status.Phase != "Running" {
			t.Fatal("RBAC run not running")
		}
	})
	t.Run("evidence-api", func(t *testing.T) { testEvidenceAPI(t, ctx, kubernetes) })
	t.Run("manager-watch", func(t *testing.T) {
		manager, err := ctrl.NewManager(config, ctrl.Options{Scheme: scheme, Cache: cache.Options{DefaultNamespaces: map[string]cache.Config{"training": {}}}, Metrics: metrics.Options{BindAddress: "0"}, HealthProbeBindAddress: "0"})
		if err != nil {
			t.Fatal(err)
		}
		managed := &Reconciler{Client: manager.GetClient(), Reader: manager.GetAPIReader()}
		if err := managed.SetupWithManager(manager); err != nil {
			t.Fatal(err)
		}
		managerContext, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- manager.Start(managerContext) }()
		t.Cleanup(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(10 * time.Second):
				t.Error("manager did not stop")
			}
		})
		run := makeRun(t, "watched")
		if err := wait.PollUntilContextTimeout(ctx, 50*time.Millisecond, 10*time.Second, true, func(ctx context.Context) (bool, error) {
			if err := kubernetes.Get(ctx, client.ObjectKeyFromObject(run), run); err != nil {
				return false, err
			}
			return run.Status.Phase == "Running", nil
		}); err != nil {
			t.Fatalf("manager did not reconcile resource: %v", err)
		}
	})

}
