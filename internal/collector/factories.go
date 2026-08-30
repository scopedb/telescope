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

func factories(statuses *scopedbexporter.StatusRegistry, captures *scopedbexporter.CaptureRegistry) (otelcol.Factories, error) {
	var err error
	result := otelcol.Factories{
		Telemetry: otelconftelemetry.NewFactory(),
	}

	fileStorageFactory := filestorage.NewFactory()
	result.Extensions, err = otelcol.MakeFactoryMap[extension.Factory](
		fileStorageFactory,
	)
	if err != nil {
		return otelcol.Factories{}, err
	}
	result.ExtensionModules = map[component.Type]string{
		fileStorageFactory.Type(): "github.com/open-telemetry/opentelemetry-collector-contrib/extension/storage/filestorage v0.150.0",
	}

	otlpFactory := otlpreceiver.NewFactory()
	result.Receivers, err = otelcol.MakeFactoryMap[receiver.Factory](
		otlpFactory,
	)
	if err != nil {
		return otelcol.Factories{}, err
	}
	result.ReceiverModules = map[component.Type]string{
		otlpFactory.Type(): "go.opentelemetry.io/collector/receiver/otlpreceiver v0.150.0",
	}

	scopeDBFactory := scopedbexporter.NewFactoryWithRegistries(statuses, captures)
	result.Exporters, err = otelcol.MakeFactoryMap[exporter.Factory](
		scopeDBFactory,
	)
	if err != nil {
		return otelcol.Factories{}, err
	}
	result.ExporterModules = map[component.Type]string{
		scopeDBFactory.Type(): "github.com/scopedb/telescope/packages/scopedbexporter v0.0.0",
	}

	batchFactory := batchprocessor.NewFactory()
	memoryLimiterFactory := memorylimiterprocessor.NewFactory()
	result.Processors, err = otelcol.MakeFactoryMap[processor.Factory](
		batchFactory,
		memoryLimiterFactory,
	)
	if err != nil {
		return otelcol.Factories{}, err
	}
	result.ProcessorModules = map[component.Type]string{
		batchFactory.Type():         "go.opentelemetry.io/collector/processor/batchprocessor v0.150.0",
		memoryLimiterFactory.Type(): "go.opentelemetry.io/collector/processor/memorylimiterprocessor v0.150.0",
	}

	return result, nil
}
