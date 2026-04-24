package mcpserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

const maxHTTPMessageBytes = 4 << 20

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handleHTTPPost(w, r)
	case http.MethodGet:
		w.Header().Set("Allow", "POST")
		http.Error(w, "server-initiated SSE is not supported", http.StatusMethodNotAllowed)
	case http.MethodDelete:
		w.Header().Set("Allow", "POST")
		http.Error(w, "MCP sessions are stateless", http.StatusMethodNotAllowed)
	default:
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleHTTPPost(w http.ResponseWriter, r *http.Request) {
	if !originAllowed(r.Header.Get("Origin")) {
		writeHTTPRPCError(w, http.StatusForbidden, nil, &rpcError{Code: -32000, Message: "origin not allowed"})
		return
	}
	if !protocolVersionAllowed(r.Header.Get("MCP-Protocol-Version")) {
		writeHTTPRPCError(w, http.StatusBadRequest, nil, &rpcError{Code: -32000, Message: "unsupported MCP protocol version"})
		return
	}
	if !acceptsHTTPResponse(r.Header.Get("Accept")) {
		writeHTTPRPCError(w, http.StatusNotAcceptable, nil, &rpcError{Code: -32000, Message: "Accept must allow application/json"})
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxHTTPMessageBytes))
	if err != nil {
		writeHTTPRPCError(w, http.StatusBadRequest, nil, &rpcError{Code: -32700, Message: "read request body", Data: err.Error()})
		return
	}

	var request rpcRequest
	if err := json.Unmarshal(body, &request); err != nil {
		writeHTTPRPCError(w, http.StatusBadRequest, nil, &rpcError{Code: -32700, Message: "parse error", Data: err.Error()})
		return
	}
	if request.JSONRPC != "2.0" {
		writeHTTPRPCError(w, http.StatusBadRequest, request.ID, &rpcError{Code: -32600, Message: "invalid JSON-RPC version"})
		return
	}
	if strings.TrimSpace(request.Method) == "" {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if len(request.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	response := s.handle(r.Context(), request)
	writeHTTPJSON(w, http.StatusOK, response)
}

func writeHTTPRPCError(w http.ResponseWriter, status int, id json.RawMessage, err *rpcError) {
	writeHTTPJSON(w, status, rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   err,
	})
}

func writeHTTPJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","error":{"code":-32603,"message":"encode response","data":%q}}`, err.Error())
	}
}

func protocolVersionAllowed(raw string) bool {
	version := strings.TrimSpace(raw)
	switch version {
	case "", "2025-06-18", "2025-03-26", "2024-11-05":
		return true
	default:
		return false
	}
}

func acceptsHTTPResponse(raw string) bool {
	accept := strings.TrimSpace(raw)
	if accept == "" || accept == "*/*" {
		return true
	}
	for _, item := range strings.Split(accept, ",") {
		mediaType := strings.TrimSpace(strings.Split(item, ";")[0])
		if mediaType == "*/*" || mediaType == "application/json" {
			return true
		}
	}
	return false
}

func originAllowed(raw string) bool {
	origin := strings.TrimSpace(raw)
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
