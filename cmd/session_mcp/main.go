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
	SessionID    string `json:"sessionID" jsonschema:"the session ID the pull request belongs to"`
	ToolResponse string `json:"tool_response" jsonschema:"the GitHub tool response as a JSON string containing html_url"`
}

type ghPROutput struct {
	HTMLURL string `json:"html_url" jsonschema:"the pull request URL taken from tool_response"`
}

type sessionTitleInput struct {
	SessionID string `json:"sessionID" jsonschema:"the session ID the title belongs to"`
	Title     string `json:"title" jsonschema:"the session's new title"`
}

// extractHTMLURL returns the html_url field from a GitHub tool response. It
// accepts these forms:
//
//   - a JSON object: {"html_url":"..."}
//   - that object double-encoded as a JSON string literal, with the outer
//     quotes and escapes still present: "{\"html_url\":\"...\"}"
//   - an array of MCP content blocks whose text holds the object:
//     [{"type":"text","text":"{\"html_url\":\"...\"}"}]. This is what a
//     Claude Code hook passes for ${tool_response} after an MCP tool call.
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
		return "", errors.New("tool_response has no text content with html_url")
	}

	var resp struct {
		HTMLURL string `json:"html_url"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("tool_response is not valid JSON: %w", err)
	}

	if resp.HTMLURL == "" {
		return "", errors.New("tool_response has no html_url")
	}

	return resp.HTMLURL, nil
}

func newServer(logger *slog.Logger) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "session_mcp", Version: "v0.1.0"}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "gh_pr",
		Description: "Log the pull request URL (html_url in tool_response) for a session.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ghPRInput) (*mcp.CallToolResult, ghPROutput, error) {
		url, err := extractHTMLURL(in.ToolResponse)
		if err != nil {
			logger.WarnContext(ctx, "gh_pr", "sessionID", in.SessionID, "error", err)
			return nil, ghPROutput{}, err
		}

		logger.InfoContext(ctx, "gh_pr", "sessionID", in.SessionID, "html_url", url)

		// Reply with plain text rather than the default JSON text content.
		// Claude Code parses an mcp_tool hook's text reply as hook output,
		// and a JSON object that isn't valid hook output is reported as an
		// error. The structured output still carries html_url.
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
