// Command session_mcp runs an HTTP MCP server on port 8080. Its tools log
// session details: gh_pr logs a session's pull request URL, and
// session_title logs a session's title.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const addr = ":8080"

type ghPRInput struct {
	SessionID string `json:"sessionID" jsonschema:"the session ID the pull request belongs to"`

	// ToolResponse is the GitHub create_pull_request tool's result, as a
	// client's PostToolUse hook passes it for "${tool_response}". It is any,
	// not string, because Claude Code and Codex send different JSON types
	// for that same placeholder:
	//
	//   - Claude Code substitutes the result into the argument as a string.
	//   - Codex keeps the placeholder's JSON type, so it sends the MCP
	//     CallToolResult as an object.
	//
	// toolResponseText turns either into JSON text, and extractHTMLURL
	// describes both formats in full.
	ToolResponse any `json:"tool_response" jsonschema:"the GitHub create_pull_request result: a JSON string (from Claude Code) or an MCP CallToolResult object (from Codex) whose GitHub object holds html_url or url"`
}

type ghPROutput struct {
	HTMLURL string `json:"html_url" jsonschema:"the pull request's web URL, taken from html_url or url in tool_response"`
}

type sessionTitleInput struct {
	SessionID string `json:"sessionID" jsonschema:"the session ID the title belongs to"`
	Title     string `json:"title" jsonschema:"the session's new title"`
}

// toolResponseText returns tool_response as JSON text for extractHTMLURL.
// A string, which is what Claude Code sends, is used as it is. Anything else,
// such as the CallToolResult object Codex sends, is encoded back to JSON.
func toolResponseText(v any) (string, error) {
	switch v := v.(type) {
	case nil:
		return "", errors.New("tool_response is missing")
	case string:
		return v, nil
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("tool_response can't be encoded as JSON: %w", err)
		}
		return string(data), nil
	}
}

// extractHTMLURL returns the pull request's web URL from a GitHub
// create_pull_request result, given as JSON text. It handles the
// tool_response formats that Claude Code and Codex send from a PostToolUse
// hook. hook-samples/claude/posttool-use-mcp-pr-gh.json and
// hook-samples/codex/posttool-use-mcp-pr-gh.json in the repository are full
// hook payloads from each.
//
// Claude Code sends the tool's text result as a string, so the text is the
// GitHub object itself, with the link under html_url:
//
//	{"html_url":"https://github.com/gclitheroe/hello-agent/pull/139","id":"5284238282"}
//
// Codex sends the MCP CallToolResult object, which toolResponseText encodes
// back to JSON. Its text content holds the GitHub object, and here the
// GitHub server puts the link under url:
//
//	{"content":[{"type":"text","text":"{\"id\":\"5319074384\",\"url\":\"https://github.com/gclitheroe/hello-agent/pull/142\"}"}]}
//
// It also accepts these related forms:
//
//   - a bare array of MCP content blocks, which an earlier Claude Code
//     capture had: [{"type":"text","text":"{\"html_url\":\"...\"}"}]
//   - any of the above double-encoded as a JSON string literal, with the
//     outer quotes and escapes still present: "{\"html_url\":\"...\"}"
//
// The link is html_url when the object has one, and url otherwise. In a full
// GitHub pull request object, url is the API URL, so html_url has to win
// when both are present.
func extractHTMLURL(s string) (string, error) {
	data := []byte(s)

	var inner string
	if json.Unmarshal(data, &inner) == nil {
		data = []byte(inner)
	}

	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(data, &blocks) == nil {
		for _, b := range blocks {
			if b.Type != "text" {
				continue
			}
			if url, err := extractHTMLURL(b.Text); err == nil {
				return url, nil
			}
		}
		return "", errors.New("tool_response has no text content with html_url or url")
	}

	var resp struct {
		HTMLURL string          `json:"html_url"`
		URL     string          `json:"url"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("tool_response is not valid JSON: %w", err)
	}

	switch {
	case resp.HTMLURL != "":
		// The GitHub object as Claude Code receives it.
		return resp.HTMLURL, nil
	case resp.URL != "":
		// The GitHub object as Codex receives it.
		return resp.URL, nil
	case len(resp.Content) > 0:
		// An MCP CallToolResult, as Codex sends it: the GitHub object is in
		// its content blocks.
		return extractHTMLURL(string(resp.Content))
	}

	return "", errors.New("tool_response has no html_url or url")
}

func newServer(logger *slog.Logger) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "session_mcp", Version: "v0.1.0"}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "gh_pr",
		Description: "Log a session's pull request URL. tool_response is the GitHub create_pull_request result as a Claude Code or Codex PostToolUse hook passes it; the URL is its html_url, or url.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ghPRInput) (*mcp.CallToolResult, ghPROutput, error) {
		// Claude Code sends tool_response as a string and Codex as an
		// object, so turn it into JSON text before looking for the URL.
		text, err := toolResponseText(in.ToolResponse)
		var url string
		if err == nil {
			url, err = extractHTMLURL(text)
		}
		if err != nil {
			logger.WarnContext(ctx, "gh_pr", "sessionID", in.SessionID, "error", err)
			return nil, ghPROutput{}, err
		}

		logger.InfoContext(ctx, "gh_pr", "sessionID", in.SessionID, "html_url", url)

		// Reply with plain text rather than the default JSON text content.
		// Claude Code and Codex both read an mcp_tool hook's text reply as
		// hook output, as they would a command hook's stdout. Claude Code
		// reports a JSON object that isn't valid hook output as an error,
		// and Codex ignores plain text after PostToolUse. The structured
		// output still carries html_url.
		res := &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{
				Text: fmt.Sprintf("logged %s for session %s", url, in.SessionID),
			}},
		}

		return res, ghPROutput{HTMLURL: url}, nil
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "session_title",
		Description: "Log a session's title.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in sessionTitleInput) (*mcp.CallToolResult, any, error) {
		title := strings.TrimSpace(in.Title)
		if title == "" {
			logger.WarnContext(ctx, "session_title", "sessionID", in.SessionID, "error", "empty title")
			return nil, nil, errors.New("title is empty")
		}

		logger.InfoContext(ctx, "session_title", "sessionID", in.SessionID, "title", title)

		res := &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{
				Text: fmt.Sprintf("logged title %q for session %s", title, in.SessionID),
			}},
		}

		return res, nil, nil
	})

	return server
}

func main() {
	server := newServer(slog.Default())

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	slog.Info("session_mcp listening", "addr", addr)

	if err := httpServer.ListenAndServe(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
