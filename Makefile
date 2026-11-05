BASHLY := docker run --rm --user $(shell id -u):$(shell id -g) -v "$(CURDIR):/app" dannyben/bashly:2.0.0
SHELLCHECK := docker run --rm -v "$(CURDIR):/mnt:ro" koalaman/shellcheck:v0.10.0
TEST_SCRIPTS := $(addprefix /mnt/,$(wildcard test/*.sh test/suites/*.sh))
DISTROS ?= debian:12 ubuntu:22.04 ubuntu:24.04 debian:13

.PHONY: build dev lint test help

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
