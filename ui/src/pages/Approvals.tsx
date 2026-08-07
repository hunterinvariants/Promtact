import { Fragment, useCallback, useEffect, useMemo, useState } from "react";
import { api } from "../api";
import { Badge, Empty, Panel } from "../components";

// The calls waiting for a person, and why they are waiting.
//
// Until this page existed the gateway could hold a tool call and nobody could
// see it: the API had returned the pending action all along, and no screen ever
// asked for it. An agent stopped mid-task and the console showed a quiet
// dashboard — which is the worst failure available to a control of this kind,
// because it looks like nothing is happening in both the working case and the
// broken one.
//
// The question this page has to answer is not "what is pending". It is "why is
// this pending, and can I let it through". For an injection chain that means
// naming the thing the agent read before it tried to act, because the action
// itself always looks reasonable — that is what makes the attack work.

type Action = {
  id: string;
  type?: string;
  asset_id?: string;
  target?: string;
  reason?: string;
  created_at?: string;
  approval_status?: string;
  execution_status?: string;
  metadata?: Record<string, string>;
};

// Findings are recorded as machine names because they end up in audit records.
// A person reading a held call needs the sentence, not the identifier.
const FINDING_LABELS: Record<string, string> = {
  hidden_unicode:
    "The content contained characters that render as nothing. A person reviewing that page would not have seen the text a model reads.",
  image_exfiltration:
    "The content embedded an image address carrying a query string — the usual shape of data leaving without anyone clicking.",
  instruction_override:
    "The content told its reader to disregard earlier instructions. Documents do not normally address their reader that way.",
  hidden_markup:
    "The content hid text using markup that renders to nothing, and that text spoke to a model rather than to a reader.",
  addresses_model:
    "The content spoke to a model rather than to a reader. On its own this is weak — documentation about AI reads like this too.",
  encoded_blob: "The content held a long encoded run, which is how text gets past a reader but not past a decoder.",
};

function describeFinding(finding: string): string {
  const [key] = finding.split(":");
  if (FINDING_LABELS[key]) return FINDING_LABELS[key];
  if (key === "credential_material") return "The content carried something shaped like a credential into the agent's context.";
  if (key === "canary_material") return "The content touched a deception asset, which nothing legitimate reads.";
  if (key === "untrusted_context")
    return "This action reaches outward, and the session had already read content from an untrusted source.";
  return finding;
}

// The mark that made the difference, turned back into the sentence it stands for.
function describeTaint(mark: string): string {
  const [kind, rest] = [mark.split(":")[0], mark.split(":").slice(1).join(":")];
  switch (kind) {
    case "untrusted_origin":
      return `Read from ${rest}, which is outside your approved list.`;
    case "tool_result":
      return `Returned by ${rest}.`;
    case "secret":
      return `Carried credential-shaped material (${rest}).`;
    case "canary":
      return `Touched a deception asset (${rest}).`;
    case "inspected":
      return "The content was flagged when it came back.";
    default:
      return mark;
  }
}

