/*
 * Copyright 2026 ScopeDB, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package main

import (
	"context"
	"fmt"
	"io"
)

func runCommand(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) error {
	if len(args) == 0 {
		printUsage(stderr)
		return nil
	}

	switch args[0] {
	case "run":
		return runTelescopeCommand(ctx, args[1:], stderr)
	case "validate":
		return runConfigCommand(ctx, "validate", args[1:], stdin, stdout, stderr)
	case "preview":
		return runConfigCommand(ctx, "preview", args[1:], stdin, stdout, stderr)
	case "inspect":
		return runInspectCommand(args[1:], stdin, stdout, stderr)
	case "plan":
		return runPlanCommand(ctx, args[1:], stdin, stdout, stderr)
	case "capture":
		return runCaptureCommand(ctx, args[1:], stdout, stderr)
	case "verify":
		return runVerifyCommand(ctx, args[1:], stdout, stderr)
	case "status":
		return runStatusCommand(ctx, args[1:], stdout, stderr)
	case "query":
		return runQueryCommand(ctx, args[1:], stdin, stdout, stderr)
	case "advanced":
		return runAdvanced(ctx, args[1:], stdout, stderr)
	case "version":
		fmt.Fprintln(stdout, version)
		return nil
	case "help", "-h", "--help":
		printUsage(stderr)
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func supportedSignal(signal string) bool {
	return signal == "logs" || signal == "traces" || signal == "metrics"
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `Telescope

Usage:
  Setup:
    telescope inspect [options] <signal>           Discover mapping selectors in an OTLP sample
    telescope preview [options] [telescope.yaml]   Preview sample projection without appending
    telescope plan [options] [telescope.yaml]      Plan additive ScopeDB table DDL
    telescope validate [options] [telescope.yaml]  Validate config and destination tables
    telescope run [options] [telescope.yaml]       Run the OTLP-to-ScopeDB data plane

  Operations:
    telescope status [options]                     Report local delivery state

  Diagnostics:
    telescope capture [options] <signal>           Capture OTLP samples for mapping preview
    telescope verify [options] [signals...]        Verify synthetic OTLP-to-append delivery
    telescope query [options] [scopeql]            Execute one ScopeQL statement

  Other:
    telescope version                              Print the build version

Connection options:
  --env-file                 Load KEY=VALUE bootstrap config file
  --scopedb-endpoint         ScopeDB physical region endpoint
  --scopedb-api-key          ScopeDB API key

Validate options:
  --offline                  Skip ScopeDB destination checks

Preview options:
  --offline                  Skip ScopeDB destination checks
  --sample signal=path       Preview OTLP JSON or protobuf; repeat per signal
  --strict                   Fail on unobserved, partial, or default-only columns

Inspect options:
  --sample path              Read one OTLP JSON or protobuf sample; use - for stdin
  --format                   Output human or json, default human

Plan options:
  --sample signal=path       Add representative mapping evidence; repeat per signal
  --format                   Output human, json, or scopeql, default human
  --out                      Write ScopeQL while retaining the human plan on stdout

Capture options:
  --endpoint                 Telescope operational HTTP base URL
  --listen-http              Standalone OTLP/HTTP address; no config or ScopeDB required
  --limit                    Maximum records to capture, default 100
  --timeout                  Time to wait for telemetry, default 45s

Query options:
  --file                     Read one ScopeQL statement from a file; use - for stdin
  --format                   Output table, json, or jsonl, default table
  --timeout                  Maximum ScopeDB execution time, default 30s

Run options:
  --http-addr                Operational HTTP listen address, overrides TELESCOPE_HTTP_ADDR

Environment:
  TELESCOPE_SCOPEDB_ENDPOINT   ScopeDB physical region endpoint
  TELESCOPE_SCOPEDB_API_KEY    ScopeDB API key
  SCOPEDB_ENDPOINT             Shared fallback for TELESCOPE_SCOPEDB_ENDPOINT
  SCOPEDB_API_KEY              Shared fallback for TELESCOPE_SCOPEDB_API_KEY
  TELESCOPE_HTTP_ADDR          Operational HTTP listen address, default :8080
  TELESCOPE_OTLP_GRPC_ADDR     OTLP gRPC listen address, default 0.0.0.0:4317
  TELESCOPE_OTLP_HTTP_ADDR     OTLP HTTP listen address, default 0.0.0.0:4318
  TELESCOPE_QUEUE_DIR          Persistent queue directory, default $HOME/.telescope/queue
  TELESCOPE_QUEUE_MAX_BYTES    Logical queued telemetry byte capacity, default 536870912 (512 MiB)

Advanced:
  telescope advanced collector <otelcol command>
`)
}
