# Production Readiness Contract

This document defines what “production-ready” means for Promtact. It is a release contract, not a feature wishlist.

## Invariants

1. **Fail closed at the action boundary.** Unknown identities, provenance
   mismatches, malformed policy inputs, and unavailable approval state never
   expand tool permissions.
2. **Keep untrusted content as data.** Tool output, MCP resources, prompts,
   telemetry, and ticket content cannot become control-plane instructions.
3. **Require explicit authority.** A detection may propose containment, but an
   operator approval is required before an external response executor receives
   the action.
4. **Preserve evidence.** Every security-relevant decision records actor,
   tenant, target, policy result, and audit-chain linkage.
5. **Bound resource use.** Request bodies, history, concurrency, polling,
   retries, and outbound connections have explicit limits.
6. **Separate liveness from readiness.** `/healthz` proves the process is alive;
   `/readyz` proves its configured store is available.
7. **Promote verified artifacts.** Releases are checksummed, SBOM-described,
   and keylessly signed. Deployments consume a verified artifact rather than
   rebuilding source.

## Release evidence

| Property | Required evidence |
|---|---|
| Correctness | Full tests and aggregate coverage gate |
| Concurrency safety | Race detector and overload/backpressure tests |
| Dependency safety | `govulncheck`, dependency review, and Dependabot |
| Image safety | Non-root image plus Trivy high/critical gate |
| Source safety | CodeQL and immutable Action references |
| Artifact integrity | SPDX SBOM, SHA-256 manifest, Sigstore bundle |
| Runtime health | Startup smoke test and separate readiness probe |
| Detection quality | Versioned `promtactl validate` result and trend history |
| Decision latency | Versioned `promtactl bench` result for the gateway |
| Audit integrity | Hash-chain and anchor validation tests |

## Deployment boundary

The repository's full Compose stack is a reproducible evaluation environment,
not a turnkey internet-facing production topology. Production additionally
requires:

- TLS termination and a correctly configured trusted-proxy allowlist;
- unique, secret-managed session and API credentials;
- managed Postgres with encrypted backups and restore drills;
- SSO claim mapping and tenant-isolation validation;
- outbound allowlists for MCP, webhooks, and ticket integrations;
- centralized logs, alerts on failed audit validation, and retention policies;
- an approval-protected deployment environment with rollback evidence.

The detailed controls live in `hardening.md`, `operations.md`, `ha.md`,
`tenancy.md`, and `threat-model.md`. If an implementation contradicts this
contract, the implementation is the defect.
