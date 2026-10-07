# API contract

The API group is `anvil.dev`.
The current version is `v1alpha1`.
All resources use one namespace.
References contain names and exact unique identifiers (UIDs) where an execution already exists.
A reference never grants permission to read another namespace.

## Resource fields

| Resource | Request fields | Result fields |
| --- | --- | --- |
| Anvil | Image digest, database and identity references, storage, optional gateway, replicas, configuration revision | Owned deployment references, dependency conditions, observed generation |
| TrainingRecipe | Enabled flag and immutable binding | No execution status |
| TrainingRun | Immutable recipe hash, parameters and duration; monotonic cancellation | Phase, observed generation, recipe UID, Workflow UID and hash, measured progress, declared outputs, times, conditions |
| TrainingArtifact | Immutable source-run UID, verifier recipe hash and duration, optional checkpoint output selection | Verified manifest, source lineage, verifier-run UID, conditions and recording time |
| EvaluationRun | Immutable artifact UID, evaluator recipe hash and duration | Exact artifact manifest, evaluator-run UID, report, pass result, conditions and recording time |
| ModelPromotion | Immutable artifact UID, evaluation UID, explicit Approved or Rejected decision and reason | Bound evidence, approval result, conditions and recording time |

The schemas in `config/crd/` define field types, required fields, limits, and defaults.
Those files and the chart copies must match exactly.
The API rejects unknown request fields through pruning or schema validation.
Use strict CLI validation before submission to detect misspelled fields.

A recipe binding fixes the template name, revision, specification hash, worker account, duration limit, and concurrency key.
It also defines fixed parameters, permitted user parameters, and declared output names.
The canonical revision annotation is `anvil.dev/recipe-revision`.
The operator accepts the legacy `anvil.daviestechlabs.io/recipe-revision` annotation for compatibility.
Conflicting annotations fail admission.
Changing executable code or a binding requires a new recipe name.

## Conditions and phases

A condition is current only when its observed generation matches the resource generation.
A true condition reports its named check; it does not certify unrelated systems.

`Anvil.Ready` reports current owned Deployments and configured dependencies.
It does not prove database recovery or training quality.
`TrainingRun.Accepted` reports admission or execution observation.
`CapacityAvailable` becomes false while Argo or the scheduler reports a capacity wait.
`CancellationRequested` becomes true while termination proceeds.
Kubernetes events report training and installation changes.

Training phases are Pending, Submitting, Running, Cancelling, Succeeded, Failed, and Cancelled.
Pending includes held recipes and missing reviewed dependencies.
Submitting records admission before Workflow creation.
Running includes Argo execution and capacity waits.
Cancelling retains capacity until Argo terminates and all observed workers exit.
Terminal phases retain the recorded identity and declared outputs.
A completed run never executes again.

Evidence uses a Ready condition.
Artifact phases are Pending, Verifying, Verified, and Failed.
Evaluation phases are Pending, Evaluating, Completed, and Failed.
A completed evaluation can report a failed quality test without an execution failure.
Promotion phases are Approved, Rejected, and Failed.
Only a completed passing evaluation can support Approved status.

Transport and permission failures remain retryable.
Integrity failures record Failed status and require a new evidence request.
A missing or replaced recorded execution cannot produce a replacement execution.
An observation finalizer guards an evidence run until its UID becomes durable.
Deleting evidence releases that guard without bypassing training worker cleanup.

## Workflow progress

Optional `TrainingRun.status.progress` records Argo's reported completed and total work counts.
Each count is an integer between zero and one billion.
Completed work cannot exceed total work.
Missing or malformed Argo progress clears this optional field.
The controller reads progress only after verifying the original Workflow identity and specification.
Progress never changes execution completion or cancellation rules.

The [Argo progress contract](https://argo-workflows.readthedocs.io/en/release-4.1/progress/) normally counts tasks; reviewed workers can report their own work units.
The total can grow as a workflow expands.
The console shows the latest counts, percentage, condition message, and observation timestamp.
It uses an indeterminate bar when measurable progress is absent or the observed generation is stale.
A percentage does not estimate remaining time, model quality, or artifact verification.

Existing installations must apply the reviewed TrainingRun CRD update before using this optional status field.
Helm upgrades do not update that definition automatically.

## Outputs and storage

Training accepts only declared string outputs of at most 8,000 bytes each.
The controller does not download files or run arbitrary verification code itself.
A reviewed verifier recipe reads retained bytes through its bounded storage configuration.
Its `anvil.dev/purpose` annotation must be `artifact-verification`.
An artifact source must be a current successful TrainingRun with complete execution lineage.

The `artifact-manifest` output contains exactly `uri` and lowercase `sha256` fields as JSON.
Checkpoint artifacts select `checkpoint-manifest` with `spec.outputParameter`.
The verifier receives `artifact-uri` and `artifact-sha256` as bounded recipe parameters.
Its `verified-artifact` output must attest the same URI and SHA-256 hash.
Artifact status retains source-run, recipe and Workflow identities plus the input request snapshot and executable specification hashes.
The recipe binding hash fixes declared inputs and their administrator-reviewed verification code.
External input bytes still require verification in that code.
Storage owners enforce retention, access control, and protection from other writers.

Evaluator recipes require the `artifact-evaluation` purpose annotation.
They receive the same exact manifest through bounded parameters.
Their `evaluation-report` JSON contains `artifactSHA256`, a boolean `passed`, and a numeric `metrics` map.
Reviewed evaluator code defines the dataset, metrics, and pass threshold.
The operator rejects missing, mismatched, malformed, or failed execution evidence.

A promotion request records a separate human decision.
Creating it requires the promotion-approver Role.
Run submission and the operator Role do not permit creating that decision.
Kubernetes audit logs identify the authenticated creator.
A request cannot claim another user's identity through a free-form approver field.
Promotion status records the approved manifest and exact passing evaluation.
Serving activation remains under its separate owner and permissions.

## Cancellation and deletion

Cancellation only moves from false to true.
The controller sets Argo shutdown to Terminate.
Run deletion follows the same path.
The run cleanup finalizer waits for observed worker exit.
The Workflow observation finalizer waits for durable terminal status and worker exit.
Ownership or execution conflicts retain the guard for diagnosis.

Deleting an artifact or evaluation can delete its owned evidence run through garbage collection.
That run retains its training cleanup rules.
Source runs, retained files, and unrelated artifacts have no ownership link to that deletion.
The operator never deletes an artifact file or PVC.
Storage or namespace deletion follows the storage owner's policies.

## Versioning and compatibility

| Kubernetes | Argo | Evidence |
| --- | --- | --- |
| 1.37.0 API server | Simulated Argo status | Local CRD, RBAC, admission, controller, and Helm lifecycle tests |
| 1.37.0 kind cluster, ARM64 | 4.1.3 | Original implementation acceptance; rerun the [CPU lifecycle](../quickstart/README.md) for this export |

This table reports tested coverage only.
No wider Kubernetes or Argo version range is certified yet.
The API remains alpha.
Broader version certification and future API migrations require separate release evidence.
A schema compatibility test compares existing resources against the recorded three-resource API baseline.
It rejects removed fields, new required fields, narrowed limits, changed defaults, and changed validation rules.
The real API suite also checks immutable evidence requests and rejected promotion without passing evaluation.

Helm does not upgrade CRDs automatically.
Back up existing resources and test the schema change before applying it.
Apply the reviewed CRD definitions before upgrading the operator.
Existing three-resource installations need the three additional evidence CRDs before this operator starts.
Never delete CRDs as an upgrade procedure.
A future served version requires explicit conversion, storage migration, and old-resource round-trip tests.
