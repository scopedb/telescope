OTEL_VERSION ?= v0.150.0
GOTOOLCHAIN ?= go1.25.3
OCB ?= ./bin/builder
IMAGE ?= scopedb-telescope:0.1.0
GATEWAY_COLLECTOR_DIR ?= services/gateway/collector
GATEWAY_DEPLOY_DIR ?= services/gateway/deploy
API_DIR ?= services/api
TELESCOPE ?= ./bin/telescope

.PHONY: fmt-check
fmt-check:
	@tmp="$$(mktemp)"; \
	git ls-files '*.go' ':!:$(GATEWAY_COLLECTOR_DIR)/_build/**' | while IFS= read -r file; do \
		gofmt -l "$$file"; \
	done > "$$tmp"; \
	if [ -s "$$tmp" ]; then \
		echo "Unformatted Go files:"; \
		cat "$$tmp"; \
		rm -f "$$tmp"; \
		exit 1; \
	fi; \
	rm -f "$$tmp"

.PHONY: tidy-check
tidy-check:
	GOTOOLCHAIN=$(GOTOOLCHAIN) go mod tidy
	git diff --exit-code -- go.mod go.sum

.PHONY: test
test:
	GOTOOLCHAIN=$(GOTOOLCHAIN) go test ./...

.PHONY: ci-go
ci-go: fmt-check tidy-check test

.PHONY: build-ocb
build-ocb:
	mkdir -p bin
	GOBIN=$(PWD)/bin GOTOOLCHAIN=$(GOTOOLCHAIN) go install go.opentelemetry.io/collector/cmd/builder@$(OTEL_VERSION)

.PHONY: build
build:
	mkdir -p bin
	GOTOOLCHAIN=$(GOTOOLCHAIN) go build -o $(abspath $(TELESCOPE)) ./$(API_DIR)/cmd/telescope

.PHONY: build-collector
build-collector:
	cd $(GATEWAY_COLLECTOR_DIR) && GOWORK=off GOTOOLCHAIN=$(GOTOOLCHAIN) $(abspath $(OCB)) --config builder-config.yaml

.PHONY: validate
validate: build
	cd $(GATEWAY_COLLECTOR_DIR) && \
	TELESCOPE_SCOPEDB_ENDPOINT="$${TELESCOPE_SCOPEDB_ENDPOINT:?TELESCOPE_SCOPEDB_ENDPOINT is required}" \
	TELESCOPE_SCOPEDB_API_KEY="$${TELESCOPE_SCOPEDB_API_KEY:?TELESCOPE_SCOPEDB_API_KEY is required}" \
	TELESCOPE_ENV="$${TELESCOPE_ENV:-default}" \
	TELESCOPE_OTLP_GRPC_ADDR="$${TELESCOPE_OTLP_GRPC_ADDR:-0.0.0.0:4317}" \
	TELESCOPE_OTLP_HTTP_ADDR="$${TELESCOPE_OTLP_HTTP_ADDR:-0.0.0.0:4318}" \
	TELESCOPE_HEALTH_ADDR="$${TELESCOPE_HEALTH_ADDR:-0.0.0.0:13133}" \
	$(abspath $(TELESCOPE)) collector validate --config config/demo.yaml

.PHONY: validate-deploy
validate-deploy: build
	cd $(GATEWAY_COLLECTOR_DIR) && \
	TELESCOPE_SCOPEDB_ENDPOINT="$${TELESCOPE_SCOPEDB_ENDPOINT:?TELESCOPE_SCOPEDB_ENDPOINT is required}" \
	TELESCOPE_SCOPEDB_API_KEY="$${TELESCOPE_SCOPEDB_API_KEY:?TELESCOPE_SCOPEDB_API_KEY is required}" \
	TELESCOPE_ENV="$${TELESCOPE_ENV:-default}" \
	TELESCOPE_OTLP_GRPC_ADDR="$${TELESCOPE_OTLP_GRPC_ADDR:-0.0.0.0:4317}" \
	TELESCOPE_OTLP_HTTP_ADDR="$${TELESCOPE_OTLP_HTTP_ADDR:-0.0.0.0:4318}" \
	TELESCOPE_HEALTH_ADDR="$${TELESCOPE_HEALTH_ADDR:-0.0.0.0:13133}" \
	$(abspath $(TELESCOPE)) collector validate --config config/deploy.yaml

.PHONY: validate-configs
validate-configs: validate validate-deploy

.PHONY: docker-build
docker-build:
	docker build -f $(GATEWAY_COLLECTOR_DIR)/Dockerfile -t $(IMAGE) .

.PHONY: ci-runtime
ci-runtime: validate-configs docker-build

.PHONY: demo
demo:
	@echo "Export TELESCOPE_SCOPEDB_ENDPOINT and TELESCOPE_SCOPEDB_API_KEY, then run:"
	@echo "  ./bin/telescope daemon"
	@echo "Use telemetrygen to send logs, traces, and metrics to the configured OTLP ports."
