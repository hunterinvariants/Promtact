# Capabilities

The full inventory of what the service does today. The README keeps only the
shape of the system; this is the exhaustive list.


- Go HTTP service with Postgres persistence for production and JSON snapshot
  fallback for local development.
- Policy engine for agent-tool abuse, taint-aware source-to-sink flow,
  secret exposure, unexpected egress, discovery, lateral movement, destructive
  impact, deception hits, suspicious model runtime activity, and versioned
  threat-pack content.
- Tool-provenance verification: when a signed tool fingerprint is declared for a
  tool, the gateway denies a provenance mismatch (spoofed/tampered tool) and
  gates a missing fingerprint - supply-chain control for agent tools.
- Agent-identity verification: registered agents present a signed identity token;
  the gateway denies impersonation (token mismatch) and gates unknown or
  unidentified agents, so tool calls are attributed to a verified agent rather
  than a spoofable actor string.
- Inline tool-call PEP for enforce-before-execute decisions at the tool
  boundary, backed by a separate PDP endpoint for diagnostics.
- Gateway queue, approval polling, a transport proxy for tool backends, and an
  MCP reverse-proxy that classifies each method by surface - passing through
  lifecycle/enumeration/notifications, gating `tools/call` against the approved
  list, and content-gating resource/prompt/sampling/completion surfaces.
- Org-scoped policy sets: per-tenant overrides of the approved-tool and
  approved-egress allowlists applied inline in the gateway, managed by an
  admin-only API or seeded from a file at startup.
- Deception/canary token registry with a management API, plus hot policy and
  threat-pack reload via `SIGHUP` or an admin endpoint.
- Correlator for multi-signal sequences such as discovery, credential touch,
  agent tool call, and outbound flow.
- Dry-run response planner for host isolation, egress blocking, tool disabling,
  ticket creation, and secret rotation, with approval-gated execution export.
- Native incident-ticket connectors for GitHub issues, Jira, and ServiceNow,
  plus a generic ticket webhook, dispatched first-enabled-wins.
- GitHub workflow dispatch for approval-gated response execution.
- User/token authentication with role-based access control, plus OIDC and SAML
  single sign-on and logical or physical multi-tenancy.
- Ed25519-signed commercial license tokens with an edition-status endpoint and
  an `promtactl license` keygen/issue/verify workflow.
- Audit log for authentication failures, RBAC denials, ingestion, response
  planning, and response approvals, with tamper-evident hash chaining and a
  validation endpoint.
- Per-asset investigation timeline endpoint.
- `promtactl replay` for safe JSONL telemetry replay into the ingest API.
- `promtactl validate` for authorized, benign detection validation - a library of
  MITRE ATT&CK-mapped tool-call emulations scored against the inline gateway.
- `promtactl mcp-demo` (with `promtactl mcp-stub`) for an end-to-end proof that the
  MCP reverse-proxy gates a real MCP client's tool calls live, and `promtactl
  bench` for inline-gateway latency/throughput numbers.
- A LangChain reference integration in [examples/langchain](examples/langchain)
  that gates a third-party agent framework's tool calls through the gateway.
- `promtactl agent` for long-running tail-based collection from supported
  defensive telemetry sources, including native Windows Event Log and Linux
  journald modes.
- Browser dashboard with asset risk graph, alerts, events, rules, response
  actions, and a live ATT&CK detection-coverage panel, plus session-based
  dashboard login.
- Alert webhook export for SIEM-style integrations.
- systemd and Windows service starter packaging.
- AGPLv3-or-later community license, commercial dual-license path, and CLA from
  day 1.
