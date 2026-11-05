MODULES := pkg services/*

.PHONY: build test tidy isolation-check lint booking-run booking-build-lambda

# Workspace-wide build/test. The repo root is NOT a module (it only holds
# go.work), so `go build ./...` must run per-module; go.work still resolves
# cross-module imports inside each.
build:
	@for m in $(MODULES); do echo "==> go build ($$m)"; (cd $$m && go build ./...); done

test:
	@for m in $(MODULES); do echo "==> go test ($$m)"; (cd $$m && go test ./...); done

tidy:
	@for m in $(MODULES); do echo "==> go mod tidy ($$m)"; (cd $$m && go mod tidy); done

# Prove each module is self-contained without the workspace (what CI enforces).
isolation-check:
	@for m in $(MODULES); do echo "==> GOWORK=off build ($$m)"; (cd $$m && GOWORK=off go build ./...); done

lint:
	@for m in $(MODULES); do echo "==> golangci-lint ($$m)"; (cd $$m && golangci-lint run --timeout 5m); done

# Convenience passthroughs to the booking service's own Makefile.
booking-run:
	$(MAKE) -C services/booking run

booking-build-lambda:
	$(MAKE) -C services/booking build-lambda
