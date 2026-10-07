package controller

import (
	api "anvil.dev/operator/api/v1alpha1"
	"context"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"strings"
	"testing"
)

func evidenceFixture(t *testing.T) (client.Client, *api.TrainingArtifact, *api.TrainingRecipe) {
	t.Helper()
	scheme := runtime.NewScheme()
	_ = api.AddToScheme(scheme)
	source := &api.TrainingRun{ObjectMeta: metav1.ObjectMeta{Name: "source", Namespace: "training", UID: "source-uid", Generation: 1}, Status: api.TrainingRunStatus{Phase: "Succeeded", ObservedGeneration: 1, RecipeUID: "recipe-uid", WorkflowRef: &api.WorkflowReference{Name: "workflow", UID: "workflow-uid"}, WorkflowSpecSHA256: strings.Repeat("a", 64), Outputs: map[string]string{"artifact-manifest": `{"uri":"pvc://store/models/model.json","sha256":"` + strings.Repeat("b", 64) + `"}`}, Conditions: []metav1.Condition{{Type: "Accepted", Status: metav1.ConditionTrue}}}}
	recipe := &api.TrainingRecipe{ObjectMeta: metav1.ObjectMeta{Name: "verify", Namespace: "training", Annotations: map[string]string{"anvil.dev/purpose": "artifact-verification"}}, Spec: api.TrainingRecipeSpec{Enabled: true, Binding: api.RecipeBinding{MaxDurationSeconds: 30, Parameters: map[string]api.Parameter{"artifact-uri": {Required: true, MaxLength: 2048}, "artifact-sha256": {Required: true, MaxLength: 64}}}}}
	hash, _ := Hash(recipe.Spec.Binding)
	artifact := &api.TrainingArtifact{ObjectMeta: metav1.ObjectMeta{Name: "artifact", Namespace: "training", UID: "artifact-uid", Generation: 1}, Spec: api.TrainingArtifactSpec{SourceRun: api.ObjectReference{Name: source.Name, UID: string(source.UID)}, Verifier: api.EvidenceExecution{RecipeRef: api.RecipeReference{Name: recipe.Name, BindingSHA256: hash}, DurationSeconds: 30}}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&api.TrainingRun{}, &api.TrainingArtifact{}, &api.EvaluationRun{}, &api.ModelPromotion{}).WithObjects(source, recipe, artifact).Build()
	return c, artifact, recipe
}
func evidenceStep(t *testing.T, c client.Client, object client.Object, kind string) {
	t.Helper()
	r := &EvidenceReconciler{Client: c, Reader: c, Kind: kind}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(object)}); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(object), object); err != nil {
		t.Fatal(err)
	}
}
func finishEvidence(t *testing.T, c client.Client, owner client.Object, output, value string) {
	t.Helper()
	ctx := context.Background()
	var run api.TrainingRun
	if err := c.Get(ctx, types.NamespacedName{Namespace: owner.GetNamespace(), Name: "evidence-" + string(owner.GetUID())}, &run); err != nil {
		t.Fatal(err)
	}
	run.UID = types.UID("execution-" + string(owner.GetUID()))
	if err := c.Update(ctx, &run); err != nil {
		t.Fatal(err)
	}
	run.Status = api.TrainingRunStatus{Phase: "Succeeded", ObservedGeneration: run.Generation, Outputs: map[string]string{output: value}, Conditions: []metav1.Condition{{Type: "Accepted", Status: metav1.ConditionTrue}}}
	if err := c.Status().Update(ctx, &run); err != nil {
		t.Fatal(err)
	}
}
func verifyArtifact(t *testing.T, c client.Client, a *api.TrainingArtifact) {
	t.Helper()
	evidenceStep(t, c, a, "TrainingArtifact")
	finishEvidence(t, c, a, "verified-artifact", `{"uri":"pvc://store/models/model.json","sha256":"`+strings.Repeat("b", 64)+`"}`)
	for i := 0; i < 3; i++ {
		evidenceStep(t, c, a, "TrainingArtifact")
	}
	if a.Status.Phase != "Verified" {
		t.Fatalf("not verified: %+v", a.Status)
	}
}
func TestEvidenceChainRequiresExactBytesAndSeparateDecision(t *testing.T) {
	c, a, recipe := evidenceFixture(t)
	verifyArtifact(t, c, a)
	if a.Status.Lineage.SourceRun.UID != "source-uid" || a.Status.Lineage.WorkflowUID != "workflow-uid" {
		t.Fatal("lineage was lost")
	}
	evalRecipe := recipe.DeepCopy()
	evalRecipe.Name = "evaluate"
	evalRecipe.ResourceVersion = ""
	evalRecipe.Annotations = map[string]string{"anvil.dev/purpose": "artifact-evaluation"}
	if err := c.Create(context.Background(), evalRecipe); err != nil {
		t.Fatal(err)
	}
	hash, _ := Hash(evalRecipe.Spec.Binding)
	evaluation := &api.EvaluationRun{ObjectMeta: metav1.ObjectMeta{Name: "evaluation", Namespace: a.Namespace, UID: "evaluation-uid", Generation: 1}, Spec: api.EvaluationRunSpec{ArtifactRef: api.ObjectReference{Name: a.Name, UID: string(a.UID)}, Evaluator: api.EvidenceExecution{RecipeRef: api.RecipeReference{Name: "evaluate", BindingSHA256: hash}, DurationSeconds: 30}}}
	if err := c.Create(context.Background(), evaluation); err != nil {
		t.Fatal(err)
	}
	evidenceStep(t, c, evaluation, "EvaluationRun")
	finishEvidence(t, c, evaluation, "evaluation-report", `{"artifactSHA256":"`+strings.Repeat("b", 64)+`","passed":true,"metrics":{"mse":0.001}}`)
	for i := 0; i < 3; i++ {
		evidenceStep(t, c, evaluation, "EvaluationRun")
	}
	if evaluation.Status.Phase != "Completed" || !*evaluation.Status.Passed {
		t.Fatalf("evaluation not complete: %+v", evaluation.Status)
	}
	promotion := &api.ModelPromotion{ObjectMeta: metav1.ObjectMeta{Name: "promotion", Namespace: a.Namespace, UID: "promotion-uid", Generation: 1}, Spec: api.ModelPromotionSpec{ArtifactRef: evaluation.Spec.ArtifactRef, EvaluationRef: api.ObjectReference{Name: evaluation.Name, UID: string(evaluation.UID)}, Decision: "Approved", Reason: "Reviewed synthetic CPU evidence"}}
	if err := c.Create(context.Background(), promotion); err != nil {
		t.Fatal(err)
	}
	evidenceStep(t, c, promotion, "ModelPromotion")
	if promotion.Status.Phase != "Approved" || promotion.Status.Manifest.SHA256 != a.Status.Manifest.SHA256 {
		t.Fatal("promotion did not bind exact evidence")
	}
}
func TestArtifactRejectsForgedDigest(t *testing.T) {
	c, a, _ := evidenceFixture(t)
	evidenceStep(t, c, a, "TrainingArtifact")
	finishEvidence(t, c, a, "verified-artifact", `{"uri":"pvc://store/models/model.json","sha256":"`+strings.Repeat("c", 64)+`"}`)
	for i := 0; i < 3; i++ {
		evidenceStep(t, c, a, "TrainingArtifact")
	}
	if a.Status.Phase != "Failed" {
		t.Fatal("accepted changed artifact")
	}
}
func TestEvidenceCannotReplaceRecordedExecution(t *testing.T) {
	c, a, _ := evidenceFixture(t)
	evidenceStep(t, c, a, "TrainingArtifact")
	finishEvidence(t, c, a, "verified-artifact", `{}`)
	evidenceStep(t, c, a, "TrainingArtifact")
	var run api.TrainingRun
	key := types.NamespacedName{Namespace: a.Namespace, Name: a.Status.ExecutionRef.Name}
	_ = c.Get(context.Background(), key, &run)
	run.UID = "replacement"
	run.Finalizers = nil
	_ = c.Update(context.Background(), &run)
	evidenceStep(t, c, a, "TrainingArtifact")
	if a.Status.Phase != "Failed" {
		t.Fatal("adopted replacement execution")
	}
}
func TestEvidenceTransportLossRecoversGuardedExecution(t *testing.T) {
	c, a, _ := evidenceFixture(t)
	broken := &failingCreate{Client: c, after: true}
	r := &EvidenceReconciler{Client: broken, Reader: c, Kind: "TrainingArtifact"}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(a)}); err == nil {
		t.Fatal("transport failure not returned")
	}
	evidenceStep(t, c, a, "TrainingArtifact")
	evidenceStep(t, c, a, "TrainingArtifact")
	if a.Status.ExecutionRef == nil || a.Status.ExecutionRef.UID != "original-workflow" || broken.count != 1 {
		t.Fatal("did not recover original guarded execution")
	}
}

