package controller

import (
	"context"
	"reflect"
	"testing"

	api "anvil.dev/operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestWorkflowProgressBounds(t *testing.T) {
	for _, test := range []struct {
		value    string
		expected *api.WorkflowProgress
	}{
		{"3/10", &api.WorkflowProgress{Completed: 3, Total: 10}},
		{"0/0", &api.WorkflowProgress{}},
		{"1000000000/1000000000", &api.WorkflowProgress{Completed: 1_000_000_000, Total: 1_000_000_000}},
		{"", nil}, {"10/3", nil}, {"-1/10", nil}, {"1/+10", nil}, {"1/0", nil}, {"1/1000000001", nil}, {"1/2/3", nil},
	} {
		workflow := &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"progress": test.value}}}
		if actual := workflowProgress(workflow); !reflect.DeepEqual(actual, test.expected) {
			t.Fatalf("progress %q: %+v", test.value, actual)
		}
	}
}

func TestProgressObservationPreservesExecutionIdentity(t *testing.T) {
	c, run, _, _ := testClient(t)
	r := &Reconciler{Client: c, Reader: c}
	for i := 0; i < 3; i++ {
		if err := reconcileStep(t, r, run); err != nil {
			t.Fatal(err)
		}
	}
	workflow := &unstructured.Unstructured{}
	workflow.SetGroupVersionKind(WorkflowGVK)
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: run.Namespace, Name: WorkflowName(run)}, workflow); err != nil {
		t.Fatal(err)
	}
	workflow.SetUID("original-progress-workflow")
	workflow.Object["status"] = map[string]any{"phase": "Running", "progress": "3/10"}
	if err := c.Update(context.Background(), workflow); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := reconcileStep(t, r, run); err != nil {
			t.Fatal(err)
		}
	}
	if run.Status.Progress == nil || run.Status.Progress.Completed != 3 || run.Status.Progress.Total != 10 || run.Status.WorkflowRef.UID != "original-progress-workflow" || run.Status.Phase != "Running" {
		t.Fatalf("observation: %+v", run.Status)
	}
	workflow.Object["status"] = map[string]any{"phase": "Running", "progress": "3/20"}
	if err := c.Update(context.Background(), workflow); err != nil {
		t.Fatal(err)
	}
	if err := reconcileStep(t, r, run); err != nil {
		t.Fatal(err)
	}
	if run.Status.Progress.Total != 20 {
		t.Fatal("dynamic graph total did not refresh")
	}
	var all unstructured.UnstructuredList
	all.SetGroupVersionKind(WorkflowGVK.GroupVersion().WithKind("WorkflowList"))
	if err := c.List(context.Background(), &all, client.InNamespace(run.Namespace)); err != nil {
		t.Fatal(err)
	}
	if len(all.Items) != 1 {
		t.Fatal("progress created another execution")
	}
}
