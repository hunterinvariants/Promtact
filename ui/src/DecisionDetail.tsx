import { useEffect } from "react";

// One decision, in the order that proves it.
//
// What came back, what the agent then wanted to do, what the gateway decided
// and on what grounds, and the record it sits on. That order is the causality,
// and a JSON dump does not have it - a reader has to reconstruct the argument
// themselves and mostly does not bother.
//
// The hidden instruction is shown decoded and marked. An analyst is used to
// hunting through text for something wrong; here the thing that was wrong was
// invisible by construction, and showing it plainly is the difference between
// a claim about a threat and seeing it.

type Decision = {
  id: string;
  type?: string;
  reason?: string;
  created_at?: string;
  approval_status?: string;
  execution_status?: string;
  metadata?: Record<string, string>;
};

function Section({ step, title, children }: { step: number; title: string; children: React.ReactNode }) {
  return (
    <section className="detail-step">
      <h4>
        <span className="detail-step-number">{step}</span>
        {title}
      </h4>
      {children}
    </section>
  );
}

export default function DecisionDetail({
  decision,
  onClose,
}: {
  decision: Decision;
  onClose: () => void;
}) {
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  const meta = decision.metadata || {};
  const findings = (meta.result_findings || "").split(",").filter(Boolean);
  const taint = (meta.result_taint || "").split(",").filter(Boolean);
  const hiddenText = meta.evidence_hidden_text;
  const hiddenCount = meta.evidence_hidden_unicode;
  const outcome =
    meta.result_reason || decision.reason || decision.execution_status || decision.approval_status || "-";

  return (
    <div className="modal-backdrop" onClick={onClose}>
      <div className="modal" onClick={(event) => event.stopPropagation()} role="dialog" aria-modal="true">
        <header className="modal-head">
          <div>
            <h3>{meta.tool || decision.type || "tool call"}</h3>
            <div className="panel-note">
              {decision.created_at ? new Date(decision.created_at).toLocaleString() : ""}
              {meta.actor ? ` · ${meta.actor}` : ""}
            </div>
          </div>
          <button className="btn" onClick={onClose}>
            Close
          </button>
        </header>

        <div className="modal-body">
          {hiddenText || hiddenCount ? (
            <Section step={1} title="What came back from the tool">
              {hiddenCount ? <p className="panel-note">Found in the response: {hiddenCount}.</p> : null}
              {hiddenText ? (
                <>
                  <p className="panel-note">
                    Invisible to anyone opening that document. This is what the
                    model would have read:
                  </p>
                  {/* Rendered as text, never as markup. It is attacker-written
                      content and the console is not going to interpret it. */}
                  <div className="hidden-payload mono">{hiddenText}</div>
                </>
              ) : null}
            </Section>
          ) : null}

          <Section step={hiddenText || hiddenCount ? 2 : 1} title="What the agent asked for">
            <dl>
              {[
                ["Tool", meta.tool],
                // meta.command on an MCP call is the raw JSON-RPC params, which
                // is not something a person reads. The arguments say the same
                // thing in a form they do.
                ["Command", meta.command && !meta.command.startsWith("{") ? meta.command : ""],
                ["Arguments", meta.arguments],
                ["Destination", meta.destination || meta.mcp_upstream_url || meta.proxy_upstream_url],
                ["Agent", meta.agent_id],
                ["Asked by", meta.actor],
              ]
                .filter(([, value]) => value)
                .map(([label, value]) => (
                  <div key={label as string}>
                    <dt>{label}</dt>
                    <dd className="mono">{value as string}</dd>
                  </div>
                ))}
            </dl>
          </Section>

          <Section step={hiddenText || hiddenCount ? 3 : 2} title="What the gateway decided">
            <p style={{ fontWeight: 500 }}>{outcome}</p>
            {findings.length ? (
              <>
                <p className="panel-note">Signals that fired:</p>
                <ul className="detail-list">
                  {findings.map((finding) => (
                    <li key={finding} className="mono">{finding}</li>
                  ))}
                </ul>
              </>
            ) : null}
            {taint.length ? (
              <>
                <p className="panel-note">What this session had already read:</p>
                <ul className="detail-list">
                  {taint.map((mark) => (
                    <li key={mark} className="mono">{mark}</li>
                  ))}
                </ul>
              </>
            ) : null}
            {/* The two verdicts are separate and saying so is the product's
                whole argument. "Verdict allow" printed next to "withheld" reads
                as a contradiction; what actually happened is that the request
                was unremarkable and the answer was not, which is precisely why
                gating requests alone does not work. */}
            {meta.verdict ? (
              <p className="panel-note">
                On the request: <strong>{meta.verdict}</strong>
                {meta.risk ? ` · risk ${meta.risk}` : ""}
                {decision.execution_status === "withheld"
                  ? " - the call itself was unremarkable. What came back was not."
                  : ""}
              </p>
            ) : null}
          </Section>

          <Section step={hiddenText || hiddenCount ? 4 : 3} title="The record this sits on">
            <dl>
              <div>
                <dt>Record</dt>
                <dd className="mono">{decision.id}</dd>
              </div>
              {meta.request_id ? (
                <div>
                  <dt>Request</dt>
                  <dd className="mono">{meta.request_id}</dd>
                </div>
              ) : null}
              {meta.tenant ? (
                <div>
                  <dt>Tenant</dt>
                  <dd className="mono">{meta.tenant}</dd>
                </div>
              ) : null}
            </dl>
            <p className="panel-note" style={{ maxWidth: "70ch" }}>
              This decision is one link in the hash chain shown in the header.
              Removing or altering it changes every hash after it, which is what
              the external witness refuses.
            </p>
          </Section>
        </div>
      </div>
    </div>
  );
}
