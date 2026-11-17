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
	// contentBlocks is a bare array of MCP content blocks, the form Claude
	// Code's hook payload has for some MCP servers.
	contentBlocks = `[{"type":"text","text":"{\"html_url\":\"https://github.com/gclitheroe/hello-agent/pull/128\",\"id\":\"5235365474\"}"}]`

	// codexResult is the form Codex sends for ${tool_response}, copied from
	// hook-samples/codex/posttool-use-mcp-pr-gh.json: the MCP CallToolResult
	// object, whose GitHub object has the link under url.
	codexURL    = "https://github.com/gclitheroe/hello-agent/pull/142"
	codexResult = `{"content":[{"type":"text","text":"{\"id\":\"5319074384\",\"url\":\"https://github.com/gclitheroe/hello-agent/pull/142\"}"}]}`

	// bbClaudeBlocks is the Atlassian executeWrite result in Claude Code's
	// hook payload, copied from hook-samples/claude/posttool-use-mcp-pr-bb.json:
	// a bare array of MCP content blocks, with the link under
	// data.links.html.href.
	bbClaudeURL    = "https://bitbucket.org/gmc-demo/bbc-demo/pull-requests/2"
	bbClaudeBlocks = `[{"type":"text","text":"{\"data\":{\"id\":2,\"title\":\"Test PR via Atlassian MCP (hook logging)\",\"state\":\"OPEN\",\"source\":{\"branch\":{\"name\":\"test/mcp-pr-hook\"}},\"destination\":{\"branch\":{\"name\":\"main\"}},\"links\":{\"html\":{\"href\":\"https://bitbucket.org/gmc-demo/bbc-demo/pull-requests/2\"}}}}"}]`

	// bbCodexResult is the Atlassian executeWrite result Codex sends, copied
	// from hook-samples/codex/posttool-use-mcp-pr-bb.json: the MCP
	// CallToolResult object.
	bbCodexURL    = "https://bitbucket.org/gmc-demo/bbc-demo/pull-requests/3"
	bbCodexResult = `{"content":[{"type":"text","text":"{\"data\":{\"id\":3,\"title\":\"Test PR: Atlassian MCP payload logging (2026-11-17)\",\"state\":\"OPEN\",\"source\":{\"branch\":{\"name\":\"test/mcp-payload-log-2026-11-17\"}},\"destination\":{\"branch\":{\"name\":\"main\"}},\"links\":{\"html\":{\"href\":\"https://bitbucket.org/gmc-demo/bbc-demo/pull-requests/3\"}}}}"}],"isError":false}`
)

// jsonString returns s encoded as a JSON string literal.
func jsonString(t *testing.T, s string) string {
	t.Helper()
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("encode %q: %v", s, err)
	}
	return string(data)
}

// decodeJSON returns the JSON text s decoded into a Go value, the way an
// MCP client would send it as an object or array argument.
func decodeJSON(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("decode %q: %v", s, err)
	}
	return v
}

