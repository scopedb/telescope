FROM golang:1.25-bookworm AS builder

WORKDIR /src
COPY . .

WORKDIR /src/services/gateway/collector
RUN GOTOOLCHAIN=go1.25.3 go install go.opentelemetry.io/collector/cmd/builder@v0.150.0
RUN GOWORK=off /go/bin/builder --config builder-config.yaml

FROM alpine:3.21
RUN apk add --no-cache ca-certificates

COPY --from=builder /src/services/gateway/collector/_build/telescope-otelcol /usr/local/bin/telescope-otelcol
COPY --from=builder /src/services/gateway/collector/config/deploy.yaml /etc/telescope-otelcol/config.yaml

VOLUME ["/var/lib/telescope-otelcol/queue"]

EXPOSE 4317 4318 13133

ENTRYPOINT ["/usr/local/bin/telescope-otelcol", "--config", "/etc/telescope-otelcol/config.yaml"]
