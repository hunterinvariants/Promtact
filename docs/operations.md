# Operations

## Linux systemd

Example unit:

```text
packaging/systemd/promtact.service
```

Suggested layout:

```text
/opt/promtact/promtact
/opt/promtact/promtactl
/etc/promtact/policy.json
/etc/promtact/promtact.env
/var/lib/promtact/state.json
```

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
sudo systemctl daemon-reload
sudo systemctl enable --now promtact
```

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

## Storage

Production durable storage is Postgres via `--postgres-dsn` or
`PROMTACT_POSTGRES_DSN`. Promtact creates the required tables automatically.

The local JSON snapshot configured with `--data` remains useful for development
and quick labs, but it is not the production storage path.

The optional Postgres integration test is disabled by default and runs only when
`PROMTACT_TEST_POSTGRES_DSN` is set:

```powershell
$env:PROMTACT_TEST_POSTGRES_DSN="postgres://promtact:promtact@localhost:5432/promtact?sslmode=disable"
go test ./internal/store -run TestPostgresPersistenceIntegration -count=1
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
