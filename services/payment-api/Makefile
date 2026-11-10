# No mockgen targets: every double in this service's tests is hand-written
# (fakeRepo, fakeCredentials, fakeUsecase and friends). A generated mock of a
# port with five methods is longer than the fake that replaces it and says
# nothing about what the test expects.

.PHONY: run build build-lambda test unit-test-coverage golint mod-tidy docker-build

mod-tidy:
	go mod tidy

run:
	go run ./cmd/server

build:
	@env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/rusuh-payment-api ./cmd/server

# Lambda (Option B): provided.al2023 bootstrap binary for zip-based deploy.
build-lambda:
	@env CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -tags lambda.norpc -o bin/bootstrap ./cmd/lambda

# ARTIFACTS_DIR is quoted because SAM sets it to a path under the source
# tree, and a developer checkout can sit anywhere - "Assessment 2026/..." for
# instance. Unquoted, the space splits the -o argument in two and go reports
#   package <second half> is not in std
# which points at the toolchain rather than at the path. CI never sees it:
# /home/runner/work/... has no spaces.
# SAM makefile-builder target, named for the function in template.yaml
# (BuildMethod: makefile). The deployed path does NOT use it: an SCP blocks the
# SAM transform, so CI builds with .github/actions/cfn-deploy against
# template-cfn.yaml. This stays for `sam build` / `sam local`.
build-PaymentApiFunction:
	@env CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -tags lambda.norpc -o "$(ARTIFACTS_DIR)/bootstrap" ./cmd/lambda

test:
	go test -v ./...

unit-test-coverage:
	go test -v -covermode=count ./... -coverprofile=coverage.cov
	go tool cover -func=coverage.cov

docker-build: build
	docker build -f Dockerfile -t rusuh-payment-api:latest .

golint:
	golangci-lint run --timeout 5m --output.code-climate.path stdout
