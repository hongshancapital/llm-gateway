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

// TestOpenAICompatExecutorStream_FiltersNonStandardSSE verifies that the
// OpenAICompatExecutor drops non-standard SSE lines (event:, : keep-alive,
// data: with non-JSON payload) and only forwards valid JSON data chunks.
func TestOpenAICompatExecutorStream_FiltersNonStandardSSE(t *testing.T) {
	upstreamLines := []string{
		": keep-alive",
		"",
		"event: content_block_delta",
		"data: : keep-alive",
		"data: event: content_block_delta",
		"data: ",
		`data: {"id":"1","object":"chat.completion.chunk","choices":[{"delta":{"content":"hi"}}]}`,
		": ping",
		`data: {"id":"2","object":"chat.completion.chunk","choices":[{"delta":{"content":" there"}}]}`,
		"data: [DONE]",
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

	executor := NewOpenAICompatExecutor("openai-compatibility", &config.Config{})
	auth := &cliproxyauth.Auth{Attributes: map[string]string{
		"base_url": server.URL + "/v1",
		"api_key":  "test",
	}}
	req := cliproxyexecutor.Request{
		Model:   "gpt-4",
		Payload: []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}],"stream":true}`),
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

	// Expect exactly two valid JSON chunks and nothing else.
	if len(gotPayloads) != 2 {
		t.Fatalf("got %d payloads, want 2; payloads: %v", len(gotPayloads), gotPayloads)
	}

	for i, wantPrefix := range []string{
		`{"id":"1"`,
		`{"id":"2"`,
	} {
		if !bytes.HasPrefix([]byte(gotPayloads[i]), []byte(wantPrefix)) {
			t.Fatalf("payload[%d] = %q, want prefix %q", i, gotPayloads[i], wantPrefix)
		}
	}

	// Verify none of the non-standard lines leaked through.
	for _, p := range gotPayloads {
		if strings.Contains(p, "keep-alive") || strings.Contains(p, "content_block_delta") {
			t.Fatalf("non-standard SSE leaked into downstream: %q", p)
		}
	}
}
