# Copyright 2026 ScopeDB, Inc.
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

GOTOOLCHAIN ?= go1.25.3
HAWKEYE ?= hawkeye
DIST_DIR ?= dist
PLATFORMS ?= darwin/arm64 linux/amd64 linux/arm64
TELESCOPE ?= ./bin/telescope
LD_FLAGS ?= -s -w

.PHONY: license-check
license-check:
	$(HAWKEYE) check --config licenserc.toml

.PHONY: license-format
license-format:
	$(HAWKEYE) format --config licenserc.toml --fail-if-updated=false

.PHONY: fmt-check
fmt-check:
	@tmp="$$(mktemp)"; \
	git ls-files '*.go' | while IFS= read -r file; do \
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

.PHONY: build
build:
	mkdir -p bin
	CGO_ENABLED=0 GOTOOLCHAIN=$(GOTOOLCHAIN) go build -trimpath -ldflags "$(LD_FLAGS)" -o $(abspath $(TELESCOPE)) ./cmd/telescope

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
		GOOS="$${os}" GOARCH="$${arch}" CGO_ENABLED=0 GOTOOLCHAIN=$(GOTOOLCHAIN) go build -trimpath -ldflags "$(LD_FLAGS)" -o "$${out_dir}/telescope" ./cmd/telescope; \
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

.PHONY: validate
validate: build
	$(abspath $(TELESCOPE)) validate --offline deploy/telescope.example.yaml

.PHONY: docker-build
docker-build:
	docker build -f Dockerfile -t scopedb-telescope:ci .

.PHONY: ci-runtime
ci-runtime: validate docker-build artifacts

.PHONY: demo
demo:
	@echo "Copy deploy/telescope.example.yaml to deploy/telescope.yaml, configure its mappings, then run:"
	@echo "  ./bin/telescope run --env-file deploy/.env deploy/telescope.yaml"
	@echo "Use telemetrygen to send logs, traces, and metrics to the configured OTLP ports."
