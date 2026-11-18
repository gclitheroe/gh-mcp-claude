import type { McpToolResult, On } from 'claude-code'
import { expect, test } from 'claude-code/testing'

// The GitHub create_pull_request call and its result as Claude Code receives
// it, from hook-samples/claude/posttool-use-mcp-pr-gh.json.
const PR_LINK = 'https://github.com/gclitheroe/hello-agent/pull/139'
const PR_TEXT = `{"html_url":"${PR_LINK}","id":"5284238282"}`
const PR_CALL = {
  tool: 'mcp__github__create_pull_request',
  owner: 'gclitheroe',
  repo: 'hello-agent',
  title: 'Add eleventh PR test file',
  head: 'test-pr-2026-11-13-g',
  base: 'main',
  body: '## Summary\n- Adds `PR_TEST_11.md`.',
} as const

// Atlassian's answers, in the shapes its MCP server returns them.
const CLOUD_ID = '45e68d54-b44a-491d-8b94-37d4b79edc4f'
const SITE = 'https://gclitheroe.atlassian.net'
const ONE_SITE = {
  data: { resources: [{ cloudId: CLOUD_ID, url: SITE, products: [{ id: 'jira', access: 'read-write' }] }] },
}
const CREATED = { data: { id: '10002', key: 'DEMO-7' } }
const TRANSITIONS = {
  data: {
    transitions: [
      { id: '11', name: 'To Do', to: { name: 'To Do' } },
      { id: '41', name: 'Send for review', to: { name: 'Review' } },
      // The match is on the whole status name, so Review never picks this one.
      { id: '51', name: 'Ready', to: { name: 'Ready for Review' } },
      { id: '31', name: 'Done', to: { name: 'Done' } },
    ],
  },
}

type Call = { server: string; tool: string; args: Record<string, unknown> }
type Answers = Record<string, unknown> // Atlassian tool (or executeRead operation) -> its JSON

const reply = (data: unknown): McpToolResult => ({
  content: [{ type: 'text', text: JSON.stringify(data) }],
  isError: false,
})

// stand puts in the hooks that stand for the engine: the GitHub tool (its
// result, or an error), Atlassian's MCP server and toasts. It returns what the
// plugin called and showed.
function stand(on: On, answers: Answers, pr: { isError?: boolean } = {}) {
  const calls: Call[] = []
  const toasts: string[] = []

  on('tool.call', () => (pr.isError ? { result: {}, text: 'Validation Failed', isError: true } : { result: {}, text: PR_TEXT }))
  on('mcp.call', (_, e) => {
    calls.push({ server: e.server, tool: e.tool, args: e.args })
    const name = e.tool === 'executeRead' ? String(e.args.name) : e.tool
    return { value: name in answers ? reply(answers[name]) : reply({ error: true, message: `no answer for ${name}` }) }
  })
  on('ui.toast', (_, e) => {
    toasts.push(e.text)
    return { value: undefined }
  })

  return { calls, toasts }
}

const ALL: Answers = {
  getAccessibleAtlassianResources: ONE_SITE,
  createJiraIssue: CREATED,
  listJiraIssueTransitions: TRANSITIONS,
  transitionJiraIssue: { data: { status: { name: 'Review' } } },
}

test('creates a work item for the PR and moves it to Review', { options: { jira_project_key: 'DEMO' } }, async ($, on) => {
  const { calls, toasts } = stand(on, ALL)

  const ran = await $.tool.call(PR_CALL)

  expect(calls.map(c => `${c.server} ${c.tool}`)).toEqual([
    'atlassian getAccessibleAtlassianResources',
    'atlassian createJiraIssue',
    'atlassian executeRead',
    'atlassian transitionJiraIssue',
  ])
  expect(calls[1]?.args).toEqual({
    cloudId: CLOUD_ID,
    projectKey: 'DEMO',
    issueType: 'Task',
    summary: PR_CALL.title,
    description: `${PR_CALL.body}\n\nPull request: ${PR_LINK}`,
  })
  expect(calls[2]?.args).toEqual({ name: 'listJiraIssueTransitions', cloudId: CLOUD_ID, inputs: { issueIdOrKey: 'DEMO-7' } })
  expect(calls[3]?.args).toEqual({ cloudId: CLOUD_ID, issueIdOrKey: 'DEMO-7', transitionId: '41' })

  // The PR's own result is untouched; the model is told about the work item.
  expect(ran.text).toBe(PR_TEXT)
  expect(ran.context?.join('\n')).toContain(`DEMO-7 (${SITE}/browse/DEMO-7)`)
  expect(ran.context?.join('\n')).toContain('moved it to "Review"')
  expect(toasts).toEqual([`pr-session: created DEMO-7 for ${PR_LINK} (Review)`])
})

