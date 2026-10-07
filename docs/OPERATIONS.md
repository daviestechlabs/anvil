# Operator operations

## Diagnose a run

Start with the resource identity and current conditions.
Use the CLI with the training namespace and your normal Kubernetes credentials.

```bash
anvilctl doctor --namespace YOUR_TRAINING_NAMESPACE
anvilctl inspect --namespace YOUR_TRAINING_NAMESPACE --name YOUR_RUN
kubectl describe trainingrun YOUR_RUN -n YOUR_TRAINING_NAMESPACE
kubectl get events -n YOUR_TRAINING_NAMESPACE --sort-by=.lastTimestamp
```

RecipeMissing and TemplateMissing require the named administrator-reviewed dependency.
RecipeDisabled indicates an intentional hold.
AdmissionRejected requires a corrected new request or binding.
WorkflowConflict requires inspection of ownership, hashes, Argo defaults, and admission mutations.
WorkflowLost records a missing execution and cannot trigger another execution automatically.
WorkersRemain requires Pod and node diagnosis before capacity release.
CapacityWaiting requires inspection of the Argo mutex or scheduler's resource and placement events.
Permission and network failures retain the same execution identity for retry.

The authenticated console shows the last verified Kubernetes observation for its user's runs.
It includes the run UID, Workflow reference, phase, conditions, and observed generation.
The timestamp makes observation age explicit.
Measured progress shows Argo's completed and total work counts.
The total can grow; the percentage does not estimate remaining time.
Stale generations show a waiting indicator until the operator observes the change.
The console shows capacity waits, suspended workflows, and worker cleanup reasons beside the run.
The console does not receive additional Kubernetes credentials.
Its existing controller observes resources through its scoped Role.
Human review, queue admission, and audit remain database-owned state.
Use anvilctl for a direct current API read when the console controller is unavailable.

## Metrics and alerts

Metrics remain disabled by default.
Enable the chart's metrics Service and ingress restriction explicitly.
Install Prometheus Operator CRDs before enabling its monitor or alert resources.

```yaml
metrics:
  enabled: true
  serviceMonitor: true
  alerts: true
  monitoringNamespace: monitoring
```

The endpoint uses cluster-only HTTP on port 8080.
The ingress policy admits the chosen monitoring namespace.
The cluster network plugin must enforce NetworkPolicy.
Do not expose this endpoint through a public route.
Configure Prometheus to select the generated monitor and rules.

Metrics include run counts, oldest cancellation age, oldest capacity wait, oldest deletion age, and observation failures.
Controller-runtime also reports reconciliation failures and durations.
Metrics contain bounded phase and reason labels.
They contain no run names, UIDs, dataset paths, or parameter values.
Alerts cover repeated reconciliation errors and extended cancellation, capacity, and finalizer waits.
Inspect the named conditions and events before changing capacity or cleanup state.

## Backup

Back up the Kubernetes datastore through its cluster owner.
Back up Postgres through its database owner when the console is installed.
Back up retained artifact and checkpoint bytes through their storage owner.
Preserve the operator image digest, reviewed templates, recipes, schema versions, and identity policies.
Keep recipe bindings, resource UIDs, status, finalizers, and owner references together.
YAML exports alone do not preserve Kubernetes UIDs during recreation.
Verify backup integrity and perform an isolated recovery drill before a production change.

## Restore

Stop submission and isolate workers from the damaged execution plane.
Restore the cluster datastore and artifact storage to a consistent recovery point.
Verify resource UIDs and Workflow ownership before starting the same operator digest.
Observe existing execution records before enabling new requests.
A lost API response recovers the same guarded Workflow or evidence-run name.
A changed UID fails integrity checks instead of adopting another execution.

Do not recreate old runs from exported YAML and treat them as restored executions.
Kubernetes assigns new UIDs to recreated objects.
Treat such objects as a new installation with separate historical receipts.
If the datastore cannot restore identities, retain the history outside the active execution namespace.
Resolve or isolate old workers before accepting a new execution.
Validate storage hashes and retained lineage before accepting promotion evidence.

## Credential rotation

The operator does not read credential Secret contents.
Rotate worker repository credentials through their existing storage or identity owner.
Rotate application database and identity credentials through their existing Secret owner.
Increment `Anvil.spec.configurationRevision` after an in-place application Secret rotation.
Wait for current owned Deployment readiness and verify the authenticated user path.
Keep credential values out of recipe parameters, logs, receipts, and browser errors.
For cluster credentials, preserve the service account's scoped Role during rotation.
The operator retries observation instead of creating a new execution after authentication failure.

## Remove an installation

Hold recipes and stop new submissions.
Cancel active runs and wait for terminal status plus observed worker exit.
Confirm that no evidence-run observation guard remains.
Delete the Anvil application resource if the console is installed.
Verify removal of its owned application resources.
Uninstall the Helm release.
Retain CRDs, historical execution resources, recipes, PVCs, and external database backups.
Verify retained artifact bytes after removal.

An unavailable controller can leave finalizers in place.
Restart its recorded version and repair permissions or connectivity first.
Manual finalizer removal requires proven worker exit and retained execution evidence.
It is an incident procedure, not a routine uninstall step.
Deleting the namespace or CRDs can remove retained API history and PVCs.
Keep those actions under the cluster and storage owners' separate decisions.
