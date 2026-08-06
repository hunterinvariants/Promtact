# Design decisions

Why this is built the way it is, including the alternatives that were rejected
and what each choice costs. Where a decision has a test that pins it down, the
test is named — those are the ones that would break if someone quietly undid the
reasoning.

## Enforcement

### The decision happens before the call, not after it

Most agent-security tooling watches tool calls and reports on them. That is a
log. If the agent has already sent the customer list to an attacker's endpoint,
knowing about it is not a control.

So the policy decision point sits inline: the call blocks on the verdict. The
cost is real — every tool call now has this service in its latency path, and if
this service is down, the agent stops. That tradeoff is the product. It is why
`promtactl bench` exists and why latency is a gate rather than a nice-to-have.

### A storage outage must not become an open door

The obvious failure mode of an inline enforcement point: the database goes away,
the service cannot record its decision, and it either fails open (an attacker's
dream) or fails closed on everything (an outage).

Neither. Decisions keep being made from in-memory policy and are appended to a
local journal with an fsync per record. When storage returns, the journal is
reconciled. A deny during an outage is still a deny, and the evidence still
exists afterwards.

The journal is bounded and *refuses* new records when full rather than dropping
the oldest. Silently discarding evidence is worse than refusing the call, because
the operator would never learn it happened.

Rejected: buffering in memory only. It survives a database restart but not a
process restart, and process restarts are more common.

This is the strongest claim in [technical-claims.md](technical-claims.md), and
it is demonstrated by injecting a real failure — replacing the journal's parent
directory with a regular file — rather than by mocking one.

### Content from a tool is data, never instruction

Anything an agent reads can carry text aimed at the model. A policy that could
be changed by the content it inspects is not a policy.

Practically: tool output never re-enters policy evaluation as configuration, and
the matcher is deliberately obfuscation-resistant rather than looking for exact
strings, because the interesting cases are the ones where someone tried not to
be matched.

## Identity

### People and machines are different kinds of account

Directory records declare a kind: `human` or `service`. This looks like
bookkeeping and is not.

Without it a second factor cannot be required of anyone. Turn MFA on for a
tenant and every agent authenticating with an API key breaks, so nobody turns it
on. Separating the two makes MFA enforceable against people while agents keep
working.

A service account can never hold a console session. The rule is enforced twice —
the directory query excludes it, and `MintSession` refuses it — because a future
login path that resolves an identity some other way would otherwise reintroduce
a permanent MFA-exempt door. Redundancy is the point.

An unrecognised or missing kind is read as `human`. Defaulting the other way
would hand any corrupted row the exemption machines carry.

Pinned by `TestServiceAccountCannotOpenASession`,
`TestUnknownKindDefaultsToHuman`.

### The second factor fails closed

If the enrolment state cannot be read, the login is refused. The alternative —
letting everyone in without a second factor precisely when the database is
unhealthy — turns a database problem into an authentication bypass.

Enrolment is not enforcement. A TOTP secret counts only once its owner has
produced a code from it, so a mistyped setup cannot lock someone out of their
own tenant. And a confirmed enrolment cannot be silently replaced, or anyone
holding a session could swap out the factor and take over the account.

A spent time step is claimed in the database. A TOTP code stays valid for its
whole thirty-second window; without the claim, a code captured mid-window is
replayable for the rest of it. Concurrent attempts elect exactly one winner
(`TestConcurrentTOTPStepClaimsElectOneWinner`), because the insert *is* the
lock — a read-then-write would be separable by a race.

Failure reasons stay distinguishable. Telling a user "wrong credentials" when a
code was merely missing makes them retype their key until the backoff locks them
out. The distinction leaks only that the first factor was correct, which whoever
holds that credential already knows.

### The identity cache only exists during an outage

Directory lookups hit the database on every request, so a revoked key or a
suspended tenant takes effect on the very next call. A general cache would put a
window on revocation, and revocation latency is the whole value of central
identity.

There is a cache, but it is consulted *only* after a lookup errors — which is
why `IdentityByTokenHash` returns `(Identity, bool, error)` and distinguishes
"this identity does not exist" from "the directory is unreachable". Only the
second may be answered from memory.

## Interfaces

### SCIM lives under `/api/`, not at `/scim/v2`

The authentication middleware guards `/api/` and `/metrics`. Everything else is
served without authentication, because everything else is the console.

Mounting SCIM at the conventional root would therefore have published a
provisioning API — create accounts, grant roles — with no authentication at all.
Identity providers accept an arbitrary base URL, so the conventional path buys
nothing. `TestNoProvisioningEndpointOutsideTheGuardedPrefix` fails if anyone
adds one there later.

Related: provisioning is admin-only for every method. Under the generic read
rule, a viewer could have enumerated a customer's entire user directory over
GET.

### Offboarding suspends and revokes; it does not delete

SCIM `DELETE` suspends the account and revokes its API keys in one transaction.
The record stays.

The security requirement is that the credentials stop working, and suspension
achieves that atomically. A deleted row takes its audit trail with it, which is
exactly the trail you want when the reason for the offboarding was suspicious
activity. Reactivation does not resurrect revoked keys — they may have been the
reason for the suspension.

