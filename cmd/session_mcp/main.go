// Command session_mcp runs an HTTP MCP server on port 8080. Its tools log
// session details: gh_pr and bb_pr log a session's GitHub or Bitbucket pull
// request URL, and session_title logs a session's title.
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

// bbCreatePR is the Atlassian MCP server's executeWrite operation that
// creates a Bitbucket pull request.
const bbCreatePR = "createBitbucketRepoPullRequest"

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
	// toolResponseText turns either into JSON text, and githubPRURL
	// describes both formats in full.
	ToolResponse any `json:"tool_response" jsonschema:"the GitHub create_pull_request result: a JSON string (from Claude Code) or an MCP CallToolResult object (from Codex) whose GitHub object holds html_url or url"`
}

type bbPRInput struct {
	SessionID string `json:"sessionID" jsonschema:"the session ID the pull request belongs to"`

	// Operation is the executeWrite operation, which the hook passes from
	// "${tool_input.name}". The Atlassian MCP server makes every write, for
	// Jira and Confluence as well as Bitbucket, through its one executeWrite
	// tool, so the hook runs for all of them. Only createBitbucketRepoPullRequest
	// creates a pull request. The result alone can't tell them apart, because
	// other Bitbucket operations, such as updating a pull request, can also
	// return a pull request with a link.
	Operation string `json:"operation" jsonschema:"the Atlassian executeWrite operation (tool_input.name); only createBitbucketRepoPullRequest is logged"`

	// ToolResponse is executeWrite's result, as a client's PostToolUse hook
	// passes it for "${tool_response}". It is any for the same reason as
	// ghPRInput.ToolResponse, and bitbucketPRURL describes both clients'
	// formats in full.
	ToolResponse any `json:"tool_response" jsonschema:"the Atlassian executeWrite result: MCP content blocks as a JSON string (from Claude Code) or an MCP CallToolResult object (from Codex) whose Bitbucket object holds data.links.html.href"`
}

type prOutput struct {
	HTMLURL string `json:"html_url,omitempty" jsonschema:"the pull request's web URL; empty when bb_pr skips an operation"`
}

type sessionTitleInput struct {
	SessionID string `json:"sessionID" jsonschema:"the session ID the title belongs to"`
	Title     string `json:"title" jsonschema:"the session's new title"`
}

// toolResponseText returns tool_response as JSON text for githubPRURL and
// bitbucketPRURL. A string is used as it is. Anything else, such as the
// CallToolResult object Codex sends, is encoded back to JSON.
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

var errNoLink = errors.New("tool_response has no pull request link")

// extractLink returns a pull request's web URL from a create-pull-request
// tool's result, given as JSON text. It unwraps the forms Claude Code and
// Codex send from a PostToolUse hook, and calls link on each JSON object it
// reaches. link returns the URL, or "" when the object has none.
//
// The forms it unwraps are:
//
//   - the tool's own JSON object, which link reads directly
//   - an MCP CallToolResult object, {"content":[...]}, whose text content
//     holds the tool's object
//   - a bare array of MCP content blocks, [{"type":"text","text":"..."}]
//   - any of the above double-encoded as a JSON string literal, with the
//     outer quotes and escapes still present
//
// githubPRURL and bitbucketPRURL show which form each client sends.
func extractLink(s string, link func(data []byte) string) (string, error) {
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
			if url, err := extractLink(b.Text, link); err == nil {
				return url, nil
			}
		}
		return "", errNoLink
	}

	var result struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("tool_response is not valid JSON: %w", err)
	}

	if url := link(data); url != "" {
		return url, nil
	}
	if len(result.Content) > 0 {
		// An MCP CallToolResult: the tool's object is in its content blocks.
		return extractLink(string(result.Content), link)
	}

	return "", errNoLink
}

// githubPRURL returns the pull request's web URL from the GitHub MCP server's
// create_pull_request result, as tool_response JSON text. The GitHub server
// returns a different object to each client, and each client wraps it
// differently. hook-samples/claude/posttool-use-mcp-pr-gh.json and
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
// extractLink also accepts the other forms it lists.
func githubPRURL(s string) (string, error) {
	return extractLink(s, githubLink)
}

// githubLink reads a GitHub pull request object's web URL: html_url when it
// has one, and url otherwise. In a full GitHub pull request object, url is
// the API URL, so html_url has to win when both are present.
func githubLink(data []byte) string {
	var pr struct {
		HTMLURL string `json:"html_url"`
		URL     string `json:"url"`
	}
	if json.Unmarshal(data, &pr) != nil {
		return ""
	}
	if pr.HTMLURL != "" {
		return pr.HTMLURL
	}
	return pr.URL
}

