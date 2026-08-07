import { useEffect, useState } from "react";
import { api } from "../api";
import { Badge, Empty, Panel, severityTone } from "../components";
import { explainRule, RISK_SCALE } from "../explain";

export default function Alerts() {
  const [alerts, setAlerts] = useState<any[]>([]);
  const [filter, setFilter] = useState("all");
  const [expanded, setExpanded] = useState<string | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    api
      .alerts()
      .then((data) => setAlerts(data || []))
      .catch((err) => setError(err.message));
  }, []);

  const visible = alerts.filter((a) => {
    if (filter === "all") return true;
    if (filter === "open") return (a.status || "open") === "open";
    return (a.severity || "").toLowerCase() === filter;
  });

  return (
    <>
      {error ? <div className="callout">⚠ {error}</div> : null}
      <Panel
        title="Alerts"
        note={`${visible.length} of ${alerts.length}`}
        bodyless
        actions={
          <select className="select" value={filter} onChange={(e) => setFilter(e.target.value)} aria-label="Filter alerts">
            <option value="all">All</option>
            <option value="open">Open</option>
            <option value="critical">Critical</option>
            <option value="high">High</option>
            <option value="medium">Medium</option>
            <option value="low">Low</option>
          </select>
        }
      >
        {visible.length === 0 ? (
          <Empty>No alerts match this filter.</Empty>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Severity</th>
                <th>Alert</th>
                <th>Asset</th>
                <th>Status</th>
                <th>Raised</th>
              </tr>
            </thead>
            <tbody>
              {visible.map((alert) => {
                const open = expanded === alert.id;
                const explained = explainRule(alert.rule_id);
                return (
                  <>
                    <tr
                      key={alert.id}
                      className="is-clickable"
                      onClick={() => setExpanded(open ? null : alert.id)}
                    >
                      <td>
                        <Badge tone={severityTone(alert.severity)}>{alert.severity || "info"}</Badge>
                      </td>
                      <td>
                        <div style={{ fontWeight: 500 }}>{alert.title || alert.rule_id}</div>
                        <div className="panel-note">{explained.summary || alert.description}</div>
                      </td>
                      <td className="mono">{alert.asset_id || "—"}</td>
                      <td>
                        <Badge tone={(alert.status || "open") === "open" ? "warning" : "good"}>
                          {alert.status || "open"}
                        </Badge>
                      </td>
                      <td className="num panel-note">
                        {alert.created_at ? new Date(alert.created_at).toLocaleString() : "—"}
                      </td>
                    </tr>
                    {open ? (
                      <tr key={alert.id + "-detail"}>
                        <td colSpan={5} className="alert-detail">
                          <AlertDetail alert={alert} explained={explained} />
                        </td>
                      </tr>
                    ) : null}
                  </>
                );
              })}
            </tbody>
          </table>
        )}
      </Panel>
    </>
  );
}

/**
 * What a person needs when an alert lands: what happened, why it matters, what
 * to do, and the evidence — in that order. The rule identifier belongs at the
 * bottom, where an engineer can find it and a customer can ignore it.
 */
// Exported because the overview shows the same alerts, and a reader who clicks
// one there expects it to open there — not to be sent to another page to click
// it a second time.
export function AlertDetail({ alert, explained }: { alert: any; explained: ReturnType<typeof explainRule> }) {
  const evidence: [string, string][] = Object.entries(alert.evidence || {})
    .filter(([, value]) => value)
    .map(([key, value]) => [key, String(value)]);

  return (
    <div className="detail-body">
      <section>
        <h4>What happened</h4>
        <p>{explained.what || alert.description}</p>
      </section>

      {explained.why ? (
        <section>
          <h4>Why it matters</h4>
          <p>{explained.why}</p>
        </section>
      ) : null}

      {explained.doThis ? (
        <section>
          <h4>What to do</h4>
          <p>{explained.doThis}</p>
        </section>
      ) : null}

      {evidence.length ? (
        <section>
          <h4>Evidence</h4>
          <dl>
            {evidence.map(([key, value]) => (
              <div key={key}>
                <dt>{key.replace(/_/g, " ")}</dt>
                <dd className="mono">{value}</dd>
              </div>
            ))}
          </dl>
        </section>
      ) : null}

      <section>
        <h4>Where</h4>
        <dl>
          <div><dt>Asset</dt><dd className="mono">{alert.asset_id || "—"}</dd></div>
          <div><dt>Raised</dt><dd>{alert.created_at ? new Date(alert.created_at).toLocaleString() : "—"}</dd></div>
          {alert.event_ids?.length ? (
            <div><dt>Events</dt><dd>{alert.event_ids.length} linked — see the Events page</dd></div>
          ) : null}
          <div><dt>Rule</dt><dd className="mono">{alert.rule_id}</dd></div>
        </dl>
      </section>

      <p className="detail-foot">{RISK_SCALE}</p>
    </div>
  );
}
