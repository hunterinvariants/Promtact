import { useEffect, useState } from "react";
import { api } from "../api";
import { Badge, Empty, Panel, StatTile, severityTone } from "../components";

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
  const critical = openAlerts.filter((a) => ["critical", "high"].includes((a.severity || "").toLowerCase()));
  const uptime = status?.uptime_seconds ? Math.floor(status.uptime_seconds / 3600) : 0;

  return (
    <>
      {error ? <div className="callout">⚠ {error}</div> : null}

      <div className="grid grid-metrics">
        <StatTile label="Open alerts" value={openAlerts.length} hint={`${critical.length} high or critical`} />
        <StatTile label="Assets" value={status?.asset_count ?? "—"} hint="seen in this tenant" />
        <StatTile label="Events" value={status?.event_count ?? "—"} hint="ingested" />
        <StatTile label="Response actions" value={status?.action_count ?? "—"} hint="planned or executed" />
        <StatTile label="Audit records" value={status?.audit_count ?? "—"} hint="hash-chained" />
        <StatTile label="Uptime" value={`${uptime}h`} hint={status?.version ? `v${status.version}` : ""} />
      </div>

      <Panel
        title="Recent alerts"
        note={`${openAlerts.length} open`}
        bodyless
      >
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
