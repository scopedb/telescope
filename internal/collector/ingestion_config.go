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

package collector

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/scopedb/telescope/packages/scopedbexporter"
	"go.yaml.in/yaml/v3"
)

func LoadConfig(path string) (scopedbexporter.IngestionConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return scopedbexporter.IngestionConfig{}, fmt.Errorf("read Telescope config %s: %w", path, err)
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var config scopedbexporter.IngestionConfig
	if err := decoder.Decode(&config); err != nil {
		return scopedbexporter.IngestionConfig{}, fmt.Errorf("decode Telescope config %s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return scopedbexporter.IngestionConfig{}, fmt.Errorf("decode Telescope config %s: multiple YAML documents are not supported", path)
		}
		return scopedbexporter.IngestionConfig{}, fmt.Errorf("decode Telescope config %s: %w", path, err)
	}
	if err := config.Validate(); err != nil {
		return scopedbexporter.IngestionConfig{}, fmt.Errorf("validate Telescope config %s: %w", path, err)
	}
	return config, nil
}

func ConfigURI(config scopedbexporter.IngestionConfig) (string, error) {
	if err := config.Validate(); err != nil {
		return "", err
	}

	var rendered map[string]any
	if err := yaml.Unmarshal([]byte(defaultConfig), &rendered); err != nil {
		return "", fmt.Errorf("decode embedded collector config: %w", err)
	}
	scopeDB := rendered["exporters"].(map[string]any)["scopedb"].(map[string]any)
	pipelines := rendered["service"].(map[string]any)["pipelines"].(map[string]any)
	tables := make(map[string]string, 3)
	mappings := make(map[string]scopedbexporter.MappingConfig, 3)
	for _, signal := range []string{"logs", "traces", "metrics"} {
		signalConfig, enabled := config.Signal(signal)
		if !enabled {
			delete(pipelines, signal)
			continue
		}
		tables[signal] = signalConfig.Table
		mappings[signal] = signalConfig.Mapping
	}
	scopeDB["tables"] = tables
	scopeDB["mappings"] = mappings

	data, err := yaml.Marshal(rendered)
	if err != nil {
		return "", fmt.Errorf("encode collector config: %w", err)
	}
	return "yaml:" + strings.TrimSpace(string(data)) + "\n", nil
}
