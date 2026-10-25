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
	"fmt"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

type RuntimeSetting struct {
	Path   string
	Value  string
	Source string
}

// RuntimeSettings reports resolved values without exposing credentials or mappings.
func (c Config) RuntimeSettings() ([]RuntimeSetting, error) {
	c.Destination.APIKey = "<redacted>"
	headers := make(map[string]string, len(c.Telemetry.OTLP.Headers))
	for key := range c.Telemetry.OTLP.Headers {
		headers[key] = "<redacted>"
	}
	c.Telemetry.OTLP.Headers = headers
	var document yaml.Node
	if err := document.Encode(c); err != nil {
		return nil, err
	}
	var settings []RuntimeSetting
	walkRuntimeSettings(&document, "", func(path, value string) {
		source := c.Sources[path]
		if source == "" {
			source = "default"
		}
		settings = append(settings, RuntimeSetting{Path: path, Value: value, Source: source})
	})
	settings = append(settings, RuntimeSetting{Path: "delivery.queue.total_max_bytes", Value: fmt.Sprint(c.Delivery.Queue.MaxBytes * int64(len(c.EnabledSignals()))), Source: "per-signal capacity × enabled signals"})
	sort.Slice(settings, func(i, j int) bool { return settings[i].Path < settings[j].Path })
	return settings, nil
}

func (c Config) Warnings() []string {
	var warnings []string
	for _, path := range c.overridden {
		warnings = append(warnings, fmt.Sprintf("%s overrides YAML field %s", c.Sources[path], path))
	}
	if c.Delivery.Queue.Type == "memory" && c.Delivery.Queue.MaxBytes > c.memorySoftLimitBytes()/4/int64(len(c.EnabledSignals())) {
		warnings = append(warnings, "combined memory queue capacity exceeds one quarter of the memory soft limit; serialized bytes understate live memory, so verify decoded data, batch, and request overhead under load")
	}
	sort.Strings(warnings)
	return warnings
}

func (c Config) memorySoftLimitBytes() int64 {
	return int64(c.Resources.MemoryLimitMiB-c.Resources.MemorySpikeMiB) << 20
}

func (c *Config) recordReferences(path, value string) {
	var references []string
	for _, match := range environmentReference.FindAllStringSubmatch(value, -1) {
		references = append(references, match[1])
	}
	if len(references) > 0 {
		c.Sources[path] += " with env references " + strings.Join(references, ", ")
	}
}

func walkRuntimeSettings(node *yaml.Node, path string, visit func(string, string)) {
	switch node.Kind {
	case yaml.DocumentNode:
		for _, child := range node.Content {
			walkRuntimeSettings(child, path, visit)
		}
	case yaml.AliasNode:
		walkRuntimeSettings(node.Alias, path, visit)
	case yaml.MappingNode:
		for i := 0; i < len(node.Content); i += 2 {
			key := node.Content[i].Value
			if path == "" && key == "signals" {
				continue
			}
			if key == "<<" {
				walkRuntimeSettings(node.Content[i+1], path, visit)
				continue
			}
			if path != "" {
				key = path + "." + key
			}
			walkRuntimeSettings(node.Content[i+1], key, visit)
		}
	case yaml.ScalarNode:
		visit(path, node.Value)
	}
}
