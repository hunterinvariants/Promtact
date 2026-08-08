import { Fragment, useEffect, useMemo, useState } from "react";
import { api } from "../api";
import { Badge, Empty, Panel } from "../components";

// The front page, rebuilt around what this product actually does.
//
// What stood here before was a security operations dashboard: open alerts,
// assets, events, detection coverage. That is the vocabulary of an endpoint
// product, and it belongs to the half of this system we removed. Somebody
// selling "how do you verify what your AI agents do" opened the console and
// found a wall of alerts about hosts.
//
// A gateway has three questions and they are the only ones on this page.
// What did agents ask to do. What was decided, and why. Can the record of
// those decisions be trusted.

type Decision = {
  id: string;
  type?: string;
  reason?: string;
  created_at?: string;
  approval_status?: string;
  execution_status?: string;
  metadata?: Record<string, string>;
};

type Chain = {
  total: number;
  linked: number;
  valid: boolean;
  head?: string;
  tenant_records?: number;
};

type Witness = {
  configured: boolean;
  agrees?: boolean;
  diverged?: boolean;
  witnessed_index?: number;
  witnessed_head?: string;
  witnessed_at?: string;
  note?: string;
};

// A decision's outcome, in the words a person would use for it.
function describe(decision: Decision): { label: string; tone: "good" | "warning" | "critical" | "neutral" } {
  const execution = decision.execution_status || "";
  if (execution === "withheld") return { label: "answer withheld", tone: "good" };
  if (execution === "blocked") return { label: "refused", tone: "good" };
  if (decision.approval_status === "required" && !execution) {
    return { label: "waiting for a person", tone: "warning" };
  }
  if (execution === "executed") return { label: "allowed", tone: "neutral" };
  return { label: execution || decision.approval_status || "—", tone: "neutral" };
}

export default function Gateway({ onNavigate }: { onNavigate?: (page: any) => void }) {
  const [decisions, setDecisions] = useState<Decision[]>([]);
  const [chain, setChain] = useState<Chain | null>(null);
  const [witness, setWitness] = useState<Witness | null>(null);
  const [expanded, setExpanded] = useState<string | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      try {
        const [actions, chainState, witnessState] = await Promise.all([
          api.actions(),
          api.auditChain(),
          api.auditWitness(),
        ]);
        if (cancelled) return;
        setDecisions(actions || []);
        setChain(chainState);
        setWitness(witnessState);
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

  const counts = useMemo(() => {
    let allowed = 0;
    let held = 0;
    let stopped = 0;
    for (const decision of decisions) {
      const execution = decision.execution_status || "";
      if (execution === "withheld" || execution === "blocked") stopped++;
      else if (decision.approval_status === "required" && !execution) held++;
      else if (execution === "executed") allowed++;
    }
    return { allowed, held, stopped, total: decisions.length };
  }, [decisions]);

  return (
    <>
      {error ? <div className="callout">⚠ {error}</div> : null}

      <div className="grid grid-metrics">
        <div className="stat">
          <div className="stat-value">{counts.total}</div>
          <div className="stat-label">Tool calls decided</div>
          <div className="stat-hint">every one on the record</div>
        </div>
        <div className="stat">
          <div className="stat-value">{counts.allowed}</div>
          <div className="stat-label">Allowed</div>
          <div className="stat-hint">agents worked normally</div>
        </div>
        <div
          className={`stat ${counts.held ? "is-clickable" : ""}`}
          onClick={() => counts.held && onNavigate?.("approvals")}
        >
          <div className="stat-value">{counts.held}</div>
          <div className="stat-label">Waiting for a person</div>
          <div className="stat-hint">an agent is stopped mid-task</div>
        </div>
        <div className="stat">
          <div className="stat-value">{counts.stopped}</div>
          <div className="stat-label">Stopped</div>
          <div className="stat-hint">refused, or the answer withheld</div>
        </div>
      </div>

      <Panel
        title="Can this record be trusted?"
        note={chain ? `${chain.total} records` : ""}
      >
        {/* The second half of the product, and the half that is usually
            missing elsewhere. Stated as a question because that is the one an
            auditor asks. */}
        {chain ? (
          <dl>
            <div>
              <dt>Chain</dt>
              <dd>
                {chain.valid
                  ? `intact — all ${chain.linked} records still hash to the one before`
                  : "BROKEN — a record has been changed or removed"}
              </dd>
            </div>
            {witness?.configured ? (
              <div>
                <dt>External witness</dt>
                <dd>
                  {witness.diverged
                    ? `diverged — the witness holds record ${witness.witnessed_index}, this server does not`
                    : witness.agrees
                      ? `agrees at record ${witness.witnessed_index}`
                      : `configured, holding record ${witness.witnessed_index}`}
                </dd>
              </div>
            ) : (
              <div>
                <dt>External witness</dt>
                <dd>
                  not configured — this chain detects accidental corruption only.
                  Anyone who can write to the database can recompute every hash.
                </dd>
              </div>
            )}
          </dl>
        ) : (
          <Empty>Loading.</Empty>
        )}
        <p className="panel-note">
          With a witness, an operator cannot quietly edit this record even with
          full access to the server and its database — the witness already holds
          an earlier head and refuses a chain that got shorter or changed.
        </p>
      </Panel>

      <Panel title="What agents asked to do" note={`${decisions.length}`} bodyless>
        {decisions.length === 0 ? (
          <Empty>
            No tool calls yet. Point an agent at this gateway, or open the
            Demonstration page.
          </Empty>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Tool</th>
                <th>Outcome</th>
                <th>Why</th>
                <th>When</th>
              </tr>
            </thead>
            <tbody>
              {decisions.slice(0, 25).map((decision) => {
                const meta = decision.metadata || {};
                const outcome = describe(decision);
                const open = expanded === decision.id;
                return (
                  <Fragment key={decision.id}>
                    <tr className="is-clickable" onClick={() => setExpanded(open ? null : decision.id)}>
                      <td className="mono">{meta.tool || decision.type || "—"}</td>
                      <td>
                        <Badge tone={outcome.tone}>{outcome.label}</Badge>
                      </td>
                      <td className="panel-note">{meta.result_reason || decision.reason || "—"}</td>
                      <td className="num panel-note">
                        {decision.created_at ? new Date(decision.created_at).toLocaleString() : "—"}
                      </td>
                    </tr>
                    {open ? (
                      <tr>
                        <td colSpan={4} className="event-detail">
                          <dl>
                            {[
                              ["Tool", meta.tool],
                              ["Command", meta.command],
                              ["Asked by", meta.actor],
                              ["Destination", meta.destination || meta.mcp_upstream_url || meta.proxy_upstream_url],
                              ["Verdict", meta.verdict],
                              ["Risk", meta.risk],
                              ["Found in the answer", meta.result_findings],
                              ["Session had read", meta.result_taint],
                            ]
                              .filter(([, value]) => value)
                              .map(([label, value]) => (
                                <div key={label as string}>
                                  <dt>{label}</dt>
                                  <dd className="mono">{value as string}</dd>
                                </div>
                              ))}
                          </dl>
                        </td>
                      </tr>
                    ) : null}
                  </Fragment>
                );
              })}
            </tbody>
          </table>
        )}
      </Panel>
    </>
  );
}
