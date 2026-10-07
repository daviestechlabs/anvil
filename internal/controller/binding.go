package controller

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"unicode/utf8"

	api "anvil.dev/operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const Finalizer = "anvil.dev/training-cleanup"
const WorkflowFinalizer = "anvil.dev/workflow-observation"
const WorkflowHash = "anvil.dev/workflow-spec-sha256"
const RequestHash = "anvil.dev/request-sha256"

var WorkflowGVK = apiVersion("Workflow")
var TemplateGVK = apiVersion("WorkflowTemplate")
var namePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
var imagePattern = regexp.MustCompile(`^[^{}\s]+@sha256:[a-f0-9]{64}$`)
var shaPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func Hash(value any) (string, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	// Hash JSON by sorted object keys, including Go structs. Preserve exact JSON numbers.
	var normalized any
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	if err := decoder.Decode(&normalized); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// Argo's typed templates serialize zero-valued structs and Container.Name even
// when the submitted JSON omitted them. Normalize only those known empty fields.
// Nonempty metadata, I/O bindings, resources and names remain part of the hash.
func workflowSpecHash(spec map[string]any) (string, error) {
	return workflowSpecHashVersion(spec, "2")
}

func workflowSpecHashVersion(spec map[string]any, version string) (string, error) {
	normalized := (&unstructured.Unstructured{Object: spec}).DeepCopy().Object
	removeEmptyMap := func(object map[string]any, key string) {
		if value, ok := object[key].(map[string]any); ok && len(value) == 0 {
			delete(object, key)
		}
	}
	templates, _ := normalized["templates"].([]any)
	for _, raw := range templates {
		template, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		for _, key := range []string{"metadata", "inputs", "outputs"} {
			removeEmptyMap(template, key)
		}
		for _, key := range []string{"container", "script"} {
			if container, ok := template[key].(map[string]any); ok {
				if name, ok := container["name"].(string); ok && name == "" {
					delete(container, "name")
				}
				removeEmptyMap(container, "resources")
				// Kubernetes VolumeMount.ReadOnly defaults to false and Argo omits that zero value.
				// A true value remains bound; this cannot authorize a writable replacement of a read-only mount.
				if mounts, ok := container["volumeMounts"].([]any); ok && version == "2" {
					for _, raw := range mounts {
						if mount, ok := raw.(map[string]any); ok {
							if value, ok := mount["readOnly"].(bool); ok && !value {
								delete(mount, "readOnly")
							}
						}
					}
				}
			}
		}
	}
	return Hash(normalized)
}

func Parameters(binding api.RecipeBinding, supplied map[string]string) (map[string]string, error) {
	result := map[string]string{}
	for key, value := range binding.FixedParameters {
		if !namePattern.MatchString(key) {
			return nil, fmt.Errorf("invalid fixed parameter %q", key)
		}
		result[key] = value
	}
	for key := range supplied {
		if _, ok := binding.Parameters[key]; !ok {
			return nil, fmt.Errorf("unknown parameter %q", key)
		}
	}
	for key, rule := range binding.Parameters {
		if !namePattern.MatchString(key) {
			return nil, fmt.Errorf("invalid parameter %q", key)
		}
		if _, fixed := result[key]; fixed {
			return nil, fmt.Errorf("parameter %q is fixed and editable", key)
		}
		value, present := supplied[key]
		if !present && rule.Default != nil {
			value, present = *rule.Default, true
		}
		if rule.Required && (!present || value == "") {
			return nil, fmt.Errorf("parameter %q is required", key)
		}
		if rule.MaxLength < 1 || rule.MaxLength > 8000 {
			return nil, fmt.Errorf("invalid maximum length for %q", key)
		}
		if !present {
			continue
		}
		if utf8.RuneCountInString(value) > rule.MaxLength {
			return nil, fmt.Errorf("parameter %q is too long", key)
		}
		if len(rule.Choices) > 0 {
			found := false
			for _, choice := range rule.Choices {
				if value == choice {
					found = true
				}
			}
			if !found {
				return nil, fmt.Errorf("parameter %q is outside the allowlist", key)
			}
		}
		if rule.Pattern != "" {
			pattern, err := regexp.Compile("^(?:" + rule.Pattern + ")$")
			if err != nil {
				return nil, fmt.Errorf("invalid recipe pattern for %q", key)
			}
			if !pattern.MatchString(value) {
				return nil, fmt.Errorf("parameter %q has an invalid format", key)
			}
		}
		result[key] = value
	}
	return result, nil
}

// Nested template references would escape the content hash. All executable code must be in this template.
func hasReference(value any) bool {
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			if key == "templateRef" || key == "workflowTemplateRef" {
				return true
			}
			if hasReference(child) {
				return true
			}
		}
	case []any:
		for _, child := range item {
			if hasReference(child) {
				return true
			}
		}
	}
	return false
}

