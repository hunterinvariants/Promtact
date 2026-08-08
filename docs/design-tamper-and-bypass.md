# Making the two hard claims true

Two properties decide whether this product is worth buying, and neither is true
today in the form a buyer would want:

1. The record cannot be altered by anyone, including whoever runs the server.
2. The agent cannot get to its tools except through the gateway.

What follows is how to get there. It is worth saying first why the marketing
words for these are the wrong words, because that decision shapes the design.

## Why not "tamper-proof" and "non-bypassable"

No system in this class claims either, and the ones that are actually trusted
claim something narrower.

Certificate Transparency underpins the whole web PKI. It does not claim logs
cannot be altered. It claims that alteration is *detectable by independent
monitors*, and it is trusted precisely because that claim survives scrutiny.
Blockchain systems do claim tamper-proof, and no enterprise security buyer
takes it seriously.

A security engineer hearing "tamper-proof" asks one question: what happens if
the operator has root and the database. If the answer is anything other than
"they still cannot do it without it being visible", the word was wrong and
everything after it is discounted. That is a worse outcome than never having
said it.

So the target properties are:

> **No single party can alter the record without it being detectable** — including
> the operator, including us.
>
> **The agent holds no credential that works anywhere except through the
> gateway** — so a route around it reaches nothing.

Both are stronger than they sound, both are achievable, and both survive the
question.

---

## Part 1: making the record unalterable in practice

### What is wrong now

The chain is hash-linked and the head is published to a witness. Two gaps:

**The server can recompute everything.** The hashes are plain SHA-256 over
record contents. Anyone able to write to the database can rewrite a record,
recompute every subsequent hash, and produce a chain that verifies. Only the
witness stops this, and only for the portion it has seen.

**The witness sees the head periodically.** Between two publications there is a
window in which recent records can be rewritten without the witness ever having
seen the version being replaced.

### Change 1: sign each record with a key the server cannot read

Move from a hash chain to a signed hash chain, where the signing key lives in
hardware or a managed KMS and is non-exportable.

```
record.hash      = SHA-256(prev_hash || record)          # as now
record.signature = KMS.Sign(record.hash)                  # new
```

The operator can still delete rows, and it is worth being precise about what
signing does and does not stop, because this is where the questioning goes.
Breaking the chain does not break a signature: the old signatures stay valid on
the records that remain. What signing prevents is producing a *replacement* —
forging a record needs the key, and the key cannot leave the KMS. So the attack
degrades from "rewrite history convincingly" to "delete and leave a hole", and a
hole is what the witness receipts turn into evidence. Producing a believable
history stops being "run an UPDATE" and becomes "obtain the signing key", which
is a different class of problem with its own audit trail on the KMS side.

Concretely: AWS KMS, GCP KMS or Azure Key Vault with an asymmetric key and
`Sign` permission only, or a local TPM 2.0 for on-premises. The verification key
is public, so a customer or auditor can check the chain without any access to
the deployment.

This is the single highest-value change. It costs one KMS call per audit record
— batch them, sign a Merkle root per batch, not per record, and the cost
disappears.

**What it still does not give you:** an operator who controls the KMS *policy*
can grant themselves signing rights. The mitigation is the same as everywhere
else in key management: the KMS is in a different account or subscription with
its own access control, and its own audit log records who signed what. Say that,
rather than implying the key is magic.

### Change 2: witness every batch, and store the countersignature

Today the witness holds a head. Make it *return a signed receipt* for each head
it accepts, and store that receipt alongside the chain.

```
POST /anchor { index, head }
  <- { index, head, witnessed_at, signature }   # signed by the witness
```

Now the local database contains proof of what a third party saw and when.
Removing a record means also producing a witness receipt for the shortened
chain, which requires the witness's key. A missing or non-matching receipt is
positive evidence of tampering rather than an absence of evidence.

This also closes the window: publish per batch with a bounded interval (say ten
seconds), and the unwitnessed window is bounded by that interval and stated.

### Change 3: more than one witness, at least one outside your control

One witness that the vendor runs is a vendor you have to trust. The property a
buyer actually wants is that *no single party* can rewrite history.

- One witness operated by the customer.
- One by the vendor.
- Optionally a public transparency log or a notary service.

Verification requires agreement. This is exactly the Certificate Transparency
arrangement, and it is why that system is credible without ever using the word
tamper-proof.

