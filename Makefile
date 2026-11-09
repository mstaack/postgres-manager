ARCH ?= $(shell uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//')
LDFLAGS := -s -w -X main.version=$(VERSION)
GOLANGCI := docker run --rm -v "$(CURDIR):/app" -w /app golangci/golangci-lint:v2.4.0
DISTROS ?= debian:12 debian:13 ubuntu:22.04 ubuntu:24.04

.PHONY: build build-all unit lint test help

build: ## Build dist/pgm-linux-$(ARCH) (ARCH=amd64|arm64)
	@CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH) go build -trimpath -ldflags "$(LDFLAGS)" -o dist/pgm-linux-$(ARCH) ./cmd/pgm

build-all: ## Build Linux and macOS binaries for amd64 and arm64
	@set -e; for os in linux darwin; do for arch in amd64 arm64; do \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o dist/pgm-$$os-$$arch ./cmd/pgm; done; done

unit: ## Unit tests
	@go test ./...

lint: ## go vet + golangci-lint (Docker)
	@go vet -tags e2e ./... && $(GOLANGCI) golangci-lint run ./...

test: ## End-to-end tests in containers (DISTROS="debian:12 …", RUN="Users|Menu")
	@E2E_IMAGES="$(DISTROS)" go test -tags e2e -count=1 -v -timeout 120m ./test/e2e $(if $(RUN),-run '$(RUN)')

help:
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-10s %s\n", $$1, $$2}'
