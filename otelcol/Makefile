OTEL_VERSION ?= v0.150.0
GOTOOLCHAIN ?= go1.25.3
OCB ?= ../bin/builder

.PHONY: build
build:
	GOWORK=off GOTOOLCHAIN=$(GOTOOLCHAIN) $(OCB) --config builder-config.yaml

.PHONY: validate
validate:
	VENDOR_DB_ENDPOINT=http://localhost:8080 VENDOR_API_KEY=demo-key ./_build/vendor-otelcol validate --config config/demo.yaml

.PHONY: validate-deploy
validate-deploy:
	SCOPEDB_ENDPOINT=http://localhost:8080 SCOPEDB_API_KEY=demo-key SCOPEDB_TABLE=public.vendor_otel_raw ./_build/vendor-otelcol validate --config config/deploy.yaml
