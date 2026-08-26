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
	"strings"

	"github.com/scopedb/telescope/packages/scopedbexporter"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/confmap"
	"go.opentelemetry.io/collector/confmap/provider/envprovider"
	"go.opentelemetry.io/collector/confmap/provider/fileprovider"
	"go.opentelemetry.io/collector/confmap/provider/httpprovider"
	"go.opentelemetry.io/collector/confmap/provider/httpsprovider"
	"go.opentelemetry.io/collector/confmap/provider/yamlprovider"
	"go.opentelemetry.io/collector/otelcol"
)

func Settings(configURI string, version string) otelcol.CollectorSettings {
	return settings(configURI, version, false)
}

func settings(configURI string, version string, disableGracefulShutdown bool) otelcol.CollectorSettings {
	applyDefaultEnv()

	configURI = strings.TrimSpace(configURI)
	if configURI == "" {
		configURI = starterConfigURI()
	} else if !strings.Contains(configURI, ":") {
		configURI = "file:" + configURI
	}

	return otelcol.CollectorSettings{
		BuildInfo: component.BuildInfo{
			Command:     "telescope",
			Description: "Telescope telemetry runtime",
			Version:     version,
		},
		Factories:               factories,
		DisableGracefulShutdown: disableGracefulShutdown,
		ConfigProviderSettings: otelcol.ConfigProviderSettings{
			ResolverSettings: confmap.ResolverSettings{
				URIs: []string{configURI},
				ProviderFactories: []confmap.ProviderFactory{
					envprovider.NewFactory(),
					fileprovider.NewFactory(),
					httpprovider.NewFactory(),
					httpsprovider.NewFactory(),
					yamlprovider.NewFactory(),
				},
			},
		},
		ProviderModules: map[string]string{
			envprovider.NewFactory().Create(confmap.ProviderSettings{}).Scheme():   "go.opentelemetry.io/collector/confmap/provider/envprovider v1.56.0",
			fileprovider.NewFactory().Create(confmap.ProviderSettings{}).Scheme():  "go.opentelemetry.io/collector/confmap/provider/fileprovider v1.56.0",
			httpprovider.NewFactory().Create(confmap.ProviderSettings{}).Scheme():  "go.opentelemetry.io/collector/confmap/provider/httpprovider v1.56.0",
			httpsprovider.NewFactory().Create(confmap.ProviderSettings{}).Scheme(): "go.opentelemetry.io/collector/confmap/provider/httpsprovider v1.56.0",
			yamlprovider.NewFactory().Create(confmap.ProviderSettings{}).Scheme():  "go.opentelemetry.io/collector/confmap/provider/yamlprovider v1.56.0",
		},
	}
}

func starterConfigURI() string {
	uri, err := ConfigURIForIngestion(scopedbexporter.StarterIngestionConfig())
	if err != nil {
		panic(fmt.Sprintf("render built-in starter config: %v", err))
	}
	return uri
}

func New(configURI string, version string) (*otelcol.Collector, error) {
	return otelcol.NewCollector(settings(configURI, version, true))
}
