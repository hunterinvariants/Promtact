# Operations

## Linux systemd

Example unit:

```text
packaging/systemd/promtact.service
```

Suggested layout:

```text
/opt/promtact/current/promtact
/opt/promtact/current/promtactl
/opt/promtact/releases/<sha>/promtact
/opt/promtact/releases/<sha>/promtactl
/etc/promtact/policy.json
/etc/promtact/promtact.env
/var/lib/promtact/state.json
```

The service unit points at `/opt/promtact/current`, so deployments can swap a
release symlink atomically and restart the service with a rollback available.

For production, set Postgres in `/etc/promtact/promtact.env`:

```text
PROMTACT_POSTGRES_DSN=postgres://promtact:promtact@postgres:5432/promtact?sslmode=disable
PROMTACT_SESSION_SECRET=<strong-random-secret>
```

For local development and integration tests, start the bundled Compose service:

```powershell
docker compose up -d postgres
$env:PROMTACT_POSTGRES_DSN="postgres://promtact:promtact@localhost:5432/promtact?sslmode=disable"
```

Create a dedicated user, copy the binaries and policy file, install the unit,
then enable it:

```bash
sudo useradd --system --home /var/lib/promtact --shell /usr/sbin/nologin promtact
sudo mkdir -p /opt/promtact /etc/promtact /var/lib/promtact
sudo chown -R promtact:promtact /var/lib/promtact
sudo cp packaging/systemd/promtact.service /etc/systemd/system/promtact.service
sudo chown root:promtact /etc/promtact/policy.json
sudo chmod 0640 /etc/promtact/policy.json
sudo systemctl daemon-reload
sudo systemctl enable --now promtact
```

The packaged unit binds `127.0.0.1:8080` and runs as the non-root `promtact` user
under a full systemd sandbox (`ProtectSystem=strict`, `SystemCallFilter`,
`RestrictAddressFamilies`, empty `CapabilityBoundingSet`, `MemoryDenyWriteExecute`,
`UMask=0077`, and more — `systemd-analyze security promtact` ≈ 1.5/OK). Reach the
dashboard through a reverse proxy or an SSH tunnel
(`ssh -L 8080:127.0.0.1:8080 host`), and keep `/etc/promtact/*.env` at mode `0600`.

## GitHub Deploy

The repository includes a GitHub Actions deployment workflow for reachable
Ubuntu hosts. It expects a release layout like this:

```text
/opt/promtact/releases/<sha>/promtact
/opt/promtact/releases/<sha>/promtactl
/opt/promtact/current -> /opt/promtact/releases/<sha>
```

The workflow needs SSH credentials and host details through repository or
environment secrets:

- `PROMTACT_DEPLOY_HOST`
- `PROMTACT_DEPLOY_PORT`
- `PROMTACT_DEPLOY_USER`
- `PROMTACT_DEPLOY_SSH_KEY`

The target user must be able to restart `promtact` through `sudo` without an
interactive password prompt.

The actual release swap logic lives in `scripts/deploy-release.sh`. It prints
step groups, service status, and the last journal lines if a step fails, so the
GitHub Actions log shows the real failure point instead of only `exit 1`.

## GitHub Self-hosted Runner

For a local Ubuntu VM staging environment, the repository includes a
`deploy-self-hosted.yml` workflow that runs on a GitHub Actions self-hosted
runner installed on the VM itself. This avoids SSH hops and tests the exact
release swap used in production.

Expected runner labels:

- `self-hosted`
- `linux`
- `x64`
- `promtact-staging`

Suggested one-time runner setup on the VM:

```bash
sudo useradd --system --create-home --home-dir /opt/actions-runner --shell /bin/bash runner
sudo mkdir -p /opt/actions-runner
sudo chown runner:runner /opt/actions-runner
```

Then run:

```bash
sudo bash scripts/setup-self-hosted-runner.sh
```

The script downloads the latest Linux x64 runner release, registers it against
the repository, and installs it as a service **running as the non-root `runner`
user** (not root). It uses `gh auth token` or `GITHUB_TOKEN` (a repository-admin
token) to create the runner registration token.

It also installs a fixed, root-owned deploy entrypoint at
`/usr/local/sbin/promtact-deploy` plus a root-owned copy of `deploy-release.sh`
under `/opt/promtact/bin/`, and grants the runner passwordless sudo for **only the
wrapper** — not arbitrary privileged commands:

