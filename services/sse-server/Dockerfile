# Build from the repository root: docker build -f services/sse-server/Dockerfile .
# (the module replaces the shared pkg with ../../pkg, so both directories are copied.)
FROM golang:1.26 AS golang
WORKDIR /src
COPY pkg ./pkg
COPY services/sse-server ./services/sse-server
WORKDIR /src/services/sse-server
ENV GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=amd64
RUN go build -trimpath -ldflags="-s -w" -o /out/rusuh-sse-server ./cmd/server

# distroless/static carries CA certificates, time zones and a non-root user, and nothing else.
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=golang /out/rusuh-sse-server /app/rusuh-sse-server
USER 65532:65532
EXPOSE 8085
ENTRYPOINT ["/app/rusuh-sse-server"]
