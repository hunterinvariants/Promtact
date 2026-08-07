import { useEffect, useState } from "react";
import { api } from "../api";

// The question an operator asks first: is anything broken. One answer at the
// top, then the parts that make it up.
//
// Each component states a fact next to its colour. A board of green squares
// tells you nothing when it turns amber — you still have to go and find out
// what "amber" meant.

type Component = {
  name: string;
  state: "ok" | "warn" | "bad" | "off";
  headline: string;
  detail: string;
};

const LABELS: Record<Component["state"], string> = {
  ok: "Operational",
  warn: "Degraded",
  bad: "Failed",
  off: "Not configured",
};

export default function Health() {
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
          <p>
            {components.filter((c) => c.state === "ok").length} of{" "}
            {components.filter((c) => c.state !== "off").length} checked components healthy
            {status?.version ? ` · Promtact ${status.version}` : ""}
            {status?.uptime_seconds != null ? ` · up ${formatUptime(status.uptime_seconds)}` : ""}
          </p>
        </div>
      </section>

      <div className="health-grid">
        {components.map((component) => (
          <article key={component.name} className={`health-card state-${component.state}`}>
            <header>
              <span className="health-dot" aria-hidden="true" />
              <h3>{component.name}</h3>
              <span className="health-state">{LABELS[component.state]}</span>
            </header>
            <p className="health-headline">{component.headline}</p>
            <p className="health-detail">{component.detail}</p>
          </article>
        ))}
      </div>

      <p className="health-foot">
        Refreshed every 10 seconds. This page reports what the service can see about itself;
        whether it is reachable from the internet is checked separately, from outside.
      </p>
    </>
  );
}

function buildComponents(status: any, validation: any): Component[] {
  const a = status?.assurance;
  const list: Component[] = [];

  list.push({
    name: "Enforcement gateway",
    state: !status ? "bad" : a?.degraded_mode ? "warn" : "ok",
    headline: !status
      ? "Unreachable"
      : a?.degraded_mode
        ? "Deciding, but not persisting"
        : "Deciding and persisting",
    detail: a?.degraded_mode
      ? `${(a.journal_depth ?? 0).toLocaleString()} decisions held in the local journal`
      : `${(status?.gateway_limit ?? 0).toLocaleString()} concurrent decisions permitted`,
  });

  list.push({
    name: "Storage",
    state: !status ? "bad" : status.last_storage_error ? "bad" : "ok",
    headline: status?.last_storage_error ? "Write failing" : status?.storage_mode || "unknown",
    detail: status?.last_storage_error
      ? String(status.last_storage_error)
      : `schema version ${status?.schema_version ?? "—"}`,
  });

  list.push({
    name: "Audit chain",
    state: !a ? "off" : a.audit_chain_valid ? "ok" : "bad",
    headline: !a ? "Not visible to this account" : a.audit_chain_valid ? "Valid" : "Broken",
    detail: a ? `${a.audit_chain_index.toLocaleString()} records linked` : "platform operator only",
  });

  list.push({
    name: "External witness",
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
  });

  list.push({
    name: "Operator access reporting",
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
  });

  if (validation) {
    const passed = validation.passed ?? 0;
    const total = validation.total ?? 0;
    const clean = total > 0 && passed === total;
    list.push({
      name: "Detection validation",
      state: total === 0 ? "off" : clean ? "ok" : "bad",
      headline: total === 0 ? "Never run" : `${passed} of ${total} techniques held`,
      detail:
        (validation.missed ?? 0) > 0 || (validation.false_positives ?? 0) > 0
          ? `${validation.missed ?? 0} missed, ${validation.false_positives ?? 0} false positives`
          : validation.suite_version || "benign ATT&CK emulation suite",
    });
  }

  list.push({
    name: "Alert delivery",
    state: status?.last_export_error ? "warn" : "ok",
    headline: status?.last_export_error ? "Last export failed" : "No delivery failures",
    detail: status?.last_export_error
      ? String(status.last_export_error)
      : "webhook and connectors reporting cleanly",
  });

  return list;
}

function formatUptime(seconds: number): string {
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${minutes}m`;
  return `${minutes}m`;
}
