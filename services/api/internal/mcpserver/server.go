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

package mcpserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/scopedb/telescope/services/api/internal/httpapi"
)

const protocolVersion = "2025-06-18"

type Server struct {
	service httpapi.TelemetryService
	name    string
	version string
}

func New(service httpapi.TelemetryService, name string, version string) (*Server, error) {
	if service == nil {
		return nil, fmt.Errorf("telemetry service is required")
	}
	if strings.TrimSpace(name) == "" {
		name = "telescope"
	}
	return &Server{
		service: service,
		name:    name,
		version: strings.TrimSpace(version),
	}, nil
}

func (s *Server) Serve(ctx context.Context, input io.Reader, output io.Writer) error {
	reader := bufio.NewReader(input)
	for {
		message, err := readMessage(reader)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		var request rpcRequest
		if err := json.Unmarshal(message, &request); err != nil {
			_ = writeMessage(output, rpcResponse{
				JSONRPC: "2.0",
				Error:   &rpcError{Code: -32700, Message: "parse error", Data: err.Error()},
			})
			continue
		}
		if request.ID == nil {
			continue
		}

		response := s.handle(ctx, request)
		if err := writeMessage(output, response); err != nil {
			return err
		}
	}
}

func (s *Server) handle(ctx context.Context, request rpcRequest) rpcResponse {
	result, err := s.dispatch(ctx, request.Method, request.Params)
	if err != nil {
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      request.ID,
			Error:   err,
		}
	}
	return rpcResponse{
		JSONRPC: "2.0",
		ID:      request.ID,
		Result:  result,
	}
}

func (s *Server) dispatch(ctx context.Context, method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		var init initializeRequest
		_ = json.Unmarshal(params, &init)
		version := init.ProtocolVersion
		if strings.TrimSpace(version) == "" {
			version = protocolVersion
		}
		return initializeResult{
			ProtocolVersion: version,
			Capabilities: serverCapabilities{
				Tools:     map[string]any{},
				Resources: map[string]any{},
			},
			ServerInfo: serverInfo{
				Name:    s.name,
				Version: s.version,
			},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return toolsListResult{Tools: toolDefinitions()}, nil
	case "tools/call":
		return s.callTool(ctx, params)
	case "resources/list":
		return resourcesListResult{Resources: resourceDefinitions()}, nil
	case "resources/read":
		return s.readResource(ctx, params)
	default:
		return nil, &rpcError{Code: -32601, Message: "method not found"}
	}
}

func (s *Server) callTool(ctx context.Context, params json.RawMessage) (any, *rpcError) {
	var request toolCallRequest
	if err := json.Unmarshal(params, &request); err != nil {
		return nil, invalidParams(err)
	}

	var result any
	var err error
	switch request.Name {
	case "health":
		result = s.service.Health(ctx)
	case "schema":
		result, err = s.service.Schema(ctx)
	case "schema_guide":
		result, err = s.service.SchemaGuide(ctx)
	case "search":
		var searchRequest httpapi.SearchRequest
		if err := decodeArguments(request.Arguments, &searchRequest); err != nil {
			return nil, invalidParams(err)
		}
		result, err = s.service.Search(ctx, searchRequest)
	case "aggregate":
		var aggregateRequest httpapi.AggregateRequest
		if err := decodeArguments(request.Arguments, &aggregateRequest); err != nil {
			return nil, invalidParams(err)
		}
		result, err = s.service.Aggregate(ctx, aggregateRequest)
	default:
		return nil, &rpcError{Code: -32602, Message: "unknown tool", Data: request.Name}
	}
	if err != nil {
		return toolError(err), nil
	}
	return toolResult(result), nil
}

func (s *Server) readResource(ctx context.Context, params json.RawMessage) (any, *rpcError) {
	var request resourceReadRequest
	if err := json.Unmarshal(params, &request); err != nil {
		return nil, invalidParams(err)
	}

	switch request.URI {
	case "scopedb://telemetry/schema":
		schema, err := s.service.Schema(ctx)
		if err != nil {
			return nil, internalRPCError(err)
		}
		text, err := marshalPretty(schema)
		if err != nil {
			return nil, internalRPCError(err)
		}
		return resourceReadResult{Contents: []resourceContent{{
			URI:      request.URI,
			MimeType: "application/json",
			Text:     text,
		}}}, nil
	case "scopedb://telemetry/guide.md":
		guide, err := s.service.SchemaGuide(ctx)
		if err != nil {
			return nil, internalRPCError(err)
		}
		return resourceReadResult{Contents: []resourceContent{{
			URI:      request.URI,
			MimeType: "text/markdown",
			Text:     guide,
		}}}, nil
	default:
		return nil, &rpcError{Code: -32602, Message: "unknown resource", Data: request.URI}
	}
}

func decodeArguments(arguments json.RawMessage, target any) error {
	if len(arguments) == 0 || bytes.Equal(bytes.TrimSpace(arguments), []byte("null")) {
		arguments = []byte("{}")
	}
	return json.Unmarshal(arguments, target)
}

func toolResult(value any) toolCallResult {
	text, err := marshalPretty(value)
	if err != nil {
		return toolError(err)
	}
	return toolCallResult{
		Content: []toolContent{{
			Type: "text",
			Text: text,
		}},
	}
}

func toolError(err error) toolCallResult {
	if serviceErr, ok := err.(*httpapi.ServiceError); ok {
		return toolResult(httpapi.ErrorResponse{
			Error: httpapi.ErrorBody{
				Code:    serviceErr.Code,
				Message: serviceErr.Message,
				Details: serviceErr.Details,
			},
		}).withError()
	}
	return toolCallResult{
		IsError: true,
		Content: []toolContent{{
			Type: "text",
			Text: err.Error(),
		}},
	}
}

func (r toolCallResult) withError() toolCallResult {
	r.IsError = true
	return r
}

func marshalPretty(value any) (string, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	default:
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
}

func invalidParams(err error) *rpcError {
	return &rpcError{Code: -32602, Message: "invalid params", Data: err.Error()}
}

func internalRPCError(err error) *rpcError {
	return &rpcError{Code: -32603, Message: "internal error", Data: err.Error()}
}

func readMessage(reader *bufio.Reader) ([]byte, error) {
	contentLength := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, fmt.Errorf("parse content length: %w", err)
			}
			contentLength = parsed
		}
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}

	message := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, message); err != nil {
		return nil, err
	}
	return message, nil
}

func writeMessage(writer io.Writer, response rpcResponse) error {
	payload, err := json.Marshal(response)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "Content-Length: %d\r\n\r\n", len(payload)); err != nil {
		return err
	}
	_, err = writer.Write(payload)
	return err
}
