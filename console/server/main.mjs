import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { createConsole } from "./app.mjs";
import { kubernetesClient } from "./kubernetes.mjs";
import { demo } from "./demo.mjs";

const env = process.env;
const host = env.ANVIL_CONSOLE_HOST || "127.0.0.1";
const accessToken = env.ANVIL_CONSOLE_TOKEN_FILE
  ? (await readFile(env.ANVIL_CONSOLE_TOKEN_FILE, "utf8")).trim()
  : "";
if (accessToken && accessToken.length < 32)
  throw new Error("Use a console token of at least 32 characters");
if (!["127.0.0.1", "::1", "localhost"].includes(host) && !accessToken)
  throw new Error("Remote binding requires a console token");
const preview = env.ANVIL_CONSOLE_DEMO === "1";
const namespace = env.ANVIL_NAMESPACE || (preview ? "anvil-demo" : "");
const inCluster = env.KUBERNETES_SERVICE_HOST;
const url =
  env.ANVIL_KUBE_API ||
  (inCluster
    ? `https://${inCluster.includes(":") ? `[${inCluster}]` : inCluster}:${env.KUBERNETES_SERVICE_PORT_HTTPS || "443"}`
    : "");
const serviceAccount = "/var/run/secrets/kubernetes.io/serviceaccount/";
const kube =
  !preview && url
    ? kubernetesClient({
        url,
        namespace,
        tokenFile: env.ANVIL_KUBE_TOKEN_FILE || (inCluster ? serviceAccount + "token" : undefined),
        caFile: env.ANVIL_KUBE_CA_FILE || (inCluster ? serviceAccount + "ca.crt" : undefined),
      })
    : undefined;
const server = createConsole({
  namespace,
  kube,
  accessToken,
  allowWrites: env.ANVIL_CONSOLE_WRITES === "1",
  demo: preview ? demo : null,
  dist: fileURLToPath(new URL("../dist", import.meta.url)),
});
server.listen(Number(env.PORT || 8081), host, () => console.log("Anvil console is listening"));
for (const signal of ["SIGINT", "SIGTERM"])
  process.on(signal, () => {
    server.close();
    setTimeout(() => process.exit(0), 5000).unref();
  });
