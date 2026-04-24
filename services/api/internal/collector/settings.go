package collector

import (
	"strings"

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

func DaemonSettings(configURI string, version string) otelcol.CollectorSettings {
	return settings(configURI, version, true)
}

func settings(configURI string, version string, disableGracefulShutdown bool) otelcol.CollectorSettings {
	ApplyDefaultEnv()

	configURI = strings.TrimSpace(configURI)
	if configURI == "" {
		configURI = "yaml:" + DefaultConfig
	} else if !strings.Contains(configURI, ":") {
		configURI = "file:" + configURI
	}

	return otelcol.CollectorSettings{
		BuildInfo: component.BuildInfo{
			Command:     "telescope",
			Description: "Telescope telemetry runtime",
			Version:     version,
		},
		Factories:               Factories,
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

func New(configURI string, version string) (*otelcol.Collector, error) {
	return otelcol.NewCollector(DaemonSettings(configURI, version))
}
