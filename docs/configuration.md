# Configuration

Every flag and environment variable, the connector settings, the policy file
format, and telemetry replay. Start from the README for the common path; come
here when you need a specific knob.


```text
Core
--addr                  HTTP listen address (default :8080)
--web                   static dashboard directory (default web)
--demo                  load safe demo telemetry at startup
--insecure              allow open mode on non-loopback listen addresses
--retention-window      retention window for events, alerts, actions, audits (default 30d)
--gateway-max-in-flight max in-flight gateway operations before backpressure (default 64)
--trusted-proxies       comma-separated trusted proxy CIDRs or IPs

Persistence and policy
--data                  optional JSON snapshot path for local persistence
--postgres-dsn          Postgres DSN, defaults to PROMTACT_POSTGRES_DSN
--policy                optional JSON policy configuration path
--threat-pack           optional threat pack JSON file
--deception-tokens      optional JSON file of deception/canary tokens
--tenant-policies       optional JSON file of org-scoped policy sets

Authentication and licensing
--api-token             legacy admin token, defaults to PROMTACT_API_TOKEN
--license-file          path to a commercial license token file
--license-public-key    base64 ed25519 public key to verify the license

Webhooks
--alert-webhook-url / --alert-webhook-token         new-alert SIEM/webhook export
--ticket-webhook-url / --ticket-webhook-token       incident ticket webhook
--response-webhook-url / --response-webhook-token    approved response webhook

GitHub integration
--github-api-base       optional GitHub API base URL
--github-owner          GitHub owner for issue and workflow integrations
--github-repo           GitHub repository for issue and workflow integrations
--github-token          GitHub token for issue and workflow integrations
--github-workflow-file  GitHub workflow file for approved response actions
--github-workflow-ref   GitHub ref for workflow dispatch

Jira integration
--jira-base-url         Jira base URL for incident tickets
--jira-email            Jira account email
--jira-api-token        Jira API token
--jira-project-key      Jira project key for incidents
--jira-issue-type       Jira issue type (default Task)

ServiceNow integration
--servicenow-url        ServiceNow instance URL for incidents
--servicenow-user       ServiceNow user
--servicenow-password   ServiceNow password

MCP interception
--mcp-upstream-url      upstream MCP server URL for transparent interception
--mcp-upstream-token    optional bearer token for the MCP upstream

OIDC single sign-on
--oidc-issuer-url       OIDC issuer URL for SSO login
--oidc-client-id        OIDC client ID
--oidc-client-secret    OIDC client secret
--oidc-redirect-url     OIDC redirect URL
--oidc-scopes           comma-separated OIDC scopes (default openid,profile,email)
--oidc-tenant-claim     OIDC claim name for tenant assignment
--oidc-role-claim       OIDC claim name for roles
--oidc-email-claim      OIDC claim name for username/email

SAML single sign-on
--saml-root-url         SAML service provider root URL
--saml-idp-metadata-url SAML identity provider metadata URL
--saml-key-path         SAML signing key path
--saml-cert-path        SAML signing certificate path
--saml-tenant-attribute SAML attribute name for tenant assignment
--saml-role-attribute   SAML attribute name for roles
--saml-email-attribute  SAML attribute name for username/email

High availability and multi-tenancy
--public-url            canonical public URL for HA and SSO callbacks
--instance-name         instance label for HA deployments
--tenant-isolation-mode logical or physical tenant isolation (default logical)
--tenant-registry-path  path to the tenant registry JSON
--tenant-postgres-dsn-template Postgres DSN template for physical tenant stores
--tenant-data-path-template    file path template for physical tenant stores
```

Every flag has a matching `PROMTACT_*` environment variable (for example `--addr`
has no env var, while `--postgres-dsn` reads `PROMTACT_POSTGRES_DSN`). A few
behaviors are env-only: `PROMTACT_SESSION_SECRET` (required when authentication or
SSO is configured), `PROMTACT_AUDIT_HMAC_SECRET` (anchors the audit hash chain),
and `PROMTACT_MANIFEST_HMAC_SECRET` / `PROMTACT_MANIFEST_REQUIRE_SIGNED` (threat-pack
manifest signing).

When users are configured in the policy file, all API endpoints require
`Authorization: Bearer <token>` or `X-Promtact-Token: <token>` and are checked
against RBAC roles. `--api-token` remains a legacy admin-token compatibility
path.