func validateImages(value any) error {
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			if key == "image" {
				image, ok := child.(string)
				if !ok || !imagePattern.MatchString(image) {
					return fmt.Errorf("workflow images must use static SHA-256 digests")
				}
			}
			if err := validateImages(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range item {
			if err := validateImages(child); err != nil {
				return err
			}
		}
	}
	return nil
}

func BuildWorkflow(run *api.TrainingRun, recipe *api.TrainingRecipe, template *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	if recipe.Name != run.Spec.Request.RecipeRef.Name || recipe.Namespace != run.Namespace {
		return nil, fmt.Errorf("recipe identity differs")
	}
	binding := recipe.Spec.Binding
	bindingHash, err := Hash(binding)
	if err != nil {
		return nil, err
	}
	if !recipe.Spec.Enabled {
		return nil, fmt.Errorf("recipe is disabled")
	}
	if run.Spec.Request.RecipeRef.BindingSHA256 != bindingHash {
		return nil, fmt.Errorf("recipe binding hash differs")
	}
	if run.Spec.Request.DurationSeconds < 1 || run.Spec.Request.DurationSeconds > binding.MaxDurationSeconds {
		return nil, fmt.Errorf("duration exceeds recipe limit")
	}
	if template.GetNamespace() != run.Namespace || template.GetName() != binding.WorkflowTemplate {
		return nil, fmt.Errorf("template identity differs")
	}
	revision := template.GetAnnotations()["anvil.dev/recipe-revision"]
	legacyRevision := template.GetAnnotations()["anvil.daviestechlabs.io/recipe-revision"]
	if revision == "" {
		revision = legacyRevision
	}
	if revision != binding.Revision || (legacyRevision != "" && legacyRevision != revision) {
		return nil, fmt.Errorf("template revision differs")
	}
	spec, exists, err := unstructured.NestedMap(template.Object, "spec")
	if err != nil || !exists {
		return nil, fmt.Errorf("template spec is missing")
	}
	templateHash, err := Hash(spec)
	if err != nil {
		return nil, err
	}
	if !shaPattern.MatchString(binding.TemplateSpecSHA256) || templateHash != binding.TemplateSpecSHA256 {
		return nil, fmt.Errorf("template content hash differs")
	}
	if hasReference(spec) {
		return nil, fmt.Errorf("external template references are not supported")
	}
	for _, field := range []string{"shutdown", "suspend"} {
		if _, exists := spec[field]; exists {
			return nil, fmt.Errorf("template must omit %s", field)
		}
	}
	if err := validateImages(spec); err != nil {
		return nil, err
	}
	if !namePattern.MatchString(binding.ConcurrencyKey) || !namePattern.MatchString(binding.ServiceAccountName) {
		return nil, fmt.Errorf("invalid worker or concurrency binding")
	}
	parameters, err := Parameters(binding, run.Spec.Request.Parameters)
	if err != nil {
		return nil, err
	}
	// Do not allow unbound template argument defaults to enter a run.
	existing, _, err := unstructured.NestedSlice(spec, "arguments", "parameters")
	if err != nil {
		return nil, err
	}
	for _, raw := range existing {
		parameter, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid template argument")
		}
		name, _ := parameter["name"].(string)
		if _, exists := parameters[name]; !exists {
			return nil, fmt.Errorf("template argument %q is not bound by recipe", name)
		}
	}
	if artifacts, found, _ := unstructured.NestedSlice(spec, "arguments", "artifacts"); found && len(artifacts) > 0 {
		return nil, fmt.Errorf("template artifact arguments are not supported")
	}
	keys := make([]string, 0, len(parameters))
	for key := range parameters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	arguments := make([]any, 0, len(keys))
	for _, key := range keys {
		arguments = append(arguments, map[string]any{"name": key, "value": parameters[key]})
	}
	spec["arguments"] = map[string]any{"parameters": arguments}
	spec["serviceAccountName"] = binding.ServiceAccountName
	spec["activeDeadlineSeconds"] = run.Spec.Request.DurationSeconds
	spec["synchronization"] = map[string]any{"mutexes": []any{map[string]any{"name": binding.ConcurrencyKey}}}
	// Argo retries inside a reviewed template are permitted; the operator never resubmits a completed run.
	workflowHash, err := workflowSpecHash(spec)
	if err != nil {
		return nil, err
	}
	requestHash, err := Hash(run.Spec.Request)
	if err != nil {
		return nil, err
	}
	workflow := &unstructured.Unstructured{Object: map[string]any{"spec": spec}}
	workflow.SetGroupVersionKind(WorkflowGVK)
	workflow.SetName(WorkflowName(run))
	workflow.SetNamespace(run.Namespace)
	workflow.SetLabels(map[string]string{"app.kubernetes.io/managed-by": "anvil-operator", "anvil.dev/run-uid": string(run.UID)})
	workflow.SetAnnotations(map[string]string{WorkflowHash: workflowHash, RequestHash: requestHash, "anvil.dev/recipe-uid": string(recipe.UID), "anvil.dev/workflow-hash-version": "2"})
	workflow.SetFinalizers([]string{WorkflowFinalizer})
	yes := true
	workflow.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: api.GroupVersion.String(), Kind: "TrainingRun", Name: run.Name, UID: run.UID, Controller: &yes, BlockOwnerDeletion: &yes}})
	return workflow, nil
}

