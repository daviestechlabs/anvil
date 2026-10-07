# Anvil

Anvil governs reviewed training, evaluation, and promotion on Kubernetes.
Argo executes the reviewed workflow code.
Anvil binds that code to permitted inputs, execution identity, retained artifacts, and explicit review decisions.
The operator provides the Kubernetes execution interface.
A custom resource definition (CRD) adds a resource type to the Kubernetes API.
This module supplies installation and training CRDs with native Go controllers.
It uses Argo Workflows for execution.
The training controller requires no Anvil web service, Postgres, Authentik, or homelab endpoint.
It targets Argo Workflows 4.1.

`TrainingRecipe` binds reviewed workflow code and permitted inputs.
`TrainingRun` requests one execution of that recipe.
Kubernetes stores the request, execution identity, conditions, and outputs.
The controller never promotes a model or changes a serving Deployment.

Anvil uses the [MIT license](LICENSE).
Its dependencies and build image have explicit versions or content hashes.
The API is `anvil.dev/v1alpha1`.
The alpha API can change before a stable release.

## Public scope

This repository ships the Kubernetes operator, anvilctl, Helm chart, and portable CPU workflow.
The application installation API accepts an externally built console image.
The console frontend and site-specific serving adapters remain outside this first public package.


The reusable core owns reviewed Kubernetes execution and evidence records.
Argo owns worker execution and scheduling.
Recipe administrators own executable code, input authorization, storage, and worker permissions.
The console owns authenticated human review and audit history.
Cluster, database, gateway, and storage owners retain their infrastructure responsibilities.
Serving owners activate models after a separate decision.

The standalone Go module needs no sibling repository package.
Use the [portable CPU workflow](quickstart/README.md) before configuring private data or accelerator placement.
Use [the API contract](docs/API.md) for resource fields, conditions, output formats, and tested compatibility.
Use [the operations guide](docs/OPERATIONS.md) for metrics, diagnosis, backup, restore, rotation, and removal.

`TrainingArtifact` verifies retained bytes through reviewed code and records their execution lineage.
`EvaluationRun` evaluates that exact artifact through another reviewed recipe.
`ModelPromotion` records an explicit decision through a separate approver permission.
These records do not activate model serving.

## Application installation

The [Helm chart](charts/anvil-operator/README.md) installs this operator and its CRDs.
An `Anvil` resource describes one application installation in the configured application namespace.
The installation controller manages Deployments, service accounts, the web Service, and an optional Gateway API route.
It reports current readiness through conditions and Kubernetes events.
Configuration changes trigger application rollouts.
External database and storage references preserve their separate owners and deletion policies.

The chart enables installation reconciliation through `--installation-namespace`.
The existing Kustomize configuration retains training-only reconciliation.
The operator watches only its configured application and training namespaces.
It rejects attempts to adopt existing Flux resources.
The [installation example](examples/installation.yaml) contains illustrative references that require replacement.

## Execution contract

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
Prepared homelab templates bind the current shared retention defaults explicitly.
The CPU example uses the same reviewed retention policy.

Cancellation sets Argo shutdown to `Terminate`.
The run remains active until Argo becomes terminal and all observed workers stop.
Deleting a run uses the same termination path.
A finalizer retains the run until that check passes.
Execution errors retain ownership and retry observation of the same Workflow.
An ownership or specification conflict holds the run for operator inspection.

## Local checks

Use Go 1.26 or later.
Run the package checks.

```bash
./scripts/test.sh
go build ./cmd/anvil-operator ./cmd/anvilctl
kubectl kustomize config/default > /tmp/anvil-install.yaml
```

The integration suite starts a local Kubernetes API server and etcd.
It checks the shipped CRDs and controller transitions.
It simulates Argo status, worker exit, Deployment availability, and Gateway acceptance.
It does not prove live Argo execution.

Install the test binaries with the pinned setup tool.
Run the integration suite.

```bash
go run sigs.k8s.io/controller-runtime/tools/setup-envtest@v0.0.0-20250517180713-32e5e9e948a5 \
  use 1.37.0 --bin-dir /tmp/anvil-envtest -p path
export KUBEBUILDER_ASSETS=/tmp/anvil-envtest/k8s/1.37.0-linux-amd64
./scripts/test-integration.sh
```