The dashboard uses `POST /api/session` to exchange a configured user name and
token for a session cookie, or `GET /api/sso/oidc/login` / `GET /api/sso/saml/login`
for SSO. `GET /api/session` reports the current dashboard state, includes SSO
availability, and `DELETE /api/session` logs out.

For HA, run multiple replicas behind a load balancer with the same Postgres
database, shared SAML signing material, and distinct `--instance-name` values.
`--public-url` should match the canonical external URL used by SSO callbacks
and health checks.

For physical tenant isolation, set `--tenant-isolation-mode physical` and
either `--tenant-postgres-dsn-template` or `--tenant-data-path-template`. The
dashboard exposes a tenant admin panel backed by `GET /api/tenants` and
`POST /api/tenants`.

## Incident-Ticket Connectors

Confirmed or approved response actions can open an incident ticket. Connectors
are evaluated first-enabled-wins in this order: GitHub issue, Jira, ServiceNow,
then the generic ticket webhook. A connector is enabled only when all of its
required settings are present, and exactly one ticket is created per action.

GitHub issues use the GitHub integration flags above. Jira creates an issue via
`POST <jira-base-url>/rest/api/2/issue` with HTTP Basic auth (`email:api-token`):

```text
--jira-base-url     https://your-org.atlassian.net
--jira-email        bot@your-org.com
--jira-api-token    <api token>
--jira-project-key  SEC
--jira-issue-type   Task            # optional, defaults to Task
```

ServiceNow creates an incident via `POST <servicenow-url>/api/now/table/incident`
with HTTP Basic auth (`user:password`):

```text
--servicenow-url       https://your-instance.service-now.com
--servicenow-user      integration.user
--servicenow-password  <password>
```

The generic ticket webhook (`--ticket-webhook-url` / `--ticket-webhook-token`)
remains available as a fallback transport.

## Commercial License Tokens

The product ships as a community edition by default. A signed license token
upgrades the reported edition. Licenses are Ed25519-signed: the vendor issues
tokens with a private key, and a deployment verifies them with the public key,
so a license cannot be forged without the private key. This is the technical
edition gate; it is separate from the AGPL / commercial legal licensing below.

Generate a key pair (vendor side, keep the private key secret):

```powershell
go run ./cmd/promtactl license keygen
```

Issue a license for an organization:

```powershell
go run ./cmd/promtactl license issue `
  --private-key $env:PROMTACT_LICENSE_PRIVATE_KEY `
  --org "Example Corp" --features sso,multi-tenant --valid-for 8760h
```

Verify a token against the public key:

```powershell
go run ./cmd/promtactl license verify --public-key <base64-public-key> --token <token>
```

Run the server with a license; `GET /api/license` reports the edition, features,
expiry, and validity:

```powershell
go run ./cmd/promtact --addr 127.0.0.1:8080 `
  --license-file license.token --license-public-key <base64-public-key>
