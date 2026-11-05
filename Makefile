BASHLY := docker run --rm --user $(shell id -u):$(shell id -g) -v "$(CURDIR):/app" dannyben/bashly
SHELLCHECK := docker run --rm -v "$(CURDIR):/mnt:ro" koalaman/shellcheck:stable

.PHONY: build dev lint test help

build: ## Generate ./postgres-manager (production: no debug helpers)
	@$(BASHLY) generate --env production

dev: ## Regenerate on every change in src/
	@$(BASHLY) generate --watch

lint: build ## shellcheck the generated script and the test harness
	@$(SHELLCHECK) -S warning /mnt/postgres-manager /mnt/test/run.sh

test: build ## End-to-end test in Debian 12 and Ubuntu 24.04 containers
	@bash test/run.sh debian:12
	@bash test/run.sh ubuntu:24.04

help:
	@grep -E '^[a-z]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-8s %s\n", $$1, $$2}'
