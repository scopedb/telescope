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
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	scopedb "github.com/scopedb/goscopedb"
)

const defaultQueryTimeout = 30 * time.Second

func runQueryCommand(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) error {
	flags := flag.NewFlagSet("query", flag.ContinueOnError)
	flags.SetOutput(stderr)
	bootstrap := addBootstrapFlags(flags)
	filePath := flags.String("file", "", "read one ScopeQL statement from a file; use - for stdin")
	format := flags.String("format", "table", "output format: table, json, or jsonl")
	timeout := flags.Duration("timeout", defaultQueryTimeout, "maximum ScopeDB statement execution time")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: telescope query [options] [scopeql]")
		fmt.Fprintln(stderr, "\nExecute one ScopeQL statement with Telescope's ScopeDB connection.")
		fmt.Fprintln(stderr, "When no statement or --file is given, ScopeQL is read from stdin.")
		fmt.Fprintln(stderr, "\nOptions:")
		flags.PrintDefaults()
		fmt.Fprintln(stderr, "\nExamples:")
		fmt.Fprintln(stderr, `  telescope query "FROM scopedb.otel.traces WHERE trace_id = '<trace-id>' LIMIT 1"`)
		fmt.Fprintln(stderr, `  telescope query --format json "SELECT now() AS current_time"`)
		fmt.Fprintln(stderr, "  telescope query --file query.scopeql")
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *timeout <= 0 {
		return errors.New("--timeout must be greater than zero")
	}
	outputFormat := strings.ToLower(strings.TrimSpace(*format))
	if outputFormat != "table" && outputFormat != "json" && outputFormat != "jsonl" {
		return fmt.Errorf("unsupported query format %q; choose table, json, or jsonl", *format)
	}
	statement, err := readQueryStatement(flags, strings.TrimSpace(*filePath), stdin)
	if err != nil {
		return err
	}
	if err := applyBootstrapFlags(bootstrap); err != nil {
		return err
	}
	endpoint, apiKey := scopeDBCredentials()
	if endpoint == "" {
		return errors.New("ScopeDB endpoint is required; set TELESCOPE_SCOPEDB_ENDPOINT or SCOPEDB_ENDPOINT, or pass --scopedb-endpoint")
	}

	client, err := scopedb.NewClient(scopedb.Config{Endpoint: endpoint, APIKey: apiKey})
	if err != nil {
		return err
	}
	defer client.Close()

	waitCtx, cancelWait := context.WithTimeout(ctx, *timeout+5*time.Second)
	defer cancelWait()
	scopeQLStatement := client.Statement(statement)
	scopeQLStatement.ExecTimeout = timeout.String()
	handle, err := scopeQLStatement.Submit(waitCtx)
	if err != nil {
		return fmt.Errorf("submit ScopeQL: %w", err)
	}
	result, err := handle.Wait(waitCtx)
	if err != nil {
		if waitCtx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			waitCause := err
			if ctxErr := waitCtx.Err(); ctxErr != nil {
				waitCause = ctxErr
			}
			cancelCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			outcome, cancelErr := handle.Cancel(cancelCtx)
			waitErr := fmt.Errorf("statement %s did not complete: %w", handle.ID(), waitCause)
			if cancelErr != nil {
				return errors.Join(waitErr, fmt.Errorf("cancel statement %s: %w", handle.ID(), cancelErr))
			}
			return fmt.Errorf("%w; ScopeDB status is %s", waitErr, outcome.Status)
		}
		return queryExecutionError(handle.ID().String(), err)
	}
	if err := writeQueryResult(stdout, result, outputFormat); err != nil {
		return fmt.Errorf("write ScopeQL result: %w", err)
	}
	return nil
}

func readQueryStatement(flags *flag.FlagSet, filePath string, stdin io.Reader) (string, error) {
	if flags.NArg() > 1 {
		return "", fmt.Errorf("query accepts one ScopeQL argument, got %d", flags.NArg())
	}
	if filePath != "" && flags.NArg() != 0 {
		return "", errors.New("--file and a ScopeQL argument cannot be used together")
	}

	var contents []byte
	var err error
	switch {
	case filePath == "-" || filePath == "" && flags.NArg() == 0:
		contents, err = io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("read ScopeQL from stdin: %w", err)
		}
	case filePath != "":
		contents, err = os.ReadFile(filePath)
		if err != nil {
			return "", fmt.Errorf("read ScopeQL file %s: %w", filePath, err)
		}
	default:
		contents = []byte(flags.Arg(0))
	}
	statement := strings.TrimSpace(string(contents))
	if statement == "" {
		return "", errors.New("ScopeQL statement is empty")
	}
	return statement, nil
}

