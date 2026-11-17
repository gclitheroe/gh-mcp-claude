# gh-mcp-claude
Demo of logging pull requests from the GitHub and Atlassian (Bitbucket) MCP servers with Claude Code and Codex

## session_mcp

`session_mcp` is an MCP server that serves Streamable HTTP on port 8080. It logs session details to stderr and has three tools.

`gh_pr` logs a session's GitHub pull request URL. Its inputs are:

- `sessionID`: the session ID.
- `tool_response`: the result of the GitHub MCP server's `create_pull_request` tool, as a PostToolUse hook passes it.

`bb_pr` logs a session's Bitbucket pull request URL. Its inputs are:

- `sessionID`: the session ID.
- `operation`: the Atlassian MCP server's `executeWrite` operation, which the hook takes from `tool_input.name`.
- `tool_response`: the result of `executeWrite`, as a PostToolUse hook passes it.

The Atlassian MCP server makes every write, for Jira and Confluence as well as Bitbucket, through its one `executeWrite` tool, so the hook runs after all of them. Neither client can match a hook on an MCP tool's arguments, so `bb_pr` does the filtering. It logs only `createBitbucketRepoPullRequest`. To any other operation it replies `skipped "<operation>": not createBitbucketRepoPullRequest`, without logging it or reporting an error. It can't go by the result alone, because other Bitbucket operations, such as updating a pull request, can also return a pull request with a link.

Both PR tools find the pull request's web URL in `tool_response` and log it with the `sessionID`, under the key `html_url`. Claude Code and Codex pass `tool_response` in different forms, and the tools accept both. See [tool_response formats](#tool_response-formats).

`session_title` logs a session's title. Its inputs are:

- `sessionID`: the session ID.
- `title`: the session's new title. An empty title is an error.

The tools reply with plain text, such as `logged <html_url> for session <sessionID>`. Claude Code and Codex read a hook's reply the way they read a command hook's output, and plain text keeps them from treating it as hook JSON.

### tool_response formats

The plugins pass `"${tool_response}"` to `gh_pr` and `bb_pr`, but the two clients fill it in differently. Claude Code passes a string, and Codex keeps the placeholder's JSON type, so it passes the MCP `CallToolResult` object.

**GitHub.** The GitHub server returns a different object to each client.