```

## Policy Configuration

The policy file is JSON:

```json
{
  "approved_tools": ["asset_inventory", "ticket_create", "policy_read", "siem_search"],
  "approved_egress_hosts": ["api.openai.com", "github.com", "login.microsoftonline.com"],
  "threat_pack_path": "configs\\example.threat-pack.json",
  "correlation_window": "30m",
  "users": [
    {
      "name": "admin",
      "token_sha256": "replace-with-sha256-token-hash",
      "roles": ["admin"]
    }
  ]
}
```

See [configs/example.policy.json](configs/example.policy.json) and
[configs/example.rbac.policy.json](configs/example.rbac.policy.json).

Create a token hash:

```powershell
go run ./cmd/promtactl token-hash --token "replace-with-secret-token"
```

Roles:

- `viewer`: read-only API access.
- `ingestor`: read API access and event/demo ingestion.
- `analyst`: read API access, ingestion, and response planning.
- `operator`: analyst permissions plus response approvals.
- `admin`: all API operations.

Audit logs require `analyst`, `operator`, or `admin`.

The policy and threat pack can be hot-reloaded without a restart by sending
`SIGHUP` or calling `POST /api/policy/reload` as an admin. Deception/canary
tokens can be seeded with `--deception-tokens` and managed at runtime through
`GET/POST/DELETE /api/deception/tokens`.

The inline gateway and proxy path enforce a bounded in-flight limit to apply
backpressure on the critical decision path; configure it with
`--gateway-max-in-flight` or `PROMTACT_GATEWAY_MAX_IN_FLIGHT`.

The MCP reverse-proxy path is enabled by setting `--mcp-upstream-url` and an
optional `--mcp-upstream-token`.

### Org-Scoped Policy Sets

Each tenant can override the global approved-tool and approved-egress
allowlists. A non-empty list fully replaces the global list for that tenant's
gateway and detection decisions; an omitted list falls back to the global
policy, so a tenant without an overlay behaves exactly as before.

Overlays are managed through the admin-only `/api/policy/tenants` endpoint
(`GET` to list, `POST`/`PUT` to set, `DELETE?tenant_id=...` to remove) and can
be seeded at startup from a JSON file with `--tenant-policies`:

```json
[
  {
    "tenant_id": "acme",
    "approved_tools": ["asset_inventory", "siem_search"],
    "approved_egress": ["acme-cdn.example.net"]
  }
]
```

## Telemetry Replay and Collectors

`promtactl replay` reads newline-delimited JSON events and posts them to
`/api/events`.

```powershell
go run ./cmd/promtactl replay --file examples\demo-events.jsonl --url http://localhost:8080
```

With write-token protection:

```powershell
go run ./cmd/promtactl replay --file examples\demo-events.jsonl --token $env:PROMTACT_API_TOKEN
```

Validate a file without sending it:

```powershell
go run ./cmd/promtactl replay --file examples\demo-events.jsonl --dry-run
```

Run the wedge demo against a live server:

```powershell
go run ./cmd/promtactl wedge-demo --url http://localhost:8080 --approved-by operator --await-approval
```

Validate that the inline gateway enforces against realistic agent threat
patterns on your own authorized deployment. `promtactl validate` runs a curated
library of benign, MITRE ATT&CK-mapped tool-call emulations through the
read-only `/api/gateway/decide` path and prints a pass/fail scorecard (including
a benign baseline to catch false positives). It emits only synthetic descriptive
telemetry - no real commands or exploit payloads are executed:

```powershell
go run ./cmd/promtactl validate --url http://localhost:8080 --token $env:PROMTACT_API_TOKEN
```

Use it after upgrades or policy changes as a detection regression check; a
non-zero exit means an expected verdict did not hold. Add `--json` for CI,
`--coverage` for an ATT&CK tactic/technique coverage map, or `--continuous
--interval 1h --webhook <url>` to run it as a long-lived monitor that alerts on
regression. Schedule it with the packaged `promtact-validate.timer` (with an
`OnFailure=` webhook alert), and surface the latest result plus a trend
sparkline as a **Detection Coverage** panel in the dashboard via `--output` /
`--history` and `PROMTACT_VALIDATION_RESULT_PATH` / `PROMTACT_VALIDATION_HISTORY_PATH`
(see [docs/operations.md](docs/operations.md)).

Normalize external defensive logs to Promtact JSONL:

```powershell
go run ./cmd/promtactl collect --source suricata-eve --file eve.json --output events.jsonl
go run ./cmd/promtactl collect --source zeek-conn --file conn.log --output events.jsonl
go run ./cmd/promtactl collect --source sysmon-json --file sysmon.jsonl --output events.jsonl
go run ./cmd/promtactl collect --source auditd --file audit.log --output events.jsonl
```

Run a long-lived collector agent that tails a source file and posts batches to
the ingest API:

```powershell
go run ./cmd/promtactl agent --source sysmon-json --file sysmon.jsonl --url http://localhost:8080 --state-file .cache\agent-state.json
```

Native collector modes:

```powershell
go run ./cmd/promtactl agent --source windows-eventlog --log-name Microsoft-Windows-Sysmon/Operational --url http://localhost:8080
go run ./cmd/promtactl agent --source journald --journal-unit ssh.service --url http://localhost:8080
```

Sign a threat-pack manifest (requires `PROMTACT_MANIFEST_HMAC_SECRET`):

```powershell
go run ./cmd/promtactl sign-manifest --file configs\example.threat-pack.json
```

Operations notes are in [docs/operations.md](docs/operations.md).
