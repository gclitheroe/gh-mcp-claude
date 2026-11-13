import type { EngineInterface, Register } from 'claude-code'

// The MCP server and tool that receive the title. SERVER must match the name
// session_mcp was added under (`claude mcp add ... session-mcp ...`), the
// same server the PR hook in hooks.json calls.
const SERVER = 'session-mcp'
const TOOL = 'session_title'

// How often to reread the session registry for a new title.
const POLL_MS = 5000

// What the last title sent (or being sent) was, as `<sessionID> <title>`, so a
// title reaches the server once per session however many hooks see it.
let lastSent: string | undefined
// The last title a failure toast was shown for, so a poll can't repeat it.
let lastWarned: string | undefined

async function send($: EngineInterface, title: string): Promise<void> {
  if (title === '') return
  const sessionID = await $.session.id()
  const key = `${sessionID} ${title}`
  if (key === lastSent) return

  const previous = lastSent
  lastSent = key
  let problem: string
  try {
    const sent = await $.mcp.call(SERVER, TOOL, { sessionID, title })
    if (!sent.isError) return
    // The server got the title and refused it; sending it again won't help.
    problem = `${SERVER} rejected the title`
  } catch (err) {
    // Not delivered: let the next poll try again.
    if (lastSent === key) lastSent = previous
    const reason = err instanceof Error ? err.message : String(err)
    problem = `couldn't send the title to ${SERVER}: ${reason}`
  }
  if (lastWarned === key) return
  lastWarned = key
  $.ui.toast(`${$.plugin.name}: ${problem}`)
}

// The session registry: one JSON file per Claude Code process,
// <config dir>/sessions/<pid>.json, that holds the session's id and its
// current title as `name`. Desktop renames, automatic titles and /rename all
// update it at once. It's an internal file, not part of the mod API, so every
// read here tolerates it being missing or shaped differently.
type RegistryEntry = { sessionId?: unknown; name?: unknown }

async function registryDir($: EngineInterface): Promise<string | undefined> {
  const configDir = await $.env.get('CLAUDE_CONFIG_DIR')
  if (configDir) return `${configDir}/sessions`
  const home = await $.env.get('HOME')
  return home ? `${home}/.claude/sessions` : undefined
}

async function readEntry($: EngineInterface, path: string): Promise<RegistryEntry | undefined> {
  try {
    const value: unknown = JSON.parse(await $.fs.read(path))
    return typeof value === 'object' && value !== null ? value : undefined
  } catch {
    return undefined
  }
}

// There is no pid in the mod API, so find this process's file by session id.
// A resumed session can leave a dead process's file with the same id behind;
// the live one is rewritten on every status change, so take the newest.
async function findEntry($: EngineInterface, sessionID: string): Promise<string | undefined> {
  const dir = await registryDir($)
  if (dir === undefined) return undefined
  let files
  try {
    files = await $.fs.list(dir)
  } catch {
    return undefined
  }
  const newestFirst = files
    .filter(f => f.kind === 'file' && f.name.endsWith('.json'))
    .sort((a, b) => b.mtimeMs - a.mtimeMs)
  for (const file of newestFirst) {
    const path = `${dir}/${file.name}`
    if ((await readEntry($, path))?.sessionId === sessionID) return path
  }
  return undefined
}

let entryPath: string | undefined
let polling = false

async function poll($: EngineInterface): Promise<void> {
  if (polling) return
  polling = true
  try {
    const sessionID = await $.session.id()
    let entry = entryPath === undefined ? undefined : await readEntry($, entryPath)
    // First tick, the file is gone, or /clear moved the process to a new id.
    if (entry?.sessionId !== sessionID) {
      entryPath = await findEntry($, sessionID)
      entry = entryPath === undefined ? undefined : await readEntry($, entryPath)
    }
    if (typeof entry?.name === 'string') await send($, entry.name.trim())
  } catch {
    // Try again on the next tick.
  } finally {
    polling = false
  }
}

let timer: { cancel: () => void } | undefined

export const register: Register = on => {
  // Wrap /rename: let the built-in rename run, then send the new title to
  // session_mcp straight away. A bare /rename names the session itself, so its
  // args are empty; the poll below sends that name. If this hook fails, .catch
  // still lets the rename run, or keeps its result if it already ran.
  on('command.run', { command: 'rename' }, async ($, e, next) => {
    const result = await next(e)
    await send($, e.args.trim())
    return result
  }).catch(($, e, next) => next(e))

  // Poll the session registry for title changes that don't go through
  // /rename, such as the desktop app's rename and its automatic titles. The
  // poll starts with the session, so a plugin installed mid-session starts
  // polling in the next one, and a hot reload of the plugin cancels it.
  on('session.start', async ($, e, next) => {
    const started = await next(e)
    timer?.cancel()
    timer = $.clock.every(POLL_MS, () => void poll($))
    return started
  }).catch(($, e, next) => next(e))
}