```text
runner ALL=(root) NOPASSWD: /usr/local/sbin/promtact-deploy
```

The wrapper validates its inputs and runs the root-owned deploy script, so the
runner can trigger a deploy of the built artifacts but cannot perform arbitrary
root actions; a build-time supply-chain compromise is confined to the
unprivileged runner account. Do not run the runner service as root.

After the runner is online, trigger the workflow manually from GitHub (the
deployable ref is allowlisted to `main` / `vX.Y.Z`) and it will:

- build the Linux binaries (as the non-root runner)
- call `sudo /usr/local/sbin/promtact-deploy` to install them under
  `/opt/promtact/releases/<sha>`, repoint `/opt/promtact/current`, restart `promtact`, and
  verify `GET /readyz`

## Windows Service

Build or download `promtact.exe`, place it at `C:\Program Files\Promtact\promtact.exe`,
then run PowerShell as Administrator:

```powershell
.\packaging\windows\install-service.ps1
```

The script registers a Windows service named `Promtact` and stores runtime state
under `C:\ProgramData\Promtact`.

## Webhook Export

New alerts can be exported to a SIEM or webhook endpoint:

```powershell
$env:PROMTACT_ALERT_WEBHOOK_URL="https://siem.example.invalid/promtact"
$env:PROMTACT_ALERT_WEBHOOK_TOKEN="replace-with-token"
go run ./cmd/promtact --demo --addr 127.0.0.1:8080
```

The payload type is `promtact.alerts`.

Incident ticket creation can be exported to a ticketing webhook transport:

```powershell
$env:PROMTACT_TICKET_WEBHOOK_URL="https://ticketing.example.invalid/promtact"
$env:PROMTACT_TICKET_WEBHOOK_TOKEN="replace-with-token"
go run ./cmd/promtact --demo --addr 127.0.0.1:8080
```

The payload type is `promtact.incident_ticket`.

Approved response actions can also be exported to a response webhook transport:

```powershell
$env:PROMTACT_RESPONSE_WEBHOOK_URL="https://soar.example.invalid/promtact"
$env:PROMTACT_RESPONSE_WEBHOOK_TOKEN="replace-with-token"
go run ./cmd/promtact --demo --addr 127.0.0.1:8080
```

The payload type is `promtact.response_action`.

GitHub can be used as a concrete execution target for incidents and approved
runbooks:

```powershell
$env:PROMTACT_GITHUB_OWNER="hunterinvariants"
$env:PROMTACT_GITHUB_REPO="promtact"
$env:PROMTACT_GITHUB_TOKEN="replace-with-token"
$env:PROMTACT_GITHUB_WORKFLOW_FILE="runbook.yml"
go run ./cmd/promtact --demo --addr 127.0.0.1:8080
```

Incident plans create GitHub issues. Approved response actions dispatch the
configured workflow file.

Native Jira and ServiceNow connectors create incidents directly when configured:

```text
PROMTACT_JIRA_BASE_URL=https://your-org.atlassian.net
PROMTACT_JIRA_EMAIL=bot@your-org.com
PROMTACT_JIRA_API_TOKEN=replace-with-token
PROMTACT_JIRA_PROJECT_KEY=SEC
PROMTACT_SERVICENOW_URL=https://your-instance.service-now.com
PROMTACT_SERVICENOW_USER=integration.user
PROMTACT_SERVICENOW_PASSWORD=replace-with-password
```

Ticket connectors are tried first-enabled-wins: GitHub issue, Jira, ServiceNow,
then the generic ticket webhook.

## Storage

Production durable storage is Postgres via `--postgres-dsn` or
`PROMTACT_POSTGRES_DSN`. Promtact creates and upgrades the required tables through
versioned migrations tracked in `promtact_schema_migrations`.

The local JSON snapshot configured with `--data` remains useful for development
and quick labs, but it is not the production storage path.

`GET /api/status` exposes the active `schema_version` when Postgres is enabled.

The optional Postgres integration test is disabled by default and runs only when
`PROMTACT_TEST_POSTGRES_DSN` is set:

```powershell
$env:PROMTACT_TEST_POSTGRES_DSN="postgres://promtact:promtact@localhost:5432/promtact?sslmode=disable"
go test ./internal/store -run TestPostgresPersistenceIntegration -count=1
```

