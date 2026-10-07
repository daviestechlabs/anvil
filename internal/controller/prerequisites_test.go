package controller

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakediscovery "k8s.io/client-go/discovery/fake"
	clienttesting "k8s.io/client-go/testing"
)

func TestArgoPrerequisitesRequireAllExecutionAPIs(t *testing.T) {
	discovery := &fakediscovery.FakeDiscovery{Fake: &clienttesting.Fake{}}
	if err := CheckArgoAPI(discovery); err == nil || !strings.Contains(err.Error(), "install Argo Workflows") {
		t.Fatalf("missing group accepted: %v", err)
	}
	resources := &metav1.APIResourceList{GroupVersion: "argoproj.io/v1alpha1"}
	discovery.Resources = []*metav1.APIResourceList{resources}
	for _, kind := range []string{"Workflow", "WorkflowTemplate", "WorkflowTaskResult"} {
		if err := CheckArgoAPI(discovery); err == nil {
			t.Fatal("incomplete Argo API accepted")
		}
		resources.APIResources = append(resources.APIResources, metav1.APIResource{Name: strings.ToLower(kind) + "s", Kind: kind, Namespaced: true})
	}
	if err := CheckArgoAPI(discovery); err != nil {
		t.Fatal(err)
	}
	// No mutation or adoption of the shared Argo installation is performed.
	for _, action := range discovery.Actions() {
		if action.GetVerb() != "get" {
			t.Fatalf("unexpected action: %v", action)
		}
	}
}
