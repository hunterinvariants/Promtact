param(
    [string]$BaseUrl = "http://localhost:8080",
    [int]$Requests = 10000,
    [int]$Concurrency = 32,
    [string]$MaxP99 = "25ms",
    [double]$MinRps = 1000
)
$ErrorActionPreference = "Stop"
$env:GOCACHE = Join-Path (Get-Location) ".gocache"
go test ./internal/policy -run 'Test.*(Identity|Provenance|Taint|History|Obfuscat)' -count=1
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
go test ./internal/policy -bench GateToolCall -benchmem -count=5
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
go run ./cmd/promtactl validate --url $BaseUrl --json
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
go run ./cmd/promtactl bench --url $BaseUrl --requests $Requests --concurrency $Concurrency --max-p99 $MaxP99 --min-rps $MinRps --json
exit $LASTEXITCODE
