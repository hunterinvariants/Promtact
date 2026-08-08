import { useState } from "react";
import { api } from "../api";
import { Badge, Empty, Panel } from "../components";

// The whole demonstration, in the browser, as a walkthrough.
//
// Every step of this used to be a shell command: read the file, check its size,
// run the agent, list the outbox, read the message, print the audit trail,
// verify the chain. The person who decides whether to buy this does not read
// terminals, and asking them to is asking them to take the interesting part on
// trust.
//
// Each step states what the audience should take away before it runs, so the
// room is watching for the right thing rather than reading output and guessing
// what mattered.

type Step = { tool: string; outcome: string; detail?: string; length?: number; hidden?: string };
type RunResult = { steps: Step[]; sent: boolean; recipient?: string; outbox: number; summary: string };
type Doc = { name: string; visible: string; bytes: number; visible_runes: number; hidden_runes: number; hidden?: string };
type Message = { name: string; body: string };
type Audit = { id: string; action: string; outcome: string; timestamp?: string; chain_index?: number; metadata?: Record<string, string> };

function outcomeTone(outcome: string): "good" | "warning" | "critical" | "neutral" {
  if (outcome === "sent") return "critical";
  if (outcome === "withheld" || outcome === "held") return "good";
  return "neutral";
}

function StepCard({
  number,
  title,
  says,
  action,
  busy,
  onRun,
  error,
  children,
}: {
  number: number;
  title: string;
  says: string;
  action: string;
  busy: boolean;
  onRun: () => void;
  error?: string;
  children?: React.ReactNode;
}) {
  return (
    <Panel
      title={`${number}. ${title}`}
      actions={
        <button className="btn btn-primary" onClick={onRun} disabled={busy}>
          {busy ? "…" : action}
        </button>
      }
    >
      <p className="panel-note" style={{ maxWidth: "78ch" }}>{says}</p>
      {error ? <div className="callout">⚠ {error}</div> : null}
      {children}
    </Panel>
  );
}

