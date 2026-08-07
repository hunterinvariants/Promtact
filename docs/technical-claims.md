# Verifiable Technical Claims

Public claims are valid only when their acceptance command passes for the exact commit and named environment. Results must not be generalized across hardware, policies, databases, or releases.

## 1. Identity and provenance fail closed

**Claim.** When agent identities or tool provenance are configured, Promtact denies invalid identity tokens and mismatched tool fingerprints, and requires approval for missing or unknown claims. Verified identity never suppresses content detection.

```powershell
go test ./internal/policy -run 'Test.*(Identity|Provenance)' -count=1
go test ./internal/server -run 'Test.*Provenance' -count=1
go test ./internal/server -run 'TestIdentityCache|TestUnknownIdentity' -count=1
cd examples/langchain; python -m unittest test_promtact_gateway
```

Provenance is enforced identically on every surface an agent can reach: the direct tool-call API, the MCP reverse proxy (where the claim travels in the request's `_meta`), and the LangChain connector. That matters because a single unattested surface would be the way around a pinned fingerprint.

Scope: attestation is a claim the client presents, so it binds a tool build to a caller that possesses the fingerprint; it is not a measurement of the running tool's code.

## 2. Source-to-sink flows survive covered obfuscation and chain context

**Claim.** Promtact identifies sensitive sources and action/external sinks, emits flows in decision metadata, and escalates direct or history-linked flows. Encodings in the versioned corpus do not bypass enforcement.

```powershell
go test ./internal/policy -run 'Test.*(Taint|History|Obfuscat)' -count=1
```

Scope: deterministic terms and decoding variants in the corpus, not universal information-flow tracking.

## 3. Sequence-aware decisions are deterministic and explainable

**Claim.** Calls sharing a run/session/agent history key affect later risk within bounded memory; unrelated keys remain isolated. Decisions return verdict, reason, risk, findings, taint context, and request ID.

```powershell
go test ./internal/policy -run 'Test.*Gateway.*(History|Sequence|Reason|Metadata)' -count=1
go test ./internal/server -run 'Test.*(Gateway|MCP|Tenant)' -count=1
```

Scope: in-process history; cross-region sequence state is not implemented.

## 4. Detection behavior is reproducibly regression-tested

**Claim.** `promtactl validate` runs the named `promtact-agent-security-v1` synthetic suite, separates false positives from missed enforcement, records reasons, and exits non-zero on regression.

```powershell
go run ./cmd/promtactl validate --url http://localhost:8080 --json --output evidence/validation.json
go test ./cmd/promtactl -run TestValidation -count=1
```

Scope: curated synthetic regression corpus, not an independent held-out scientific benchmark.

## 5. Inline latency is measured against release gates

**Claim.** On a named environment, Promtact reproduces raw-policy and HTTP latency measurements and fails when declared P99 or throughput objectives are missed. Evidence must name commit, CPU, Go version, policy, concurrency, sample size, percentiles, throughput, and errors.

```powershell
go test ./internal/policy -bench GateToolCall -benchmem -count=5
go run ./cmd/promtactl bench --url http://localhost:8080 --requests 10000 --concurrency 32 --max-p99 25ms --min-rps 1000 --json
```

Thresholds above are example release targets, not universal performance claims.

## 6. Enforcement survives a storage outage without losing decisions

**Claim.** When durable storage is unavailable, the gateway keeps deciding and keeps enforcing: a deny stays a deny and an approval requirement stays an approval requirement, served to the caller rather than replaced by a server error. Records that cannot be persisted are written to a local fsynced journal and replayed into storage on recovery, so the audit trail is delayed but not lost. On healthy storage nothing is journalled and behavior is unchanged.

```powershell
go test ./internal/server -run 'Test(Deny|Approval|Journal|Healthy)' -count=1
go test ./internal/config -run 'Test.*(Signed|Signature|Tampered)' -count=1
```

The acceptance test injects a real write failure rather than mocking one: the store is pointed at a path whose parent cannot be created, so every write fails the way it would during an outage.

Why this matters: answering a denial with `500` is not a neutral failure. A caller that treats an error as "gateway unavailable" would proceed with exactly the call that was just blocked, turning a storage incident into a security bypass.

Scope and limits, stated deliberately:

- **Authentication continues for agents already seen.** A directory identity resolved before the outage stays usable for `--identity-cache-ttl` (default five minutes); an identity never seen is rejected, because failing closed is the only safe answer when the directory cannot be consulted. The cache is consulted *only* after a directory failure, so while the database is healthy every request is resolved against it and a revoked key stops working immediately. The added exposure is narrow: revocation writes to the same database, so nothing can be revoked during an outage anyway — the only widened window is a key revoked shortly before the outage began. Set the TTL to `0` to disable the fallback and fail closed immediately.
- **The policy the server restarts with is verified.** With `PROMTACT_POLICY_HMAC_SECRET` set, `policy.json` carries a detached signature and a missing or altered document stops startup instead of taking effect. This matters most exactly here: a restart during an outage reads that file as the only source of approved tools, principals, roles, pinned fingerprints and agent identities.
- **The journal is local.** It survives a process restart, but not the loss of the host it runs on.
- **The journal refuses new records when full** rather than rotating, because discarding the oldest security records is the loss it exists to prevent. Decisions are still served and enforced; the refusal is counted in `promtact_decision_journal_dropped_total`.
- **Pending approvals cannot be granted during an outage,** since the action is not in storage. That is the safe direction: the call keeps waiting.

Operational signals: `promtact_degraded_mode`, `promtact_decision_journal_depth`, `promtact_decision_journal_dropped_total`.

## 7. The operator cannot erase the evidence trail unnoticed

**Claim.** The audit chain is hash-linked and its head is published to a witness in a different trust domain from the host. The witness refuses a chain that got shorter and refuses a different head for an index it already recorded. An operator holding root can rewrite local history and recompute the local anchor over it; they cannot make the witness agree, and the resulting disagreement is reported, exported as a metric, and itself written into the chain.

```powershell
go test ./internal/server -run 'Test(Anchor|Witness|Truncated|Rewritten|Divergence)' -count=1
```

Against a live deployment, the property is the refusals rather than the storage. Both probes below are safe: the witness rejects them, so nothing is overwritten.

```bash
# A shortened chain is refused, naming the index it already holds.
curl -s -X POST "$WITNESS/anchor" -H "Authorization: Bearer $TOKEN"   -H "Content-Type: application/json" -d '{"chain_index":5,"head":"deadbeef","valid":true}'

# The same index with a different head is refused, naming both heads.
curl -s -X POST "$WITNESS/anchor" -H "Authorization: Bearer $TOKEN"   -H "Content-Type: application/json" -d '{"chain_index":62,"head":"0000...","valid":true}'
```

Why this matters: a locally anchored chain is only as trustworthy as the host. Truncation in particular is invisible to a local anchor, because a shortened history re-anchors against itself perfectly well. The witness is what makes the length itself a fact someone else holds.

Scope and limits, stated deliberately:

- **This closes erasure, not reading.** An operator with host access can still read customer data without producing an audit record. Nothing on a single host can prevent that, and the claim is not made. See [access-control.md](access-control.md).
- **The divergence flag is sticky.** It is cleared only by a verification that agrees, never by the next successful publish — otherwise rewriting history and waiting one interval would silence the alarm.
- **The witness is not a dependency of enforcement.** An unreachable witness neither fails a request nor stops the service; it degrades the evidence, not the control.
- **One publisher is assumed.** The witness store is eventually consistent, so two publishers racing could both be accepted. It is a witness, not a distributed ledger.
- **Witnessing is off unless configured.** Without `--audit-witness-url`, the chain is anchored locally only, which does not protect against an operator.

Operational signals: `promtact_audit_witness_diverged`, `promtact_audit_witnessed_index`, and `GET /api/audit/witness`.

## 8. Operator access to the database is announced, observed, and reconciled

**Claim.** Direct database access by the operator is announced before it is taken, observed independently of the operator, and the two are reconciled. An announcement is written to the audit chain and carried to the external witness. Observation comes from Postgres's own connection log, read by a shipper whose credential can do nothing else. A session with no announcement covering it produces an audit record and an alert. The findings are derived from those records, so restarting the service does not reset what an auditor sees.

```powershell
go test ./internal/server -run 'Test(Breakglass|Covers|Unannounced|Announced|ServicesOwn|Shipper|Reporter|FindingsSurvive)' -count=1
```

Against a live deployment the property is that it fires in one direction and stays quiet in the other. A control that only does the first is an alarm nobody keeps.

```bash
# Connect without announcing: both counters rise.
psql "$DSN" -c 'SELECT 1'
curl -s "$URL/api/access-log" -H "Authorization: Bearer $REPORTER_TOKEN"

# Announce, then connect: observed rises, unannounced does not.
promtactl breakglass --reason "..." --minutes 5
psql "$DSN" -c 'SELECT 1'
curl -s "$URL/api/access-log" -H "Authorization: Bearer $REPORTER_TOKEN"
```

Measured on the hosted deployment: 5/3 → 7/5 unannounced (+2 observed, +2 unannounced, one connect and one disconnect), then 7/5 → 9/5 announced (+2 observed, +0 unannounced).

Why this matters: an audit trail that records what customers do and not what the provider does answers the wrong half of the diligence question. This is the half a buyer asks about and almost nobody at this scale can demonstrate.

Scope and limits, stated deliberately:

- **This does not prevent an operator from reading data.** On a single host with one root user nothing can. It makes reading attributable and erasure detectable, and the claim is bounded to that.
- **Announcing is voluntary.** The pair is what closes it: skipping the announcement leaves the session observed and unreconciled, which is the finding.
- **The shipper runs on the host and can be stopped.** Silence after it has reported is surfaced as `shipper_silent`; the gap between stopping it and noticing it is real.
- **Open announcements are held in memory.** A restart clears them, so sessions during that window read as unannounced — noise rather than silence, which is the right direction. The findings themselves are durable.
- **The reporter credential can only report.** It reaches neither provisioning, nor break-glass, nor the witness, nor the gateway; the test enumerates what it must not touch.
- **A two-minute grace applies either side of a window,** because the log reports whole seconds while the window opens with nanoseconds. Every second of it excuses a real session, so it is capped by a test.

Operational signals: `GET /api/access-log`, audit actions `operator.breakglass.opened`, `operator.breakglass.closed` and `operator.database.session`.

## Claims intentionally not made

Promtact does **not** claim: admission of agents never seen before while the directory is down (those fail closed by design); cross-host durability of the decision journal; attestation of a running tool's actual code, since provenance verifies a claim the client presents rather than measuring the binary; or prevention of an operator with host access reading customer data, which no single-host design can offer.
