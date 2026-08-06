import { useEffect, useState } from "react";
import { api } from "../api";
import { Badge, Empty, Panel } from "../components";

function riskTone(score: number) {
  if (score >= 70) return "critical" as const;
  if (score >= 40) return "serious" as const;
  if (score >= 20) return "warning" as const;
  return "good" as const;
}

export default function Assets() {
  const [assets, setAssets] = useState<any[]>([]);
  const [error, setError] = useState("");

  useEffect(() => {
    api
      .assets()
      .then((data) => setAssets(data || []))
      .catch((err) => setError(err.message));
  }, []);

  const sorted = [...assets].sort((a, b) => (b.risk_score || 0) - (a.risk_score || 0));

  return (
    <>
      {error ? <div className="callout">⚠ {error}</div> : null}
      <Panel title="Assets" note={`${assets.length} tracked`} bodyless>
        {sorted.length === 0 ? (
          <Empty>No assets seen yet.</Empty>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Risk</th>
                <th>Asset</th>
                <th>Agent surface</th>
                <th>Last seen</th>
              </tr>
            </thead>
            <tbody>
              {sorted.map((asset) => (
                <tr key={asset.id}>
                  <td>
                    <Badge tone={riskTone(asset.risk_score || 0)}>{asset.risk_score ?? 0}</Badge>
                  </td>
                  <td>
                    <div style={{ fontWeight: 500 }}>{asset.hostname || asset.id}</div>
                    <div className="panel-note mono">{asset.id}</div>
                  </td>
                  <td className="panel-note">{(asset.agent_surface || []).join(", ") || "—"}</td>
                  <td className="num panel-note">
                    {asset.last_seen ? new Date(asset.last_seen).toLocaleString() : "—"}
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