func WorkflowName(run *api.TrainingRun) string { return "anvil-" + string(run.UID) }

func VerifyWorkflow(run *api.TrainingRun, workflow *unstructured.Unstructured) error {
	owner := metav1.GetControllerOf(workflow)
	if owner == nil || owner.UID != run.UID || owner.Name != run.Name || owner.Kind != "TrainingRun" || owner.APIVersion != api.GroupVersion.String() {
		return fmt.Errorf("workflow owner differs")
	}
	if run.Status.WorkflowRef != nil && run.Status.WorkflowRef.UID != "" && run.Status.WorkflowRef.UID != string(workflow.GetUID()) {
		return fmt.Errorf("workflow UID differs")
	}
	requestHash, err := Hash(run.Spec.Request)
	if err != nil {
		return err
	}
	if workflow.GetAnnotations()[RequestHash] != requestHash {
		return fmt.Errorf("workflow request differs")
	}
	spec, _, err := unstructured.NestedMap(workflow.Object, "spec")
	if err != nil {
		return err
	}
	// Termination is the sole workflow-spec mutation that the operator permits.
	if shutdown, exists := spec["shutdown"]; exists {
		if shutdown != "Terminate" || (!run.Spec.Cancel && run.DeletionTimestamp.IsZero()) {
			return fmt.Errorf("unexpected workflow shutdown")
		}
		delete(spec, "shutdown")
	}
	version := workflow.GetAnnotations()["anvil.dev/workflow-hash-version"]
	if version == "" {
		version = "1"
	}
	if version != "1" && version != "2" {
		return fmt.Errorf("unsupported workflow hash version")
	}
	specHash, err := workflowSpecHashVersion(spec, version)
	if err != nil {
		return err
	}
	if !shaPattern.MatchString(run.Status.WorkflowSpecSHA256) || run.Status.WorkflowSpecSHA256 != workflow.GetAnnotations()[WorkflowHash] || run.Status.WorkflowSpecSHA256 != specHash {
		return fmt.Errorf("workflow execution spec differs; check Argo workflowDefaults and mutating admission policies")
	}
	if run.Status.RecipeUID != "" && workflow.GetAnnotations()["anvil.dev/recipe-uid"] != run.Status.RecipeUID {
		return fmt.Errorf("workflow recipe UID differs")
	}
	return nil
}
