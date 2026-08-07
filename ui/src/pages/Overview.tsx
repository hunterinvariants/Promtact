import { useEffect, useState } from "react";
import { api } from "../api";
import { Badge, Empty, Panel, severityTone } from "../components";

// The overview answers two questions in order: what did enforcement do, and can
// the record of it be trusted. Everything else is secondary and sized that way.
//
// The decision funnel is the product in one picture. A dashboard of equally
// weighted tiles makes a reader hunt for the point; this states it.

type Assurance = {
  decisions_allowed: number;
  decisions_gated: number;
  decisions_denied: number;
  decisions_total: number;
  audit_chain_valid: boolean;
  audit_chain_index: number;
  witness_configured: boolean;
  witness_index: number;
  witness_diverged: boolean;
  degraded_mode: boolean;
  journal_depth: number;
  unannounced_sessions: number;
  shipper_silent: boolean;
};

export default function Overview() {
  const [status, setStatus] = useState<any>(null);
  const [alerts, setAlerts] = useState<any[]>([]);
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      try {
        const [s, a] = await Promise.all([api.status(), api.alerts()]);
        if (cancelled) return;
        setStatus(s);
        setAlerts(a || []);
        setError("");
      } catch (err: any) {
        if (!cancelled) setError(err.message || "failed to load");
      }
    };
    load();
    const handle = window.setInterval(load, 10000);
    return () => {
      cancelled = true;
      window.clearInterval(handle);
    };
  }, []);

  const openAlerts = alerts.filter((a) => (a.status || "open") === "open");
  const critical = openAlerts.filter((a) =>
    ["critical", "high"].includes((a.severity || "").toLowerCase())
  );
  const assurance: Assurance | null = status?.assurance ?? null;

  return (
    <>
      {error ? <div className="callout">⚠ {error}</div> : null}

      {assurance ? <Funnel assurance={assurance} /> : null}
      {assurance ? <AssuranceStrip assurance={assurance} /> : null}

      <div className="grid grid-secondary">
        <Secondary label="Open alerts" value={openAlerts.length}
          hint={`${critical.length} high or critical`} tone={critical.length ? "warn" : "calm"} />
        <Secondary label="Assets" value={status?.asset_count ?? "—"} hint="seen in this tenant" />
        <Secondary label="Events" value={status?.event_count ?? "—"} hint="ingested" />
        <Secondary label="Response actions" value={status?.action_count ?? "—"} hint="planned or executed" />
        <Secondary label="Audit records" value={status?.audit_count ?? "—"} hint="hash-chained" />
      </div>

      <Panel title="Recent alerts" note={`${openAlerts.length} open`} bodyless>
        {openAlerts.length === 0 ? (
          <Empty>No open alerts. The gateway is quiet.</Empty>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Severity</th>
                <th>Rule</th>
                <th>Asset</th>
                <th>Raised</th>
              </tr>
            </thead>
            <tbody>
              {openAlerts.slice(0, 8).map((alert) => (
                <tr key={alert.id}>
                  <td>
                    <Badge tone={severityTone(alert.severity)}>{alert.severity || "info"}</Badge>
                  </td>
                  <td>
                    <div style={{ fontWeight: 500 }}>{alert.title || alert.rule_id}</div>
                    <div className="panel-note mono">{alert.rule_id}</div>
                  </td>
                  <td className="mono">{alert.asset_id || "—"}</td>
                  <td className="num panel-note">
                    {alert.created_at ? new Date(alert.created_at).toLocaleString() : "—"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>
    </>
  );
}

/**
 * Every tool call that reached the gateway, and what happened to it.
 *
 * The bars are proportional to the real counts rather than to a fixed scale: a
 * deployment that has denied nothing should look like one that has denied
 * nothing, not like one with a tiny sliver of red.
 */
function Funnel({ assurance }: { assurance: Assurance }) {
  const total = assurance.decisions_total;
  const rows = [
    { key: "allowed", label: "Allowed", value: assurance.decisions_allowed,
      note: "proceeded to the tool" },
    { key: "gated", label: "Held for approval", value: assurance.decisions_gated,
      note: "waiting on a person" },
    { key: "denied", label: "Refused", value: assurance.decisions_denied,
      note: "never reached the tool" },
  ];

  return (
    <section className="funnel">
      <div className="funnel-head">
        <div>
          <h2>Tool calls decided</h2>
          <p className="funnel-sub">
            Every call is decided before it runs. This is what the gateway did with them.
          </p>
        </div>
        <div className="funnel-total">
          <span className="funnel-total-value">{total.toLocaleString()}</span>
          <span className="funnel-total-label">decisions</span>
        </div>
      </div>

      {total === 0 ? (
        <Empty>No tool calls decided yet. The gateway is in place and idle.</Empty>
      ) : (
        <div className="funnel-rows">
          {rows.map((row) => {
            const share = total > 0 ? (row.value / total) * 100 : 0;
            return (
              <div className="funnel-row" key={row.key}>
                <div className="funnel-row-label">
                  <span className={`dot dot-${row.key}`} />
                  {row.label}
                </div>
                <div className="funnel-bar">
                  <div className={`funnel-fill fill-${row.key}`} style={{ width: `${share}%` }} />
                </div>
                <div className="funnel-row-value">
                  <strong>{row.value.toLocaleString()}</strong>
                  <span className="funnel-row-share">{share.toFixed(share < 1 && share > 0 ? 2 : 0)}%</span>
                </div>
                <div className="funnel-row-note">{row.note}</div>
              </div>
            );
          })}
        </div>
      )}
    </section>
  );
}

/**
 * Whether the record of those decisions can be trusted.
 *
 * These are the properties a buyer asks about and most products cannot show:
 * the chain validates, an outside party has witnessed it, enforcement is not
 * running degraded, and nobody reached the database unannounced. Each states a
 * fact rather than a colour, so "good" is legible without knowing the palette.
 */
function AssuranceStrip({ assurance }: { assurance: Assurance }) {
  const items = [
    {
      label: "Audit chain",
      ok: assurance.audit_chain_valid,
      value: assurance.audit_chain_valid ? "Valid" : "Broken",
      note: `${assurance.audit_chain_index.toLocaleString()} records linked`,
    },
    {
      label: "External witness",
      ok: assurance.witness_configured && !assurance.witness_diverged,
      neutral: !assurance.witness_configured,
      value: !assurance.witness_configured
        ? "Not configured"
        : assurance.witness_diverged
          ? "Diverged"
          : "Agrees",
      note: assurance.witness_configured
        ? `witnessed at ${assurance.witness_index.toLocaleString()}`
        : "history is anchored locally only",
    },
    {
      label: "Enforcement",
      ok: !assurance.degraded_mode,
      value: assurance.degraded_mode ? "Degraded" : "Normal",
      note: assurance.degraded_mode
        ? `${assurance.journal_depth.toLocaleString()} decisions journalled`
        : "storage healthy",
    },
    {
      label: "Operator access",
      ok: assurance.unannounced_sessions === 0 && !assurance.shipper_silent,
      value: assurance.unannounced_sessions === 0 ? "All announced" : `${assurance.unannounced_sessions} unannounced`,
      note: assurance.shipper_silent ? "the session reporter has gone quiet" : "reconciled against break-glass",
    },
  ];

  return (
    <div className="assurance">
      {items.map((item) => (
        <div
          key={item.label}
          className={`assurance-item ${item.neutral ? "is-neutral" : item.ok ? "is-ok" : "is-bad"}`}
        >
          <div className="assurance-label">{item.label}</div>
          <div className="assurance-value">{item.value}</div>
          <div className="assurance-note">{item.note}</div>
        </div>
      ))}
    </div>
  );
}

function Secondary({ label, value, hint, tone }: {
  label: string; value: any; hint?: string; tone?: "warn" | "calm";
}) {
  return (
    <div className={`secondary ${tone === "warn" ? "is-warn" : ""}`}>
      <div className="secondary-value">{typeof value === "number" ? value.toLocaleString() : value}</div>
      <div className="secondary-label">{label}</div>
      {hint ? <div className="secondary-hint">{hint}</div> : null}
    </div>
  );
}
