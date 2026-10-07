package controller

import (
	"context"
	"fmt"
	"testing"

	api "anvil.dev/operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type failingCreate struct {
	client.Client
	after bool
	count int
}

func (c *failingCreate) Create(ctx context.Context, object client.Object, options ...client.CreateOption) error {
	c.count++
	if c.after {
		object.SetUID("original-workflow")
		if err := c.Client.Create(ctx, object, options...); err != nil {
			return err
		}
	}
	return fmt.Errorf("simulated transport loss")
}

func testClient(t *testing.T) (client.Client, *api.TrainingRun, *api.TrainingRecipe, *unstructured.Unstructured) {
	t.Helper()
	scheme := runtime.NewScheme()
	_ = api.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	run, recipe, template := fixtures(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&api.TrainingRun{}).WithObjects(run, recipe, template).Build()
	return c, run, recipe, template
}
func reconcileStep(t *testing.T, r *Reconciler, run *api.TrainingRun) error {
	t.Helper()
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(run)})
	if readErr := r.Reader.Get(context.Background(), client.ObjectKeyFromObject(run), run); readErr != nil {
		t.Fatal(readErr)
	}
	return err
}

func TestAmbiguousCreateRecoversWithoutDuplicate(t *testing.T) {
	c, run, _, _ := testClient(t)
	broken := &failingCreate{Client: c, after: true}
	r := &Reconciler{Client: broken, Reader: c}
	for i := 0; i < 2; i++ {
		if err := reconcileStep(t, r, run); err != nil {
			t.Fatal(err)
		}
	}
	if err := reconcileStep(t, r, run); err == nil {
		t.Fatal("transport failure not surfaced")
	}
	if run.Status.Phase != "Submitting" {
		t.Fatal("transport failure lost admission")
	}
	r = &Reconciler{Client: c, Reader: c}
	if err := reconcileStep(t, r, run); err != nil {
		t.Fatal(err)
	}
	if run.Status.WorkflowRef.UID != "original-workflow" || broken.count != 1 {
		t.Fatal("did not recover same workflow")
	}
}

func TestTransientCreateFailureRetainsAdmission(t *testing.T) {
	c, run, _, _ := testClient(t)
	broken := &failingCreate{Client: c}
	r := &Reconciler{Client: broken, Reader: c}
	for i := 0; i < 2; i++ {
		if err := reconcileStep(t, r, run); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := reconcileStep(t, r, run); err == nil {
			t.Fatal("failure not surfaced")
		}
		if run.Status.Phase != "Submitting" || run.Status.WorkflowRef == nil {
			t.Fatal("transient failure became terminal")
		}
	}
}

func TestDisabledRecipeHoldsAndCancellationCreatesNoWorkflow(t *testing.T) {
	c, run, recipe, _ := testClient(t)
	recipe.Spec.Enabled = false
	if err := c.Update(context.Background(), recipe); err != nil {
		t.Fatal(err)
	}
	r := &Reconciler{Client: c, Reader: c}
	for i := 0; i < 2; i++ {
		if err := reconcileStep(t, r, run); err != nil {
			t.Fatal(err)
		}
	}
	if run.Status.Phase != "Pending" {
		t.Fatal("held recipe was admitted")
	}
	run.Spec.Cancel = true
	if err := c.Update(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if err := reconcileStep(t, r, run); err != nil {
		t.Fatal(err)
	}
	if run.Status.Phase != "Cancelled" {
		t.Fatal("queued cancellation did not finish")
	}
	workflow := &unstructured.Unstructured{}
	workflow.SetGroupVersionKind(WorkflowGVK)
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: run.Namespace, Name: WorkflowName(run)}, workflow); !apierrors.IsNotFound(err) {
		t.Fatal("cancelled run created a workflow")
	}
}

func TestInvalidInputsFailBeforeCreation(t *testing.T) {
	c, run, _, _ := testClient(t)
	run.Spec.Request.Parameters = map[string]string{"command": "arbitrary"}
	if err := c.Update(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	r := &Reconciler{Client: c, Reader: c}
	for i := 0; i < 2; i++ {
		if err := reconcileStep(t, r, run); err != nil {
			t.Fatal(err)
		}
	}
	if run.Status.Phase != "Failed" {
		t.Fatal("invalid input not rejected")
	}
}

func TestTerminalRunRefreshesGenerationWithoutRepeatingExecution(t *testing.T) {
	c, run, _, _ := testClient(t)
	run.Generation = 2
	run.Spec.Cancel = true
	if err := c.Update(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	run.Status.Phase = "Succeeded"
	run.Status.ObservedGeneration = 1
	run.Status.Outputs = map[string]string{"model-sha256": "recorded"}
	if err := c.Status().Update(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	r := &Reconciler{Client: c, Reader: c}
	if err := reconcileStep(t, r, run); err != nil {
		t.Fatal(err)
	}
	if run.Status.Phase != "Succeeded" || run.Status.ObservedGeneration != 2 || run.Status.Outputs["model-sha256"] != "recorded" {
		t.Fatal("completed run changed or cannot acknowledge its generation")
	}
}
