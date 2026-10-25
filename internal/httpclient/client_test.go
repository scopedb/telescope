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
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	scopedb "github.com/scopedb/goscopedb"
	"github.com/stretchr/testify/require"
)

func TestRedirectDoesNotSendCredentialsOverHTTP(t *testing.T) {
	var calls atomic.Int32
	plaintext := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer plaintext.Close()
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plaintext.URL, http.StatusTemporaryRedirect)
	}))
	defer tlsServer.Close()
	client, err := NewScopeDB(scopedb.Config{Endpoint: tlsServer.URL, APIKey: "test-key"})
	require.NoError(t, err)
	defer client.Close()
	client.HTTP.Transport.(*http.Transport).TLSClientConfig = tlsServer.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	_, err = client.Table("logs").Catalog().Get(t.Context())
	var redirectErr *url.Error
	require.ErrorAs(t, err, &redirectErr)
	require.ErrorContains(t, redirectErr, "refusing HTTPS downgrade redirect")
	require.Zero(t, calls.Load())
}
