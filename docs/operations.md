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

Current durable storage is the local JSON snapshot configured with `--data`.
This is suitable for local labs, pilots, and single-node testing.

SQLite/Postgres is the next storage milestone. It should be implemented behind
the existing store boundary so the API and collectors do not change when the
storage backend changes.

