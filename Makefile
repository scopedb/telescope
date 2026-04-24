OTEL_VERSION ?= v0.150.0
GOTOOLCHAIN ?= go1.25.3
OCB ?= ./bin/builder
IMAGE ?= scopedb-telescope:0.1.0
GATEWAY_COLLECTOR_DIR ?= services/gateway/collector
GATEWAY_DEPLOY_DIR ?= services/gateway/deploy
EXPORTER_DIR ?= packages/scopedbexporter

.PHONY: test
test:
	cd $(EXPORTER_DIR) && GOTOOLCHAIN=$(GOTOOLCHAIN) go test ./...

.PHONY: build-ocb
build-ocb:
	mkdir -p bin
	GOBIN=$(PWD)/bin GOTOOLCHAIN=$(GOTOOLCHAIN) go install go.opentelemetry.io/collector/cmd/builder@$(OTEL_VERSION)

.PHONY: build
build:
	cd $(GATEWAY_COLLECTOR_DIR) && GOWORK=off GOTOOLCHAIN=$(GOTOOLCHAIN) $(abspath $(OCB)) --config builder-config.yaml

.PHONY: validate
validate:
	cd $(GATEWAY_COLLECTOR_DIR) && \
	SCOPEDB_ENDPOINT="$${SCOPEDB_ENDPOINT:?SCOPEDB_ENDPOINT is required}" \
	SCOPEDB_API_KEY="$${SCOPEDB_API_KEY:?SCOPEDB_API_KEY is required}" \
	./_build/telescope-otelcol validate --config config/demo.yaml

.PHONY: validate-deploy
validate-deploy:
	cd $(GATEWAY_COLLECTOR_DIR) && \
	SCOPEDB_ENDPOINT="$${SCOPEDB_ENDPOINT:?SCOPEDB_ENDPOINT is required}" \
	SCOPEDB_API_KEY="$${SCOPEDB_API_KEY:?SCOPEDB_API_KEY is required}" \
	./_build/telescope-otelcol validate --config config/deploy.yaml

.PHONY: docker-build
docker-build:
	docker build -f $(GATEWAY_COLLECTOR_DIR)/Dockerfile -t $(IMAGE) .

.PHONY: demo
demo:
	@echo "Export SCOPEDB_ENDPOINT and SCOPEDB_API_KEY, then run:"
	@echo "  ./services/gateway/collector/_build/telescope-otelcol --config services/gateway/collector/config/demo.yaml"
	@echo "Use telemetrygen to send logs, traces, and metrics to localhost:4317 or localhost:4318."
