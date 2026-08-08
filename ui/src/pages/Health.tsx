import { useEffect, useState } from "react";
import { api } from "../api";

// The question an operator asks first: is anything broken. One answer at the
// top, then the parts that make it up.
//
// Each component states a fact next to its colour. A board of green squares
// tells you nothing when it turns amber - you still have to go and find out
// what "amber" meant.

type Component = {
  name: string;
  state: "ok" | "warn" | "bad" | "off";
  headline: string;
  detail: string;
  // What a card reveals when opened: the numbers you would otherwise fetch by
  // hand. Several components have no page of their own, and inventing a link
  // that goes nowhere is worse than none - so the detail comes to the card.
  facts: [string, string][];
  // How this component's status was established. "Three of three healthy"
  // invites exactly one question - checked by what? - and a status page that
  // cannot answer it is asking to be believed rather than read.
  checkedBy: string;
  goTo?: { page: string; label: string };
};

const LABELS: Record<Component["state"], string> = {
  ok: "Operational",
  warn: "Degraded",
  bad: "Failed",
  off: "Not configured",
};

export default function Health({ onNavigate }: { onNavigate?: (page: any) => void }) {
  const [status, setStatus] = useState<any>(null);
  const [validation, setValidation] = useState<any>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      try {
        const s = await api.status();
        if (cancelled) return;
        setStatus(s);
        setError("");
      } catch (err: any) {
        if (!cancelled) setError(err.message || "failed to load");
      }
      try {
        const v = await api.validation();
        if (!cancelled) setValidation(v);
      } catch {
        // The validation result is optional: a deployment that does not run the
        // suite is not broken, it just has one fewer signal.
      }
    };
    load();
    const handle = window.setInterval(load, 10000);
    return () => {
      cancelled = true;
      window.clearInterval(handle);
    };
  }, []);

  const components = buildComponents(status, validation);
  const failing = components.filter((c) => c.state === "bad");
  const degraded = components.filter((c) => c.state === "warn");
  const overall: Component["state"] = failing.length ? "bad" : degraded.length ? "warn" : "ok";

  const headline =
    overall === "ok"
      ? "All systems operational"
      : overall === "warn"
        ? `${degraded.length} component${degraded.length === 1 ? "" : "s"} degraded`
        : `${failing.length} component${failing.length === 1 ? "" : "s"} failed`;

  return (
    <>
      {error ? <div className="callout">⚠ {error}</div> : null}

      <section className={`health-banner state-${overall}`}>
        <div className="health-banner-mark" aria-hidden="true" />
        <div>
          <h2>{headline}</h2>
          {/* The count alone provoked exactly one question - checked how? -
              and hid the rest: components this account cannot see were counted
              out of the total, so "3 of 3" was true and said nothing. The
              summary now names what was checked and what was not visible, and
              the evidence for each is on its card without a click. */}
          <p>
            {components.filter((c) => c.state === "ok").length} of{" "}
            {components.filter((c) => c.state !== "off").length} healthy
            {components.filter((c) => c.state === "off").length > 0
              ? `, ${components.filter((c) => c.state === "off").length} not visible to this account`
              : ""}
            {status?.version ? ` · Promtact ${status.version}` : ""}
            {status?.uptime_seconds != null ? ` · up ${formatUptime(status.uptime_seconds)}` : ""}
          </p>
          <p className="health-banner-note">
            Checked just now against the running service: every audit record
            re-hashed and compared with the next, the head fetched back from the
            external witness, the last write to storage and the last delivery to
            each connector. Each card states what established its own status.
          </p>
        </div>
      </section>

      {/* Components this account cannot see are left out rather than shown as
          grey cards saying so. Three dead tiles beside the live ones read as a
          broken deployment, and the reader has no way to tell "you lack the
          role" from "this is not working". */}
      <div className="health-grid">
        {components
          .filter((component) => component.headline !== "Not visible to this account")
          .map((component) => (
            <HealthCard key={component.name} component={component} onNavigate={onNavigate} />
          ))}
      </div>

      <p className="health-foot">
        Refreshed every 10 seconds. This page reports what the service can see about itself;
        whether it is reachable from the internet is checked separately, from outside.
      </p>
    </>
  );
}