Portable backups are JSON snapshots produced by `promtactl backup` and restored
with `promtactl restore`:

```powershell
go run ./cmd/promtactl backup --postgres-dsn $env:PROMTACT_POSTGRES_DSN --output backup.json
go run ./cmd/promtactl restore --postgres-dsn $env:PROMTACT_POSTGRES_DSN --input backup.json
```

The service also exposes `GET /healthz` and `GET /readyz` for process and
database readiness checks.

Response actions are split by connector:

- `create_incident_ticket` uses the ticket webhook as soon as the plan is stored.
- approval-required actions use the response webhook only after operator approval.

## Collector Agents

Long-running collector agents tail source files, persist offsets, normalize new
content, and submit batches to the ingest API.

Example:

```powershell
go run ./cmd/promtactl agent --source sysmon-json --file sysmon.jsonl --url http://localhost:8080 --state-file .cache\agent-state.json
```

Use `--once` for a single pass over the current file contents, or omit it to
keep polling for appended telemetry.

Native source modes are also available:

```powershell
go run ./cmd/promtactl agent --source windows-eventlog --log-name Microsoft-Windows-Sysmon/Operational --url http://localhost:8080
go run ./cmd/promtactl agent --source journald --journal-unit ssh.service --url http://localhost:8080
```

## Detection Validation

`promtactl validate` checks that the inline gateway still enforces against
realistic agent threat patterns on your own authorized deployment. It runs a
curated library of benign, MITRE ATT&CK-mapped tool-call emulations through the
read-only `/api/gateway/decide` endpoint and scores each against its expected
verdict, plus a benign baseline to catch false positives. Each emulation is
tagged with its own gateway history key, so results are deterministic and
order-independent across repeated runs.

```powershell
go run ./cmd/promtactl validate --url http://localhost:8080 --token $env:PROMTACT_API_TOKEN
```

The emulations carry only synthetic descriptive strings — no real commands,
exploits, or attack payloads run against any target. Run it after upgrades or
policy changes; it exits non-zero if an expected verdict did not hold, so it can
gate a deploy. Add `--json` for machine-readable output in CI. Sample run:

```text
promtactl validate — agent-gateway detection validation
  PASS  -            benign-baseline         want=allow                  got=allow
  PASS  T1552.001    secret-in-context       want=>=require_approval     got=require_approval
  PASS  T1057        discovery-chain         want=>=require_approval     got=require_approval
  PASS  T1083        file-discovery          want=>=require_approval     got=require_approval
  PASS  T1059        prompt-injection        want=>=require_approval     got=require_approval
  PASS  T1027        obfuscated-secret       want=>=require_approval     got=require_approval
  PASS  T1140        deobfuscate-execute     want=>=require_approval     got=require_approval
  PASS  T1071.001    web-c2-beacon           want=>=require_approval     got=require_approval
  PASS  T1021        lateral-movement        want=>=require_approval     got=require_approval
  PASS  T1490        inhibit-recovery        want=>=require_approval     got=require_approval
  PASS  T1486        ransomware-impact       want=>=require_approval     got=require_approval
  PASS  T1567        unapproved-egress       want=>=require_approval     got=require_approval
  PASS  T1530        canary-touch            want=>=deny                 got=deny
  PASS  TA0002       unapproved-tool         want=>=deny                 got=deny

Summary: 14/14 held  (0 missed, 0 false positives)
```

### ATT&CK coverage map

`--coverage` groups the same run by tactic/technique and marks each `HELD` or
`GAP`, for a coverage report you can attach to an audit:

```powershell
go run ./cmd/promtactl validate --url http://localhost:8080 --token $env:PROMTACT_API_TOKEN --coverage
```

### Continuous monitoring and scheduling

For ongoing assurance, validate on a schedule. Two options:

- **systemd timer (recommended):** install the packaged
  `promtact-validate.service` (oneshot) and `promtact-validate.timer`. The service
  reads a least-privilege token via `--token-file /etc/promtact/validation.token`
  (make it readable by the `promtact` user: `chown promtact:promtact`, `chmod 600`),
  writes the latest result with `--output /var/lib/promtact/validation-last.json`,
  and appends a trend point with `--history /var/lib/promtact/validation-history.jsonl`.
  Enable with `systemctl enable --now promtact-validate.timer`.
