# plumego — root Makefile
# Minimal targets. Most work happens via codex --yolo or go toolchain directly.

.PHONY: help bundle validate-diff validate-diff-dry milestone check-spec check-plan check-card check-verify new-milestone new-plan new-card new-verify run-card milestone-status gates website-gates fmt vet test test-race reference-vet reference-test setup-hooks website-sync

# Default: show help
help:

# ── Gate Profile Auto-Selection ───────────────────────────────────────────────

validate-diff: ## Auto-select and run minimal gate profile for current git diff
	@cd cmd/plumego && go run . agents validate-diff --dir ../..

validate-diff-dry: ## Print gate commands validate-diff would run without executing them
	@cd cmd/plumego && go run . agents validate-diff --dir ../.. --dry-run

# ── Task Execution Bundle ─────────────────────────────────────────────────────

bundle: ## Generate a task execution bundle: make bundle TASK=http_endpoint MODULE=x/tenant
	@if [ -z "$(TASK)" ] || [ -z "$(MODULE)" ]; then \
	  echo "Error: TASK and MODULE are required."; \
	  echo "  Example: make bundle TASK=http_endpoint MODULE=x/tenant"; \
	  echo "  Task types: http_endpoint, middleware, bugfix_triage, symbol_change,"; \
	  echo "              tenant_policy_change, stable_root_boundary_review, control_plane"; \
	  exit 1; \
	fi
	@cd cmd/plumego && go run . agents bundle \
	  --task $(TASK) \
	  --module $(MODULE) \
	  --dir ../.. \
	  --output ../../.agent-bundle.yaml
	@echo "Bundle written to .agent-bundle.yaml"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
	  awk 'BEGIN {FS = ":.*?## "}; {printf "  %-20s %s\n", $$1, $$2}'

# ── Milestone Runner ──────────────────────────────────────────────────────────

