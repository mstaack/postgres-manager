ARCH ?= $(shell uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
LDFLAGS := -s -w -X main.version=$(shell cat VERSION)
GOLANGCI := docker run --rm -v "$(CURDIR):/app" -w /app golangci/golangci-lint:v2.4.0
DISTROS ?= debian:12 ubuntu:22.04 ubuntu:24.04 debian:13

.PHONY: build unit lint test help

build: ## Build dist/pgm-linux-$(ARCH) (ARCH=amd64|arm64)
	@CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH) go build -trimpath -ldflags "$(LDFLAGS)" -o dist/pgm-linux-$(ARCH) ./cmd/pgm

unit: ## Go unit tests
	@go test ./...

lint: ## go vet + golangci-lint
	@go vet ./... && $(GOLANGCI) golangci-lint run ./...

test: build ## End-to-end tests in containers (DISTROS="debian:12 …", SUITES="users menu")
	@set -e; for d in $(DISTROS); do bash test/run.sh $$d $(SUITES); done

help:
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-8s %s\n", $$1, $$2}'