- **Long-lived monitor:** `promtactl validate --continuous --interval 1h
  --webhook https://hooks.example/regress` re-runs the suite on an interval and
  POSTs a JSON alert (`type: detection_regression`) to the webhook whenever a
  detection stops holding.

Create the least-privilege validation identity by adding an `ingestor` user to
`policy.json` (`promtactl token-hash` produces the `token_sha256`), then restart
the server so the new user loads. Store the raw token only in the root-readable
`--token-file`.

### Validating with agent identity enforced

If you enforce `agent_identities` (so every tool call must present a verified
agent), the emulations must carry an identity too, or the benign baseline is
gated as an unidentified agent. Register a validation agent and pass it to the
suite:

```bash
promtactl validate --url http://127.0.0.1:8080 --token-file /etc/promtact/validation.token \
  --agent-id validation-agent --agent-token-file /etc/promtact/validation-agent.token
```

The verified identity does not weaken detection — the threat cases still fire on
their content — so the suite stays green while identity is enforced in
production. Add the same two flags to `promtact-validate.service` for the timer.

### Alerting on regression

`promtact-validate.service` ships with `OnFailure=promtact-validate-alert.service`. When
a run regresses (or fails to complete), the alert unit runs
`/usr/local/sbin/promtact-validate-alert`, which reads the result file and POSTs a
single webhook alert — rich (with the failed techniques) when a result is
available, generic otherwise. Install the script and configure the destination:

```bash
# install the alert helper + units (raw files from the repo)
base=https://raw.githubusercontent.com/hunterinvariants/promtact/main
curl -fsSL "$base/scripts/promtact-validate-alert.sh" -o /usr/local/sbin/promtact-validate-alert
chmod 0755 /usr/local/sbin/promtact-validate-alert
for u in promtact-validate.service promtact-validate.timer promtact-validate-alert.service; do
  curl -fsSL "$base/packaging/systemd/$u" -o "/etc/systemd/system/$u"
done

# configure the webhook (root-only)
umask 077
cat >/etc/promtact/validate.env <<'ENV'
PROMTACT_VALIDATE_ALERT_URL=https://hooks.example/detection-regression
PROMTACT_VALIDATE_ALERT_TOKEN=optional-bearer-token
ENV

systemctl daemon-reload && systemctl enable --now promtact-validate.timer
```

### Verifying the alert path

Fire a synthetic regression to prove the alert reaches a human, without touching
the live gateway:

```bash
cat >/tmp/val-fail.json <<'JSON'
{"total":1,"passed":0,"missed":1,"false_positives":0,
 "results":[{"name":"canary-touch","technique":"T1530","tactic":"Collection",
             "want":">=deny","got":"allow","pass":false}]}
JSON
set -a; . /etc/promtact/validate.env; set +a
PROMTACT_VALIDATE_RESULT_FILE=/tmp/val-fail.json /usr/local/sbin/promtact-validate-alert
rm -f /tmp/val-fail.json
```

Expect `-> HTTP 204`. A `401` means the sender's token and the receiver's differ;
a `503` means the receiver has no shared secret configured. Run this after every
change to the alert path — an alert channel that silently stopped working is
indistinguishable from "nothing ever went wrong".

Host the receiver off the monitored machine; see
[deploy/cloudflare-worker](../deploy/cloudflare-worker). A receiver on the same
host dies with the thing it watches.

### Coverage and trend in the dashboard

Point the server at the result and history files and the dashboard shows a live
**Detection Coverage** panel (`GET /api/gateway/validation`, read-only, viewer
role): a per-technique `HELD`/`GAP` list plus a sparkline trend of the held
ratio over recent runs. Set both paths in `/etc/promtact/promtact.env` and restart:

```bash
{
  echo 'PROMTACT_VALIDATION_RESULT_PATH=/var/lib/promtact/validation-last.json'
  echo 'PROMTACT_VALIDATION_HISTORY_PATH=/var/lib/promtact/validation-history.jsonl'
} >> /etc/promtact/promtact.env
systemctl restart promtact
```

The panel reads the files the validation timer writes — no re-running of the
suite on page load, so it never touches the live gateway from the browser. The
trend appears once at least two runs have been recorded.

## MCP integration demo

`promtactl mcp-demo` proves the gateway secures a real MCP client end-to-end: it
drives a curated sequence of MCP tool calls through the reverse-proxy
(`/api/mcp/proxy`) to a real upstream MCP server and scores each as forwarded,
gated for approval, or blocked. Use the bundled `promtactl mcp-stub` as the
upstream:

