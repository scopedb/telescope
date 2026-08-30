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

FROM --platform=$BUILDPLATFORM golang:1.25-bookworm AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN GOTOOLCHAIN=go1.25.13 go mod download

COPY . .
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=development
RUN GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} CGO_ENABLED=0 GOTOOLCHAIN=go1.25.13 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/telescope ./cmd/telescope

FROM alpine:3.21
RUN apk add --no-cache ca-certificates

COPY --from=builder /out/telescope /usr/local/bin/telescope

VOLUME ["/var/lib/telescope/queue"]

EXPOSE 4317 4318 8080

ENTRYPOINT ["/usr/local/bin/telescope"]
CMD ["run", "/etc/telescope/telescope.yaml"]
