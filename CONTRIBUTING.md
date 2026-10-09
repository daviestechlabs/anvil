# Contributing

Anvil is an alpha Kubernetes control plane for reviewed AI workflows.
Open an issue before changing the API or execution guarantees.
Use small pull requests with a clear problem statement and validation results.
Maintainers review changes before merge.
No contributor license agreement is required.

## Maintenance model

The full Anvil implementation is maintained in a separate, authoritative private repository.
This repository publishes its reusable open source core.
Contributors can submit issues and pull requests here without access to the private repository.
Maintainers review shared changes in the authoritative repository before publishing an updated export.
Private integrations and deployment configuration remain outside this package.

The portable console uses shared Anvil presentation files and namespace-scoped Kubernetes APIs.
GitHub CI validates this package; production deployments follow the authoritative repository's release process.

## Local checks

Install Go 1.26.5 or later, Python 3.11 or later, PyYAML, and Helm.
Build the CLI and run the package checks.

```bash
go build -o /tmp/anvilctl ./cmd/anvilctl
./scripts/test.sh
python3 scripts/test-chart.py
python3 scripts/test-api-compatibility.py
ANVILCTL_PATH=/tmp/anvilctl python3 quickstart/test_quickstart.py
```

Controller changes need race tests and local Kubernetes API tests.
CRD changes need schema compatibility tests and matching chart copies.
Execution changes need the disposable CPU lifecycle test.
Use synthetic data and a disposable cluster.
Never run contribution tests against production.

## Console checks

Run these commands from `console/` with Node.js 24 and Bun 1.4.0.

```bash
bun install --frozen-lockfile
bun run build
bun run test
bunx playwright install --with-deps chromium
bun run test:browser
```

Browser checks use synthetic resources and disposable loopback APIs.
The console cannot approve promotions or edit reviewed recipes.

## Kubernetes integration checks

Run the checks from the repository root.

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

## Review rules

Preserve immutable execution identity and fail-closed evidence checks.
Keep worker privileges separate from operator privileges.
Promotion records approval; serving activation remains a separate action.
Document API changes and resource retention behavior.
Include tests for changed failure and recovery behavior.
Do not commit credentials, private endpoints, datasets, model weights, or production receipts.
The CI secret scanner supplements manual review.

## Community

Treat contributors with respect.
Discuss technical decisions without personal attacks or harassment.
Maintainers can remove abusive content and restrict participation.
Use GitHub issues for ordinary support.
Use the security reporting route for vulnerabilities.
