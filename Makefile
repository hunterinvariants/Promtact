.PHONY: help fmt vet test race coverage verify build container

COVERAGE_MIN ?= 50

help:
	@echo "fmt       Format Go sources"
	@echo "vet       Run static analysis"
	@echo "test      Run unit and integration tests"
	@echo "race      Run tests with the race detector"
	@echo "coverage  Enforce aggregate coverage (default: $(COVERAGE_MIN)%)"
	@echo "verify    Run the complete local quality gate"
	@echo "build     Build server and CLI"
	@echo "container Build the hardened container image"

fmt:
	gofmt -w $$(find cmd internal -name '*.go' -type f)

vet:
	go vet ./...

test:
	go test ./...

race:
	go test -race ./...

coverage:
	go test -coverprofile=.cache/coverage.out ./...
	@total=$$(go tool cover -func .cache/coverage.out | awk '/^total:/ {gsub("%","",$$3); print $$3}'); \
	awk -v total="$$total" -v minimum="$(COVERAGE_MIN)" 'BEGIN { \
	  printf "aggregate coverage: %.1f%% (minimum %.1f%%)\n", total, minimum; \
	  if (total + 0 < minimum + 0) exit 1 \
	}'

verify:
	sh ./scripts/verify.sh

build:
	go build -trimpath -o bin/promtact ./cmd/promtact
	go build -trimpath -o bin/promtactl ./cmd/promtactl

container:
	docker build -t promtact:local .
