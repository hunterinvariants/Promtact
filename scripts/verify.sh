#!/usr/bin/env sh
set -eu

coverage_min="${COVERAGE_MIN:-50}"
mkdir -p .cache

unformatted="$(gofmt -l cmd internal)"
if [ -n "$unformatted" ]; then
  echo "Go files require formatting:"
  echo "$unformatted"
  exit 1
fi

go vet ./...
go test -race -coverprofile=.cache/coverage.out ./...

total="$(go tool cover -func .cache/coverage.out | awk '/^total:/ {gsub("%","",$3); print $3}')"
awk -v total="$total" -v minimum="$coverage_min" 'BEGIN {
  printf "aggregate coverage: %.1f%% (minimum %.1f%%)\n", total, minimum
  if (total + 0 < minimum + 0) exit 1
}'

go build -trimpath -o .cache/promtact ./cmd/promtact
go build -trimpath -o .cache/promtactl ./cmd/promtactl
