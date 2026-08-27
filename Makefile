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
PLATFORMS   := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64
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

## console-check: parse the web console's script when a JS engine is available
# The console has no build step and no package manifest (feature 012, NFR-001),
# so nothing would otherwise notice a syntax error before a reader's browser
# did. `node --check` parses without executing; when node is absent this says so
# and passes, the same shape as `lint` above.
console-check:
	@if command -v node >/dev/null 2>&1; then \
	  node --check internal/httpapi/assets/app.js && echo "console script parses"; \
	else echo "node not installed; skipping the console syntax check"; fi

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
ci: fmt-check vet lint console-check test-race sdd-verify build eval

## docker: build the container image
docker:
	docker build -t $(IMAGE):$(IMAGE_TAG) \
	  --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg BUILD_DATE=$(BUILD_DATE) .

## demo: run the bundled offline demo and write a report
demo: build
	./scripts/demo.sh

## dist: cross-compile release binaries, package them with the bilingual docs, and checksum everything
# Packaging lives here rather than in the release workflow because a step that
# only runs when a tag is pushed is a step nobody has ever run. The first
# attempt to cut v0.1.0 failed here, on a bug that had been present since the
# workflow was written: the docs were flattened into one directory, where
# `docs/en/user-manual.md` and `docs/zh/user-manual.md` collide. In a Makefile
# target, `make ci` runs it on every push instead.
dist: dist-binaries dist-package dist-verify

dist-binaries:
	@mkdir -p $(DIST_DIR)
	@for platform in $(PLATFORMS); do \
	  os=$${platform%/*}; arch=$${platform#*/}; \
	  echo "building $$os/$$arch"; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags '$(LDFLAGS)' \
	    -o $(DIST_DIR)/mas-$$os-$$arch ./cmd/mas || exit 1; \
	done

## dist-package: wrap each cross-compiled binary with the licence, both READMEs and the bilingual docs
# SHA256SUMS covers the tarballs and nothing else, because the tarballs are what
# the release publishes. It previously also listed the bare cross-compiled
# binaries, which are intermediates and never uploaded — so `sha256sum -c
# SHA256SUMS` against a downloaded release would have failed on four missing
# files. A checksum file that cannot be checked is worse than none.
dist-package:
	@set -e; cd $(DIST_DIR); rm -rf pkg; \
	for platform in $(PLATFORMS); do \
	  os=$${platform%/*}; arch=$${platform#*/}; \
	  slug="$$os-$$arch"; \
	  root="pkg/mas-$(VERSION)-$$slug"; \
	  mkdir -p "$$root/docs"; \
	  cp "mas-$$slug" "$$root/mas"; \
	  cp ../LICENSE ../README.md ../README.zh.md "$$root/"; \
	  cp ../deploy/config/mas.example.yaml "$$root/"; \
	  cp -R ../docs/en ../docs/zh "$$root/docs/"; \
	  tar -czf "mas-$$slug.tar.gz" -C pkg "mas-$(VERSION)-$$slug"; \
	  echo "packaged mas-$$slug.tar.gz"; \
	done; \
	rm -rf pkg; \
	rm -f SHA256SUMS; \
	sha256sum mas-*.tar.gz > SHA256SUMS; \
	cat SHA256SUMS

## dist-verify: assert the packaged artifacts are usable, not merely produced
dist-verify:
	./scripts/verify-dist.sh $(DIST_DIR) $(VERSION)

## clean: remove build outputs
clean:
	rm -rf $(BIN_DIR) $(DIST_DIR) coverage.out

.PHONY: help build fmt fmt-check vet lint console-check test test-race cover \
        test-foundation test-capability test-knowledge test-reasoning test-output test-surfaces \
        eval eval-baseline sdd-verify errcodes-docs ci docker demo \
        dist dist-binaries dist-package dist-verify clean
