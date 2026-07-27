# Quality Gates

Promtact uses one local quality contract and runs the same
contract in CI. A change is releasable only when formatting, static analysis,
race detection, tests, aggregate coverage, builds, dependency scanning, and
container scanning pass.

## Local verification

On Linux or macOS:

```bash
./scripts/verify.sh
```

On Windows:

```powershell
$env:APPDATA="$PWD\.cache\appdata"
$env:GOTELEMETRY="off"
$env:GOCACHE="$PWD\.cache\go-build"
$env:GOMODCACHE="$PWD\.cache\go-mod"
.\scripts\verify.ps1
```

The default aggregate coverage floor is 50%. Raise it for a local or CI run
with `COVERAGE_MIN`; never lower it merely to merge a change.

```bash
COVERAGE_MIN=55 ./scripts/verify.sh
```

The gate deliberately uses the Go toolchain rather than an opaque wrapper:

1. `gofmt` reports unformatted production and test code.
2. `go vet` catches suspicious constructs.
3. `go test -race` exercises the complete package graph with the race detector.
4. `go tool cover` enforces an aggregate coverage floor.
5. Both shipped binaries are built with `-trimpath`.

The `quality` workflow adds `govulncheck` for reachable Go vulnerabilities and
Trivy for high or critical vulnerabilities in the final runtime image. All
third-party GitHub Actions are pinned to immutable commit SHAs.

## Container contract

The image is a two-stage build. The runtime:

- contains only the two application binaries, static web assets, configuration
  examples, CA certificates, and timezone data;
- runs as UID/GID `10001`, never as root;
- supports a read-only root filesystem;
- has no compiler or Go module cache;
- is built with `CGO_ENABLED=0`, `-trimpath`, and stripped symbols.

Run the complete local stack:

```bash
docker compose -f compose.full.yaml up --build
```

The stack waits for Postgres readiness, exposes the service on port `8080`,
uses a read-only application filesystem, and loads safe demo telemetry. The
included credentials are development-only defaults; set `PROMTACT_API_TOKEN` and
`PROMTACT_SESSION_SECRET` before using the stack outside a disposable workstation.

## Evidence expected on a pull request

- the `ci`, `quality`, CodeQL, and dependency-review workflows are green;
- new security-sensitive behavior has a deny-path test, not only a happy-path
  test;
- changes to a trust boundary update `docs/threat-model.md`;
- gateway changes include a benchmark or explain why latency is unaffected;
- user-visible behavior and operational changes are documented.
