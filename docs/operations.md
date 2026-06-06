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
the repository, and installs it as a service under the `runner` user. It uses
`gh auth token` or `GITHUB_TOKEN` to create the runner registration token.
That GitHub token must have repository admin access so GitHub can create the
runner registration token.

The runner user needs passwordless sudo for the deployment steps:

```bash
sudo visudo -f /etc/sudoers.d/promtact-runner
```

Add:

```text
runner ALL=(root) NOPASSWD: /usr/bin/install, /bin/ln, /bin/rm, /bin/chown, /bin/systemctl, /usr/bin/journalctl
```

After the runner is online, trigger the workflow manually from GitHub and it
will:

- build the Linux binaries
- install them under `/opt/promtact/releases/<sha>`
- repoint `/opt/promtact/current`
- restart `promtact`
- verify `GET /readyz`

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
go run ./cmd/promtact --demo
```

The payload type is `promtact.alerts`.

Incident ticket creation can be exported to a ticketing webhook transport:

```powershell
$env:PROMTACT_TICKET_WEBHOOK_URL="https://ticketing.example.invalid/promtact"
$env:PROMTACT_TICKET_WEBHOOK_TOKEN="replace-with-token"
go run ./cmd/promtact --demo
```

The payload type is `promtact.incident_ticket`.

Approved response actions can also be exported to a response webhook transport:

```powershell
$env:PROMTACT_RESPONSE_WEBHOOK_URL="https://soar.example.invalid/promtact"
$env:PROMTACT_RESPONSE_WEBHOOK_TOKEN="replace-with-token"
go run ./cmd/promtact --demo
```

The payload type is `promtact.response_action`.

GitHub can be used as a concrete execution target for incidents and approved
runbooks:

```powershell
$env:PROMTACT_GITHUB_OWNER="hunterinvariants"
$env:PROMTACT_GITHUB_REPO="promtact"
$env:PROMTACT_GITHUB_TOKEN="replace-with-token"
$env:PROMTACT_GITHUB_WORKFLOW_FILE="runbook.yml"
go run ./cmd/promtact --demo
```

Incident plans create GitHub issues. Approved response actions dispatch the
configured workflow file.

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

## Audit Log

The service records audit events for authentication failures, RBAC denials,
event ingestion, demo loads, response planning, and response approvals. Audit
events are stored in Postgres table `promtact_audit_events` in production mode and
are exposed through `GET /api/audit`.

`GET /api/audit` requires `analyst`, `operator`, or `admin`.

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
browser.

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