function HealthCard({ component, onNavigate }: {
  component: Component; onNavigate?: (page: any) => void;
}) {
  const [open, setOpen] = useState(false);
  const id = `health-${component.name.replace(/\s+/g, "-").toLowerCase()}`;

  return (
    <article className={`health-card state-${component.state} ${open ? "is-open" : ""}`}>
      <button
        className="health-card-toggle"
        aria-expanded={open}
        aria-controls={id}
        onClick={() => setOpen((v) => !v)}
      >
        <span className="health-dot" aria-hidden="true" />
        <span className="health-card-title">
          <span className="health-name">{component.name}</span>
          <span className="health-headline">{component.headline}</span>
          <span className="health-detail">{component.detail}</span>
          <span className="health-basis">{component.checkedBy}</span>
        </span>
        <span className="health-state">{LABELS[component.state]}</span>
        <span className="health-chevron" aria-hidden="true">{open ? "−" : "+"}</span>
      </button>

      {open ? (
        <div className="health-facts" id={id}>
          {/* First, because it is the question a status page provokes and
              usually cannot answer. */}
          <dl>
            {component.facts.map(([label, value]) => (
              <div key={label}>
                <dt>{label}</dt>
                <dd>{value}</dd>
              </div>
            ))}
          </dl>
          {component.goTo && onNavigate ? (
            <button className="health-go" onClick={() => onNavigate(component.goTo!.page)}>
              {component.goTo.label} →
            </button>
          ) : null}
        </div>
      ) : null}
    </article>
  );
}

