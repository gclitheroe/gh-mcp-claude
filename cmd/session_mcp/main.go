// Command session_mcp runs an HTTP MCP server on port 8080 with a single
// tool, gh_pr, that logs the session ID and pull request URL it receives.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
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

// extractHTMLURL returns the html_url field from a JSON object string. It
// also accepts the object double-encoded as a JSON string literal, such as
// "{\"html_url\":\"...\"}" with the outer quotes and escapes still present.
func extractHTMLURL(s string) (string, error) {
	var resp struct {
		HTMLURL string `json:"html_url"`
	}

	if err := json.Unmarshal([]byte(s), &resp); err != nil {
		var inner string
		if json.Unmarshal([]byte(s), &inner) != nil {
			return "", fmt.Errorf("tool_response is not valid JSON: %w", err)
		}
		if err := json.Unmarshal([]byte(inner), &resp); err != nil {
			return "", fmt.Errorf("tool_response is not valid JSON: %w", err)
		}
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

		return nil, ghPROutput{HTMLURL: url}, nil
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
