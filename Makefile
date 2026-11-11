# Repository commands. Task implementation lives in internal/tools/task-workflow.
.DEFAULT_GOAL := help

STABLE_MODULES := core router contract middleware security store health log metrics
BOUNDARY_CHECKS := dependency-rules cross-extension-deps agent-workflow module-manifests \
	reference-layout extension-maturity extension-beta-evidence public-entrypoints-sync
TASK_TARGETS := milestone check-spec check-plan check-verify check-card new-milestone \
	new-plan new-card new-verify run-card milestone-status
export M C N TITLE ID SLUG R TASK MODULE COVERAGE_FILE

.PHONY: help validate-diff validate-diff-dry bundle $(TASK_TARGETS) \
	gates go-gates check boundary-check api-check doc-check fmt fmt-check vet lint test test-race \
	reference-vet reference-test stable-coverage cli-check website-check-generated \
	website-gates website-sync setup-hooks tooling-test pre-push-check

help: ## Show available commands
	@awk 'BEGIN {FS = ":.*## "} /^## / {printf "\n%s\n", substr($$0, 4)} /^[a-z][a-z-]*:.*## / {printf "  %-25s %s\n", $$1, $$2}' Makefile

## Daily development
validate-diff: ## Select and run gates for the current diff
	@cd cmd/plumego && go run . agents validate-diff --dir ../..

validate-diff-dry: ## Preview the selected gates
	@cd cmd/plumego && go run . agents validate-diff --dir ../.. --dry-run

fmt: ## Format Go source files in place
	gofmt -w .

fmt-check: ## Check Go formatting without changing files
	@set -e; files=$$(gofmt -l .); test -z "$$files" || { printf '%s\n' "$$files"; exit 1; }

vet: ## Run go vet on the root module
	go vet ./...

lint: ## Run golangci-lint v2 (zero findings required)
	golangci-lint run ./...

test: ## Run root module tests
	go test -timeout 20s ./...

test-race: ## Run root module race tests
	go test -race -timeout 60s ./...

## Quality gates
# Use ordered recursive calls: make -j must not overlap expensive gate suites.
check: ## Run source, boundary, API, documentation, and tooling checks
	$(MAKE) -j1 fmt-check tooling-test boundary-check api-check doc-check

gates: ## Run complete Go validation (lint and website are separate)
	$(MAKE) -j1 go-gates cli-check

go-gates: ## Run root and reference Go validation used by CI
	$(MAKE) -j1 check vet reference-vet test-race reference-test stable-coverage

pre-push-check: ## Run the same quick checks for every branch
	$(MAKE) -j1 fmt-check vet doc-check test

tooling-test: ## Test repository command orchestration in isolation
	python3 internal/tools/task-workflow-test.py

boundary-check: ## Validate repository boundaries and manifests
	@set -e; for check in $(BOUNDARY_CHECKS); do go run ./internal/checks/$$check; done
	go run ./internal/checks/deprecation-inventory -strict

api-check: ## Compare stable module API snapshots
	@set -e; tmp=$$(mktemp -d "$${TMPDIR:-/tmp}/plumego-stable-api.XXXXXX"); \
	trap 'rm -rf "$$tmp"' EXIT; \
	for module in $(STABLE_MODULES); do \
	  pattern="./$$module/..."; \
	  case "$$module" in core|router) pattern="./$$module" ;; esac; \
	  go run ./internal/checks/extension-api-snapshot -module "$$pattern" -out "$$tmp/$$module.snapshot"; \
	  go run ./internal/checks/extension-api-snapshot -compare "docs/evidence/stable-api/snapshots/$$module-head.snapshot" "$$tmp/$$module.snapshot"; \
	done

doc-check: ## Compile and run documentation snippets
	go run ./internal/tools/doc-snippets

