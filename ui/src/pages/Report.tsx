import { useEffect, useState } from "react";
import { api } from "../api";
import { Empty, Panel } from "../components";

// The evidence pack, as a page somebody can print and file.
//
// Printing is deliberately the browser's job. A PDF library would be a third
// dependency in a project that has two on purpose, and it would produce a
// document nobody could check against the running system. A printed page comes
// from the same data the console shows, and the person printing it watched it
// load.
//
// The report does not flatter. An empty period says so, an unwitnessed chain
// says what that does and does not prove, and the last section lists what this
// system cannot see. A report that contains only good news is one an auditor
// stops reading.

type Report = {
  tenant: string;
  generated_at: string;
  generated_by: string;
  period: { from: string; to: string; days: number };
  counts: { decided: number; allowed: number; held: number; stopped: number };
  stopped_for: { reason: string; count: number }[];
  held_for: { reason: string; count: number }[];
  approvals: { tool: string; reason: string; approved_by: string; at: string }[];
  policy_changes: { at: string; by: string; added?: string; removed?: string }[];
  integrity: {
    records: number;
    linked: number;
    valid: boolean;
    head: string;
    witness_configured: boolean;
    witness_agrees: boolean;
    witness_index: number;
    witness_at?: string;
    statement: string;
  };
  not_covered: string[];
};

const PERIODS = [
  { label: "Last 7 days", days: 7 },
  { label: "Last 30 days", days: 30 },
  { label: "Last 90 days", days: 90 },
];

function day(value: string) {
  return new Date(value).toLocaleDateString();
}

