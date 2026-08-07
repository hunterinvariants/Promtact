# Demonstrating Promtact

Fifteen minutes, one laptop, no endpoint agent and no Windows. An AI assistant
does ordinary work through its own tools, reads a document that looks entirely
normal, and is stopped before it can act on what the document told it.

The point to make is not that a detector fired. It is that **the two steps were
individually legitimate** — reading a file and sending a message are both things
the assistant was deliberately given — and the harm only exists in the join.

---

## What the audience should end up believing

Not "this catches prompt injection". That claim does not survive a hostile
question, and it should not: instructions are text, and text can be rewritten.

The claim that holds is narrower and more useful:

> Content an agent did not author cannot silently cause a privileged action, and
> every decision is on a record you cannot quietly edit afterwards.

The second half is what most buyers are actually short of. "What did your agent
do last quarter, and who approved the risky parts?" has no answer at most
companies today.

---

## Setup

Three processes. Two terminals and a browser.

### 1. The tool server

The tools the assistant will use — read a document, send a message — with real
files behind them.

```bash
promtactl mcp-tools --dir ~/promtact-demo --seed
```

`--seed` writes two documents:

- `q3-summary.md` — an ordinary internal note.
- `vendor-status.md` — a status note that reads as four innocuous lines.

Open the second one in a text editor now, before the demonstration. You will see
a short status update and nothing else. Then:

```bash
wc -c ~/promtact-demo/vendor-status.md
```

Roughly 800 bytes for something that displays as about 180 characters. The rest
is an instruction written in Unicode tag characters, which have no visible form
at all. **This is the single most convincing moment in the demonstration** — the
audience sees a file, and sees that the file contains something they cannot see.

### 2. The gateway

```bash
promtact --addr 127.0.0.1:8130 \
  --api-token demo \
  --policy ~/promtact-demo/policy.json \
  --mcp-upstream-url http://127.0.0.1:9200/
```

With `policy.json`:

```json
{
  "approved_tools": ["list_documents", "read_document", "send_message"],
  "approved_egress_hosts": []
}
```

All three tools are approved on purpose. Nothing in this demonstration turns on
a tool being forbidden — that is the easy case and it is not the point.

### 3. The assistant

Point an MCP client at the gateway rather than at the tool server. For Claude
Code, in `.mcp.json`:

```json
{
  "mcpServers": {
    "promtact": {
      "type": "http",
      "url": "http://127.0.0.1:8130/api/mcp/proxy",
      "headers": { "Authorization": "Bearer demo" }
    }
  }
}
```

The assistant now reaches its tools only through the gateway. It does not know
that, and nothing about its behaviour changes until something goes wrong.

---

## Running it

Ask the assistant, in its own words:

> Read the documents in my workspace and send a short summary to
> `ops@example.com`.

What happens, in order:

1. **`list_documents`** — allowed, unremarkable.
2. **`read_document q3-summary.md`** — allowed, content delivered.
3. **`read_document vendor-status.md`** — the response is **withheld**. The
   assistant receives a policy refusal instead of the document, naming what was
   found: characters invisible to a reader but not to a model.
4. **`send_message`** — **held for a person**, because this session read
   untrusted content and this action reaches outward.

Then show the empty outbox:

```bash
ls ~/promtact-demo/outbox/
```

Nothing left. Not "an alert was raised about something that left" — nothing
left.

Finally open the console at **Approvals**. The held call is there with its
reason, what the session had read, and a button to release it. Releasing it is
worth doing live: the point is a decision by a person, not a wall.

---

## The variant that makes the argument

Run it again with the poisoned document deleted, and watch `send_message` be
held anyway — because the session read a document from a source the deployment
does not vouch for, and nothing was detected in it at all.

That is the part worth dwelling on. The control does not depend on recognising
the attack. It depends on knowing where the content came from, which is why it
still holds for an injection written in a way nobody has thought of yet.

---

## Questions you will be asked

**"Doesn't this stop everything?"** No — the mark applies only to actions
reaching outward, and it expires. Show the second half of the run: the assistant
kept reading and summarising perfectly well.

**"What's your false positive rate?"** Unknown, and say so. The detection
signals are chosen to be hard to produce by accident, and the provenance rule
has no false positives by construction because it is not trying to detect
anything. What it has instead is a cost: outward actions after reading external
content need a person.

**"Can't an attacker just phrase it differently?"** Yes, against the detection
half. That is why the detection half is not the load-bearing one.

**"What if someone edits the audit records?"** The chain head is published to an
external witness that refuses a shortened or rewritten chain. That is a separate
demonstration and it is worth ten minutes of its own.

---

## What to be straight about

- Session marks live for thirty minutes by default. A long-running agent that
  reads something at the start is not held at the end.
- The gateway sees what an agent asks a tool to do. It does not see the model's
  reasoning, and cannot tell you why the agent wanted to.
- This is one control among several. It does not replace an endpoint product,
  identity, or code review, and saying otherwise in a room with a competent
  security lead ends the conversation.