func TestPromotionRejectsWrongIdentityAndFailedQuality(t *testing.T) {
	for _, scenario := range []string{"artifact-uid", "failed-quality", "evaluation-artifact"} {
		t.Run(scenario, func(t *testing.T) {
			c, a, _ := evidenceFixture(t)
			verifyArtifact(t, c, a)
			passed := true
			artifactRef := api.ObjectReference{Name: a.Name, UID: string(a.UID)}
			evaluation := &api.EvaluationRun{ObjectMeta: metav1.ObjectMeta{Name: "evaluated", Namespace: a.Namespace, UID: "evaluated-uid", Generation: 1}, Spec: api.EvaluationRunSpec{ArtifactRef: artifactRef}, Status: api.EvidenceStatus{Phase: "Completed", ObservedGeneration: 1, Passed: &passed, Manifest: a.Status.Manifest, ArtifactRef: &artifactRef, Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue}}}}
			if scenario == "failed-quality" {
				passed = false
			}
			if scenario == "evaluation-artifact" {
				evaluation.Spec.ArtifactRef.UID = "other-artifact"
			}
			if err := c.Create(context.Background(), evaluation); err != nil {
				t.Fatal(err)
			}
			promotion := &api.ModelPromotion{ObjectMeta: metav1.ObjectMeta{Name: "decision", Namespace: a.Namespace, UID: "decision-uid", Generation: 1}, Spec: api.ModelPromotionSpec{ArtifactRef: artifactRef, EvaluationRef: api.ObjectReference{Name: evaluation.Name, UID: string(evaluation.UID)}, Decision: "Approved", Reason: "Explicit reviewed decision"}}
			if scenario == "artifact-uid" {
				promotion.Spec.ArtifactRef.UID = "replaced-artifact"
			}
			if err := c.Create(context.Background(), promotion); err != nil {
				t.Fatal(err)
			}
			evidenceStep(t, c, promotion, "ModelPromotion")
			if promotion.Status.Phase != "Failed" {
				t.Fatal("promotion accepted invalid evidence", scenario)
			}
		})
	}
}
