package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	prURL       = "https://github.com/gclitheroe/hello-agent/pull/128"
	plainJSON   = `{"html_url":"https://github.com/gclitheroe/hello-agent/pull/128","id":"5235365474"}`
	escapedJSON = `"{\"html_url\":\"https://github.com/gclitheroe/hello-agent/pull/128\",\"id\":\"5235365474\"}"`
)

func TestExtractHTMLURL(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "plain JSON", in: plainJSON, want: prURL},
		{name: "escaped JSON string", in: escapedJSON, want: prURL},
		{name: "missing html_url", in: `{"id":"5235365474"}`, wantErr: true},
		{name: "invalid JSON", in: `not json`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractHTMLURL(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("extractHTMLURL(%q) = %q, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("extractHTMLURL(%q) error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("extractHTMLURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// syncBuffer is a bytes.Buffer that is safe to write from the server
// goroutine and read from the test goroutine.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestGHPRToolOverHTTP(t *testing.T) {
	var logs syncBuffer
	server := newServer(slog.New(slog.NewTextHandler(&logs, nil)))

	ts := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	defer ts.Close()

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "gh_pr",
		Arguments: map[string]any{
			"sessionID":     "session-123",
			"tool_response": escapedJSON,
		},
	})
	if err != nil {
		t.Fatalf("call gh_pr: %v", err)
	}
	if res.IsError {
		t.Fatalf("gh_pr returned a tool error: %v", res.GetError())
	}

	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var out ghPROutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal structured content: %v", err)
	}
	if out.HTMLURL != prURL {
		t.Errorf("html_url = %q, want %q", out.HTMLURL, prURL)
	}

	got := logs.String()
	for _, want := range []string{"sessionID=session-123", "html_url=" + prURL} {
		if !strings.Contains(got, want) {
			t.Errorf("log %q does not contain %q", got, want)
		}
	}

	res, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name: "gh_pr",
		Arguments: map[string]any{
			"sessionID":     "session-123",
			"tool_response": `{"id":"5235365474"}`,
		},
	})
	if err != nil {
		t.Fatalf("call gh_pr with bad input: %v", err)
	}
	if !res.IsError {
		t.Errorf("gh_pr with no html_url: IsError = false, want true")
	}
}
