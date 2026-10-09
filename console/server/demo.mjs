// Synthetic data only. This mode cannot submit runs or contact Kubernetes.
export const demo = {
  anvils: {
    items: [
      {
        metadata: { name: "workspace" },
        status: { conditions: [{ type: "Ready", status: "True", message: "Operator ready" }] },
      },
    ],
  },
  trainingrecipes: {
    items: [
      {
        metadata: { name: "cpu-training", uid: "demo-recipe" },
        spec: {
          enabled: true,
          binding: {
            revision: "1",
            workflowTemplate: "cpu-training",
            maxDurationSeconds: 120,
            parameters: { steps: { default: "4" } },
          },
        },
      },
    ],
  },
  trainingruns: {
    items: [
      {
        metadata: {
          name: "cpu-training-02",
          uid: "demo-run-02",
          resourceVersion: "2",
          creationTimestamp: "2026-01-01T12:04:00Z",
        },
        spec: { request: { recipeRef: { name: "cpu-training" }, durationSeconds: 120 } },
        status: {
          phase: "Running",
          progress: { completed: 2, total: 4 },
          workflowRef: { name: "cpu-training-02" },
          startedAt: "2026-01-01T12:04:00Z",
        },
      },
      {
        metadata: {
          name: "cpu-training-01",
          uid: "demo-run-01",
          resourceVersion: "1",
          creationTimestamp: "2026-01-01T12:00:00Z",
        },
        spec: { request: { recipeRef: { name: "cpu-training" }, durationSeconds: 120 } },
        status: {
          phase: "Succeeded",
          progress: { completed: 4, total: 4 },
          finishedAt: "2026-01-01T12:02:00Z",
        },
      },
    ],
  },
  trainingartifacts: {
    items: [
      {
        metadata: { name: "cpu-artifact" },
        spec: { sourceRun: { name: "cpu-training-01", uid: "demo-run-01" } },
        status: {
          phase: "Verified",
          manifest: { uri: "s3://example-artifacts/cpu/model", sha256: "0".repeat(64) },
        },
      },
    ],
  },
  evaluationruns: {
    items: [
      {
        metadata: { name: "cpu-evaluation" },
        status: { phase: "Succeeded", passed: true, report: "Synthetic CPU acceptance passed" },
      },
    ],
  },
  modelpromotions: {
    items: [
      {
        metadata: { name: "cpu-promotion" },
        spec: {
          decision: "Approved",
          reason: "Synthetic acceptance",
          artifactRef: { name: "cpu-artifact" },
        },
        status: { phase: "Approved" },
      },
    ],
  },
};
