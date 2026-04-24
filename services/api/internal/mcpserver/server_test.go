package mcpserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/your-org/vendor-otel-gateway/services/api/internal/httpapi"
)

func TestServeInitializeAndListTools(t *testing.T) {
	output := runMessages(t,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	)

	responses := decodeResponses(t, output)
	if responses[0]["error"] != nil {
		t.Fatalf("unexpected initialize error: %#v", responses[0])
	}
	result := responses[0]["result"].(map[string]any)
	if result["protocolVersion"] == "" {
		t.Fatalf("missing protocol version: %#v", result)
	}

	toolsResult := responses[1]["result"].(map[string]any)
	tools := toolsResult["tools"].([]any)
	if len(tools) != 5 {
		t.Fatalf("unexpected tools: %#v", tools)
	}
	toolsText := string(mustMarshalJSON(t, toolsResult))
	if !strings.Contains(toolsText, "Valid fields come from the schema tool") {
		t.Fatalf("missing field discovery hint: %s", toolsText)
	}
}

func TestServeCallSearch(t *testing.T) {
	output := runMessages(t, `{
	  "jsonrpc":"2.0",
	  "id":1,
	  "method":"tools/call",
	  "params":{
	    "name":"search",
	    "arguments":{
	      "source":"events_v1",
	      "time_range":{"start":"2026-04-23T00:00:00Z","end":"2026-04-23T01:00:00Z"},
	      "limit":1
	    }
	  }
	}`)

	responses := decodeResponses(t, output)
	result := responses[0]["result"].(map[string]any)
	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, `"row_id": "row-1"`) {
		t.Fatalf("unexpected tool result: %s", text)
	}
}

func TestServeReadGuideResource(t *testing.T) {
	output := runMessages(t, `{
	  "jsonrpc":"2.0",
	  "id":1,
	  "method":"resources/read",
	  "params":{"uri":"scopedb://telemetry/guide.md"}
	}`)

	responses := decodeResponses(t, output)
	result := responses[0]["result"].(map[string]any)
	contents := result["contents"].([]any)
	text := contents[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "guide") {
		t.Fatalf("unexpected resource text: %s", text)
	}
}

func TestServeToolErrorReturnsPureJSONText(t *testing.T) {
	output := runMessagesWithService(t, errorService{}, `{
	  "jsonrpc":"2.0",
	  "id":1,
	  "method":"tools/call",
	  "params":{
	    "name":"search",
	    "arguments":{
	      "source":"events_v1",
	      "time_range":{"start":"2026-04-23T00:00:00Z","end":"2026-04-23T01:00:00Z"},
	      "project":["time_unix_nano"]
	    }
	  }
	}`)

	responses := decodeResponses(t, output)
	result := responses[0]["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("expected tool error: %#v", result)
	}

	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	if !strings.HasPrefix(text, "{") {
		t.Fatalf("tool error should be pure JSON text, got: %s", text)
	}

	var response httpapi.ErrorResponse
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		t.Fatalf("decode tool error json: %v body=%s", err, text)
	}
	if response.Error.Message != "unknown project field: time_unix_nano" {
		t.Fatalf("unexpected error: %#v", response.Error)
	}
}

func TestServeHTTPInitialize(t *testing.T) {
	server := newTestMCPServer(t, fakeService{})
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{
	  "jsonrpc":"2.0",
	  "id":1,
	  "method":"initialize",
	  "params":{"protocolVersion":"2025-06-18"}
	}`))
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("MCP-Protocol-Version", "2025-06-18")
	recorder := httptest.NewRecorder()

	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("unexpected content-type: %s", recorder.Header().Get("Content-Type"))
	}

	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	result := response["result"].(map[string]any)
	if result["protocolVersion"] != "2025-06-18" {
		t.Fatalf("unexpected initialize result: %#v", result)
	}
}

func TestServeHTTPNotificationAccepted(t *testing.T) {
	server := newTestMCPServer(t, fakeService{})
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{
	  "jsonrpc":"2.0",
	  "method":"notifications/initialized"
	}`))
	request.Header.Set("Accept", "application/json, text/event-stream")
	recorder := httptest.NewRecorder()

	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusAccepted {
		t.Fatalf("unexpected status: %d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("expected empty body, got: %s", recorder.Body.String())
	}
}

