BASHLY := docker run --rm --user $(shell id -u):$(shell id -g) -v "$(CURDIR):/app" dannyben/bashly:2.0.0
SHELLCHECK := docker run --rm -v "$(CURDIR):/mnt:ro" koalaman/shellcheck:v0.10.0
TEST_SCRIPTS := $(addprefix /mnt/,$(wildcard test/*.sh test/suites/*.sh))
DISTROS ?= debian:12 ubuntu:22.04 ubuntu:24.04 debian:13

.PHONY: build dev lint test help go-build go-unit go-lint go-test

build: ## Generate ./postgres-manager (production: no debug helpers)
	@$(BASHLY) generate --env production

dev: ## Regenerate on every change in src/
	@$(BASHLY) generate --watch

lint: build ## shellcheck the generated script and the tests
	@$(SHELLCHECK) -S warning -x -P /mnt/test /mnt/postgres-manager $(TEST_SCRIPTS)

test: build ## End-to-end tests in containers (DISTROS="debian:12 …", SUITES="users menu")
	@set -e; for d in $(DISTROS); do bash test/run.sh $$d $(SUITES); done

help:
	@grep -E '^[a-z]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-8s %s\n", $$1, $$2}'

GO_ARCH ?= $(shell uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
GO_LDFLAGS := -s -w -X main.version=$(shell cat VERSION)
GOLANGCI := docker run --rm -v "$(CURDIR):/app" -w /app golangci/golangci-lint:v2.4.0

go-build: ## Build dist/pgm-linux-$(GO_ARCH) (GO_ARCH=amd64|arm64)
	@CGO_ENABLED=0 GOOS=linux GOARCH=$(GO_ARCH) go build -trimpath -ldflags "$(GO_LDFLAGS)" -o dist/pgm-linux-$(GO_ARCH) ./cmd/pgm

go-unit: ## Go unit tests
	@go test ./...

go-lint: ## go vet + golangci-lint
	@go vet ./... && $(GOLANGCI) golangci-lint run ./...

go-test: go-build ## E2E suites listed in test/go-suites against the Go binary
	@set -e; for d in $(DISTROS); do PGM_IMPL=go bash test/run.sh $$d $${SUITES:-$$(cat test/go-suites)}; done
