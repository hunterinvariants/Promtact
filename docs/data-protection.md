# Data protection

What Promtact processes, where it is kept, how long, and who can reach it.

This document describes the software as it is built and the hosted deployment as
it is operated today. Where a control is implemented but not switched on, that
is stated rather than glossed over — a reader can verify every claim here
against the code and the tests named alongside it.

## Two roles, two answers

**Self-hosted.** The customer runs the binary on their own infrastructure. All
data stays there. Promtact receives nothing: there is no telemetry, no phone
home, no licence check that transmits usage. The customer is controller and
operator both, and this document describes only what the software does with
their data.

**Hosted.** Promtact operates the service on the customer's behalf. Promtact is
then a processor: the customer decides what is sent and why, Promtact processes
it only to provide the service and only on documented instructions.

## What the system sees

Promtact inspects agent tool calls. The fields it stores are these, and their
sensitivity differs sharply:

| Field | Content | Note |
| --- | --- | --- |
| `Timestamp`, `Tenant`, `Kind` | Metadata | Not personal |
| `AssetID`, `Hostname` | Machine identifiers | Can identify a workstation, and therefore a person |
| `Actor` | The agent or account making the call | Often a person's account name |
| `SourceIP` | Origin of the request | Personal data under GDPR |
| `Process`, `ToolName` | What ran | Not usually personal |
| `Command` | **Tool call arguments** | Arbitrary; see below |
| `Destination` | Egress target | Can reveal behaviour |
| `Evidence` | Why a rule fired | Excerpts of the above |

**The `Command` and `Evidence` fields are the ones that matter.** A tool call's
arguments are whatever the agent passed — a database query, a file path, an
email body, a customer record. Promtact cannot know in advance what a customer's
agents handle, so it must be assumed that these fields can contain personal
data, including special categories.

This is inherent to the function. A control that decides whether a tool call may
proceed has to see the call. What follows from it is that the retention window
and the access controls below are the real protections, not a claim that no
personal data is processed.

## Retention

Events, alerts, response actions and audit records are deleted after the
retention window, which defaults to **30 days** and is set per deployment with
`--retention-window` or `PROMTACT_RETENTION_WINDOW`.

Deletion is by age, applied to all four record types. There is no separate
archive: expired records are gone from the database, not moved.

Two things are deliberately excluded from that sweep and kept until the tenant
is deleted: tenant accounts with their users and API key hashes, and the audit
chain state. The chain records the hash of each audit entry; discarding it would
break the tamper-evidence of everything that remains.

## Encryption

**In transit.** The hosted deployment terminates TLS at Cloudflare and reaches
the origin over an outbound-only tunnel; the service itself binds to loopback
and is not reachable from the internet directly. Self-hosted deployments are
responsible for their own transport security.

**At rest.** Values that are verifiers — API keys, recovery codes, session
material — are stored only as SHA-256 hashes and cannot be recovered. Values
that must be readable to work, currently only TOTP seeds, are sealed with
envelope encryption: a per-record data key wrapped by a key that is not in the
database (see [design-decisions.md](design-decisions.md)).

**Not enabled by default, and not enabled in the current hosted deployment.**
Envelope encryption is opt-in via `PROMTACT_ENCRYPTION_KEYS`. Full-database
encryption at rest is the responsibility of the underlying storage and is not
provided by Promtact. This is a real gap and is listed as such in
[access-control.md](access-control.md).

## Location

The hosted deployment runs on a single virtual machine in Europe, with Postgres
on the same host. Cloudflare terminates TLS at the edge closest to the visitor;
edge nodes do not store request content.

No data is transferred to processors outside the deployment except where a
customer configures an outbound integration themselves — a webhook, a ticket
system, a SIEM. Those destinations are the customer's choice and their
responsibility.

## Deletion and export

Tenant offboarding suspends the account and revokes its credentials in one
transaction; the records stay until the retention window expires or the tenant
is deleted outright. Deleting a tenant removes its users, keys, MFA enrolments
and usage records by cascade.

Audit records are the exception and are kept for their retention window even
after a tenant is suspended, because they are the evidence of what happened
before the suspension.

Export is available through the API for alerts, events, actions and audit
records. There is no self-service "download everything" button; a request is
fulfilled manually.

## Subject rights

For the hosted service, requests under GDPR or the Swiss FADP are handled by
the customer as controller. Promtact assists as processor: locating records for
an identified subject, correcting them, or deleting them ahead of the retention
window.

Because `Command` and `Evidence` can contain free-form content, a deletion
request may require searching those fields rather than a keyed lookup. That is
slower and it is worth saying so rather than implying a clean index exists.

## Sub-processors

| Provider | Purpose | Data reached |
| --- | --- | --- |
| Cloudflare | TLS termination, tunnel, alert receiver | Request metadata in transit; alert summaries |
| Hosting provider | Virtual machine and storage | Everything, at the infrastructure layer |

Customer-configured integrations (GitHub, Jira, ServiceNow, webhooks) are not
sub-processors of Promtact: the customer chooses them and holds the
relationship.

## What is not claimed

- No SOC 2 or ISO 27001 certification.
- No third-party penetration test.
- No contractual uptime commitment.
- Full-disk or full-database encryption at rest is not provided by Promtact.
- Operator access to the hosted database is not fully audited; see
  [access-control.md](access-control.md).

## Contact

Data protection enquiries: **privacy@promtact.com**
Security reports: see [SECURITY.md](../SECURITY.md).
