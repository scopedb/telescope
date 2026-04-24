FROM golang:1.25-bookworm AS builder

WORKDIR /src
COPY . .

RUN GOTOOLCHAIN=go1.25.3 go build -o /out/telescope ./services/api/cmd/telescope

FROM alpine:3.21
RUN apk add --no-cache ca-certificates

COPY --from=builder /out/telescope /usr/local/bin/telescope

VOLUME ["/var/lib/telescope/queue"]

EXPOSE 4317 4318 8080 13133

ENTRYPOINT ["/usr/local/bin/telescope", "daemon"]
