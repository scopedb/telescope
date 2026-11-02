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

package scopedbexporter

import (
	scopedb "github.com/scopedb/goscopedb"
	"github.com/scopedb/telescope/internal/httpclient"
)

const (
	maxAppendRequestBytes = 8 * 1024 * 1024
	maxAppendRequestRows  = 200_000
)

type appendClient struct {
	*httpclient.ScopeDBClient
	plans map[string]*mappingPlan
	batch AppendBatchConfig
}

func newClient(cfg *Config) (*appendClient, error) {
	compiled, err := cfg.ingestionConfig().compile()
	if err != nil {
		return nil, err
	}
	sdkClient, err := httpclient.NewScopeDB(scopedb.Config{
		Endpoint:     cfg.Endpoint,
		APIKey:       string(cfg.APIKey),
		Compression:  sdkCompression(cfg.compressionMode()),
		WriteTimeout: new(cfg.Timeout.Timeout),
	})
	if err != nil {
		return nil, err
	}

	plans := make(map[string]*mappingPlan, len(compiled))
	for _, plan := range compiled {
		plans[plan.signal] = plan
	}

	return &appendClient{
		ScopeDBClient: sdkClient,
		plans:         plans,
		batch:         cfg.AppendBatch,
	}, nil
}

func sdkCompression(mode string) scopedb.Compression {
	switch mode {
	case "gzip":
		return scopedb.CompressionGzip
	default:
		return scopedb.CompressionZstd
	}
}
