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

GOTOOLCHAIN ?= go1.27.3
HAWKEYE ?= hawkeye
KUBECTL ?= kubectl
DIST_DIR ?= dist
PLATFORMS ?= darwin/arm64 linux/amd64 linux/arm64
TELESCOPE ?= ./bin/telescope
VERSION ?= $(shell git describe --tags --always 2>/dev/null || printf 'development')
# Resolve once so every target in this invocation uses the same build version.
VERSION := $(VERSION)
LD_FLAGS ?= -s -w -X main.version=$(VERSION)

.PHONY: license-check
license-check:
	$(HAWKEYE) check --config licenserc.toml

.PHONY: license-format
license-format:
	$(HAWKEYE) format --config licenserc.toml

.PHONY: fmt
fmt:
	@git ls-files --cached --others --exclude-standard -- '*.go' | while IFS= read -r file; do \
		gofmt -w "$$file"; \
	done

.PHONY: fmt-check
fmt-check:
	@tmp="$$(mktemp)"; \
	git ls-files --cached --others --exclude-standard -- '*.go' | while IFS= read -r file; do \
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
	GOTOOLCHAIN=$(GOTOOLCHAIN) go mod tidy -diff
	GOTOOLCHAIN=$(GOTOOLCHAIN) go mod verify

.PHONY: vet
vet:
	GOTOOLCHAIN=$(GOTOOLCHAIN) go vet -tags=integration ./...

.PHONY: lint
lint:
	GOTOOLCHAIN=$(GOTOOLCHAIN) go tool staticcheck -tags=integration ./...

.PHONY: vuln-check
vuln-check:
	GOTOOLCHAIN=$(GOTOOLCHAIN) go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...

.PHONY: test
test:
	GOTOOLCHAIN=$(GOTOOLCHAIN) go test ./...

.PHONY: test-integration
test-integration:
	@test -n "$$TELESCOPE_SCOPEDB_INTEGRATION_ENDPOINT" && test -n "$$TELESCOPE_SCOPEDB_INTEGRATION_API_KEY" || { echo "Set TELESCOPE_SCOPEDB_INTEGRATION_ENDPOINT and TELESCOPE_SCOPEDB_INTEGRATION_API_KEY" >&2; exit 1; }
	GOTOOLCHAIN=$(GOTOOLCHAIN) go test -race -tags=integration ./... -count=1 -timeout=5m

.PHONY: test-race
test-race:
	GOTOOLCHAIN=$(GOTOOLCHAIN) go test -race ./...

.PHONY: integration-compile
integration-compile:
	GOTOOLCHAIN=$(GOTOOLCHAIN) go test -tags=integration ./... -run '^$$'

.PHONY: check
check: fmt-check tidy-check vet lint vuln-check test-race integration-compile

.PHONY: ci-go
ci-go: check

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
	GOTOOLCHAIN=$(GOTOOLCHAIN) python3 scripts/build-archives.py "$(DIST_DIR)" "$(VERSION)" "$(LD_FLAGS)" $(PLATFORMS)
	$(MAKE) checksums
	python3 scripts/check-archives.py "$(DIST_DIR)" "$(VERSION)"

.PHONY: checksums
checksums:
	@set -eu; \
	cd $(DIST_DIR); \
	shasum -a 256 *.tar.gz > SHA256SUMS

.PHONY: validate
validate: build
	$(abspath $(TELESCOPE)) validate --offline deploy/telescope.example.yaml
	$(abspath $(TELESCOPE)) validate --offline deploy/telescope.v0.2-compatible.yaml
	$(abspath $(TELESCOPE)) preview --offline --sample traces=deploy/samples/traces.otlp.json deploy/telescope.v0.2-compatible.yaml

.PHONY: docker-build
docker-build:
	docker build --build-arg VERSION=$(VERSION) -f Dockerfile -t scopedb-telescope:ci .

.PHONY: docker-smoke
docker-smoke: docker-build
	sh scripts/smoke-container.sh "$(VERSION)"

.PHONY: kubernetes-validate
kubernetes-validate:
	KUBECTL="$(KUBECTL)" sh scripts/check-kubernetes.sh

.PHONY: compose-validate
compose-validate:
	python3 scripts/check-compose.py

.PHONY: ci-runtime
ci-runtime: validate compose-validate kubernetes-validate docker-smoke artifacts
