package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// ObjectReference always names an exact Kubernetes identity in the same namespace.
type ObjectReference struct {
	Name string `json:"name"`
	UID  string `json:"uid"`
}
type EvidenceExecution struct {
	RecipeRef       RecipeReference `json:"recipeRef"`
	DurationSeconds int64           `json:"durationSeconds"`
}
type ArtifactManifest struct {
	URI    string `json:"uri"`
	SHA256 string `json:"sha256"`
}
type ArtifactLineage struct {
	Request            TrainingRequest `json:"request"`
	SourceRun          ObjectReference `json:"sourceRun"`
	RecipeUID          string          `json:"recipeUID"`
	WorkflowUID        string          `json:"workflowUID"`
	WorkflowSpecSHA256 string          `json:"workflowSpecSHA256"`
	RequestSHA256      string          `json:"requestSHA256"`
}
type EvidenceStatus struct {
	Phase              string             `json:"phase,omitempty"`
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
	ExecutionRef       *ObjectReference   `json:"executionRef,omitempty"`
	Manifest           *ArtifactManifest  `json:"manifest,omitempty"`
	Lineage            *ArtifactLineage   `json:"lineage,omitempty"`
	ArtifactRef        *ObjectReference   `json:"artifactRef,omitempty"`
	EvaluationRef      *ObjectReference   `json:"evaluationRef,omitempty"`
	Report             string             `json:"report,omitempty"`
	Passed             *bool              `json:"passed,omitempty"`
	RecordedAt         *metav1.Time       `json:"recordedAt,omitempty"`
}
type TrainingArtifactSpec struct {
	OutputParameter string            `json:"outputParameter,omitempty"`
	SourceRun       ObjectReference   `json:"sourceRun"`
	Verifier        EvidenceExecution `json:"verifier"`
}
type TrainingArtifact struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              TrainingArtifactSpec `json:"spec"`
	Status            EvidenceStatus       `json:"status,omitempty"`
}
type TrainingArtifactList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TrainingArtifact `json:"items"`
}
type EvaluationRunSpec struct {
	ArtifactRef ObjectReference   `json:"artifactRef"`
	Evaluator   EvidenceExecution `json:"evaluator"`
}
type EvaluationRun struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              EvaluationRunSpec `json:"spec"`
	Status            EvidenceStatus    `json:"status,omitempty"`
}
type EvaluationRunList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EvaluationRun `json:"items"`
}

// Creating this resource requires a separate promotion approver Role. Identity belongs to the API audit log.
type ModelPromotionSpec struct {
	ArtifactRef   ObjectReference `json:"artifactRef"`
	EvaluationRef ObjectReference `json:"evaluationRef"`
	Decision      string          `json:"decision"`
	Reason        string          `json:"reason"`
}
type ModelPromotion struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ModelPromotionSpec `json:"spec"`
	Status            EvidenceStatus     `json:"status,omitempty"`
}
type ModelPromotionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ModelPromotion `json:"items"`
}

func (r *TrainingArtifact) DeepCopy() *TrainingArtifact        { return copyJSON(r) }
func (r *TrainingArtifact) DeepCopyObject() runtime.Object     { return r.DeepCopy() }
func (r *TrainingArtifactList) DeepCopyObject() runtime.Object { return copyJSON(r) }
func (r *EvaluationRun) DeepCopy() *EvaluationRun              { return copyJSON(r) }
func (r *EvaluationRun) DeepCopyObject() runtime.Object        { return r.DeepCopy() }
func (r *EvaluationRunList) DeepCopyObject() runtime.Object    { return copyJSON(r) }
func (r *ModelPromotion) DeepCopy() *ModelPromotion            { return copyJSON(r) }
func (r *ModelPromotion) DeepCopyObject() runtime.Object       { return r.DeepCopy() }
func (r *ModelPromotionList) DeepCopyObject() runtime.Object   { return copyJSON(r) }