func TestServeHTTPGetReturnsMethodNotAllowed(t *testing.T) {
	server := newTestMCPServer(t, fakeService{})
	request := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	request.Header.Set("Accept", "text/event-stream")
	recorder := httptest.NewRecorder()

	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("unexpected status: %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestServeHTTPRejectsNonLoopbackOrigin(t *testing.T) {
	server := newTestMCPServer(t, fakeService{})
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{
	  "jsonrpc":"2.0",
	  "id":1,
	  "method":"ping"
	}`))
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Origin", "https://example.com")
	recorder := httptest.NewRecorder()

	server.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("unexpected status: %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func runMessages(t *testing.T, payloads ...string) []byte {
	t.Helper()

	return runMessagesWithService(t, fakeService{}, payloads...)
}

func runMessagesWithService(t *testing.T, service httpapi.TelemetryService, payloads ...string) []byte {
	t.Helper()

	var input bytes.Buffer
	for _, payload := range payloads {
		input.WriteString(frame(payload))
	}

	server, err := New(service, "test-mcp", "test")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	var output bytes.Buffer
	if err := server.Serve(context.Background(), &input, &output); err != nil {
		t.Fatalf("serve: %v", err)
	}
	return output.Bytes()
}

func frame(payload string) string {
	return "Content-Length: " + strconv.Itoa(len([]byte(payload))) + "\r\n\r\n" + payload
}

func mustMarshalJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return data
}

func decodeResponses(t *testing.T, output []byte) []map[string]any {
	t.Helper()

	reader := bytes.NewReader(output)
	buffered := bufio.NewReader(reader)
	responses := make([]map[string]any, 0)
	for reader.Len() > 0 {
		message, err := readMessage(buffered)
		if err != nil {
			t.Fatalf("read response: %v", err)
		}
		var response map[string]any
		if err := json.Unmarshal(message, &response); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		responses = append(responses, response)
	}
	return responses
}

func newTestMCPServer(t *testing.T, service httpapi.TelemetryService) *Server {
	t.Helper()
	server, err := New(service, "test-mcp", "test")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return server
}

type fakeService struct{}

func (fakeService) Health(context.Context) httpapi.HealthResponse {
	return httpapi.HealthResponse{Status: "ok", Service: "test", Version: "test"}
}

func (fakeService) Schema(context.Context) (httpapi.SchemaResponse, error) {
	return httpapi.SchemaResponse{Relations: []httpapi.RelationSchema{{Name: "events_v1", Kind: "event"}}}, nil
}

func (fakeService) SchemaGuide(context.Context) (string, error) {
	return "# guide\n", nil
}

func (fakeService) Search(context.Context, httpapi.SearchRequest) (httpapi.SearchResponse, error) {
	return httpapi.SearchResponse{
		Rows: []map[string]any{{"row_id": "row-1"}},
		Page: httpapi.SearchPage{Limit: 1},
		Meta: httpapi.SearchMeta{AppliedQuery: httpapi.AppliedQuery{Source: "events_v1", Limit: 1}},
	}, nil
}

func (fakeService) Aggregate(context.Context, httpapi.AggregateRequest) (httpapi.AggregateResponse, error) {
	return httpapi.AggregateResponse{
		Rows: []map[string]any{{"count": 1}},
		Meta: httpapi.AggregateMeta{AppliedQuery: httpapi.AppliedQuery{Source: "events_v1", Limit: 1}},
	}, nil
}

type errorService struct {
	fakeService
}

func (errorService) Search(context.Context, httpapi.SearchRequest) (httpapi.SearchResponse, error) {
	return httpapi.SearchResponse{}, &httpapi.ServiceError{
		Status:  400,
		Code:    "bad_request",
		Message: "unknown project field: time_unix_nano",
		Details: map[string]any{
			"section": "project",
			"field":   "time_unix_nano",
		},
	}
}

func (errorService) Aggregate(context.Context, httpapi.AggregateRequest) (httpapi.AggregateResponse, error) {
	return httpapi.AggregateResponse{}, errors.New("unexpected aggregate call")
}
