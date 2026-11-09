GOBIN       := $(shell go env GOPATH)/bin
MOCKGEN     := $(GOBIN)/mockgen
MOCK_DIR    := _mock

.PHONY: mock mock-install run build build-lambda test unit-test-coverage golint mod-tidy docker-build

mock-install:
	go install go.uber.org/mock/mockgen@latest

mock:
	@echo "Generating mocks..."
	$(MOCKGEN) \
		-source=internal/outbox/port/inbound/publisher.go \
		-destination=$(MOCK_DIR)/outbox/port/inbound/publisher_mock.go \
		-package=mock_inbound
	$(MOCKGEN) \
		-source=internal/outbox/port/outbound/outbox.go \
		-destination=$(MOCK_DIR)/outbox/port/outbound/outbox_mock.go \
		-package=mock_outbound
	$(MOCKGEN) \
		-source=internal/outbox/port/outbound/lease.go \
		-destination=$(MOCK_DIR)/outbox/port/outbound/lease_mock.go \
		-package=mock_outbound
	$(MOCKGEN) \
		-source=internal/outbox/port/outbound/publisher.go \
		-destination=$(MOCK_DIR)/outbox/port/outbound/publisher_mock.go \
		-package=mock_outbound
	@echo "Done."

mod-tidy:
	go mod tidy

run:
	go run ./cmd/server

build:
	@env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/rusuh-outbox-publisher ./cmd/server

# Lambda (Option B): provided.al2023 bootstrap binary for zip-based deploy.
build-lambda:
	@env CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -tags lambda.norpc -o bin/bootstrap ./cmd/lambda

# ARTIFACTS_DIR is quoted because SAM sets it to a path under the source
# tree, and a developer checkout can sit anywhere - "Assessment 2026/..." for
# instance. Unquoted, the space splits the -o argument in two and go reports
#   package <second half> is not in std
# which points at the toolchain rather than at the path. CI never sees it:
# /home/runner/work/... has no spaces.
# SAM makefile-builder target (used by template.yaml BuildMethod: makefile).
build-OutboxPublisherFunction:
	@env CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -tags lambda.norpc -o "$(ARTIFACTS_DIR)/bootstrap" ./cmd/lambda

test:
	go test -v ./...

unit-test-coverage:
	go test -v -covermode=count ./... -coverprofile=coverage.cov
	go tool cover -func=coverage.cov

gen-mock-source:
	$(MOCKGEN) -package=${pkg} -destination=$(destination) -source=${source}

docker-build: build
	docker build -f Dockerfile -t rusuh-outbox-publisher:latest .

golint:
	golangci-lint run --timeout 5m --output.code-climate.path stdout
