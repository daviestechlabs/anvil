import React, { useCallback, useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  Activity,
  ArrowUpRight,
  Boxes,
  CheckCircle2,
  ChevronRight,
  FileCode2,
  FlaskConical,
  Layers3,
  Menu,
  Play,
  RefreshCw,
  ShieldCheck,
  Square,
  X,
} from "lucide-react";
import { AnvilMark } from "@shared/mark";
import "./console.css";

type Resource = {
  metadata: { name: string; uid?: string; resourceVersion?: string; creationTimestamp?: string };
  spec?: any;
  status?: any;
};
type Snapshot = {
  namespace: string;
  connected: boolean;
  demo: boolean;
  allowWrites: boolean;
  collections: Record<string, { items: Resource[]; truncated?: boolean }>;
  errors: Record<string, string>;
};
const pages = [
  { id: "overview", label: "Overview", icon: Boxes, group: "Workspace" },
  { id: "trainingrecipes", label: "Recipes", icon: FileCode2, group: "Workspace" },
  { id: "trainingruns", label: "Runs", icon: Activity, group: "Workspace" },
  { id: "trainingartifacts", label: "Artifacts", icon: Layers3, group: "Evidence" },
  { id: "evaluationruns", label: "Evaluations", icon: FlaskConical, group: "Evidence" },
  { id: "modelpromotions", label: "Promotions", icon: ShieldCheck, group: "Evidence" },
];
const terminal = new Set(["Succeeded", "Failed", "Cancelled", "Rejected"]);
function phase(item: Resource) {
  return (
    item.status?.phase ||
    (item.spec?.enabled !== undefined ? (item.spec.enabled ? "Enabled" : "Disabled") : "Pending")
  );
}
function Badge({ value }: { value: string }) {
  return (
    <span
      className={`badge ${["Succeeded", "Verified", "Approved", "Enabled"].includes(value) ? "good" : ["Failed", "Rejected"].includes(value) ? "bad" : ""}`}
    >
      {value}
    </span>
  );
}
function Progress({ item }: { item: Resource }) {
  const completed = item.status?.progress?.completed;
  const total = item.status?.progress?.total;
  const measured = Number.isFinite(completed) && Number.isFinite(total) && total > 0;
  return (
    <div className="progress-cell">
      {measured ? (
        <>
          <progress
            aria-label={`${item.metadata.name} progress`}
            max={total}
            value={Math.min(Math.max(0, completed), total)}
          />
          <span>
            {completed} / {total} steps
          </span>
        </>
      ) : (
        <span className="muted">
          {terminal.has(phase(item)) ? "Finished" : "Waiting for workflow progress"}
        </span>
      )}
    </div>
  );
}
function App() {
  const [page, setPage] = useState(() => location.hash.slice(1) || "overview");
  const [token, setToken] = useState("");
  const [tokenDraft, setTokenDraft] = useState("");
  const currentToken = useRef(token);
  currentToken.current = token;
  const observation = useRef(0);
  const [snapshot, setSnapshot] = useState<Snapshot>();
  const [error, setError] = useState("");
  const [unauthorized, setUnauthorized] = useState(false);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");
  const [drawer, setDrawer] = useState(false);
  const opener = useRef<HTMLElement | null>(null);
  const [detail, setDetail] = useState<Resource>();
  const [creating, setCreating] = useState(false);
  const [cancelling, setCancelling] = useState<Resource>();
  const dialog = useRef<HTMLDialogElement>(null);
  const modalOpener = useRef<HTMLElement | null>(null);
  const drawerRef = useRef<HTMLDialogElement>(null);
  const request = useCallback(
    async (path: string, body?: unknown) => {
      const response = await fetch(path, {
        method: body === undefined ? "GET" : "POST",
        headers: {
          ...(token ? { authorization: `Bearer ${token}` } : {}),
          ...(body === undefined ? {} : { "content-type": "application/json" }),
        },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
        signal: AbortSignal.timeout(12000),
      });
      const result = await response.json();
      if (response.status === 401 && currentToken.current === token) {
        setUnauthorized(true);
        setSnapshot(undefined);
      }
      if (!response.ok) throw new Error(result.error || "Console request failed");
      return result;
    },
    [token],
  );
  const refresh = useCallback(async () => {
    const sequence = ++observation.current;
    setLoading(true);
    const current = () => currentToken.current === token && sequence === observation.current;
    try {
      const result = await request("/api/console");
      if (current()) {
        setSnapshot(result);
        setUnauthorized(false);
        setError("");
      }
    } catch (error) {
      if (current())
        setError(error instanceof Error ? error.message : "Could not load the console");
    } finally {
      if (current()) setLoading(false);
    }
  }, [request, token]);
  useEffect(() => {
    void refresh();
    const interval = setInterval(() => {
      if (document.visibilityState === "visible") void refresh();
    }, 10000);
    return () => clearInterval(interval);
  }, [refresh]);
  useEffect(() => {
    const change = () => setPage(location.hash.slice(1) || "overview");
    window.addEventListener("hashchange", change);
    return () => window.removeEventListener("hashchange", change);
  }, []);
  useEffect(() => {
    if (drawer) drawerRef.current?.showModal();
    else drawerRef.current?.close();
  }, [drawer]);
  const modal = Boolean(detail || creating || cancelling);
  useEffect(() => {
    if (modal) dialog.current?.showModal();
    else dialog.current?.close();
  }, [modal]);
  const close = () => {
    setDetail(undefined);
    setCreating(false);
    setCancelling(undefined);
    modalOpener.current?.focus();
  };
  const open = (action: () => void) => {
    modalOpener.current = document.activeElement as HTMLElement;
    setNotice("");
    action();
  };
  const closeDrawer = () => {
    setDrawer(false);
    opener.current?.focus();
  };
  const items = (key: string) => snapshot?.collections[key]?.items || [];
  const selected = pages.find((item) => item.id === page) || pages[0];
  const runs = [...items("trainingruns")].sort((a, b) =>
    (b.metadata.creationTimestamp || "").localeCompare(a.metadata.creationTimestamp || ""),
  );
  const active = runs.filter((run) => !terminal.has(phase(run)));
  const errors = snapshot ? Object.entries(snapshot.errors) : [];
  const navigate = (id: string) => {
    location.hash = id;
    closeDrawer();
  };
  const nav = (
    <>
      {["Workspace", "Evidence"].map((group) => (
        <section key={group}>
          <p className="nav-label">{group}</p>
          {pages
            .filter((item) => item.group === group)
            .map((item) => (
              <a
                key={item.id}
                href={`#${item.id}`}
                aria-current={selected.id === item.id ? "page" : undefined}
                onClick={() => closeDrawer()}
              >
                <item.icon size={17} />
                <span>{item.label}</span>
                {selected.id === item.id && <ChevronRight size={14} />}
              </a>
            ))}
        </section>
      ))}
    </>
  );
  const table = (key: string, values = items(key)) => (
    <div className="resource-list">
      {snapshot?.errors[key] ? (
        <p role="alert">
          {snapshot.errors[key]} <button onClick={() => void refresh()}>Retry</button>
        </p>
      ) : values.length ? (
        values.map((item) => (
          <article className="resource-row" key={item.metadata.uid || item.metadata.name}>
            <div className="resource-title">
              <button className="text-button" onClick={() => open(() => setDetail(item))}>
                {item.metadata.name}
                <ArrowUpRight size={14} />
              </button>
              <p className="muted">
                {key === "trainingruns"
                  ? item.spec?.request?.recipeRef?.name
                  : key === "trainingrecipes"
                    ? item.spec?.binding?.workflowTemplate
                    : key === "trainingartifacts"
                      ? item.spec?.sourceRun?.name
                      : key === "modelpromotions"
                        ? item.spec?.artifactRef?.name
                        : item.spec?.artifactRef?.name || "Kubernetes resource"}
              </p>
            </div>
            <Badge value={phase(item)} />
            {key === "trainingruns" && <Progress item={item} />}
            {key === "trainingruns" && snapshot?.allowWrites && !terminal.has(phase(item)) && (
              <button className="subtle" onClick={() => open(() => setCancelling(item))}>
                <Square size={13} />
                Cancel
              </button>
            )}
          </article>
        ))
      ) : (
        <div className="empty">
          <selected.icon size={24} />
          <p>No {pages.find((item) => item.id === key)?.label.toLowerCase() || "resources"} yet.</p>
          <p className="muted">Resources appear here when they exist in this namespace.</p>
        </div>
      )}
      {snapshot?.collections[key]?.truncated && (
        <p className="warning">
          Showing the first 200 resources. Use kubectl for the complete list.
        </p>
      )}
    </div>
  );
  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setNotice("");
    try {
      const data = new FormData(event.currentTarget);
      const result = await request("/api/trainingruns", {
        name: data.get("name"),
        recipe: data.get("recipe"),
        bindingSHA256: data.get("bindingSHA256"),
        durationSeconds: Number(data.get("durationSeconds")),
        parameters: JSON.parse(String(data.get("parameters"))),
      });
      close();
      navigate("trainingruns");
      await refresh();
      setNotice(
        `Run ${result.name} was submitted. The operator validates its reviewed binding before starting Argo.`,
      );
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "Run submission failed");
    } finally {
      setBusy(false);
    }
  }
  async function cancel() {
    if (!cancelling) return;
    setBusy(true);
    setNotice("");
    try {
      await request(`/api/trainingruns/${cancelling.metadata.name}/cancel`, {
        uid: cancelling.metadata.uid,
        resourceVersion: cancelling.metadata.resourceVersion,
      });
      close();
      await refresh();
      setNotice("Cancellation requested. The operator updates the run after workflow termination.");
    } catch (error) {
      setNotice(error instanceof Error ? error.message : "Cancellation failed");
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="anvil-console console-shell">
      <a
        className="skip-link"
        href="#main"
        onClick={(event) => {
          event.preventDefault();
          document.getElementById("main")?.focus();
        }}
      >
        Skip to content
      </a>
      <aside className="sidebar">
        <a className="brand" href="#overview">
          <AnvilMark className="mark" />
          <span>Anvil</span>
          <span className="alpha">alpha</span>
        </a>
        <nav aria-label="Main navigation">{nav}</nav>
        <footer>
          <span className="status-dot" />
          Kubernetes console<p>{snapshot?.namespace || "Connecting"}</p>
        </footer>
      </aside>
      <div className="workspace">
        <header>
          <button
            className="mobile-menu subtle"
            aria-label="Open navigation"
            onClick={(event) => {
              opener.current = event.currentTarget;
              setDrawer(true);
            }}
          >
            <Menu size={20} />
          </button>
          <span>{selected.label}</span>
          <div className="header-actions">
            <span className="runtime-pill">
              <span className="status-dot" />
              {snapshot?.demo
                ? "Demo · synthetic data"
                : snapshot?.connected
                  ? snapshot.namespace
                  : "Not connected"}
            </span>
            {token && (
              <button
                className="subtle"
                onClick={() => {
                  setToken("");
                  setSnapshot(undefined);
                }}
              >
                Sign out
              </button>
            )}
            <button
              className="icon-button"
              aria-label="Refresh resources"
              disabled={loading}
              onClick={() => void refresh()}
            >
              <RefreshCw size={16} className={loading ? "spinning" : ""} />
            </button>
          </div>
        </header>
        <main id="main" tabIndex={-1}>
          {unauthorized ? (
            <section className="login card">
              <AnvilMark className="mark" />
              <h1>Open your workspace</h1>
              <p className="muted">
                Enter the console access token provided by your administrator.
              </p>
              <form
                onSubmit={(event) => {
                  event.preventDefault();
                  setToken(tokenDraft);
                  setTokenDraft("");
                }}
              >
                <label>
                  Console access token
                  <input
                    type="password"
                    autoComplete="off"
                    value={tokenDraft}
                    onChange={(event) => setTokenDraft(event.target.value)}
                    required
                  />
                </label>
                <button type="submit">
                  Connect
                  <ChevronRight size={16} />
                </button>
              </form>
            </section>
          ) : (
            <>
              <div className="page-heading">
                <div>
                  <p className="eyebrow">{snapshot?.namespace || "Anvil workspace"}</p>
                  <h1>{selected.label}</h1>
                  <p className="muted">
                    {page === "overview"
                      ? "Reviewed workflows, execution progress, and model evidence."
                      : page === "trainingrecipes"
                        ? "Workflow code and inputs reviewed before execution."
                        : page === "trainingruns"
                          ? "Runs submitted to the Anvil operator and Argo Workflows."
                          : page === "modelpromotions"
                            ? "Approval decisions. Model activation stays with your serving system."
                            : "Evidence recorded as Kubernetes resources."}
                  </p>
                </div>
                {snapshot?.allowWrites && ["overview", "trainingruns"].includes(page) && (
                  <button onClick={() => open(() => setCreating(true))}>
                    <Play size={15} />
                    New run
                  </button>
                )}
              </div>
              {error && (
                <div className="warning" role="alert">
                  {error} <button onClick={() => void refresh()}>Retry</button>
                  {snapshot && <span> Showing the last successful observation.</span>}
                </div>
              )}
              {notice && !modal && (
                <p role="status" className="notice">
                  {notice}
                </p>
              )}
              {!snapshot && loading ? (
                <div className="card empty" role="status">
                  Loading Kubernetes resources…
                </div>
              ) : !snapshot ? (
                <div className="card empty">No observation is available. Retry to connect.</div>
              ) : !snapshot.connected && !snapshot.demo ? (
                <div className="card empty">
                  <h2>Connect Kubernetes</h2>
                  <p className="muted">
                    Configure the console server with an API endpoint and an explicit namespace.
                  </p>
                  <p>The console guide describes credentials and permissions.</p>
                </div>
              ) : page === "overview" ? (
                <>
                  <div className="stats">
                    {[
                      ["Active runs", active.length, "trainingruns"],
                      [
                        "Reviewed recipes",
                        items("trainingrecipes").filter((item) => item.spec?.enabled).length,
                        "trainingrecipes",
                      ],
                      ["Artifacts", items("trainingartifacts").length, "trainingartifacts"],
                      ["Evaluations", items("evaluationruns").length, "evaluationruns"],
                    ].map(([label, count, key]) => (
                      <a className="card stat" key={key} href={`#${key}`}>
                        <span className="muted">{label}</span>
                        <strong>
                          {snapshot.errors[String(key)] ? "—" : count}
                          {snapshot.collections[String(key)]?.truncated ? "+" : ""}
                        </strong>
                        <ArrowUpRight size={15} />
                      </a>
                    ))}
                  </div>
                  {errors.length > 0 && (
                    <div className="warning" role="alert">
                      Some resource lists are unavailable. Open their pages for details.
                    </div>
                  )}
                  <section className="card">
                    <div className="section-heading">
                      <div>
                        <h2>Current activity</h2>
                        <p className="muted">
                          {active.length
                            ? "Progress reported by the operator."
                            : "No active runs in this observation."}
                        </p>
                      </div>
                      <a href="#trainingruns">
                        All runs
                        <ArrowUpRight size={14} />
                      </a>
                    </div>
                    {table("trainingruns", active.length ? active : runs.slice(0, 5))}
                  </section>
                  <div className="overview-bottom">
                    <section className="card">
                      <div className="section-heading">
                        <h2>Operator readiness</h2>
                        <ShieldCheck size={18} />
                      </div>
                      {snapshot.errors.anvils ? (
                        <p role="alert">{snapshot.errors.anvils}</p>
                      ) : items("anvils").length ? (
                        items("anvils").map((item) => (
                          <div key={item.metadata.name}>
                            <h3>{item.metadata.name}</h3>
                            {(item.status?.conditions || []).map((condition: any) => (
                              <p className="condition" key={condition.type}>
                                <Badge
                                  value={
                                    condition.status === "True"
                                      ? condition.type
                                      : `${condition.type}: ${condition.status}`
                                  }
                                />
                                <span className="muted">
                                  {condition.message || condition.reason}
                                </span>
                              </p>
                            ))}
                          </div>
                        ))
                      ) : (
                        <p className="muted">No Anvil installation resource is present.</p>
                      )}
                    </section>
                    <section className="card">
                      <div className="section-heading">
                        <h2>Evidence pipeline</h2>
                        <CheckCircle2 size={18} />
                      </div>
                      <p className="muted">Each stage records a separate Kubernetes identity.</p>
                      <div className="pipeline">
                        {["Recipes", "Runs", "Artifacts", "Evaluations", "Promotions"].map(
                          (label, index) => (
                            <React.Fragment key={label}>
                              <a href={`#${pages[index + 1].id}`}>{label}</a>
                              {index < 4 && <ChevronRight size={12} />}
                            </React.Fragment>
                          ),
                        )}
                      </div>
                      <p className="muted">Promotion approval uses a separate Kubernetes role.</p>
                    </section>
                  </div>
                </>
              ) : (
                <section className="card">
                  <div className="section-heading">
                    <h2>{selected.label}</h2>
                    <span className="muted">{items(selected.id).length} observed</span>
                  </div>
                  {table(selected.id, selected.id === "trainingruns" ? runs : items(selected.id))}
                </section>
              )}
            </>
          )}
        </main>
      </div>
      <nav className="mobile-bar" aria-label="Mobile navigation">
        {pages.slice(0, 3).map((item) => (
          <a
            key={item.id}
            href={`#${item.id}`}
            aria-current={selected.id === item.id ? "page" : undefined}
          >
            <item.icon size={19} />
            {item.label}
          </a>
        ))}
        <button
          onClick={(event) => {
            opener.current = event.currentTarget;
            setDrawer(true);
          }}
        >
          <Menu size={19} />
          More
        </button>
      </nav>
      <dialog
        className="drawer"
        ref={drawerRef}
        onCancel={closeDrawer}
        onClose={() => {
          setDrawer(false);
          opener.current?.focus();
        }}
        aria-label="Navigation"
      >
        <div className="section-heading">
          <span className="brand">
            <AnvilMark className="mark" />
            Anvil
          </span>
          <button className="icon-button" aria-label="Close navigation" onClick={closeDrawer}>
            <X size={20} />
          </button>
        </div>
        <nav>{nav}</nav>
      </dialog>
      <dialog
        className="modal"
        ref={dialog}
        onCancel={(event) => {
          if (busy) event.preventDefault();
          else close();
        }}
        onClose={close}
        aria-label={
          creating ? "Review and submit a run" : cancelling ? "Cancel run" : "Resource details"
        }
      >
        <div className="section-heading">
          <h2>
            {creating
              ? "Review and submit a run"
              : cancelling
                ? "Cancel run"
                : detail?.metadata.name}
          </h2>
          <button className="icon-button" aria-label="Close dialog" disabled={busy} onClick={close}>
            <X size={20} />
          </button>
        </div>
        {creating && (
          <form onSubmit={submit}>
            <p className="muted">
              The operator verifies the recipe binding before it submits an Argo workflow.
            </p>
            <label>
              Run name
              <input
                name="name"
                pattern="[a-z0-9][a-z0-9-]*"
                maxLength={63}
                placeholder="training-run-01"
                required
              />
            </label>
            <label>
              Reviewed recipe
              <select name="recipe" aria-label="Reviewed recipe" required>
                {items("trainingrecipes")
                  .filter((item) => item.spec?.enabled)
                  .map((item) => (
                    <option key={item.metadata.name}>{item.metadata.name}</option>
                  ))}
              </select>
            </label>
            <label>
              Recipe binding SHA-256
              <input name="bindingSHA256" pattern="[a-f0-9]{64}" maxLength={64} required />
            </label>
            <p className="muted helper">
              Get this digest with <code>anvilctl recipe-hash recipe.yaml</code>.
            </p>
            <label>
              Maximum duration (seconds)
              <input
                name="durationSeconds"
                type="number"
                min={1}
                max={604800}
                defaultValue={120}
                required
              />
            </label>
            <label>
              Parameters (JSON string values)
              <textarea
                name="parameters"
                aria-label="Parameters (JSON string values)"
                defaultValue="{}"
                rows={3}
                required
              />
            </label>
            <label className="checkbox">
              <input type="checkbox" required />I reviewed the recipe, digest, duration, and inputs.
            </label>
            {notice && (
              <p role="alert" className="warning">
                {notice}
              </p>
            )}
            <button
              type="submit"
              disabled={busy || !items("trainingrecipes").some((item) => item.spec?.enabled)}
            >
              {busy ? "Submitting…" : "Submit reviewed run"}
              <Play size={14} />
            </button>
          </form>
        )}
        {cancelling && (
          <>
            <p>
              Request cancellation of <strong>{cancelling.metadata.name}</strong>?
            </p>
            <p className="muted">
              The operator terminates its workflow. This action can stop unfinished work.
            </p>
            {notice && (
              <p className="warning" role="alert">
                {notice}
              </p>
            )}
            <button disabled={busy} onClick={() => void cancel()}>
              {busy ? "Requesting…" : "Request cancellation"}
            </button>
          </>
        )}
        {detail && (
          <>
            <p className="muted">
              Observed Kubernetes resource. Refresh the page to load current state.
            </p>
            <pre>{JSON.stringify(detail, null, 2)}</pre>
          </>
        )}
      </dialog>
    </div>
  );
}
createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
