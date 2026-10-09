import { createServer } from "node:http";
import { timingSafeEqual } from "node:crypto";
import { readFile, realpath } from "node:fs/promises";
import { resolve, extname, sep } from "node:path";
import { ConsoleError, resources, validName } from "./kubernetes.mjs";

const mime = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript",
  ".css": "text/css",
  ".svg": "image/svg+xml",
};
const terminal = new Set(["Succeeded", "Failed", "Cancelled", "Rejected"]);
const equal = (a, b) => {
  const x = Buffer.from(a);
  const y = Buffer.from(b);
  return x.length === y.length && timingSafeEqual(x, y);
};
function assert(condition, message) {
  if (!condition) throw new ConsoleError(400, message);
}
async function jsonBody(request) {
  if (request.headers["content-type"]?.split(";")[0] !== "application/json")
    throw new ConsoleError(415, "Use application/json");
  const chunks = [];
  let size = 0;
  for await (const chunk of request) {
    size += chunk.length;
    if (size > 32768) throw new ConsoleError(413, "Request exceeds 32 KiB");
    chunks.push(chunk);
  }
  try {
    return JSON.parse(Buffer.concat(chunks).toString("utf8"));
  } catch {
    throw new ConsoleError(400, "Invalid JSON");
  }
}
export function createConsole({
  namespace,
  kube,
  accessToken = "",
  allowWrites = false,
  demo = null,
  dist,
}) {
  if (!validName(namespace)) throw new Error("Set an explicit Kubernetes namespace");
  if (allowWrites && (!kube || demo || accessToken.length < 32))
    throw new Error("Writes require Kubernetes and a console token of at least 32 characters");
  const server = createServer(async (request, response) => {
    response.setHeader("x-content-type-options", "nosniff");
    response.setHeader("referrer-policy", "no-referrer");
    response.setHeader("cache-control", "no-store");
    response.setHeader(
      "content-security-policy",
      "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'",
    );
    const send = (status, body) => {
      response.writeHead(status, { "content-type": "application/json" });
      response.end(JSON.stringify(body));
    };
    try {
      if (
        !accessToken &&
        !["127.0.0.1", "localhost", "[::1]"].includes(
          new URL(`http://${request.headers.host}`).hostname,
        )
      )
        throw new ConsoleError(403, "Unauthenticated access requires a loopback host");
      const url = new URL(request.url, "http://console.invalid");
      if (!url.pathname.startsWith("/api/")) {
        if (request.method !== "GET" && request.method !== "HEAD")
          throw new ConsoleError(405, "Method not allowed");
        if (!dist) throw new ConsoleError(404, "Build the console first");
        const root = await realpath(dist);
        const path = resolve(
          root,
          "." + decodeURIComponent(url.pathname === "/" ? "/index.html" : url.pathname),
        );
        if (!path.startsWith(root + sep)) throw new ConsoleError(404, "File not found");
        const actual = await realpath(path).catch(() => {
          throw new ConsoleError(404, "File not found");
        });
        if (!actual.startsWith(root + sep) || !mime[extname(actual)])
          throw new ConsoleError(404, "File not found");
        response.writeHead(200, { "content-type": mime[extname(actual)] });
        response.end(request.method === "HEAD" ? undefined : await readFile(actual));
        return;
      }
      if (url.pathname === "/api/health" && request.method === "GET") {
        send(200, { healthy: true });
        return;
      }
      if (accessToken && !equal(request.headers.authorization ?? "", `Bearer ${accessToken}`))
        throw new ConsoleError(401, "Enter your console access token");
      if (url.pathname === "/api/console" && request.method === "GET") {
        const collections = {};
        const errors = {};
        if (demo) Object.assign(collections, demo);
        else if (kube)
          await Promise.all(
            resources.map(async (resource) => {
              try {
                const list = await kube(resource);
                if (!Array.isArray(list.items))
                  throw new ConsoleError(502, "Invalid Kubernetes resource list");
                collections[resource] = {
                  items: list.items,
                  truncated: Boolean(list.metadata?.continue),
                };
              } catch (error) {
                errors[resource] =
                  error instanceof ConsoleError ? error.message : "Kubernetes is unavailable";
              }
            }),
          );
        send(200, {
          namespace,
          connected: Boolean(kube),
          demo: Boolean(demo),
          allowWrites,
          collections,
          errors,
        });
        return;
      }
      if (!allowWrites || demo || !kube) throw new ConsoleError(403, "Run submission is disabled");
      if (request.method !== "POST") throw new ConsoleError(405, "Method not allowed");
      // Bearer authorization is required for all writes. Reject cross-origin browser requests too.
      if (request.headers.origin && new URL(request.headers.origin).host !== request.headers.host)
        throw new ConsoleError(403, "Use the console origin");
      if (url.pathname === "/api/trainingruns") {
        const input = await jsonBody(request);
        assert(
          input && validName(input.name) && validName(input.recipe),
          "Use valid run and recipe names",
        );
        assert(
          typeof input.bindingSHA256 === "string" && /^[a-f0-9]{64}$/.test(input.bindingSHA256),
          "Use the reviewed recipe binding SHA-256",
        );
        assert(
          Number.isSafeInteger(input.durationSeconds) &&
            input.durationSeconds >= 1 &&
            input.durationSeconds <= 604800,
          "Duration must be 1–604800 seconds",
        );
        assert(
          input.parameters &&
            typeof input.parameters === "object" &&
            !Array.isArray(input.parameters) &&
            Object.entries(input.parameters).every(
              ([key, value]) =>
                /^[a-zA-Z0-9_-]{1,64}$/.test(key) &&
                typeof value === "string" &&
                value.length <= 4096,
            ),
          "Parameters must contain string values",
        );
        const recipe = await kube("trainingrecipes", { name: input.recipe });
        assert(recipe.spec?.enabled === true, "The recipe is disabled");
        assert(
          input.durationSeconds <= recipe.spec.binding.maxDurationSeconds,
          "Duration exceeds the recipe limit",
        );
        const run = await kube("trainingruns", {
          method: "POST",
          body: {
            apiVersion: "anvil.dev/v1alpha1",
            kind: "TrainingRun",
            metadata: { name: input.name, namespace },
            spec: {
              request: {
                recipeRef: { name: input.recipe, bindingSHA256: input.bindingSHA256 },
                durationSeconds: input.durationSeconds,
                parameters: input.parameters,
              },
            },
          },
        });
        send(201, { name: run.metadata.name, uid: run.metadata.uid });
        return;
      }
      const match = url.pathname.match(/^\/api\/trainingruns\/([a-z0-9-]+)\/cancel$/);
      if (match) {
        assert(validName(match[1]), "Invalid run identity");
        const input = await jsonBody(request);
        assert(
          typeof input?.uid === "string" &&
            input.uid.length > 0 &&
            typeof input.resourceVersion === "string",
          "Use the current run identity",
        );
        const run = await kube("trainingruns", { name: match[1] });
        if (
          run.metadata.uid !== input.uid ||
          run.metadata.resourceVersion !== input.resourceVersion
        )
          throw new ConsoleError(409, "The run changed; refresh before cancelling");
        assert(!terminal.has(run.status?.phase), "The run has already finished");
        await kube("trainingruns", {
          name: match[1],
          method: "PATCH",
          body: {
            metadata: { uid: input.uid, resourceVersion: input.resourceVersion },
            spec: { cancel: true },
          },
        });
        send(200, { name: match[1], cancelled: true });
        return;
      }
      throw new ConsoleError(404, "API route not found");
    } catch (error) {
      send(error instanceof ConsoleError ? error.status : 500, {
        error: error instanceof ConsoleError ? error.message : "Console request failed",
      });
    }
  });
  server.requestTimeout = 15000;
  server.headersTimeout = 10000;
  return server;
}
