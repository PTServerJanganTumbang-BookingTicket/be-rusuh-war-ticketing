# Build from the repository root: docker build -f services/admission-ctl/Dockerfile .
# (the module replaces the shared pkg with ../../pkg, so both directories are copied.)
FROM golang:1.26 AS golang
WORKDIR /src
COPY pkg ./pkg
COPY services/admission-ctl ./services/admission-ctl
WORKDIR /src/services/admission-ctl
ENV GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=amd64
RUN go build -trimpath -ldflags="-s -w" -o /out/rusuh-admission-ctl ./cmd/worker

# distroless/static carries CA certificates, time zones and a non-root user, and nothing else.
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=golang /out/rusuh-admission-ctl /app/rusuh-admission-ctl
USER 65532:65532
ENTRYPOINT ["/app/rusuh-admission-ctl"]
