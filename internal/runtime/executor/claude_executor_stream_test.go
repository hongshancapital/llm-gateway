package executor

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
)

// TestClaudeExecutorStream_PassthroughFiltersNonStandardSSE verifies that the
// ClaudeExecutor drops non-standard SSE lines (event:, : keep-alive,
// data: with non-JSON payload) in the from==to passthrough path.
func TestClaudeExecutorStream_PassthroughFiltersNonStandardSSE(t *testing.T) {
	upstreamLines := []string{
		": keep-alive",
		// Orphan event: line followed by : keep-alive instead of data: (litellm proxy bug).
		// Must be dropped per WHATWG SSE spec — no data field means no dispatch.
		"event: content_block_delta",
		": keep-alive",
		// Normal event+data pair must still pass through correctly.
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
		": ping",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" there"}}`,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, line := range upstreamLines {
			_, _ = fmt.Fprintf(w, "%s\n", line)
		}
		_, _ = w.Write([]byte("\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"base_url": server.URL,
		"api_key":  "test",
	}}
	req := cliproxyexecutor.Request{
		Model:   "claude-3-5-sonnet-20241022",
		Payload: []byte(`{"messages":[{"role":"user","content":"hi"}]}`),
	}
	opts := cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
		Stream:       true,
	}

	result, err := executor.ExecuteStream(context.Background(), auth, req, opts)
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil StreamResult")
	}

	var gotPayloads []string
	var streamErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		for chunk := range result.Chunks {
			if chunk.Err != nil {
				streamErr = chunk.Err
				return
			}
			if len(chunk.Payload) > 0 {
				gotPayloads = append(gotPayloads, string(chunk.Payload))
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for stream")
	}

	if streamErr != nil {
		t.Fatalf("unexpected stream error: %v", streamErr)
	}

	// Expect at least two valid JSON data lines forwarded.
	// Empty line payloads (SSE event delimiters) may also be present.
	var dataPayloads []string
	for _, p := range gotPayloads {
		if strings.HasPrefix(p, "data:") {
			dataPayloads = append(dataPayloads, p)
		}
	}
	if len(dataPayloads) != 2 {
		t.Fatalf("got %d data payloads, want 2; payloads: %v", len(dataPayloads), dataPayloads)
	}

	for i, wantPrefix := range []string{
		`data: {"type":"content_block_delta"`,
		`data: {"type":"content_block_delta"`,
	} {
		if !bytes.HasPrefix([]byte(dataPayloads[i]), []byte(wantPrefix)) {
			t.Fatalf("payload[%d] = %q, want prefix %q", i, dataPayloads[i], wantPrefix)
		}
	}

	// Verify none of the non-standard lines leaked through.
	for _, p := range gotPayloads {
		if strings.Contains(p, "keep-alive") {
			t.Fatalf("non-standard SSE leaked into downstream: %q", p)
		}
	}

	// Verify that only 2 event: lines were forwarded (the orphan was dropped).
	var eventPayloads []string
	for _, p := range gotPayloads {
		if strings.HasPrefix(p, "event:") {
			eventPayloads = append(eventPayloads, p)
		}
	}
	if len(eventPayloads) != 2 {
		t.Fatalf("got %d event payloads, want 2 (orphan should be dropped); payloads: %v", len(eventPayloads), eventPayloads)
	}
}

// TestClaudeExecutorStream_TranslationFiltersNonStandardSSE verifies that the
// ClaudeExecutor drops non-standard SSE lines before translation in the from!=to path.
func TestClaudeExecutorStream_TranslationFiltersNonStandardSSE(t *testing.T) {
	upstreamLines := []string{
		": keep-alive",
		"data: : keep-alive",
		"data: event: content_block_delta",
		"data: ",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
		": ping",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, line := range upstreamLines {
			_, _ = fmt.Fprintf(w, "%s\n", line)
		}
		_, _ = w.Write([]byte("\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer server.Close()

	executor := NewClaudeExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"base_url": server.URL,
		"api_key":  "test",
	}}
	req := cliproxyexecutor.Request{
		Model:   "claude-3-5-sonnet-20241022",
		Payload: []byte(`{"model":"claude-3-5-sonnet-20241022","messages":[{"role":"user","content":"hi"}],"stream":true}`),
	}
	opts := cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai"),
		Stream:       true,
	}

	result, err := executor.ExecuteStream(context.Background(), auth, req, opts)
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil StreamResult")
	}

	var gotPayloads []string
	var streamErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		for chunk := range result.Chunks {
			if chunk.Err != nil {
				streamErr = chunk.Err
				return
			}
			if len(chunk.Payload) > 0 {
				gotPayloads = append(gotPayloads, string(chunk.Payload))
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for stream")
	}

	if streamErr != nil {
		t.Fatalf("unexpected stream error: %v", streamErr)
	}

	// In translation mode the translator may emit multiple chunks per line or synthetic
	// chunks (e.g. finish_reason). We only care that no non-standard SSE leaked.
	for _, p := range gotPayloads {
		if strings.Contains(p, "keep-alive") {
			t.Fatalf("non-standard SSE leaked into downstream: %q", p)
		}
	}

	// At least one real content chunk should have been produced.
	var hasContent bool
	for _, p := range gotPayloads {
		if strings.Contains(p, "hello") {
			hasContent = true
			break
		}
	}
	if !hasContent {
		t.Fatalf("expected at least one chunk containing 'hello', got %v", gotPayloads)
	}
}
