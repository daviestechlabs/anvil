package controller

import (
	"strings"
	"testing"

	api "anvil.dev/operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

func fixtures(t *testing.T) (*api.TrainingRun, *api.TrainingRecipe, *unstructured.Unstructured) {
	t.Helper()
	template := &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{
		"entrypoint": "train", "templates": []any{map[string]any{"name": "train", "container": map[string]any{"image": "example.org/trainer@sha256:" + strings.Repeat("a", 64), "command": []any{"/train"}}}},
	}}}
	template.SetGroupVersionKind(TemplateGVK)
	template.SetName("reviewed-training-v1")
	template.SetNamespace("training")
	template.SetUID(types.UID("template-uid"))
	template.SetAnnotations(map[string]string{"anvil.daviestechlabs.io/recipe-revision": "v1"})
	spec, _, _ := unstructured.NestedMap(template.Object, "spec")
	templateHash, _ := Hash(spec)
	defaultValue := "200"
	recipe := &api.TrainingRecipe{ObjectMeta: metav1.ObjectMeta{Name: "training-v1", Namespace: "training", UID: types.UID("recipe-uid")}, Spec: api.TrainingRecipeSpec{Enabled: true, Binding: api.RecipeBinding{
		Revision: "v1", WorkflowTemplate: template.GetName(), TemplateSpecSHA256: templateHash, ServiceAccountName: "trainer", ConcurrencyKey: "cpu-training", MaxDurationSeconds: 300,
		FixedParameters: map[string]string{"manifest-sha256": strings.Repeat("b", 64)}, Parameters: map[string]api.Parameter{"epochs": {Default: &defaultValue, Required: true, MaxLength: 3, Choices: []string{"100", "200"}}}, Outputs: []string{"model-sha256"},
	}}}
	bindingHash, _ := Hash(recipe.Spec.Binding)
	run := &api.TrainingRun{ObjectMeta: metav1.ObjectMeta{Name: "train", Namespace: "training", UID: types.UID("44444444-aaaa-bbbb-cccc-111111111111"), Generation: 1}, Spec: api.TrainingRunSpec{Request: api.TrainingRequest{RecipeRef: api.RecipeReference{Name: recipe.Name, BindingSHA256: bindingHash}, DurationSeconds: 120}}}
	return run, recipe, template
}

func TestWorkflowBindingAndSnapshot(t *testing.T) {
	run, recipe, template := fixtures(t)
	workflow, err := BuildWorkflow(run, recipe, template)
	if err != nil {
		t.Fatal(err)
	}
	run.Status.WorkflowSpecSHA256 = workflow.GetAnnotations()[WorkflowHash]
	if err := VerifyWorkflow(run, workflow); err != nil {
		t.Fatal(err)
	}
	spec, _, _ := unstructured.NestedMap(workflow.Object, "spec")
	if hasReference(spec) {
		t.Fatal("workflow contains a late template reference")
	}
	if spec["serviceAccountName"] != "trainer" || spec["activeDeadlineSeconds"] != int64(120) {
		t.Fatal(spec)
	}
	locks, _, err := unstructured.NestedSlice(spec, "synchronization", "mutexes")
	if err != nil || len(locks) != 1 || locks[0].(map[string]any)["name"] != "cpu-training" {
		t.Fatal("capacity lock missing")
	}
	_ = unstructured.SetNestedField(template.Object, "changed", "spec", "entrypoint")
	if err := VerifyWorkflow(run, workflow); err != nil {
		t.Fatal("template mutation changed existing workflow", err)
	}
	if _, err := BuildWorkflow(run, recipe, template); err == nil {
		t.Fatal("admitted changed template bytes")
	}
}