function buildComponents(status: any, validation: any): Component[] {
  const a = status?.assurance;
  const list: Component[] = [];

  list.push({
    name: "Enforcement gateway",
    checkedBy: "The service answered on its own address and reported its limiter, journal depth and decision latency.",
    state: !status ? "bad" : a?.degraded_mode ? "warn" : "ok",
    headline: !status
      ? "Unreachable"
      : a?.degraded_mode
        ? "Deciding, but not persisting"
        : "Deciding and persisting",
    detail: a?.degraded_mode
      ? `${(a.journal_depth ?? 0).toLocaleString()} decisions held in the local journal`
      : `${(status?.gateway_limit ?? 0).toLocaleString()} concurrent decisions permitted`,
    facts: [
      ["Decisions in flight", `${status?.gateway_inflight ?? 0} of ${status?.gateway_limit ?? 0}`],
      ["Rejected by backpressure", num(status?.gateway_rejected)],
      ["Decision latency, p99", status?.gateway_p99_millis != null ? `${status.gateway_p99_millis} ms` : "-"],
      ["Journal depth", num(a?.journal_depth)],
      ["Instance", status?.instance_name || "-"],
    ],
    goTo: { page: "overview", label: "See the decision funnel" },
  });

  list.push({
    name: "Storage",
    checkedBy: "The last write attempt and the schema version the store reports having applied.",
    state: !status ? "bad" : status.last_storage_error ? "bad" : "ok",
    headline: status?.last_storage_error ? "Write failing" : status?.storage_mode || "unknown",
    detail: status?.last_storage_error
      ? String(status.last_storage_error)
      : `schema version ${status?.schema_version ?? "-"}`,
    facts: [
      ["Mode", status?.storage_mode || "-"],
      ["Schema version", String(status?.schema_version ?? "-")],
      ["Tenant isolation", status?.tenant_isolation || "-"],
      ["Last write error", status?.last_storage_error || "none"],
    ],
  });

  list.push({
    name: "Audit chain",
    checkedBy: "Every record re-hashed and compared with the hash stored on the record after it.",
    state: !a ? "off" : a.audit_chain_valid ? "ok" : "bad",
    headline: !a ? "Not visible to this account" : a.audit_chain_valid ? "Valid" : "Broken",
    detail: a ? `${a.audit_chain_index.toLocaleString()} records linked` : "platform operator only",
    facts: [
      ["Records linked", num(a?.audit_chain_index)],
      ["Head", a?.audit_chain_head ? String(a.audit_chain_head).slice(0, 24) + "…" : "-"],
      ["Validates", a?.audit_chain_valid ? "yes" : "no"],
    ],
  });

  list.push({
    name: "External witness",
    checkedBy: "The head published to the witness, fetched back and compared with the head this server holds.",
    state: !a ? "off" : !a.witness_configured ? "off" : a.witness_diverged ? "bad" : "ok",
    headline: !a
      ? "Not visible to this account"
      : !a.witness_configured
        ? "Not configured"
        : a.witness_diverged
          ? "Disagrees with local history"
          : "Agrees with local history",
    detail:
      a?.witness_configured
        ? `witnessed at index ${a.witness_index.toLocaleString()}`
        : "history is anchored locally only, which does not protect against an operator",
    facts: [
      ["Witnessed index", a?.witness_configured ? num(a.witness_index) : "-"],
      ["Local index", num(a?.audit_chain_index)],
      ["Agreement", !a?.witness_configured ? "no witness" : a.witness_diverged ? "diverged" : "agrees"],
      ["Protects against", "history being rewritten or truncated by someone with host access"],
    ],
  });

  list.push({
    name: "Operator access reporting",
    checkedBy: "Database sessions observed on the host, reconciled against announced break-glass windows.",
    state: !a ? "off" : a.shipper_silent ? "warn" : a.unannounced_sessions > 0 ? "warn" : "ok",
    headline: !a
      ? "Not visible to this account"
      : a.shipper_silent
        ? "The session reporter has gone quiet"
        : a.unannounced_sessions > 0
          ? `${a.unannounced_sessions} unannounced session${a.unannounced_sessions === 1 ? "" : "s"}`
          : "Every session announced",
    detail: a?.shipper_silent
      ? "database sessions are no longer being observed"
      : "reconciled against break-glass windows",
    facts: [
      ["Unannounced sessions", num(a?.unannounced_sessions)],
      ["Reporter last seen", a?.shipper_last_seen ? new Date(a.shipper_last_seen).toLocaleString() : "never"],
      ["Reporting", a?.shipper_silent ? "silent" : "active"],
      ["Announce access with", "promtactl breakglass --reason \"…\""],
    ],
  });

  if (validation) {
    const passed = validation.passed ?? 0;
    const total = validation.total ?? 0;
    const clean = total > 0 && passed === total;
    list.push({
      name: "Detection validation",
    checkedBy: "The last run of the benign emulation suite and how many of its techniques were caught.",
      state: total === 0 ? "off" : clean ? "ok" : "bad",
      headline: total === 0 ? "Never run" : `${passed} of ${total} techniques held`,
      detail:
        (validation.missed ?? 0) > 0 || (validation.false_positives ?? 0) > 0
          ? `${validation.missed ?? 0} missed, ${validation.false_positives ?? 0} false positives`
          : validation.suite_version || "benign ATT&CK emulation suite",
      facts: [
        ["Techniques held", `${passed} of ${total}`],
        ["Missed", num(validation.missed)],
        ["False positives", num(validation.false_positives)],
        ["Suite", validation.suite_version || "-"],
      ],
      goTo: { page: "detections", label: "See technique coverage" },
    });
  }


  // The four below are specific to this product rather than to running a
  // service. Postgres and webhooks are operations; these are the parts whose
  // failure makes the gateway stop being a control while it still answers.

  list.push({
    name: "Policy",
    state: !a ? "off" : a.policy_loaded ? "ok" : "warn",
    headline: !a
      ? "Not visible to this account"
      : a.policy_loaded
        ? `${a.approved_tools ?? 0} tools approved`
        : "Running on built-in defaults",
    detail: a?.policy_loaded
      ? `${a.approved_egress ?? 0} destinations approved`
      : "no policy file loaded, so the defaults apply",
    checkedBy:
      "The policy file was read and parsed just now. A policy that fails to load falls back to built-in defaults, which is a different policy than the one written and looks identical from outside.",
    facts: [
      ["Approved tools", num(a?.approved_tools)],
      ["Approved destinations", num(a?.approved_egress)],
      ["Policy file", a?.policy_path || "none"],
    ],
    goTo: { page: "policy", label: "Open the policy" },
  });

  list.push({
    name: "Session marks",
    state: !a ? "off" : a.session_mark_error ? "bad" : a.session_marks_durable ? "ok" : "warn",
    headline: !a
      ? "Not visible to this account"
      : a.session_mark_error
        ? "Not being written"
        : a.session_marks_durable
          ? "Surviving a restart"
          : "In memory only",
    detail: a?.session_mark_error
      ? String(a.session_mark_error)
      : a?.session_marks_durable
        ? "an action following untrusted content still needs a person after a restart"
        : "a restart releases every marked session at once",
    checkedBy:
      "The last attempt to write a session mark to durable storage. This is the control that holds an action after an agent has read untrusted content; if it stops being written nothing fails, and the next restart releases every marked session.",
    facts: [
      ["Durable", a?.session_marks_durable ? "yes" : "no"],
      ["Last write error", a?.session_mark_error || "none"],
    ],
  });

  list.push({
    name: "Waiting for a person",
    state: !a ? "off" : (a.approvals_waiting ?? 0) > 0 ? "warn" : "ok",
    headline: !a
      ? "Not visible to this account"
      : (a.approvals_waiting ?? 0) > 0
        ? `${a.approvals_waiting} held`
        : "Nothing held",
    detail:
      (a?.approvals_waiting ?? 0) > 0
        ? "agents are stopped mid-task until somebody decides"
        : "no agent is blocked on a decision",
    checkedBy:
      "Counted from the response actions this tenant holds. A queue nobody watches is an outage that reports itself as healthy: the gateway is fine and the agent is not moving.",
    facts: [["Held for approval", num(a?.approvals_waiting)]],
    goTo: { page: "approvals", label: "See what is waiting" },
  });

  list.push({
    name: "Tool server",
    state: !a ? "off" : a.mcp_upstream ? "ok" : "warn",
    headline: !a ? "Not visible to this account" : a.mcp_upstream ? "Configured" : "Not configured",
    detail: a?.mcp_upstream || "the MCP path answers nothing without an upstream",
    checkedBy:
      "The upstream address this gateway forwards approved tool calls to, as configured at startup. Configured is not the same as reachable, and this does not claim otherwise - a failed forward appears as an error on the call itself.",
    facts: [["Upstream", a?.mcp_upstream || "none"]],
  });

  list.push({
    name: "Alert delivery",
    checkedBy: "The last delivery attempt to each configured webhook and connector.",
    state: status?.last_export_error ? "warn" : "ok",
    headline: status?.last_export_error ? "Last export failed" : "No delivery failures",
    detail: status?.last_export_error
      ? String(status.last_export_error)
      : "webhook and connectors reporting cleanly",
    facts: [
      ["Last export error", status?.last_export_error || "none"],
      ["Public URL", status?.public_url || "-"],
    ],
    goTo: { page: "alerts", label: "See open alerts" },
  });

  return list;
}

function num(value: any): string {
  return typeof value === "number" ? value.toLocaleString() : "-";
}

function formatUptime(seconds: number): string {
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${minutes}m`;
  return `${minutes}m`;
}
