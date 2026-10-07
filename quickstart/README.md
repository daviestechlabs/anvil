# Portable CPU quickstart

This workflow needs no Anvil console, database, identity gateway, or homelab endpoint.
It trains a synthetic linear model and retains model and checkpoint bytes on a persistent volume claim (PVC).
Its recipes remain held until an administrator reviews and enables them.
The trainer has no Kubernetes token mount.
The Argo executor has only task-result permissions.

## Build and test locally

Run these commands from the standalone operator directory on a Linux Docker host.
Install Go 1.26.5, Python 3, curl, and Docker with BuildKit.
The test wrapper installs pinned kind, kubectl, and Helm tools into temporary storage.

```bash
docker build -t anvil.local/anvil-operator:cpu .
scripts/test-clean-cluster.sh anvil.local/anvil-operator:cpu /tmp/anvil-cpu-receipt.json
```

This path needs no registry publication or existing cluster.
It loads the built image by its actual manifest digest into a fresh kind cluster.
The wrapper removes that cluster after the test.
The receipt records the successful lifecycle before cleanup.
Failed tests print resource status, events, and operator logs before cleanup.

## Use a disposable cluster

Use a disposable Kubernetes cluster with Argo Workflows 4.1.3 and a dynamic storage class.
The Argo controller must watch the training namespace.
Use a published operator image digest for the cluster's processor architecture.
Install Helm and kubectl.
Build anvilctl from the standalone operator module with Go 1.26.5.

```bash
go build -o /tmp/anvilctl ./cmd/anvilctl
python3 quickstart/smoke.py \
  --namespace anvil-cpu-example \
  --kubeconfig /path/to/disposable-cluster-config \
  --operator-image YOUR_REGISTRY/anvil-operator@sha256:YOUR_DIGEST \
  --anvilctl /tmp/anvilctl \
  --receipt /tmp/anvil-cpu-receipt.json
```

The namespace must not exist before this command.
The script installs the Helm chart and waits for its actual operator Pod.
It verifies these steps with real Argo workers.

1. Train the model and retain a checkpoint.
2. Verify both files through a reviewed verifier recipe.
3. Resume the checkpoint and reproduce the exact final model.
4. Evaluate that model against a fixed synthetic holdout.
5. Create a separate promotion decision after passing evaluation.
6. Cancel an active worker and wait for its exit.
7. Upgrade the Helm release and verify unchanged execution identities.
8. Remove the operator and read the retained model from a fresh Pod.

The promotion decision records approval only.
It does not activate model serving.
The script uses administrator access within its disposable namespace.
Production deployments must bind the separate submitter and approver Roles.

Use `--storage-class` to choose storage and `--node` to choose placement.
The default storage request needs one gigabyte.
The script leaves the namespace and PVC after success or failure.
Inspect the receipt and events before removing them.
Deleting that namespace can delete its PVC and storage under the storage class's reclaim policy.

To review manifests before installation, use the preparation command alone.

```bash
python3 quickstart/prepare.py --namespace anvil-training \
  --worker your-worker --storage-class your-storage-class \
  --anvilctl /tmp/anvilctl --output /tmp/anvil-cpu-manifests
```

Review the generated storage, worker permissions, immutable artifact configuration, templates, and held recipes.
The configuration disables Argo log archiving for this synthetic example.
Production artifact repositories and their credentials remain administrator-owned dependencies.
The PVC retains files under content-addressed paths.
The trainer publishes files with atomic links and refuses to replace existing content.
The verifier recomputes SHA-256 from retained bytes.
Storage administrators must enforce retention and protect those files from other writers.

The recovery test uses a checkpoint from a successful source execution.
Cancelled training does not claim successful model output or a recoverable checkpoint.

## Automated gate

`quickstart/smoke.py` is the shared release acceptance workflow.
A successful receipt requires an actual image installation and retained bytes after removal.
Local source tests prove the numerical and verification contract only.
API-server tests prove CRD validation and reconciliation only.
Neither test substitutes for the release acceptance receipt.

The GitHub CPU workflow runs the same acceptance script on a fresh kind cluster.
It loads the locally built image by its actual OCI manifest digest.
It uses pinned Kubernetes 1.37.0 and Argo 4.1.3.
It publishes no image and touches no production Kubernetes context.
The workflow retains a successful CPU receipt as a CI artifact.
A failed test retains its CI logs and removes the disposable kind cluster.
