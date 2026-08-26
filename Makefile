# MAS-Turbo — build, test and delivery targets.
# Gate targets mirror specs/001-mvp-core/tasks.md checkpoint gates.

SHELL       := /usr/bin/env bash
MODULE      := github.com/zlrrr/multi-agent-system-turbo
BIN_DIR     := bin
DIST_DIR    := dist
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE  ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS     := -s -w \
  -X $(MODULE)/internal/version.Version=$(VERSION) \
  -X $(MODULE)/internal/version.Commit=$(COMMIT) \
  -X $(MODULE)/internal/version.BuildDate=$(BUILD_DATE)
GO          ?= go
IMAGE       ?= mas-turbo
IMAGE_TAG   ?= $(VERSION)

.DEFAULT_GOAL := help

## help: list targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //' | awk -F: '{printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

## build: compile mas and sddctl into bin/
build:
	@mkdir -p $(BIN_DIR)
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/mas ./cmd/mas
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/sddctl ./cmd/sddctl

## fmt: format all Go sources
fmt:
	$(GO) fmt ./...

## fmt-check: fail if any file is unformatted
fmt-check:
	@out=$$(gofmt -l . | grep -v '^vendor/' || true); \
	if [ -n "$$out" ]; then echo "unformatted files:"; echo "$$out"; exit 1; fi

## vet: run go vet
vet:
	$(GO) vet ./...

## lint: run golangci-lint when available, otherwise vet
lint:
	@if command -v golangci-lint >/dev/null 2>&1; then golangci-lint run ./...; \
	else echo "golangci-lint not installed; falling back to go vet"; $(GO) vet ./...; fi

## test: run the full test suite
test:
	$(GO) test ./...

## test-race: run the full suite under the race detector
test-race:
	$(GO) test -race ./...

## cover: run tests with a coverage profile
cover:
	$(GO) test -coverprofile=coverage.out -covermode=atomic ./...
	@$(GO) tool cover -func=coverage.out | tail -1

## test-foundation: gate G-A
test-foundation:
	$(GO) test ./pkg/... ./internal/core/... ./internal/config/... ./internal/safety/... ./internal/obs/...

## test-capability: gate G-B
test-capability:
	$(GO) test ./internal/tool/... ./internal/collector/... ./internal/envadapter/... ./internal/source/...

## test-knowledge: gate G-C
test-knowledge:
	$(GO) test ./internal/knowledge/... ./internal/rules/...

## test-reasoning: gate G-D
test-reasoning:
	$(GO) test -race ./internal/llm/... ./internal/agent/... ./internal/orchestrator/...

## test-output: gate G-E
test-output:
	$(GO) test ./internal/report/... ./internal/store/... ./internal/service/...

## test-surfaces: gate G-F
test-surfaces:
	$(GO) test ./internal/cli/... ./internal/httpapi/...

## eval: run the corpus against the recorded baseline; non-zero exit on regression
eval: build
	$(BIN_DIR)/mas eval --matrix --baseline internal/eval/baseline.json

## eval-baseline: re-record the baseline (review the diff before committing)
eval-baseline: build
	$(BIN_DIR)/mas eval --matrix --write-baseline internal/eval/baseline.json

## sdd-verify: bilingual parity, cascade staleness and requirement coverage
sdd-verify:
	$(GO) run ./cmd/sddctl verify

## errcodes-docs: regenerate the bilingual error-code references
errcodes-docs: build
	$(BIN_DIR)/mas errcodes --format markdown --lang en > docs/en/error-codes.md
	$(BIN_DIR)/mas errcodes --format markdown --lang zh > docs/zh/error-codes.md

## ci: everything CI enforces
ci: fmt-check vet lint test-race sdd-verify build eval

## docker: build the container image
docker:
	docker build -t $(IMAGE):$(IMAGE_TAG) \
	  --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg BUILD_DATE=$(BUILD_DATE) .

## demo: run the bundled offline demo and write a report
demo: build
	./scripts/demo.sh

## dist: cross-compile release binaries with checksums
dist:
	@mkdir -p $(DIST_DIR)
	@for platform in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do \
	  os=$${platform%/*}; arch=$${platform#*/}; \
	  echo "building $$os/$$arch"; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags '$(LDFLAGS)' \
	    -o $(DIST_DIR)/mas-$$os-$$arch ./cmd/mas || exit 1; \
	done
	@cd $(DIST_DIR) && sha256sum mas-* > SHA256SUMS

## clean: remove build outputs
clean:
	rm -rf $(BIN_DIR) $(DIST_DIR) coverage.out

.PHONY: help build fmt fmt-check vet lint test test-race cover \
        test-foundation test-capability test-knowledge test-reasoning test-output test-surfaces \
        eval eval-baseline sdd-verify errcodes-docs ci docker demo dist clean
