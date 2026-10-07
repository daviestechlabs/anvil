package controller

import (
	"context"
	"fmt"
	"reflect"
	"time"

	api "anvil.dev/operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func apiVersion(kind string) schema.GroupVersionKind {
	return schema.GroupVersionKind{Group: "argoproj.io", Version: "v1alpha1", Kind: kind}
}

type Reconciler struct {
	client.Client
	Reader   client.Reader
	Recorder record.EventRecorder
}

func (r *Reconciler) SetupWithManager(manager ctrl.Manager) error {
	workflow := &unstructured.Unstructured{}
	workflow.SetGroupVersionKind(WorkflowGVK)
	return ctrl.NewControllerManagedBy(manager).For(&api.TrainingRun{}).Owns(workflow).Complete(r)
}

func terminal(phase string) bool {
	return phase == "Succeeded" || phase == "Failed" || phase == "Cancelled"
}

func (r *Reconciler) status(ctx context.Context, run *api.TrainingRun, phase, reason, message string, accepted bool) (ctrl.Result, error) {
	original := run.DeepCopy()
	run.Status.Phase = phase
	run.Status.ObservedGeneration = run.Generation
	conditionStatus := metav1.ConditionFalse
	if accepted {
		conditionStatus = metav1.ConditionTrue
	}
	meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{Type: "Accepted", Status: conditionStatus, Reason: reason, Message: message, ObservedGeneration: run.Generation})
	capacity := metav1.ConditionTrue
	if reason == "CapacityWaiting" {
		capacity = metav1.ConditionFalse
	}
	meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{Type: "CapacityAvailable", Status: capacity, Reason: reason, Message: message, ObservedGeneration: run.Generation})
	cancellation := metav1.ConditionFalse
	if phase == "Cancelling" || run.Spec.Cancel {
		cancellation = metav1.ConditionTrue
	}
	meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{Type: "CancellationRequested", Status: cancellation, Reason: reason, Message: message, ObservedGeneration: run.Generation})
	if terminal(phase) && run.Status.FinishedAt == nil {
		now := metav1.Now()
		run.Status.FinishedAt = &now
	}
	if !reflect.DeepEqual(original.Status, run.Status) {
		if err := r.Status().Patch(ctx, run, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
			return ctrl.Result{}, err
		}
		if r.Recorder != nil && (original.Status.Phase != phase || !reflect.DeepEqual(original.Status.Conditions, run.Status.Conditions)) {
			r.Recorder.Event(run, corev1.EventTypeNormal, reason, message)
		}
	}
	if terminal(phase) {
		// Completion must be durable before releasing the Workflow observation finalizer.
		return ctrl.Result{Requeue: true}, nil
	}
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

func (r *Reconciler) workersStopped(ctx context.Context, run *api.TrainingRun) (bool, error) {
	var pods corev1.PodList
	if err := r.Reader.List(ctx, &pods, client.InNamespace(run.Namespace), client.MatchingLabels{"workflows.argoproj.io/workflow": WorkflowName(run)}); err != nil {
		return false, err
	}
	for _, pod := range pods.Items {
		// Even a terminating Pod retains capacity until its execution reaches a terminal phase.
		if pod.Status.Phase != corev1.PodSucceeded && pod.Status.Phase != corev1.PodFailed {
			return false, nil
		}
	}
	return true, nil
}

func (r *Reconciler) finishDeletion(ctx context.Context, run *api.TrainingRun) (ctrl.Result, error) {
	if released, err := r.releaseWorkflow(ctx, run); err != nil || !released {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, err
	}
	original := run.DeepCopy()
	controllerutil.RemoveFinalizer(run, Finalizer)
	return ctrl.Result{}, r.Patch(ctx, run, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{}))
}