- **Claude Code** ([pr-session plugin](#pr-session-plugin)) passes the GitHub tool's JSON text. The link is under `html_url`.

  ```json
  "{\"html_url\":\"https://github.com/gclitheroe/hello-agent/pull/139\",\"id\":\"5284238282\"}"
  ```

- **Codex** ([pr-session-codex plugin](#pr-session-codex-plugin)) passes the `CallToolResult`. The GitHub object is in its text content, and the link is under `url`.

  ```json
  {"content":[{"type":"text","text":"{\"id\":\"5319074384\",\"url\":\"https://github.com/gclitheroe/hello-agent/pull/142\"}"}]}
  ```

`gh_pr` takes `html_url` when there is one and `url` otherwise, because in a full GitHub pull request object `url` is the API URL.

**Bitbucket.** The Atlassian server returns the same text to both clients: the Bitbucket pull request wrapped in `data`, with the link under `data.links.html.href`.

- **Claude Code** passes the MCP content blocks that hold that text, as a JSON string. Decoded, the string is:

  ```json
  [{"type":"text","text":"{\"data\":{\"id\":2,...,\"links\":{\"html\":{\"href\":\"https://bitbucket.org/gmc-demo/bbc-demo/pull-requests/2\"}}}}"}]
  ```

- **Codex** passes the `CallToolResult`:

  ```json
  {"content":[{"type":"text","text":"{\"data\":{\"id\":3,...,\"links\":{\"html\":{\"href\":\"https://bitbucket.org/gmc-demo/bbc-demo/pull-requests/3\"}}}}"}],"isError":false}
  ```

`bb_pr` reads `links.html`, never `links.self`, which is the API URL. It also accepts a bare Bitbucket pull request object, with the link under `links.html.href`.

Both tools accept any of these forms: the tool's own object, a `CallToolResult`, a bare array of MCP content blocks, or any of them escaped as a JSON string.

`hook-samples/` has complete PostToolUse hook payloads from each client, in `claude/` and `codex/`. The examples above come from these.

- `posttool-use-mcp-pr-gh.json`: the GitHub MCP server's `create_pull_request`, which `gh_pr` handles.
- `posttool-use-mcp-pr-bb.json`: the Atlassian MCP server's `executeWrite` creating a Bitbucket pull request, which `bb_pr` handles.

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

The [pr-session](#pr-session-plugin) and [pr-session-codex](#pr-session-codex-plugin) plugins call the server by the name `session-mcp`, so keep that name if you use them.

### Test

```sh
go test ./...
```

## pr-session plugin

`plugins/pr-session` is a Claude Code plugin that sends session details to `session_mcp`. It does three things:

- **GitHub PRs.** A PostToolUse `mcp_tool` hook runs after the GitHub MCP server's `create_pull_request` tool (`mcp__github__create_pull_request`) succeeds. It calls `gh_pr` with the session ID and the tool's response, so the server logs the new PR's URL for that session.
- **Bitbucket PRs.** A second PostToolUse `mcp_tool` hook runs after the Atlassian MCP server's `executeWrite` tool (`mcp__atlassian__executeWrite`) succeeds. It calls `bb_pr` with the session ID, the operation (`tool_input.name`) and the tool's response. The server logs the new PR's URL when the operation is `createBitbucketRepoPullRequest`, and skips any other write.
- **Session titles.** A mod (`hooks/register.ts`) wraps the built-in `/rename` command. After the rename runs, it calls `session_title` with the session ID and the new title.

All three are registered in `hooks/hooks.json`, the hooks under `hooks` and the mod under `modules`.

The title is only sent when you type `/rename <title>`. Two kinds of rename aren't sent:

- A bare `/rename`, which picks a name itself, because the command doesn't receive that name.
- Titles the desktop app sets, both its automatic titles and its own rename, because they don't go through `/rename`.

### Before you use it

The plugin doesn't install or configure `session_mcp` or the MCP servers it watches, so set them up yourself:

1. Start the server (see [Run](#run)).
2. Add it to Claude Code under the name `session-mcp`. The hooks and the mod call the server by this name:

   ```sh
   claude mcp add --transport http -s user session-mcp http://localhost:8080/mcp
   ```

   `-s user` makes the server available in every project. Leave it out to add the server for the current project only.
3. Configure the MCP servers whose pull requests you want logged, under these names, which the hooks match exactly:
   - The official GitHub MCP server as `github`. Its PR tool is then `mcp__github__create_pull_request`.
   - Atlassian's MCP server as `atlassian`. Its write tool is then `mcp__atlassian__executeWrite`. For example:

     ```sh
     claude mcp add --transport http atlassian https://mcp.atlassian.com/v2/mcp
     ```

     Then sign in to Atlassian from `/mcp`.

Start `session_mcp` before Claude Code. If `session-mcp` isn't configured, isn't running, or has a different name, nothing is logged, but the PRs, the other Atlassian writes and the renames still go through. The hooks report a non-blocking error, the Bitbucket one after every Atlassian write, and the mod shows a short notice.

### Install

From a clone of this repo:

```sh
claude plugin marketplace add ./
claude plugin install pr-session@gh-mcp-demo
```

Or from GitHub:

```sh
claude plugin marketplace add gclitheroe/gh-mcp-claude
claude plugin install pr-session@gh-mcp-demo
```

To try it for one session without installing it:

```sh
claude --plugin-dir plugins/pr-session
```

### Upgrade from gh-pr-session

This plugin used to be called `gh-pr-session`. To switch to the new name:

```sh
claude plugin uninstall gh-pr-session@gh-mcp-demo
claude plugin marketplace update gh-mcp-demo
claude plugin install pr-session@gh-mcp-demo
```

### Validate

```sh
claude plugin validate plugins/pr-session
claude plugin validate .
```

### Check it works

Create a PR through the GitHub MCP server, or a Bitbucket PR through the Atlassian MCP server, in Claude Code. The `session_mcp` log should show a line such as:

```
INFO gh_pr sessionID=3f27d8ef-5be7-4648-83fb-b943d4c21d25 html_url=https://github.com/gclitheroe/hello-agent/pull/139
INFO bb_pr sessionID=aa1c0aac-473b-4284-ab3d-b15006d48417 html_url=https://bitbucket.org/gmc-demo/bbc-demo/pull-requests/2
```

## pr-session-codex plugin

`plugins/pr-session-codex` is a Codex plugin that logs new PRs to `session_mcp`. It is hooks only: it has the PR hooks of the [pr-session plugin](#pr-session-plugin), and nothing for session titles.

Its two PostToolUse `mcp_tool` hooks are:

- **GitHub PRs.** It runs after the GitHub MCP server's `create_pull_request` tool (`mcp__github__create_pull_request`) returns, and calls `gh_pr` with the session ID and the tool's result.
- **Bitbucket PRs.** It runs after the Atlassian MCP server's `executeWrite` tool (`mcp__atlassian__executeWrite`) returns, and calls `bb_pr` with the session ID, the operation (`tool_input.name`) and the tool's result. The server logs the new PR's URL when the operation is `createBitbucketRepoPullRequest`, and skips any other write. Because this hook runs after every Atlassian write, its status message is "Checking for a Bitbucket pull request".

Codex passes the results as objects rather than strings, which both tools accept (see [tool_response formats](#tool_response-formats)).

The plugin's files are:

- `.codex-plugin/plugin.json`: the manifest.
- `hooks/hooks.json`: the hooks. Codex reads hooks from this path by default.

The repository's Codex marketplace, `gh-mcp-demo`, is `.agents/plugins/marketplace.json`.

### Before you use it

The plugin doesn't install or configure `session_mcp` or the MCP servers it watches, so set them up yourself:

1. Start the server (see [Run](#run)).
2. Add it to Codex under the name `session-mcp`, which the hooks call the server by:

   ```sh
   codex mcp add session-mcp --url http://localhost:8080/mcp
   ```

   This adds the following to `~/.codex/config.toml`:

   ```toml
   [mcp_servers.session-mcp]
   url = "http://localhost:8080/mcp"
   ```
3. Configure the MCP servers whose pull requests you want logged, under these names. The hooks' matchers, `^mcp__github__create_pull_request$` and `^mcp__atlassian__executeWrite$`, match their tools exactly.
   - The official GitHub MCP server as `github`. For example:

     ```toml
     [mcp_servers.github]
     url = "https://api.githubcopilot.com/mcp/"
     bearer_token_env_var = "GITHUB_PAT"
     ```

     Here `GITHUB_PAT` is the name of the environment variable that holds your GitHub token.
   - Atlassian's MCP server as `atlassian`:

     ```sh
     codex mcp add atlassian --url https://mcp.atlassian.com/v2/mcp
     ```

     This adds the following to `~/.codex/config.toml`:

     ```toml
     [mcp_servers.atlassian]
     url = "https://mcp.atlassian.com/v2/mcp"
     ```

     Sign in to Atlassian when Codex asks, or with `codex mcp login atlassian`.

Start `session_mcp` before Codex. If `session-mcp` isn't configured, isn't running, or has a different name, nothing is logged, but the PRs and the other Atlassian writes still go through. Codex reports the hooks' failures without blocking.

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
   codex plugin add pr-session-codex@gh-mcp-demo
   ```

   You can also install it from `/plugins` in the Codex CLI: open the `gh-mcp-demo` tab and install `pr-session-codex`. In the desktop app, use the Plugins Directory.

3. Start a new Codex session.
4. Trust the hooks. Codex doesn't run a plugin's hooks until you've reviewed them. In the CLI, run `/hooks` and trust both `pr-session-codex` hooks. In the desktop app, use **Review hooks** in the composer, or **Settings > Hooks**.

`codex plugin list` shows the plugin as `installed, enabled`.

### Upgrade from gh-pr-session-codex

This plugin used to be called `gh-pr-session-codex`. To switch to the new name:

1. For a GitHub marketplace, refresh it first with `codex plugin marketplace upgrade`.
2. Remove the old plugin and install the new one:

   ```sh
   codex plugin remove gh-pr-session-codex@gh-mcp-demo
   codex plugin add pr-session-codex@gh-mcp-demo
   ```

3. Start a new Codex session and trust the hooks in `/hooks`.

### Update

Codex installs a copy of the plugin under `~/.codex/plugins/cache/`. To pick up changes:

1. For a GitHub marketplace, refresh it first with `codex plugin marketplace upgrade`.
2. Run `codex plugin add pr-session-codex@gh-mcp-demo` again.
3. Trust the hooks again in `/hooks`. Codex asks for a new review whenever a plugin's hooks change.

### Turn it off or remove it

To turn it off without uninstalling it, press Space on it in `/plugins`, or set this in `~/.codex/config.toml`:

```toml
[plugins."pr-session-codex@gh-mcp-demo"]
enabled = false
```

To uninstall it:

```sh
codex plugin remove pr-session-codex@gh-mcp-demo
```

### Check it works

Create a PR through the GitHub MCP server, or a Bitbucket PR through the Atlassian MCP server, in Codex. The `session_mcp` log should show a line such as:

```
INFO gh_pr sessionID=01a1ddc4-9882-7a63-bee4-0b40426c92bb html_url=https://github.com/gclitheroe/hello-agent/pull/142
INFO bb_pr sessionID=01a1e6b3-296f-7c22-b8f4-644a465aaec8 html_url=https://bitbucket.org/gmc-demo/bbc-demo/pull-requests/3
```

`sessionID` is the Codex session (thread) ID.
