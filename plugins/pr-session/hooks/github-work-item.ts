import type { EngineInterface, On, PluginOptions } from 'claude-code'

// After the GitHub MCP server creates a pull request, create a Jira work item
// for it through Atlassian's MCP server, and move the item to a status (Ready
// for Review by default). Claude Code only: Codex has no mods.

// The GitHub MCP server's PR tool. The server must be configured as `github`,
// the same name the gh_pr hook in hooks.json matches.
const GITHUB_CREATE_PR = 'mcp__github__create_pull_request'

// Atlassian's MCP server, by the name it was added under
// (`claude mcp add ... atlassian https://mcp.atlassian.com/v2/mcp`).
const ATLASSIAN = 'atlassian'

// The plugin's userConfig fields (see .claude-plugin/plugin.json).
type Settings = {
  projectKey: string // jira_project_key: nothing is created while it's empty
  issueType: string // jira_issue_type
  status: string // jira_status: the status to move the new item to
  site: string // jira_site: a site URL, hostname or cloudId; empty to look it up
}

function readSettings(options: PluginOptions): Settings {
  const text = (name: string, fallback: string): string => {
    const value = options[name]
    return typeof value === 'string' && value.trim() !== '' ? value.trim() : fallback
  }
  return {
    projectKey: text('jira_project_key', ''),
    issueType: text('jira_issue_type', 'Task'),
    status: text('jira_status', 'Ready for Review'),
    site: text('jira_site', ''),
  }
}

// parseJSON returns text parsed as JSON, or undefined when it isn't JSON.
function parseJSON(text: string | undefined): unknown {
  if (text === undefined) return undefined
  try {
    return JSON.parse(text)
  } catch {
    return undefined
  }
}

// field reads a nested value by its keys, or undefined if any step is missing.
function field(value: unknown, ...path: string[]): unknown {
  for (const key of path) {
    if (typeof value !== 'object' || value === null) return undefined
    value = (value as Record<string, unknown>)[key]
  }
  return value
}

function nonEmpty(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() !== '' ? value : undefined
}

// prLink returns the pull request's web URL from the GitHub tool's result
// text, which Claude Code receives as {"html_url":"https://github.com/...",...}.
// It takes html_url when there is one and url otherwise, as session_mcp's
// gh_pr does: in a full pull request object url is the API URL.
export function prLink(text: string | undefined): string | undefined {
  const pr = parseJSON(text)
  return nonEmpty(field(pr, 'html_url')) ?? nonEmpty(field(pr, 'url'))
}

// callAtlassian calls a tool on Atlassian's MCP server and returns its text
// result parsed as JSON. Atlassian reports some failures with isError and
// others as {"error":true,"message":"..."} text, so either one throws.
async function callAtlassian($: EngineInterface, tool: string, args: Record<string, unknown>): Promise<unknown> {
  const res = await $.mcp.call(ATLASSIAN, tool, args)
  const text = res.content
    .filter(block => block.type === 'text')
    .map(block => block.text ?? '')
    .join('')
  const data = parseJSON(text)
  if (res.isError || field(data, 'error') === true) {
    const message = nonEmpty(field(data, 'message')) ?? (text.slice(0, 200) || 'failed')
    throw new Error(`${tool}: ${message}`)
  }
  return data
}

type Site = {
  cloudId: string // what Atlassian's tools take as cloudId
  url?: string // the site's web address, for the work item's link
}