function RunSteps({ result }: { result: RunResult }) {
  return (
    <>
      <table>
        <tbody>
          {result.steps.map((step, index) => (
            <tr key={index}>
              <td className="mono" style={{ width: "42%" }}>{step.tool}</td>
              <td style={{ width: 130 }}>
                <Badge tone={outcomeTone(step.outcome)}>{step.outcome}</Badge>
              </td>
              <td className="panel-note">
                {step.hidden ? (
                  <>
                    <div style={{ fontWeight: 500 }}>The agent decoded a hidden instruction:</div>
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
      <p style={{ fontWeight: 500, marginTop: 10 }}>{result.summary}</p>
    </>
  );
}

export default function Demonstration({ onNavigate }: { onNavigate?: (page: any) => void }) {
  const [docs, setDocs] = useState<Doc[] | null>(null);
  const [direct, setDirect] = useState<RunResult | null>(null);
  const [guarded, setGuarded] = useState<RunResult | null>(null);
  const [outboxAfterDirect, setOutboxAfterDirect] = useState<Message[] | null>(null);
  const [outboxAfterGuarded, setOutboxAfterGuarded] = useState<Message[] | null>(null);
  const [audit, setAudit] = useState<Audit[] | null>(null);
  const [chain, setChain] = useState<any>(null);
  const [witness, setWitness] = useState<any>(null);
  const [busy, setBusy] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});

  const step = async (key: string, work: () => Promise<void>) => {
    setBusy(key);
    setErrors((current) => ({ ...current, [key]: "" }));
    try {
      await work();
    } catch (err: any) {
      setErrors((current) => ({ ...current, [key]: err.message }));
    } finally {
      setBusy("");
    }
  };

  const poisoned = docs?.find((doc) => doc.hidden_runes > 0);

  return (
    <>
      <Panel title="What you are about to see">
        <p style={{ maxWidth: "78ch" }}>
          An AI agent is asked to read the documents in a workspace and send a
          summary onward. One of those documents was written by somebody else.
        </p>
        <p style={{ maxWidth: "78ch" }}>
          The agent is deliberately credulous: it does whatever a document tells
          it to. That is the assumption rather than a shortcut - a control worth
          buying is one that holds when the model is fooled, not one that hopes
          it will not be.
        </p>
      </Panel>

      <StepCard
        number={1}
        title="The document that looks harmless"
        says="Read it. Four lines of status update. Then look at what it weighs: the difference between what it displays and what it contains is the attack."
        action="Open the workspace"
        busy={busy === "docs"}
        error={errors.docs}
        onRun={() => step("docs", async () => setDocs(await api.demoDocuments()))}
      >
        {docs ? (
          <>
            {docs.map((doc) => (
              <div key={doc.name} style={{ marginTop: 12 }}>
                <div style={{ fontWeight: 500 }} className="mono">{doc.name}</div>
                <pre className="event-detail mono" style={{ whiteSpace: "pre-wrap", marginTop: 6 }}>
                  {doc.visible}
                </pre>
                <div className="panel-note">
                  {doc.bytes} bytes on disk, {doc.visible_runes} characters visible
                  {doc.hidden_runes > 0 ? (
                    <>
                      {" "}- and <strong>{doc.hidden_runes} characters that render as nothing at all</strong>.
                    </>
                  ) : (
                    <> - nothing hidden.</>
                  )}
                </div>
              </div>
            ))}
            {poisoned?.hidden ? (
              <div className="callout" style={{ marginTop: 12 }}>
                <div style={{ fontWeight: 500 }}>Decoded, the invisible characters say:</div>
                <div className="mono event-command">{poisoned.hidden}</div>
                <div className="panel-note">
                  A person reviewing that file sees none of this. A model reads it
                  as ordinary text.
                </div>
              </div>
            ) : null}
          </>
        ) : (
          <Empty>Not opened yet.</Empty>
        )}
      </StepCard>

      <StepCard
        number={2}
        title="The agent works without a gateway"
        says="This is how most agent deployments run today: the agent talks to its tools, and nothing sits between them."
        action="Run unguarded"
        busy={busy === "direct"}
        error={errors.direct}
        onRun={() =>
          step("direct", async () => {
            setDirect(await api.runDemoAgent("direct"));
            setOutboxAfterDirect(null);
            setOutboxAfterGuarded(null);
            setGuarded(null);
          })
        }
      >
        {direct ? <RunSteps result={direct} /> : <Empty>Not run yet.</Empty>}
      </StepCard>

      <StepCard
        number={3}
        title="What left the building"
        says="Not an alert about something that left. The message itself, addressed by whoever wrote that document."
        action="Open the outbox"
        busy={busy === "outbox1"}
        error={errors.outbox1}
        onRun={() => step("outbox1", async () => setOutboxAfterDirect(await api.demoOutbox()))}
      >
        {outboxAfterDirect ? (
          outboxAfterDirect.length === 0 ? (
            <Empty>Empty - run step 2 first.</Empty>
          ) : (
            outboxAfterDirect.map((message) => (
              <pre key={message.name} className="event-detail mono" style={{ whiteSpace: "pre-wrap" }}>
                {message.body}
              </pre>
            ))
          )
        ) : (
          <Empty>Not opened yet.</Empty>
        )}
      </StepCard>

      <StepCard
        number={4}
        title="The same agent, through Promtact"
        says="Same agent, same documents, same tools. The only change is that its calls now pass through the gateway."
        action="Run guarded"
        busy={busy === "gateway"}
        error={errors.gateway}
        onRun={() =>
          step("gateway", async () => {
            setGuarded(await api.runDemoAgent("gateway"));
            setOutboxAfterGuarded(null);
          })
        }
      >
        {guarded ? <RunSteps result={guarded} /> : <Empty>Not run yet.</Empty>}
      </StepCard>

      <StepCard
        number={5}
        title="What left this time"
        says="The poisoned document never reached the agent, so there was no instruction to obey - and the onward call is waiting for a person."
        action="Open the outbox"
        busy={busy === "outbox2"}
        error={errors.outbox2}
        onRun={() => step("outbox2", async () => setOutboxAfterGuarded(await api.demoOutbox()))}
      >
        {outboxAfterGuarded ? (
          outboxAfterGuarded.length === 0 ? (
            <div className="callout">
              <div style={{ fontWeight: 500 }}>The outbox is empty. Nothing left.</div>
            </div>
          ) : (
            outboxAfterGuarded.map((message) => (
              <pre key={message.name} className="event-detail mono" style={{ whiteSpace: "pre-wrap" }}>
                {message.body}
              </pre>
            ))
          )
        ) : (
          <Empty>Not opened yet.</Empty>
        )}
      </StepCard>

      <StepCard
        number={6}
        title="Every decision, with its reason"
        says="Not just that something was stopped: what was asked, what was decided, and on what grounds."
        action="Show the record"
        busy={busy === "audit"}
        error={errors.audit}
        onRun={() =>
          step("audit", async () => {
            const records: Audit[] = await api.audit();
            setAudit(records.filter((r) => r.action === "mcp.proxy" || r.action === "gateway.decide").slice(0, 6));
          })
        }
      >
        {audit ? (
          audit.length === 0 ? (
            <Empty>No gateway decisions recorded yet.</Empty>
          ) : (
            <table>
              <tbody>
                {[...audit].reverse().map((record) => (
                  <tr key={record.id}>
                    <td className="mono" style={{ width: "24%" }}>
                      {(record.metadata || {}).tool || "-"}
                    </td>
                    <td style={{ width: 130 }}>
                      <Badge tone={outcomeTone(record.outcome === "pending_approval" ? "held" : record.outcome)}>
                        {record.outcome === "pending_approval" ? "held" : record.outcome}
                      </Badge>
                    </td>
                    <td className="panel-note">
                      {(record.metadata || {}).result_reason || (record.metadata || {}).reason || "-"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )
        ) : (
          <Empty>Not shown yet.</Empty>
        )}
      </StepCard>

      <StepCard
        number={7}
        title="Why you can believe that record"
        says="An audit trail is only worth as much as its resistance to being edited afterwards - including by whoever runs the server."
        action="Verify"
        busy={busy === "chain"}
        error={errors.chain}
        onRun={() =>
          step("chain", async () => {
            const [chainState, witnessState] = await Promise.all([api.auditChain(), api.auditWitness()]);
            setChain(chainState);
            setWitness(witnessState);
          })
        }
      >
        {chain ? (
          <>
            <p>
              {chain.valid
                ? `The chain is intact: all ${chain.linked} records still hash to the one before.`
                : "The chain does not verify - a record has been changed or removed."}
            </p>
            {witness?.configured ? (
              <p>
                {witness.agrees
                  ? `An external witness, outside this server, agrees at record ${witness.witnessed_index}.`
                  : witness.diverged
                    ? `The witness holds record ${witness.witnessed_index} and this server cannot produce it. Something here was removed or rewritten.`
                    : `The witness is configured and holds record ${witness.witnessed_index}.`}
              </p>
            ) : (
              <p>
                No external witness is configured. Without one this chain detects
                accidental corruption and nothing more: anyone who can write to
                the database can rewrite every record and recompute every hash.
              </p>
            )}
            {witness?.configured && witness?.agrees ? (
              <p style={{ fontWeight: 500, maxWidth: "78ch" }}>
                That is the part worth taking away. Even an administrator with
                full access to this server and its database cannot quietly remove
                a decision from this record - the witness already holds an earlier
                head and refuses a chain that got shorter or changed.
              </p>
            ) : null}
          </>
        ) : (
          <Empty>Not verified yet.</Empty>
        )}
      </StepCard>

      {direct && guarded ? (
        <Panel title="In one sentence">
          <p style={{ maxWidth: "78ch" }}>
            {direct.sent && !guarded.sent ? (
              <>
                Content an agent did not author caused an internal document to be
                sent to <span className="mono">{direct.recipient}</span>. Through
                the gateway the same attempt was stopped, and every decision is on
                a record that cannot be quietly edited afterwards.
              </>
            ) : (
              // Derived from the runs, so a pair that does not show the contrast
              // says so rather than narrating a success that did not happen.
              <>
                This pair does not show the contrast: unguarded sent=
                {String(direct.sent)}, guarded sent={String(guarded.sent)}. Check
                both runs above before drawing a conclusion.
              </>
            )}
          </p>
          {onNavigate ? (
            <button className="btn" onClick={() => onNavigate("approvals")}>
              See the call that is waiting
            </button>
          ) : null}
        </Panel>
      ) : null}
    </>
  );
}
