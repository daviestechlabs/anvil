# Anvil operator Helm chart

Helm installs the operator, scoped permissions, and Anvil custom resource definitions (CRDs).
An `Anvil` resource describes the application installation.
The installation controller reconciles native Kubernetes resources.
The training controller reconciles `TrainingRecipe` and `TrainingRun` resources through Argo.

The chart does not create an application instance automatically.
It does not run Helm inside a reconciliation loop.
The application namespace is the Helm release namespace.
One installation named `anvil` is supported in that namespace.
One operator serves that application namespace and its configured training namespace.

## Install

Install Argo Workflows 4.1 and create the training namespace first.
Helm requires the Workflow, WorkflowTemplate, and WorkflowTaskResult APIs before installation or upgrade.
The operator checks their presence at startup without cluster-wide CRD permissions.
The cluster owner retains Argo installation, controller configuration, upgrades, and removal.
Review Argo controller defaults and admission mutations against the operator's [execution contract](../../docs/EXECUTION.md).
Added specification fields cause a `WorkflowConflict` condition.
An observation finalizer retains each Workflow until Anvil records completion and confirms worker exit.
Configure artifact storage and worker credentials if the Argo controller requires log archiving.
Provision a PostgreSQL database and a Bound ReadWriteMany staging claim in the application namespace.
Supply database credentials through an existing Secret.
Supply its certificate authority through an existing ConfigMap or Secret.
CloudNativePG can own the database and these Secrets.
The Anvil operator receives no Secret-read permission.

Publish the operator image with [the build script](../../scripts/build.sh).
Set `REGISTRY=your-registry.example/anvil TAG=local PUSH=1` when running `./scripts/build.sh`.
Use its verified digest when installing the chart.
Run these commands from the repository root.

```bash
helm upgrade --install anvil-operator charts/anvil-operator \
  --namespace anvil --create-namespace \
  --set operatorImage.repository=your-registry.example/anvil-operator \
  --set operatorImage.digest=sha256:YOUR_PUBLISHED_DIGEST
```

Review [the installation example](examples/installation.yaml).
Replace its illustrative image digest, identity settings, and dependency references.
Install that resource through your deployment system.

```bash
kubectl get anvils -n anvil
kubectl describe anvil anvil -n anvil
kubectl get events -n anvil --sort-by=.lastTimestamp
```

The web application requires a signed gateway identity token and configured group membership.
Configure the gateway's authentication policy separately.
An optional `spec.gateway` creates an HTTPRoute for the HTTPS public origin.
Install Gateway API before enabling that field.
The Gateway owns TLS and authentication.
Anvil waits for that parent listener's current acceptance and resolved references.

For offline rendering, declare the three required APIs explicitly.
These flags describe the target cluster; they do not install Argo.
Live Helm installation checks the cluster's discovered APIs.

```bash
helm template anvil-operator charts/anvil-operator \
  --namespace anvil \
  --set operatorImage.digest=sha256:YOUR_PUBLISHED_DIGEST \
  --api-versions argoproj.io/v1alpha1/Workflow \
  --api-versions argoproj.io/v1alpha1/WorkflowTemplate \
  --api-versions argoproj.io/v1alpha1/WorkflowTaskResult
```

## Chart archive

Package the chart from the repository root.

```bash
helm package charts/anvil-operator --destination dist
helm show chart dist/anvil-operator-0.1.0.tgz
helm show crds dist/anvil-operator-0.1.0.tgz
```

The archive includes all six CRDs, this guide, the MIT license, and the installation example.
Use the archive path instead of `charts/anvil-operator` in the Helm installation command.
Keep the published operator image digest explicit.
Packaging does not publish an image, upload the chart, or install resources.
CI packages this archive before its local Kubernetes installation check.

## Application lifecycle

The operator owns the web, controller, and artifact-importer Deployments.
It also owns their service accounts, the web Service, and an optional HTTPRoute.
The importer remains disabled unless the resource enables it.
Web replicas use rolling updates.
Controller and importer updates use replacement to avoid overlapping coordinators.

Changing `spec.image` requests an application upgrade to that exact digest.
Changing `spec.webReplicas` requests scaling.
Referenced ConfigMap changes update the configuration hash and restart application Pods.
Increment `spec.configurationRevision` after an in-place Secret rotation.
The operator does not watch Secret contents.
Removing `spec.gateway` deletes only this installation's owned route.

The `Ready` condition checks the current Deployment generations and optional Gateway status.
It also requires the staging claim and referenced ConfigMaps.
It does not prove database recovery, model quality, or a real training execution.
Kubernetes reports missing credential Secrets through Pod events.

Database, credential, certificate, and storage resources retain their separate owners.
Deleting the Anvil resource removes its owned application resources through Kubernetes garbage collection.
It retains the database and staging claim.
The staging claim reference remains immutable to prevent accidental data replacement.
Changing it requires a separate installation migration.

## CRD upgrades and removal

Helm installs the CRDs from `crds/` before installing the operator.
Helm does not upgrade or delete those definitions automatically.
Review schema compatibility and manage CRD upgrades through your deployment system.
The [Helm CRD lifecycle guide](https://helm.sh/docs/chart_best_practices/custom_resource_definitions/) describes that boundary.
CI verifies that the chart's CRD copies match `config/crd/` exactly.

Cancel active TrainingRuns and wait for their workers before removing their controller.
Delete the Anvil application resource before uninstalling its Helm release.
Removing Helm alone leaves the application resource and its workloads present without installation reconciliation.
Existing databases and storage remain outside both deletion paths.

## Checks

The chart tests require Helm and PyYAML.
They check Argo prerequisites, rendering, digest requirements, CRD copies, namespace scope, and permissions.

```bash
python scripts/test-chart.py
./scripts/test.sh
ANVIL_HELM_TEST=1 ./scripts/test-integration.sh
```

Set `KUBEBUILDER_ASSETS` before running the integration suite.
The suite uses a real Kubernetes API server and simulated Deployment and Gateway status.
It proves admission, reconciliation, upgrades, ownership checks, and storage retention.

Helm also installs, upgrades, and removes the chart against that local API server.
The installation controller uses the chart's actual scoped permissions during that check.
The check verifies that Helm retains the CRDs, application resource, and external storage.
It does not start application Pods or prove application execution.
Set `ANVIL_HELM_CHART` to an absolute chart archive path to test that archive instead of the source directory.

## Evidence and monitoring

Install the [separate evidence and approver Roles](../../config/rbac/evidence-reviewer.yaml) in the training namespace.
Bind them to your authenticated groups through your deployment system.
The operator can record status but cannot create promotion decisions.
Enable metrics and optional Prometheus resources through the [operations guide](../../docs/OPERATIONS.md).
The [portable CPU workflow](../../quickstart/README.md) proves the release lifecycle with retained bytes.
