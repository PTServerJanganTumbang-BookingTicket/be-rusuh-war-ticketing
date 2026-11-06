# Go modules = every directory with a go.mod (pkg + services/*). Folders such as
# services/queue that only hold features/ so far are skipped until they get a go.mod.
MODULES := $(patsubst %/go.mod,%,$(wildcard pkg/go.mod services/*/go.mod))

# Tool versions pinned to CI (.github/workflows/cicd.yml). Built once into .bin/.
GOLANGCI_VERSION := v2.15.0
GOLANGCI         := .bin/golangci-lint-$(GOLANGCI_VERSION)
SPECCHECK        := tools/speccheck/speccheck
GOVULN_VERSION   := v1.9.0
GOVULNCHECK      := .bin/govulncheck-$(GOVULN_VERSION)

# docker compose host ports (see docker-compose.yml).
REDIS_PORT    ?= 16379
POSTGRES_PORT ?= 15432
export REDIS_PORT POSTGRES_PORT

COVERAGE_MIN ?= 80

# Integration tests and @integration features run only when these are set
# (pkg/specsuite). The targets below that need them export them.
REDIS_ADDR   ?= 127.0.0.1:$(REDIS_PORT)
DATABASE_URL ?= postgres://rusuh:rusuh-local-only@127.0.0.1:$(POSTGRES_PORT)/rusuh?sslmode=disable

.PHONY: build test tidy isolation-check lint booking-run booking-build-lambda \
        fmt-check vet spec step test-short verify-fast verify deps-up deps-down coverage

# Workspace-wide build/test. The repo root is NOT a module (it only holds
# go.work), so `go build ./...` must run per-module; go.work still resolves
# cross-module imports inside each.
build:
	@for m in $(MODULES); do echo "==> go build ($$m)"; (cd $$m && go build ./...) || exit 1; done

test:
	@for m in $(MODULES); do echo "==> go test ($$m)"; (cd $$m && go test ./...) || exit 1; done

tidy:
	@for m in $(MODULES); do echo "==> go mod tidy ($$m)"; (cd $$m && go mod tidy) || exit 1; done

# Prove each module is self-contained without the workspace (what CI enforces).
isolation-check:
	@for m in $(MODULES); do echo "==> GOWORK=off build ($$m)"; (cd $$m && GOWORK=off go build ./...) || exit 1; done

# ---------------------------------------------------------------------------
# Spec-first harness (docs/DEVELOPMENT-FLOW.md §4)
# ---------------------------------------------------------------------------

$(GOLANGCI):
	@mkdir -p .bin
	@echo "==> building golangci-lint $(GOLANGCI_VERSION) into .bin/"
	GOWORK=off GOBIN=$(CURDIR)/.bin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)
	@mv .bin/golangci-lint $(GOLANGCI)

$(GOVULNCHECK):
	@mkdir -p .bin
	@echo "==> building govulncheck $(GOVULN_VERSION) into .bin/"
	GOWORK=off GOBIN=$(CURDIR)/.bin go install golang.org/x/vuln/cmd/govulncheck@$(GOVULN_VERSION)
	@mv .bin/govulncheck $(GOVULNCHECK)

# tools/speccheck is outside go.work, so it builds with GOWORK=off.
$(SPECCHECK): tools/speccheck/main.go tools/speccheck/go.mod
	@echo "==> building speccheck"
	@cd tools/speccheck && GOWORK=off go build -o speccheck .

lint: $(GOLANGCI)
	@for m in $(MODULES); do echo "==> golangci-lint ($$m)"; (cd $$m && $(CURDIR)/$(GOLANGCI) run --timeout 5m) || exit 1; done

# gofmt check only (the PostToolUse hook already rewrites edited files).
fmt-check:
	@echo "==> gofmt -l"
	@out="$$(find . -name '*.go' -not -path './.bin/*' -not -path '*/vendor/*' -print0 | xargs -0 gofmt -l)"; \
	  if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet:
	@for m in $(MODULES); do echo "==> go vet ($$m)"; (cd $$m && go vet ./...) || exit 1; done

# Parse every .feature and lint its tags.
spec: $(SPECCHECK)
	@echo "==> speccheck"
	@$(SPECCHECK) .

# Red → green loop for one build step: gate on @draft / missing suites, then run that
# step's approved features (strict godog suites under go test).
step: export REDIS_ADDR := $(REDIS_ADDR)
step: export DATABASE_URL := $(DATABASE_URL)
step: $(SPECCHECK)
	@test -n "$(STEP)" || { echo "usage: make step STEP=N"; exit 2; }
	@$(SPECCHECK) -step $(STEP) . || exit 2
	@for d in $$($(SPECCHECK) -step $(STEP) -dirs .); do \
	  echo "==> go test ($$d) @step-$(STEP)"; \
	  (cd $$d && GODOG_TAGS='@step-$(STEP) && ~@draft' go test -run '^TestFeatures$$' -count=1 .) || exit 1; \
	done

# Unit level only: integration tests and @integration features skip under -short.
test-short:
	@for m in $(MODULES); do echo "==> go test -short ($$m)"; (cd $$m && go test -short -count=1 ./...) || exit 1; done

# Stop hook (scripts/claude-hooks/stop-verify.sh) and every turn.
verify-fast: fmt-check vet spec test-short lint
	@echo "verify-fast: OK"

deps-up:
	docker compose up -d --wait

deps-down:
	docker compose down -v

coverage:
	@mkdir -p coverage
	@for m in $(MODULES); do n=$$(echo $$m | tr / -); echo "==> coverage ($$m)"; \
	  (cd $$m && go test -count=1 -coverpkg=./... -coverprofile=$(CURDIR)/coverage/$$n.out ./...) || exit 1; done

# End of each build step and CI: everything, on real Redis cluster + Postgres.
# The coverage gate excludes cmd/ and mocks, like sonar.coverage.exclusions.
verify: export REDIS_ADDR := $(REDIS_ADDR)
verify: export DATABASE_URL := $(DATABASE_URL)
verify: verify-fast deps-up isolation-check $(GOVULNCHECK)
	@mkdir -p coverage
	@for m in $(MODULES); do n=$$(echo $$m | tr / -); echo "==> go test -race ($$m)"; \
	  (cd $$m && go test -race -count=1 -coverpkg=./... -coverprofile=$(CURDIR)/coverage/$$n.out ./...) || exit 1; \
	  grep -vE '/(cmd|_mock|mocks?)/' $(CURDIR)/coverage/$$n.out > $(CURDIR)/coverage/$$n.gate.out; \
	  pct=$$(go tool cover -func=$(CURDIR)/coverage/$$n.gate.out | awk '/^total:/{sub("%","",$$3); print $$3}'); \
	  echo "    coverage $$m: $$pct%"; \
	  awk -v p="$$pct" -v min=$(COVERAGE_MIN) 'BEGIN{exit (p+0 < min)}' || { echo "coverage below $(COVERAGE_MIN)% in $$m"; exit 1; }; \
	done
	@for m in $(MODULES); do echo "==> govulncheck ($$m)"; (cd $$m && $(CURDIR)/$(GOVULNCHECK) ./...) || exit 1; done
	@echo "verify: OK"

# Convenience passthroughs to the booking service's own Makefile.
booking-run:
	$(MAKE) -C services/booking run

booking-build-lambda:
	$(MAKE) -C services/booking build-lambda
