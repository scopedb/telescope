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

package httpclient

import (
	"errors"
	"net/http"

	scopedb "github.com/scopedb/goscopedb"
)

// New returns an owned transport with the standard redirect limit and no HTTPS downgrade.
func New() *http.Client {
	return &http.Client{
		Transport: http.DefaultTransport.(*http.Transport).Clone(),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			for _, previous := range via {
				if previous.URL.Scheme == "https" && req.URL.Scheme != "https" {
					return errors.New("refusing HTTPS downgrade redirect")
				}
			}
			return nil
		},
	}
}

// ScopeDBClient closes both the SDK and the HTTP transport supplied to it.
type ScopeDBClient struct {
	*scopedb.Client
	HTTP *http.Client
}

func NewScopeDB(config scopedb.Config) (*ScopeDBClient, error) {
	transport := New()
	config.HTTPClient = transport
	client, err := scopedb.NewClient(config)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, err
	}
	return &ScopeDBClient{Client: client, HTTP: transport}, nil
}

func (c *ScopeDBClient) Close() {
	c.Client.Close()
	c.HTTP.CloseIdleConnections()
}
