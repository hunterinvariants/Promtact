# HTTP API

The machine-readable specification lives at `/api/openapi.json` and is served
from the binary. This page is the human-readable companion.


Ingest one event:

```powershell
Invoke-RestMethod -Method Post -Uri http://localhost:8080/api/events -ContentType application/json -Body '{
  "kind": "agent_tool_call",
  "asset_id": "dev-agent-01",
  "hostname": "dev-agent-01",
  "actor": "local-agent",
  "tool_name": "shell_exec",
  "command": "read env token",
  "signal": "agent referenced token material",
  "labels": ["agent", "credential"]
}'
```

Gate a tool call before execution:

```powershell
Invoke-RestMethod -Method Post -Uri http://localhost:8080/api/gateway/execute -ContentType application/json -Body '{
  "asset_id": "dev-agent-01",
  "hostname": "dev-agent-01",
  "actor": "local-agent",
  "tool_name": "asset_inventory",
  "command": "inventory scan",
  "arguments": "token=abc123",
  "labels": ["agent", "tool-call"]
}'
```

With write-token protection enabled:

```powershell
Invoke-RestMethod -Method Post -Uri http://localhost:8080/api/events `
  -Headers @{ Authorization = "Bearer $env:PROMTACT_API_TOKEN" } `
  -ContentType application/json `
  -Body '{"kind":"deception_hit","asset_id":"dev-agent-01","signal":"canary token touched"}'
```

Endpoints (RBAC roles apply only when authentication is configured):

| Method | Path | Roles | Purpose |
| --- | --- | --- | --- |
| GET | `/healthz` | none | Liveness probe returning version |
| GET | `/readyz` | none | Readiness probe pinging storage |
| GET | `/api/status` | any authenticated | Tenant-scoped counts and gateway/storage status |
| GET/POST/DELETE | `/api/session` | none | Session/SSO info, login, logout |
| GET | `/api/sso/oidc/login`, `/api/sso/oidc/callback` | none | OIDC SSO login and callback |
| GET | `/api/sso/saml/login`, `/api/sso/saml/complete` | none | SAML SSO login and completion |
| POST | `/api/gateway/decide` | ingestor, analyst, operator | Gate a tool call and return a verdict (PDP) |
| POST | `/api/gateway/execute` | ingestor, analyst, operator | Gate then allow/block/queue/execute a tool call (PEP) |
| POST | `/api/gateway/proxy` | ingestor, analyst, operator | Gate and forward a tool call to an upstream URL |
| POST | `/api/mcp/proxy` | ingestor, analyst, operator | Gate and forward a request to the MCP upstream |
| GET | `/api/gateway/queue` | analyst, operator | List pending gateway approval actions |
| GET | `/api/gateway/actions/{id}` | analyst, operator | Fetch a single gateway action |
| POST | `/api/policy/reload` | admin | Hot-reload policy and threat-pack files |
| GET/POST/PUT/DELETE | `/api/policy/tenants` | admin | Manage per-tenant policy overlays |
| GET/POST/DELETE | `/api/deception/tokens` | operator | List, register, and remove deception tokens |
| GET | `/api/timeline` | any authenticated | Chronological investigation view for one asset |
| GET | `/api/license` | any authenticated | Report license/edition status |
| GET/POST | `/api/events` | GET: any authenticated; POST: ingestor, analyst, operator | List or ingest events |
| GET | `/api/alerts` | any authenticated | List tenant alerts |
| GET | `/api/assets` | any authenticated | List tenant assets |
| GET | `/api/policies` | any authenticated | List active policy rules |
| GET | `/api/audit` | analyst, operator | List tenant audit log entries |
| GET | `/api/audit/chain` | analyst, operator | Return the hash-chained audit log |
| GET/POST | `/api/responses` | GET: any authenticated; POST: analyst, operator | List response actions or plan actions for an alert |
| POST | `/api/responses/approve` | operator | Approve and execute a pending response action |
| GET/POST | `/api/tenants` | admin | List or register tenant backends |
| GET/PUT/DELETE | `/api/tenants/{id}` | admin | Get, update, or delete one tenant backend |
| GET/POST | `/api/admin/tenants` | platform admin | List or provision customer tenants |
| GET | `/api/admin/tenants/{id}/usage?period=YYYY-MM` | platform admin | Read monthly billable usage counters |
| GET | `/metrics` | any authenticated | Prometheus metrics without tenant or secret labels |
| POST | `/api/demo` | ingestor, analyst, operator | Load demo events for the tenant |

The measurable product claims, acceptance commands, limitations, and release gates are defined in [Verifiable Technical Claims](docs/technical-claims.md).