func queryExecutionError(statementID string, err error) error {
	var scopeDBError *scopedb.Error
	if !errors.As(err, &scopeDBError) || scopeDBError.StatementDetails == nil {
		return fmt.Errorf("statement %s: %w", statementID, err)
	}
	details := scopeDBError.StatementDetails
	message := details.Message
	if message == "" {
		message = scopeDBError.Message
	}
	if len(details.Details) > 0 {
		return fmt.Errorf("statement %s failed [%s]: %s; details=%s", statementID, details.Code, message, details.Details)
	}
	return fmt.Errorf("statement %s failed [%s]: %s", statementID, details.Code, message)
}

func writeQueryResult(w io.Writer, result *scopedb.ResultSet, format string) error {
	rows, err := result.RawRows()
	if err != nil {
		return err
	}
	switch format {
	case "table":
		return writeQueryTable(w, result.Schema, rows)
	case "json", "jsonl":
		objects, err := queryResultObjects(result.Schema, rows)
		if err != nil {
			return err
		}
		encoder := json.NewEncoder(w)
		if format == "json" {
			encoder.SetIndent("", "  ")
			return encoder.Encode(objects)
		}
		for _, object := range objects {
			if err := encoder.Encode(object); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported query format %q", format)
	}
}

func writeQueryTable(w io.Writer, schema scopedb.Schema, rows [][]*string) error {
	table := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if len(schema) > 0 {
		header := make([]string, len(schema))
		for index, field := range schema {
			if field == nil {
				return fmt.Errorf("result schema field %d is nil", index)
			}
			header[index] = queryTableCell(field.Name)
		}
		fmt.Fprintln(table, strings.Join(header, "\t"))
	}
	for rowIndex, row := range rows {
		if len(row) != len(schema) {
			return fmt.Errorf("result row %d has %d cells for %d columns", rowIndex, len(row), len(schema))
		}
		cells := make([]string, len(row))
		for index, cell := range row {
			if cell == nil {
				cells[index] = "NULL"
				continue
			}
			cells[index] = queryTableCell(*cell)
		}
		fmt.Fprintln(table, strings.Join(cells, "\t"))
	}
	if err := table.Flush(); err != nil {
		return err
	}
	label := "rows"
	if len(rows) == 1 {
		label = "row"
	}
	_, err := fmt.Fprintf(w, "(%d %s)\n", len(rows), label)
	return err
}

func queryTableCell(value string) string {
	return strings.NewReplacer("\\", "\\\\", "\t", "\\t", "\r", "\\r", "\n", "\\n").Replace(value)
}

func queryResultObjects(schema scopedb.Schema, rows [][]*string) ([]map[string]any, error) {
	names := make([]string, len(schema))
	seen := make(map[string]struct{}, len(schema))
	for index, field := range schema {
		if field == nil {
			return nil, fmt.Errorf("result schema field %d is nil", index)
		}
		if _, ok := seen[field.Name]; ok {
			return nil, fmt.Errorf("JSON output requires unique columns; alias duplicate %q with AS", field.Name)
		}
		seen[field.Name] = struct{}{}
		names[index] = field.Name
	}

	objects := make([]map[string]any, 0, len(rows))
	for rowIndex, row := range rows {
		if len(row) != len(schema) {
			return nil, fmt.Errorf("result row %d has %d cells for %d columns", rowIndex, len(row), len(schema))
		}
		object := make(map[string]any, len(row))
		for index, cell := range row {
			value, err := queryJSONValue(cell, schema[index].Type)
			if err != nil {
				return nil, fmt.Errorf("row %d column %q: %w", rowIndex, names[index], err)
			}
			object[names[index]] = value
		}
		objects = append(objects, object)
	}
	return objects, nil
}

func queryJSONValue(cell *string, dataType scopedb.DataType) (any, error) {
	if cell == nil {
		return nil, nil
	}
	value := *cell
	switch dataType {
	case scopedb.StringDataType,
		scopedb.BinaryDataType,
		scopedb.TimestampDataType,
		scopedb.IntervalDataType:
		return value, nil
	case scopedb.IntDataType:
		return strconv.ParseInt(value, 10, 64)
	case scopedb.UIntDataType:
		return strconv.ParseUint(value, 10, 64)
	case scopedb.FloatDataType:
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, err
		}
		if math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return nil, nil
		}
		return parsed, nil
	case scopedb.BooleanDataType:
		return strconv.ParseBool(value)
	case scopedb.ArrayDataType, scopedb.ObjectDataType, scopedb.AnyDataType:
		var decoded any
		decoder := json.NewDecoder(strings.NewReader(value))
		decoder.UseNumber()
		if err := decoder.Decode(&decoded); err != nil {
			if dataType == scopedb.AnyDataType {
				return value, nil
			}
			return nil, fmt.Errorf("decode %s value: %w", dataType, err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("decode %s value: trailing JSON", dataType)
		}
		return decoded, nil
	case scopedb.NullDataType:
		return nil, fmt.Errorf("unexpected non-null value for null data type")
	default:
		return nil, fmt.Errorf("unsupported ScopeDB data type %q", dataType)
	}
}