// siteURL returns the web address for a jira_site value, when it is a URL or
// hostname rather than a cloudId.
function siteURL(site: string): string | undefined {
  if (/^https?:\/\//.test(site)) return site.replace(/\/+$/, '')
  if (site.includes('.')) return `https://${site.replace(/\/+$/, '')}`
  return undefined
}

// The site getAccessibleAtlassianResources found, kept for the rest of a load.
type SiteCache = { site?: Site }

// findSite returns the Atlassian site to create work items in: jira_site when
// it is set, else the one site with Jira that the signed-in user can reach,
// looked up once.
async function findSite($: EngineInterface, settings: Settings, cache: SiteCache): Promise<Site> {
  if (settings.site !== '') return { cloudId: settings.site, url: siteURL(settings.site) }
  if (cache.site !== undefined) return cache.site

  // {"data":{"resources":[{"cloudId":"...","url":"https://x.atlassian.net","products":[{"id":"jira",...}]}]}}
  const data = await callAtlassian($, 'getAccessibleAtlassianResources', {})
  const resources = field(data, 'data', 'resources')
  const withJira = (Array.isArray(resources) ? resources : []).filter(resource => {
    const products = field(resource, 'products')
    return !Array.isArray(products) || products.some(product => field(product, 'id') === 'jira')
  })

  const cloudId = nonEmpty(field(withJira[0], 'cloudId'))
  if (withJira.length !== 1 || cloudId === undefined) {
    throw new Error(
      withJira.length === 0
        ? 'no Atlassian site with Jira is available to this login'
        : `${withJira.length} Atlassian sites have Jira; set jira_site to the one to use`,
    )
  }

  cache.site = { cloudId, url: nonEmpty(field(withJira[0], 'url'))?.replace(/\/+$/, '') }
  return cache.site
}

type WorkItem = {
  key: string
  url?: string
  moved: boolean // whether it reached settings.status
  note?: string // why it didn't, when it didn't
}

// createWorkItem creates the Jira work item for a pull request, then moves it
// to settings.status. Jira creates an item in its workflow's first status, so
// the move is a transition: the one whose target status has that name.
async function createWorkItem(
  $: EngineInterface,
  settings: Settings,
  cache: SiteCache,
  pr: { title: string; body: string; link: string },
): Promise<WorkItem> {
  const site = await findSite($, settings, cache)

  // The description is markdown, which createJiraIssue converts to Jira's
  // format: the PR's own description, then its link.
  const description = pr.body === '' ? `Pull request: ${pr.link}` : `${pr.body}\n\nPull request: ${pr.link}`
  const created = await callAtlassian($, 'createJiraIssue', {
    cloudId: site.cloudId,
    projectKey: settings.projectKey,
    issueType: settings.issueType,
    summary: pr.title,
    description,
  })

  const key =
    nonEmpty(field(created, 'data', 'key')) ??
    nonEmpty(field(created, 'key')) ??
    nonEmpty(field(created, 'data', 'issue', 'key'))
  if (key === undefined) throw new Error('createJiraIssue returned no issue key')

  const item: WorkItem = { key, url: site.url && `${site.url}/browse/${key}`, moved: false }

  // The item exists from here on, so a failure only stops the move.
  try {
    // {"data":{"transitions":[{"id":"21","name":"...","to":{"name":"In Progress",...}},...]}}
    const listed = await callAtlassian($, 'executeRead', {
      name: 'listJiraIssueTransitions',
      cloudId: site.cloudId,
      inputs: { issueIdOrKey: key },
    })
    const transitions = field(listed, 'data', 'transitions')
    const wanted = settings.status.toLowerCase()
    const transition = (Array.isArray(transitions) ? transitions : []).find(
      t => String(field(t, 'to', 'name') ?? '').toLowerCase() === wanted,
    )
    const transitionId = field(transition, 'id')
    if (transitionId === undefined) {
      item.note = `its workflow has no transition to "${settings.status}"`
      return item
    }

    await callAtlassian($, 'transitionJiraIssue', {
      cloudId: site.cloudId,
      issueIdOrKey: key,
      transitionId: String(transitionId),
    })
    item.moved = true
  } catch (err) {
    item.note = `couldn't move it to "${settings.status}": ${err instanceof Error ? err.message : String(err)}`
  }

  return item
}

export function registerGitHubWorkItems(on: On, options: PluginOptions): void {
  const settings = readSettings(options)
  const siteCache: SiteCache = {}

  // Pull requests already handled in this load, by link, so a repeated hook
  // never creates a second work item for the same PR.
  const handled = new Set<string>()
  let warnedNoProject = false

  // Let the PR be created first, then make its work item. The PR's result is
  // returned as it came, with one context line for the model when a work
  // item was made, so a Jira failure never changes what the PR call did.
  on('tool.call', { tool: GITHUB_CREATE_PR }, async ($, e, next) => {
    const ran = await next(e)
    if (ran.deny !== undefined || ran.isError === true) return ran

    const link = prLink(ran.text)
    if (link === undefined || handled.has(link)) return ran

    if (settings.projectKey === '') {
      if (!warnedNoProject) {
        warnedNoProject = true
        $.ui.toast(`${$.plugin.name}: no Jira project set (jira_project_key), so no work item was created for ${link}`)
      }
      return ran
    }
    handled.add(link)

    try {
      const item = await createWorkItem($, settings, siteCache, {
        title: nonEmpty(e.title) ?? `Pull request ${link}`,
        body: typeof e.body === 'string' ? e.body.trim() : '',
        link,
      })

      const where = item.url ?? item.key
      const outcome = item.moved ? `moved it to "${settings.status}"` : `left it in its first status, because ${item.note}`
      $.ui.toast(
        item.moved
          ? `${$.plugin.name}: created ${item.key} for ${link} (${settings.status})`
          : `${$.plugin.name}: created ${item.key} for ${link}, but ${item.note}`,
      )
      return {
        ...ran,
        context: [...(ran.context ?? []), `${$.plugin.name} created Jira work item ${item.key} (${where}) for pull request ${link} and ${outcome}.`],
      }
    } catch (err) {
      const reason = err instanceof Error ? err.message : String(err)
      $.ui.toast(`${$.plugin.name}: couldn't create a Jira work item for ${link}: ${reason}`)
      return ran
    }
  }).catch(($, e, next) => next(e))
}
