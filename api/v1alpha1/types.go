// Package v1alpha1 defines Anvil's namespaced training API.
package v1alpha1

import (
	"encoding/json"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const Group = "anvil.dev"

var GroupVersion = schema.GroupVersion{Group: Group, Version: "v1alpha1"}
var schemeBuilder = runtime.NewSchemeBuilder(func(s *runtime.Scheme) error {
	s.AddKnownTypes(GroupVersion, &TrainingRecipe{}, &TrainingRecipeList{}, &TrainingRun{}, &TrainingRunList{})
	s.AddKnownTypes(GroupVersion, &Anvil{}, &AnvilList{}, &TrainingArtifact{}, &TrainingArtifactList{}, &EvaluationRun{}, &EvaluationRunList{}, &ModelPromotion{}, &ModelPromotionList{})
	metav1.AddToGroupVersion(s, GroupVersion)
	return nil
})

var AddToScheme = schemeBuilder.AddToScheme

type Parameter struct {
	Required  bool     `json:"required,omitempty"`
	Default   *string  `json:"default,omitempty"`
	Choices   []string `json:"choices,omitempty"`
	Pattern   string   `json:"pattern,omitempty"`
	MaxLength int      `json:"maxLength"`
}

type RecipeBinding struct {
	Revision           string               `json:"revision"`
	WorkflowTemplate   string               `json:"workflowTemplate"`
	TemplateSpecSHA256 string               `json:"templateSpecSHA256"`
	ServiceAccountName string               `json:"serviceAccountName"`
	ConcurrencyKey     string               `json:"concurrencyKey"`
	MaxDurationSeconds int64                `json:"maxDurationSeconds"`
	FixedParameters    map[string]string    `json:"fixedParameters,omitempty"`
	Parameters         map[string]Parameter `json:"parameters,omitempty"`
	Outputs            []string             `json:"outputs,omitempty"`
}

type TrainingRecipeSpec struct {
	Enabled bool          `json:"enabled"`
	Binding RecipeBinding `json:"binding"`
}

type TrainingRecipe struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              TrainingRecipeSpec `json:"spec"`
}

type RecipeReference struct {
	Name          string `json:"name"`
	BindingSHA256 string `json:"bindingSHA256"`
}

type TrainingRequest struct {
	RecipeRef       RecipeReference   `json:"recipeRef"`
	Parameters      map[string]string `json:"parameters,omitempty"`
	DurationSeconds int64             `json:"durationSeconds"`
}

type TrainingRunSpec struct {
	Request TrainingRequest `json:"request"`
	Cancel  bool            `json:"cancel"`
}

type WorkflowReference struct {
	Name string `json:"name"`
	UID  string `json:"uid,omitempty"`
}

type WorkflowProgress struct {
	Completed int64 `json:"completed"`
	Total     int64 `json:"total"`
}

type TrainingRunStatus struct {
	Phase              string             `json:"phase,omitempty"`
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	WorkflowSpecSHA256 string             `json:"workflowSpecSHA256,omitempty"`
	RecipeUID          string             `json:"recipeUID,omitempty"`
	WorkflowRef        *WorkflowReference `json:"workflowRef,omitempty"`
	Progress           *WorkflowProgress  `json:"progress,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
	OutputParameters   []string           `json:"outputParameters,omitempty"`
	Outputs            map[string]string  `json:"outputs,omitempty"`
	StartedAt          *metav1.Time       `json:"startedAt,omitempty"`
	FinishedAt         *metav1.Time       `json:"finishedAt,omitempty"`
}

type TrainingRun struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              TrainingRunSpec   `json:"spec"`
	Status            TrainingRunStatus `json:"status,omitempty"`
}

type TrainingRecipeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TrainingRecipe `json:"items"`
}

type TrainingRunList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TrainingRun `json:"items"`
}

// These API objects contain JSON data only. Keep copies independent, including maps and optional fields.
func copyJSON[T any](in *T) *T {
	if in == nil {
		return nil
	}
	out := new(T)
	b, err := json.Marshal(in)
	if err != nil {
		panic(err)
	}
	if err := json.Unmarshal(b, out); err != nil {
		panic(err)
	}
	return out
}
func (r *TrainingRecipe) DeepCopy() *TrainingRecipe          { return copyJSON(r) }
func (r *TrainingRecipe) DeepCopyObject() runtime.Object     { return r.DeepCopy() }
func (r *TrainingRecipeList) DeepCopyObject() runtime.Object { return copyJSON(r) }
func (r *TrainingRun) DeepCopy() *TrainingRun                { return copyJSON(r) }
func (r *TrainingRun) DeepCopyObject() runtime.Object        { return r.DeepCopy() }
func (r *TrainingRunList) DeepCopyObject() runtime.Object    { return copyJSON(r) }
