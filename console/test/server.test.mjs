import { test } from "node:test";
import assert from "node:assert/strict";
import { request as httpRequest } from "node:http";
import { writeFile } from "node:fs/promises";
import { createConsole } from "../server/app.mjs";
import { kubernetesClient, resources } from "../server/kubernetes.mjs";
import { demo } from "../server/demo.mjs";

import { fixture } from "./fixture.mjs";
const request = {
  name: "test-run",
  recipe: "cpu-training",
  bindingSHA256: "0".repeat(64),
  durationSeconds: 120,
  parameters: { steps: "4" },
};
test("reads only six namespaced CRDs; authenticates and reports truncation", async (t) => {
  const f = await fixture(t);
  assert.equal((await fetch(f.base + "/api/console")).status, 401);
  assert.equal(f.calls.length, 0);
  const response = await f.get("/api/console");
  const result = await response.json();
  assert.equal(result.namespace, "anvil-test");
  assert.equal(result.collections.trainingrecipes.truncated, true);
  assert.equal(f.calls.length, 6);
  assert.deepEqual(
    new Set(f.calls.map((call) => call.path)),
    new Set(
      resources.map(
        (resource) => `/apis/anvil.dev/v1alpha1/namespaces/anvil-test/${resource}?limit=200`,
      ),
    ),
  );
  assert.ok(
    f.calls.every((call) => call.authorization === "Bearer synthetic-service-account-token"),
  );
  assert.equal(response.headers.get("cache-control"), "no-store");
  assert.ok(!JSON.stringify(result).includes("synthetic-service-account-token"));
  await writeFile(f.tokenFile, "rotated-synthetic-token");
  await f.get("/api/console");
  assert.ok(
    f.calls.slice(6).every((call) => call.authorization === "Bearer rotated-synthetic-token"),
  );
});
test("creates only a reviewed TrainingRun and prevents unprivileged or arbitrary writes", async (t) => {
  const f = await fixture(t);
  assert.equal((await f.post("/api/trainingruns", request, { authorization: "" })).status, 401);
  assert.equal(
    (await f.post("/api/trainingruns", request, { origin: "https://foreign.example" })).status,
    403,
  );
  assert.equal(
    (await f.post("/api/trainingruns", { ...request, recipe: "../secrets" })).status,
    400,
  );
  assert.equal(
    (await f.post("/api/trainingruns", { ...request, parameters: { steps: 4 } })).status,
    400,
  );
  assert.equal(
    (await f.post("/api/trainingruns", { ...request, durationSeconds: 121 })).status,
    400,
  );
  assert.equal((await f.post("/api/modelpromotions", {})).status, 404);
  assert.equal((await f.post("/api/trainingruns", request)).status, 201);
  const written = f.calls.at(-1);
  assert.equal(written.method, "POST");
  assert.equal(written.path, "/apis/anvil.dev/v1alpha1/namespaces/anvil-test/trainingruns");
  assert.deepEqual(written.body, {
    apiVersion: "anvil.dev/v1alpha1",
    kind: "TrainingRun",
    metadata: { name: "test-run", namespace: "anvil-test" },
    spec: {
      request: {
        recipeRef: { name: "cpu-training", bindingSHA256: request.bindingSHA256 },
        durationSeconds: 120,
        parameters: { steps: "4" },
      },
    },
  });
});
test("cancellation binds the observed UID and resource version", async (t) => {
  const f = await fixture(t);
  const path = "/api/trainingruns/cpu-training-02/cancel";
  assert.equal((await f.post(path, { uid: "recreated-run", resourceVersion: "2" })).status, 409);
  assert.equal((await f.post(path, { uid: "demo-run-02", resourceVersion: "old" })).status, 409);
  assert.equal((await f.post(path, { uid: "demo-run-02", resourceVersion: "2" })).status, 200);
  assert.deepEqual(f.calls.at(-1).body, {
    metadata: { uid: "demo-run-02", resourceVersion: "2" },
    spec: { cancel: true },
  });
  assert.equal(
    (
      await f.post("/api/trainingruns/cpu-training-01/cancel", {
        uid: "demo-run-01",
        resourceVersion: "1",
      })
    ).status,
    400,
  );
});
test("read-only and demo modes cannot dispatch work", async (t) => {
  const f = await fixture(t, { allowWrites: false, demo, kube: undefined });
  const result = await (await f.get("/api/console")).json();
  assert.equal(result.demo, true);
  assert.equal((await f.post("/api/trainingruns", request)).status, 403);
  assert.equal(f.calls.length, 0);
  assert.throws(
    () => createConsole({ namespace: "anvil-test", allowWrites: true }),
    /Writes require/,
  );
});
test("upstream denial and deadline retain separate errors without sensitive details", async (t) => {
  const f = await fixture(t);
  f.fail();
  const result = await (await f.get("/api/console")).json();
  assert.equal(Object.keys(result.errors).length, 6);
  assert.ok(!JSON.stringify(result).includes("private-upstream-detail"));
  assert.equal((await f.post("/api/trainingruns", request)).status, 403);
  const slow = await fixture(t);
  slow.delay();
  const timedOut = await (await slow.get("/api/console")).json();
  assert.match(timedOut.errors.trainingruns, /timed out/);
});
test("rejects remote HTTP, ambiguous origins, and unsafe namespace identities", () => {
  assert.throws(
    () => kubernetesClient({ url: "http://cluster.example", namespace: "anvil" }),
    /HTTPS/,
  );
  assert.throws(
    () => kubernetesClient({ url: "https://cluster.example/proxy", namespace: "anvil" }),
    /origin/,
  );
  assert.throws(
    () => kubernetesClient({ url: "https://cluster.example", namespace: "../other" }),
    /namespace/,
  );
});
test("bounds input size and rejects unauthenticated DNS rebinding", async (t) => {
  const f = await fixture(t);
  assert.equal(
    (await f.post("/api/trainingruns", { ...request, extra: "x".repeat(40000) })).status,
    413,
  );
  const local = await fixture(t, { accessToken: "", allowWrites: false });
  const status = await new Promise((resolve) =>
    httpRequest(
      local.base + "/api/console",
      { headers: { host: "foreign.example" } },
      (response) => {
        response.resume();
        resolve(response.statusCode);
      },
    ).end(),
  );
  assert.equal(status, 403);
  assert.equal(local.calls.length, 0);
});
