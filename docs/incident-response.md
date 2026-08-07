# Incident response

What happens when something goes wrong, who does it, and by when.

This plan is written for the operation that exists: one person, one hosted
deployment, and customers who self-host. A plan that describes a rota and a
war room would fail on its first use and would not survive a single question in
diligence. What follows is meant to be executed.

## Severity

| | Meaning | Example | First response |
| --- | --- | --- | --- |
| **S1** | Customer data exposed or enforcement bypassed | An authorisation flaw lets one tenant read another's records | 1 hour |
| **S2** | Service down, or a control silently not working | Enforcement failing open; the console unreachable | 4 hours |
| **S3** | Degraded, control intact | Alert delivery failing; validation suite regressing | 1 business day |
| **S4** | Defect without security or availability impact | A wrong label in the console | Next release |

Severity is assigned on first assessment and revised as facts arrive. **When
unsure between two levels, take the higher one** — the cost of over-reacting is
an hour; the cost of under-reacting is discovering the real severity from a
customer.

## Detection

| Source | Covers |
| --- | --- |
| Hourly detection-validation suite | A control that stopped working — the suite fails, systemd fires the alert unit, the alert reaches a phone |
| `promtact_*` metrics | Degraded mode, journal depth, audit chain validity, backpressure |
| Audit chain validation | Tampering or corruption of the evidence trail |
| systemd unit failure | The service died or could not start |
| Vulnerability reports | Third parties, via the address in [SECURITY.md](../SECURITY.md) |

The validation suite is the important one: it exercises fourteen ATT&CK-mapped
techniques against the live gateway every hour and fails loudly if a verdict
changes. It is how a control that quietly stopped enforcing becomes visible
within the hour instead of at the next audit.

## The procedure

**1. Contain before diagnosing.** If enforcement may be bypassed or data may be
exposed, stop the exposure first: revoke the credential, suspend the tenant,
stop the service. A service that is down is recoverable; data that has left is
not. Diagnosis is faster afterwards anyway, because the system stops changing.

**2. Preserve evidence.** Before restarting anything, capture the journal
(`journalctl -u promtact`), the current audit chain head and its validity, and a
database snapshot. Restarting a service is the most common way an incident's
evidence is lost.

**3. Assess scope.** Which tenants, which records, over what period. The audit
chain answers who did what; the retention window bounds how far back the
question can be answered at all.

**4. Fix, then verify with a test.** A fix without a regression test is an
invitation to the same incident. The test is written before the incident is
closed, not afterwards.

**5. Notify.** See below.

**6. Write it up within five working days.** What happened, when it was
detected, what the impact was, what was done, what changed so it cannot recur.
Kept in this repository, because the ones worth reading are the ones with a
commit next to them.

## Notification

**Customers, hosted deployment.** For any incident where customer data was or
may have been exposed: within **72 hours** of becoming aware, by email to the
tenant's registered administrator. The first message goes out even when the
picture is incomplete, and says so — waiting for certainty is how the 72 hours
get missed.

**Supervisory authority.** Where Promtact acts as processor, the controller
notifies; Promtact provides the facts they need without delay. Where Promtact is
itself controller, notification follows GDPR Article 33 and the Swiss FADP.

**Self-hosting customers.** A security defect in the software is disclosed
through a release and a security advisory on the repository, following the
coordinated-disclosure timeline in [SECURITY.md](../SECURITY.md). Promtact has
no visibility into self-hosted deployments and cannot detect an incident there.

## Recovery

| What | Where | Restore |
| --- | --- | --- |
| Database | Nightly dump, root-only, off the application path | `pg_restore` into a fresh database, then repoint the service |
| Configuration | `/etc/promtact/`, root and service account only | Reinstall from the repository plus the environment file |
| Releases | Several previous builds kept on the host | Move the `current` symlink and restart |
| Source and history | Git remote plus a verified local bundle | Clone from either |

The deploy script rolls back to the previous release by itself when a deploy
fails its readiness check, so the common case does not need a human.

**Restores are tested when the backup procedure changes, not on a schedule.** A
quarterly claim nobody performs is worse than an honest "when it changes",
because the first tells an auditor to check the evidence and there is none.

## Roles

One person: detection, decision, execution, communication. Escalation is to
external specialists engaged for the incident if it exceeds what one person can
handle — which for an S1 involving customer data it may well.

This is a real limitation and it belongs in a buyer's model of the risk. It is
also the ordinary state of a company this size, and the mitigation is not
pretending otherwise: it is automated detection that does not depend on someone
watching, and controls that fail closed when the operator is asleep.

## Worked example: 2026-08-06, schema altered by a test run

Recorded because a plan with a real entry is worth more than one with none.

**What happened.** Integration tests were run against the production database.
They create real records by design, and one test renamed the live tables while
verifying a rename path. The running service kept its open connection and stayed
up, so nothing alarmed.

**Severity.** S2 — the service was serving, but a restart would have failed and
the schema no longer matched the deployed binary.

**Detection.** By inspection during the change, not by an alarm. Nothing would
have caught it automatically. That is the finding.

**Impact.** No data lost, no data exposed, no customer affected. The deployment
was pre-revenue with test tenants only.

**Response.** Full dump taken before any remediation. A fresh database was built
from the migrations and the data imported into it, rather than trusting the
rename; row counts were compared per table and the audit chain revalidated. The
old database was left intact as a fallback.

**Changes.** A separate test database was created, with its connection string
kept apart from the production one. The rename path that caused the damage was
removed from the codebase entirely: it renamed whatever lived in the current
schema, which is only correct if every connection agrees on the schema, and a
connection pool does not.

**Lesson.** The failure was not the test. It was that a test's connection string
could point at production at all. Controls that depend on the operator reading
carefully fail at two in the morning.
