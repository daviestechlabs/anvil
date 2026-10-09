# Anvil console

The console shows Anvil resources in one Kubernetes namespace.
It uses the shared Anvil theme, grouped navigation, and compact overview.
It shows workflow progress reported by the operator.
Missing progress shows a waiting message.
The browser refreshes visible pages every ten seconds.

## Try the interface

Install Node.js 24 and Bun 1.4.0.
Run these commands from the console directory.

```bash
bun install --frozen-lockfile
bun run build
ANVIL_CONSOLE_DEMO=1 bun run start
```

Open `http://127.0.0.1:8081`.
Demo mode uses synthetic data.
It cannot contact Kubernetes or submit runs.

## Connect a namespace

Install the Anvil operator and its six resource definitions first.
Argo remains a prerequisite managed by the cluster administrator.
Recipes and their workflow templates need separate review before use.

Use `kubectl proxy` for local reads with your existing Kubernetes identity.
Keep both processes bound to loopback.

```bash
kubectl proxy --address=127.0.0.1 --port=8001
```

In another terminal, start the console.
Replace `anvil` with your namespace.

```bash
ANVIL_NAMESPACE=anvil ANVIL_KUBE_API=http://127.0.0.1:8001 bun run start
```

The console reads installations, recipes, runs, artifacts, evaluations, and promotions.
It reports resource errors separately and labels lists limited to 200 objects.
Resource details include the observed workflow identity and operator conditions.
It does not fetch model weights, artifact bytes, or arbitrary URLs.

## Submit reviewed runs

Submission stays disabled by default.
Enable it with a separate console access token and explicit write permissions.
Create a token file outside this checkout.
Use a secret manager or generate at least 32 random characters.

```bash
ANVIL_NAMESPACE=anvil \
ANVIL_KUBE_API=http://127.0.0.1:8001 \
ANVIL_CONSOLE_TOKEN_FILE=/secure/console-token \
ANVIL_CONSOLE_WRITES=1 bun run start
```

Enter that token in the browser.
The browser retains it in memory until sign-out or reload.
Use `anvilctl recipe-hash recipe.yaml` to get the reviewed recipe binding digest.
Select **New run**, enter the digest and inputs, then confirm your review.
The console creates a `TrainingRun` resource.

The operator validates its binding before it submits an Argo workflow.
Submission alone does not mean execution started.

Cancellation checks the run UID and resource version before requesting termination.
The operator records the final outcome.
Artifact verification and evaluation records remain visible.
Create those records with Kubernetes tooling and separately reviewed recipes.
The console cannot edit recipes, approve promotions, or activate serving models.

## Cluster deployment

Build the image from this directory after exporting the public package.

```bash
docker build -t your-registry.example/anvil-console:local .
```

The [deployment example](examples/deployment.yaml) uses a namespace-scoped read role.
Change its namespace and image to reviewed values before applying it.
Create the `anvil-console-access` Secret separately with a `token` key.
Do not commit that Secret or its value.
Apply [write permissions](examples/writer-role.yaml) only when submission is required.
Set `ANVIL_CONSOLE_WRITES=1` in the Deployment after reviewing those permissions.

In-cluster mode uses the mounted service-account token and cluster CA.
Token reads follow service-account rotation.
For an external API, set `ANVIL_KUBE_TOKEN_FILE` and `ANVIL_KUBE_CA_FILE`.
The client verifies TLS and rejects remote plain HTTP.

Remote listening requires a console token.
Place the service behind an authenticated TLS gateway with rate limits.
The console token protects resource reads and writes.
The token represents one trusted operator group, rather than individual users.
Kubernetes audits identify the console service account.

Use separate instances and service accounts for separate trust boundaries.
Do not expose the HTTP port directly to an untrusted network.

The example grants no Secret, Pod, Workflow, recipe-update, or promotion-write permissions.
The operator retains its separate execution role.

## Development

Run `bun run build`, `bun run test`, and `bun run test:browser`.
Install Chromium with `bunx playwright install --with-deps chromium` before browser tests.
Tests use disposable loopback APIs and synthetic resources.
They check authentication, fixed namespace access, run identity, failures, and responsive navigation.
They do not use production credentials.

For development, run the server on port 8081 and `bun run dev` in another terminal.
The Vite server proxies `/api` requests to that loopback server.
