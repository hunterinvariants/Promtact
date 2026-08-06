import { useEffect, useState } from "react";
import { api } from "../api";
import { Badge, CoverageTrend, Empty, Panel, StatTile, TrendPoint } from "../components";

export default function Detections() {
  const [data, setData] = useState<any>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    api
      .validation()
      .then(setData)
      .catch((err) => setError(err.status === 404 ? "" : err.message));
  }, []);

  const result = data?.result;
  const rows: any[] = result?.results || [];
  const history: TrendPoint[] = (data?.history || []).map((h: any) => ({
    time: h.time,
    passed: h.passed,
    total: h.total,
  }));

  const sorted = [...rows].sort((a, b) =>
    (a.tactic || "").localeCompare(b.tactic || "") || (a.technique || "").localeCompare(b.technique || "")
  );
  const held = rows.filter((r) => r.pass).length;

  if (!data && !error) {
    return (
      <Panel title="Detection coverage">
        <Empty>Loading…</Empty>
      </Panel>
    );
  }

  if (!result) {
    return (
      <Panel title="Detection coverage">
        <Empty>
          No validation result yet. Run <span className="mono">promtactl validate --output …</span> and point the server
          at that file to populate this view.
        </Empty>
      </Panel>
    );
  }

  return (
    <>
      {error ? <div className="callout">⚠ {error}</div> : null}

      <div className="grid grid-metrics">
        <StatTile label="Techniques held" value={`${held}/${rows.length}`} hint="expected verdict enforced" />
        <StatTile label="Missed detections" value={result.missed ?? 0} hint="threat pattern not caught" />
        <StatTile label="False positives" value={result.false_positives ?? 0} hint="benign traffic flagged" />
        <StatTile
          label="Last run"
          value={data.ran_at ? new Date(data.ran_at).toLocaleDateString() : "—"}
          hint={data.ran_at ? new Date(data.ran_at).toLocaleTimeString() : ""}
        />
      </div>

      <Panel title="Coverage over time" note="share of detections holding, per validation run">
        <CoverageTrend history={history} />
      </Panel>

      <Panel title="ATT&CK coverage" note={`${held} of ${rows.length} enforced as expected`} bodyless>
        <table>
          <thead>
            <tr>
              <th>State</th>
              <th>Tactic</th>
              <th>Technique</th>
              <th>Emulation</th>
              <th>Expected</th>
              <th>Observed</th>
            </tr>
          </thead>
          <tbody>
            {sorted.map((row) => (
              <tr key={row.name}>
                <td>
                  <Badge tone={row.pass ? "good" : "critical"}>{row.pass ? "Held" : "Gap"}</Badge>
                </td>
                <td>{row.tactic || "—"}</td>
                <td className="mono">{row.technique || "—"}</td>
                <td>{row.name}</td>
                <td className="mono panel-note">{row.want}</td>
                <td className="mono">{row.got}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </Panel>
    </>
  );
}