## Run a milestone spec through codex --yolo.
## Usage: make milestone M=active/M-001-short-name
##   M must be a milestone directory path relative to tasks/milestones/.
milestone: ## Run a milestone spec: make milestone M=active/M-001-short-name
	@if [ -z "$(M)" ]; then \
	  echo "Error: M is required. Example: make milestone M=active/M-001-short-name"; \
	  exit 1; \
	fi
	@INPUT="$(M)"; \
	BASE=$${INPUT##*/}; \
	ID=$$(printf '%s\n' "$$BASE" | sed -nE 's/.*(M-[0-9][0-9][0-9]).*/\1/p'); \
	if [ -z "$$ID" ]; then echo "Error: M must include a milestone id like M-001"; exit 1; fi; \
	SPEC=tasks/milestones/$$INPUT/$$ID.md; \
	if [ ! -f "$$SPEC" ]; then SPEC=tasks/milestones/$$INPUT.md; fi; \
	if [ ! -f "$$SPEC" ]; then \
	  for candidate in tasks/milestones/active/$$ID-*/$$ID.md tasks/milestones/done/$$ID-*/$$ID.md; do \
	    if [ -f "$$candidate" ]; then SPEC=$$candidate; break; fi; \
	  done; \
	fi; \
	PLAN=$$(dirname "$$SPEC")/plan-$$ID.md; \
	if [ ! -f "$$SPEC" ]; then \
	  echo "Error: $$SPEC not found."; \
	  echo "Available specs:"; \
	  find tasks/milestones/active -mindepth 1 -maxdepth 1 -type d -print 2>/dev/null | sed 's|tasks/milestones/||'; \
	  exit 1; \
	fi; \
	if [ ! -f "$$PLAN" ]; then \
	  echo "Error: $$PLAN not found."; \
	  echo "Run: make new-plan M=$(M)"; \
	  echo "Then fill the plan and validate it with: make check-plan M=$(M)"; \
	  exit 1; \
	fi; \
	echo "Validating spec before launch ..."; \
	internal/tools/check-spec "$$SPEC" || { echo "Fix spec errors above, then re-run."; exit 1; }; \
	echo "Validating plan before launch ..."; \
	internal/tools/check-spec "$$PLAN" || { echo "Fix plan errors above, then re-run."; exit 1; }; \
	echo "Launching codex --yolo on $$SPEC ..."; \
	codex --yolo "$$(cat "$$SPEC")"

check-spec: ## Validate a milestone spec: make check-spec M=active/M-001-short-name
	@if [ -z "$(M)" ]; then \
	  echo "Error: M is required. Example: make check-spec M=active/M-001-short-name"; \
	  exit 1; \
	fi
	@INPUT="$(M)"; \
	BASE=$${INPUT##*/}; \
	ID=$$(printf '%s\n' "$$BASE" | sed -nE 's/.*(M-[0-9][0-9][0-9]).*/\1/p'); \
	if [ -z "$$ID" ]; then echo "Error: M must include a milestone id like M-001"; exit 1; fi; \
	SPEC=tasks/milestones/$$INPUT/$$ID.md; \
	if [ ! -f "$$SPEC" ]; then SPEC=tasks/milestones/$$INPUT.md; fi; \
	if [ ! -f "$$SPEC" ]; then \
	  for candidate in tasks/milestones/active/$$ID-*/$$ID.md tasks/milestones/done/$$ID-*/$$ID.md; do \
	    if [ -f "$$candidate" ]; then SPEC=$$candidate; break; fi; \
	  done; \
	fi; \
	internal/tools/check-spec "$$SPEC"

check-plan: ## Validate a milestone plan: make check-plan M=active/M-001-short-name
	@if [ -z "$(M)" ]; then \
	  echo "Error: M is required. Example: make check-plan M=active/M-001-short-name"; \
	  exit 1; \
	fi
	@INPUT="$(M)"; \
	BASE=$${INPUT##*/}; \
	ID=$$(printf '%s\n' "$$BASE" | sed -nE 's/.*(M-[0-9][0-9][0-9]).*/\1/p'); \
	if [ -z "$$ID" ]; then echo "Error: M must include a milestone id like M-001"; exit 1; fi; \
	SPEC=tasks/milestones/$$INPUT/$$ID.md; \
	if [ ! -f "$$SPEC" ]; then SPEC=tasks/milestones/$$INPUT.md; fi; \
	if [ ! -f "$$SPEC" ]; then \
	  for candidate in tasks/milestones/active/$$ID-*/$$ID.md tasks/milestones/done/$$ID-*/$$ID.md; do \
	    if [ -f "$$candidate" ]; then SPEC=$$candidate; break; fi; \
	  done; \
	fi; \
	PLAN=$$(dirname "$$SPEC")/plan-$$ID.md; \
	if [ ! -f "$$PLAN" ]; then \
	  echo "Error: $$PLAN not found."; \
	  echo "Create it with: make new-plan M=$(M)"; \
	  exit 1; \
	fi; \
	internal/tools/check-spec "$$PLAN"

check-verify: ## Validate a milestone verify report: make check-verify M=active/M-001-short-name
	@if [ -z "$(M)" ]; then \
	  echo "Error: M is required. Example: make check-verify M=active/M-001-short-name"; \
	  exit 1; \
	fi
	@INPUT="$(M)"; \
	BASE=$${INPUT##*/}; \
	ID=$$(printf '%s\n' "$$BASE" | sed -nE 's/.*(M-[0-9][0-9][0-9]).*/\1/p'); \
	if [ -z "$$ID" ]; then echo "Error: M must include a milestone id like M-001"; exit 1; fi; \
	SPEC=tasks/milestones/$$INPUT/$$ID.md; \
	if [ ! -f "$$SPEC" ]; then SPEC=tasks/milestones/$$INPUT.md; fi; \
	if [ ! -f "$$SPEC" ]; then \
	  for candidate in tasks/milestones/active/$$ID-*/$$ID.md tasks/milestones/done/$$ID-*/$$ID.md; do \
	    if [ -f "$$candidate" ]; then SPEC=$$candidate; break; fi; \
	  done; \
	fi; \
	VERIFY=$$(dirname "$$SPEC")/verify-$$ID.md; \
	if [ ! -f "$$VERIFY" ]; then \
	  echo "Error: $$VERIFY not found."; \
	  echo "Create it with: make new-verify M=$(M)"; \
	  exit 1; \
	fi; \
	internal/tools/check-spec "$$VERIFY"

check-card: ## Validate a task card: make check-card C=active/0001-slice-router-work
	@if [ -z "$(C)" ]; then \
	  echo "Error: C is required. Example: make check-card C=active/0001-slice-router-work or C=blocked/0001-waiting-on-release"; \
	  exit 1; \
	fi
	@internal/tools/check-spec tasks/cards/$(C).md

new-milestone: ## Scaffold a new milestone spec: make new-milestone N=001 TITLE="My feature"
	@if [ -z "$(N)" ] || [ -z "$(TITLE)" ]; then \
	  echo "Error: N and TITLE are required."; \
	  echo "  Example: make new-milestone N=001 TITLE=\"Add ResourceHandler\""; \
	  exit 1; \
	fi
	@SLUG=$$(printf '%s\n' "$(TITLE)" | tr '[:upper:]' '[:lower:]' | sed -E 's/[^a-z0-9]+/-/g; s/^-//; s/-$$//'); \
	DEST_DIR=tasks/milestones/active/M-$(N)-$$SLUG; \
	DEST=$$DEST_DIR/M-$(N).md; \
	if [ -e "$$DEST_DIR" ]; then \
	  echo "Error: $$DEST_DIR already exists."; exit 1; \
	fi; \
	mkdir -p "$$DEST_DIR"; \
	rewrite() { expr="$$1"; file="$$2"; tmp=$$(mktemp); sed "$$expr" "$$file" > "$$tmp" && mv "$$tmp" "$$file"; }; \
	cp tasks/milestones/TEMPLATE.md "$$DEST"; \
	TITLE_ESC=$$(printf '%s\n' "$(TITLE)" | sed 's/[&/]/\\&/g'); \
	rewrite "s/M-XXX: <Title>/M-$(N): $$TITLE_ESC/" "$$DEST"; \
	rewrite "s|milestone/M-XXX-<slug>|milestone/M-$(N)-<slug>|" "$$DEST"; \
	echo "Created: $$DEST"; \
	echo "Next: fill in Goal, Architecture Decisions, Context, Tasks, then:"; \
	echo "  make check-spec M=active/M-$(N)-$$SLUG"

new-plan: ## Scaffold a milestone plan: make new-plan M=active/M-001-short-name
	@if [ -z "$(M)" ]; then \
	  echo "Error: M is required. Example: make new-plan M=active/M-001-short-name"; \
	  exit 1; \
	fi
	@INPUT="$(M)"; \
	BASE=$${INPUT##*/}; \
	ID_HINT=$$(printf '%s\n' "$$BASE" | sed -nE 's/.*(M-[0-9][0-9][0-9]).*/\1/p'); \
	SPEC=tasks/milestones/$$INPUT/$$ID_HINT.md; \
	if [ ! -f "$$SPEC" ]; then SPEC=tasks/milestones/$$INPUT.md; fi; \
	if [ ! -f "$$SPEC" ]; then \
	  for candidate in tasks/milestones/active/$$ID_HINT-*/$$ID_HINT.md tasks/milestones/done/$$ID_HINT-*/$$ID_HINT.md; do \
	    if [ -f "$$candidate" ]; then SPEC=$$candidate; break; fi; \
	  done; \
	fi; \
	if [ ! -f "$$SPEC" ]; then \
	  echo "Error: $$SPEC not found."; \
	  exit 1; \
	fi; \
	ID=$$(sed -n 's/^# \(M-[0-9][0-9][0-9]\): .*$$/\1/p' "$$SPEC" | head -n1); \
	TITLE=$$(sed -n 's/^# M-[0-9][0-9][0-9]: \(.*\)$$/\1/p' "$$SPEC" | head -n1); \
	if [ -z "$$ID" ] || [ -z "$$TITLE" ]; then \
	  echo "Error: could not derive milestone ID/title from $$SPEC"; \
	  exit 1; \
	fi; \
	DEST=$$(dirname "$$SPEC")/plan-$$ID.md; \
	if [ -f "$$DEST" ]; then \
	  echo "Error: $$DEST already exists."; \
	  exit 1; \
	fi; \
	rewrite() { expr="$$1"; file="$$2"; tmp=$$(mktemp); sed "$$expr" "$$file" > "$$tmp" && mv "$$tmp" "$$file"; }; \
	cp tasks/milestones/PLAN_TEMPLATE.md "$$DEST"; \
	TITLE_ESC=$$(printf '%s\n' "$$TITLE" | sed 's/[&/]/\\&/g'); \
	rewrite "s/^# Plan for M-XXX: <Title>/# Plan for $$ID: $$TITLE_ESC/" "$$DEST"; \
	rewrite "s/M-XXX/$$ID/g" "$$DEST"; \
	rewrite "s/<Title>/$$TITLE_ESC/g" "$$DEST"; \
	echo "Created: $$DEST"; \
	echo "Next: fill the plan fields, then:"; \
	echo "  make check-plan M=$(M)"

new-verify: ## Scaffold a milestone verify report: make new-verify M=active/M-001-short-name
	@if [ -z "$(M)" ]; then \
	  echo "Error: M is required. Example: make new-verify M=active/M-001-short-name"; \
	  exit 1; \
	fi
	@INPUT="$(M)"; \
	BASE=$${INPUT##*/}; \
	ID_HINT=$$(printf '%s\n' "$$BASE" | sed -nE 's/.*(M-[0-9][0-9][0-9]).*/\1/p'); \
	SPEC=tasks/milestones/$$INPUT/$$ID_HINT.md; \
	if [ ! -f "$$SPEC" ]; then SPEC=tasks/milestones/$$INPUT.md; fi; \
	if [ ! -f "$$SPEC" ]; then \
	  for candidate in tasks/milestones/active/$$ID_HINT-*/$$ID_HINT.md tasks/milestones/done/$$ID_HINT-*/$$ID_HINT.md; do \
	    if [ -f "$$candidate" ]; then SPEC=$$candidate; break; fi; \
	  done; \
	fi; \
	if [ ! -f "$$SPEC" ]; then \
	  echo "Error: $$SPEC not found."; \
	  exit 1; \
	fi; \
	ID=$$(sed -n 's/^# \(M-[0-9][0-9][0-9]\): .*$$/\1/p' "$$SPEC" | head -n1); \
	TITLE=$$(sed -n 's/^# M-[0-9][0-9][0-9]: \(.*\)$$/\1/p' "$$SPEC" | head -n1); \
	if [ -z "$$ID" ] || [ -z "$$TITLE" ]; then \
	  echo "Error: could not derive milestone ID/title from $$SPEC"; \
	  exit 1; \
	fi; \
	DEST=$$(dirname "$$SPEC")/verify-$$ID.md; \
	if [ -f "$$DEST" ]; then \
	  echo "Error: $$DEST already exists."; \
	  exit 1; \
	fi; \
	rewrite() { expr="$$1"; file="$$2"; tmp=$$(mktemp); sed "$$expr" "$$file" > "$$tmp" && mv "$$tmp" "$$file"; }; \
	cp tasks/milestones/VERIFY_TEMPLATE.md "$$DEST"; \
	TITLE_ESC=$$(printf '%s\n' "$$TITLE" | sed 's/[&/]/\\&/g'); \
	rewrite "s/^# Verify M-XXX: <Title>/# Verify $$ID: $$TITLE_ESC/" "$$DEST"; \
	rewrite "s/M-XXX/$$ID/g" "$$DEST"; \
	rewrite "s/<Title>/$$TITLE_ESC/g" "$$DEST"; \
	echo "Created: $$DEST"; \
	echo "Next: fill the verify report, then:"; \
	echo "  make check-verify M=$(M)"

run-card: ## Execute a task card (validate → bundle → codex): make run-card C=active/0001-foo
	@if [ -z "$(C)" ]; then \
	  echo "Error: C is required. Example: make run-card C=active/0001-foo"; \
	  exit 1; \
	fi
	@CARD=tasks/cards/$(C).md; \
	if [ ! -f "$$CARD" ]; then \
	  echo "Error: $$CARD not found."; \
	  echo "Available cards:"; \
	  ls tasks/cards/active/*.md 2>/dev/null | sed 's|tasks/cards/||; s|\.md||'; \
	  exit 1; \
	fi; \
	echo "Validating card before launch ..."; \
	internal/tools/check-spec "$$CARD" || { echo "Fix card errors above, then re-run."; exit 1; }; \
	MODULE=$$(grep -m1 '^Primary Module:' "$$CARD" | sed 's/Primary Module:[[:space:]]*//' | tr -d '\r'); \
	RECIPE=$$(grep -m1 '^Recipe:' "$$CARD" | sed 's|Recipe:[[:space:]]*specs/change-recipes/||; s|\.yaml$$||' | tr -d '\r'); \
	if [ -z "$$MODULE" ] || [ -z "$$RECIPE" ]; then \
	  echo "Error: card must have 'Primary Module:' and 'Recipe:' filled before running."; \
	  exit 1; \
	fi; \
	echo "Generating bundle for task=$$RECIPE module=$$MODULE ..."; \
	(cd cmd/plumego && go run . agents bundle \
	  --task "$$RECIPE" \
	  --module "$$MODULE" \
	  --dir ../.. \
	  --output ../../.agent-bundle.yaml) || { echo "Bundle generation failed."; exit 1; }; \
	echo "Bundle written to .agent-bundle.yaml"; \
	echo "Launching codex --yolo on $$CARD ..."; \
	codex --yolo "$$(cat "$$CARD")"

milestone-status: ## Show checkpoint progress for a milestone: make milestone-status M=active/M-001-short-name
	@if [ -z "$(M)" ]; then \
	  echo "Error: M is required. Example: make milestone-status M=active/M-001-short-name"; \
	  exit 1; \
	fi
	@INPUT="$(M)"; \
	BASE=$${INPUT##*/}; \
	ID=$$(printf '%s\n' "$$BASE" | sed -nE 's/.*(M-[0-9][0-9][0-9]).*/\1/p'); \
	if [ -z "$$ID" ]; then echo "Error: M must include a milestone id like M-001"; exit 1; fi; \
	SPEC=tasks/milestones/$$INPUT/$$ID.md; \
	if [ ! -f "$$SPEC" ]; then SPEC=tasks/milestones/$$INPUT.md; fi; \
	if [ ! -f "$$SPEC" ]; then \
	  for candidate in tasks/milestones/active/$$ID-*/$$ID.md tasks/milestones/done/$$ID-*/$$ID.md; do \
	    if [ -f "$$candidate" ]; then SPEC=$$candidate; break; fi; \
	  done; \
	fi; \
	DIR=$$(dirname "$$SPEC"); \
	CHECKPOINT=$$DIR/checkpoint-$$ID.json; \
	PLAN=$$DIR/plan-$$ID.md; \
	echo "=== Milestone $$ID Status ==="; \
	if [ ! -f "$$PLAN" ]; then \
	  echo "  Plan:        NOT FOUND  (run: make new-plan M=$(M))"; \
	else \
	  echo "  Plan:        found"; \
	fi; \
	if [ ! -f "$$CHECKPOINT" ]; then \
	  echo "  Checkpoints: none recorded"; \
	else \
	  echo "  Checkpoints:"; \
	  grep -o '"phase":"[^"]*","passed":[^,}]*' "$$CHECKPOINT" 2>/dev/null | \
	    sed 's/"phase":"//; s/","passed"://; s/true/ ✓ passed/; s/false/ ✗ failed/' | \
	    sed 's/^/    /' || echo "    (could not parse checkpoint file)"; \
	fi

new-card: ## Scaffold a task card: make new-card ID=0001 SLUG=slice-router-work M=M-001 R=fix-bug
	@if [ -z "$(ID)" ] || [ -z "$(SLUG)" ] || [ -z "$(M)" ]; then \
	  echo "Error: ID, SLUG, and M are required."; \
	  echo "  Example: make new-card ID=0001 SLUG=slice-router-work M=M-001 R=fix-bug"; \
	  exit 1; \
	fi
	@DEST=tasks/cards/active/$(ID)-$(SLUG).md; \
	if [ -f "$$DEST" ]; then \
	  echo "Error: $$DEST already exists."; \
	  exit 1; \
	fi; \
	rewrite() { expr="$$1"; file="$$2"; tmp=$$(mktemp); sed "$$expr" "$$file" > "$$tmp" && mv "$$tmp" "$$file"; }; \
	cp tasks/cards/TEMPLATE.md "$$DEST"; \
	rewrite "s/^# Card XXXX/# Card $(ID)/" "$$DEST"; \
	rewrite "s/^Milestone:/Milestone: $(M)/" "$$DEST"; \
	if [ -n "$(R)" ]; then \
	  RECIPE_PATH="specs/change-recipes/$(R).yaml"; \
	  if [ ! -f "$$RECIPE_PATH" ]; then \
	    echo "Error: $$RECIPE_PATH not found."; \
	    echo "Available recipes:"; \
	    ls specs/change-recipes/*.yaml 2>/dev/null | sed 's|specs/change-recipes/||; s|\.yaml$$||'; \
	    rm -f "$$DEST"; \
	    exit 1; \
	  fi; \
	  rewrite "s|^Recipe: .*|Recipe: $$RECIPE_PATH|" "$$DEST"; \
	fi; \
	echo "Created: $$DEST"; \
	echo "Next: fill the card fields, then:"; \
	echo "  make check-card C=active/$(ID)-$(SLUG)"; \
	if [ -z "$(R)" ]; then \
	  echo "Hint: set Recipe: to one of specs/change-recipes/*.yaml or scaffold with R=<recipe-name> next time."; \
	fi

# ── Quality Gates (run locally, mirrors CI) ───────────────────────────────────

gates: ## Run all required quality gates (mirrors CI)
	go run ./internal/checks/dependency-rules
	go run ./internal/checks/cross-extension-deps
	go run ./internal/checks/agent-workflow
	go run ./internal/checks/module-manifests
	go run ./internal/checks/reference-layout
	go run ./internal/checks/extension-maturity
	go run ./internal/checks/extension-beta-evidence
	go run ./internal/checks/deprecation-inventory -strict
	go run ./internal/checks/public-entrypoints-sync
	@set -e; \
	TMP=$$(mktemp -d "$${TMPDIR:-/tmp}/plumego-stable-api.XXXXXX"); \
	trap 'rm -rf "$$TMP"' EXIT; \
	go run ./internal/checks/extension-api-snapshot -module ./core -out "$$TMP/core-head.snapshot"; \
	go run ./internal/checks/extension-api-snapshot -compare docs/evidence/stable-api/snapshots/core-head.snapshot "$$TMP/core-head.snapshot"; \
	go run ./internal/checks/extension-api-snapshot -module ./router -out "$$TMP/router-head.snapshot"; \
	go run ./internal/checks/extension-api-snapshot -compare docs/evidence/stable-api/snapshots/router-head.snapshot "$$TMP/router-head.snapshot"; \
	go run ./internal/checks/extension-api-snapshot -module ./contract/... -out "$$TMP/contract-head.snapshot"; \
	go run ./internal/checks/extension-api-snapshot -compare docs/evidence/stable-api/snapshots/contract-head.snapshot "$$TMP/contract-head.snapshot"; \
	go run ./internal/checks/extension-api-snapshot -module ./middleware/... -out "$$TMP/middleware-head.snapshot"; \
	go run ./internal/checks/extension-api-snapshot -compare docs/evidence/stable-api/snapshots/middleware-head.snapshot "$$TMP/middleware-head.snapshot"; \
	go run ./internal/checks/extension-api-snapshot -module ./security/... -out "$$TMP/security-head.snapshot"; \
	go run ./internal/checks/extension-api-snapshot -compare docs/evidence/stable-api/snapshots/security-head.snapshot "$$TMP/security-head.snapshot"; \
	go run ./internal/checks/extension-api-snapshot -module ./store/... -out "$$TMP/store-head.snapshot"; \
	go run ./internal/checks/extension-api-snapshot -compare docs/evidence/stable-api/snapshots/store-head.snapshot "$$TMP/store-head.snapshot"; \
	go run ./internal/checks/extension-api-snapshot -module ./health/... -out "$$TMP/health-head.snapshot"; \
	go run ./internal/checks/extension-api-snapshot -compare docs/evidence/stable-api/snapshots/health-head.snapshot "$$TMP/health-head.snapshot"; \
	go run ./internal/checks/extension-api-snapshot -module ./log/... -out "$$TMP/log-head.snapshot"; \
	go run ./internal/checks/extension-api-snapshot -compare docs/evidence/stable-api/snapshots/log-head.snapshot "$$TMP/log-head.snapshot"; \
	go run ./internal/checks/extension-api-snapshot -module ./metrics/... -out "$$TMP/metrics-head.snapshot"; \
	go run ./internal/checks/extension-api-snapshot -compare docs/evidence/stable-api/snapshots/metrics-head.snapshot "$$TMP/metrics-head.snapshot"
	go run ./internal/tools/doc-snippets
	go vet ./...
	$(MAKE) reference-vet
	go test -race -timeout 60s ./...
	$(MAKE) reference-test
	go test -coverprofile=/tmp/plumego-stable.cover ./core ./router ./middleware/... ./contract ./security/... ./store/... ./health ./log ./metrics >/tmp/plumego-stable-cover.log
	@TOTAL=$$(go tool cover -func=/tmp/plumego-stable.cover | awk '/^total:/ {gsub("%","",$$3); print $$3}'); \
	echo "Stable-module total coverage: $$TOTAL%"; \
	awk -v total="$$TOTAL" -v min="90.0" 'BEGIN { if (total+0 < min+0) exit 1 }' || { \
	  echo "Coverage gate failed: expected >= 90.0%, got $$TOTAL%"; \
	  tail -n 50 /tmp/plumego-stable-cover.log || true; \
	  exit 1; \
	}
	cd cmd/plumego && go vet ./...
	cd cmd/plumego && go test -race -timeout 60s ./...
	@TMPDIR=$$(mktemp -d); \
	git diff -- website/src/generated > "$$TMPDIR/before"; \
	cd website && pnpm sync; \
	cd ..; \
	git diff -- website/src/generated > "$$TMPDIR/after"; \
	if ! cmp -s "$$TMPDIR/before" "$$TMPDIR/after"; then \
	  echo "website generated files are stale. Run: cd website && pnpm sync"; \
	  exit 1; \
	fi
	@echo "All gates passed. Run 'make website-gates' to validate the docs website build."

website-gates: ## Validate the docs website: content/API checks + static build
	cd website && pnpm check
	cd website && pnpm build
	@echo "Website gates passed."

fmt: ## Format all Go source files in-place
	gofmt -w .

vet: ## Run go vet on all packages
	go vet ./...

lint: ## Run golangci-lint (zero-finding gate; requires golangci-lint v2 on PATH)
	golangci-lint run ./...

reference-vet: ## Run go vet for every standalone reference module
	@set -e; \
	for mod in reference/*/go.mod; do \
	  dir=$$(dirname "$$mod"); \
	  echo "==> $$dir"; \
	  (cd "$$dir" && go vet ./...); \
	done

test: ## Run tests (standard timeout)
	go test -timeout 20s ./...

test-race: ## Run tests with race detector
	go test -race -timeout 60s ./...

reference-test: ## Run tests for every standalone reference module
	@set -e; \
	for mod in reference/*/go.mod; do \
	  dir=$$(dirname "$$mod"); \
	  echo "==> $$dir"; \
	  (cd "$$dir" && go test ./...); \
	done

# ── Git Hooks ─────────────────────────────────────────────────────────────────

website-sync: ## Regenerate website/src/generated/ from docs sources and stage the result
	cd website && pnpm sync
	git add website/src/generated/
	@echo "Generated files staged. Review with 'git diff --cached website/src/generated/' then commit."

setup-hooks: ## Install local git hooks (pre-push quality gates)
	@cp .githooks/pre-push .git/hooks/pre-push
	@chmod +x .git/hooks/pre-push
	@echo "Installed: .git/hooks/pre-push"
	@echo "Quality gates now run automatically before every git push."
	@echo "  milestone/* branches: full gate suite"
	@echo "  other branches:       quick check (vet + fmt + tests)"
	@echo "Skip once with: git push --no-verify"
