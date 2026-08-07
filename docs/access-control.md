# Access control

Who can reach customer data, through which paths, and what is recorded when
they do.

This is written for a reader doing diligence, which means it names the gap as
plainly as the controls. A document that only lists strengths is one an auditor
stops trusting at the first thing they find that it omitted.

## Customer-facing access

Every request to `/api/` and `/metrics` is authenticated and authorised before
it reaches a handler.

| Role | May do |
| --- | --- |
| `viewer` | Read within their tenant |
| `ingestor` | Submit telemetry, request tool decisions |
| `analyst` | Read audit records, work the approval queue |
| `operator` | Approve response actions, manage deception tokens |
| `admin` | Tenant administration, provisioning, key management |

Three properties are worth naming because they are the ones usually missing:

**Authorisation is central, not per-handler.** `RequiredRoles` maps method and
path to the roles allowed, so a new handler cannot forget its own check. It
matches admin prefixes *before* the generic read rule — a mistake in that order
once let any viewer read platform-wide metrics, which is why the ordering now
has a test.

**The tenant comes from the credential, never the request.** Directory lookups
put the tenant in the SQL query rather than checking ownership afterwards, so
another customer's record identifier resolves to "not found" instead of
confirming it exists.

**Revocation is immediate.** Identity is resolved from the database on every
request rather than cached, so a revoked key or suspended tenant stops working
on the next call. The only cache is consulted when the database is *unreachable*,
and it has a bounded lifetime.

## Machine identity

Agents authenticate as service accounts: bearer key only, never an interactive
session. Human accounts can be required to present a second factor without
breaking any agent, because the two are different kinds of record. An account
whose kind is missing or unrecognised is treated as human, so a corrupted row
cannot inherit the exemption machines carry.

## Operator access to the hosted deployment

This is the part a buyer will ask about.

**Who.** One person operates the hosted deployment. There is no second
administrator, and therefore no four-eyes control on operator actions.

**How.** Access is by SSH key to the virtual machine. Password authentication is
disabled. From there, `root` can read the Postgres database directly.

**What is recorded.**

| Path | Audited by Promtact | Recorded elsewhere |
| --- | --- | --- |
| Admin API (provisioning, tenants, keys) | Yes, in the tamper-evident chain | — |
| Console session | Yes | — |
| Direct `psql` against the database | **No** | Postgres and shell logs on the host |
| Filesystem access to backups | **No** | Shell history |

**The gap, stated plainly: an operator with server access can read customer data
without producing an entry in the application's audit chain.** The host keeps
shell and database logs, but those are written on the same machine the operator
controls, so they are a deterrent rather than tamper-evident evidence.

This is the honest position of nearly every service of this size. A control
plane that gates agent access to tools has an obvious argument for gating its
own operators the same way, so half of it has been built and the other half is
named rather than glossed over.

**What has been built against it.** The audit chain head is published to an
external witness — a Cloudflare Worker with its own storage, in a different
trust domain from the host. The witness refuses a chain that got shorter and
refuses a different head for an index it already recorded. An operator can still
rewrite local history and recompute the local anchor over it; what they cannot
do is make the witness agree. The disagreement is reported at
`GET /api/audit/witness`, exposed as `promtact_audit_witness_diverged`, and
recorded in the chain itself.

The divergence flag is deliberately sticky: it is cleared only by a verification
that agrees, never by the next successful publish. Otherwise rewriting history
and waiting one interval would silence the alarm.

This closes the *erasure* half of the problem and not the *reading* half. Both
are stated because a reader will work out the difference anyway.

**Compensating controls today.**

- The audit chain is hash-linked, so records cannot be edited or removed after
  the fact without the chain failing validation. An operator can *read* without
  a trace; they cannot quietly *rewrite* history.
- The chain's validity is exposed as a metric and checked by an endpoint, so
  tampering surfaces rather than staying silent.
- Backups are root-only and stored outside the application's reach.
- Encrypted values are sealed with a key held outside the database, so a stolen
  dump alone does not decrypt them — where that encryption is enabled.

## Credential handling

API keys and recovery codes are stored only as SHA-256 hashes; the plaintext is
shown once at creation and cannot be recovered afterwards. Session cookies are
HMAC-signed, `HttpOnly`, and revocable server-side so a logout cannot be
replayed. Failed logins back off exponentially, keyed by source, and a failed
second factor counts against the same backoff so a code cannot be brute-forced
once the first factor is known.

Secrets reach the service through an environment file readable only by root and
the service account. No secret is committed to the repository; the release
workflow signs artifacts with keyless signing rather than a stored key.

## Separation of environments

Integration tests run against a dedicated database, configured separately from
production. This exists because the separation was once absent and a test run
altered the production schema — the incident is recorded in
[incident-response.md](incident-response.md) as the worked example.

## Review

Access rights are reviewed when they change rather than on a calendar, because a
scheduled quarterly review by the only person who holds the access would be
theatre. What is real: every provisioning action lands in the audit chain, and
the chain can be validated independently at any time.
