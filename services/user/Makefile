GOBIN       := $(shell go env GOPATH)/bin
MOCKGEN     := $(GOBIN)/mockgen
MOCK_DIR    := _mock

.PHONY: mock mock-install run build build-lambda test unit-test-coverage golint mod-tidy docker-build

mock-install:
	go install go.uber.org/mock/mockgen@latest

mock:
	@echo "Generating mocks..."
	$(MOCKGEN) \
		-source=internal/user/port/inbound/user.go \
		-destination=$(MOCK_DIR)/user/port/inbound/user_mock.go \
		-package=mock_inbound
	$(MOCKGEN) \
		-source=internal/user/port/outbound/user.go \
		-destination=$(MOCK_DIR)/user/port/outbound/user_mock.go \
		-package=mock_outbound
	$(MOCKGEN) \
		-source=internal/user/port/outbound/dukcapil.go \
		-destination=$(MOCK_DIR)/user/port/outbound/dukcapil_mock.go \
		-package=mock_outbound
	@echo "Done."

mod-tidy:
	go mod tidy

run:
	go run ./cmd/server

build:
	@env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/rusuh-user ./cmd/server

# Lambda (Option B): provided.al2023 bootstrap binary for zip-based deploy.
build-lambda:
	@env CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -tags lambda.norpc -o bin/bootstrap ./cmd/lambda

# SAM makefile-builder target (used by template.yaml BuildMethod: makefile).
build-UserFunction:
	@env CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -tags lambda.norpc -o $(ARTIFACTS_DIR)/bootstrap ./cmd/lambda

test:
	go test -v ./...

unit-test-coverage:
	go test -v -covermode=count ./... -coverprofile=coverage.cov
	go tool cover -func=coverage.cov

gen-mock-source:
	$(MOCKGEN) -package=${pkg} -destination=$(destination) -source=${source}

docker-build: build
	docker build -f Dockerfile -t rusuh-user:latest .

golint:
	golangci-lint run --timeout 5m --output.code-climate.path stdout
