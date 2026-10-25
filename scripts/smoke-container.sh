#!/bin/sh
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

set -eu
version="${1:?version is required}"
image="${TELESCOPE_IMAGE:-scopedb-telescope:ci}"
test "$(docker run --rm "$image" version)" = "$version"
cid="$(docker run -d \
  -p 127.0.0.1::8080 \
  -v "$PWD/deploy/telescope.example.yaml:/etc/telescope/telescope.yaml:ro" \
  -e TELESCOPE_SCOPEDB_ENDPOINT=http://127.0.0.1:1 \
  -e TELESCOPE_SCOPEDB_API_KEY=container-smoke \
  -e TELESCOPE_HTTP_ADDR=0.0.0.0:8080 \
  -e TELESCOPE_OTLP_GRPC_ADDR=0.0.0.0:4317 \
  -e TELESCOPE_OTLP_HTTP_ADDR=0.0.0.0:4318 \
  -e TELESCOPE_QUEUE_DIR=/tmp/telescope-queue \
  "$image")"
cleanup() { docker rm --force "$cid" >/dev/null 2>&1 || true; }
trap cleanup EXIT INT TERM
address="$(docker port "$cid" 8080/tcp | head -n 1)"
ready=false
for attempt in $(seq 1 100); do
  if curl --fail --silent --show-error "http://$address/readyz" >/dev/null 2>&1; then
    ready=true
    break
  fi
  if [ "$(docker inspect --format '{{.State.Running}}' "$cid")" != true ]; then
    break
  fi
  sleep 0.1
done
if [ "$ready" != true ]; then
  docker logs "$cid"
  exit 1
fi
health=false
for attempt in $(seq 1 600); do
  case "$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{end}}' "$cid")" in
    healthy) health=true; break ;;
    unhealthy) break ;;
  esac
  sleep 0.1
done
if [ "$health" != true ]; then
  echo "container health check did not report healthy"
  docker inspect --format '{{json .State.Health}}' "$cid"
  docker logs "$cid"
  exit 1
fi
status="$(curl --fail --silent --show-error "http://$address/v1/ingestion/status")"
printf '%s' "$status" | grep --fixed-strings --quiet "\"version\":\"$version\""
printf '%s' "$status" | grep --quiet '"config_digest":"sha256:[0-9a-f]\{64\}"'
docker exec "$cid" telescope status --endpoint http://127.0.0.1:8080 >/dev/null
curl --fail --silent --show-error "http://$address/metrics" | grep --quiet '^telescope_ingestion_queue_capacity_bytes'
