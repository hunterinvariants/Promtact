$ErrorActionPreference = "Stop"

$coverageMinimum = if ($env:COVERAGE_MIN) { [double]$env:COVERAGE_MIN } else { 50.0 }
New-Item -ItemType Directory -Force -Path ".cache" | Out-Null

$unformatted = @(gofmt -l cmd internal)
if ($unformatted.Count -gt 0) {
    Write-Error "Go files require formatting:`n$($unformatted -join "`n")"
}

go vet ./...
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

$testArgs = @("test", "-coverprofile=.cache/coverage.out")
if (Get-Command gcc -ErrorAction SilentlyContinue) {
    $testArgs += "-race"
} else {
    Write-Host "race detector skipped locally: no C compiler found (CI enforces it)"
}
$testArgs += "./..."
go @testArgs
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

$totalLine = go tool cover -func .cache/coverage.out | Select-Object -Last 1
if ($totalLine -notmatch '(\d+(?:\.\d+)?)%') {
    Write-Error "Could not parse aggregate coverage from: $totalLine"
}
$coverage = [double]$Matches[1]
Write-Host ("aggregate coverage: {0:N1}% (minimum {1:N1}%)" -f $coverage, $coverageMinimum)
if ($coverage -lt $coverageMinimum) {
    Write-Error "Coverage gate failed."
}

go build -trimpath -o .cache/promtact.exe ./cmd/promtact
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
go build -trimpath -o .cache/promtactl.exe ./cmd/promtactl
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
