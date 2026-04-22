FROM golang:1.25-bookworm AS builder

WORKDIR /src
COPY . .

WORKDIR /src/otelcol
RUN GOTOOLCHAIN=go1.25.3 go install go.opentelemetry.io/collector/cmd/builder@v0.150.0
RUN GOWORK=off /go/bin/builder --config builder-config.yaml

FROM alpine:3.21
RUN apk add --no-cache ca-certificates

COPY --from=builder /src/otelcol/_build/vendor-otelcol /usr/local/bin/vendor-otelcol
COPY --from=builder /src/otelcol/config/deploy.yaml /etc/vendor-otelcol/config.yaml

VOLUME ["/var/lib/vendor-otelcol/queue"]

EXPOSE 4317 4318 13133

ENTRYPOINT ["/usr/local/bin/vendor-otelcol", "--config", "/etc/vendor-otelcol/config.yaml"]
