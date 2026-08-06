# Promtact

[![ci](https://github.com/hunterinvariants/promtact/actions/workflows/ci.yml/badge.svg)](https://github.com/hunterinvariants/promtact/actions/workflows/ci.yml)
[![quality](https://github.com/hunterinvariants/promtact/actions/workflows/quality.yml/badge.svg?branch=main)](https://github.com/hunterinvariants/promtact/actions/workflows/quality.yml)
[![codeql](https://github.com/hunterinvariants/promtact/actions/workflows/codeql.yml/badge.svg?branch=main)](https://github.com/hunterinvariants/promtact/actions/workflows/codeql.yml)
[![race detector](https://img.shields.io/badge/tests-race%20detector-2a78d6)](.github/workflows/ci.yml)
[![fuzzed](https://img.shields.io/badge/parsers-fuzzed-2a78d6)](internal/policy/gateway_fuzz_test.go)
[![signed releases](https://img.shields.io/badge/releases-cosign%20signed-0ca30c)](SECURITY.md#verifying-releases)
[![sbom](https://img.shields.io/badge/SBOM-SPDX-0ca30c)](SECURITY.md#verifying-releases)
[![security policy](https://img.shields.io/badge/security-policy-0ca30c)](SECURITY.md)
[![license](https://img.shields.io/badge/license-AGPL--3.0--or--later%20%2F%20commercial-blue)](LICENSE)
[![release](https://img.shields.io/github/v/release/hunterinvariants/promtact?sort=semver)](https://github.com/hunterinvariants/promtact/releases/latest)

An AI agent that can call tools can also be talked into calling the wrong one.
Promtact sits in front of those calls and decides, per call,
whether it happens: allow, hold for a human, or refuse.

It is a policy enforcement point for agent tool calls, plus the telemetry and
audit trail needed to answer "what did the agent do, and who let it" afterwards.
It speaks MCP, so it can be dropped in front of an existing MCP server without
touching the agent.

It contains no exploit logic, no malware behaviour, and nothing that attacks a
target. Demo mode generates telemetry, nothing else.

## About this project

This is my design and my implementation. I built it to solve a specific
problem: stopping agentic tool calls before they cause damage. Every
architectural decision is documented in
[docs/design-decisions.md](docs/design-decisions.md) — I can defend each one
because I made them based on real operational experience.

Like any modern developer, I use available tools to accelerate delivery. The
core logic, security model, integrations, and test suite are my own work; I
reviewed every line before it was committed. The test suite exists because I
don't ship code I haven't seen break first.

## How it works

```mermaid
flowchart LR
  A[Agent / MCP client] --> B[Inline PEP]
  B --> C[Policy + identity + provenance]
  C -->|allow| D[Bounded tool proxy]
  C -->|gate| E[Human approval queue]
  C -->|deny| F[Audit evidence]
  D --> G[Approved upstream]
  E -->|approved| D
  B --> H[Telemetry + correlation]
  H --> I[Alerts + dry-run response plans]
  I --> E
```

Every call passes through one decision point before it can act. The decision
looks at three things: what the tool is allowed to do, whether the agent making
the call is who it claims to be, and whether the tool itself is the one that was
approved rather than a substituted version.

Four choices shape everything else:

**A decision is made before the call, not observed after it.** Detection that
reports what an agent already did is a log, not a control. The enforcement point
sits inline, and a call that cannot be decided does not proceed.

**Denial survives the database going down.** If storage fails, decisions keep
being made and are written to a local journal, then reconciled when storage
returns. An outage must not become an open door — see
[docs/technical-claims.md](docs/technical-claims.md), which states this as a
claim with the command that demonstrates it.

**Content from a tool is data, never instruction.** Anything an agent reads —
a web page, a file, a tool response — can contain text aimed at the model. It is
never allowed to change what the policy permits.

**Anything consequential needs a person.** Not every call, but the ones that
delete, exfiltrate or spend. Those queue for approval rather than being guessed
at.

## Quick start

Run the complete non-root application and Postgres stack:

```powershell
docker compose -f compose.full.yaml up --build
```

Open `http://localhost:8080`. The Compose credentials are development-only;
override `PROMTACT_API_TOKEN` and `PROMTACT_SESSION_SECRET` outside a disposable local
environment.

Run the service with safe demo telemetry:

```powershell
$env:APPDATA="$PWD\.cache\appdata"
$env:GOTELEMETRY="off"
$env:GOCACHE="$PWD\.cache\go-build"
$env:GOMODCACHE="$PWD\.cache\go-mod"
go run ./cmd/promtact --demo --addr 127.0.0.1:8080
```

The server binds to a loopback address by default. Binding a non-loopback
address requires authentication, or `--insecure` for an explicitly open mode.

Run with Postgres persistence:

```powershell
docker compose up -d postgres
$env:PROMTACT_POSTGRES_DSN="postgres://promtact:promtact@localhost:5432/promtact?sslmode=disable"
$env:PROMTACT_SESSION_SECRET="replace-with-a-strong-random-secret"
go run ./cmd/promtact --demo --addr 127.0.0.1:8080 --policy configs\example.policy.json
```

Run with local JSON persistence for development:

```powershell
go run ./cmd/promtact --demo --addr 127.0.0.1:8080 --data .cache\promtact-state.json
```

Open:

```text
http://localhost:8080
```

Run tests:

```powershell
$env:APPDATA="$PWD\.cache\appdata"
$env:GOTELEMETRY="off"
$env:GOCACHE="$PWD\.cache\go-build"
$env:GOMODCACHE="$PWD\.cache\go-mod"
go test ./...
```

GitHub CI runs the same test suite with a real Postgres service, smoke-tests a
live startup, and builds Linux/Windows binaries for `amd64` and `arm64`.

GitHub security automation includes CodeQL analysis and Dependabot updates for
Go modules and GitHub Actions, plus dependency-review checks on pull requests.

Tagged releases publish platform binaries, an SPDX SBOM, and a `SHA256SUMS`
manifest (covering the binaries and the SBOM). The release workflow signs the
checksum manifest with Sigstore keyless signing. Verify a downloaded release
against the published bundle before trusting it:

```bash
cosign verify-blob \
  --bundle SHA256SUMS.bundle \
  --certificate-identity "https://github.com/hunterinvariants/promtact/.github/workflows/release.yml@refs/tags/vX.Y.Z" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  SHA256SUMS
sha256sum -c SHA256SUMS   # then check the binary against the verified manifest
```

Postgres operators can create and restore portable JSON backups with
`promtactl backup` and `promtactl restore`.

Run the optional Postgres integration test:

```powershell
docker compose up -d postgres
$env:PROMTACT_TEST_POSTGRES_DSN="postgres://promtact:promtact@localhost:5432/promtact?sslmode=disable"
go test ./internal/store -run TestPostgresPersistenceIntegration -count=1
```

## Documentation

| | |
| --- | --- |
| [Capabilities](docs/capabilities.md) | Everything the service does today |
| [Configuration](docs/configuration.md) | Flags, connectors, policy files, replay |
| [HTTP API](docs/api.md) | Endpoints; the OpenAPI document is at `/api/openapi.json` |
| [Operations](docs/operations.md) | Running it: MFA, SCIM, encryption, tracing |
| [Design decisions](docs/design-decisions.md) | Why it is built this way, including what was rejected |
| [Technical claims](docs/technical-claims.md) | Claims with the commands that check them |
| [Threat model](docs/threat-model.md) | What it defends against, and what it does not |
| [Production readiness](docs/production-readiness.md) | The contract a deployment is held to |

## License

Community distribution is licensed under AGPL-3.0-or-later.

Commercial licenses are available separately for organizations that need
closed-source redistribution, proprietary network use, enterprise terms, or
support. See [COMMERCIAL-LICENSE.md](COMMERCIAL-LICENSE.md).

External contributions require a signed CLA before merge. See [CLA.md](CLA.md).

## Safety Boundary

This project is for authorized defensive monitoring and response simulation.
Do not add exploit code, malware behavior, credential theft tooling, or
autonomous propagation logic.
