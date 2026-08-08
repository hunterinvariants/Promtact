import { useEffect, useState } from "react";
import { api } from "../api";
import { Empty, Panel } from "../components";

// The policy, where the person it governs can change it.
//
// This was a JSON file on a server that only root could edit, which meant every
// adjustment to the central setting of the product went through us. A control
// nobody can configure is a control nobody owns.
//
// The page is written for the person deciding, not the person who wrote the
// schema: an approved tool is described as a permission rather than as a list
// entry, and removing one says what will happen to agents that call it.

type PolicyView = {
  approved_tools: string[];
  approved_egress_hosts: string[];
  agent_identities?: { agent_id: string; has_key: boolean }[];
  correlation_window?: string;
  editable: boolean;
  path?: string;
  note?: string;
};

function ListEditor({
  title,
  says,
  placeholder,
  values,
  onChange,
  disabled,
}: {
  title: string;
  says: string;
  placeholder: string;
  values: string[];
  onChange: (next: string[]) => void;
  disabled: boolean;
}) {
  const [draft, setDraft] = useState("");

  const add = () => {
    const value = draft.trim();
    if (!value || values.some((v) => v.toLowerCase() === value.toLowerCase())) {
      setDraft("");
      return;
    }
    onChange([...values, value]);
    setDraft("");
  };

  return (
    <Panel title={title}>
      <p className="panel-note" style={{ maxWidth: "72ch" }}>{says}</p>

      {values.length === 0 ? (
        <Empty>Nothing listed.</Empty>
      ) : (
        <table>
          <tbody>
            {values.map((value) => (
              <tr key={value}>
                <td className="mono">{value}</td>
                <td style={{ width: 90, textAlign: "right" }}>
                  <button
                    className="btn"
                    disabled={disabled}
                    onClick={() => onChange(values.filter((v) => v !== value))}
                  >
                    Remove
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      <div className="row" style={{ marginTop: 10, gap: 8 }}>
        <input
          className="input"
          value={draft}
          placeholder={placeholder}
          disabled={disabled}
          onChange={(event) => setDraft(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              event.preventDefault();
              add();
            }
          }}
        />
        <button className="btn" onClick={add} disabled={disabled || !draft.trim()}>
          Add
        </button>
      </div>
    </Panel>
  );
}

export default function Policy() {
  const [policy, setPolicy] = useState<PolicyView | null>(null);
  const [tools, setTools] = useState<string[]>([]);
  const [hosts, setHosts] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  useEffect(() => {
    api
      .policy()
      .then((current) => {
        setPolicy(current);
        setTools(current.approved_tools || []);
        setHosts(current.approved_egress_hosts || []);
      })
      .catch((err) => setError(err.message));
  }, []);

  const dirty =
    policy !== null &&
    (JSON.stringify(tools) !== JSON.stringify(policy.approved_tools || []) ||
      JSON.stringify(hosts) !== JSON.stringify(policy.approved_egress_hosts || []));

  const save = async () => {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const updated = await api.updatePolicy({ approved_tools: tools, approved_egress_hosts: hosts });
      setPolicy(updated);
      setTools(updated.approved_tools || []);
      setHosts(updated.approved_egress_hosts || []);
      setNotice("Applied. It takes effect on the next tool call, and the change is in the audit record with your name on it.");
    } catch (err: any) {
      setError(err.message);
    } finally {
      setBusy(false);
    }
  };

  if (!policy) {
    return error ? <div className="callout">⚠ {error}</div> : <Empty>Loading.</Empty>;
  }

  const locked = !policy.editable || busy;

  return (
    <>
      {error ? <div className="callout">⚠ {error}</div> : null}
      {notice ? <div className="callout">{notice}</div> : null}
      {policy.note ? <div className="callout">{policy.note}</div> : null}

      <Panel
        title="Changes"
        note={dirty ? "unsaved" : "saved"}
        actions={
          <button className="btn btn-primary" onClick={save} disabled={locked || !dirty}>
            {busy ? "Applying…" : "Apply"}
          </button>
        }
      >
        <p style={{ maxWidth: "72ch" }}>
          Nothing here takes effect until you apply it. Every change is recorded
          with what was added, what was removed, and who did it.
        </p>
      </Panel>

      <ListEditor
        title="Tools agents may call"
        says="A call to anything not on this list is refused before it reaches the tool. Removing one stops agents using it from the next call onward — they will see a refusal, not a failure."
        placeholder="tool name, e.g. read_document"
        values={tools}
        onChange={setTools}
        disabled={locked}
      />

      <ListEditor
        title="Destinations agents may reach"
        says="Anywhere else is treated as an unapproved destination and held for a person. Leaving this empty means every outward destination needs approval, which is safe and noisy."
        placeholder="host name, e.g. api.openai.com"
        values={hosts}
        onChange={setHosts}
        disabled={locked}
      />

      <Panel title="Registered agents" note={`${policy.agent_identities?.length || 0}`}>
        <p className="panel-note" style={{ maxWidth: "72ch" }}>
          An agent that cannot identify itself has its calls held for a person,
          however ordinary the tool. Registering one is how it works unattended.
        </p>
        {policy.agent_identities && policy.agent_identities.length > 0 ? (
          <table>
            <tbody>
              {policy.agent_identities.map((agent) => (
                <tr key={agent.agent_id}>
                  <td className="mono">{agent.agent_id}</td>
                  <td className="panel-note">{agent.has_key ? "has a key" : "no key set"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <Empty>
            None registered. Every agent's calls are held for a person until one
            is.
          </Empty>
        )}
      </Panel>
    </>
  );
}