export default function Report() {
  const [report, setReport] = useState<Report | null>(null);
  const [days, setDays] = useState(30);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const load = (period: number) => {
    setBusy(true);
    setError("");
    const to = new Date();
    const from = new Date(to.getTime() - period * 24 * 60 * 60 * 1000);
    api
      .report(from.toISOString(), to.toISOString())
      .then(setReport)
      .catch((err) => setError(err.message))
      .finally(() => setBusy(false));
  };

  useEffect(() => load(days), [days]);

  const download = () => {
    if (!report) return;
    const blob = new Blob([JSON.stringify(report, null, 2)], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = `promtact-report-${report.period.from.slice(0, 10)}-to-${report.period.to.slice(0, 10)}.json`;
    link.click();
    URL.revokeObjectURL(url);
  };

  if (error) return <div className="callout">⚠ {error}</div>;
  if (!report) return <Empty>{busy ? "Building the report." : "No report."}</Empty>;

  const { counts, integrity } = report;

  return (
    <>
      <div className="no-print">
        <Panel
          title="Period"
          actions={
            <div className="row" style={{ gap: 8 }}>
              <select
                className="select"
                value={days}
                onChange={(event) => setDays(Number(event.target.value))}
                aria-label="Reporting period"
              >
                {PERIODS.map((period) => (
                  <option key={period.days} value={period.days}>
                    {period.label}
                  </option>
                ))}
              </select>
              <button className="btn" onClick={download}>
                Download JSON
              </button>
              <button className="btn btn-primary" onClick={() => window.print()}>
                Print / PDF
              </button>
            </div>
          }
        >
          <p className="panel-note">
            The printed page is this page. The JSON is the same data for a SIEM
            or a ticket.
          </p>
        </Panel>
      </div>

      <Panel title={`Agent activity — ${report.tenant}`}>
        <p className="panel-note">
          {day(report.period.from)} to {day(report.period.to)} · generated{" "}
          {new Date(report.generated_at).toLocaleString()} by {report.generated_by}
        </p>

        {counts.decided === 0 ? (
          <div className="callout" style={{ marginTop: 12 }}>
            No agent tool calls passed through this gateway in the period. That
            is either a quiet period or a sign that agents are reaching their
            tools another way, and the two are worth telling apart.
          </div>
        ) : (
          <div className="grid grid-metrics" style={{ marginTop: 12 }}>
            <div className="stat">
              <div className="stat-value">{counts.decided}</div>
              <div className="stat-label">Tool calls decided</div>
            </div>
            <div className="stat">
              <div className="stat-value">{counts.allowed}</div>
              <div className="stat-label">Allowed</div>
            </div>
            <div className="stat">
              <div className="stat-value">{counts.held}</div>
              <div className="stat-label">Held for a person</div>
            </div>
            <div className="stat">
              <div className="stat-value">{counts.stopped}</div>
              <div className="stat-label">Stopped</div>
            </div>
          </div>
        )}
      </Panel>

      {report.stopped_for.length > 0 ? (
        <Panel title="What was stopped, and why" bodyless>
          <table>
            <tbody>
              {report.stopped_for.map((row) => (
                <tr key={row.reason}>
                  <td className="num" style={{ width: 70 }}>{row.count}</td>
                  <td>{row.reason}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </Panel>
      ) : null}

      {report.held_for.length > 0 ? (
        <Panel title="What needed a person, and why" bodyless>
          <table>
            <tbody>
              {report.held_for.map((row) => (
                <tr key={row.reason}>
                  <td className="num" style={{ width: 70 }}>{row.count}</td>
                  <td>{row.reason}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </Panel>
      ) : null}

      <Panel title="Who approved what" note={`${report.approvals.length}`} bodyless>
        {report.approvals.length === 0 ? (
          <Empty>Nothing was approved in this period.</Empty>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Tool</th>
                <th>Approved by</th>
                <th>When</th>
              </tr>
            </thead>
            <tbody>
              {report.approvals.map((approval, index) => (
                <tr key={index}>
                  <td className="mono">{approval.tool || "—"}</td>
                  <td>{approval.approved_by}</td>
                  <td className="num panel-note">{new Date(approval.at).toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>

      <Panel title="Changes to the policy" note={`${report.policy_changes.length}`} bodyless>
        {report.policy_changes.length === 0 ? (
          <Empty>The policy was not changed in this period.</Empty>
        ) : (
          <table>
            <thead>
              <tr>
                <th>When</th>
                <th>By</th>
                <th>Added</th>
                <th>Removed</th>
              </tr>
            </thead>
            <tbody>
              {report.policy_changes.map((change, index) => (
                <tr key={index}>
                  <td className="num panel-note">{new Date(change.at).toLocaleString()}</td>
                  <td>{change.by}</td>
                  <td className="mono">{change.added || "—"}</td>
                  <td className="mono">{change.removed || "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>

      <Panel title="Why this record can be relied on">
        <dl>
          <div>
            <dt>Records</dt>
            <dd>
              {integrity.records} in the chain, {integrity.linked} hash-linked
            </dd>
          </div>
          <div>
            <dt>Verified</dt>
            <dd>{integrity.valid ? "yes" : "no — see below"}</dd>
          </div>
          {integrity.witness_configured ? (
            <div>
              <dt>External witness</dt>
              <dd>
                {integrity.witness_agrees ? "agrees" : "does not agree"} at record{" "}
                {integrity.witness_index}
                {integrity.witness_at ? `, last published ${new Date(integrity.witness_at).toLocaleString()}` : ""}
              </dd>
            </div>
          ) : null}
          <div>
            <dt>Chain head</dt>
            <dd className="mono">{integrity.head || "—"}</dd>
          </div>
        </dl>
        <p style={{ maxWidth: "78ch", marginTop: 8 }}>{integrity.statement}</p>
      </Panel>

      <Panel title="What this report does not cover">
        {/* Placed last and not softened. Every one of these is a question that
            gets asked, and answering it first is worth more than being asked
            and appearing to have hidden it. */}
        <ul style={{ maxWidth: "78ch", paddingLeft: 18 }}>
          {report.not_covered.map((item, index) => (
            <li key={index} style={{ marginBottom: 6 }}>{item}</li>
          ))}
        </ul>
      </Panel>
    </>
  );
}
