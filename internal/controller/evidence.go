package controller

import (
	api "anvil.dev/operator/api/v1alpha1"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"time"
)

type EvidenceReconciler struct {
	client.Client
	Reader client.Reader
	Kind   string
}

const EvidenceExecutionFinalizer = "anvil.dev/evidence-observation"

func (r *EvidenceReconciler) object() client.Object {
	switch r.Kind {
	case "TrainingArtifact":
		return &api.TrainingArtifact{}
	case "EvaluationRun":
		return &api.EvaluationRun{}
	default:
		return &api.ModelPromotion{}
	}
}
func (r *EvidenceReconciler) SetupWithManager(manager ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(manager).For(r.object()).Owns(&api.TrainingRun{}).Complete(r)
}
func evidenceStatus(object client.Object) *api.EvidenceStatus {
	switch value := object.(type) {
	case *api.TrainingArtifact:
		return &value.Status
	case *api.EvaluationRun:
		return &value.Status
	default:
		return &object.(*api.ModelPromotion).Status
	}
}

type integrityError struct{ error }

func reject(format string, args ...any) error { return integrityError{fmt.Errorf(format, args...)} }

func strictJSON(value string, target any) error {
	decoder := json.NewDecoder(bytes.NewBufferString(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return reject("unexpected JSON suffix")
	}
	return nil
}
func completedEvidence(status api.EvidenceStatus, generation int64, phase string) bool {
	return status.Phase == phase && status.ObservedGeneration == generation && meta.IsStatusConditionTrue(status.Conditions, "Ready")
}
func sourceSucceeded(run *api.TrainingRun) bool {
	return run.Status.Phase == "Succeeded" && run.Status.ObservedGeneration == run.Generation && meta.IsStatusConditionTrue(run.Status.Conditions, "Accepted") && run.DeletionTimestamp.IsZero()
}
func (r *EvidenceReconciler) exact(ctx context.Context, namespace string, reference api.ObjectReference, object client.Object) error {
	if reference.Name == "" || reference.UID == "" {
		return reject("exact name and UID are required")
	}
	if err := r.Reader.Get(ctx, types.NamespacedName{Namespace: namespace, Name: reference.Name}, object); err != nil {
		return err
	}
	if string(object.GetUID()) != reference.UID || !object.GetDeletionTimestamp().IsZero() {
		return reject("referenced resource identity changed or is deleting")
	}
	return nil
}
func (r *EvidenceReconciler) execution(ctx context.Context, owner client.Object, execution api.EvidenceExecution, parameters map[string]string, purpose string) (*api.TrainingRun, error) {
	var recipe api.TrainingRecipe
	if err := r.Reader.Get(ctx, types.NamespacedName{Namespace: owner.GetNamespace(), Name: execution.RecipeRef.Name}, &recipe); err != nil {
		return nil, err
	}
	hash, err := Hash(recipe.Spec.Binding)
	if err != nil {
		return nil, err
	}
	if hash != execution.RecipeRef.BindingSHA256 || recipe.Annotations["anvil.dev/purpose"] != purpose || !recipe.Spec.Enabled {
		return nil, reject("evidence recipe must be enabled and reviewed for %s", purpose)
	}
	if execution.DurationSeconds < 1 || execution.DurationSeconds > recipe.Spec.Binding.MaxDurationSeconds {
		return nil, reject("evidence duration exceeds reviewed limit")
	}
	if _, err := Parameters(recipe.Spec.Binding, parameters); err != nil {
		return nil, reject("evidence inputs rejected: %v", err)
	}
	desired := &api.TrainingRun{ObjectMeta: metav1.ObjectMeta{Name: "evidence-" + string(owner.GetUID()), Namespace: owner.GetNamespace()}, Spec: api.TrainingRunSpec{Request: api.TrainingRequest{RecipeRef: execution.RecipeRef, DurationSeconds: execution.DurationSeconds, Parameters: parameters}}}
	desired.APIVersion = api.GroupVersion.String()
	desired.Kind = "TrainingRun"
	desired.Finalizers = []string{EvidenceExecutionFinalizer}
	yes := true
	desired.OwnerReferences = []metav1.OwnerReference{{APIVersion: api.GroupVersion.String(), Kind: r.Kind, Name: owner.GetName(), UID: owner.GetUID(), Controller: &yes, BlockOwnerDeletion: &yes}}
	var run api.TrainingRun
	status := evidenceStatus(owner)
	err = r.Reader.Get(ctx, client.ObjectKeyFromObject(desired), &run)
	if apierrors.IsNotFound(err) {
		if status.ExecutionRef != nil {
			return nil, reject("recorded evidence execution is missing; create a new evidence request")
		}
		if err = r.Create(ctx, desired); err != nil && !apierrors.IsAlreadyExists(err) {
			return nil, err
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	parent := metav1.GetControllerOf(&run)
	if parent == nil || parent.UID != owner.GetUID() || parent.Kind != r.Kind || parent.Name != owner.GetName() || parent.APIVersion != api.GroupVersion.String() || !reflect.DeepEqual(run.Spec.Request, desired.Spec.Request) {
		return nil, reject("evidence execution ownership or request differs")
	}
	if status.ExecutionRef != nil && status.ExecutionRef.UID != string(run.UID) {
		return nil, reject("evidence execution UID changed")
	}
	if status.ExecutionRef == nil {
		status.ExecutionRef = &api.ObjectReference{Name: run.Name, UID: string(run.UID)}
		// Persist identity before releasing the create-response guard or consuming evidence.
		return nil, nil
	}
	if controllerutil.ContainsFinalizer(&run, EvidenceExecutionFinalizer) {
		base := run.DeepCopy()
		controllerutil.RemoveFinalizer(&run, EvidenceExecutionFinalizer)
		if err := r.Patch(ctx, &run, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
			return nil, err
		}
	}
	if run.Spec.Cancel || !run.DeletionTimestamp.IsZero() {
		return nil, reject("evidence execution is cancelled or deleting")
	}
	return &run, nil
}
func (r *EvidenceReconciler) artifact(ctx context.Context, artifact *api.TrainingArtifact) (string, error) {
	var source api.TrainingRun
	if err := r.exact(ctx, artifact.Namespace, artifact.Spec.SourceRun, &source); err != nil {
		return "", err
	}
	if !sourceSucceeded(&source) {
		if terminal(source.Status.Phase) {
			return "", reject("source training did not succeed")
		}
		return "Pending", nil
	}
	if source.Status.WorkflowRef == nil || source.Status.WorkflowRef.UID == "" || !shaPattern.MatchString(source.Status.WorkflowSpecSHA256) || source.Status.RecipeUID == "" {
		return "", reject("source execution lineage is incomplete")
	}
	var manifest api.ArtifactManifest
	output := artifact.Spec.OutputParameter
	if output == "" {
		output = "artifact-manifest"
	}
	if output != "artifact-manifest" && output != "checkpoint-manifest" {
		return "", reject("unsupported artifact output")
	}
	if err := strictJSON(source.Status.Outputs[output], &manifest); err != nil {
		return "", reject("invalid artifact manifest: %w", err)
	}
	if !shaPattern.MatchString(manifest.SHA256) || manifest.URI == "" || len(manifest.URI) > 2048 {
		return "", reject("invalid artifact content reference")
	}
	run, err := r.execution(ctx, artifact, artifact.Spec.Verifier, map[string]string{"artifact-uri": manifest.URI, "artifact-sha256": manifest.SHA256}, "artifact-verification")
	if err != nil {
		return "", err
	}
	if run == nil || !terminal(run.Status.Phase) {
		return "Verifying", nil
	}
	if !sourceSucceeded(run) {
		return "", reject("artifact verifier did not succeed")
	}
	var verified api.ArtifactManifest
	if err := strictJSON(run.Status.Outputs["verified-artifact"], &verified); err != nil || verified != manifest {
		return "", reject("verifier did not attest the exact artifact bytes")
	}
	requestHash, err := Hash(source.Spec.Request)
	if err != nil {
		return "", err
	}
	artifact.Status.Manifest = &manifest
	artifact.Status.Lineage = &api.ArtifactLineage{Request: source.Spec.Request, SourceRun: artifact.Spec.SourceRun, RecipeUID: source.Status.RecipeUID, WorkflowUID: source.Status.WorkflowRef.UID, WorkflowSpecSHA256: source.Status.WorkflowSpecSHA256, RequestSHA256: requestHash}
	return "Verified", nil
}

type EvaluationReport struct {
	ArtifactSHA256 string             `json:"artifactSHA256"`
	Passed         *bool              `json:"passed"`
	Metrics        map[string]float64 `json:"metrics"`
}

func (r *EvidenceReconciler) evaluation(ctx context.Context, evaluation *api.EvaluationRun) (string, error) {
	var artifact api.TrainingArtifact
	if err := r.exact(ctx, evaluation.Namespace, evaluation.Spec.ArtifactRef, &artifact); err != nil {
		return "", err
	}
	if !completedEvidence(artifact.Status, artifact.Generation, "Verified") {
		if artifact.Status.Phase == "Failed" {
			return "", reject("artifact verification failed")
		}
		return "Pending", nil
	}
	if artifact.Status.Manifest == nil {
		return "", reject("verified artifact has no manifest")
	}
	manifest := artifact.Status.Manifest
	run, err := r.execution(ctx, evaluation, evaluation.Spec.Evaluator, map[string]string{"artifact-uri": manifest.URI, "artifact-sha256": manifest.SHA256}, "artifact-evaluation")
	if err != nil {
		return "", err
	}
	if run == nil || !terminal(run.Status.Phase) {
		return "Evaluating", nil
	}
	if !sourceSucceeded(run) {
		return "", reject("evaluator did not succeed")
	}
	report := run.Status.Outputs["evaluation-report"]
	var parsed EvaluationReport
	if err := strictJSON(report, &parsed); err != nil || parsed.ArtifactSHA256 != manifest.SHA256 || parsed.Passed == nil || len(parsed.Metrics) == 0 {
		return "", reject("evaluation report lacks exact artifact evidence")
	}
	evaluation.Status.ArtifactRef = &evaluation.Spec.ArtifactRef
	evaluation.Status.Manifest = manifest
	evaluation.Status.Report = report
	evaluation.Status.Passed = parsed.Passed
	return "Completed", nil
}
func (r *EvidenceReconciler) promotion(ctx context.Context, promotion *api.ModelPromotion) (string, error) {
	if promotion.Spec.Decision == "Rejected" {
		return "Rejected", nil
	}
	if promotion.Spec.Decision != "Approved" || promotion.Spec.Reason == "" {
		return "", reject("explicit decision and reason are required")
	}
	var artifact api.TrainingArtifact
	var evaluation api.EvaluationRun
	if err := r.exact(ctx, promotion.Namespace, promotion.Spec.ArtifactRef, &artifact); err != nil {
		return "", err
	}
	if err := r.exact(ctx, promotion.Namespace, promotion.Spec.EvaluationRef, &evaluation); err != nil {
		return "", err
	}
	if !completedEvidence(artifact.Status, artifact.Generation, "Verified") || !completedEvidence(evaluation.Status, evaluation.Generation, "Completed") || evaluation.Status.Passed == nil || !*evaluation.Status.Passed || artifact.Status.Manifest == nil || evaluation.Status.Manifest == nil || *artifact.Status.Manifest != *evaluation.Status.Manifest || evaluation.Spec.ArtifactRef != promotion.Spec.ArtifactRef || evaluation.Status.ArtifactRef == nil || *evaluation.Status.ArtifactRef != promotion.Spec.ArtifactRef {
		return "", reject("promotion requires passing evaluation of this exact verified artifact")
	}
	promotion.Status.Manifest = artifact.Status.Manifest
	promotion.Status.ArtifactRef = &promotion.Spec.ArtifactRef
	promotion.Status.EvaluationRef = &promotion.Spec.EvaluationRef
	promotion.Status.Report = evaluation.Status.Report
	return "Approved", nil
}
func (r *EvidenceReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	object := r.object()
	if err := r.Reader.Get(ctx, request.NamespacedName, object); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	// Recover create responses before reading dependencies. Source deletion cannot strand this guard.
	if r.Kind != "ModelPromotion" {
		var child api.TrainingRun
		err := r.Reader.Get(ctx, types.NamespacedName{Namespace: object.GetNamespace(), Name: "evidence-" + string(object.GetUID())}, &child)
		if err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		if err == nil && controllerutil.ContainsFinalizer(&child, EvidenceExecutionFinalizer) {
			parent := metav1.GetControllerOf(&child)
			if parent == nil || parent.UID != object.GetUID() || parent.Name != object.GetName() || parent.Kind != r.Kind || parent.APIVersion != api.GroupVersion.String() {
				return ctrl.Result{}, fmt.Errorf("evidence guard ownership differs")
			}
			status := evidenceStatus(object)
			if status.ExecutionRef == nil && object.GetDeletionTimestamp().IsZero() {
				base := object.DeepCopyObject().(client.Object)
				status.ExecutionRef = &api.ObjectReference{Name: child.Name, UID: string(child.UID)}
				return ctrl.Result{Requeue: true}, r.Status().Patch(ctx, object, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
			}
			if status.ExecutionRef != nil && status.ExecutionRef.UID != string(child.UID) {
				return ctrl.Result{}, fmt.Errorf("evidence guard UID differs")
			}
			base := child.DeepCopy()
			controllerutil.RemoveFinalizer(&child, EvidenceExecutionFinalizer)
			if err := r.Patch(ctx, &child, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
				return ctrl.Result{}, err
			}
		}
	}
	if !object.GetDeletionTimestamp().IsZero() {
		return ctrl.Result{}, nil
	}
	status := evidenceStatus(object)
	if status.Phase == "Verified" || status.Phase == "Completed" || status.Phase == "Approved" || status.Phase == "Rejected" || status.Phase == "Failed" {
		return ctrl.Result{}, nil
	}
	original := object.DeepCopyObject().(client.Object)
	var phase string
	var err error
	switch value := object.(type) {
	case *api.TrainingArtifact:
		phase, err = r.artifact(ctx, value)
	case *api.EvaluationRun:
		phase, err = r.evaluation(ctx, value)
	case *api.ModelPromotion:
		phase, err = r.promotion(ctx, value)
	}
	reason, message, ready := "Progressing", "Waiting for execution evidence", metav1.ConditionFalse
	if err != nil {
		// Transport and authorization errors remain retryable; integrity failures fail closed.
		var integrity integrityError
		if !errors.As(err, &integrity) {
			return ctrl.Result{RequeueAfter: 5 * time.Second}, err
		}
		phase = "Failed"
		reason = "EvidenceRejected"
		message = err.Error()
	} else if phase == "Verified" || phase == "Completed" || phase == "Approved" || phase == "Rejected" {
		ready = metav1.ConditionTrue
		reason = "EvidenceRecorded"
		message = "Evidence recorded; serving activation remains separate"
		now := metav1.Now()
		status.RecordedAt = &now
	}
	status.Phase = phase
	status.ObservedGeneration = object.GetGeneration()
	meta.SetStatusCondition(&status.Conditions, metav1.Condition{Type: "Ready", Status: ready, Reason: reason, Message: message, ObservedGeneration: object.GetGeneration()})
	if !reflect.DeepEqual(evidenceStatus(original), status) {
		if err := r.Status().Patch(ctx, object, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
			return ctrl.Result{}, err
		}
	}
	if ready == metav1.ConditionTrue || phase == "Failed" {
		return ctrl.Result{}, nil
	}
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}
