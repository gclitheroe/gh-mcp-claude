# gh-mcp-claude
Demo of GH MCP with Claude Code and Codex

## session_mcp

`session_mcp` is an MCP server that serves Streamable HTTP on port 8080. It logs session details to stderr and has two tools.

`gh_pr` logs a session's pull request URL. Its inputs are:

- `sessionID`: the session ID.
- `tool_response`: the result of the GitHub MCP server's `create_pull_request` tool, as a PostToolUse hook passes it. Claude Code and Codex pass it in different forms, and the tool accepts both. See [tool_response formats](#tool_response-formats).

The tool finds the pull request's web URL in `tool_response` and logs it with the `sessionID`.

`session_title` logs a session's title. Its inputs are:

- `sessionID`: the session ID.
- `title`: the session's new title. An empty title is an error.

Both tools reply with plain text, such as `logged <html_url> for session <sessionID>`. Claude Code and Codex read a hook's reply the way they read a command hook's output, and plain text keeps them from treating it as hook JSON.

### tool_response formats

Both plugins pass `"${tool_response}"` to `gh_pr`, but the two clients fill it in differently:

- **Claude Code** ([gh-pr-session plugin](#gh-pr-session-plugin)) passes a string: the GitHub tool's JSON text. The link is under `html_url`.

  ```json
  "{\"html_url\":\"https://github.com/gclitheroe/hello-agent/pull/139\",\"id\":\"5284238282\"}"
  ```

- **Codex** ([gh-pr-session-codex plugin](#gh-pr-session-codex-plugin)) keeps the placeholder's JSON type, so it passes the MCP `CallToolResult` object. The GitHub object is in its text content, and the link is under `url`.

  ```json
  {"content":[{"type":"text","text":"{\"id\":\"5319074384\",\"url\":\"https://github.com/gclitheroe/hello-agent/pull/142\"}"}]}
  ```

`gh_pr` also accepts a bare array of MCP content blocks, and any of these forms escaped as a JSON string. It takes `html_url` when there is one and `url` otherwise, because in a full GitHub pull request object `url` is the API URL.

`hook-samples/` has complete PostToolUse hook payloads from each client, in `claude/` and `codex/`:

- `posttool-use-mcp-pr-gh.json`: the GitHub MCP server's `create_pull_request`, the tool the plugins handle. The examples above come from these.
- `posttool-use-mcp-pr-bb.json`: the Atlassian MCP server's `executeWrite` creating a Bitbucket pull request, for comparison. The plugins don't match this tool, and `gh_pr` doesn't read its link, which is under `data.links.html.href`.

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

You can also install it without cloning:

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

To use it from Codex:

```sh
codex mcp add session-mcp --url http://localhost:8080/mcp
```

The [gh-pr-session](#gh-pr-session-plugin) and [gh-pr-session-codex](#gh-pr-session-codex-plugin) plugins call the server by the name `session-mcp`, so keep that name if you use them.

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

Or from GitHub:

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

## gh-pr-session-codex plugin

`plugins/gh-pr-session-codex` is a Codex plugin that logs new PRs to `session_mcp`. It is hooks only: it has the PR hook of the [gh-pr-session plugin](#gh-pr-session-plugin), and nothing for session titles.

Its PostToolUse `mcp_tool` hook runs after the GitHub MCP server's `create_pull_request` tool (`mcp__github__create_pull_request`) returns. It calls `gh_pr` with the session ID and the tool's result, so the server logs the new PR's URL for that session. Codex passes the result as an object rather than a string, which `gh_pr` accepts (see [tool_response formats](#tool_response-formats)).

The plugin's files are:

- `.codex-plugin/plugin.json`: the manifest.
- `hooks/hooks.json`: the hook. Codex reads hooks from this path by default.

The repository's Codex marketplace, `gh-mcp-demo`, is `.agents/plugins/marketplace.json`.

### Before you use it

The plugin doesn't install or configure `session_mcp`, so set it up yourself:

1. Start the server (see [Run](#run)).
2. Add it to Codex under the name `session-mcp`, which the hook calls the server by:

   ```sh
   codex mcp add session-mcp --url http://localhost:8080/mcp
   ```

   This adds the following to `~/.codex/config.toml`:

   ```toml
   [mcp_servers.session-mcp]
   url = "http://localhost:8080/mcp"
   ```
3. Configure the official GitHub MCP server under the name `github`. Its PR tool is then `mcp__github__create_pull_request`, which the hook's matcher, `^mcp__github__create_pull_request$`, matches exactly. For example:

   ```toml
   [mcp_servers.github]
   url = "https://api.githubcopilot.com/mcp/"
   bearer_token_env_var = "GITHUB_PAT"
   ```

   Here `GITHUB_PAT` is the name of the environment variable that holds your GitHub token.

Start `session_mcp` before Codex. If `session-mcp` isn't configured, isn't running, or has a different name, nothing is logged, but the PR still goes through. Codex reports the hook's failure without blocking.

### Install

1. Add this repository as a Codex marketplace. From a clone of this repo:

   ```sh
   codex plugin marketplace add ./
   ```

   Or from GitHub:

   ```sh
   codex plugin marketplace add gclitheroe/gh-mcp-claude
   ```

2. Install the plugin:

   ```sh
   codex plugin add gh-pr-session-codex@gh-mcp-demo
   ```

   You can also install it from `/plugins` in the Codex CLI: open the `gh-mcp-demo` tab and install `gh-pr-session-codex`. In the desktop app, use the Plugins Directory.

3. Start a new Codex session.
4. Trust the hook. Codex doesn't run a plugin's hooks until you've reviewed them. In the CLI, run `/hooks` and trust the `gh-pr-session-codex` hook. In the desktop app, use **Review hooks** in the composer, or **Settings > Hooks**.

`codex plugin list` shows the plugin as `installed, enabled`.

### Update

Codex installs a copy of the plugin under `~/.codex/plugins/cache/`. To pick up changes:

1. For a GitHub marketplace, refresh it first with `codex plugin marketplace upgrade`.
2. Run `codex plugin add gh-pr-session-codex@gh-mcp-demo` again.
3. Trust the hook again in `/hooks`. Codex asks for a new review whenever a plugin's hooks change.

### Turn it off or remove it

To turn it off without uninstalling it, press Space on it in `/plugins`, or set this in `~/.codex/config.toml`:

```toml
[plugins."gh-pr-session-codex@gh-mcp-demo"]
enabled = false
```

To uninstall it:

```sh
codex plugin remove gh-pr-session-codex@gh-mcp-demo
```

### Check it works

Create a PR through the GitHub MCP server in Codex. The `session_mcp` log should show a line such as:

```
INFO gh_pr sessionID=01a1ddc4-9882-7a63-bee4-0b40426c92bb html_url=https://github.com/gclitheroe/hello-agent/pull/142
```

`sessionID` is the Codex session (thread) ID.