For an OEM buyer such as an MDR provider, this matters more than anything else
on this page: they can hold their own witness, so their evidence does not
depend on trusting the vendor. That is a selling point, not a concession.

### Change 3b: a public transparency log as one of the witnesses

A witness the vendor runs is a vendor the customer has to trust. A witness the
customer runs is one *they* have to keep running. A public transparency log is
neither: Sigstore's Rekor accepts a signed entry, timestamps it, and includes it
in a Merkle tree that anyone can audit and nobody can retroactively edit.

What gets published is the **root hash of a batch**, not the records. A hash
reveals nothing about what it covers, so the usual objection — a bank will not
put its audit trail in a public log — does not apply to the thing being
published. Say that plainly, because the objection is reflexive and the answer
disarms it.

What *is* revealed is metadata: that this organisation runs Promtact, and how
often it anchors. For a bank or a defence customer that alone can be
unacceptable, and the honest answer is to offer both and let them choose:

| Mode | Anchored to | Suits |
| --- | --- | --- |
| Public | Rekor, plus optionally a customer witness | SaaS, startups, anyone who benefits from not being trusted |
| Private | A witness the customer operates, and one the vendor operates | Regulated customers, anything where anchoring cadence is itself sensitive |
| Both | All of the above | The default worth recommending |

Two cautions before this is promised to anyone:

- Rekor was built for software supply-chain artifacts. Using it as a general
  anchoring service is legitimate but off-label; its rate limits, retention and
  availability guarantees need checking against a per-minute anchoring cadence
  before the feature is sold rather than after.
- A public log entry is permanent. Anchoring the wrong thing once cannot be
  undone, so what goes into an entry needs to be exactly a root hash and a key
  identifier, and nothing that could later turn out to be sensitive.

### Change 4: append-only storage for the audit stream

Postgres cannot prevent a superuser from deleting rows. Writing the audit stream
additionally to storage with an immutability lock — S3 Object Lock in compliance
mode, Azure immutable blobs, or a WORM appliance — means deletion is refused by
the storage layer itself for the retention period, not by the application asking
nicely.

The database stays the working copy. The locked store is the evidence copy.

### Change 5: resolve the retention conflict honestly

A hash chain and a deletion policy contradict each other, and this bit us in
production: retention pruned old records and the verification reported BROKEN,
indistinguishable from an attack.

The fix is a checkpoint. When records are pruned, record a signed checkpoint
containing the hash of the last removed record and the count removed.
Verification then starts from the checkpoint rather than from record one, and a
gap with no checkpoint remains what it should be — a finding.

### What the claim becomes

> Every decision is signed with a key that does not leave its hardware module,
> hash-linked to the decision before it, and countersigned by witnesses at least
> one of which you operate. Altering or removing a decision requires the signing
> key *and* the agreement of every witness. Absent that, the alteration is
> detectable by anyone holding the public verification key — including your
> auditor, without access to our systems.

That is a stronger statement than "tamper-proof" because it says what would have
to happen, and a reader can check each part.

---

## Part 2: making the gateway unavoidable

### What is wrong now

The gateway sees what passes through it. During testing an assistant whose tool
call was slow simply read the files from disk instead, and nothing was gated —
not because the gateway failed, but because it was never involved. Any agent
with a general-purpose capability has routes the gateway does not see.

Being "in the path" is a property of the deployment, not of the software. So the
software has to make the alternative routes useless.

### Change 6: the gateway holds the credentials, the agent does not — built

This is the change that does the work, and it is now in the product.

`promtactl credential set --tool <name>` installs a secret the gateway presents
upstream; the agent keeps only its gateway token. Selection is by exact tool
name, a `prefix_*` wildcard, or `*` as the tenant fallback, most specific first.
Secrets are sealed at rest with the existing envelope encryption, and the store
refuses to write one at all when no key is configured rather than quietly
putting a customer's production key in every backup.

Three properties are enforced by test rather than by intention:

- The secret reaches the tool and appears in no response, no audit record and
  no action metadata. What is recorded is a fingerprint - enough to answer
  "which credential did the agent use" during an investigation, useless to
  anyone hoping to reuse it.
- An agent presenting its gateway token directly to a tool that checks its own
  authentication is refused, while the same call through the gateway succeeds.
  Both halves are asserted, because a test showing only the refusal would pass
  just as well if the tool were simply unreachable.
