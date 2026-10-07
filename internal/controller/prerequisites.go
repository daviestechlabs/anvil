package controller

import (
	"fmt"

	"k8s.io/client-go/discovery"
)

// Discovery requires no cluster-wide CRD mutation or Secret permission.
func CheckArgoAPI(api discovery.DiscoveryInterface) error {
	resources, err := api.ServerResourcesForGroupVersion("argoproj.io/v1alpha1")
	if err != nil {
		return fmt.Errorf("Argo API discovery failed: install Argo Workflows CRDs and its controller before Anvil; check API connectivity and discovery permissions: %w", err)
	}
	for name, kind := range map[string]string{"workflows": "Workflow", "workflowtemplates": "WorkflowTemplate", "workflowtaskresults": "WorkflowTaskResult"} {
		found := false
		for _, resource := range resources.APIResources {
			if resource.Name == name && resource.Kind == kind && resource.Namespaced {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("Argo prerequisite missing: argoproj.io/v1alpha1/%s; install Argo Workflows CRDs and its controller before Anvil", kind)
		}
	}
	return nil
}