test('a second call for the same PR creates nothing more', { options: { jira_project_key: 'DEMO' } }, async ($, on) => {
  const { calls } = stand(on, ALL)

  await $.tool.call(PR_CALL)
  await $.tool.call(PR_CALL)

  expect(calls.filter(c => c.tool === 'createJiraIssue')).toHaveLength(1)
  // The site was looked up once, and kept.
  expect(calls.filter(c => c.tool === 'getAccessibleAtlassianResources')).toHaveLength(1)
})

test('jira_site and the other settings are used as given', {
  options: { jira_project_key: 'OPS', jira_issue_type: 'Story', jira_status: 'done', jira_site: 'gclitheroe.atlassian.net' },
}, async ($, on) => {
  const { calls } = stand(on, ALL)

  const ran = await $.tool.call(PR_CALL)

  // No lookup: jira_site names the site.
  expect(calls.map(c => c.tool)).toEqual(['createJiraIssue', 'executeRead', 'transitionJiraIssue'])
  expect(calls[0]?.args).toMatchObject({ cloudId: 'gclitheroe.atlassian.net', projectKey: 'OPS', issueType: 'Story' })
  // The status matches the transition's target status, case-insensitively.
  expect(calls[2]?.args).toMatchObject({ transitionId: '31' })
  expect(ran.context?.join('\n')).toContain('https://gclitheroe.atlassian.net/browse/DEMO-7')
})

test('with no project key, nothing is created and one notice is shown', async ($, on) => {
  const { calls, toasts } = stand(on, ALL)

  const ran = await $.tool.call(PR_CALL)
  await $.tool.call({ ...PR_CALL, title: 'Another' })

  expect(calls).toEqual([])
  expect(toasts).toHaveLength(1)
  expect(toasts[0]).toContain('jira_project_key')
  expect(ran.text).toBe(PR_TEXT)
  expect(ran.context).toBeUndefined()
})

test('a failed PR call creates nothing', { options: { jira_project_key: 'DEMO' } }, async ($, on) => {
  const { calls, toasts } = stand(on, ALL, { isError: true })

  const ran = await $.tool.call(PR_CALL)

  expect(ran.isError).toBe(true)
  expect(calls).toEqual([])
  expect(toasts).toEqual([])
})

test('several Atlassian sites and no jira_site: nothing is created', { options: { jira_project_key: 'DEMO' } }, async ($, on) => {
  const twoSites = {
    data: {
      resources: [
        { cloudId: 'a', url: 'https://one.atlassian.net', products: [{ id: 'jira' }] },
        { cloudId: 'b', url: 'https://two.atlassian.net', products: [{ id: 'jira' }] },
      ],
    },
  }
  const { calls, toasts } = stand(on, { ...ALL, getAccessibleAtlassianResources: twoSites })

  const ran = await $.tool.call(PR_CALL)

  expect(calls.map(c => c.tool)).toEqual(['getAccessibleAtlassianResources'])
  expect(toasts[0]).toContain('set jira_site')
  expect(ran.context).toBeUndefined()
})

test('no transition to the status: the item stays in its first status', { options: { jira_project_key: 'DEMO' } }, async ($, on) => {
  const noReview = {
    data: {
      transitions: [
        { id: '11', name: 'To Do', to: { name: 'To Do' } },
        { id: '31', name: 'Done', to: { name: 'Done' } },
      ],
    },
  }
  const { calls, toasts } = stand(on, { ...ALL, listJiraIssueTransitions: noReview })

  const ran = await $.tool.call(PR_CALL)

  expect(calls.map(c => c.tool)).not.toContain('transitionJiraIssue')
  expect(toasts[0]).toContain('created DEMO-7')
  expect(toasts[0]).toContain('no transition to "Review"')
  expect(ran.context?.join('\n')).toContain('left it in its first status')
})

test('a Jira error leaves the PR result as it was', { options: { jira_project_key: 'DEMO' } }, async ($, on) => {
  const { calls, toasts } = stand(on, {
    ...ALL,
    createJiraIssue: { error: true, message: 'Field issuetype is invalid', statusCode: 400 },
  })

  const ran = await $.tool.call(PR_CALL)

  expect(calls.map(c => c.tool)).toEqual(['getAccessibleAtlassianResources', 'createJiraIssue'])
  expect(toasts[0]).toContain("couldn't create a Jira work item")
  expect(toasts[0]).toContain('Field issuetype is invalid')
  expect(ran.text).toBe(PR_TEXT)
  expect(ran.context).toBeUndefined()
})
