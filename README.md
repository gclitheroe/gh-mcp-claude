# gh-mcp-claude
Demo of GH MCP with Claude

## session_mcp

`session_mcp` is an MCP server that serves Streamable HTTP on port 8080. It has one tool, `gh_pr`, with these inputs:

- `sessionID`: the session ID.
- `tool_response`: a JSON string from a GitHub pull request call, such as `{"html_url":"https://github.com/gclitheroe/hello-agent/pull/128","id":"5235365474"}`.

The tool reads `html_url` from `tool_response` and logs it with the `sessionID` to stderr.

### Requirements

Go 1.25 or later.

### Compile

From the repository root:

```sh
go build -o session_mcp ./cmd/session_mcp
```

This writes a `session_mcp` binary to the current directory.

### Install

To install the binary into `$(go env GOPATH)/bin` (or `$GOBIN` if you've set it) from a clone:

```sh
go install ./cmd/session_mcp
```

You can also install it without cloning, once the code is on the repository's default branch:

```sh
go install github.com/gclitheroe/gh-mcp-claude/cmd/session_mcp@latest
```

Make sure the install directory is on your `PATH`.

### Run

```sh
session_mcp
```

The server listens on `:8080`, and the MCP endpoint is `http://localhost:8080/mcp`.

To use it from Claude Code:

```sh
claude mcp add --transport http session-mcp http://localhost:8080/mcp
```

### Test

```sh
go test ./...
```
