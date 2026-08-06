import { useEffect, useState } from "react";
import { api } from "../api";
import { Badge, Empty, Panel, severityTone } from "../components";

export default function Alerts() {
  const [alerts, setAlerts] = useState<any[]>([]);
  const [filter, setFilter] = useState("all");
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
              {visible.map((alert) => (
                <tr key={alert.id}>
                  <td>
                    <Badge tone={severityTone(alert.severity)}>{alert.severity || "info"}</Badge>
                  </td>
                  <td>
                    <div style={{ fontWeight: 500 }}>{alert.title || alert.rule_id}</div>
                    <div className="panel-note">{alert.description}</div>
                    <div className="panel-note mono">{alert.rule_id}</div>
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
              ))}
            </tbody>
          </table>
        )}
      </Panel>
    </>
  );
}
