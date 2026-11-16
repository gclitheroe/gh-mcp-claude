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
	prURL = "https://github.com/gclitheroe/hello-agent/pull/128"
	// plainJSON is the form Claude Code sends for ${tool_response}: the
	// GitHub object as a string, with the link under html_url.
	plainJSON   = `{"html_url":"https://github.com/gclitheroe/hello-agent/pull/128","id":"5235365474"}`
	escapedJSON = `"{\"html_url\":\"https://github.com/gclitheroe/hello-agent/pull/128\",\"id\":\"5235365474\"}"`
	// contentBlocks is a bare array of MCP content blocks, the form an
	// earlier Claude Code capture had.
	contentBlocks = `[{"type":"text","text":"{\"html_url\":\"https://github.com/gclitheroe/hello-agent/pull/128\",\"id\":\"5235365474\"}"}]`

	// codexResult is the form Codex sends for ${tool_response}, copied from
	// hook-samples/codex/posttool-use-mcp-pr-gh.json: the MCP CallToolResult
	// object, whose GitHub object has the link under url.
	codexURL    = "https://github.com/gclitheroe/hello-agent/pull/142"
	codexResult = `{"content":[{"type":"text","text":"{\"id\":\"5319074384\",\"url\":\"https://github.com/gclitheroe/hello-agent/pull/142\"}"}]}`
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
		{name: "MCP content blocks", in: contentBlocks, want: prURL},
		{
			name: "MCP content blocks after a non-text block",
			in:   `[{"type":"image","data":"x"},{"type":"text","text":"{\"html_url\":\"https://github.com/gclitheroe/hello-agent/pull/128\"}"}]`,
			want: prURL,
		},
		{name: "MCP content blocks without a link", in: `[{"type":"text","text":"{\"id\":\"5235365474\"}"}]`, wantErr: true},
		{name: "Codex CallToolResult", in: codexResult, want: codexURL},
		{name: "url without html_url", in: `{"id":"5319074384","url":"https://github.com/gclitheroe/hello-agent/pull/142"}`, want: codexURL},
		{
			name: "html_url wins over url",
			in:   `{"url":"https://api.github.com/repos/gclitheroe/hello-agent/pulls/128","html_url":"https://github.com/gclitheroe/hello-agent/pull/128"}`,
			want: prURL,
		},
		{name: "CallToolResult without a link", in: `{"content":[{"type":"text","text":"{\"id\":\"5319074384\"}"}]}`, wantErr: true},
		{name: "missing html_url and url", in: `{"id":"5235365474"}`, wantErr: true},
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

func TestToolResponseText(t *testing.T) {
	// Claude Code's string is used as it is.
	if got, err := toolResponseText(plainJSON); err != nil || got != plainJSON {
		t.Errorf("toolResponseText(string) = %q, %v; want %q, nil", got, err, plainJSON)
	}

	// Codex's object is encoded back to JSON that extractHTMLURL reads.
	var obj any
	if err := json.Unmarshal([]byte(codexResult), &obj); err != nil {
		t.Fatalf("unmarshal codexResult: %v", err)
	}
	text, err := toolResponseText(obj)
	if err != nil {
		t.Fatalf("toolResponseText(object) error: %v", err)
	}
	if url, err := extractHTMLURL(text); err != nil || url != codexURL {
		t.Errorf("extractHTMLURL(toolResponseText(object)) = %q, %v; want %q, nil", url, err, codexURL)
	}

	if _, err := toolResponseText(nil); err == nil {
		t.Error("toolResponseText(nil): want error")
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

// connect starts session_mcp on a test HTTP server, logging to logs, and
// returns an MCP client session connected to it.
func connect(t *testing.T, logs *syncBuffer) *mcp.ClientSession {
	t.Helper()

	server := newServer(slog.New(slog.NewTextHandler(logs, nil)))
	ts := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	t.Cleanup(ts.Close)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { session.Close() })

	return session
}

func TestSessionTitleToolOverHTTP(t *testing.T) {
	var logs syncBuffer
	session := connect(t, &logs)
	ctx := context.Background()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "session_title",
		Arguments: map[string]any{"sessionID": "session-123", "title": "  GH MCP Demo  "},
	})
	if err != nil {
		t.Fatalf("call session_title: %v", err)
	}
	if res.IsError {
		t.Fatalf("session_title returned a tool error: %v", res.GetError())
	}

	if len(res.Content) != 1 {
		t.Fatalf("got %d content items, want 1", len(res.Content))
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content is %T, want *mcp.TextContent", res.Content[0])
	}
	if want := `logged title "GH MCP Demo" for session session-123`; text.Text != want {
		t.Errorf("text content = %q, want %q", text.Text, want)
	}

	got := logs.String()
	for _, want := range []string{"session_title", "sessionID=session-123", `title="GH MCP Demo"`} {
		if !strings.Contains(got, want) {
			t.Errorf("log %q does not contain %q", got, want)
		}
	}

	res, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "session_title",
		Arguments: map[string]any{"sessionID": "session-123", "title": "   "},
	})
	if err != nil {
		t.Fatalf("call session_title with empty title: %v", err)
	}
	if !res.IsError {
		t.Errorf("session_title with empty title: IsError = false, want true")
	}
}

func TestGHPRToolOverHTTP(t *testing.T) {
	var logs syncBuffer
	session := connect(t, &logs)
	ctx := context.Background()

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

	if len(res.Content) != 1 {
		t.Fatalf("got %d content items, want 1", len(res.Content))
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content is %T, want *mcp.TextContent", res.Content[0])
	}
	if want := "logged " + prURL + " for session session-123"; text.Text != want {
		t.Errorf("text content = %q, want %q", text.Text, want)
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

// TestGHPRToolCodexObjectOverHTTP passes tool_response as a JSON object, the
// way a Codex mcp_tool hook sends "${tool_response}".
func TestGHPRToolCodexObjectOverHTTP(t *testing.T) {
	var logs syncBuffer
	session := connect(t, &logs)

	var toolResponse any
	if err := json.Unmarshal([]byte(codexResult), &toolResponse); err != nil {
		t.Fatalf("unmarshal codexResult: %v", err)
	}

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "gh_pr",
		Arguments: map[string]any{
			"sessionID":     "codex-session",
			"tool_response": toolResponse,
		},
	})
	if err != nil {
		t.Fatalf("call gh_pr: %v", err)
	}
	if res.IsError {
		t.Fatalf("gh_pr returned a tool error: %v", res.GetError())
	}

	got := logs.String()
	for _, want := range []string{"sessionID=codex-session", "html_url=" + codexURL} {
		if !strings.Contains(got, want) {
			t.Errorf("log %q does not contain %q", got, want)
		}
	}
}
