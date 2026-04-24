package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/scopedb/telescope/services/api/internal/semantic"
)

func TestGetHealth(t *testing.T) {
	server := newTestServer(t, &fakeRunner{}, "test")

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	recorder := httptest.NewRecorder()

	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", recorder.Code)
	}

	var response HealthResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Status != "ok" || response.Service != serviceName || response.Version != "test" {
		t.Fatalf("unexpected health response: %#v", response)
	}
}

func TestGetLLMSText(t *testing.T) {
	server := newTestServer(t, &fakeRunner{}, "test")

	request := httptest.NewRequest(http.MethodGet, "/llms.txt", nil)
	recorder := httptest.NewRecorder()

	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); !strings.Contains(got, "text/markdown") {
		t.Fatalf("unexpected content-type: %s", got)
	}
	body := recorder.Body.String()
	if !strings.HasPrefix(body, "# Telescope\n") {
		t.Fatalf("missing title: %s", body)
	}
	if !strings.Contains(body, "[Semantic schema](/v1/schema)") || !strings.Contains(body, "[MCP](/mcp)") {
		t.Fatalf("missing runtime endpoints: %s", body)
	}
}

func TestGetSchema(t *testing.T) {
	server := newTestServer(t, &fakeRunner{}, "test")

	request := httptest.NewRequest(http.MethodGet, "/v1/schema", nil)
	recorder := httptest.NewRecorder()

	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", recorder.Code, recorder.Body.String())
	}

	var response SchemaResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Relations) != 3 {
		t.Fatalf("unexpected relations: %#v", response.Relations)
	}
	if response.Relations[0].DefaultSort[0].Field != "ts" {
		t.Fatalf("unexpected default sort: %#v", response.Relations[0].DefaultSort)
	}
	if len(response.Relations[1].Advisory.IdentityFields) == 0 || response.Relations[1].Advisory.IdentityFields[0] != "trace_id" {
		t.Fatalf("unexpected advisory: %#v", response.Relations[1].Advisory)
	}

	if response.Relations[0].Fields[0].Name != "ts" {
		t.Fatalf("unexpected first field: %#v", response.Relations[0].Fields[0])
	}
}

func TestGetSchemaGuide(t *testing.T) {
	server := newTestServer(t, &fakeRunner{}, "test")

	request := httptest.NewRequest(http.MethodGet, "/v1/schema/guide.md", nil)
	recorder := httptest.NewRecorder()

	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); !strings.Contains(got, "text/markdown") {
		t.Fatalf("unexpected content-type: %s", got)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "# ScopeDB OTel Schema Guide") {
		t.Fatalf("missing title: %s", body)
	}
	if !strings.Contains(body, "## `executions_v1`") || !strings.Contains(body, "- `trace_id`") {
		t.Fatalf("missing relation advisory: %s", body)
	}
	if !strings.Contains(body, "promoted semantic field names only") || !strings.Contains(body, "`record` field is full-fidelity evidence payload") {
		t.Fatalf("missing query surface guidance: %s", body)
	}
}