// bitbucketPRURL returns the pull request's web URL from the Atlassian MCP
// server's executeWrite result for createBitbucketRepoPullRequest, as
// tool_response JSON text. hook-samples/claude/posttool-use-mcp-pr-bb.json
// and hook-samples/codex/posttool-use-mcp-pr-bb.json in the repository are
// full hook payloads from each client.
//
// The Atlassian server's text result wraps the Bitbucket pull request in
// data, with the link under data.links.html.href. Both clients receive the
// same object, but wrap it differently.
//
// Claude Code's hook payload has the result as a bare array of MCP content
// blocks, and its mcp_tool hook passes that array as a JSON string. Decoded,
// the string is:
//
//	[{"type":"text","text":"{\"data\":{\"id\":2,...,\"links\":{\"html\":{\"href\":\"https://bitbucket.org/gmc-demo/bbc-demo/pull-requests/2\"}}}}"}]
//
// Codex sends the MCP CallToolResult object:
//
//	{"content":[{"type":"text","text":"{\"data\":{\"id\":3,...,\"links\":{\"html\":{\"href\":\"https://bitbucket.org/gmc-demo/bbc-demo/pull-requests/3\"}}}}"}],"isError":false}
//
// extractLink also accepts the other forms it lists.
func bitbucketPRURL(s string) (string, error) {
	return extractLink(s, bitbucketLink)
}

// bbLinks is the part of a Bitbucket pull request object that holds its web
// URL. Its links also has self, which is the API URL, so only html is read.
type bbLinks struct {
	Links struct {
		HTML struct {
			Href string `json:"href"`
		} `json:"html"`
	} `json:"links"`
}

// bitbucketLink reads a Bitbucket pull request's web URL: data.links.html.href
// as the Atlassian server returns it, or links.html.href in a bare Bitbucket
// API pull request object.
func bitbucketLink(data []byte) string {
	var resp struct {
		Data *bbLinks `json:"data"`
		bbLinks
	}
	if json.Unmarshal(data, &resp) != nil {
		return ""
	}
	if resp.Data != nil && resp.Data.Links.HTML.Href != "" {
		return resp.Data.Links.HTML.Href
	}
	return resp.Links.HTML.Href
}

// textResult returns a tool reply with one plain-text content block.
func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

// logPRURL finds the pull request's web URL in toolResponse with find, and
// logs it for the session under the tool's name.
func logPRURL(ctx context.Context, logger *slog.Logger, tool, sessionID string, toolResponse any, find func(string) (string, error)) (*mcp.CallToolResult, prOutput, error) {
	// Claude Code and Codex send tool_response as different JSON types, so
	// turn it into JSON text before looking for the URL.
	text, err := toolResponseText(toolResponse)
	var url string
	if err == nil {
		url, err = find(text)
	}
	if err != nil {
		logger.WarnContext(ctx, tool, "sessionID", sessionID, "error", err)
		return nil, prOutput{}, err
	}

	logger.InfoContext(ctx, tool, "sessionID", sessionID, "html_url", url)

	// Reply with plain text rather than the default JSON text content.
	// Claude Code and Codex both read an mcp_tool hook's text reply as hook
	// output, as they would a command hook's stdout. Claude Code reports a
	// JSON object that isn't valid hook output as an error, and Codex ignores
	// plain text after PostToolUse. The structured output still carries
	// html_url.
	return textResult(fmt.Sprintf("logged %s for session %s", url, sessionID)), prOutput{HTMLURL: url}, nil
}

func newServer(logger *slog.Logger) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "session_mcp", Version: "v0.2.0"}, nil)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "gh_pr",
		Description: "Log a session's GitHub pull request URL. tool_response is the GitHub MCP server's create_pull_request result as a Claude Code or Codex PostToolUse hook passes it; the URL is its html_url, or url.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ghPRInput) (*mcp.CallToolResult, prOutput, error) {
		return logPRURL(ctx, logger, "gh_pr", in.SessionID, in.ToolResponse, githubPRURL)
	})

	mcp.AddTool(server, &mcp.Tool{
		Name:        "bb_pr",
		Description: "Log a session's Bitbucket pull request URL. operation is the Atlassian MCP server's executeWrite operation (tool_input.name): only createBitbucketRepoPullRequest is logged, and other operations are skipped without an error. tool_response is executeWrite's result as a Claude Code or Codex PostToolUse hook passes it; the URL is its data.links.html.href.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in bbPRInput) (*mcp.CallToolResult, prOutput, error) {
		if in.Operation != bbCreatePR {
			// The hook runs after every executeWrite call, so most calls
			// aren't pull requests. Skipping one is the normal case, not an
			// error, and isn't worth an Info log line.
			logger.DebugContext(ctx, "bb_pr skipped", "sessionID", in.SessionID, "operation", in.Operation)
			return textResult(fmt.Sprintf("skipped %q: not %s", in.Operation, bbCreatePR)), prOutput{}, nil
		}

		return logPRURL(ctx, logger, "bb_pr", in.SessionID, in.ToolResponse, bitbucketPRURL)
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

		return textResult(fmt.Sprintf("logged title %q for session %s", title, in.SessionID)), nil, nil
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