func TestGitHubPRURL(t *testing.T) {
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
		{name: "Bitbucket result", in: bbCodexResult, wantErr: true},
		{name: "invalid JSON", in: `not json`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := githubPRURL(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("githubPRURL(%q) = %q, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("githubPRURL(%q) error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("githubPRURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestBitbucketPRURL(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "Claude Code content blocks", in: bbClaudeBlocks, want: bbClaudeURL},
		{name: "Claude Code content blocks as a JSON string", in: jsonString(t, bbClaudeBlocks), want: bbClaudeURL},
		{name: "Codex CallToolResult", in: bbCodexResult, want: bbCodexURL},
		{
			name: "bare pull request, html wins over self",
			in:   `{"id":2,"links":{"self":{"href":"https://api.bitbucket.org/2.0/repositories/gmc-demo/bbc-demo/pullrequests/2"},"html":{"href":"https://bitbucket.org/gmc-demo/bbc-demo/pull-requests/2"}}}`,
			want: bbClaudeURL,
		},
		{name: "data without links", in: `{"data":{"id":2}}`, wantErr: true},
		{
			name:    "only a self link",
			in:      `{"data":{"links":{"self":{"href":"https://api.bitbucket.org/2.0/repositories/gmc-demo/bbc-demo/pullrequests/2"}}}}`,
			wantErr: true,
		},
		{name: "CallToolResult error", in: `{"content":[{"type":"text","text":"Pull request creation failed"}],"isError":true}`, wantErr: true},
		{name: "GitHub result", in: plainJSON, wantErr: true},
		{name: "invalid JSON", in: `not json`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := bitbucketPRURL(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("bitbucketPRURL(%q) = %q, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("bitbucketPRURL(%q) error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("bitbucketPRURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestToolResponseText(t *testing.T) {
	// A string is used as it is.
	if got, err := toolResponseText(plainJSON); err != nil || got != plainJSON {
		t.Errorf("toolResponseText(string) = %q, %v; want %q, nil", got, err, plainJSON)
	}

	// Codex's object is encoded back to JSON that githubPRURL reads.
	text, err := toolResponseText(decodeJSON(t, codexResult))
	if err != nil {
		t.Fatalf("toolResponseText(object) error: %v", err)
	}
	if url, err := githubPRURL(text); err != nil || url != codexURL {
		t.Errorf("githubPRURL(toolResponseText(object)) = %q, %v; want %q, nil", url, err, codexURL)
	}

	// An array of content blocks is encoded back to JSON too.
	text, err = toolResponseText(decodeJSON(t, bbClaudeBlocks))
	if err != nil {
		t.Fatalf("toolResponseText(array) error: %v", err)
	}
	if url, err := bitbucketPRURL(text); err != nil || url != bbClaudeURL {
		t.Errorf("bitbucketPRURL(toolResponseText(array)) = %q, %v; want %q, nil", url, err, bbClaudeURL)
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

// replyText returns the text of a tool result's single text content block.
func replyText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) != 1 {
		t.Fatalf("got %d content items, want 1", len(res.Content))
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content is %T, want *mcp.TextContent", res.Content[0])
	}
	return text.Text
}

// structuredOutput decodes a PR tool's structured output.
func structuredOutput(t *testing.T, res *mcp.CallToolResult) prOutput {
	t.Helper()
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var out prOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal structured content %s: %v", raw, err)
	}
	return out
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

	if got, want := replyText(t, res), `logged title "GH MCP Demo" for session session-123`; got != want {
		t.Errorf("text content = %q, want %q", got, want)
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

	if out := structuredOutput(t, res); out.HTMLURL != prURL {
		t.Errorf("html_url = %q, want %q", out.HTMLURL, prURL)
	}
	if got, want := replyText(t, res), "logged "+prURL+" for session session-123"; got != want {
		t.Errorf("text content = %q, want %q", got, want)
	}

	got := logs.String()
	for _, want := range []string{"gh_pr", "sessionID=session-123", "html_url=" + prURL} {
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

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "gh_pr",
		Arguments: map[string]any{
			"sessionID":     "codex-session",
			"tool_response": decodeJSON(t, codexResult),
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

func TestBBPRToolOverHTTP(t *testing.T) {
	tests := []struct {
		name         string
		sessionID    string
		toolResponse any
		want         string
	}{
		// Claude Code's mcp_tool hook sends the content blocks as JSON text.
		{name: "Claude Code content blocks as text", sessionID: "claude-session", toolResponse: bbClaudeBlocks, want: bbClaudeURL},
		{name: "content blocks as an array", sessionID: "claude-session", toolResponse: decodeJSON(t, bbClaudeBlocks), want: bbClaudeURL},
		{name: "Codex CallToolResult object", sessionID: "codex-session", toolResponse: decodeJSON(t, bbCodexResult), want: bbCodexURL},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs syncBuffer
			session := connect(t, &logs)

			res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
				Name: "bb_pr",
				Arguments: map[string]any{
					"sessionID":     tt.sessionID,
					"operation":     bbCreatePR,
					"tool_response": tt.toolResponse,
				},
			})
			if err != nil {
				t.Fatalf("call bb_pr: %v", err)
			}
			if res.IsError {
				t.Fatalf("bb_pr returned a tool error: %v", res.GetError())
			}

			if out := structuredOutput(t, res); out.HTMLURL != tt.want {
				t.Errorf("html_url = %q, want %q", out.HTMLURL, tt.want)
			}
			if got, want := replyText(t, res), "logged "+tt.want+" for session "+tt.sessionID; got != want {
				t.Errorf("text content = %q, want %q", got, want)
			}

			got := logs.String()
			for _, want := range []string{"bb_pr", "sessionID=" + tt.sessionID, "html_url=" + tt.want} {
				if !strings.Contains(got, want) {
					t.Errorf("log %q does not contain %q", got, want)
				}
			}
		})
	}
}

// TestBBPRToolSkipsOtherOperations checks that bb_pr ignores executeWrite
// calls that don't create a pull request, since the hook runs after every
// Atlassian write.
func TestBBPRToolSkipsOtherOperations(t *testing.T) {
	var logs syncBuffer
	session := connect(t, &logs)

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "bb_pr",
		Arguments: map[string]any{
			"sessionID":     "session-123",
			"operation":     "createJiraIssue",
			"tool_response": decodeJSON(t, `{"content":[{"type":"text","text":"{\"key\":\"DEMO-1\"}"}],"isError":false}`),
		},
	})
	if err != nil {
		t.Fatalf("call bb_pr: %v", err)
	}
	if res.IsError {
		t.Fatalf("bb_pr returned a tool error for a skipped operation: %v", res.GetError())
	}

	if got, want := replyText(t, res), `skipped "createJiraIssue": not createBitbucketRepoPullRequest`; got != want {
		t.Errorf("text content = %q, want %q", got, want)
	}
	if out := structuredOutput(t, res); out.HTMLURL != "" {
		t.Errorf("html_url = %q, want empty", out.HTMLURL)
	}
	if got := logs.String(); strings.Contains(got, "bb_pr") {
		t.Errorf("log %q has a bb_pr line for a skipped operation", got)
	}
}

func TestBBPRToolWithoutLink(t *testing.T) {
	var logs syncBuffer
	session := connect(t, &logs)

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "bb_pr",
		Arguments: map[string]any{
			"sessionID":     "session-123",
			"operation":     bbCreatePR,
			"tool_response": decodeJSON(t, `{"content":[{"type":"text","text":"Pull request creation failed"}],"isError":true}`),
		},
	})
	if err != nil {
		t.Fatalf("call bb_pr: %v", err)
	}
	if !res.IsError {
		t.Errorf("bb_pr with no link: IsError = false, want true")
	}
}
