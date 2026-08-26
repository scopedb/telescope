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
	"github.com/open-telemetry/opentelemetry-collector-contrib/extension/healthcheckextension"
	"github.com/open-telemetry/opentelemetry-collector-contrib/extension/storage/filestorage"
	"github.com/scopedb/telescope/packages/scopedbexporter"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/exporter"
	"go.opentelemetry.io/collector/extension"
	"go.opentelemetry.io/collector/otelcol"
	"go.opentelemetry.io/collector/processor"
	"go.opentelemetry.io/collector/processor/batchprocessor"
	"go.opentelemetry.io/collector/processor/memorylimiterprocessor"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/collector/receiver/otlpreceiver"
	otelconftelemetry "go.opentelemetry.io/collector/service/telemetry/otelconftelemetry"
)

func factories() (otelcol.Factories, error) {
	var err error
	factories := otelcol.Factories{
		Telemetry: otelconftelemetry.NewFactory(),
	}

	factories.Extensions, err = otelcol.MakeFactoryMap[extension.Factory](
		filestorage.NewFactory(),
		healthcheckextension.NewFactory(),
	)
	if err != nil {
		return otelcol.Factories{}, err
	}
	factories.ExtensionModules = map[component.Type]string{
		filestorage.NewFactory().Type():          "github.com/open-telemetry/opentelemetry-collector-contrib/extension/storage/filestorage v0.150.0",
		healthcheckextension.NewFactory().Type(): "github.com/open-telemetry/opentelemetry-collector-contrib/extension/healthcheckextension v0.150.0",
	}

	factories.Receivers, err = otelcol.MakeFactoryMap[receiver.Factory](
		otlpreceiver.NewFactory(),
	)
	if err != nil {
		return otelcol.Factories{}, err
	}
	factories.ReceiverModules = map[component.Type]string{
		otlpreceiver.NewFactory().Type(): "go.opentelemetry.io/collector/receiver/otlpreceiver v0.150.0",
	}

	factories.Exporters, err = otelcol.MakeFactoryMap[exporter.Factory](
		scopedbexporter.NewFactory(),
	)
	if err != nil {
		return otelcol.Factories{}, err
	}
	factories.ExporterModules = map[component.Type]string{
		scopedbexporter.NewFactory().Type(): "github.com/scopedb/telescope/packages/scopedbexporter v0.0.0",
	}

	factories.Processors, err = otelcol.MakeFactoryMap[processor.Factory](
		batchprocessor.NewFactory(),
		memorylimiterprocessor.NewFactory(),
	)
	if err != nil {
		return otelcol.Factories{}, err
	}
	factories.ProcessorModules = map[component.Type]string{
		batchprocessor.NewFactory().Type():         "go.opentelemetry.io/collector/processor/batchprocessor v0.150.0",
		memorylimiterprocessor.NewFactory().Type(): "go.opentelemetry.io/collector/processor/memorylimiterprocessor v0.150.0",
	}

	return factories, nil
}
