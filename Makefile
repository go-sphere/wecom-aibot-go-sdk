GO ?= go
GOLANGCI_LINT ?= golangci-lint

GO_MOD_DIRS := . examples/basic examples/agent
DIRECT_DEPS_TEMPLATE := {{if and (not .Main) (not .Indirect) (not .Replace)}}{{.Path}}{{end}}

# Resolve go-sphere modules straight from GitHub, bypassing the module proxy
# and its cached "@latest", which lags behind freshly pushed tags.
DIRECT_ORIGIN := GOPRIVATE=github.com/go-sphere/*

.DEFAULT_GOAL := check

.PHONY: deps-update tidy tidy-check fmt build test lint check

deps-update:
	@set -eu; \
	for dir in $(GO_MOD_DIRS); do \
		echo "==> updating $$dir"; \
		( cd "$$dir"; \
		  GOWORK=off $(DIRECT_ORIGIN) $(GO) mod tidy; \
		  deps="$$(GOWORK=off $(DIRECT_ORIGIN) $(GO) list -m -f '$(DIRECT_DEPS_TEMPLATE)' all)"; \
		  if [ -n "$$deps" ]; then GOWORK=off $(DIRECT_ORIGIN) $(GO) get -u $$deps; fi; \
		  GOWORK=off $(DIRECT_ORIGIN) $(GO) mod tidy ); \
	done

tidy:
	@set -eu; \
	for dir in $(GO_MOD_DIRS); do \
		echo "==> tidying $$dir"; \
		( cd "$$dir" && GOWORK=off $(GO) mod tidy ); \
	done

# Non-mutating counterpart of tidy, for CI: fails if any module's go.mod/go.sum
# is not what a consumer would resolve.
tidy-check:
	@set -eu; \
	for dir in $(GO_MOD_DIRS); do \
		echo "==> checking dependencies in $$dir"; \
		( cd "$$dir" && GOWORK=off $(GO) mod tidy -diff ); \
	done

fmt:
	@set -eu; \
	for dir in $(GO_MOD_DIRS); do \
		echo "==> formatting $$dir"; \
		( cd "$$dir" && $(GO) fmt ./... && \
		  $(GOLANGCI_LINT) fmt --no-config --enable gofmt --enable goimports ); \
	done

# -o /dev/null: the examples are main packages, and compiling them must not
# drop binaries into the tree.
build:
	@set -eu; \
	for dir in $(GO_MOD_DIRS); do \
		echo "==> building $$dir"; \
		( cd "$$dir" && $(GO) build -o /dev/null ./... ); \
	done

test:
	@set -eu; \
	for dir in $(GO_MOD_DIRS); do \
		echo "==> testing $$dir"; \
		( cd "$$dir" && $(GO) test ./... ); \
	done

lint:
	@set -eu; \
	for dir in $(GO_MOD_DIRS); do \
		echo "==> linting $$dir"; \
		( cd "$$dir"; \
		  $(GOLANGCI_LINT) fmt --no-config --enable gofmt --enable goimports --diff; \
		  $(GO) vet ./...; \
		  $(GOLANGCI_LINT) run --no-config ); \
	done

check: tidy-check
	$(MAKE) lint
	$(MAKE) test
