OTEL_VERSION ?= v0.150.0
GOTOOLCHAIN ?= go1.25.3
OCB ?= ./bin/builder
IMAGE ?= scopedb-otel-gateway:0.1.0

.PHONY: test
test:
	cd exporter/vendordbexporter && GOTOOLCHAIN=$(GOTOOLCHAIN) go test ./...

.PHONY: build-ocb
build-ocb:
	mkdir -p bin
	GOBIN=$(PWD)/bin GOTOOLCHAIN=$(GOTOOLCHAIN) go install go.opentelemetry.io/collector/cmd/builder@$(OTEL_VERSION)

.PHONY: build
build:
	cd otelcol && GOWORK=off GOTOOLCHAIN=$(GOTOOLCHAIN) ../$(OCB) --config builder-config.yaml

.PHONY: validate
validate:
	cd otelcol && VENDOR_DB_ENDPOINT=http://localhost:8080 VENDOR_API_KEY=demo-key ./_build/vendor-otelcol validate --config config/demo.yaml

.PHONY: validate-deploy
validate-deploy:
	cd otelcol && SCOPEDB_ENDPOINT=http://localhost:8080 SCOPEDB_API_KEY=demo-key SCOPEDB_TABLE=public.vendor_otel_raw ./_build/vendor-otelcol validate --config config/deploy.yaml

.PHONY: mockdb
mockdb:
	cd examples/mockdb && GOTOOLCHAIN=$(GOTOOLCHAIN) go run .

.PHONY: docker-build
docker-build:
	docker build -f otelcol/Dockerfile -t $(IMAGE) .

.PHONY: demo
demo:
	@echo "Run mockdb in one terminal with VENDOR_API_KEY=demo-key if needed."
	@echo "Then run the collector with VENDOR_DB_ENDPOINT=http://localhost:8080 and VENDOR_API_KEY=demo-key."
	@echo "Use telemetrygen to send logs, traces, and metrics to localhost:4317 or localhost:4318."
