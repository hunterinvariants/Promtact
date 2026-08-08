import { useState } from "react";
import { api } from "../api";
import { Badge, Empty, Panel } from "../components";

// The two runs, side by side, from the console.
//
// Everything here is real: the same agent, the same documents, the same tools,
// and for the guarded run the same handler that serves a customer's agent. What
// differs between the two columns is one thing — whether the calls go through
// the gateway.
//
// The agent is deliberately credulous. It decodes whatever instruction is
// hidden in a document and obeys it, with no model involved. That is the honest
// assumption rather than a shortcut: a demonstration resting on a model being
// fooled on cue is really claiming the model will probably resist, and nobody
// buys a control whose value depends on the thing it protects behaving well.

type Step = {
  tool: string;
  outcome: string;
  detail?: string;
  length?: number;
  hidden?: string;
};

type RunResult = {
  via: string;
  guarded: boolean;
  steps: Step[];
  sent: boolean;
  recipient?: string;
  outbox: number;
  summary: string;
};

function outcomeTone(outcome: string) {
  if (outcome === "sent") return "critical" as const;
  if (outcome === "withheld" || outcome === "held") return "good" as const;
  if (outcome === "denied") return "good" as const;
  return "neutral" as const;
}

function Run({ title, subtitle, result, busy, onRun, error }: {
  title: string;
  subtitle: string;
  result: RunResult | null;
  busy: boolean;
  onRun: () => void;
  error: string;
}) {
  return (
    <Panel
      title={title}
      note={subtitle}
      actions={
        <button className="btn btn-primary" onClick={onRun} disabled={busy}>
          {busy ? "Running…" : "Run"}
        </button>
      }
    >
      {error ? <div className="callout">⚠ {error}</div> : null}

      {!result && !error ? <Empty>Not run yet.</Empty> : null}

      {result ? (
        <>
          <table>
            <tbody>
              {result.steps.map((step, index) => (
                <tr key={index}>
                  <td className="mono" style={{ width: "45%" }}>{step.tool}</td>
                  <td style={{ width: 110 }}>
                    <Badge tone={outcomeTone(step.outcome)}>{step.outcome}</Badge>
                  </td>
                  <td className="panel-note">
                    {step.hidden ? (
                      <>
                        {/* The moment worth pausing on: the agent read text that
                            a person looking at the same file cannot see. */}
                        <div style={{ fontWeight: 500 }}>Hidden instruction decoded:</div>
                        <div className="mono event-command">{step.hidden}</div>
                      </>
                    ) : (
                      step.detail || (step.length ? `${step.length} characters` : "")
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>

          <div className={result.sent ? "callout" : ""} style={{ marginTop: 12 }}>
            <div style={{ fontWeight: 500 }}>
              Outbox: {result.outbox === 0 ? "empty" : `${result.outbox} message`}
            </div>
            <div className="panel-note">{result.summary}</div>
          </div>
        </>
      ) : null}
    </Panel>
  );
}

export default function Demonstration({ onNavigate }: { onNavigate?: (page: any) => void }) {
  const [direct, setDirect] = useState<RunResult | null>(null);
  const [guarded, setGuarded] = useState<RunResult | null>(null);
  const [busy, setBusy] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});

  const run = async (via: "direct" | "gateway") => {
    setBusy(via);
    setErrors((current) => ({ ...current, [via]: "" }));
    try {
      const result = await api.runDemoAgent(via);
      if (via === "direct") setDirect(result);
      else setGuarded(result);
    } catch (err: any) {
      setErrors((current) => ({ ...current, [via]: err.message }));
    } finally {
      setBusy("");
    }
  };

  return (
    <>
      <Panel title="What this shows">
        <p>
          An agent is asked to read the documents in a workspace and send a summary.
          One of those documents carries an instruction written in characters that
          have no visible form: a person opening the file sees four lines of status
          update and nothing else.
        </p>
        <p>
          Both runs use the same agent, the same documents and the same tools. The
          only difference is whether the agent&rsquo;s calls go through the gateway.
        </p>
        <p className="panel-note">
          Each run empties the outbox first, so what is in it afterwards belongs to
          that run. The guarded run goes through the same handler that serves a real
          agent — policy, response inspection, session marking and audit record.
        </p>
      </Panel>

      <div className="demo-grid">
        <Run
          title="Without the gateway"
          subtitle="the agent talks to its tools directly"
          result={direct}
          busy={busy === "direct"}
          error={errors.direct || ""}
          onRun={() => run("direct")}
        />
        <Run
          title="Through Promtact"
          subtitle="the same agent, gated"
          result={guarded}
          busy={busy === "gateway"}
          error={errors.gateway || ""}
          onRun={() => run("gateway")}
        />
      </div>

      {direct && guarded ? (
        <Panel title="The difference">
          <p>
            {direct.sent && !guarded.sent ? (
              <>
                Unguarded, the contents of an internal document left for{" "}
                <span className="mono">{direct.recipient}</span> — an address chosen
                by whoever wrote that file. Guarded, the poisoned document never
                reached the agent, and the onward call is waiting for a person.
              </>
            ) : (
              // Stated from the results rather than asserted, so a run that does
              // not show the difference says so instead of narrating a success
              // that did not happen.
              <>
                This pair does not show the contrast: unguarded sent={String(direct.sent)},
                guarded sent={String(guarded.sent)}. Check both runs above before drawing
                a conclusion.
              </>
            )}
          </p>
          <p className="panel-note">
            The decisions behind the guarded run are on the Approvals page, and every
            one of them is in the audit chain.
          </p>
          {onNavigate ? (
            <button className="btn" onClick={() => onNavigate("approvals")}>
              Open Approvals
            </button>
          ) : null}
        </Panel>
      ) : null}
    </>
  );
}
