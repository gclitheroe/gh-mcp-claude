import type { Register } from 'claude-code'

// The MCP server and tool that receive the title. SERVER must match the name
// session_mcp was added under (`claude mcp add ... session-mcp ...`), the
// same server the PR hook in hooks.json calls.
const SERVER = 'session-mcp'
const TOOL = 'session_title'

export const register: Register = on => {
  // Wrap /rename: let the built-in rename run, then send the new title to
  // session_mcp. A bare /rename names the session itself, so its args are
  // empty and there is no title to send. If this hook fails, .catch still
  // lets the rename run, or keeps its result if it already ran.
  on('command.run', { command: 'rename' }, async ($, e, next) => {
    const result = await next(e)

    const title = e.args.trim()
    if (title === '') return result

    try {
      const sent = await $.mcp.call(SERVER, TOOL, { sessionID: await $.session.id(), title })
      if (sent.isError) $.ui.toast(`${$.plugin.name}: ${SERVER} rejected the title`)
    } catch (err) {
      const reason = err instanceof Error ? err.message : String(err)
      $.ui.toast(`${$.plugin.name}: couldn't send the title to ${SERVER}: ${reason}`)
    }

    return result
  }).catch(($, e, next) => next(e))
}
