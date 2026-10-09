import { createServer } from "node:http";
import { mkdtemp, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createConsole } from "../server/app.mjs";
import { kubernetesClient } from "../server/kubernetes.mjs";
import { demo } from "../server/demo.mjs";
export const token = "synthetic-console-access-token-for-tests";
export async function fixture(t, overrides = {}) {
  const calls = [];
  const state = structuredClone(demo);
  let failure = "";
  let delay = false;
  const upstream = createServer(async (req, res) => {
    const chunks = [];
    for await (const chunk of req) chunks.push(chunk);
    calls.push({
      method: req.method,
      path: req.url,
      authorization: req.headers.authorization,
      body: chunks.length ? JSON.parse(Buffer.concat(chunks)) : undefined,
    });
    if (delay) return;
    if (failure) {
      res.writeHead(403);
      res.end(JSON.stringify({ error: "private-upstream-detail" }));
      return;
    }
    const resource = req.url.split("/")[6]?.split("?")[0];
    const name = req.url.split("/")[7];
    const body = calls.at(-1).body;
    let value = name
      ? state[resource]?.items.find((item) => item.metadata.name === name)
      : {
          items: state[resource]?.items || [],
          metadata: { continue: resource === "trainingrecipes" ? "more" : "" },
        };
    if (req.method === "POST") {
      value = {
        ...body,
        metadata: { ...body.metadata, uid: "created-uid", resourceVersion: "3" },
        status: { phase: "Pending" },
      };
      state[resource].items.push(value);
    }
    if (req.method === "PATCH") {
      value = { ...value, spec: { ...value.spec, ...body.spec }, status: { phase: "Cancelled" } };
      state[resource].items = state[resource].items.map((item) =>
        item.metadata.name === name ? value : item,
      );
    }
    if (!value) {
      res.writeHead(404);
      res.end("{}");
      return;
    }
    res.setHeader("content-type", "application/json");
    res.end(JSON.stringify(value));
  });
  await new Promise((resolve) => upstream.listen(0, "127.0.0.1", resolve));
  const dir = await mkdtemp(join(tmpdir(), "anvil-console-test-"));
  const tokenFile = join(dir, "token");
  await writeFile(tokenFile, "synthetic-service-account-token");
  const kube = kubernetesClient({
    url: `http://127.0.0.1:${upstream.address().port}`,
    namespace: "anvil-test",
    tokenFile,
    timeout: 150,
  });
  const server = createConsole({
    namespace: "anvil-test",
    kube,
    accessToken: token,
    allowWrites: true,
    ...overrides,
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  t.after(async () => {
    server.closeAllConnections();
    upstream.closeAllConnections();
    await Promise.all([
      new Promise((resolve) => server.close(resolve)),
      new Promise((resolve) => upstream.close(resolve)),
    ]);
    await rm(dir, { recursive: true });
  });
  const base = `http://127.0.0.1:${server.address().port}`;
  return {
    calls,
    base,
    server,
    tokenFile,
    fail: () => {
      failure = "403";
    },
    delay: () => {
      delay = true;
    },
    get: (path) => fetch(base + path, { headers: { authorization: `Bearer ${token}` } }),
    post: (path, body, headers = {}) =>
      fetch(base + path, {
        method: "POST",
        headers: {
          authorization: `Bearer ${token}`,
          "content-type": "application/json",
          ...headers,
        },
        body: JSON.stringify(body),
      }),
  };
}
