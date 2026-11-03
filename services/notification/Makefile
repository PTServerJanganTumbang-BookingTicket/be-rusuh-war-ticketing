GOBIN       := $(shell go env GOPATH)/bin
MOCKGEN     := $(GOBIN)/mockgen
MOCK_DIR    := _mock

.PHONY: mock mock-install build build-lambda test unit-test-coverage golint mod-tidy

mock-install:
	go install go.uber.org/mock/mockgen@latest

mock:
	@echo "Generating mocks..."
	$(MOCKGEN) \
		-source=internal/notification/port/inbound/notifier.go \
		-destination=$(MOCK_DIR)/notification/port/inbound/notifier_mock.go \
		-package=mock_inbound
	$(MOCKGEN) \
		-source=internal/notification/port/outbound/sender.go \
		-destination=$(MOCK_DIR)/notification/port/outbound/sender_mock.go \
		-package=mock_outbound
	$(MOCKGEN) \
		-source=internal/notification/port/outbound/dedupe.go \
		-destination=$(MOCK_DIR)/notification/port/outbound/dedupe_mock.go \
		-package=mock_outbound
	@echo "Done."

mod-tidy:
	go mod tidy

# SQS-driven only: there is no HTTP server entrypoint, so no `run` target.
build: build-lambda

# Lambda (Option B): provided.al2023 bootstrap binary for zip-based deploy.
build-lambda:
	@env CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -tags lambda.norpc -o bin/bootstrap ./cmd/lambda

# SAM makefile-builder target (used by template.yaml BuildMethod: makefile).
build-NotificationFunction:
	@env CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -tags lambda.norpc -o $(ARTIFACTS_DIR)/bootstrap ./cmd/lambda

test:
	go test -v ./...

unit-test-coverage:
	go test -v -covermode=count ./... -coverprofile=coverage.cov
	go tool cover -func=coverage.cov

gen-mock-source:
	$(MOCKGEN) -package=${pkg} -destination=$(destination) -source=${source}

golint:
	golangci-lint run --timeout 5m --output.code-climate.path stdout
