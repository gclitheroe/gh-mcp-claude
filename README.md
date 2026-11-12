# gh-mcp-claude
Demo of GH MCP with Claude

## session_mcp

`session_mcp` is an MCP server that serves Streamable HTTP on port 8080. It logs session details to stderr and has two tools.

`gh_pr` logs a session's pull request URL. Its inputs are:

- `sessionID`: the session ID.
- `tool_response`: a JSON string from a GitHub pull request call, such as `{"html_url":"https://github.com/gclitheroe/hello-agent/pull/128","id":"5235365474"}`. It can also be that object escaped as a JSON string, or an array of MCP content blocks that contain it, such as `[{"type":"text","text":"{\"html_url\":\"…\"}"}]`. The content-block form is what the [gh-pr-session plugin](#gh-pr-session-plugin)'s hook passes.

The tool reads `html_url` from `tool_response` and logs it with the `sessionID`.

`session_title` logs a session's title. Its inputs are:

- `sessionID`: the session ID.
- `title`: the session's new title. An empty title is an error.

Both tools reply with plain text, such as `logged <html_url> for session <sessionID>`, so Claude Code doesn't try to parse the reply as hook JSON.

### Requirements

Go 1.25 or later.

### Compile

From the repository root:

```sh
go build -o bin/ ./cmd/session_mcp
```

This writes the binary to `bin/session_mcp`. Go creates the `bin` directory if it doesn't exist, and git ignores it.

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

If you compiled it into `bin/`:

```sh
./bin/session_mcp
```

If you installed it:

```sh
session_mcp
```

The server listens on `:8080`, and the MCP endpoint is `http://localhost:8080/mcp`.

To use it from Claude Code:

```sh
claude mcp add --transport http session-mcp http://localhost:8080/mcp
```

The [gh-pr-session plugin](#gh-pr-session-plugin) calls the server by the name `session-mcp`, so keep that name if you use the plugin.

### Test

```sh
go test ./...
```

## gh-pr-session plugin

`plugins/gh-pr-session` is a Claude Code plugin that sends session details to `session_mcp`. It does two things:

- **New PRs.** A PostToolUse `mcp_tool` hook runs after the GitHub MCP server's `create_pull_request` tool (`mcp__github__create_pull_request`) succeeds. It calls `gh_pr` with the session ID and the tool's response, so the server logs the new PR's URL for that session.
- **Session titles.** A mod (`hooks/register.ts`) wraps the built-in `/rename` command. After the rename runs, it calls `session_title` with the session ID and the new title. Both are registered in `hooks/hooks.json`, the hook under `hooks` and the mod under `modules`.

The title is only sent when you type `/rename <title>`. Two kinds of rename aren't sent:

- A bare `/rename`, which picks a name itself, because the command doesn't receive that name.
- Titles the desktop app sets, both its automatic titles and its own rename, because they don't go through `/rename`.

### Before you use it

The plugin doesn't install or configure `session_mcp`, so set it up yourself:

1. Start the server (see [Run](#run)).
2. Add it to Claude Code under the name `session-mcp`. Both the hook and the mod call the server by this name:

   ```sh
   claude mcp add --transport http -s user session-mcp http://localhost:8080/mcp
   ```

   `-s user` makes the server available in every project. Leave it out to add the server for the current project only.
3. Configure the official GitHub MCP server under the name `github`. Its PR tool is then `mcp__github__create_pull_request`, which the hook matches exactly.

Start `session_mcp` before Claude Code. If `session-mcp` isn't configured, isn't running, or has a different name, nothing is logged, but the PR and the rename still go through. The PR hook reports a non-blocking error, and the mod shows a short notice.

### Install

From a clone of this repo:

```sh
claude plugin marketplace add ./
claude plugin install gh-pr-session@gh-mcp-demo
```

Or from GitHub, once this is on the repository's default branch:

```sh
claude plugin marketplace add gclitheroe/gh-mcp-claude
claude plugin install gh-pr-session@gh-mcp-demo
```

To try it for one session without installing it:

```sh
claude --plugin-dir plugins/gh-pr-session
```

### Validate

```sh
claude plugin validate plugins/gh-pr-session
claude plugin validate .
```
