# Execution contract

Recipes and runs share one namespace with their Argo templates and workers.
Each operator watches one namespace.
Install another operator for another namespace.

A recipe binds the template name, revision, and specification hash.
Its binding also fixes the worker service account, input rules, execution limit, and capacity lock.
An administrator can enable or disable the recipe.
Changing its binding requires a new recipe.

A run binds the complete recipe binding hash.
Its request remains immutable.
Cancellation can change from false to true once.
Kubernetes enforces both rules through validation expressions.

The controller copies the reviewed template specification into the Workflow.
Later template edits cannot change that Workflow.
Nested template references and mutable container images fail admission.
Template argument defaults must have corresponding recipe bindings.
The controller rejects unknown inputs and attempts to replace fixed inputs.

The recipe's concurrency key creates an [Argo mutex](https://argo-workflows.readthedocs.io/en/release-4.1/fields/#synchronization).
A mutex permits one execution per key in that namespace.

The template remains responsible for dataset authorization, source verification, artifact storage, and training isolation.
External ConfigMaps, Secrets, and storage objects are outside the template specification hash.
Existing trainers must retain their content and authorization checks.
Keep outputs in quarantine until independent evaluation and promotion pass.

The Workflow name derives from the run's Kubernetes unique identifier (UID).
The controller records admission and the expected Workflow specification hash before creation.
A lost creation response recovers the same name.
A recorded Workflow UID cannot change.
A missing recorded Workflow fails its run; it cannot trigger another execution.

Workflow integrity comparison omits known empty fields from Argo's typed serialization.
These fields are empty template metadata, input and output maps, container names, and resource maps.
Volume mounts also omit an explicit false readOnly value, which Kubernetes defines as the default.
True readOnly values and nonempty fields remain part of the execution hash.
New Workflows use hash version two.
Existing Workflows without that annotation retain version-one comparison.
Template content hashes still bind the exact reviewed specification.

Retain Workflows until Anvil records terminal status.
Each owned Workflow has an observation finalizer.
The finalizer blocks Workflow deletion until Anvil records the result and confirms worker exit.
Reviewed templates can specify Argo retention and Pod cleanup.
Their specification hashes bind those policies with the executable code.
Kubernetes retains a pending deletion during an operator outage.
After recovery, Anvil records completion before releasing that deletion.

Deleting a run releases its Workflow guard before removing the run's cleanup finalizer.
Do not remove these finalizers manually while workers remain active.

Argo merges [controller defaults](https://argo-workflows.readthedocs.io/en/release-4.1/default-workflow-specs/) into inline Workflow specifications.
Defaults that add fields change the reviewed specification and cause a `WorkflowConflict` condition.
Use a compatible Argo controller configuration before enabling recipes.
Review admission policies for the same mutation risk.
Anvil does not ignore added execution fields or change shared Argo configuration automatically.
Controller log archiving also requires a working artifact repository and worker credentials.
The CPU example uses the same reviewed retention policy.

Cancellation sets Argo shutdown to `Terminate`.
The run remains active until Argo becomes terminal and all observed workers stop.
Deleting a run uses the same termination path.
A finalizer retains the run until that check passes.
Execution errors retain ownership and retry observation of the same Workflow.
An ownership or specification conflict holds the run for operator inspection.
