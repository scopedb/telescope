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
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	statusapi "github.com/scopedb/telescope/internal/status"
)

func runStatusCommand(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) error {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: telescope status [options]")
		fmt.Fprintln(stderr, "\nReport local receiver, batch, queue, and ScopeDB delivery state.")
		fmt.Fprintln(stderr, "\nOptions:")
		flags.PrintDefaults()
	}
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

	requestCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	status, err := readIngestionStatus(requestCtx, &http.Client{}, *endpoint)
	if err != nil {
		return err
	}
	writeStatus(stdout, status)
	return nil
}

func writeStatus(w io.Writer, status statusapi.IngestionStatusResponse) {
	fmt.Fprintf(w, "state: %s\n", status.State)
	if status.Version != "" {
		fmt.Fprintf(w, "version: %s\n", status.Version)
	}
	if status.ConfigDigest != "" {
		fmt.Fprintf(w, "config digest: %s\n", status.ConfigDigest)
	}
	table := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "SIGNAL\tSTATE\tRECEIVED\tWRITTEN\tDROPPED\tQUEUED\tDESTINATION")
	for _, signal := range status.Signals {
		fmt.Fprintf(
			table,
			"%s\t%s\t%d\t%d\t%d\t%s\t%s\n",
			signal.Signal,
			signal.State,
			signal.Received,
			signal.Written,
			signal.Dropped,
			formatQueue(signal.Queue),
			signal.Table,
		)
	}
	_ = table.Flush()
	if status.QueueStorage.Available {
		fmt.Fprintf(w, "queue storage: %s allocated\n", formatBytes(status.QueueStorage.AllocatedBytes))
	} else if status.QueueStorage.Error != "" {
		fmt.Fprintf(w, "queue storage: unavailable (%s)\n", status.QueueStorage.Error)
	}
	if !status.InternalTelemetry.Available && status.InternalTelemetry.Error != "" {
		fmt.Fprintf(w, "internal telemetry: %s\n", status.InternalTelemetry.Error)
	}
	for _, signal := range status.Signals {
		if reasons := formatInvalidReasons(signal.InvalidItemsByReason); reasons != "" {
			fmt.Fprintf(w, "%s invalid: %s\n", signal.Signal, reasons)
		}
		if unsettled := unsettledRecords(signal); unsettled > 0 {
			fmt.Fprintf(
				w,
				"%s: %d accepted items have no final outcome yet; they may be waiting in Collector batch processing or an in-flight export\n",
				signal.Signal,
				unsettled,
			)
		}
		if signal.LastError != "" {
			fmt.Fprintf(w, "%s: %s\n", signal.Signal, signal.LastError)
		}
	}
}

func formatInvalidReasons(counts map[string]uint64) string {
	reasons := make([]string, 0, len(counts))
	for reason, count := range counts {
		if count > 0 {
			reasons = append(reasons, fmt.Sprintf("%s=%d", reason, count))
		}
	}
	sort.Strings(reasons)
	return strings.Join(reasons, ", ")
}

func unsettledRecords(signal statusapi.IngestionSignalStatus) uint64 {
	final := signal.Written + signal.Dropped
	if !signal.Queue.Observed || signal.Queue.Size > 0 || signal.Received <= final {
		return 0
	}
	return signal.Received - final
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