```bash
# 1) a reference upstream MCP server
promtactl mcp-stub --addr 127.0.0.1:9100 &

# 2) Promtact with the MCP upstream configured (auth is required for the proxy)
PROMTACT_SESSION_SECRET=… promtact --addr 127.0.0.1:8080 --api-token "$TOKEN" \
  --mcp-upstream-url http://127.0.0.1:9100 &

# 3) drive a real MCP client through the proxy and score enforcement
promtactl mcp-demo --url http://127.0.0.1:8080 --token "$TOKEN"
```

Expected: `tools/list` and a benign `tools/call` are forwarded; a secret-bearing
`tools/call`, an external `resources/read`, and an injection `prompts/get` are
gated for approval; an unapproved `tools/call` is blocked — `6/6 enforced as
expected`.

## Inline-PEP benchmark

`promtactl bench` measures the latency the inline gateway adds to each decision:

```bash
promtactl bench --url http://127.0.0.1:8080 --token "$TOKEN" --requests 5000 --concurrency 32
```

It reports throughput and p50/p90/p99/max latency. The decision logic itself is
sub-100µs (see `go test -bench BenchmarkGateToolCall ./internal/policy`); the
end-to-end figure is dominated by the HTTP round-trip, so run it on the target
host (loopback latency on some platforms inflates the number).

## Publishing the console through Cloudflare Tunnel

The server binds loopback only. A Cloudflare Tunnel publishes it without opening
a single inbound port: `cloudflared` runs on the host and dials out to
Cloudflare, so there is no listening socket to attack and no firewall rule to
maintain.

Install the connector as a native service rather than a container — a container
would need `network_mode: host` to reach the host's loopback, and the bridged
alternative would force the server to bind beyond `127.0.0.1`, giving up that
hardening.

```bash
# 1) install the connector (Debian/Ubuntu package), then register the service.
#    Prefix the command with a space so the tunnel token stays out of the shell
#    history, and treat that token like a password.
 sudo cloudflared service install <TUNNEL_TOKEN>
systemctl is-active cloudflared
journalctl -u cloudflared -n 20 --no-pager   # expect: Registered tunnel connection

# 2) in the Cloudflare dashboard, route a public hostname to the local server:
#    hostname app.example.com -> service http://127.0.0.1:8080, path empty.
```

Prefer the literal `http://127.0.0.1:8080` over `localhost:8080`: if `localhost`
resolves to `::1` first on a given host, the connector dials IPv6 while the
server listens on IPv4 and every request fails with a 502 that looks like a
configuration error. Verify with `ss -lntp | grep 8080` and
`getent ahosts localhost`.

### Required settings behind the tunnel

Both of these matter and neither is cosmetic:

```bash
# in /etc/promtact/promtact.env
PROMTACT_TRUSTED_PROXIES=127.0.0.1,::1
PROMTACT_PUBLIC_URL=https://app.example.com
```

Requests arrive from the connector on loopback, so without
`PROMTACT_TRUSTED_PROXIES` the server treats `127.0.0.1` as the client for every
request. Two things break as a result:

- **Login lockout becomes global.** The brute-force backoff is keyed by source
  IP, so one attacker's failed logins would lock out every customer — a
  self-inflicted denial of service. With the proxy trusted, `X-Forwarded-For`
  identifies the real client and the lockout applies per attacker.
- **The session cookie loses its `Secure` flag,** because the server only honors
  `X-Forwarded-Proto` from a trusted proxy and the local hop is plain HTTP.

`PROMTACT_PUBLIC_URL` gives SSO callbacks and HA the externally reachable address.

Verify from outside the host:

```bash
curl -sI https://app.example.com/     # expect HTTP/2 200 plus server: cloudflare
```

## Request logging and correlation

Every response carries an `X-Correlation-Id`. A caller may supply its own so its
trace joins ours; the value is accepted only if it is short and alphanumeric,
because it is written into log lines and an unvalidated one would let a caller
forge them. Anything else is replaced by a generated id. The same id is recorded
on the audit event, so a decision can be followed from the caller's logs through
the access log into the audit trail.

Enable one JSON line per request for log shipping:

```bash
# in /etc/promtact/promtact.env
PROMTACT_STRUCTURED_LOGS=true
```

