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
	"encoding/json"
	"flag"
	"strings"
	"testing"

	"github.com/scopedb/telescope/packages/scopedbexporter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInspectCommandReadsStdinAndPrintsCopyableSelectors(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runInspectCommand(
		[]string{"logs", "--sample", "-"},
		bytes.NewReader(deploymentSample(t, "logs")),
		&stdout,
		&stderr,
	)
	require.NoError(t, err)
	assert.Empty(t, stderr.String())
	for _, expected := range []string{
		"sample logs <- - (1 records",
		`resource.attributes["service.name"]`,
		`log.attributes["order.id"]`,
		"inspection result:",
		"next: copy selected sources into signals.logs.mapping",
	} {
		assert.Contains(t, stdout.String(), expected)
	}
	assert.NotContains(t, stdout.String(), "order accepted")
}

func TestInspectCommandPrintsVersionedJSON(t *testing.T) {
	var stdout bytes.Buffer
	err := runInspectCommand(
		[]string{
			"--format", "json",
			"--sample", "../../deploy/samples/traces.otlp.json",
			"traces",
		},
		strings.NewReader(""),
		&stdout,
		&bytes.Buffer{},
	)
	require.NoError(t, err)

	var inspection scopedbexporter.SampleInspection
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &inspection))
	assert.Equal(t, scopedbexporter.SampleInspectionVersion, inspection.Version)
	assert.Equal(t, "traces", inspection.Signal)
	assert.Equal(t, 1, inspection.Records)
	assert.NotEmpty(t, inspection.Fields)
}

func TestPrintSampleInspectionHighlightsPartialAndMixedFields(t *testing.T) {
	var output bytes.Buffer
	err := printSampleInspection(&output, "logs.otlp.json", scopedbexporter.SampleInspection{
		Signal:  "logs",
		Records: 2,
		Fields: []scopedbexporter.SampleFieldInspection{
			{
				Group:         "log",
				Selector:      `log.attributes["attempt"]`,
				ObservedTypes: []string{"int"},
				Populated:     1,
				Total:         2,
			},
			{
				Group:         "log",
				Selector:      `log.body["id"]`,
				ObservedTypes: []string{"int", "string"},
				Populated:     2,
				Total:         2,
			},
		},
	})
	require.NoError(t, err)
	assert.Regexp(t, `50% \(1/2\)\s+PARTIAL`, output.String())
	assert.Regexp(t, `int\|string\s+100% \(2/2\)\s+MIXED`, output.String())
	assert.Contains(t, output.String(), "inspection result: selectors=2, partial=1, mixed=1")
}

func TestInspectCommandRejectsIncompleteInput(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing signal", args: []string{"--sample", "-"}, want: "requires exactly one signal"},
		{name: "unsupported signal", args: []string{"profiles", "--sample", "-"}, want: "unsupported inspect signal"},
		{name: "missing sample", args: []string{"logs"}, want: "requires --sample"},
		{name: "unsupported format", args: []string{"logs", "--sample", "-", "--format", "yaml"}, want: "unsupported inspect format"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runInspectCommand(
				tt.args,
				strings.NewReader(""),
				&bytes.Buffer{},
				&bytes.Buffer{},
			)
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestInspectHelpShowsCapturePipeline(t *testing.T) {
	var stderr bytes.Buffer
	err := runInspectCommand(
		[]string{"--help"},
		strings.NewReader(""),
		&bytes.Buffer{},
		&stderr,
	)
	assert.ErrorIs(t, err, flag.ErrHelp)
	assert.Contains(t, stderr.String(), "Usage: telescope inspect [options] <signal>")
	assert.Contains(t, stderr.String(), "telescope capture --listen-http :4318 traces | telescope inspect traces --sample -")
}
