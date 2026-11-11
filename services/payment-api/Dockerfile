FROM golang:1.26 AS golang
WORKDIR /app
COPY . .
RUN make build

FROM alpine:3.18.2 AS alpine
RUN apk update && \
    apk add --no-cache ca-certificates=20241121-r1 tzdata=2025b-r0 && \
    update-ca-certificates

FROM alpine:3.18.2
WORKDIR /app
COPY --from=alpine /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=alpine /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=alpine /etc/passwd /etc/passwd
COPY --from=golang /app/bin/rusuh-payment-api /app/rusuh-payment-api

RUN adduser -S appuser && chown -R appuser /app
USER appuser

ENTRYPOINT ["./rusuh-payment-api"]