// releaseWorkflow permits Argo TTL or Kubernetes garbage collection only after worker exit.
// Call it after durable terminal status, or while finishing a verified deletion.
func (r *Reconciler) releaseWorkflow(ctx context.Context, run *api.TrainingRun) (bool, error) {
	if run.Status.WorkflowRef == nil || run.Status.WorkflowRef.UID == "" {
		return true, nil
	}
	workflow := &unstructured.Unstructured{}
	workflow.SetGroupVersionKind(WorkflowGVK)
	err := r.Reader.Get(ctx, types.NamespacedName{Namespace: run.Namespace, Name: WorkflowName(run)}, workflow)
	if apierrors.IsNotFound(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if !controllerutil.ContainsFinalizer(workflow, WorkflowFinalizer) {
		return true, nil
	}
	if err := VerifyWorkflow(run, workflow); err != nil {
		return false, err
	}
	stopped, err := r.workersStopped(ctx, run)
	if err != nil || !stopped {
		return false, err
	}
	original := workflow.DeepCopy()
	controllerutil.RemoveFinalizer(workflow, WorkflowFinalizer)
	if err := r.Patch(ctx, workflow, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Reconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	var run api.TrainingRun
	if err := r.Reader.Get(ctx, request.NamespacedName, &run); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if terminal(run.Status.Phase) && run.DeletionTimestamp.IsZero() {
		if released, err := r.releaseWorkflow(ctx, &run); err != nil || !released {
			return ctrl.Result{RequeueAfter: 5 * time.Second}, err
		}
		if run.Status.ObservedGeneration != run.Generation {
			return r.status(ctx, &run, run.Status.Phase, "AlreadyComplete", "Execution is already terminal", meta.IsStatusConditionTrue(run.Status.Conditions, "Accepted"))
		}
		return ctrl.Result{}, nil
	}
	if !controllerutil.ContainsFinalizer(&run, Finalizer) {
		if !run.DeletionTimestamp.IsZero() {
			return ctrl.Result{}, nil
		}
		original := run.DeepCopy()
		controllerutil.AddFinalizer(&run, Finalizer)
		return ctrl.Result{Requeue: true}, r.Patch(ctx, &run, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{}))
	}
	workflow := &unstructured.Unstructured{}
	workflow.SetGroupVersionKind(WorkflowGVK)
	err := r.Reader.Get(ctx, types.NamespacedName{Namespace: run.Namespace, Name: WorkflowName(&run)}, workflow)
	if err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	cancelling := run.Spec.Cancel || !run.DeletionTimestamp.IsZero()
	if apierrors.IsNotFound(err) {
		stopped, err := r.workersStopped(ctx, &run)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !stopped {
			return r.status(ctx, &run, "Cancelling", "WorkersRemain", "Waiting for workers of the missing Workflow to exit", true)
		}
		if cancelling {
			if !run.DeletionTimestamp.IsZero() {
				return r.finishDeletion(ctx, &run)
			}
			return r.status(ctx, &run, "Cancelled", "Cancelled", "No workflow workers remain", true)
		}
		if run.Status.WorkflowRef != nil && run.Status.WorkflowRef.UID != "" {
			return r.status(ctx, &run, "Failed", "WorkflowLost", "The recorded Workflow is missing; create a new TrainingRun to retry", true)
		}
		var recipe api.TrainingRecipe
		if err := r.Reader.Get(ctx, types.NamespacedName{Namespace: run.Namespace, Name: run.Spec.Request.RecipeRef.Name}, &recipe); err != nil {
			if apierrors.IsNotFound(err) {
				return r.status(ctx, &run, "Pending", "RecipeMissing", "Waiting for the named TrainingRecipe", false)
			}
			return ctrl.Result{}, err
		}
		if !recipe.Spec.Enabled {
			return r.status(ctx, &run, "Pending", "RecipeDisabled", "The recipe is held by its administrator", false)
		}
		if run.Status.RecipeUID != "" && run.Status.RecipeUID != string(recipe.UID) {
			return r.status(ctx, &run, "Failed", "RecipeReplaced", "Recipe UID changed after admission", false)
		}
		template := &unstructured.Unstructured{}
		template.SetGroupVersionKind(TemplateGVK)
		if err := r.Reader.Get(ctx, types.NamespacedName{Namespace: run.Namespace, Name: recipe.Spec.Binding.WorkflowTemplate}, template); err != nil {
			if apierrors.IsNotFound(err) {
				return r.status(ctx, &run, "Pending", "TemplateMissing", "Waiting for the reviewed WorkflowTemplate", false)
			}
			return ctrl.Result{}, err
		}
		desired, err := BuildWorkflow(&run, &recipe, template)
		if err != nil {
			return r.status(ctx, &run, "Failed", "AdmissionRejected", err.Error(), false)
		}
		if run.Status.Phase != "Submitting" {
			run.Status.RecipeUID = string(recipe.UID)
			run.Status.WorkflowSpecSHA256 = desired.GetAnnotations()[WorkflowHash]
			run.Status.WorkflowRef = &api.WorkflowReference{Name: desired.GetName()}
			run.Status.OutputParameters = append([]string(nil), recipe.Spec.Binding.Outputs...)
			// Store admission before create. A lost response recovers this deterministic Workflow name.
			original := &api.TrainingRun{}
			if err := r.Reader.Get(ctx, request.NamespacedName, original); err != nil {
				return ctrl.Result{}, err
			}
			run.Status.Phase = "Submitting"
			if err := r.Status().Patch(ctx, &run, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}
		// Cancellation can race Workflow creation. The next reconcile terminates
		// any Workflow created during that race.
		if err := r.Create(ctx, desired); err != nil {
			if apierrors.IsAlreadyExists(err) {
				return ctrl.Result{Requeue: true}, nil
			}
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}
	if run.Status.WorkflowRef == nil || run.Status.WorkflowSpecSHA256 == "" || run.Status.RecipeUID == "" {
		return r.status(ctx, &run, "Cancelling", "WorkflowConflict", "Workflow exists without a durable admission record", false)
	}
	if err := VerifyWorkflow(&run, workflow); err != nil {
		// Keep the finalizer and retain nonterminal status while ownership or execution integrity is uncertain.
		_, statusErr := r.status(ctx, &run, "Cancelling", "WorkflowConflict", err.Error(), false)
		if statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{}, err
	}
	// Save identity before observing output or mutating termination state.
	if run.Status.WorkflowRef == nil || run.Status.WorkflowRef.UID == "" {
		original := run.DeepCopy()
		run.Status.WorkflowRef = &api.WorkflowReference{Name: workflow.GetName(), UID: string(workflow.GetUID())}
		run.Status.RecipeUID = workflow.GetAnnotations()["anvil.dev/recipe-uid"]
		if err := r.Status().Patch(ctx, &run, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}
	phase, _, _ := unstructured.NestedString(workflow.Object, "status", "phase")
	progress := workflowProgress(workflow)
	if !reflect.DeepEqual(run.Status.Progress, progress) {
		original := run.DeepCopy()
		run.Status.Progress = progress
		if err := r.Status().Patch(ctx, &run, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
			return ctrl.Result{}, err
		}
		// Continue lifecycle handling even if an older CRD prunes optional progress.
	}
	completed := phase == "Succeeded" || phase == "Failed" || phase == "Error"
	if cancelling && !completed {
		original := workflow.DeepCopy()
		if err := unstructured.SetNestedField(workflow.Object, "Terminate", "spec", "shutdown"); err != nil {
			return ctrl.Result{}, err
		}
		if !reflect.DeepEqual(original.Object, workflow.Object) {
			if err := r.Patch(ctx, workflow, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
				return ctrl.Result{}, err
			}
		}
		return r.status(ctx, &run, "Cancelling", "TerminationRequested", "Waiting for Argo termination and worker exit", true)
	}
	if completed {
		stopped, err := r.workersStopped(ctx, &run)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !stopped {
			waitingPhase := "Running"
			if cancelling {
				waitingPhase = "Cancelling"
			}
			return r.status(ctx, &run, waitingPhase, "WorkersRemain", "Argo is terminal; waiting for worker exit", true)
		}
		if !run.DeletionTimestamp.IsZero() {
			return r.finishDeletion(ctx, &run)
		}
		if cancelling {
			return r.status(ctx, &run, "Cancelled", "Cancelled", "Argo terminated and all workers stopped", true)
		}
		if phase != "Succeeded" {
			return r.status(ctx, &run, "Failed", "WorkflowFailed", "Argo reported "+phase, true)
		}
		outputs, _, err := unstructured.NestedSlice(workflow.Object, "status", "outputs", "parameters")
		if err != nil {
			return ctrl.Result{}, err
		}
		values := map[string]string{}
		for _, item := range outputs {
			output, ok := item.(map[string]any)
			if !ok {
				continue
			}
			key, _ := output["name"].(string)
			value, ok := output["value"].(string)
			if ok {
				values[key] = value
			}
		}
		verified := map[string]string{}
		for _, key := range run.Status.OutputParameters {
			value, exists := values[key]
			if !exists || len(value) > 8000 {
				return r.status(ctx, &run, "Failed", "OutputRejected", fmt.Sprintf("Required output %q is missing or exceeds 8000 bytes", key), true)
			}
			verified[key] = value
		}
		// Persist outputs together with completion, through the same status patch.
		original := run.DeepCopy()
		run.Status.Outputs = verified
		run.Status.Phase = "Succeeded"
		run.Status.ObservedGeneration = run.Generation
		now := metav1.Now()
		run.Status.FinishedAt = &now
		meta.SetStatusCondition(&run.Status.Conditions, metav1.Condition{Type: "Accepted", Status: metav1.ConditionTrue, Reason: "WorkflowSucceeded", Message: "Execution completed; outputs remain unpromoted", ObservedGeneration: run.Generation})
		return ctrl.Result{Requeue: true}, r.Status().Patch(ctx, &run, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{}))
	}
	if run.Status.StartedAt == nil {
		original := run.DeepCopy()
		now := metav1.Now()
		run.Status.StartedAt = &now
		if err := r.Status().Patch(ctx, &run, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
			return ctrl.Result{}, err
		}
	}
	waiting, _, _ := unstructured.NestedSlice(workflow.Object, "status", "synchronization", "mutex", "waiting")
	if len(waiting) > 0 {
		return r.status(ctx, &run, "Running", "CapacityWaiting", "Waiting for the reviewed Argo capacity lock", true)
	}
	var pods corev1.PodList
	if err := r.Reader.List(ctx, &pods, client.InNamespace(run.Namespace), client.MatchingLabels{"workflows.argoproj.io/workflow": WorkflowName(&run)}); err != nil {
		return ctrl.Result{}, err
	}
	for _, pod := range pods.Items {
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionFalse && condition.Reason == "Unschedulable" {
				return r.status(ctx, &run, "Running", "CapacityWaiting", "Worker cannot obtain its requested scheduling capacity", true)
			}
		}
	}
	if suspended, _, _ := unstructured.NestedBool(workflow.Object, "spec", "suspend"); suspended {
		return r.status(ctx, &run, "Running", "WorkflowSuspended", "The reviewed Workflow is suspended", true)
	}
	if phase == "" || phase == "Pending" {
		return r.status(ctx, &run, "Running", "WorkflowPending", "Waiting for Argo to start the reviewed Workflow", true)
	}
	return r.status(ctx, &run, "Running", "WorkflowActive", "Argo phase: "+phase, true)
}
