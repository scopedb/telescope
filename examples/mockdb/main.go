package main

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
)

const (
	defaultListenAddr  = ":8080"
	expectedAPIKey     = "demo-key"
	ingestPath         = "/v1/ingest"
	defaultContentType = "application/json"
)

type ingestRequest struct {
	Type      string            `json:"type"`
	Data      ingestRequestData `json:"data"`
	Statement string            `json:"statement"`
}

type ingestRequestData struct {
	Format string `json:"format"`
	Rows   string `json:"rows"`
}

type receivedIngestRequest struct {
	Type      string           `json:"type"`
	Statement string           `json:"statement"`
	Rows      []map[string]any `json:"rows"`
}

type serverState struct {
	mu          sync.Mutex
	payloads    []receivedIngestRequest
	failCount   int
	forceStatus int
}

func main() {
	state := &serverState{
		failCount:   mustEnvInt("MOCKDB_FAIL_COUNT", 0),
		forceStatus: mustEnvInt("MOCKDB_FORCE_STATUS", 0),
	}
	listenAddr := envOrDefault("MOCKDB_LISTEN_ADDR", defaultListenAddr)

	mux := http.NewServeMux()
	mux.HandleFunc(ingestPath, state.handleIngest)
	mux.HandleFunc("/debug/payloads", state.handlePayloads)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	log.Printf("mockdb listening on %s", listenAddr)
	log.Printf("mockdb accepts Authorization: Bearer <token>")
	if err := http.ListenAndServe(listenAddr, mux); err != nil {
		log.Fatal(err)
	}
}

func (s *serverState) handleIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !isAuthorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if s.forceStatus != 0 {
		http.Error(w, fmt.Sprintf("forced status %d", s.forceStatus), s.forceStatus)
		return
	}

	if s.shouldFail() {
		http.Error(w, "temporary failure", http.StatusInternalServerError)
		return
	}

	body, err := readRequestBody(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var request ingestRequest
	if err := json.Unmarshal(body, &request); err != nil {
		http.Error(w, fmt.Sprintf("invalid json: %v", err), http.StatusBadRequest)
		return
	}

	rows, err := parseRows(request.Data.Rows)
	if err != nil {
		http.Error(w, fmt.Sprintf("invalid rows payload: %v", err), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.payloads = append(s.payloads, receivedIngestRequest{
		Type:      request.Type,
		Statement: request.Statement,
		Rows:      rows,
	})
	s.mu.Unlock()

	signal := ""
	if len(rows) > 0 {
		if value, ok := rows[0]["signal"].(string); ok {
			signal = value
		}
	}

	log.Printf("received signal=%s records=%d mode=%s", signal, len(rows), request.Type)
	w.Header().Set("Content-Type", defaultContentType)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":      true,
		"signal":  signal,
		"records": len(rows),
	})
}

func (s *serverState) handlePayloads(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	w.Header().Set("Content-Type", defaultContentType)
	_ = json.NewEncoder(w).Encode(s.payloads)
}

func (s *serverState) shouldFail() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.failCount <= 0 {
		return false
	}

	s.failCount--
	return true
}

func isAuthorized(r *http.Request) bool {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if auth == "" {
		return false
	}

	if !strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return false
	}

	return strings.TrimSpace(auth[len("Bearer "):]) == expectedAPIKey
}

func readRequestBody(r *http.Request) ([]byte, error) {
	var reader io.Reader = r.Body
	if strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil, fmt.Errorf("create gzip reader: %w", err)
		}
		defer zr.Close()
		reader = zr
	}

	return io.ReadAll(reader)
}

func mustEnvInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		log.Fatalf("invalid %s=%q: %v", name, raw, err)
	}
	return value
}

func envOrDefault(name, fallback string) string {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	return raw
}

func parseRows(raw string) ([]map[string]any, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}

	if strings.HasPrefix(strings.TrimSpace(raw), "[") {
		var rows []map[string]any
		if err := json.Unmarshal([]byte(raw), &rows); err != nil {
			return nil, err
		}
		return rows, nil
	}

	lines := strings.Split(raw, "\n")
	rows := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}

	return rows, nil
}