- A call released by a person goes out under the same brokered credential it
  would have used had it been allowed outright. Otherwise approval would
  quietly change which identity the tool saw, and on an upstream that accepts
  only the brokered key every approved call would fail while every allowed one
  worked.

Deployments that have not adopted brokering fall back to the statically
configured upstream token and are unaffected.

The remaining gap is procedural and worth stating to a customer plainly: while
the agent still holds the original secret, none of this changes anything. The
credential has to be removed from the agent for the dead end to exist, and no
software can confirm that has happened.

Today an agent holds an API key for the tool it calls, and Promtact sits beside
that relationship. Invert it: **the tool credentials live in the gateway**, and
the agent holds only a token that is worthless anywhere except the gateway.

```
before:  agent ──[tool API key]──> tool
         agent ──> gateway ──> tool          (gateway optional in practice)

after:   agent ──[gateway token]──> gateway ──[tool API key]──> tool
         agent ─────────X─────────> tool     (no credential, refused)
```

An agent that finds another route now arrives without a credential and the tool
refuses it. Bypassing stops being a shortcut and becomes a dead end. This needs
no cooperation from the agent and no network controls, which is why it is the
right first change.

It also gives something valuable for free: credential rotation happens in one
place, and an agent's access can be revoked without touching the agent.

### Change 7: egress control, so the network agrees

Credential brokering handles tools that authenticate. It does nothing about a
file on the same disk, or an unauthenticated internal service.

For those, the agent runs where the gateway is the only reachable route:

- A container whose network namespace permits egress only to the gateway
  (Kubernetes NetworkPolicy, or an egress proxy as the sole route).
- No host filesystem mounts beyond a working directory.
- The tool servers reachable only from the gateway's network, not the agent's.

This is deployment work rather than product work, but the product should ship
the manifests that do it, because "configure your network correctly" is advice
nobody follows and everybody claims to.

### Change 8: attest what is calling

Credential brokering assumes the credential is held by the agent it was issued
to. Where the environment supports it, bind the credential to the workload:
SPIFFE/SPIRE identity, a Kubernetes projected service account token, or cloud
workload identity. Then a stolen gateway token used from elsewhere fails too.

### What the claim becomes

> The agent holds no credential for any tool. Its only credential is accepted by
> the gateway and by nothing else, so a route around the gateway arrives without
> authority. Where the environment allows it, that credential is additionally
> bound to the workload identity, so a copied token does not work from anywhere
> else.

Again: stronger than "non-bypassable", and it survives the follow-up question,
which will be "what if the agent has a shell". The answer is that it then has no
credentials, and if it can reach the tools unauthenticated then the tools have a
problem the gateway was never going to solve.

---

## Order of work

1. ~~**Credential brokering** (Change 6)~~ — **done**. Largest effect, no
   infrastructure dependency, turns bypass into a dead end.
2. **Witness receipts** (Change 2). Converts absence of evidence into evidence,
   and needs only the Cloudflare Worker that already exists. Ahead of KMS
   deliberately: the threat being defended against is the operator, and a key
   the operator's own server uses is a key the operator can eventually reach. A
   third party able to contradict the server is what bites in that case; a
   local signature is not.
3. **Signed records via KMS** (Change 1). Turns "the operator can rewrite this"
   into "the operator needs a key they do not have". Worth documenting as a
   customer option before running it ourselves - Cloud KMS is inexpensive but
   not free, and an OEM buyer will want their own key anyway.
4. **Retention checkpoints** (Change 5). Removes a permanent false alarm that is
   already live.
5. **Multiple witnesses** (Change 3). The claim that matters to an OEM buyer.
6. **Egress manifests and append-only storage** (Changes 4 and 7). Deployment
   assets.
7. **Workload attestation** (Change 8). Last, because it depends on the
   customer's platform.

## What will still not be true afterwards

Worth writing down before anyone asks in a meeting.

- An operator who controls the KMS policy can grant themselves signing rights.
  What they cannot do is make that invisible: the KMS records it.
- An agent that can execute arbitrary code inside its own container can do
  anything that container can do. The container's reach is the boundary, not
  the gateway.
- Prompt injection is still not reliably detectable. None of this changes that,
  and none of it needs to: the point of provenance and credential brokering is
  that detection is not what the guarantee rests on.
- A customer who runs the only witness on the same host as the gateway has one
  witness and no independence. The product should refuse to describe that as
  witnessed.