reference-vet: ## Run go vet for standalone reference modules
reference-test: ## Test standalone reference modules
reference-vet reference-test:
	@set -e; for mod in reference/*/go.mod; do \
	  dir=$$(dirname "$$mod"); echo "==> $$dir"; \
	  go -C "$$dir" $(patsubst reference-%,%,$@) ./...; \
	done

stable-coverage: ## Require 90% stable coverage; optionally save COVERAGE_FILE=<path>
	@set -e; tmp=$$(mktemp -d "$${TMPDIR:-/tmp}/plumego-coverage.XXXXXX"); \
	trap 'rm -rf "$$tmp"' EXIT; \
	go test -coverprofile="$$tmp/stable.cover" ./core ./router ./middleware/... ./contract ./security/... ./store/... ./health ./log ./metrics >"$$tmp/test.log" 2>&1 || { cat "$$tmp/test.log"; exit 1; }; \
	go tool cover -func="$$tmp/stable.cover" >"$$tmp/coverage.txt"; \
	total=$$(awk '/^total:/ {gsub("%","",$$3); print $$3}' "$$tmp/coverage.txt"); \
	echo "Stable-module total coverage: $$total%"; \
	awk -v total="$$total" 'BEGIN {if (total == "" || total+0 < 90) exit 1}' || { cat "$$tmp/test.log"; exit 1; }; \
	if [ -n "$$COVERAGE_FILE" ]; then cp "$$tmp/stable.cover" "$$COVERAGE_FILE"; fi

cli-check: ## Run CLI vet and race tests
	cd cmd/plumego && go vet ./...
	cd cmd/plumego && go test -race -timeout 60s ./...

## Documentation website
website-sync: ## Regenerate website data (does not stage files)
	cd website && pnpm sync

website-check-generated: ## Fail if regeneration changes website data
	@set -e; tmp=$$(mktemp -d "$${TMPDIR:-/tmp}/plumego-website.XXXXXX"); \
	trap 'rm -rf "$$tmp"' EXIT; \
	git diff HEAD -- website/src/generated >"$$tmp/before"; \
	$(MAKE) website-sync; \
	git diff HEAD -- website/src/generated >"$$tmp/after"; \
	cmp -s "$$tmp/before" "$$tmp/after" || { echo "Website data is stale. Run make website-sync and commit the generated files."; exit 1; }

website-gates: ## Check generated data, validate, and build the website
	$(MAKE) website-check-generated
	cd website && pnpm check
	cd website && pnpm build

## Task artifacts
bundle: ## Generate an execution bundle: TASK=<recipe> MODULE=<path>
	@test -n "$$TASK" && test -n "$$MODULE" || { echo "TASK and MODULE are required."; exit 1; }
	@cd cmd/plumego && go run . agents bundle --task "$$TASK" --module "$$MODULE" --dir ../.. --output ../../.agent-bundle.yaml
	@echo "Bundle written to .agent-bundle.yaml"

milestone: ## Launch a milestone: M=active/M-001-slug
check-spec: ## Validate a milestone: M=active/M-001-slug
check-plan: ## Validate a milestone plan: M=active/M-001-slug
check-verify: ## Validate a milestone report: M=active/M-001-slug
check-card: ## Validate a card: C=active/0001-slug[.md]
new-milestone: ## Create a milestone: N=001 TITLE="My feature"
new-plan: ## Create a milestone plan: M=active/M-001-slug
new-verify: ## Create a milestone report: M=active/M-001-slug
new-card: ## Create a card: ID=0001 SLUG=name M=M-001 R=fix-bug
run-card: ## Validate, bundle, and launch a card: C=active/0001-slug[.md]
milestone-status: ## Show milestone checkpoints: M=active/M-001-slug

$(TASK_TARGETS):
	@bash internal/tools/task-workflow $@

## Local setup
setup-hooks: ## Install the local pre-push hook
	@hook=$$(git rev-parse --git-path hooks/pre-push); \
	cp .githooks/pre-push "$$hook" && chmod +x "$$hook"
	@echo "Installed pre-push hook (make pre-push-check)."