Each line carries the correlation id, method, path, status, duration, principal,
tenant and client IP. Query strings and headers are deliberately excluded — they
carry tokens.

## API versioning

Every endpoint is reachable twice: at its original path and under `/api/v1`.
Nothing was moved — the validation suite, the LangChain connector, the console
and deployed agents all keep working — but new integrations can pin a version
that will not shift under them.

```bash
curl -sH "Authorization: Bearer $TOKEN" https://app.example.com/api/v1/status
curl -sH "Authorization: Bearer $TOKEN" https://app.example.com/api/v1/openapi.json
```

Versioned responses carry `X-API-Version: v1`. Unversioned API responses carry
`Deprecation: true` and a `Link` header pointing at the successor path, so an
integrator can find what to migrate to without reading a changelog. The original
paths still work and are not scheduled for removal; the header is a signal, not a
countdown.

The OpenAPI document is embedded in the binary, so it describes the deployment
being talked to rather than whatever was published elsewhere. It requires
authentication like the rest of the API — the same document is in the public
repository, so gating it protects nothing, but keeping the set of unauthenticated
paths as small as possible does.

## Signing the policy file

`policy.json` decides which tools are approved, which principals exist and what
roles they hold, which tool fingerprints are pinned and which agent identities
are registered. It is read from local disk at startup — including a restart
during a database outage, when it is the only source of that truth. Sign it so
tampering is detected instead of silently taking effect:

```bash
# a host-held secret, separate from the threat-pack key on purpose: a compromised
# detection-content key must not also be able to forge identities
export PROMTACT_POLICY_HMAC_SECRET='...'
promtactl sign-policy --file /etc/promtact/policy.json   # writes policy.json.sig
```

Put `PROMTACT_POLICY_HMAC_SECRET` in `/etc/promtact/promtact.env` and restart. From then on
a missing or mismatched signature stops startup. Re-sign after every edit — the
signature covers the bytes on disk exactly. Set `PROMTACT_POLICY_REQUIRE_SIGNED=true`
to refuse starting even when no key is configured, so a misconfigured host fails
loudly rather than running unverified.

## Surviving a storage outage

The gateway computes its verdict in process, so a database outage does not change
what it decides — only whether the record can be stored. Configure a local
journal so a storage incident cannot discard an enforcement decision:

```bash
# in /etc/promtact/promtact.env
PROMTACT_DECISION_JOURNAL_PATH=/var/lib/promtact/decision-journal.jsonl
PROMTACT_DECISION_JOURNAL_MAX_ENTRIES=10000
```

While storage is unavailable the server keeps serving verdicts, appends the
alerts and actions it could not persist to that journal (fsynced per record), and
replays them into storage on recovery — automatically on the next successful
write and, if the deployment is idle, once a minute.

Watch `promtact_degraded_mode`, `promtact_decision_journal_depth` and
`promtact_decision_journal_dropped_total`. A non-zero dropped counter means the
journal hit its cap and refused records; decisions were still enforced, but that
part of the audit trail is gone, so alert on it.

Two limits worth knowing before you rely on this: principals provisioned into
the tenant directory are verified against the database, so during a full outage
only principals declared in `policy.json` can still authenticate; and the journal
lives on the host, so it survives a restart but not the loss of that machine.

## Load and HA verification

The in-process behaviour under load is covered by tests run under the race
detector in CI: concurrent decisions stay correct, the backpressure semaphore
sheds excess load with 429 without leaking slots, and the store is safe under
concurrent access. To validate a real deployment end-to-end:

- **Load / latency:** run `promtactl bench` against the target with rising
  `--concurrency` until p99 or the error rate degrades; that is the host's
  sustainable rate. Backpressure (`--gateway-max-in-flight`, default 64) caps
  in-flight critical operations and returns 429 beyond it.
- **Multi-instance HA:** run two or more instances behind a load balancer against
  a shared Postgres, with `--public-url` / `--instance-name` set per instance.
  `scripts/ha-rollout.sh` does a readiness-gated rolling restart and
  `scripts/ha-check.sh` probes `/readyz`; drain one instance and confirm the
  other serves uninterrupted (see [ha.md](ha.md)).
- **Database-outage drill:** with a Postgres backend, stop the database and
  confirm the server returns clean errors (no crash) and recovers when the
  database returns. A malformed/unreachable DSN fails fast at startup rather than
  hanging.

