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
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scopedb/telescope/packages/scopedbexporter"
)

func TestPrintConfigurationSummaryCountsColumns(t *testing.T) {
	var output bytes.Buffer
	printConfigurationSummary(&output, []scopedbexporter.SignalMappingDescription{{
		Signal: "logs",
		Columns: []scopedbexporter.MappingColumnDescription{
			{Column: "message", OutputType: "string"},
			{Column: "body", OutputType: "dynamic", RuntimeDependent: true},
		},
	}})
	if got, want := output.String(), "configuration: ok (columns=2, statically-typed=1, sample-check=1)\n"; got != want {
		t.Fatalf("printConfigurationSummary() = %q, want %q", got, want)
	}
}

func TestTelescopeConfigPath(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    string
		wantErr bool
	}{
		{name: "default", want: defaultTelescopeConfigPath},
		{name: "explicit", args: []string{"custom.yaml"}, want: "custom.yaml"},
		{name: "too many", args: []string{"one.yaml", "two.yaml"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flags := flag.NewFlagSet("test", flag.ContinueOnError)
			if err := flags.Parse(tt.args); err != nil {
				t.Fatalf("parse flags: %v", err)
			}
			got, err := telescopeConfigPath(flags)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("telescopeConfigPath() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("telescopeConfigPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPreviewUsesStdinSampleWithoutWriting(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "telescope.yaml")
	if err := os.WriteFile(configPath, []byte(`
signals:
  logs:
    table: app.logs
    mapping:
      message: log.message
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	sample := `{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"stringValue":"hello"}}]}]}]}`
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := runConfigCommand(
		"preview",
		[]string{"--offline", "--sample", "logs=-", configPath},
		strings.NewReader(sample),
		&stdout,
		&stderr,
	)
	if err != nil {
		t.Fatalf("runConfigCommand() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "sample logs <- - (1 records)") {
		t.Fatalf("preview output missing sample: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `{"message":"hello"}`) {
		t.Fatalf("preview output missing projected row: %s", stdout.String())
	}
}

func TestValidateRejectsSampleFlag(t *testing.T) {
	err := runConfigCommand(
		"validate",
		[]string{"--sample", "logs=sample.json"},
		strings.NewReader(""),
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPreviewRequiresSample(t *testing.T) {
	err := runConfigCommand(
		"preview",
		[]string{"--offline"},
		strings.NewReader(""),
		&bytes.Buffer{},
		&bytes.Buffer{},
	)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "preview requires at least one --sample") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPreviewStrictFailsIncompleteSample(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "telescope.yaml")
	if err := os.WriteFile(configPath, []byte(`
signals:
  logs:
    table: app.logs
    mapping:
      message: log.message
      tenant: resource.attributes["tenant"]
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	sample := `{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"body":{"stringValue":"hello"}}]}]}]}`
	var stdout bytes.Buffer

	err := runConfigCommand(
		"preview",
		[]string{"--offline", "--strict", "--sample", "logs=-", configPath},
		strings.NewReader(sample),
		&stdout,
		&bytes.Buffer{},
	)
	if err == nil {
		t.Fatal("expected strict preview error")
	}
	if !strings.Contains(err.Error(), "warnings under --strict") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "WARN unobserved") {
		t.Fatalf("preview output missing warning: %s", stdout.String())
	}
}

func TestPreviewHelpShowsPositionalConfigAndCapturePipeline(t *testing.T) {
	var stderr bytes.Buffer
	err := runConfigCommand(
		"preview",
		[]string{"--help"},
		strings.NewReader(""),
		&bytes.Buffer{},
		&stderr,
	)
	if err != flag.ErrHelp {
		t.Fatalf("runConfigCommand() error = %v, want %v", err, flag.ErrHelp)
	}
	for _, expected := range []string{
		"Usage: telescope preview [options] [telescope.yaml]",
		"-strict",
		"telescope capture logs | telescope preview",
	} {
		if !strings.Contains(stderr.String(), expected) {
			t.Fatalf("preview help missing %q: %s", expected, stderr.String())
		}
	}
}
