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

GOTOOLCHAIN ?= go1.25.13
HAWKEYE ?= hawkeye
KUBECTL ?= kubectl
DIST_DIR ?= dist
PLATFORMS ?= darwin/arm64 linux/amd64 linux/arm64
TELESCOPE ?= ./bin/telescope
VERSION ?= $(shell git describe --tags --always 2>/dev/null || printf 'development')
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
	GOTOOLCHAIN=$(GOTOOLCHAIN) go vet ./...

.PHONY: lint
lint:
	GOTOOLCHAIN=$(GOTOOLCHAIN) go tool staticcheck ./...

.PHONY: vuln-check
vuln-check:
	GOTOOLCHAIN=$(GOTOOLCHAIN) go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...

.PHONY: test
test:
	GOTOOLCHAIN=$(GOTOOLCHAIN) go test ./...

.PHONY: test-integration
test-integration:
	GOTOOLCHAIN=$(GOTOOLCHAIN) go test -tags=integration ./...

.PHONY: test-race
test-race:
	GOTOOLCHAIN=$(GOTOOLCHAIN) go test -race ./...

.PHONY: check
check: fmt-check tidy-check vet lint vuln-check test

.PHONY: ci-go
ci-go: fmt-check tidy-check vet lint vuln-check test-race

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
	docker build --build-arg VERSION=$(VERSION) -f Dockerfile -t scopedb-telescope:ci .

.PHONY: docker-smoke
docker-smoke: docker-build
	@set -eu; \
	test "$$(docker run --rm scopedb-telescope:ci version)" = "$(VERSION)"; \
	cid="$$(docker run -d \
		-p 127.0.0.1::8080 \
		-v "$(abspath deploy/telescope.example.yaml):/etc/telescope/telescope.yaml:ro" \
		-e TELESCOPE_SCOPEDB_ENDPOINT=http://127.0.0.1:1 \
		-e TELESCOPE_SCOPEDB_API_KEY=container-smoke \
		-e TELESCOPE_HTTP_ADDR=0.0.0.0:8080 \
		-e TELESCOPE_OTLP_GRPC_ADDR=0.0.0.0:4317 \
		-e TELESCOPE_OTLP_HTTP_ADDR=0.0.0.0:4318 \
		-e TELESCOPE_QUEUE_DIR=/tmp/telescope-queue \
		scopedb-telescope:ci)"; \
	cleanup() { docker rm --force "$$cid" >/dev/null 2>&1 || true; }; \
	trap cleanup EXIT INT TERM; \
	address="$$(docker port "$$cid" 8080/tcp | head -n 1)"; \
	ready=false; \
	for attempt in $$(seq 1 100); do \
		if curl --fail --silent --show-error "http://$$address/readyz" >/dev/null 2>&1; then \
			ready=true; \
			break; \
		fi; \
		if [ "$$(docker inspect --format '{{.State.Running}}' "$$cid")" != true ]; then \
			break; \
		fi; \
		sleep 0.1; \
	done; \
	if [ "$$ready" != true ]; then \
		docker logs "$$cid"; \
		exit 1; \
	fi; \
	status="$$(curl --fail --silent --show-error "http://$$address/v1/ingestion/status")"; \
	printf '%s' "$$status" | grep --quiet '"version":"$(VERSION)"'; \
	printf '%s' "$$status" | grep --quiet '"config_digest":"sha256:[0-9a-f]\{64\}"'; \
	docker exec "$$cid" telescope status --endpoint http://127.0.0.1:8080 >/dev/null; \
	curl --fail --silent --show-error "http://$$address/metrics" \
		| grep --quiet '^telescope_ingestion_queue_capacity_bytes'

.PHONY: kubernetes-validate
kubernetes-validate:
	@set -eu; \
	rendered="$$(mktemp)"; \
	trap 'rm -f "$$rendered"' EXIT; \
	$(KUBECTL) kustomize deploy/kubernetes/example > "$$rendered"; \
	config_name="$$(sed -n '/^kind: ConfigMap$$/,/^---$$/s/^  name: //p' "$$rendered")"; \
	test -n "$$config_name"; \
	sed -n '/^kind: ConfigMap$$/,/^---$$/p' "$$rendered" | grep --quiet '^  namespace: telescope$$'; \
	test "$$(grep -c "name: $$config_name$$" "$$rendered")" -eq 2

.PHONY: ci-runtime
ci-runtime: validate kubernetes-validate docker-smoke artifacts

.PHONY: demo
demo:
	@echo "Copy deploy/telescope.example.yaml to deploy/telescope.yaml, configure its mappings, then run:"
	@echo "  ./bin/telescope run --env-file deploy/.env deploy/telescope.yaml"
	@echo "Use telemetrygen to send logs, traces, and metrics to the configured OTLP ports."
