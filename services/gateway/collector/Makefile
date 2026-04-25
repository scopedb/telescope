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
OCB ?= ../../../bin/builder
TELESCOPE ?= ../../../bin/telescope

.PHONY: build
build:
	cd ../../.. && GOTOOLCHAIN=$(GOTOOLCHAIN) go build -o $(abspath $(TELESCOPE)) ./services/api/cmd/telescope

.PHONY: build-collector
build-collector:
	GOWORK=off GOTOOLCHAIN=$(GOTOOLCHAIN) $(OCB) --config builder-config.yaml

.PHONY: validate
validate: build
	TELESCOPE_SCOPEDB_ENDPOINT="${TELESCOPE_SCOPEDB_ENDPOINT:?TELESCOPE_SCOPEDB_ENDPOINT is required}" \
	TELESCOPE_SCOPEDB_API_KEY="${TELESCOPE_SCOPEDB_API_KEY:?TELESCOPE_SCOPEDB_API_KEY is required}" \
	TELESCOPE_ENV="${TELESCOPE_ENV:-default}" \
	TELESCOPE_OTLP_GRPC_ADDR="${TELESCOPE_OTLP_GRPC_ADDR:-0.0.0.0:4317}" \
	TELESCOPE_OTLP_HTTP_ADDR="${TELESCOPE_OTLP_HTTP_ADDR:-0.0.0.0:4318}" \
	TELESCOPE_HEALTH_ADDR="${TELESCOPE_HEALTH_ADDR:-0.0.0.0:13133}" \
	$(TELESCOPE) collector validate
