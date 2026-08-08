import { useEffect, useState } from "react";
import { api } from "../api";
import { Empty, Panel } from "../components";

// Connecting an agent, without asking us.
//
// This was a command the vendor ran on the customer's server. A customer who
// wanted a second agent had to open a ticket, and a prospect evaluating the
// product could not connect anything at all.
//
// The page gives back what somebody actually needs to finish the job: the
// address, the identity, the secret once, and the configuration to paste. A
// screen that says "agent created" and leaves the reader to work out the rest
// has done the easy half.

type Agent = { agent_id: string; has_key: boolean };

export default function Connect() {
  const [agents, setAgents] = useState<Agent[]>([]);
  const [name, setName] = useState("");
  const [created, setCreated] = useState<{ agent_id: string; secret: string; note: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [editable, setEditable] = useState(true);

  const load = () =>
    api
      .policy()
      .then((policy) => {
        setAgents(policy.agent_identities || []);
        setEditable(Boolean(policy.editable));
      })
      .catch((err) => setError(err.message));

  useEffect(() => {
    load();
  }, []);

  const register = async () => {
    setBusy(true);
    setError("");
    try {
      const result = await api.registerAgent(name.trim());
      setCreated(result);
      setName("");
      await load();
    } catch (err: any) {
      setError(err.message);
    } finally {
      setBusy(false);
    }
  };

  const remove = async (agentID: string) => {
    setBusy(true);
    setError("");
    try {
      await api.removeAgent(agentID);
      await load();
    } catch (err: any) {
      setError(err.message);
    } finally {
      setBusy(false);
    }
  };

  const base = window.location.origin;
  const endpoint = `${base}/api/mcp/proxy`;

  const snippet = created
    ? JSON.stringify(
        {
          mcpServers: {
            promtact: {
              type: "http",
              url: endpoint,
              headers: {
                Authorization: "Bearer YOUR_API_KEY",
                "X-Promtact-Agent-Id": created.agent_id,
                "X-Promtact-Agent-Token": created.secret,
              },
            },
          },
        },
        null,
        2,
      )
    : "";

  return (
    <>
      {error ? <div className="callout">⚠ {error}</div> : null}
      {!editable ? (
        <div className="callout">
          This deployment was started without a policy file, so agents cannot be
          registered here.
        </div>
      ) : null}

      <Panel title="How an agent connects">
        <p style={{ maxWidth: "76ch" }}>
          Point the agent&rsquo;s tool client at this gateway instead of at its
          tool server. Nothing about the agent changes, and nothing about its
          behaviour changes until a call is refused or held.
        </p>
        <dl>
          <div>
            <dt>Endpoint</dt>
            <dd className="mono">{endpoint}</dd>
          </div>
        </dl>
        <p className="panel-note" style={{ maxWidth: "76ch" }}>
          An agent that cannot identify itself has its calls held for a person,
          however ordinary the tool. Registering one below is how it works
          unattended.
        </p>
      </Panel>

      <Panel
        title="Register an agent"
        actions={
          <button className="btn btn-primary" onClick={register} disabled={busy || !editable || !name.trim()}>
            {busy ? "…" : "Register"}
          </button>
        }
      >
        <div className="row" style={{ gap: 8 }}>
          <input
            className="input"
            value={name}
            placeholder="agent id, e.g. research-agent"
            disabled={busy || !editable}
            onChange={(event) => setName(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter" && name.trim()) {
                event.preventDefault();
                register();
              }
            }}
          />
        </div>
      </Panel>

      {created ? (
        <Panel title={`${created.agent_id} — copy this now`}>
          {/* Shown once, and said plainly. A secret displayed without that
              warning is one somebody closes the tab on. */}
          <div className="callout">
            <div style={{ fontWeight: 500 }}>Secret</div>
            <div className="mono event-command">{created.secret}</div>
            <div className="panel-note">{created.note}</div>
          </div>

          <h4 style={{ marginTop: 14 }}>Configuration</h4>
          <p className="panel-note" style={{ maxWidth: "76ch" }}>
            For an MCP client. Replace <span className="mono">YOUR_API_KEY</span>{" "}
            with the key of the account this agent authenticates as — the agent
            identity proves which agent it is, the API key proves it may talk to
            this gateway at all.
          </p>
          <pre className="event-detail mono" style={{ whiteSpace: "pre-wrap" }}>
            {snippet}
          </pre>
        </Panel>
      ) : null}

      <Panel title="Registered agents" note={`${agents.length}`} bodyless>
        {agents.length === 0 ? (
          <Empty>
            None yet. Every agent&rsquo;s calls are held for a person until one
            is registered.
          </Empty>
        ) : (
          <table>
            <tbody>
              {agents.map((agent) => (
                <tr key={agent.agent_id}>
                  <td className="mono">{agent.agent_id}</td>
                  <td className="panel-note">{agent.has_key ? "has a key" : "no key set"}</td>
                  <td style={{ width: 100, textAlign: "right" }}>
                    <button className="btn" disabled={busy || !editable} onClick={() => remove(agent.agent_id)}>
                      Remove
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        <p className="panel-note" style={{ marginTop: 10, maxWidth: "76ch" }}>
          Removing an agent does not block it. Its calls are held for a person
          from the next one onward, which is a queue rather than an outage —
          worth knowing before you remove one on a Friday.
        </p>
      </Panel>
    </>
  );
}