func TestAdmissionRejectsUnboundExecution(t *testing.T) {
	for _, name := range []string{"binding", "duration", "revision", "reference", "image", "suspend", "arguments", "unknown", "fixed", "choice"} {
		t.Run(name, func(t *testing.T) {
			run, recipe, template := fixtures(t)
			switch name {
			case "binding":
				run.Spec.Request.RecipeRef.BindingSHA256 = strings.Repeat("f", 64)
			case "duration":
				run.Spec.Request.DurationSeconds = 301
			case "revision":
				template.SetAnnotations(nil)
			case "reference":
				_ = unstructured.SetNestedField(template.Object, map[string]any{"name": "mutable"}, "spec", "workflowTemplateRef")
			case "image":
				_ = unstructured.SetNestedSlice(template.Object, []any{map[string]any{"container": map[string]any{"image": "trainer:latest"}}}, "spec", "templates")
			case "suspend":
				_ = unstructured.SetNestedField(template.Object, true, "spec", "suspend")
			case "arguments":
				_ = unstructured.SetNestedSlice(template.Object, []any{map[string]any{"name": "arbitrary-command", "value": "rm"}}, "spec", "arguments", "parameters")
			case "unknown":
				run.Spec.Request.Parameters = map[string]string{"command": "rm"}
			case "fixed":
				recipe.Spec.Binding.Parameters["manifest-sha256"] = api.Parameter{MaxLength: 64}
			case "choice":
				run.Spec.Request.Parameters = map[string]string{"epochs": "999"}
			}
			if name != "binding" {
				spec, _, _ := unstructured.NestedMap(template.Object, "spec")
				recipe.Spec.Binding.TemplateSpecSHA256, _ = Hash(spec)
				run.Spec.Request.RecipeRef.BindingSHA256, _ = Hash(recipe.Spec.Binding)
			}
			if _, err := BuildWorkflow(run, recipe, template); err == nil {
				t.Fatal("invalid execution admitted")
			}
		})
	}
}

func TestWorkflowCollisionAndDrift(t *testing.T) {
	for _, name := range []string{"owner", "uid", "request", "spec", "rehash", "shutdown"} {
		t.Run(name, func(t *testing.T) {
			run, recipe, template := fixtures(t)
			workflow, err := BuildWorkflow(run, recipe, template)
			if err != nil {
				t.Fatal(err)
			}
			run.Status.WorkflowSpecSHA256 = workflow.GetAnnotations()[WorkflowHash]
			switch name {
			case "owner":
				workflow.SetOwnerReferences(nil)
			case "uid":
				run.Status.WorkflowRef = &api.WorkflowReference{UID: "original"}
				workflow.SetUID("replacement")
			case "request":
				run.Spec.Request.DurationSeconds = 119
			case "spec":
				_ = unstructured.SetNestedField(workflow.Object, "attacker", "spec", "serviceAccountName")
			case "rehash":
				_ = unstructured.SetNestedField(workflow.Object, "attacker", "spec", "serviceAccountName")
				spec, _, _ := unstructured.NestedMap(workflow.Object, "spec")
				hash, _ := Hash(spec)
				annotations := workflow.GetAnnotations()
				annotations[WorkflowHash] = hash
				workflow.SetAnnotations(annotations)
			case "shutdown":
				_ = unstructured.SetNestedField(workflow.Object, "Terminate", "spec", "shutdown")
			}
			if err := VerifyWorkflow(run, workflow); err == nil {
				t.Fatal("unbound workflow accepted")
			}
		})
	}
	run, recipe, template := fixtures(t)
	workflow, _ := BuildWorkflow(run, recipe, template)
	run.Status.WorkflowSpecSHA256 = workflow.GetAnnotations()[WorkflowHash]
	run.Spec.Cancel = true
	_ = unstructured.SetNestedField(workflow.Object, "Terminate", "spec", "shutdown")
	if err := VerifyWorkflow(run, workflow); err != nil {
		t.Fatal(err)
	}
}

