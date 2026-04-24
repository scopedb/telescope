OTEL_VERSION ?= v0.150.0
GOTOOLCHAIN ?= go1.25.3
OCB ?= ../../../bin/builder

.PHONY: build
build:
	GOWORK=off GOTOOLCHAIN=$(GOTOOLCHAIN) $(OCB) --config builder-config.yaml

.PHONY: validate
validate:
	SCOPEDB_ENDPOINT="${SCOPEDB_ENDPOINT:?SCOPEDB_ENDPOINT is required}" \
	SCOPEDB_API_KEY="${SCOPEDB_API_KEY:?SCOPEDB_API_KEY is required}" \
	./_build/telescope-otelcol validate --config config/demo.yaml

.PHONY: validate-deploy
validate-deploy:
	SCOPEDB_ENDPOINT="${SCOPEDB_ENDPOINT:?SCOPEDB_ENDPOINT is required}" \
	SCOPEDB_API_KEY="${SCOPEDB_API_KEY:?SCOPEDB_API_KEY is required}" \
	./_build/telescope-otelcol validate --config config/deploy.yaml