func TestPostSearchUsesTimeTopByDefault(t *testing.T) {
	runner := &fakeRunner{
		results: []fakeResult{
			{
				rows: []map[string]any{{"ts": "2026-04-23T00:00:00Z", "row_id": "abcd000000000001"}},
			},
		},
	}
	server := newTestServer(t, runner, "test")

	body := []byte(`{
	  "source": "events_v1",
	  "time_range": {
	    "start": "2026-04-23T00:00:00Z",
	    "end": "2026-04-23T01:00:00Z"
	  }
	}`)

	request := httptest.NewRequest(http.MethodPost, "/v1/search", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(runner.statements) != 1 {
		t.Fatalf("expected 1 query, got %d", len(runner.statements))
	}
	if !strings.Contains(runner.statements[0], "`record_timestamp` >= '2026-04-23T00:00:00Z'::timestamp") {
		t.Fatalf("missing time_range start in statement: %s", runner.statements[0])
	}
	if !strings.Contains(runner.statements[0], "`record_timestamp` < '2026-04-23T01:00:00Z'::timestamp") {
		t.Fatalf("missing time_range end in statement: %s", runner.statements[0])
	}
	if !strings.Contains(runner.statements[0], "ORDER BY `ts` DESC, `row_id` DESC") {
		t.Fatalf("missing default ordering in statement: %s", runner.statements[0])
	}
	if !strings.Contains(runner.statements[0], "LIMIT 101") {
		t.Fatalf("missing default limit in statement: %s", runner.statements[0])
	}

	var response SearchResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Page.HasMore {
		t.Fatalf("unexpected page: %#v", response.Page)
	}
	if len(response.Meta.AppliedQuery.Sort) != 2 || response.Meta.AppliedQuery.Sort[1].Field != "row_id" {
		t.Fatalf("unexpected applied sort: %#v", response.Meta.AppliedQuery.Sort)
	}
	if response.Meta.Debug != nil {
		t.Fatalf("unexpected debug meta: %#v", response.Meta.Debug)
	}
	if response.Meta.AppliedQuery.Source != "events_v1" || response.Meta.AppliedQuery.Limit != 100 {
		t.Fatalf("unexpected applied query: %#v", response.Meta.AppliedQuery)
	}
}

func TestPostSearchReturnsCursorWhenPageIsFull(t *testing.T) {
	runner := &fakeRunner{
		results: []fakeResult{
			{
				rows: []map[string]any{
					{"ts": "2026-04-23T00:00:10Z", "row_id": "abcd000000000001"},
					{"ts": timeMustParse(t, "2026-04-23T00:00:09Z"), "row_id": "abcd000000000002"},
					{"ts": "2026-04-23T00:00:08Z", "row_id": "abcd000000000003"},
				},
			},
		},
	}
	server := newTestServer(t, runner, "test")

	body := []byte(`{
	  "source": "events_v1",
	  "time_range": {
	    "start": "2026-04-23T00:00:00Z",
	    "end": "2026-04-23T01:00:00Z"
	  },
	  "limit": 2
	}`)

	request := httptest.NewRequest(http.MethodPost, "/v1/search", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", recorder.Code, recorder.Body.String())
	}

	var response SearchResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Page.HasMore || response.Page.NextCursor == "" {
		t.Fatalf("unexpected page: %#v", response.Page)
	}
	if len(response.Rows) != 2 {
		t.Fatalf("expected trimmed rows, got %#v", response.Rows)
	}
	if !strings.Contains(runner.statements[0], "LIMIT 3") {
		t.Fatalf("expected lookahead limit in statement: %s", runner.statements[0])
	}
}

func TestPostSearchCustomProjectAddsInternalCursorFields(t *testing.T) {
	runner := &fakeRunner{
		results: []fakeResult{
			{
				rows: []map[string]any{
					{"ts": "2026-04-23T00:00:10Z", "row_id": "abcd000000000001", "service_name": "checkout-demo", "message": "PaymentTimeoutError"},
					{"ts": "2026-04-23T00:00:09Z", "row_id": "abcd000000000002", "service_name": "checkout-demo", "message": "PaymentTimeoutError"},
					{"ts": "2026-04-23T00:00:08Z", "row_id": "abcd000000000003", "service_name": "checkout-demo", "message": "PaymentTimeoutError"},
				},
			},
		},
	}
	server := newTestServer(t, runner, "test")

	body := []byte(`{
	  "source": "events_v1",
	  "time_range": {
	    "start": "2026-04-23T00:00:00Z",
	    "end": "2026-04-23T01:00:00Z"
	  },
	  "filter": {"contains":["message","PaymentTimeoutError"]},
	  "project": ["ts", "service_name", "message"],
	  "limit": 2
	}`)

	request := httptest.NewRequest(http.MethodPost, "/v1/search", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(runner.statements[0], "row_id") {
		t.Fatalf("expected internal row_id projection/order in statement: %s", runner.statements[0])
	}

	var response SearchResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Page.HasMore || response.Page.NextCursor == "" {
		t.Fatalf("expected cursor from internal row_id: %#v", response.Page)
	}
	if len(response.Rows) != 2 {
		t.Fatalf("expected trimmed rows, got %#v", response.Rows)
	}
	if _, ok := response.Rows[0]["row_id"]; ok {
		t.Fatalf("internal row_id leaked into projected response: %#v", response.Rows[0])
	}
	if response.Rows[0]["message"] != "PaymentTimeoutError" {
		t.Fatalf("unexpected projected row: %#v", response.Rows[0])
	}
}

func TestPostAggregate(t *testing.T) {
	runner := &fakeRunner{
		results: []fakeResult{
			{
				rows: []map[string]any{{"operation": "GET /checkout", "count": int64(1)}},
			},
		},
	}
	server := newTestServer(t, runner, "test")

	body := []byte(`{
	  "source": "executions_v1",
	  "time_range": {
	    "start": "2026-04-23T00:00:00Z",
	    "end": "2026-04-23T01:00:00Z"
	  },
	  "filter": {"eq":["service_name","checkout"]},
	  "group_by": [{"field":"operation"}],
	  "measures": [{"op":"count","as":"count"}],
	  "sort": [{"field":"count","direction":"desc"}],
	  "limit": 20
	}`)

	request := httptest.NewRequest(http.MethodPost, "/v1/aggregate", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(runner.statements) != 1 {
		t.Fatalf("expected 1 query, got %d", len(runner.statements))
	}
	if !strings.Contains(runner.statements[0], "FROM `scopedb`.`otel`.`traces`") {
		t.Fatalf("unexpected statement: %s", runner.statements[0])
	}
	if !strings.Contains(runner.statements[0], "count() AS `count`") {
		t.Fatalf("unexpected statement: %s", runner.statements[0])
	}

	var response AggregateResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Rows) != 1 || response.Rows[0]["operation"] != "GET /checkout" {
		t.Fatalf("unexpected rows: %#v", response.Rows)
	}
	if response.Meta.Debug != nil {
		t.Fatalf("unexpected debug meta: %#v", response.Meta.Debug)
	}
	if response.Meta.AppliedQuery.Source != "executions_v1" || len(response.Meta.AppliedQuery.GroupBy) != 1 {
		t.Fatalf("unexpected applied query: %#v", response.Meta.AppliedQuery)
	}
}

func TestPostSearchRejectsUnknownProjectField(t *testing.T) {
	server := newTestServer(t, &fakeRunner{}, "test")

	body := []byte(`{
	  "source": "events_v1",
	  "time_range": {
	    "start": "2026-04-23T00:00:00Z",
	    "end": "2026-04-23T01:00:00Z"
	  },
	  "project": ["time_unix_nano"]
	}`)

	request := httptest.NewRequest(http.MethodPost, "/v1/search", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status: %d body=%s", recorder.Code, recorder.Body.String())
	}

	var response ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Error.Code != "bad_request" || response.Error.Message != "unknown project field: time_unix_nano" {
		t.Fatalf("unexpected error: %#v", response.Error)
	}
	if response.Error.Details["field"] != "time_unix_nano" || response.Error.Details["section"] != "project" {
		t.Fatalf("unexpected details: %#v", response.Error.Details)
	}
	if !strings.Contains(response.Error.Details["hint"].(string), "Arbitrary record paths are not accepted") {
		t.Fatalf("missing field hint: %#v", response.Error.Details)
	}
}

func TestPostSearchRejectsUnknownFilterField(t *testing.T) {
	server := newTestServer(t, &fakeRunner{}, "test")

	body := []byte(`{
	  "source": "events_v1",
	  "time_range": {
	    "start": "2026-04-23T00:00:00Z",
	    "end": "2026-04-23T01:00:00Z"
	  },
	  "filter": {"eq":["event_name","PaymentTimeoutError"]}
	}`)

	request := httptest.NewRequest(http.MethodPost, "/v1/search", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status: %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "unknown filter field: event_name") {
		t.Fatalf("unexpected body: %s", recorder.Body.String())
	}
}

func TestPostAggregateRejectsUnknownGroupByField(t *testing.T) {
	server := newTestServer(t, &fakeRunner{}, "test")

	body := []byte(`{
	  "source": "events_v1",
	  "time_range": {
	    "start": "2026-04-23T00:00:00Z",
	    "end": "2026-04-23T01:00:00Z"
	  },
	  "group_by": [{"field":"event_name"}],
	  "measures": [{"op":"count"}]
	}`)

	request := httptest.NewRequest(http.MethodPost, "/v1/aggregate", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status: %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "unknown group_by field: event_name") {
		t.Fatalf("unexpected body: %s", recorder.Body.String())
	}
}

func TestPostSearchReturnsScopeQLWhenDebugRequested(t *testing.T) {
	runner := &fakeRunner{
		results: []fakeResult{
			{rows: []map[string]any{{"ts": "2026-04-23T00:00:00Z", "row_id": "abcd000000000001"}}},
		},
	}
	server := newTestServer(t, runner, "test")

	body := []byte(`{
	  "source": "events_v1",
	  "time_range": {
	    "start": "2026-04-23T00:00:00Z",
	    "end": "2026-04-23T01:00:00Z"
	  },
	  "filter": {"eq":["service_name","checkout"]},
	  "debug": {"scopeql": true},
	  "limit": 1
	}`)

	request := httptest.NewRequest(http.MethodPost, "/v1/search", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", recorder.Code, recorder.Body.String())
	}

	var response SearchResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Meta.Debug == nil || !strings.Contains(response.Meta.Debug.GeneratedScopeQL, "FROM `scopedb`.`otel`.`logs`") {
		t.Fatalf("missing debug scopeql: %#v", response.Meta.Debug)
	}
	if response.Meta.AppliedQuery.Filter == nil {
		t.Fatalf("missing applied filter: %#v", response.Meta.AppliedQuery)
	}
}

func TestPostSearchUsesCursorForSecondPage(t *testing.T) {
	runner := &fakeRunner{
		results: []fakeResult{
			{
				rows: []map[string]any{
					{"ts": "2026-04-23T00:00:08Z", "row_id": "abcd000000000003"},
				},
			},
		},
	}
	server := newTestServer(t, runner, "test")

	start := timeMustParse(t, "2026-04-23T00:00:00Z")
	end := timeMustParse(t, "2026-04-23T01:00:00Z")
	cursor, err := encodeSearchCursor(searchCursor{
		Source:    "events_v1",
		Start:     &start,
		FrozenEnd: end,
		LastTS:    timeMustParse(t, "2026-04-23T00:00:09Z"),
		LastRowID: "abcd000000000002",
	})
	if err != nil {
		t.Fatalf("encode cursor: %v", err)
	}

	body := []byte(`{
	  "source": "events_v1",
	  "time_range": {
	    "start": "2026-04-23T00:00:00Z",
	    "end": "2026-04-23T01:00:00Z"
	  },
	  "cursor": "` + cursor + `"
	}`)

	request := httptest.NewRequest(http.MethodPost, "/v1/search", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(runner.statements) != 1 {
		t.Fatalf("expected 1 query, got %d", len(runner.statements))
	}
	if !strings.Contains(runner.statements[0], "(`record_timestamp` < '2026-04-23T00:00:09Z'::timestamp) OR ((`record_timestamp` = '2026-04-23T00:00:09Z'::timestamp) AND (`row_id` < 'abcd000000000002'))") {
		t.Fatalf("missing cursor predicate: %s", runner.statements[0])
	}
}

func TestPostSearchRejectsCursorWithCustomSort(t *testing.T) {
	server := newTestServer(t, &fakeRunner{}, "test")

	body := []byte(`{
	  "source": "events_v1",
	  "time_range": {
	    "start": "2026-04-23T00:00:00Z",
	    "end": "2026-04-23T01:00:00Z"
	  },
	  "sort": [{"field":"service_name","direction":"asc"}],
	  "cursor": "next"
	}`)

	request := httptest.NewRequest(http.MethodPost, "/v1/search", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status: %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func newTestServer(t *testing.T, runner QueryRunner, version string) http.Handler {
	t.Helper()

	server, err := New(semantic.Default, runner, version)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return server
}

type fakeResult struct {
	rows []map[string]any
	err  error
}

type fakeRunner struct {
	statements []string
	results    []fakeResult
}

func (r *fakeRunner) Query(_ context.Context, statement string) ([]map[string]any, error) {
	r.statements = append(r.statements, statement)
	if len(r.results) == 0 {
		return nil, nil
	}

	result := r.results[0]
	r.results = r.results[1:]
	return result.rows, result.err
}

func (r *fakeRunner) Close() error {
	return nil
}

func timeMustParse(t *testing.T, raw string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		t.Fatalf("parse time: %v", err)
	}
	return parsed
}