## Audit Log

The service records audit events for authentication failures, RBAC denials,
event ingestion, demo loads, policy and tenant changes, gateway decisions,
response planning, and response approvals. Audit events are stored in Postgres
table `promtact_audit_events` in production mode and are exposed through
`GET /api/audit`. Records are hash-chained, HMAC-anchored with a server-held key,
and (in the Postgres path) re-derived from the rows at read time so DB tampering
is detected at runtime; the chain state is visible through `GET /api/audit/chain`.

`GET /api/audit` requires `analyst`, `operator`, or `admin`.
`GET /api/audit/chain` requires `analyst`, `operator`, or `admin`.

## Gateway Control

The inline gateway enforces a bounded in-flight limit on the critical path.
Set `--gateway-max-in-flight` or `PROMTACT_GATEWAY_MAX_IN_FLIGHT` to control
backpressure. `POST /api/gateway/proxy` forwards tool payloads to configured
upstreams only after the gate allows them.

The transparent MCP proxy uses `--mcp-upstream-url` and optional
`--mcp-upstream-token` to forward JSON-RPC MCP traffic through Promtact while the
gate inspects tool-like calls inline. The proxy reaches loopback/internal
upstreams only with the explicit `--proxy-allow-local-targets` flag (off by
default); both proxy endpoints are refused when authentication is not configured.

## Policy, License, and Deception

Hot-reload the active policy and threat pack without a restart (admin), or send
`SIGHUP` to the process:

```http
POST /api/policy/reload
```

Manage per-tenant org-scoped policy overlays (approved tools / egress); seed them
at startup with `--tenant-policies`:

```http
GET/POST/DELETE /api/policy/tenants    # admin
```

Seed deception/canary tokens with `--deception-tokens` and manage them at
runtime; a hit is denied inline by the gateway:

```http
GET/POST/DELETE /api/deception/tokens  # operator
```

Gate the commercial edition with an Ed25519 license token:

```bash
promtactl license keygen
promtactl license issue --private-key $PROMTACT_LICENSE_PRIVATE_KEY --org "Example" \
  --features sso,multi-tenant --valid-for 8760h
# run with --license-file license.token --license-public-key <base64-public-key>
# status:  GET /api/license   (community edition when none is configured)
```

A per-asset investigation timeline is available at `GET /api/timeline`.

## RBAC

Define users in the policy file with token hashes:

```json
{
  "users": [
    {
      "name": "admin",
      "token_sha256": "replace-with-sha256-token-hash",
      "roles": ["admin"]
    }
  ]
}
```

Generate a hash:

```powershell
.\promtactl.exe token-hash --token "replace-with-secret-token"
```

## Dashboard Login

The dashboard uses a session cookie instead of storing bearer tokens in the
browser. It also supports OIDC SSO when `PROMTACT_OIDC_ISSUER_URL`,
`PROMTACT_OIDC_CLIENT_ID`, and `PROMTACT_OIDC_REDIRECT_URL` are configured, plus SAML
SSO when `PROMTACT_SAML_ROOT_URL`, `PROMTACT_SAML_IDP_METADATA_URL`, and explicit
signing key/certificate paths are configured.

Login:

```http
POST /api/session
Content-Type: application/json

{"username":"admin","token":"replace-with-secret-token"}
```

Check the current session:

```http
GET /api/session
```

Logout:

```http
DELETE /api/session
```

## High Availability

Promtact is designed to run as multiple stateless replicas behind a load
balancer. Use the same Postgres database for all replicas, keep SSO signing
material shared, and set a distinct `--instance-name` on each node. Set
`--public-url` to the canonical external URL used by SSO callbacks and health
checks.

Example instance start:

```bash
sudo install -d -o root -g root /etc/promtact
sudo install -o root -g root -m 0640 /path/to/blue.env /etc/promtact/blue.env
sudo install -o root -g root -m 0640 /path/to/green.env /etc/promtact/green.env
sudo systemctl daemon-reload
sudo systemctl enable --now promtact@blue
sudo systemctl enable --now promtact@green
```

Example failover check:

```bash
curl -fsS http://blue-host/readyz
curl -fsS http://green-host/readyz
```

Use the reverse proxy example in `packaging/nginx/promtact.conf` to place a TLS
terminating load balancer in front of multiple instances.
