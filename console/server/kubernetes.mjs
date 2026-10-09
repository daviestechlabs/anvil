import http from "node:http";
import https from "node:https";
import { readFile } from "node:fs/promises";

export const resources = [
  "anvils",
  "trainingrecipes",
  "trainingruns",
  "trainingartifacts",
  "evaluationruns",
  "modelpromotions",
];
export const dnsLabel = /^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?$/;
export function validName(value) {
  return typeof value === "string" && value.length <= 63 && dnsLabel.test(value);
}
export class ConsoleError extends Error {
  constructor(status, message) {
    super(message);
    this.status = status;
  }
}

export function kubernetesClient({ url, namespace, tokenFile, caFile, timeout = 8000 }) {
  if (!validName(namespace)) throw new Error("Set an explicit Kubernetes namespace");
  const base = new URL(url);
  if (base.username || base.password || base.pathname !== "/" || base.search || base.hash)
    throw new Error("Use a Kubernetes API origin");
  if (
    base.protocol !== "https:" &&
    !(base.protocol === "http:" && ["127.0.0.1", "[::1]", "localhost"].includes(base.hostname))
  )
    throw new Error("Kubernetes requires HTTPS or a loopback proxy");
  return async (resource, { name, method = "GET", body } = {}) => {
    if (!resources.includes(resource) || (name !== undefined && !validName(name)))
      throw new ConsoleError(400, "Invalid resource identity");
    const path = `/apis/anvil.dev/v1alpha1/namespaces/${namespace}/${resource}${name ? `/${name}` : method === "GET" ? "?limit=200" : ""}`;
    const token = tokenFile ? (await readFile(tokenFile, "utf8")).trim() : "";
    const ca = caFile ? await readFile(caFile) : undefined;
    const data = body === undefined ? undefined : JSON.stringify(body);
    return new Promise((resolve, reject) => {
      const request = (base.protocol === "https:" ? https : http).request(
        new URL(path, base),
        {
          method,
          ca,
          headers: {
            accept: "application/json",
            ...(token ? { authorization: `Bearer ${token}` } : {}),
            ...(data
              ? {
                  "content-type":
                    method === "PATCH" ? "application/merge-patch+json" : "application/json",
                  "content-length": Buffer.byteLength(data),
                }
              : {}),
          },
        },
        (response) => {
          const chunks = [];
          let size = 0;
          response.on("data", (chunk) => {
            size += chunk.length;
            if (size > 4 * 1024 * 1024)
              request.destroy(
                new ConsoleError(502, "Kubernetes response exceeds the console limit"),
              );
            else chunks.push(chunk);
          });
          response.on("error", reject);
          response.on("end", () => {
            if (response.statusCode < 200 || response.statusCode >= 300) {
              const status = [400, 403, 404, 409, 422, 429].includes(response.statusCode)
                ? response.statusCode
                : 502;
              // Never return upstream response bodies, endpoints, or credentials to clients.
              reject(
                new ConsoleError(
                  status,
                  `Kubernetes rejected the request (${response.statusCode})`,
                ),
              );
              return;
            }
            try {
              resolve(JSON.parse(Buffer.concat(chunks).toString("utf8")));
            } catch {
              reject(new ConsoleError(502, "Kubernetes returned invalid JSON"));
            }
          });
        },
      );
      const deadline = setTimeout(
        () => request.destroy(new ConsoleError(504, "Kubernetes request timed out")),
        timeout,
      );
      request.on("close", () => clearTimeout(deadline));
      request.on("error", (error) =>
        reject(
          error instanceof ConsoleError
            ? error
            : new ConsoleError(502, "Kubernetes is unavailable"),
        ),
      );
      request.end(data);
    });
  };
}
