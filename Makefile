# Copyright 2026 ScopeDB contributors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

OTEL_VERSION ?= v0.150.0
GOTOOLCHAIN ?= go1.25.3
OCB ?= ./bin/builder
HAWKEYE ?= hawkeye
VERSION ?= 0.1.0
IMAGE ?= scopedb-telescope:$(VERSION)
DIST_DIR ?= dist
PLATFORMS ?= darwin/arm64 linux/amd64 linux/arm64
GATEWAY_COLLECTOR_DIR ?= services/gateway/collector
GATEWAY_DEPLOY_DIR ?= services/gateway/deploy
API_DIR ?= services/api
TELESCOPE ?= ./bin/telescope
LD_FLAGS ?= -s -w -X main.version=$(VERSION)

.PHONY: license-check
license-check:
	$(HAWKEYE) check --config licenserc.toml

.PHONY: license-format
license-format:
	$(HAWKEYE) format --config licenserc.toml --fail-if-updated=false

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
	CGO_ENABLED=0 GOTOOLCHAIN=$(GOTOOLCHAIN) go build -trimpath -ldflags "$(LD_FLAGS)" -o $(abspath $(TELESCOPE)) ./$(API_DIR)/cmd/telescope

.PHONY: dist-clean
dist-clean:
	rm -rf $(DIST_DIR)

.PHONY: artifacts
artifacts: dist-clean
	mkdir -p $(DIST_DIR)
	@set -eu; \
	for platform in $(PLATFORMS); do \
		os="$${platform%/*}"; \
		arch="$${platform#*/}"; \
		name="telescope_$(VERSION)_$${os}_$${arch}"; \
		out_dir="$(DIST_DIR)/$${name}"; \
		echo "building artifact $${name}"; \
		mkdir -p "$${out_dir}"; \
		GOOS="$${os}" GOARCH="$${arch}" CGO_ENABLED=0 GOTOOLCHAIN=$(GOTOOLCHAIN) go build -trimpath -ldflags "$(LD_FLAGS)" -o "$${out_dir}/telescope" ./$(API_DIR)/cmd/telescope; \
		cp LICENSE README.md "$${out_dir}/"; \
		tar -C "$(DIST_DIR)" -czf "$(DIST_DIR)/$${name}.tar.gz" "$${name}"; \
		rm -rf "$${out_dir}"; \
	done
	$(MAKE) checksums

.PHONY: checksums
checksums:
	@set -eu; \
	cd $(DIST_DIR); \
	shasum -a 256 *.tar.gz > SHA256SUMS

.PHONY: build-collector
build-collector:
	cd $(GATEWAY_COLLECTOR_DIR) && GOWORK=off GOTOOLCHAIN=$(GOTOOLCHAIN) $(abspath $(OCB)) --config builder-config.yaml

.PHONY: validate
validate: build
	TELESCOPE_SCOPEDB_ENDPOINT="$${TELESCOPE_SCOPEDB_ENDPOINT:?TELESCOPE_SCOPEDB_ENDPOINT is required}" \
	TELESCOPE_SCOPEDB_API_KEY="$${TELESCOPE_SCOPEDB_API_KEY:?TELESCOPE_SCOPEDB_API_KEY is required}" \
	TELESCOPE_ENV="$${TELESCOPE_ENV:-default}" \
	TELESCOPE_OTLP_GRPC_ADDR="$${TELESCOPE_OTLP_GRPC_ADDR:-0.0.0.0:4317}" \
	TELESCOPE_OTLP_HTTP_ADDR="$${TELESCOPE_OTLP_HTTP_ADDR:-0.0.0.0:4318}" \
	TELESCOPE_HEALTH_ADDR="$${TELESCOPE_HEALTH_ADDR:-0.0.0.0:13133}" \
	$(abspath $(TELESCOPE)) collector validate

.PHONY: validate-configs
validate-configs: validate

.PHONY: docker-build
docker-build:
	docker build --build-arg VERSION=$(VERSION) -f $(GATEWAY_COLLECTOR_DIR)/Dockerfile -t $(IMAGE) .

.PHONY: ci-runtime
ci-runtime: validate-configs docker-build artifacts

.PHONY: demo
demo:
	@echo "Export TELESCOPE_SCOPEDB_ENDPOINT and TELESCOPE_SCOPEDB_API_KEY, then run:"
	@echo "  ./bin/telescope daemon"
	@echo "Use telemetrygen to send logs, traces, and metrics to the configured OTLP ports."
