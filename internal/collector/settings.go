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
	return newSettings(
		configURI,
		version,
		false,
		scopedbexporter.NewStatusRegistry(),
		scopedbexporter.NewCaptureRegistry(),
	)
}

func newSettings(
	configURI string,
	version string,
	disableGracefulShutdown bool,
	statuses *scopedbexporter.StatusRegistry,
	captures *scopedbexporter.CaptureRegistry,
) otelcol.CollectorSettings {
	applyDefaultEnv()

	configURI = strings.TrimSpace(configURI)
	if configURI != "" && !strings.Contains(configURI, ":") {
		configURI = "file:" + configURI
	}
	var configURIs []string
	if configURI != "" {
		configURIs = []string{configURI}
	}

	envFactory := envprovider.NewFactory()
	fileFactory := fileprovider.NewFactory()
	httpFactory := httpprovider.NewFactory()
	httpsFactory := httpsprovider.NewFactory()
	yamlFactory := yamlprovider.NewFactory()

	return otelcol.CollectorSettings{
		BuildInfo: component.BuildInfo{
			Command:     "telescope",
			Description: "Telescope telemetry runtime",
			Version:     version,
		},
		Factories: func() (otelcol.Factories, error) {
			return factories(statuses, captures)
		},
		DisableGracefulShutdown: disableGracefulShutdown,
		ConfigProviderSettings: otelcol.ConfigProviderSettings{
			ResolverSettings: confmap.ResolverSettings{
				URIs: configURIs,
				ProviderFactories: []confmap.ProviderFactory{
					envFactory,
					fileFactory,
					httpFactory,
					httpsFactory,
					yamlFactory,
				},
			},
		},
		ProviderModules: map[string]string{
			envFactory.Create(confmap.ProviderSettings{}).Scheme():   "go.opentelemetry.io/collector/confmap/provider/envprovider v1.56.0",
			fileFactory.Create(confmap.ProviderSettings{}).Scheme():  "go.opentelemetry.io/collector/confmap/provider/fileprovider v1.56.0",
			httpFactory.Create(confmap.ProviderSettings{}).Scheme():  "go.opentelemetry.io/collector/confmap/provider/httpprovider v1.56.0",
			httpsFactory.Create(confmap.ProviderSettings{}).Scheme(): "go.opentelemetry.io/collector/confmap/provider/httpsprovider v1.56.0",
			yamlFactory.Create(confmap.ProviderSettings{}).Scheme():  "go.opentelemetry.io/collector/confmap/provider/yamlprovider v1.56.0",
		},
	}
}

func New(configURI string, version string) (*otelcol.Collector, error) {
	return otelcol.NewCollector(newSettings(
		configURI,
		version,
		true,
		scopedbexporter.NewStatusRegistry(),
		scopedbexporter.NewCaptureRegistry(),
	))
}

func NewWithRegistries(
	configURI string,
	version string,
	statuses *scopedbexporter.StatusRegistry,
	captures *scopedbexporter.CaptureRegistry,
) (*otelcol.Collector, error) {
	return otelcol.NewCollector(newSettings(configURI, version, true, statuses, captures))
}
