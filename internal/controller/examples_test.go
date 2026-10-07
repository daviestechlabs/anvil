package controller

import (
	"os"
	"testing"

	api "anvil.dev/operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

func TestPublishedExampleBindings(t *testing.T) {
	read := func(name string, target any) {
		t.Helper()
		bytes, err := os.ReadFile("../../examples/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if err := yaml.UnmarshalStrict(bytes, target); err != nil {
			t.Fatal(err)
		}
	}
	var recipe api.TrainingRecipe
	var run api.TrainingRun
	var template map[string]any
	read("recipe.yaml", &recipe)
	read("run.yaml", &run)
	read("workflow-template.yaml", &template)
	run.UID = "44444444-aaaa-bbbb-cccc-111111111111"
	recipe.UID = "recipe-example"
	workflow, err := BuildWorkflow(&run, &recipe, &unstructured.Unstructured{Object: template})
	if err != nil {
		t.Fatal(err)
	}
	run.Status.WorkflowSpecSHA256 = workflow.GetAnnotations()[WorkflowHash]
	if err := VerifyWorkflow(&run, workflow); err != nil {
		t.Fatal(err)
	}
}