### Only one SCIM filter form is supported

`userName eq "value"` and nothing else. A general filter language means a parser
for attacker-supplied expressions sitting in front of the directory, for no
provisioning benefit — identity providers use exactly this one filter.

The first draft of that parser accepted `userName eq "a" and roles eq "admin"`,
because it checked only that the value started and ended with a quote. The test
caught it. That is the argument for keeping the accepted grammar as small as
possible.

### The API is versioned by mirroring rather than by a cut

Every endpoint answers at both its original path and under `/api/v1`. Nothing
breaks — the validation suite, the LangChain connector, the console and deployed
agents all speak the original paths — while new integrations can pin a version.
Unversioned paths carry an RFC 8594 `Deprecation` header.

The rewrite happens *before* authentication. Authorization matches concrete
prefixes such as `/api/admin/`, so a versioned path arriving unrewritten would
miss those rules and fall through to the generic read rule, handing customer data
to any viewer. Canonicalising first means every existing rule keeps applying
unchanged.

## Dependencies

The project has two direct dependencies: `pgx` and a SAML library. That is a
deliberate constraint, not an accident, and it has been paid for three times.

### OpenTelemetry: protocol, not SDK

Distributed tracing speaks OTLP/HTTP JSON directly against any collector, Tempo
or Jaeger endpoint. The OpenTelemetry SDK would have added roughly twenty
modules including protobuf and gRPC.

For a product whose argument is a lean, auditable supply chain, multiplying the
dependency surface for a feature that is off unless an endpoint is configured is
a bad trade. OTLP's JSON encoding is documented and stable, so speaking it with
the standard library costs interoperability nothing.

Export is asynchronous and bounded. A dead collector adds no latency to an
enforcement decision and cannot grow memory without limit
(`TestDeadCollectorDoesNotAffectRequests`).

### TOTP: thirty lines, no dependency

RFC 6238 is HMAC and modular arithmetic. Taking a dependency for it would add a
supply-chain edge to the authentication path — the single place in this system
where a compromised package is worth the most to an attacker.

Correctness is not asserted, it is checked: the implementation is verified
against the RFC 6238 test vectors, which is what makes it interoperable with
real authenticator apps rather than merely self-consistent.

### Encryption at rest: composition of standard-library primitives

Most secrets here are verifiers and are stored only as hashes. A TOTP seed
cannot be — it has to be readable to check a code — so it is sealed with
envelope encryption: a per-record data key, wrapped by a key that never reaches
the database.

Per-record data keys mean recovering one does not unlock the table. The key id
travels with the ciphertext, so rotation rewraps small blobs rather than
re-encrypting every row, and old records stay readable while new ones use the new
key.

Removing a key that records still reference fails loudly and names it. Treating
it as a miss would surface as users mysteriously losing their second factor, with
nothing pointing at the cause.

A key that is configured and rejected stops startup. Continuing would write
plaintext while the operator believes the data is encrypted — the failure they
would never discover. For the same reason a short key is refused at
configuration time rather than at the first write: it produces the appearance of
encryption without the substance.

The key provider is an interface so a deployment can move wrapping into a KMS or
HSM without touching any code that reads or writes secrets. The provider that
ships holds keys in memory from the environment. That is weaker than a KMS, and
it is stated as such — what it still buys is separation of key from data, which
is the property that matters when a backup leaks.

## Things that turned out to be wrong

Kept here because the reasoning is more useful than the fix.

**`/metrics` was readable by any authenticated viewer.** The counters aggregate
every tenant — total decision volume, capacity, database state. In a
multi-tenant install that hands one customer the deployment's traffic. Worse,
the existing test asserted `viewer → 200`, so the leak was codified as intended
behaviour. Both were changed.

**`approved_tools` was parsed and then never applied.** The configuration was
written into the threat pack but not into the returned policy config, so the
setting silently did nothing. Found by checking that a configured value changed
an outcome, rather than that it was loaded.

**A failure-injection test was green on Windows and red on Linux.** It broke a
path before the application was constructed; Linux returns `ENOTDIR` where the
code expected `ErrNotExist`. Fixed by constructing first, then replacing the
parent directory. Portability of the *failure*, not just the success path.

**Integration tests used fixed token hashes.** Token hashes are globally unique
in the schema, so those tests passed on a fresh database and failed on every run
after that — green exactly once, then failing with no relation to the change
that appeared to cause it. Hashes are now derived per run.

**MCP tool calls carried no provenance.** `toolCallFromMCPRequest` never set the
tool fingerprint, which made MCP a way around pinned tools — the exact control it
was supposed to be behind. Multi-surface controls need a test per surface, not
one test on the surface that was written first.

## Claims not made

Listed in [technical-claims.md](technical-claims.md) and repeated here because
what a system does not do is part of its design: admission of never-seen agents
during an outage, cross-host journal durability, and attestation of the running
tool's code. Each is a real gap with a real reason, not an oversight.
