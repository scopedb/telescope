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
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"text/tabwriter"
	"time"

	statusapi "github.com/scopedb/telescope/internal/status"
)

func runStatus(args []string) error {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	endpoint := flags.String("endpoint", defaultStatusEndpoint, "Telescope base URL or ingestion status endpoint")
	timeout := flags.Duration("timeout", 5*time.Second, "status request timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("status does not accept positional arguments")
	}
	if *timeout <= 0 {
		return fmt.Errorf("--timeout must be greater than zero")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	status, err := readIngestionStatus(ctx, &http.Client{}, *endpoint)
	if err != nil {
		return err
	}
	writeStatus(os.Stdout, status)
	return nil
}

func writeStatus(w io.Writer, status statusapi.IngestionStatusResponse) {
	fmt.Fprintf(w, "state: %s\n", status.State)
	table := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "SIGNAL\tSTATE\tRECEIVED\tWRITTEN\tQUEUED\tDESTINATION")
	for _, signal := range status.Signals {
		fmt.Fprintf(
			table,
			"%s\t%s\t%d\t%d\t%s\t%s\n",
			signal.Signal,
			signal.State,
			signal.Received,
			signal.Written,
			formatQueue(signal.Queue),
			signal.Table,
		)
	}
	_ = table.Flush()
	if !status.InternalTelemetry.Available && status.InternalTelemetry.Error != "" {
		fmt.Fprintf(w, "internal telemetry: %s\n", status.InternalTelemetry.Error)
	}
	for _, signal := range status.Signals {
		if signal.LastError != "" {
			fmt.Fprintf(w, "%s: %s\n", signal.Signal, signal.LastError)
		}
	}
}

func formatQueue(queue statusapi.IngestionQueueStatus) string {
	if !queue.Enabled {
		return "disabled"
	}
	if !queue.Observed {
		return "unavailable"
	}
	if queue.Unit == "bytes" {
		return fmt.Sprintf("%s/%s", formatBytes(queue.Size), formatBytes(queue.Capacity))
	}
	if queue.Unit == "" {
		return fmt.Sprintf("%d/%d", queue.Size, queue.Capacity)
	}
	return fmt.Sprintf("%d/%d %s", queue.Size, queue.Capacity, queue.Unit)
}

func formatBytes(value int64) string {
	units := [...]string{"B", "KiB", "MiB", "GiB", "TiB"}
	divisor := int64(1)
	unit := 0
	for unit < len(units)-1 && value >= divisor*1024 {
		divisor *= 1024
		unit++
	}
	if value%divisor == 0 {
		return fmt.Sprintf("%d %s", value/divisor, units[unit])
	}
	return fmt.Sprintf("%.1f %s", float64(value)/float64(divisor), units[unit])
}
