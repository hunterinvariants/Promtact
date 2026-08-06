# Verifiable Technical Claims

Public claims are valid only when their acceptance command passes for the exact commit and named environment. Results must not be generalized across hardware, policies, databases, or releases.

## 1. Identity and provenance fail closed

**Claim.** When agent identities or tool provenance are configured, Promtact denies invalid identity tokens and mismatched tool fingerprints, and requires approval for missing or unknown claims. Verified identity never suppresses content detection.

```powershell
go test ./internal/policy -run 'Test.*(Identity|Provenance)' -count=1
```

Scope: HTTP and MCP calls normalized into `ToolCallRequest`; not yet proof of interoperability across several agent frameworks.

## 2. Source-to-sink flows survive covered obfuscation and chain context

**Claim.** Promtact identifies sensitive sources and action/external sinks, emits flows in decision metadata, and escalates direct or history-linked flows. Encodings in the versioned corpus do not bypass enforcement.

```powershell
go test ./internal/policy -run 'Test.*(Taint|History|Obfuscat)' -count=1
```

Scope: deterministic terms and decoding variants in the corpus, not universal information-flow tracking.

## 3. Sequence-aware decisions are deterministic and explainable

**Claim.** Calls sharing a run/session/agent history key affect later risk within bounded memory; unrelated keys remain isolated. Decisions return verdict, reason, risk, findings, taint context, and request ID.

```powershell
go test ./internal/policy -run 'Test.*Gateway.*(History|Sequence|Reason|Metadata)' -count=1
go test ./internal/server -run 'Test.*(Gateway|MCP|Tenant)' -count=1
```

Scope: in-process history; cross-region sequence state is not implemented.

## 4. Detection behavior is reproducibly regression-tested

**Claim.** `promtactl validate` runs the named `promtact-agent-security-v1` synthetic suite, separates false positives from missed enforcement, records reasons, and exits non-zero on regression.

```powershell
go run ./cmd/promtactl validate --url http://localhost:8080 --json --output evidence/validation.json
go test ./cmd/promtactl -run TestValidation -count=1
```

Scope: curated synthetic regression corpus, not an independent held-out scientific benchmark.

## 5. Inline latency is measured against release gates

**Claim.** On a named environment, Promtact reproduces raw-policy and HTTP latency measurements and fails when declared P99 or throughput objectives are missed. Evidence must name commit, CPU, Go version, policy, concurrency, sample size, percentiles, throughput, and errors.

```powershell
go test ./internal/policy -bench GateToolCall -benchmem -count=5
go run ./cmd/promtactl bench --url http://localhost:8080 --requests 10000 --concurrency 32 --max-p99 25ms --min-rps 1000 --json
```

Thresholds above are example release targets, not universal performance claims.

## Claim intentionally not made

Promtact does **not** currently claim uninterrupted enforcement during control-plane or primary-database failure. Although policy evaluation is local, synchronous persistence can still fail a gateway request. This needs signed local policy snapshots, an asynchronous durable decision journal, replay protection, explicit degraded-mode rules, and failure-injection tests.
