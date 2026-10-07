package controller

import (
	"strconv"
	"strings"

	api "anvil.dev/operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Argo reports completed/total work. Counts can change as a dynamic graph grows.
// Invalid or absent progress remains unknown; it cannot fail or complete execution.
func workflowProgress(workflow *unstructured.Unstructured) *api.WorkflowProgress {
	value, _, _ := unstructured.NestedString(workflow.Object, "status", "progress")
	parts := strings.Split(value, "/")
	if len(parts) != 2 {
		return nil
	}
	counts := [2]int64{}
	for i, part := range parts {
		if len(part) == 0 || len(part) > 10 {
			return nil
		}
		for _, digit := range part {
			if digit < '0' || digit > '9' {
				return nil
			}
		}
		count, err := strconv.ParseInt(part, 10, 64)
		if err != nil || count > 1_000_000_000 {
			return nil
		}
		counts[i] = count
	}
	if counts[0] > counts[1] {
		return nil
	}
	return &api.WorkflowProgress{Completed: counts[0], Total: counts[1]}
}
