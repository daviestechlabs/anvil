package controller

import (
	api "anvil.dev/operator/api/v1alpha1"
	"context"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
)

// Run inside the existing real API-server suite. Argo execution remains a separate smoke gate.
func testEvidenceAPI(t *testing.T, ctx context.Context, c client.Client) {
	run, recipe, _ := fixtures(t)
	run.Name = "evidence-api-source"
	run.UID = ""
	run.ResourceVersion = ""
	run.Generation = 0
	recipe.Name = "evidence-api-verifier"
	recipe.UID = ""
	recipe.ResourceVersion = ""
	recipe.Annotations = map[string]string{"anvil.dev/purpose": "artifact-verification"}
	recipe.Spec.Binding.Parameters = map[string]api.Parameter{"artifact-uri": {Required: true, MaxLength: 2048}, "artifact-sha256": {Required: true, MaxLength: 64}}
	recipe.Spec.Binding.Outputs = []string{"verified-artifact"}
	hash, _ := Hash(recipe.Spec.Binding)
	run.Spec.Request.RecipeRef = api.RecipeReference{Name: recipe.Name, BindingSHA256: hash}
	run.Spec.Request.Parameters = map[string]string{"artifact-uri": "pvc://store/models/model.json", "artifact-sha256": strings.Repeat("b", 64)}
	if err := c.Create(ctx, recipe); err != nil {
		t.Fatal(err)
	}
	if err := c.Create(ctx, run); err != nil {
		t.Fatal(err)
	}
	run.Status = api.TrainingRunStatus{Phase: "Succeeded", ObservedGeneration: run.Generation, RecipeUID: string(recipe.UID), WorkflowRef: &api.WorkflowReference{Name: "source-workflow", UID: "source-workflow-uid"}, WorkflowSpecSHA256: strings.Repeat("a", 64), Outputs: map[string]string{"artifact-manifest": `{"uri":"pvc://store/models/model.json","sha256":"` + strings.Repeat("b", 64) + `"}`}, Conditions: []metav1.Condition{{Type: "Accepted", Status: metav1.ConditionTrue, Reason: "WorkflowSucceeded", Message: "simulated Argo completion", LastTransitionTime: metav1.Now()}}}
	if err := c.Status().Update(ctx, run); err != nil {
		t.Fatal(err)
	}
	artifact := &api.TrainingArtifact{ObjectMeta: metav1.ObjectMeta{Name: "evidence-api-artifact", Namespace: run.Namespace}, Spec: api.TrainingArtifactSpec{SourceRun: api.ObjectReference{Name: run.Name, UID: string(run.UID)}, Verifier: api.EvidenceExecution{RecipeRef: run.Spec.Request.RecipeRef, DurationSeconds: 30}}}
	if err := c.Create(ctx, artifact); err != nil {
		t.Fatal(err)
	}
	evidenceStep(t, c, artifact, "TrainingArtifact")
	var child api.TrainingRun
	if err := c.Get(ctx, types.NamespacedName{Namespace: run.Namespace, Name: "evidence-" + string(artifact.UID)}, &child); err != nil {
		t.Fatal(err)
	}
	uid := child.UID
	evidenceStep(t, c, artifact, "TrainingArtifact")
	evidenceStep(t, c, artifact, "TrainingArtifact")
	if err := c.Get(ctx, client.ObjectKeyFromObject(&child), &child); err != nil {
		t.Fatal(err)
	}
	child.Status = api.TrainingRunStatus{Phase: "Succeeded", ObservedGeneration: child.Generation, Outputs: map[string]string{"verified-artifact": run.Status.Outputs["artifact-manifest"]}, Conditions: run.Status.Conditions}
	if err := c.Status().Update(ctx, &child); err != nil {
		t.Fatal(err)
	}
	evidenceStep(t, c, artifact, "TrainingArtifact")
	if artifact.Status.Phase != "Verified" || artifact.Status.ExecutionRef.UID != string(uid) {
		t.Fatalf("API evidence not recorded: %+v", artifact.Status)
	}
	original := artifact.DeepCopy()
	artifact.Spec.SourceRun.UID = "replacement"
	if err := c.Update(ctx, artifact); !apierrors.IsInvalid(err) {
		t.Fatalf("artifact lineage mutable: %v", err)
	}
	artifact = original
	evaluation := &api.EvaluationRun{ObjectMeta: metav1.ObjectMeta{Name: "evidence-api-evaluation", Namespace: run.Namespace}, Spec: api.EvaluationRunSpec{ArtifactRef: api.ObjectReference{Name: artifact.Name, UID: string(artifact.UID)}, Evaluator: artifact.Spec.Verifier}}
	if err := c.Create(ctx, evaluation); err != nil {
		t.Fatal(err)
	}
	evaluation.Spec.ArtifactRef.UID = "replacement"
	if err := c.Update(ctx, evaluation); !apierrors.IsInvalid(err) {
		t.Fatalf("evaluation identity mutable: %v", err)
	}
	promotion := &api.ModelPromotion{ObjectMeta: metav1.ObjectMeta{Name: "evidence-api-promotion", Namespace: run.Namespace}, Spec: api.ModelPromotionSpec{ArtifactRef: api.ObjectReference{Name: artifact.Name, UID: string(artifact.UID)}, EvaluationRef: api.ObjectReference{Name: evaluation.Name, UID: string(evaluation.UID)}, Decision: "Approved", Reason: "test explicit approval"}}
	if err := c.Create(ctx, promotion); err != nil {
		t.Fatal(err)
	}
	evidenceStep(t, c, promotion, "ModelPromotion")
	if promotion.Status.Phase != "Failed" {
		t.Fatal("API accepted promotion without passing evaluation")
	}
	promotion.Spec.Decision = "Rejected"
	if err := c.Update(ctx, promotion); !apierrors.IsInvalid(err) {
		t.Fatalf("promotion decision mutable: %v", err)
	}
}