func TestParameterRules(t *testing.T) {
	binding := api.RecipeBinding{Parameters: map[string]api.Parameter{"hash": {Required: true, MaxLength: 64, Pattern: "[a-f0-9]{64}"}}}
	for _, supplied := range []map[string]string{nil, {"hash": "not-a-hash"}, {"hash": strings.Repeat("a", 65)}} {
		if _, err := Parameters(binding, supplied); err == nil {
			t.Fatal("bad parameter admitted")
		}
	}
	if _, err := Parameters(binding, map[string]string{"hash": strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
}

func TestReviewedRetentionUsesObservationGuard(t *testing.T) {
	run, recipe, template := fixtures(t)
	defaults := map[string]any{"secondsAfterSuccess": int64(86400), "secondsAfterFailure": int64(259200), "secondsAfterCompletion": int64(604800)}
	_ = unstructured.SetNestedField(template.Object, defaults, "spec", "ttlStrategy")
	_ = unstructured.SetNestedField(template.Object, map[string]any{"strategy": "OnWorkflowCompletion"}, "spec", "podGC")
	spec, _, _ := unstructured.NestedMap(template.Object, "spec")
	recipe.Spec.Binding.TemplateSpecSHA256, _ = Hash(spec)
	run.Spec.Request.RecipeRef.BindingSHA256, _ = Hash(recipe.Spec.Binding)
	workflow, err := BuildWorkflow(run, recipe, template)
	if err != nil {
		t.Fatal(err)
	}
	if len(workflow.GetFinalizers()) != 1 || workflow.GetFinalizers()[0] != WorkflowFinalizer {
		t.Fatal("reviewed retention lacks a Workflow observation finalizer")
	}
	run.Status.WorkflowSpecSHA256 = workflow.GetAnnotations()[WorkflowHash]
	if err := VerifyWorkflow(run, workflow); err != nil {
		t.Fatal(err)
	}
	_ = unstructured.SetNestedField(workflow.Object, int64(1), "spec", "ttlStrategy", "secondsAfterSuccess")
	if err := VerifyWorkflow(run, workflow); err == nil {
		t.Fatal("retention mutation escaped its reviewed specification hash")
	}
}

func TestWorkflowDefaultsCannotEscapeBinding(t *testing.T) {
	// Argo 4.1 merges controller defaults into inline Workflow specs.
	// Do not normalize these additions away: they can change execution or delete its record.
	for _, defaults := range []map[string]any{
		{"ttlStrategy": map[string]any{"secondsAfterSuccess": int64(86400)}},
		{"podGC": map[string]any{"strategy": "OnWorkflowCompletion"}},
		{"templateDefaults": map[string]any{"container": map[string]any{"env": []any{map[string]any{"name": "UNREVIEWED", "value": "injected"}}}}},
	} {
		run, recipe, template := fixtures(t)
		workflow, err := BuildWorkflow(run, recipe, template)
		if err != nil {
			t.Fatal(err)
		}
		run.Status.WorkflowSpecSHA256 = workflow.GetAnnotations()[WorkflowHash]
		for field, value := range defaults {
			if err := unstructured.SetNestedField(workflow.Object, value, "spec", field); err != nil {
				t.Fatal(err)
			}
		}
		if err := VerifyWorkflow(run, workflow); err == nil || !strings.Contains(err.Error(), "Argo workflowDefaults") {
			t.Fatalf("unreviewed controller defaults need an actionable conflict: %v", err)
		}
	}
}

func TestArgoEmptyTemplateSerializationPreservesBinding(t *testing.T) {
	run, recipe, template := fixtures(t)
	workflow, err := BuildWorkflow(run, recipe, template)
	if err != nil {
		t.Fatal(err)
	}
	run.Status.WorkflowSpecSHA256 = workflow.GetAnnotations()[WorkflowHash]
	serialized := workflow.DeepCopy()
	item := serialized.Object["spec"].(map[string]any)["templates"].([]any)[0].(map[string]any)
	for _, key := range []string{"metadata", "inputs", "outputs"} {
		item[key] = map[string]any{}
	}
	container := item["container"].(map[string]any)
	container["name"] = ""
	container["resources"] = map[string]any{}
	if err := VerifyWorkflow(run, serialized); err != nil {
		t.Fatal("Argo empty struct serialization changed execution identity", err)
	}
	for _, name := range []string{"metadata", "inputs", "outputs", "resources", "name"} {
		t.Run(name, func(t *testing.T) {
			changed := serialized.DeepCopy()
			item := changed.Object["spec"].(map[string]any)["templates"].([]any)[0].(map[string]any)
			switch name {
			case "metadata":
				item[name] = map[string]any{"labels": map[string]any{"injected": "true"}}
			case "inputs", "outputs":
				item[name] = map[string]any{"artifacts": []any{map[string]any{"name": "injected"}}}
			case "resources":
				item["container"].(map[string]any)[name] = map[string]any{"limits": map[string]any{"cpu": "8"}}
			case "name":
				item["container"].(map[string]any)[name] = "injected"
			}
			if err := VerifyWorkflow(run, changed); err == nil {
				t.Fatal("nonempty execution field escaped integrity verification")
			}
		})
	}
}
