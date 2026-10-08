# Anvil

Anvil is a Kubernetes operator for reviewed AI training, evaluation, and model promotion workflows.
It runs approved recipes through Argo Workflows and records execution identity, progress, artifact verification, and approval decisions as Kubernetes resources.

## How it works

- **Define** reviewed workflow code, permitted inputs, and worker permissions in a `TrainingRecipe`.
- **Run** that recipe with a `TrainingRun`, with observable status, progress, and cancellation.
- **Verify** retained model or checkpoint bytes with a `TrainingArtifact`.
- **Evaluate** the exact artifact with an `EvaluationRun`.
- **Approve** it through a `ModelPromotion` using separate approver permissions.

The repository includes the operator, `anvilctl` CLI, Helm chart, and a CPU quickstart.
The console and serving integrations are separate; promotion records a decision, while model activation stays with your serving system.

## Quickstart

Clone this repository, then run these commands from its root on a Linux Docker host.
Install Go 1.26.5 or later, Python 3.11 or later, curl, and Docker with BuildKit.

```bash
docker build -t anvil.local/anvil-operator:cpu .
./scripts/test-clean-cluster.sh anvil.local/anvil-operator:cpu /tmp/anvil-cpu-receipt.json
```

The script creates a disposable kind cluster, installs Argo and Anvil, and runs the full synthetic CPU lifecycle.
It checks training, checkpoint recovery, artifact verification, evaluation, approval, cancellation, and Helm upgrades before removing the cluster.
A JSON receipt records the results.
See the [quickstart guide](quickstart/README.md) for details and existing-cluster instructions.

## Installation and documentation

Use the [Helm installation guide](charts/anvil-operator/README.md) to deploy your own operator image by digest.
Argo Workflows is a prerequisite; the chart checks its APIs, and the cluster administrator manages Argo itself.

| Guide | Covers |
| --- | --- |
| [API reference](docs/API.md) | Resources, status, output formats, and compatibility |
| [Execution contract](docs/EXECUTION.md) | Workflow integrity, permissions, retention, and cancellation |
| [Operations](docs/OPERATIONS.md) | Metrics, troubleshooting, backup, upgrades, and removal |
| [Contributing](CONTRIBUTING.md) | Development setup, checks, and review expectations |
| [Security](SECURITY.md) | Private vulnerability reporting |

## Project status

Anvil is alpha, with API version `anvil.dev/v1alpha1`.
The CPU lifecycle is tested with Kubernetes 1.37.0 and Argo Workflows 4.1.3.
There is no stable release or published operator image yet.

Licensed under [MIT](LICENSE).
