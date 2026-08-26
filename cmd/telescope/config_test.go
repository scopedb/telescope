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
	"flag"
	"testing"
)

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