Use the printed asset path for your operating system and architecture.
Tests use a Kubernetes 1.37 API server.
The target cluster must support these validation rules and Argo Workflows.
Set `ANVIL_HELM_TEST=1` with Helm on `PATH` to check installation, upgrades, removal, and the chart's actual permissions.
That check rejects missing Argo APIs before installing workloads.
It then installs external Argo test definitions and lets Helm install all six Anvil CRDs.
It verifies that operator removal retains the CRDs, application resource, and external storage.

## Installation

The checked-in deployment uses a development image reference.
A release overlay must replace it with your published image digest.
The default overlay targets an existing `anvil-training` namespace.
The Helm chart instead requires an explicit published image digest.
Use the chart for combined application and training management.

The standalone Dockerfile uses a pinned Go builder and verified module downloads.
Build locally with Docker.
Set REGISTRY and PUSH=1 to publish your image.
Use the published image digest when installing the chart.

```bash
REGISTRY=your-registry.example/anvil TAG=local ./scripts/build.sh
kubectl kustomize config/default
```

Install Argo Workflows and permit its controller to operate in the chosen namespace.
Helm requires the Workflow, WorkflowTemplate, and WorkflowTaskResult APIs before installation or upgrade.
The operator checks these APIs at startup.
The `anvilctl doctor` command reports the same prerequisites.
These checks prove API presence; they do not prove Argo controller health.
The cluster owner manages Argo installation and upgrades.
Anvil does not install, adopt, or remove shared Argo CRDs.
Check its defaults, retention policy, and artifact repository against the execution contract above.
Apply the rendered overlay through your deployment system.
Install reviewed templates and recipes before submitting runs.
The operator needs its namespaced Role and a Lease for leader election.
Its Role cannot read Secrets, modify templates, execute in Pods, or modify serving Deployments.

Bind `anvil-run-submitter` to authenticated users who can submit training requests.
Recipe administrators need separate permission to manage recipes and templates.
Run submitters receive no Workflow or status-write permission from this Role.

## CPU example

The example trains a linear model from ten synthetic samples.
It uses no private dataset or accelerator.
It reports the learned model's SHA-256 hash.
It does not publish or promote the model.

The example files contain computed template and recipe hashes.
Verify both hashes before installation.

```bash
go run ./cmd/anvilctl template-hash examples/workflow-template.yaml
go run ./cmd/anvilctl recipe-hash examples/recipe.yaml
```

The first result must match `spec.binding.templateSpecSHA256` in the recipe.
The second must match `spec.request.recipeRef.bindingSHA256` in the run.
Recompute both after changing the template or binding.
Create a new recipe name for a changed binding.

Hashes use UTF-8 JSON with sorted object keys and Go JSON escaping.
Recipe hashes first omit empty optional fields through the typed API.
Use `anvilctl` to compute hashes instead of hashing YAML bytes.

Use your deployment system to install the example worker Role, template, and recipe.
The worker account token serves only the Argo executor; the Python trainer does not mount it.
Its init and wait containers have bounded resources for namespaces with resource quotas.
The executor needs access to the Kubernetes API.
For a Cilium default-deny namespace, review and install `examples/cilium-api-egress.yaml` through the deployment system.
Submit the run.

```bash
kubectl create -f examples/run.yaml
kubectl get trainingruns -n anvil-training -w
kubectl get trainingrun linear-model-first-run -n anvil-training -o yaml
```

Cancel an active run.

```bash
kubectl patch trainingrun linear-model-first-run -n anvil-training \
  --type merge -p '{"spec":{"cancel":true}}'
```

Create a new run name for another execution.
Reapplying a completed run does not repeat its execution.

## Development and contribution

Install Go 1.26.5 or later, Python 3.11 or later, PyYAML, and Helm.
Run these checks before opening a pull request.

```bash
./scripts/test.sh
python3 scripts/test-chart.py
python3 scripts/test-api-compatibility.py
python3 quickstart/test_quickstart.py
```

Read [CONTRIBUTING.md](CONTRIBUTING.md) for review and testing rules.
Read [SECURITY.md](SECURITY.md) before reporting a vulnerability.
The API remains alpha.
There is no published operator image or stable release yet.

