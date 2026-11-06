.PHONY: run build build-lambda test

run:
	go run ./cmd/server

build:
	@env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/rusuh-queue ./cmd/server

# Lambda: provided.al2023 bootstrap binary for zip-based deploy.
build-lambda:
	@env CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -tags lambda.norpc -o bin/bootstrap ./cmd/lambda

# SAM makefile-builder target (template.yaml BuildMethod: makefile).
build-JoinFunction:
	@env CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -tags lambda.norpc -o $(ARTIFACTS_DIR)/bootstrap ./cmd/lambda

test:
	go test ./...