export default function Approvals() {
  const [actions, setActions] = useState<Action[]>([]);
  const [expanded, setExpanded] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  const load = useCallback(
    () =>
      api
        .actions()
        .then((data) => {
          setActions(data || []);
          setError("");
        })
        .catch((err) => setError(err.message)),
    [],
  );

  useEffect(() => {
    load();
    const handle = window.setInterval(load, 10000);
    return () => window.clearInterval(handle);
  }, [load]);

  const pending = useMemo(
    () => actions.filter((a) => (a.approval_status || "") === "required" && !a.execution_status),
    [actions],
  );
  const settled = useMemo(
    () => actions.filter((a) => !((a.approval_status || "") === "required" && !a.execution_status)).slice(0, 20),
    [actions],
  );

  const approve = async (action: Action) => {
    setBusy(action.id);
    setNotice("");
    try {
      await api.approveAction(action.id, "console");
      await load();
      setNotice(`Approved. The call was released and the approval is in the audit record.`);
    } catch (err: any) {
      setError(err.message);
    } finally {
      setBusy(null);
    }
  };

  return (
    <>
      {error ? <div className="callout">⚠ {error}</div> : null}
      {notice ? <div className="callout">{notice}</div> : null}

      <Panel title="Waiting for you" note={`${pending.length} held`} bodyless>
        {pending.length === 0 ? (
          <Empty>Nothing is waiting. Calls the gateway holds appear here.</Empty>
        ) : (
          <table>
            <thead>
              <tr>
                <th>What was asked</th>
                <th>Where</th>
                <th>Held</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {pending.map((action) => {
                const open = expanded === action.id;
                const meta = action.metadata || {};
                const signals = (meta.signals || "").split(";").filter(Boolean);
                const findings = [...(meta.result_findings || "").split(",").filter(Boolean), ...signals];
                // Two sources, because a held call and the fetch that caused it
                // are different records. `result_taint` is on the call that read
                // the content; a call held for acting afterwards carries the
                // marks inside its own signals instead — and that is precisely
                // the case this page exists to explain.
                const taint = [
                  ...(meta.result_taint || "").split(",").filter(Boolean),
                  ...signals
                    .filter((signal) => signal.startsWith("untrusted_context:"))
                    .flatMap((signal) => signal.slice("untrusted_context:".length).split("|"))
                    .filter(Boolean),
                ];
                return (
                  <Fragment key={action.id}>
                    <tr className="is-clickable" onClick={() => setExpanded(open ? null : action.id)}>
                      <td>
                        <div style={{ fontWeight: 500 }}>{meta.tool || action.type || "tool call"}</div>
                        <div className="panel-note">{action.reason || "held for approval"}</div>
                      </td>
                      <td className="mono">{action.asset_id || action.target || "—"}</td>
                      <td className="num panel-note">
                        {action.created_at ? new Date(action.created_at).toLocaleString() : "—"}
                      </td>
                      <td>
                        <button
                          className="btn btn-primary"
                          disabled={busy === action.id}
                          onClick={(event) => {
                            event.stopPropagation();
                            approve(action);
                          }}
                        >
                          {busy === action.id ? "Releasing…" : "Approve"}
                        </button>
                      </td>
                    </tr>
                    {open ? (
                      <tr>
                        <td colSpan={4} className="event-detail">
                          <h4>Why this is waiting</h4>
                          <p>{action.reason || "The gateway held this call for a person."}</p>

                          {taint.length ? (
                            <>
                              {/* The half of the chain that is easy to miss. The action
                                  itself always looks reasonable; what makes it worth a
                                  second look is what the agent read just before it. */}
                              <h4>What this session had read</h4>
                              <ul>
                                {taint.map((mark) => (
                                  <li key={mark}>{describeTaint(mark)}</li>
                                ))}
                              </ul>
                            </>
                          ) : null}

                          {findings.length ? (
                            <>
                              <h4>What was found in it</h4>
                              <ul>
                                {findings.map((finding) => (
                                  <li key={finding}>{describeFinding(finding)}</li>
                                ))}
                              </ul>
                            </>
                          ) : null}

                          <h4>Details</h4>
                          <dl>
                            {[
                              ["Tool", meta.tool],
                              ["Command", meta.command],
                              ["Destination", meta.destination || meta.proxy_upstream_url || meta.mcp_upstream_url],
                              ["Asset", action.asset_id],
                              ["Action", action.id],
                            ]
                              .filter(([, value]) => value)
                              .map(([label, value]) => (
                                <div key={label as string}>
                                  <dt>{label}</dt>
                                  <dd className="mono">{value as string}</dd>
                                </div>
                              ))}
                          </dl>

                          <p className="panel-note">
                            Approving releases this one call and records who released it. It does not
                            change the policy, and the next call is judged the same way.
                          </p>
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

      <Panel title="Recently settled" note={`${settled.length}`} bodyless>
        {settled.length === 0 ? (
          <Empty>Nothing settled yet.</Empty>
        ) : (
          <table>
            <thead>
              <tr>
                <th>What</th>
                <th>Outcome</th>
                <th>When</th>
              </tr>
            </thead>
            <tbody>
              {settled.map((action) => (
                <tr key={action.id}>
                  <td>
                    <div style={{ fontWeight: 500 }}>{(action.metadata || {}).tool || action.type || "tool call"}</div>
                    <div className="panel-note">{action.reason}</div>
                  </td>
                  <td>
                    <Badge
                      tone={
                        action.execution_status === "withheld" || action.execution_status === "blocked"
                          ? "critical"
                          : action.execution_status === "executed"
                            ? "good"
                            : "warning"
                      }
                    >
                      {action.execution_status || action.approval_status || "—"}
                    </Badge>
                  </td>
                  <td className="num panel-note">
                    {action.created_at ? new Date(action.created_at).toLocaleString() : "—"}
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
