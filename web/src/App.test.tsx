import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { App, CommentBox, ReviewDetail } from './App'
import type { PanelInfo } from './api'
import { I18nProvider, LANGUAGE_STORAGE_KEY } from './i18n'

const bootstrap = {
  csrfToken: 'csrf', selectedRepository: 'E:\\repo', selectedRepositoryIdentity: 'repo-1', repositories: [{ identity: 'repo-1', name: 'repo', path: 'E:\\repo', branch: 'main', head: 'abcdef123456', headShort: 'abcdef12', commitSubject: 'Initial', dirty: false, hasConfig: true }],
  roborev: { installed: true, version: 'v0.64.0', status: { running: true }, capabilities: { reviewEnqueueApi: true, frozenRerunApi: true, jobListApi: true, jobOutputStatusApi: true, panelReview: true, fixCommand: true, panelMembersApi: true, promptLazyLoad: true, rawLog: true, streamEvents: true } },
}
const jobs = [{ id: 42, git_ref: 'abcdef123456', commit_subject: 'Initial dashboard checkpoint', branch: 'main', status: 'done', verdict: 'F', job_type: 'synthesis', panel_role: 'synthesis', panel_name: 'dashboard_cross', panel_run_uuid: '986d3bfa-05da-4621-aeca-66f493a19ea3', closed: false, enqueued_at: '2026-08-17T00:00:00Z', started_at: '2026-08-17T00:00:05Z', finished_at: '2026-08-17T00:00:30Z', review: { output: '# Finding\nActionable issue' } }]
async function openFixRefine() { await userEvent.click(screen.getByRole('button', { name: 'Fix & Refine' })) }
const defaultPanel: PanelInfo = { name: 'reviewers', kind: 'panel', members: ['codex', 'claude-code'], origin: 'global', valid: true }
const setupReady = { status: 'ready', phase: 'ready', operationActive: false, installed: true, binaryPath: 'C:\\Tools\\roborev.exe', version: 'v0.64.0', recommendedVersion: 'v0.64.0', compatible: true, panelRefineCompatible: true, daemonRunning: true, blocked: false, retryable: false, installAvailable: false, canInstall: false, canRetry: false, canStartDaemon: false, architecture: 'windows-amd64', sourceURL: 'https://github.com/kenn-io/roborev/releases/tag/v0.64.0', sourceLabel: 'Official GitHub release', assetName: 'roborev-v0.64.0-windows-amd64.zip', sha256: '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef', targetPath: 'C:\\Tools\\roborev.exe', downloadedBytes: 1200, totalBytes: 1200 }
const setupAgents = { repositoryIdentity: 'repo-1', roborevVersion: 'v0.64.0', configured: [{ name: 'Claude Code', agent: 'claude-code', model: 'sonnet', reasoning: 'medium', command: 'claude', commandSource: 'config', detected: true, detectionReason: 'Found in PATH', resolvedPath: 'C:\\Tools\\claude.exe', health: 'not_checked' }] }
const setupAgentsB = { repositoryIdentity: 'repo-2', roborevVersion: 'v0.64.0', configured: [{ name: 'Gemini CLI', agent: 'gemini', model: 'pro', reasoning: 'high', command: 'gemini', commandSource: 'config', detected: true, detectionReason: 'Found in PATH', resolvedPath: 'C:\\Tools\\gemini.exe', health: 'not_checked' }] }
const setupRuntime = { daemonRunning: true, operationActive: false, queueState: 'processing', canPause: true, canResume: false, canStart: false, canStop: true, canRestart: true, daemonCommands: { start: { executable: 'C:\\Tools\\roborev.exe', args: ['daemon', 'start'] }, stop: { executable: 'C:\\Tools\\roborev.exe', args: ['daemon', 'stop'] }, restart: { executable: 'C:\\Tools\\roborev.exe', args: ['daemon', 'restart'] } } }
const refineOptions = { executable: 'C:\\Tools\\roborev.exe', args: ['fix', '--agent', 'codex', '--quiet', '<synthesisJobID>'], allowedFixAgents: ['codex'], defaultFixAgent: 'codex', defaultMaxFixAttempts: 3, minFixAttempts: 1, maxFixAttempts: 10 }
function refineRun(overrides: Record<string, unknown> = {}) {
  return { id: 'refine-1', repositoryIdentity: 'repo-1', repositoryPath: 'E:\\repo', branch: 'main', iterationPanel: 'dashboard_cross', fixAgent: 'codex', maxFixAttempts: 3, fixAttempts: 0, phase: 'reviewing', startingHead: 'abcdef123456', currentHead: 'abcdef123456', currentReviewJobId: 42, iterations: [], command: refineOptions, ...overrides }
}
function refineEnvelope(runs: unknown[] = [refineRun()], options = refineOptions) { return { runs, options } }

function response(value: unknown, status = 200) { return Promise.resolve(new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } })) }
function memberListPath(runUUID: string, parentJobId = 42, repositoryIdentity = 'repo-1') {
  return `/api/panels/${encodeURIComponent(runUUID)}/members?parentJobId=${parentJobId}&repositoryIdentity=${encodeURIComponent(repositoryIdentity)}`
}
function memberDetailPath(runUUID: string, memberId: number, parentJobId = 42, repositoryIdentity = 'repo-1') {
  return `/api/panels/${encodeURIComponent(runUUID)}/members/${memberId}?parentJobId=${parentJobId}&repositoryIdentity=${encodeURIComponent(repositoryIdentity)}`
}
function memberShow(member: { id: number; panel_run_uuid: string; panel_role: string }, output: string, repoPath = 'E:\\repo') {
  return { job_id: member.id, job: { id: member.id, repo_path: repoPath, panel_name: 'dashboard_cross', panel_run_uuid: member.panel_run_uuid, panel_role: member.panel_role }, review: { output } }
}
function panelConfiguration(repositoryIdentity = 'repo-1', panels: PanelInfo[] = [defaultPanel], selectedPanel = defaultPanel.name, selectionRequired = false, error?: string) {
  return { repositoryIdentity, panels, selectedPanel, selectionRequired, ...(error ? { error } : {}) }
}
function withPanels(handler: (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>, panelHandler?: (repoPath: string) => Promise<Response>) {
  return vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input)
    if (path.startsWith('/api/repos/panels?')) {
      const repoPath = new URL(path, 'http://localhost').searchParams.get('repo')
      if (panelHandler) return panelHandler(repoPath ?? '')
      const repositoryIdentity = repoPath === 'E:\\other' ? 'repo-2' : 'repo-1'
      return response(panelConfiguration(repositoryIdentity))
    }
    return handler(input, init)
  })
}
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => { resolve = resolvePromise; reject = rejectPromise })
  return { promise, resolve, reject }
}

type FakeEventMessage = { data: string }
class FakeEventSource {
  static instances: FakeEventSource[] = []
  readonly url: string
  onopen: (() => void) | null = null
  onmessage: ((event: FakeEventMessage) => void) | null = null
  onerror: (() => void) | null = null
  closed = false

  constructor(url: string) {
    this.url = url
    FakeEventSource.instances.push(this)
  }

  close() { this.closed = true }
  open() { this.onopen?.() }
  emit(data: unknown) { this.onmessage?.({ data: JSON.stringify(data) }) }
  fail() { this.onerror?.() }
}

afterEach(() => { cleanup(); FakeEventSource.instances = []; vi.useRealTimers(); vi.restoreAllMocks(); window.localStorage.setItem(LANGUAGE_STORAGE_KEY, 'en') })

describe('App review workflow', () => {
  it('opens the Tasks workspace from the sidebar without Soon or roadmap placeholder', async () => {
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/reviews?')) return response([])
      return response({ success: true })
    }))
    render(<App />)
    const tasksButton = await screen.findByRole('button', { name: 'Tasks' })
    expect(within(tasksButton).queryByText('Soon')).not.toBeInTheDocument()
    await userEvent.click(tasksButton)
    expect(await screen.findByRole('heading', { name: 'Custom Task' })).toBeInTheDocument()
    expect(screen.queryByText('Checkpoint roadmap')).not.toBeInTheDocument()
    expect(screen.queryByText('Read-only')).not.toBeInTheDocument()
  })

  it('keeps Review HEAD distinct from frozen rerun', async () => {
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/reviews?')) return response(jobs)
      if (path === '/api/reviews/42') return response(jobs[0])
      return response({ success: true })
    }))
    render(<App />)
    await waitFor(() => expect(screen.getByRole('button', { name: 'Review HEAD' })).toBeEnabled())
    await userEvent.click(await screen.findByText(/#42/))
    expect(await screen.findByRole('heading', { name: 'Initial dashboard checkpoint' })).toBeInTheDocument()
    expect(await screen.findByRole('button', { name: /Rerun same target\/config/ })).toBeInTheDocument()
  })

  it('does not offer frozen rerun for a custom task result', async () => {
    const taskJob = { ...jobs[0], id: 43, job_id: 43, job_type: 'task', git_ref: 'run', uuid: '00000000-0000-0000-0000-000000000043', status: 'done' }
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/reviews?')) return response([taskJob])
      if (path === '/api/reviews/43') return response(taskJob)
      return response({ success: true })
    }))
    render(<App />)
    await userEvent.click(await screen.findByText(/#43/))
    expect(await screen.findByRole('heading', { name: 'Initial dashboard checkpoint' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Rerun same target\/config/ })).not.toBeInTheDocument()
  })

  it('renders finding state and safe review text', async () => {
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => String(input) === '/api/bootstrap' ? response(bootstrap) : response(jobs)))
    render(<App />)
    expect((await screen.findAllByText('Finding')).length).toBeGreaterThan(0)
  })

  it('searches real commit subjects and normalized verdict labels and renders elapsed timestamps', async () => {
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => String(input) === '/api/bootstrap' ? response(bootstrap) : response(jobs)))
    render(<App />)
    const search = await screen.findByRole('textbox', { name: 'Search reviews' })
    expect(await screen.findByText('25s')).toBeInTheDocument()
    await userEvent.type(search, 'Initial dashboard')
    expect(screen.getByText(/#42/)).toBeInTheDocument()
    await userEvent.clear(search)
    await userEvent.type(search, 'Finding')
    expect(screen.getByText(/#42/)).toBeInTheDocument()
  })

  it('lazy-loads and renders panel member agents', async () => {
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/reviews?')) return response(jobs)
      if (path === '/api/reviews/42') return response(jobs[0])
      if (path.includes('/api/panels/')) return response({ jobs: [
        { id: 43, panel_role: 'member', panel_run_uuid: jobs[0].panel_run_uuid, panel_member_index: 0, status: 'done', verdict: 'F', agent: 'codex' },
        { id: 44, panel_role: 'member', panel_run_uuid: jobs[0].panel_run_uuid, panel_member_index: 1, status: 'done', verdict: 'P', agent: 'claude-code' },
        { id: 45, panel_role: 'member', panel_run_uuid: jobs[0].panel_run_uuid, panel_member_index: 2, status: 'running', agent: 'grok' },
      ] })
      return response({ success: true })
    }))
    render(<App />)
    await userEvent.click(await screen.findByText(/#42/))
    await userEvent.click(await screen.findByRole('button', { name: 'Panel members' }))
    expect(await screen.findByText('claude-code')).toBeInTheDocument()
    expect(screen.getByText('grok')).toBeInTheDocument()
    expect(screen.getByText('codex').closest('article')).toHaveTextContent('Finding')
    expect(screen.getByText('claude-code').closest('article')).toHaveTextContent('Pass')
  })

  it('shows a running panel tree with member metadata and lazy member output detail', async () => {
    const parent = { ...jobs[0], status: 'running', verdict: undefined, review: undefined, panel_run_uuid: 'panel-running-1' }
    const members = [
      { id: 43, panel_role: 'member', panel_run_uuid: 'panel-running-1', panel_member_index: 0, panel_member_name: 'Security reviewer', status: 'done', verdict: 'P', agent: 'codex', model: 'o3', duration_ms: 1250 },
      { id: 44, panel_role: 'member', panel_run_uuid: 'panel-running-1', panel_member_index: 1, panel_member_name: 'UX reviewer', status: 'done', verdict: 'F', agent: 'claude-code', model: 'sonnet', duration_ms: 2500 },
      { id: 45, panel_role: 'member', panel_run_uuid: 'panel-running-1', panel_member_index: 2, panel_member_name: 'Quality reviewer', status: 'running', agent: 'grok', model: 'fast' },
    ]
    const memberDetailCalls: string[] = []
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/reviews?')) return response([parent])
      if (path === '/api/reviews/42') return response(parent)
      if (path.startsWith('/api/panels/panel-running-1/members?')) return response({ jobs: members })
      if (path === memberDetailPath('panel-running-1', 43)) { memberDetailCalls.push(path); return response(memberShow(members[0], '# Member output\nLazy-loaded member Markdown.')) }
      return response({ success: true })
    }))
    render(<App />)
    await userEvent.click(await screen.findByText(/#42/))
    expect(await screen.findByRole('button', { name: 'Panel members' })).toHaveClass('active')
    expect(await screen.findByText('Security reviewer')).toBeInTheDocument()
    expect(screen.getByText('2 / 3')).toBeInTheDocument()
    expect(screen.getByText('grok')).toBeInTheDocument()
    expect(screen.getByText('fast')).toBeInTheDocument()
    expect(screen.getAllByText('Running').length).toBeGreaterThan(0)
    expect(memberDetailCalls).toHaveLength(0)

    await userEvent.click(screen.getByRole('button', { name: /Security reviewer/ }))
    expect(await screen.findByText('Lazy-loaded member Markdown.')).toBeInTheDocument()
    expect(memberDetailCalls).toEqual([memberDetailPath('panel-running-1', 43)])
    expect(screen.getByRole('button', { name: 'Back to panel synthesis' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Rerun same target/ })).not.toBeInTheDocument()
  })

  it('refetches selected member detail when polling observes terminal completion', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const parent = { ...jobs[0], status: 'running', verdict: undefined, review: undefined, panel_run_uuid: 'panel-running-2' }
    const runningMember = { id: 43, panel_role: 'member', panel_run_uuid: 'panel-running-2', panel_member_index: 0, panel_member_name: 'Security reviewer', status: 'running', agent: 'codex', model: 'o3' }
    const terminalMember = { ...runningMember, status: 'done', verdict: 'P', duration_ms: 2100 }
    let memberPollCalls = 0
    let memberDetailCalls = 0
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/reviews?')) return response([parent])
      if (path === '/api/reviews/42') return response(parent)
      if (path.startsWith('/api/panels/panel-running-2/members?')) return response({ jobs: [memberPollCalls++ === 0 ? runningMember : terminalMember] })
      if (path === memberDetailPath('panel-running-2', 43)) { const member = memberDetailCalls++ === 0 ? runningMember : terminalMember; return response(memberShow(member, memberDetailCalls === 1 ? 'Running member output' : 'Terminal member output')) }
      return response({ success: true })
    }))
    render(<App />)
    await userEvent.click(await screen.findByText(/#42/))
    await userEvent.click(await screen.findByRole('button', { name: /Security reviewer/ }))
    expect(await screen.findByText('Running member output')).toBeInTheDocument()
    expect(memberDetailCalls).toBe(1)
    await vi.advanceTimersByTimeAsync(4100)
    expect(await screen.findByText('Terminal member output')).toBeInTheDocument()
    expect(memberDetailCalls).toBe(2)
    expect(screen.getByText('2.1s')).toBeInTheDocument()
  })

  it('clears a selected member detail when the member disappears from the current panel run', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const parent = { ...jobs[0], status: 'running', verdict: undefined, review: undefined, panel_run_uuid: 'panel-running-3' }
    const member = { id: 43, panel_role: 'member', panel_run_uuid: 'panel-running-3', panel_member_index: 0, panel_member_name: 'Security reviewer', status: 'running', agent: 'codex', model: 'o3' }
    const pendingDetail = deferred<Response>()
    let memberPollCalls = 0
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/reviews?')) return response([parent])
      if (path === '/api/reviews/42') return response(parent)
      if (path.startsWith('/api/panels/panel-running-3/members?')) return response({ jobs: memberPollCalls++ === 0 ? [member] : [] })
      if (path === memberDetailPath('panel-running-3', 43)) return pendingDetail.promise
      return response({ success: true })
    }))
    render(<App />)
    await userEvent.click(await screen.findByText(/#42/))
    await userEvent.click(await screen.findByRole('button', { name: /Security reviewer/ }))
    expect(await screen.findByRole('button', { name: 'Back to panel synthesis' })).toBeInTheDocument()
    await vi.advanceTimersByTimeAsync(4100)
    expect(screen.queryByRole('button', { name: 'Back to panel synthesis' })).not.toBeInTheDocument()
    pendingDetail.resolve(new Response(JSON.stringify({ ...member, output: 'Stale member output' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(screen.queryByText('Stale member output')).not.toBeInTheDocument()
  })

  it('ignores late member detail responses after a rapid member switch', async () => {
    const parent = { ...jobs[0], status: 'running', verdict: undefined, review: undefined, panel_run_uuid: 'panel-running-switch' }
    const firstMember = { id: 43, panel_role: 'member', panel_run_uuid: parent.panel_run_uuid, panel_member_index: 0, panel_member_name: 'Security reviewer', status: 'running', agent: 'codex', model: 'o3' }
    const secondMember = { id: 44, panel_role: 'member', panel_run_uuid: parent.panel_run_uuid, panel_member_index: 1, panel_member_name: 'UX reviewer', status: 'running', agent: 'claude-code', model: 'sonnet' }
    const firstDetail = deferred<Response>()
    const secondDetail = deferred<Response>()
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/reviews/42') return response(parent)
      if (path.startsWith('/api/panels/panel-running-switch/members?')) return response({ jobs: [firstMember, secondMember] })
      if (path === memberDetailPath('panel-running-switch', 43)) return firstDetail.promise
      if (path === memberDetailPath('panel-running-switch', 44)) return secondDetail.promise
      return response({ success: true })
    }))
    const props = { job: parent, jobs: [parent], close: vi.fn(), mutate: vi.fn().mockResolvedValue(undefined), busy: false, canRerun: true, canLoadMembers: true, repoPath: 'E:\\repo', repoIdentity: 'repo-1', toast: vi.fn() }
    render(<I18nProvider><ReviewDetail {...props} /></I18nProvider>)
    await userEvent.click(await screen.findByRole('button', { name: /Security reviewer/ }))
    await userEvent.click(await screen.findByRole('button', { name: /UX reviewer/ }))
    firstDetail.resolve(new Response(JSON.stringify(memberShow(firstMember, 'Stale member output')), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    secondDetail.resolve(new Response(JSON.stringify(memberShow(secondMember, 'Fresh member output')), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    expect(await screen.findByText('Fresh member output')).toBeInTheDocument()
    expect(screen.queryByText('Stale member output')).not.toBeInTheDocument()
  })

  it('shows an inline error for a member detail identity mismatch without replacing it', async () => {
    const parent = { ...jobs[0], status: 'running', verdict: undefined, review: undefined, panel_run_uuid: 'panel-detail-mismatch' }
    const member = { id: 43, panel_role: 'member', panel_run_uuid: parent.panel_run_uuid, panel_member_index: 0, panel_member_name: 'Security reviewer', status: 'done', verdict: 'P', agent: 'codex' }
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/reviews/42') return response(parent)
      if (path === memberListPath('panel-detail-mismatch')) return response({ jobs: [member] })
      if (path === memberDetailPath('panel-detail-mismatch', 43)) return response(memberShow({ ...member, id: 44 }, 'Wrong member output'))
      return response({ success: true })
    }))
    const props = { job: parent, jobs: [parent], close: vi.fn(), mutate: vi.fn().mockResolvedValue(undefined), busy: false, canRerun: true, canLoadMembers: true, repoPath: 'E:\\repo', repoIdentity: 'repo-1', toast: vi.fn() }
    render(<I18nProvider><ReviewDetail {...props} /></I18nProvider>)
    await userEvent.click(await screen.findByRole('button', { name: /Security reviewer/ }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Panel member detail job identity mismatch')
    expect(screen.queryByText('Wrong member output')).not.toBeInTheDocument()
    expect(screen.queryByText('Review detail refresh failed')).not.toBeInTheDocument()
  })

  it('trusts backend repository authorization instead of comparing Windows path spellings', async () => {
    const parent = { ...jobs[0], status: 'running', verdict: undefined, review: undefined, panel_run_uuid: 'panel-detail-path' }
    const member = { id: 43, panel_role: 'member', panel_run_uuid: parent.panel_run_uuid, panel_member_index: 0, panel_member_name: 'Path-authorized reviewer', status: 'done', verdict: 'P', agent: 'codex' }
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/reviews/42') return response(parent)
      if (path === memberListPath(parent.panel_run_uuid)) return response({ jobs: [member] })
      if (path === memberDetailPath(parent.panel_run_uuid, 43)) return response(memberShow(member, 'Backend-authorized member output', 'e:/foo/../repo/'))
      return response({ success: true })
    }))
    const props = { job: parent, jobs: [parent], close: vi.fn(), mutate: vi.fn().mockResolvedValue(undefined), busy: false, canRerun: true, canLoadMembers: true, repoPath: 'E:\\repo', repoIdentity: 'repo-1', toast: vi.fn() }
    render(<I18nProvider><ReviewDetail {...props} /></I18nProvider>)
    await userEvent.click(await screen.findByRole('button', { name: /Path-authorized reviewer/ }))
    expect(await screen.findByText('Backend-authorized member output')).toBeInTheDocument()
  })

  it('keeps only positive exact member jobs from the scoped panel response', async () => {
    const parent = { ...jobs[0], status: 'running', verdict: undefined, review: undefined, panel_run_uuid: 'panel-member-contract' }
    const validMember = { id: 46, panel_role: 'member', panel_run_uuid: parent.panel_run_uuid, panel_member_index: 3, panel_member_name: 'Valid member', status: 'done', verdict: 'P', agent: 'codex' }
    const invalidMembers = [
      { id: 0, panel_role: 'member', panel_run_uuid: parent.panel_run_uuid, panel_member_name: 'Zero member' },
      { id: 47, panel_role: 'synthesis', panel_run_uuid: parent.panel_run_uuid, panel_member_name: 'Synthesis row' },
      { id: 48, panel_role: 'member', panel_member_name: 'Missing run member' },
      { id: 49, panel_role: 'member', panel_run_uuid: 'other-run', panel_member_name: 'Wrong run member' },
    ]
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/reviews/42') return response(parent)
      if (path === memberListPath('panel-member-contract')) return response({ jobs: [...invalidMembers, validMember] })
      return response({ success: true })
    }))
    const props = { job: parent, jobs: [parent], close: vi.fn(), mutate: vi.fn().mockResolvedValue(undefined), busy: false, canRerun: true, canLoadMembers: true, repoPath: 'E:\\repo', repoIdentity: 'repo-1', toast: vi.fn() }
    render(<I18nProvider><ReviewDetail {...props} /></I18nProvider>)
    expect(await screen.findByText('Valid member')).toBeInTheDocument()
    expect(screen.queryByText('Zero member')).not.toBeInTheDocument()
    expect(screen.queryByText('Synthesis row')).not.toBeInTheDocument()
    expect(screen.queryByText('Missing run member')).not.toBeInTheDocument()
    expect(screen.queryByText('Wrong run member')).not.toBeInTheDocument()
  })

  it('clears member tree and detail when repository identity changes', async () => {
    const parent = { ...jobs[0], status: 'running', verdict: undefined, review: undefined, panel_run_uuid: 'panel-running-repo-switch' }
    const member = { id: 43, panel_role: 'member', panel_run_uuid: parent.panel_run_uuid, panel_member_index: 0, panel_member_name: 'Security reviewer', status: 'running', agent: 'codex', model: 'o3' }
    const pendingDetail = deferred<Response>()
    let memberPollCalls = 0
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/reviews/42') return response(parent)
      if (path.startsWith('/api/panels/panel-running-repo-switch/members?')) return response({ jobs: memberPollCalls++ === 0 ? [member] : [] })
      if (path === memberDetailPath('panel-running-repo-switch', 43)) return pendingDetail.promise
      return response({ success: true })
    }))
    const props = { job: parent, jobs: [parent], close: vi.fn(), mutate: vi.fn().mockResolvedValue(undefined), busy: false, canRerun: true, canLoadMembers: true, repoPath: 'E:\\repo', repoIdentity: 'repo-1', toast: vi.fn() }
    const { rerender } = render(<I18nProvider><ReviewDetail {...props} /></I18nProvider>)
    await userEvent.click(await screen.findByRole('button', { name: /Security reviewer/ }))
    expect(screen.getByRole('button', { name: 'Back to panel synthesis' })).toBeInTheDocument()
    rerender(<I18nProvider><ReviewDetail {...props} repoIdentity="repo-2" /></I18nProvider>)
    expect(screen.queryByRole('button', { name: 'Back to panel synthesis' })).not.toBeInTheDocument()
    pendingDetail.resolve(new Response(JSON.stringify({ ...member, output: 'Stale repository member output' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(screen.queryByText('Stale repository member output')).not.toBeInTheDocument()
  })

  it('keeps the member skeleton when parent detail resolves before the member list', async () => {
    const parent = { ...jobs[0], status: 'running', verdict: undefined, review: undefined, panel_run_uuid: 'panel-deferred-a' }
    const parentDetail = deferred<Response>()
    const memberList = deferred<Response>()
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/reviews/42') return parentDetail.promise
      if (path === memberListPath('panel-deferred-a')) return memberList.promise
      return response({ success: true })
    }))
    const props = { job: parent, jobs: [parent], close: vi.fn(), mutate: vi.fn().mockResolvedValue(undefined), busy: false, canRerun: true, canLoadMembers: true, repoPath: 'E:\\repo', repoIdentity: 'repo-1', toast: vi.fn() }
    render(<I18nProvider><ReviewDetail {...props} /></I18nProvider>)
    expect(await screen.findByLabelText('Loading…')).toBeInTheDocument()
    parentDetail.resolve(new Response(JSON.stringify({ ...parent, output: 'Parent resolved first' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(document.querySelector('.member-list-loading')).toBeInTheDocument()
    expect(screen.queryByText('0 / 0')).not.toBeInTheDocument()
    expect(screen.queryByText('No member jobs')).not.toBeInTheDocument()
    const member = { id: 43, panel_role: 'member', panel_run_uuid: parent.panel_run_uuid, panel_member_index: 0, panel_member_name: 'Resolved member', status: 'done', verdict: 'P', agent: 'codex' }
    memberList.resolve(new Response(JSON.stringify({ jobs: [member] }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    expect(await screen.findByText('Resolved member')).toBeInTheDocument()
    expect(screen.getByText('1 / 1')).toBeInTheDocument()
    expect(document.querySelector('.member-list-loading')).not.toBeInTheDocument()
  })

  it('shows parent output immediately when leaving a pending member list for Review', async () => {
    const parent = { ...jobs[0], status: 'running', verdict: undefined, review: undefined, panel_run_uuid: 'panel-deferred-b' }
    const memberList = deferred<Response>()
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/reviews/42') return response({ ...parent, output: 'Parent review output' })
      if (path === memberListPath('panel-deferred-b')) return memberList.promise
      return response({ success: true })
    }))
    const props = { job: parent, jobs: [parent], close: vi.fn(), mutate: vi.fn().mockResolvedValue(undefined), busy: false, canRerun: true, canLoadMembers: true, repoPath: 'E:\\repo', repoIdentity: 'repo-1', toast: vi.fn() }
    render(<I18nProvider><ReviewDetail {...props} /></I18nProvider>)
    expect(await screen.findByLabelText('Loading…')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Review' }))
    expect(await screen.findByText('Parent review output')).toBeInTheDocument()
    expect(document.querySelector('.member-list-loading')).not.toBeInTheDocument()
  })

  it('drops old member loading and results when repository identity switches', async () => {
    const parent = { ...jobs[0], status: 'running', verdict: undefined, review: undefined, panel_run_uuid: 'panel-deferred-c' }
    const staleRefresh = deferred<Response>()
    const secondMembers = deferred<Response>()
    const staleMember = { id: 43, panel_role: 'member', panel_run_uuid: parent.panel_run_uuid, panel_member_index: 0, panel_member_name: 'Old repository member', status: 'done', verdict: 'P', agent: 'codex' }
    let firstRepositoryCalls = 0
    let secondRepositoryCalls = 0
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/reviews/42') return response(parent)
      if (path === memberListPath('panel-deferred-c', 42, 'repo-1')) return ++firstRepositoryCalls === 1 ? response({ jobs: [staleMember] }) : staleRefresh.promise
      if (path === memberListPath('panel-deferred-c', 42, 'repo-2')) { secondRepositoryCalls++; return secondMembers.promise }
      return response({ success: true })
    }))
    const props = { job: parent, jobs: [parent], close: vi.fn(), mutate: vi.fn().mockResolvedValue(undefined), busy: false, canRerun: true, canLoadMembers: true, repoPath: 'E:\\repo', repoIdentity: 'repo-1', toast: vi.fn() }
    const { rerender } = render(<I18nProvider><ReviewDetail {...props} /></I18nProvider>)
    expect(await screen.findByText('Old repository member')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Review' }))
    await userEvent.click(screen.getByRole('button', { name: 'Panel members' }))
    await waitFor(() => expect(firstRepositoryCalls).toBe(2))
    rerender(<I18nProvider><ReviewDetail {...props} repoIdentity="repo-2" /></I18nProvider>)
    await waitFor(() => expect(secondRepositoryCalls).toBe(1))
    expect(screen.queryByText('Old repository member')).not.toBeInTheDocument()
    expect(document.querySelector('.member-list-loading')).toBeInTheDocument()
    expect(screen.queryByText('0 / 0')).not.toBeInTheDocument()
    expect(screen.queryByText('No member jobs')).not.toBeInTheDocument()
    staleRefresh.resolve(new Response(JSON.stringify({ jobs: [staleMember] }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(screen.queryByText('Old repository member')).not.toBeInTheDocument()
    secondMembers.resolve(new Response(JSON.stringify({ jobs: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await waitFor(() => expect(screen.queryByLabelText('Loading…')).not.toBeInTheDocument())
    expect(screen.queryByText('Old repository member')).not.toBeInTheDocument()
  })

  it('starts a fresh member skeleton when the panel parent or run changes', async () => {
    const firstParent = { ...jobs[0], status: 'running', verdict: undefined, review: undefined, panel_run_uuid: 'panel-scope-a' }
    const secondParent = { ...firstParent, id: 99, panel_run_uuid: 'panel-scope-b' }
    const firstMembers = deferred<Response>()
    const secondMembers = deferred<Response>()
    const staleMember = { id: 43, panel_role: 'member', panel_run_uuid: firstParent.panel_run_uuid, panel_member_index: 0, panel_member_name: 'Old run member', status: 'done', verdict: 'P', agent: 'codex' }
    const freshMember = { id: 44, panel_role: 'member', panel_run_uuid: secondParent.panel_run_uuid, panel_member_index: 0, panel_member_name: 'New run member', status: 'running', agent: 'claude-code' }
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/reviews/42') return response(firstParent)
      if (path === '/api/reviews/99') return response(secondParent)
      if (path === memberListPath(firstParent.panel_run_uuid)) return firstMembers.promise
      if (path === memberListPath(secondParent.panel_run_uuid, 99)) return secondMembers.promise
      return response({ success: true })
    }))
    const props = { job: firstParent, jobs: [firstParent], close: vi.fn(), mutate: vi.fn().mockResolvedValue(undefined), busy: false, canRerun: true, canLoadMembers: true, repoPath: 'E:\\repo', repoIdentity: 'repo-1', toast: vi.fn() }
    const { rerender } = render(<I18nProvider><ReviewDetail {...props} /></I18nProvider>)
    expect(await screen.findByLabelText('Loading…')).toBeInTheDocument()
    rerender(<I18nProvider><ReviewDetail {...props} job={secondParent} jobs={[secondParent]} /></I18nProvider>)
    expect(document.querySelector('.member-list-loading')).toBeInTheDocument()
    firstMembers.resolve(new Response(JSON.stringify({ jobs: [staleMember] }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(screen.queryByText('Old run member')).not.toBeInTheDocument()
    secondMembers.resolve(new Response(JSON.stringify({ jobs: [freshMember] }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    expect(await screen.findByText('New run member')).toBeInTheDocument()
    expect(document.querySelector('.member-list-loading')).not.toBeInTheDocument()
  })

  it('keeps the existing member tree during a same-scope status refresh', async () => {
    const runningParent = { ...jobs[0], status: 'running', verdict: undefined, review: undefined, panel_run_uuid: 'panel-scope-status' }
    const terminalParent = { ...runningParent, status: 'done', verdict: 'P' }
    const runningMember = { id: 43, panel_role: 'member', panel_run_uuid: runningParent.panel_run_uuid, panel_member_index: 0, panel_member_name: 'Stable member', status: 'running', agent: 'codex' }
    const terminalMember = { ...runningMember, status: 'done', verdict: 'P' }
    const terminalRefresh = deferred<Response>()
    let memberCalls = 0
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/reviews/42') return response(runningParent)
      if (path === memberListPath(runningParent.panel_run_uuid)) return ++memberCalls === 1 ? response({ jobs: [runningMember] }) : terminalRefresh.promise
      return response({ success: true })
    }))
    const props = { job: runningParent, jobs: [runningParent], close: vi.fn(), mutate: vi.fn().mockResolvedValue(undefined), busy: false, canRerun: true, canLoadMembers: true, repoPath: 'E:\\repo', repoIdentity: 'repo-1', toast: vi.fn() }
    const { rerender } = render(<I18nProvider><ReviewDetail {...props} /></I18nProvider>)
    expect(await screen.findByText('Stable member')).toBeInTheDocument()
    rerender(<I18nProvider><ReviewDetail {...props} job={terminalParent} jobs={[terminalParent]} /></I18nProvider>)
    await waitFor(() => expect(memberCalls).toBe(2))
    expect(screen.getByText('Stable member')).toBeInTheDocument()
    expect(document.querySelector('.member-list-loading')).not.toBeInTheDocument()
    terminalRefresh.resolve(new Response(JSON.stringify({ jobs: [terminalMember] }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    expect(await screen.findByText('Pass')).toBeInTheDocument()
  })

  it('copies and opens a finding member fix prompt across a routine member refresh', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const clipboard = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue(undefined)
    const slowOpen = deferred<Response>()
    const calls: Array<{ path: string; method: string }> = []
    let promptCalls = 0
    let memberCalls = 0
    const members = { jobs: [
      { id: 43, panel_role: 'member', panel_run_uuid: jobs[0].panel_run_uuid, panel_member_index: 0, status: 'done', verdict: 'F', agent: 'codex' },
      { id: 44, panel_role: 'member', panel_run_uuid: jobs[0].panel_run_uuid, panel_member_index: 1, status: 'done', verdict: 'P', agent: 'claude-code' },
      { id: 45, panel_role: 'member', panel_run_uuid: jobs[0].panel_run_uuid, panel_member_index: 2, status: 'running', agent: 'grok' },
    ] }
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      calls.push({ path, method: init?.method ?? 'GET' })
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/reviews?')) return response(jobs)
      if (path === '/api/reviews/42') return response(jobs[0])
      if (path.includes('/api/panels/')) { memberCalls++; return response(members) }
      if (path.startsWith('/api/reviews/43/fix-prompt?')) {
        promptCalls++
        return promptCalls === 1 ? response({ text: 'Member fix prompt', jobId: 43, stale: false }) : slowOpen.promise
      }
      return response({ success: true })
    }))
    render(<App />)
    await userEvent.click(await screen.findByText(/#42/))
    await userEvent.click(await screen.findByRole('button', { name: 'Panel members' }))
    const findingRow = (await screen.findByText('codex')).closest('article')!
    expect(within(findingRow).getByRole('button', { name: 'Copy Fix Prompt' })).toBeInTheDocument()
    expect(within(findingRow).getByRole('button', { name: 'Open Fix Prompt' })).toBeInTheDocument()
    expect(within(screen.getByText('claude-code').closest('article')!).queryByRole('button', { name: /Fix Prompt/ })).not.toBeInTheDocument()
    expect(within(screen.getByText('grok').closest('article')!).queryByRole('button', { name: /Fix Prompt/ })).not.toBeInTheDocument()

    await userEvent.click(within(findingRow).getByRole('button', { name: 'Copy Fix Prompt' }))
    expect(await screen.findByText('Fix prompt for review #43 copied.')).toBeInTheDocument()
    expect(clipboard).toHaveBeenCalledWith('Member fix prompt')
    await userEvent.click(within(screen.getByText('codex').closest('article')!).getByRole('button', { name: 'Open Fix Prompt' }))
    await vi.advanceTimersByTimeAsync(4100)
    expect(memberCalls).toBeGreaterThan(1)
    slowOpen.resolve(new Response(JSON.stringify({ text: 'Slow member prompt', jobId: 43, stale: false }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
    expect(screen.getByText('Finding · Review #43')).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: 'Fix prompt' })).toHaveValue('Slow member prompt')
    const promptPath = `/api/reviews/43/fix-prompt?repo=${encodeURIComponent(bootstrap.selectedRepository)}`
    expect(calls.filter(call => call.path === promptPath)).toHaveLength(2)
    expect(calls.filter(call => call.method !== 'GET')).toHaveLength(0)
  })

  it('ignores a pending member fix prompt when the member target disappears', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const prompt = deferred<Response>()
    let memberCalls = 0
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/reviews?')) return response(jobs)
      if (path === '/api/reviews/42') return response(jobs[0])
      if (path.includes('/api/panels/')) {
        memberCalls++
        return response({ jobs: memberCalls === 1
          ? [{ id: 43, panel_role: 'member', panel_run_uuid: jobs[0].panel_run_uuid, status: 'done', verdict: 'F', agent: 'codex' }, { id: 45, panel_role: 'member', panel_run_uuid: jobs[0].panel_run_uuid, status: 'running', agent: 'grok' }]
          : [{ id: 45, panel_role: 'member', panel_run_uuid: jobs[0].panel_run_uuid, status: 'running', agent: 'grok' }] })
      }
      if (path.startsWith('/api/reviews/43/fix-prompt?')) return prompt.promise
      return response({ success: true })
    }))
    render(<App />)
    await userEvent.click(await screen.findByText(/#42/))
    await userEvent.click(await screen.findByRole('button', { name: 'Panel members' }))
    const findingRow = (await screen.findByText('codex')).closest('article')!
    await userEvent.click(within(findingRow).getByRole('button', { name: 'Open Fix Prompt' }))
    await vi.advanceTimersByTimeAsync(4100)
    await waitFor(() => expect(screen.queryByText('codex')).not.toBeInTheDocument())
    prompt.resolve(new Response(JSON.stringify({ text: 'Removed member prompt', jobId: 43, stale: false }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(screen.queryByText('Removed member prompt')).not.toBeInTheDocument()
  })

  it('renders panel member refresh errors inline', async () => {
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/reviews?')) return response(jobs)
      if (path === '/api/reviews/42') return response(jobs[0])
      if (path.includes('/api/panels/')) return response({ error: 'daemon unavailable' }, 502)
      if (path.startsWith('/api/panel-refines?')) return response(refineEnvelope([]))
      return response({ success: true })
    }))
    render(<App />)
    await userEvent.click(await screen.findByText(/#42/))
    await userEvent.click(await screen.findByRole('button', { name: 'Panel members' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('daemon unavailable')
  })

  it('keeps the list job ID after loading a real show response', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (init?.method === 'POST') { calls.push(path); return response({ success: true }) }
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/reviews?')) return response(jobs)
      if (path === '/api/reviews/42') return response({ id: 7, job_id: 42, output: '# Result', closed: false, verdict: 'F' })
      return response({ success: true })
    }))
    render(<App />)
    await userEvent.click(await screen.findByText(/#42/))
    await userEvent.click(await screen.findByRole('button', { name: 'Close' }))
    await waitFor(() => expect(calls).toContain('/api/reviews/42/close'))
  })

  it('reconciles a selected running job when polling reaches a terminal state', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    let listCalls = 0
    const running = { ...jobs[0], status: 'running', verdict: undefined, review: undefined }
    const done = { ...jobs[0], status: 'done', verdict: 'P', review: undefined }
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/reviews?')) { listCalls++; return response(listCalls === 1 ? [running] : [done]) }
      if (path === '/api/reviews/42') return response(listCalls === 1 ? running : { ...done, output: '# Completed review' })
      return response({ success: true })
    }))
    render(<App />)
    fireEvent.click(await screen.findByText(/#42/))
    expect(await screen.findByRole('button', { name: 'Cancel' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Comments' }))
    fireEvent.change(screen.getByLabelText('Comment'), { target: { value: 'keep this draft' } })
    await vi.advanceTimersByTimeAsync(4100)
    expect((await screen.findAllByText('Pass')).length).toBeGreaterThan(0)
    expect(screen.queryByRole('button', { name: 'Cancel' })).not.toBeInTheDocument()
    expect(screen.getByLabelText('Comment')).toHaveValue('keep this draft')
    fireEvent.click(screen.getByRole('button', { name: 'Review' }))
    expect(await screen.findByText('Completed review')).toBeInTheDocument()
  })

  it('hands detail loading ownership to terminal reconciliation and ignores the late initial response', async () => {
    const initialDetail = deferred<Response>()
    const reconciledDetail = deferred<Response>()
    let detailCalls = 0
    const running = { ...jobs[0], status: 'running', verdict: undefined, review: undefined }
    const terminal = { ...jobs[0], status: 'done', verdict: 'P', review: undefined }
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/reviews/42') return ++detailCalls === 1 ? initialDetail.promise : reconciledDetail.promise
      return response({ success: true })
    }))
    const props = { job: running, jobs: [running], close: vi.fn(), mutate: vi.fn().mockResolvedValue(undefined), busy: false, canRerun: true, canLoadMembers: false, repoPath: 'E:\\repo', repoIdentity: 'repo-1', toast: vi.fn() }
    const { rerender } = render(<I18nProvider><ReviewDetail {...props} /></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Review' }))
    expect(document.querySelector('.skeleton.tall')).toBeInTheDocument()
    rerender(<I18nProvider><ReviewDetail {...props} job={terminal} jobs={[terminal]} /></I18nProvider>)
    await waitFor(() => expect(detailCalls).toBe(2))
    reconciledDetail.resolve(new Response(JSON.stringify({ ...terminal, output: 'Reconciled terminal output' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    expect(await screen.findByText('Reconciled terminal output')).toBeInTheDocument()
    expect(document.querySelector('.skeleton.tall')).not.toBeInTheDocument()
    initialDetail.resolve(new Response(JSON.stringify({ ...running, output: 'Late initial output' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(screen.queryByText('Late initial output')).not.toBeInTheDocument()
    expect(screen.getByText('Reconciled terminal output')).toBeInTheDocument()
  })

  it('ends detail loading and shows an error after terminal reconciliation retries fail', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const initialDetail = deferred<Response>()
    const firstRetry = deferred<Response>()
    const finalRetry = deferred<Response>()
    let detailCalls = 0
    const running = { ...jobs[0], status: 'running', verdict: undefined, review: undefined }
    const terminal = { ...jobs[0], status: 'done', verdict: 'P', review: undefined }
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/reviews/42') {
        detailCalls++
        return detailCalls === 1 ? initialDetail.promise : detailCalls === 2 ? firstRetry.promise : finalRetry.promise
      }
      return response({ success: true })
    }))
    const props = { job: running, jobs: [running], close: vi.fn(), mutate: vi.fn().mockResolvedValue(undefined), busy: false, canRerun: true, canLoadMembers: false, repoPath: 'E:\\repo', repoIdentity: 'repo-1', toast: vi.fn() }
    const { rerender } = render(<I18nProvider><ReviewDetail {...props} /></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Review' }))
    rerender(<I18nProvider><ReviewDetail {...props} job={terminal} jobs={[terminal]} /></I18nProvider>)
    await waitFor(() => expect(detailCalls).toBe(2))
    firstRetry.reject(new Error('first reconciliation failure'))
    await vi.advanceTimersByTimeAsync(1200)
    await waitFor(() => expect(detailCalls).toBe(3))
    expect(document.querySelector('.skeleton.tall')).toBeInTheDocument()
    finalRetry.reject(new Error('final reconciliation failure'))
    expect(await screen.findByRole('alert')).toHaveTextContent('final reconciliation failure')
    expect(document.querySelector('.skeleton.tall')).not.toBeInTheDocument()
    initialDetail.resolve(new Response(JSON.stringify({ ...running, output: 'Late initial output' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await vi.runOnlyPendingTimersAsync()
    expect(screen.queryByText('Late initial output')).not.toBeInTheDocument()
  })

  it('hands log loading ownership to terminal refresh and ignores a late initial log', async () => {
    const initialLog = deferred<Response>()
    const terminalLog = deferred<Response>()
    let logCalls = 0
    const running = { ...jobs[0], status: 'running', verdict: undefined, review: undefined }
    const terminal = { ...jobs[0], status: 'done', verdict: 'P', review: undefined }
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/reviews/42') return response(running)
      if (path === '/api/reviews/42/log') return ++logCalls === 1 ? initialLog.promise : terminalLog.promise
      return response({ success: true })
    }))
    const props = { job: running, jobs: [running], close: vi.fn(), mutate: vi.fn().mockResolvedValue(undefined), busy: false, canRerun: true, canLoadMembers: false, repoPath: 'E:\\repo', repoIdentity: 'repo-1', toast: vi.fn() }
    const { rerender } = render(<I18nProvider><ReviewDetail {...props} /></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Log' }))
    await waitFor(() => expect(logCalls).toBe(1))
    expect(document.querySelector('.skeleton.tall')).toBeInTheDocument()
    rerender(<I18nProvider><ReviewDetail {...props} job={terminal} jobs={[terminal]} /></I18nProvider>)
    await waitFor(() => expect(logCalls).toBe(2))
    terminalLog.resolve(new Response(JSON.stringify({ text: 'Terminal log' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    expect(await screen.findByText('Terminal log')).toBeInTheDocument()
    expect(document.querySelector('.skeleton.tall')).not.toBeInTheDocument()
    initialLog.resolve(new Response(JSON.stringify({ text: 'Late initial log' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(screen.queryByText('Late initial log')).not.toBeInTheDocument()
    expect(screen.getByText('Terminal log')).toBeInTheDocument()
  })

  it('ends log loading and shows retry after terminal refresh fails over a pending initial log', async () => {
    const initialLog = deferred<Response>()
    const terminalLog = deferred<Response>()
    let logCalls = 0
    const running = { ...jobs[0], status: 'running', verdict: undefined, review: undefined }
    const terminal = { ...jobs[0], status: 'done', verdict: 'P', review: undefined }
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/reviews/42') return response(running)
      if (path === '/api/reviews/42/log') return ++logCalls === 1 ? initialLog.promise : terminalLog.promise
      return response({ success: true })
    }))
    const props = { job: running, jobs: [running], close: vi.fn(), mutate: vi.fn().mockResolvedValue(undefined), busy: false, canRerun: true, canLoadMembers: false, repoPath: 'E:\\repo', repoIdentity: 'repo-1', toast: vi.fn() }
    const { rerender } = render(<I18nProvider><ReviewDetail {...props} /></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Log' }))
    await waitFor(() => expect(logCalls).toBe(1))
    expect(document.querySelector('.skeleton.tall')).toBeInTheDocument()
    rerender(<I18nProvider><ReviewDetail {...props} job={terminal} jobs={[terminal]} /></I18nProvider>)
    await waitFor(() => expect(logCalls).toBe(2))
    terminalLog.reject(new Error('terminal log unavailable'))
    expect(await screen.findByRole('alert')).toHaveTextContent('terminal log unavailable')
    expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument()
    expect(document.querySelector('.skeleton.tall')).not.toBeInTheDocument()
    initialLog.resolve(new Response(JSON.stringify({ text: 'Late initial log' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(screen.queryByText('Late initial log')).not.toBeInTheDocument()
  })

  it('surfaces a terminal log refresh error with retry after an initial log load', async () => {
    const terminalLogFailure = deferred<Response>()
    const retryLog = deferred<Response>()
    let logCalls = 0
    const running = { ...jobs[0], status: 'running', verdict: undefined, review: undefined }
    const terminal = { ...jobs[0], status: 'done', verdict: 'P', review: undefined }
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/reviews/42') return response(running)
      if (path === '/api/reviews/42/log') return ++logCalls === 1 ? response({ text: 'Initial log' }) : logCalls === 2 ? terminalLogFailure.promise : retryLog.promise
      return response({ success: true })
    }))
    const props = { job: running, jobs: [running], close: vi.fn(), mutate: vi.fn().mockResolvedValue(undefined), busy: false, canRerun: true, canLoadMembers: false, repoPath: 'E:\\repo', repoIdentity: 'repo-1', toast: vi.fn() }
    const { rerender } = render(<I18nProvider><ReviewDetail {...props} /></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Log' }))
    expect(await screen.findByText('Initial log')).toBeInTheDocument()
    rerender(<I18nProvider><ReviewDetail {...props} job={terminal} jobs={[terminal]} /></I18nProvider>)
    await waitFor(() => expect(logCalls).toBe(2))
    terminalLogFailure.reject(new Error('terminal log unavailable'))
    expect(await screen.findByRole('alert')).toHaveTextContent('terminal log unavailable')
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
    await waitFor(() => expect(logCalls).toBe(3))
    retryLog.resolve(new Response(JSON.stringify({ text: 'Refreshed terminal log' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    expect(await screen.findByText('Refreshed terminal log')).toBeInTheDocument()
    expect(screen.queryByText('Initial log')).not.toBeInTheDocument()
  })

  it('disables Review HEAD when the enqueue capability is unavailable', async () => {
    const unsupported = { ...bootstrap, roborev: { ...bootstrap.roborev, capabilities: { ...bootstrap.roborev.capabilities, reviewEnqueueApi: false } } }
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => String(input) === '/api/bootstrap' ? response(unsupported) : response([])))
    render(<App />)
    expect(await screen.findByRole('button', { name: 'Review HEAD' })).toBeDisabled()
    expect(await screen.findByText('RoboRev upgrade required')).toBeInTheDocument()
    expect(await screen.findByText('No reviews found')).toBeInTheDocument()
  })

  it('renders the normal empty queue without polling when the daemon is offline', async () => {
    const offline = { ...bootstrap, roborev: { ...bootstrap.roborev, status: { running: false } } }
    let reviewCalls = 0
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(offline)
      if (path.startsWith('/api/reviews')) reviewCalls++
      return response([])
    }))
    render(<App />)
    expect(await screen.findByText('RoboRev daemon is stopped')).toBeInTheDocument()
    expect(await screen.findByText('No reviews found')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Review HEAD' })).toBeDisabled()
    expect(reviewCalls).toBe(0)
    expect(document.querySelector('.page-loading')).not.toBeInTheDocument()
  })

  it('requires a fresh review settle when polling returns online', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const pending = deferred<Response>()
    const offlineRuntime = { ...bootstrap.roborev, status: { running: false } }
    let runtimeCalls = 0
    let pendingReviewCalls = 0
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path === '/api/runtime') return response(++runtimeCalls === 1 ? offlineRuntime : bootstrap.roborev)
      if (path.startsWith('/api/reviews?')) {
        if (runtimeCalls < 2) return response(jobs)
        pendingReviewCalls++
        return pending.promise.then(value => value.clone())
      }
      return response({ success: true })
    }))
    render(<App />)
    expect(await screen.findByText(/#42/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Review HEAD' })).toBeEnabled()
    await vi.advanceTimersByTimeAsync(10_100)
    expect(await screen.findByText('RoboRev daemon is stopped')).toBeInTheDocument()
    expect(document.querySelector('.page-loading')).not.toBeInTheDocument()
    await vi.advanceTimersByTimeAsync(10_100)
    await waitFor(() => expect(pendingReviewCalls).toBeGreaterThan(0))
    expect(screen.getByRole('button', { name: 'Review HEAD' })).toBeDisabled()
    expect(screen.queryByText('No reviews found')).not.toBeInTheDocument()
    expect(document.querySelector('.page-loading')).toBeInTheDocument()
    pending.resolve(new Response(JSON.stringify(jobs), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    expect(await screen.findByText(/#42/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Review HEAD' })).toBeEnabled()
  })

  it('serializes polling and applies a slow review response', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const slow = deferred<Response>()
    let reviewCalls = 0
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path === '/api/runtime') return response(bootstrap.roborev)
      if (path.startsWith('/api/reviews?')) { reviewCalls++; return slow.promise }
      return response({ success: true })
    }))
    render(<App />)
    expect(await screen.findByRole('button', { name: 'Review HEAD' })).toBeDisabled()
    expect(screen.queryByText('No reviews found')).not.toBeInTheDocument()
    await waitFor(() => expect(reviewCalls).toBe(1))
    await vi.advanceTimersByTimeAsync(12_000)
    expect(reviewCalls).toBe(1)
    slow.resolve(new Response(JSON.stringify(jobs), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    expect(await screen.findByText(/#42/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Review HEAD' })).toBeEnabled()
  })

  it('ignores an old repository response after switching repositories', async () => {
    const old = deferred<Response>()
    const current = deferred<Response>()
    const otherRepository = { identity: 'repo-2', name: 'other', path: 'E:\\other', branch: 'feature', head: '222222222222', headShort: '22222222', commitSubject: 'Other', dirty: false, hasConfig: true }
    const initial = { ...bootstrap, repositories: [...bootstrap.repositories, otherRepository] }
    const switched = { ...initial, selectedRepository: otherRepository.path, selectedRepositoryIdentity: otherRepository.identity }
    let bootstrapCalls = 0
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(++bootstrapCalls === 1 ? initial : switched)
      if (path === '/api/repos/select' && init?.method === 'POST') return response({ success: true })
      if (path.includes(encodeURIComponent('E:\\repo'))) return old.promise
      if (path.includes(encodeURIComponent('E:\\other'))) return current.promise.then(value => value.clone())
      return response({ success: true })
    }))
    render(<App />)
    const selector = await screen.findByRole('combobox', { name: 'Current repository' })
    await waitFor(() => expect(fetch).toHaveBeenCalledWith(expect.stringContaining(encodeURIComponent('E:\\repo')), expect.anything()))
    fireEvent.change(selector, { target: { value: otherRepository.identity } })
    await waitFor(() => expect(fetch).toHaveBeenCalledWith(expect.stringContaining(encodeURIComponent('E:\\other')), expect.anything()))
    old.resolve(new Response(JSON.stringify(jobs), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(screen.getByRole('button', { name: 'Review HEAD' })).toBeDisabled()
    expect(screen.queryByText('No reviews found')).not.toBeInTheDocument()
    expect(screen.queryByText(/#42/)).not.toBeInTheDocument()
    current.resolve(new Response(JSON.stringify([{ ...jobs[0], id: 84, commit_subject: 'Other repository review' }]), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    expect(await screen.findByText(/#84/)).toBeInTheDocument()
    expect(screen.getByText(/#84/)).toBeInTheDocument()
  })

  it('ignores an old repository error after switching repositories', async () => {
    const old = deferred<Response>()
    const current = deferred<Response>()
    const otherRepository = { identity: 'repo-2', name: 'other', path: 'E:\\other', branch: 'feature', head: '222222222222', headShort: '22222222', commitSubject: 'Other', dirty: false, hasConfig: true }
    const initial = { ...bootstrap, repositories: [...bootstrap.repositories, otherRepository] }
    const switched = { ...initial, selectedRepository: otherRepository.path, selectedRepositoryIdentity: otherRepository.identity }
    let bootstrapCalls = 0
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(++bootstrapCalls === 1 ? initial : switched)
      if (path === '/api/repos/select' && init?.method === 'POST') return response({ success: true })
      if (path.includes(encodeURIComponent('E:\\repo'))) return old.promise
      if (path.includes(encodeURIComponent('E:\\other'))) return current.promise.then(value => value.clone())
      return response({ success: true })
    }))
    render(<App />)
    const selector = await screen.findByRole('combobox', { name: 'Current repository' })
    await waitFor(() => expect(fetch).toHaveBeenCalledWith(expect.stringContaining(encodeURIComponent('E:\\repo')), expect.anything()))
    fireEvent.change(selector, { target: { value: otherRepository.identity } })
    await waitFor(() => expect(fetch).toHaveBeenCalledWith(expect.stringContaining(encodeURIComponent('E:\\other')), expect.anything()))
    old.resolve(new Response(JSON.stringify({ error: 'old repository failed' }), { status: 502, headers: { 'Content-Type': 'application/json' } }))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(screen.getByRole('button', { name: 'Review HEAD' })).toBeDisabled()
    expect(screen.queryByText('No reviews found')).not.toBeInTheDocument()
    expect(screen.queryByText('old repository failed')).not.toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    current.resolve(new Response(JSON.stringify([{ ...jobs[0], id: 84, commit_subject: 'Other repository review' }]), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    expect(await screen.findByText(/#84/)).toBeInTheDocument()
  })

  it('settles initial review readiness on an error without an infinite skeleton', async () => {
    const failed = deferred<Response>()
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/reviews?')) return failed.promise
      if (path.startsWith('/api/panel-refines?')) return response(refineEnvelope([]))
      return response({ success: true })
    }))
    render(<App />)
    expect(await screen.findByRole('button', { name: 'Review HEAD' })).toBeDisabled()
    expect(screen.queryByText('No reviews found')).not.toBeInTheDocument()
    failed.resolve(new Response(JSON.stringify({ error: 'initial review refresh failed' }), { status: 502, headers: { 'Content-Type': 'application/json' } }))
    expect(await screen.findByRole('alert')).toHaveTextContent('initial review refresh failed')
    expect(screen.getByText('No reviews found')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Review HEAD' })).toBeEnabled()
  })

  it('uses opaque identity for case-varied selected paths and updates an existing repository without duplication', async () => {
    const registered = { ...bootstrap.repositories[0], path: 'E:\\Repo', branch: 'main' }
    const caseBootstrap = { ...bootstrap, selectedRepository: 'e:\\repo', repositories: [registered] }
    const pinned = { ...registered, path: 'e:\\REPO', branch: 'feature/fresh', head: 'fedcba987654', headShort: 'fedcba98' }
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(caseBootstrap)
      if (path === '/api/reviews' && init?.method === 'POST') return response({ receipt: { job_id: 99 }, target: pinned })
      if (path.startsWith('/api/reviews?')) return response(jobs)
      return response({ success: true })
    }))
    render(<App />)
    expect(await screen.findByText('abcdef12')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByRole('button', { name: 'Review HEAD' })).toBeEnabled())
    await userEvent.click(screen.getByRole('button', { name: 'Review HEAD' }))
    expect(await screen.findByText('New HEAD review enqueued for fedcba98.')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Repositories' }))
    expect(screen.getByText('e:\\REPO')).toBeInTheDocument()
    expect(document.querySelectorAll('.repo-cards article')).toHaveLength(1)
  })

  it('keeps case-varied paths distinct when their opaque identities differ', async () => {
    const other = { ...bootstrap.repositories[0], identity: 'repo-2', name: 'other', path: 'e:\\REPO', branch: 'other' }
    const distinct = { ...bootstrap, repositories: [...bootstrap.repositories, other] }
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => String(input) === '/api/bootstrap' ? response(distinct) : response([])))
    render(<App />)
    await waitFor(() => expect(screen.getByRole('button', { name: 'Review HEAD' })).toBeEnabled())
    await userEvent.click(screen.getByRole('button', { name: 'Repositories' }))
    expect(document.querySelectorAll('.repo-cards article')).toHaveLength(2)
    expect(screen.getByText('E:\\repo')).toBeInTheDocument()
    expect(screen.getByText('e:\\REPO')).toBeInTheDocument()
  })

  it('uses identity as the selector value but posts the canonical repository path', async () => {
    const other = { ...bootstrap.repositories[0], identity: 'repo-2', name: 'other', path: 'e:\\REPO', branch: 'other' }
    const initial = { ...bootstrap, repositories: [...bootstrap.repositories, other] }
    const switched = { ...initial, selectedRepository: other.path, selectedRepositoryIdentity: other.identity }
    let bootstrapCalls = 0
    let selectBody: unknown
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(++bootstrapCalls === 1 ? initial : switched)
      if (path === '/api/repos/select' && init?.method === 'POST') { selectBody = JSON.parse(String(init.body)); return response({ success: true }) }
      if (path.startsWith('/api/reviews?')) return response([])
      return response({ success: true })
    }))
    render(<App />)
    const selector = await screen.findByRole('combobox', { name: 'Current repository' })
    await waitFor(() => expect(selector).toHaveValue(bootstrap.selectedRepositoryIdentity))
    fireEvent.change(selector, { target: { value: other.identity } })
    await waitFor(() => expect(selectBody).toEqual({ repoPath: other.path }))
    expect(String(JSON.stringify(selectBody))).not.toContain(other.identity)
    await waitFor(() => expect(selector).toHaveValue(other.identity))
  })

  it('loads the new repository panel catalog once and keeps Review HEAD gated until it settles', async () => {
    const other = { ...bootstrap.repositories[0], identity: 'repo-2', name: 'other', path: 'E:\\other', branch: 'other' }
    const initial = { ...bootstrap, repositories: [...bootstrap.repositories, other] }
    const switched = { ...initial, selectedRepository: other.path, selectedRepositoryIdentity: other.identity }
    const bPanels = deferred<Response>()
    const panelCalls: string[] = []
    let bootstrapCalls = 0
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(++bootstrapCalls === 1 ? initial : switched)
      if (path === '/api/repos/select' && init?.method === 'POST') return response({ success: true })
      if (path.startsWith('/api/reviews?')) return response([])
      return response({ success: true })
    }, repoPath => {
      panelCalls.push(repoPath)
      return repoPath === other.path ? bPanels.promise : response(panelConfiguration('repo-1'))
    }))
    render(<App />)
    await waitFor(() => expect(screen.getByRole('button', { name: 'Review HEAD' })).toBeEnabled())
    fireEvent.change(screen.getByRole('combobox', { name: 'Current repository' }), { target: { value: other.identity } })

    await waitFor(() => expect(panelCalls.filter(path => path === other.path)).toHaveLength(1))
    expect(screen.getByRole('combobox', { name: 'Review panel' })).toHaveValue('')
    expect(screen.queryByRole('option', { name: 'Cross review · reviewers' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Review HEAD' })).toBeDisabled()

    bPanels.resolve(new Response(JSON.stringify(panelConfiguration(other.identity, [{ ...defaultPanel, name: 'bravo' }], 'bravo')), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await waitFor(() => expect(screen.getByRole('combobox', { name: 'Review panel' })).toHaveValue('bravo'))
    expect(panelCalls.filter(path => path === other.path)).toHaveLength(1)
    await waitFor(() => expect(screen.getByRole('button', { name: 'Review HEAD' })).toBeEnabled())
  })

  it.each([
    ['response', 200, panelConfiguration('repo-1', [{ ...defaultPanel, name: 'alpha' }], 'alpha')],
    ['error', 502, { error: 'stale A panel failure' }],
  ])('does not leak a stale Repo A panel %s into Repo B', async (_kind, status, payload) => {
    const oldPanels = deferred<Response>()
    const other = { ...bootstrap.repositories[0], identity: 'repo-2', name: 'other', path: 'E:\\other', branch: 'other' }
    const initial = { ...bootstrap, repositories: [...bootstrap.repositories, other] }
    const switched = { ...initial, selectedRepository: other.path, selectedRepositoryIdentity: other.identity }
    const bravo = { ...defaultPanel, name: 'bravo', origin: 'local' as const }
    let bootstrapCalls = 0
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(++bootstrapCalls === 1 ? initial : switched)
      if (path === '/api/repos/select' && init?.method === 'POST') return response({ success: true })
      if (path.startsWith('/api/reviews?')) return response(path.includes(encodeURIComponent(other.path)) ? [{ ...jobs[0], id: 84 }] : jobs)
      return response({ success: true })
    }, repoPath => repoPath === other.path ? response(panelConfiguration(other.identity, [bravo], bravo.name)) : oldPanels.promise))
    render(<App />)
    fireEvent.change(await screen.findByRole('combobox', { name: 'Current repository' }), { target: { value: other.identity } })
    await waitFor(() => expect(screen.getByRole('combobox', { name: 'Review panel' })).toHaveValue(bravo.name))
    oldPanels.resolve(new Response(JSON.stringify(payload), { status, headers: { 'Content-Type': 'application/json' } }))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(screen.getByRole('combobox', { name: 'Review panel' })).toHaveValue(bravo.name)
    expect(screen.queryByText('stale A panel failure')).not.toBeInTheDocument()
    expect(screen.queryByRole('option', { name: 'Cross review · alpha' })).not.toBeInTheDocument()
  })

  it('uses a remembered server panel choice', async () => {
    const panels = [{ ...defaultPanel, name: 'alpha' }, { ...defaultPanel, name: 'beta', origin: 'local' as const }]
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => String(input) === '/api/bootstrap' ? response(bootstrap) : response(jobs), () => response(panelConfiguration('repo-1', panels, 'beta'))))
    render(<App />)
    await waitFor(() => expect(screen.getByRole('combobox', { name: 'Review panel' })).toHaveValue('beta'))
    expect(screen.getAllByText('Cross review · beta')).toHaveLength(2)
    await waitFor(() => expect(screen.getByRole('button', { name: 'Review HEAD' })).toBeEnabled())
  })

  it('uses the server auto-selection for one named panel', async () => {
    const solo = { ...defaultPanel, name: 'solo' }
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => String(input) === '/api/bootstrap' ? response(bootstrap) : response(jobs), () => response(panelConfiguration('repo-1', [solo], solo.name))))
    render(<App />)
    await waitFor(() => expect(screen.getByRole('combobox', { name: 'Review panel' })).toHaveValue('solo'))
    expect(screen.getAllByText('Cross review · solo')).toHaveLength(2)
    expect(screen.getByRole('button', { name: 'Review HEAD' })).toBeEnabled()
  })

  it('requires a choice when the server reports multiple panels without a saved selection', async () => {
    const panels = [{ ...defaultPanel, name: 'alpha' }, { ...defaultPanel, name: 'beta' }]
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => String(input) === '/api/bootstrap' ? response(bootstrap) : response(jobs), () => response(panelConfiguration('repo-1', panels, '', true))))
    render(<App />)
    expect(await screen.findByRole('option', { name: 'Select a review panel' })).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: 'Review panel' })).toHaveValue('')
    expect(screen.getByRole('button', { name: 'Review HEAD' })).toBeDisabled()
  })

  it('honors stale selection state reported by the server', async () => {
    const panels = [{ ...defaultPanel, name: 'alpha' }, { ...defaultPanel, name: 'beta' }]
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => String(input) === '/api/bootstrap' ? response(bootstrap) : response(jobs), () => response(panelConfiguration('repo-1', panels, 'removed', true))))
    render(<App />)
    expect(await screen.findByRole('option', { name: 'Select a review panel' })).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: 'Review panel' })).toHaveValue('')
    expect(screen.getByRole('button', { name: 'Review HEAD' })).toBeDisabled()
  })

  it('keeps malformed server panel state disabled', async () => {
    const stale = { ...defaultPanel, name: 'available' }
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => String(input) === '/api/bootstrap' ? response(bootstrap) : response(jobs), () => response({ ...panelConfiguration('repo-1', [stale], 'removed', true), panels: 'invalid' })))
    render(<App />)
    expect(await screen.findByText('Panel configuration unavailable')).toBeInTheDocument()
    expect(screen.getByText('The panel configuration response was malformed.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Review HEAD' })).toBeDisabled()
  })

  it('shows a safe unavailable state for a server-reported panel configuration error', async () => {
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => String(input) === '/api/bootstrap' ? response(bootstrap) : response(jobs), () => response(panelConfiguration('repo-1', [defaultPanel], '', true, 'Repository panel config could not be read.'))))
    render(<App />)
    expect(await screen.findByText('Panel configuration unavailable')).toBeInTheDocument()
    expect(screen.getByText('Repository panel config could not be read.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Review HEAD' })).toBeDisabled()
  })

  it('posts the exact selected panel for Review HEAD', async () => {
    const beta = { ...defaultPanel, name: 'beta' }
    let reviewBody: unknown
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path === '/api/reviews' && init?.method === 'POST') { reviewBody = JSON.parse(String(init.body)); return response({ receipt: { job_id: 99 }, target: bootstrap.repositories[0] }) }
      if (path.startsWith('/api/reviews?')) return response(jobs)
      return response({ success: true })
    }, () => response(panelConfiguration('repo-1', [beta], beta.name))))
    render(<App />)
    await waitFor(() => expect(screen.getByRole('button', { name: 'Review HEAD' })).toBeEnabled())
    await userEvent.click(screen.getByRole('button', { name: 'Review HEAD' }))
    await waitFor(() => expect(reviewBody).toEqual({ repoPath: bootstrap.repositories[0].path, panel: beta.name }))
  })

  it('supports a dashboard-free repository with the synthetic no-panel choice', async () => {
    const none = { name: 'none', kind: 'single' as const, members: [], origin: 'builtin' as const, valid: true }
    let reviewBody = ''
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response({ ...bootstrap, repositories: [{ ...bootstrap.repositories[0], hasConfig: false }] })
      if (path === '/api/reviews' && init?.method === 'POST') { reviewBody = String(init.body); return response({ receipt: { job_id: 99 }, target: bootstrap.repositories[0] }) }
      if (path.startsWith('/api/reviews?')) return response(jobs)
      return response({ success: true })
    }, () => response(panelConfiguration('repo-1', [none], none.name))))
    render(<App />)
    await waitFor(() => expect(screen.getByRole('combobox', { name: 'Review panel' })).toHaveValue('none'))
    expect(screen.getAllByText('Single agent · no panel')).toHaveLength(2)
    await userEvent.click(screen.getByRole('button', { name: 'Review HEAD' }))
    await waitFor(() => expect(JSON.parse(reviewBody)).toEqual({ repoPath: bootstrap.repositories[0].path, panel: 'none' }))
    expect(reviewBody).not.toContain('dashboard_cross')
  })

  it('persists a panel selection with the canonical repository path and reloads server state', async () => {
    const panels = [{ ...defaultPanel, name: 'alpha' }, { ...defaultPanel, name: 'beta' }]
    let selectedPanel = ''
    let selectionBody: unknown
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path === '/api/repos/panel-selection' && init?.method === 'POST') { selectionBody = JSON.parse(String(init.body)); selectedPanel = 'beta'; return response({ success: true }) }
      if (path.startsWith('/api/reviews?')) return response(jobs)
      return response({ success: true })
    }, () => response(panelConfiguration('repo-1', panels, selectedPanel, !selectedPanel))))
    render(<App />)
    const selector = await screen.findByRole('combobox', { name: 'Review panel' })
    await screen.findByRole('option', { name: 'Cross review · beta' })
    await userEvent.selectOptions(selector, 'beta')
    await waitFor(() => expect(selectionBody).toEqual({ repoPath: bootstrap.repositories[0].path, panel: 'beta' }))
    await waitFor(() => expect(selector).toHaveValue('beta'))
    expect(screen.getByRole('button', { name: 'Review HEAD' })).toBeEnabled()
  })

  it('retries panel discovery after an initial transient failure', async () => {
    let panelCalls = 0
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/reviews?')) return response(jobs)
      return response({ success: true })
    }, () => ++panelCalls === 1
      ? response({ error: 'temporary panel failure' }, 502)
      : response(panelConfiguration())))
    render(<App />)
    expect(await screen.findByText('Panel configuration unavailable')).toBeInTheDocument()
    expect(screen.getByText('temporary panel failure')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Review HEAD' })).toBeDisabled()
    await userEvent.click(screen.getByRole('button', { name: 'Retry panel discovery' }))
    await waitFor(() => expect(screen.getByRole('combobox', { name: 'Review panel' })).toHaveValue(defaultPanel.name))
    expect(screen.queryByText('Panel configuration unavailable')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Review HEAD' })).toBeEnabled()
  })

  it('reports a failed panel selection immediately and recovers through retry', async () => {
    const panels = [{ ...defaultPanel, name: 'alpha' }, { ...defaultPanel, name: 'beta' }]
    let selectionAttempted = false
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path === '/api/repos/panel-selection' && init?.method === 'POST') {
        selectionAttempted = true
        return response({ error: 'selection storage unavailable' }, 503)
      }
      if (path.startsWith('/api/reviews?')) return response(jobs)
      return response({ success: true })
    }, () => response(panelConfiguration('repo-1', panels, selectionAttempted ? 'alpha' : '', !selectionAttempted))))
    render(<App />)
    const selector = await screen.findByRole('combobox', { name: 'Review panel' })
    await screen.findByRole('option', { name: 'Cross review · beta' })
    await userEvent.selectOptions(selector, 'beta')
    expect(await screen.findByText('selection storage unavailable', { selector: '.toast' })).toBeInTheDocument()
    expect(screen.getByText('Panel configuration unavailable')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Retry panel discovery' }))
    await waitFor(() => expect(selector).toHaveValue('alpha'))
    expect(screen.queryByText('Panel configuration unavailable')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Review HEAD' })).toBeEnabled()
  })

  it('suppresses a stale panel-selection failure after switching repositories', async () => {
    const selection = deferred<Response>()
    const panels = [{ ...defaultPanel, name: 'alpha' }, { ...defaultPanel, name: 'beta' }]
    const other = { identity: 'repo-2', name: 'other', path: 'E:\\other', branch: 'feature', head: '222222222222', headShort: '22222222', commitSubject: 'Other', dirty: false, hasConfig: true }
    const initial = { ...bootstrap, repositories: [...bootstrap.repositories, other] }
    const switched = { ...initial, selectedRepository: other.path, selectedRepositoryIdentity: other.identity }
    let bootstrapCalls = 0
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(++bootstrapCalls === 1 ? initial : switched)
      if (path === '/api/repos/panel-selection' && init?.method === 'POST') return selection.promise
      if (path === '/api/repos/select' && init?.method === 'POST') return response({ success: true })
      if (path.startsWith('/api/reviews?')) return response([])
      return response({ success: true })
    }, repoPath => response(panelConfiguration(repoPath === other.path ? other.identity : 'repo-1', panels, 'alpha'))))
    render(<App />)
    await waitFor(() => expect(screen.getByRole('combobox', { name: 'Review panel' })).toHaveValue('alpha'))
    fireEvent.change(screen.getByRole('combobox', { name: 'Review panel' }), { target: { value: 'beta' } })
    fireEvent.change(screen.getByRole('combobox', { name: 'Current repository' }), { target: { value: other.identity } })
    await waitFor(() => expect(screen.getByRole('combobox', { name: 'Current repository' })).toHaveValue(other.identity))
    selection.resolve(new Response(JSON.stringify({ error: 'stale selection failure' }), { status: 503, headers: { 'Content-Type': 'application/json' } }))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(screen.queryByText('stale selection failure')).not.toBeInTheDocument()
    await waitFor(() => expect(screen.getByRole('combobox', { name: 'Review panel' })).toHaveValue('alpha'))
  })

  it('restores the server-selected repository after a repo switch fails', async () => {
    const other = { identity: 'repo-2', name: 'other', path: 'E:\\other', branch: 'feature', head: '222222222222', headShort: '22222222', commitSubject: 'Other', dirty: false, hasConfig: true }
    const state = { ...bootstrap, repositories: [...bootstrap.repositories, other] }
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(state)
      if (path === '/api/repos/select' && init?.method === 'POST') return response({ error: 'repository selection rejected' }, 500)
      if (path.startsWith('/api/reviews?')) return response(jobs)
      return response({ success: true })
    }))
    render(<App />)
    await screen.findByText(/#42/)
    fireEvent.change(screen.getByRole('combobox', { name: 'Current repository' }), { target: { value: other.identity } })
    expect(await screen.findByText('repository selection rejected', { selector: '.toast' })).toBeInTheDocument()
    await waitFor(() => expect(screen.getByRole('combobox', { name: 'Current repository' })).toHaveValue('repo-1'))
    expect(screen.getByText(/#42/)).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: 'Review panel' })).toHaveValue(defaultPanel.name)
    expect(screen.queryByText('Panel configuration unavailable')).not.toBeInTheDocument()
  })

  it('keeps the newest repository when an older failed switch completes later', async () => {
    const staleSwitch = deferred<Response>()
    const repoB = { identity: 'repo-2', name: 'repo-b', path: 'E:\\repo-b', branch: 'feature/b', head: '222222222222', headShort: '22222222', commitSubject: 'B', dirty: false, hasConfig: true }
    const repoC = { identity: 'repo-3', name: 'repo-c', path: 'E:\\repo-c', branch: 'feature/c', head: '333333333333', headShort: '33333333', commitSubject: 'C', dirty: false, hasConfig: true }
    const initial = { ...bootstrap, repositories: [...bootstrap.repositories, repoB, repoC] }
    const selectedC = { ...initial, selectedRepository: repoC.path, selectedRepositoryIdentity: repoC.identity }
    const cJobs = [{ ...jobs[0], id: 126, git_ref: repoC.head, commit_subject: 'Repository C review' }]
    let bootstrapCalls = 0
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(++bootstrapCalls === 1 ? initial : selectedC)
      if (path === '/api/repos/select' && init?.method === 'POST') {
        const body = JSON.parse(String(init.body)) as { repoPath: string }
        return body.repoPath === repoB.path ? staleSwitch.promise : response({ success: true })
      }
      if (path.startsWith('/api/reviews?')) return response(path.includes(encodeURIComponent(repoC.path)) ? cJobs : jobs)
      return response({ success: true })
    }, repoPath => {
      const identity = repoPath === repoC.path ? repoC.identity : repoPath === repoB.path ? repoB.identity : 'repo-1'
      return response(panelConfiguration(identity))
    }))
    render(<App />)
    await screen.findByText(/#42/)
    const selector = screen.getByRole('combobox', { name: 'Current repository' })
    fireEvent.change(selector, { target: { value: repoB.identity } })
    expect(selector).toBeDisabled()
    // A queued browser event is still guarded even though the control is disabled during the active switch.
    fireEvent.change(selector, { target: { value: repoC.identity } })
    expect(await screen.findByText(/#126/)).toBeInTheDocument()
    await waitFor(() => expect(selector).toHaveValue(repoC.identity))
    expect(selector).toBeEnabled()

    staleSwitch.resolve(new Response(JSON.stringify({ error: 'stale repository B failure' }), { status: 500, headers: { 'Content-Type': 'application/json' } }))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(selector).toHaveValue(repoC.identity)
    expect(screen.getByText('feature/c')).toBeInTheDocument()
    expect(screen.getByText('33333333')).toBeInTheDocument()
    expect(screen.getByText(/#126/)).toBeInTheDocument()
    expect(screen.queryByText(/#42/)).not.toBeInTheDocument()
    expect(screen.queryByText('stale repository B failure')).not.toBeInTheDocument()
    expect(screen.queryByText('Repository state could not be restored')).not.toBeInTheDocument()
  })

  it('uses the server-pinned Review HEAD target in the toast and repository metadata', async () => {
    const pinned = { ...bootstrap.repositories[0], branch: 'feature/fresh', head: 'fedcba987654', headShort: 'fedcba98', commitSubject: 'Fresh commit', dirty: true }
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path === '/api/reviews' && init?.method === 'POST') return response({ receipt: { job_id: 99 }, target: pinned })
      if (path.startsWith('/api/reviews?')) return response([])
      return response({ success: true })
    }))
    render(<App />)
    await waitFor(() => expect(screen.getByRole('button', { name: 'Review HEAD' })).toBeEnabled())
    await userEvent.click(screen.getByRole('button', { name: 'Review HEAD' }))
    expect(await screen.findByText('New HEAD review enqueued for fedcba98.')).toBeInTheDocument()
    expect(screen.getByText('feature/fresh')).toBeInTheDocument()
    expect(screen.getByText('fedcba98')).toBeInTheDocument()
    expect(screen.getByText('Dirty')).toBeInTheDocument()
    expect(screen.queryByText('RoboRev state updated.')).not.toBeInTheDocument()
  })

  it('shows fix prompt actions for terminal findings but not passes', async () => {
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/reviews?')) return response(jobs)
      if (path === '/api/reviews/42') return response(jobs[0])
      return response({ success: true })
    }))
    render(<App />)
    await userEvent.click(await screen.findByText(/#42/))
    expect(await screen.findByRole('button', { name: 'Copy Fix Prompt' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Open Fix Prompt' })).toBeInTheDocument()
    cleanup()

    const pass = { ...jobs[0], verdict: 'P' }
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/reviews?')) return response([pass])
      if (path === '/api/reviews/42') return response(pass)
      return response({ success: true })
    }))
    render(<App />)
    await userEvent.click(await screen.findByText(/#42/))
    await screen.findByRole('heading', { name: 'Initial dashboard checkpoint' })
    expect(screen.queryByRole('button', { name: 'Copy Fix Prompt' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Open Fix Prompt' })).not.toBeInTheDocument()
  })

  it('fetches and copies the exact repository-scoped fix prompt without mutations', async () => {
    const clipboard = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue(undefined)
    const calls: Array<{ path: string; method: string }> = []
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      calls.push({ path, method: init?.method ?? 'GET' })
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/reviews?')) return response(jobs)
      if (path === '/api/reviews/42') return response(jobs[0])
      if (path.startsWith('/api/reviews/42/fix-prompt?')) return response({ text: 'Fix this finding safely.', jobId: 42, stale: false })
      return response({ success: true })
    }))
    render(<App />)
    await userEvent.click(await screen.findByText(/#42/))
    await userEvent.click(await screen.findByRole('button', { name: 'Copy Fix Prompt' }))
    expect(await screen.findByText('Fix prompt for review #42 copied.')).toBeInTheDocument()
    expect(clipboard).toHaveBeenCalledWith('Fix this finding safely.')
    expect(calls).toContainEqual({ path: `/api/reviews/42/fix-prompt?repo=${encodeURIComponent(bootstrap.selectedRepository)}`, method: 'GET' })
    expect(calls.filter(call => call.method !== 'GET')).toHaveLength(0)
  })

  it('opens an editable fix prompt with generated finding metadata', async () => {
    const clipboard = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue(undefined)
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/reviews?')) return response(jobs)
      if (path === '/api/reviews/42') return response(jobs[0])
      if (path.startsWith('/api/reviews/42/fix-prompt?')) return response({ text: 'Original fix prompt', jobId: 42, stale: true })
      return response({ success: true })
    }))
    render(<App />)
    await userEvent.click(await screen.findByText(/#42/))
    await userEvent.click(await screen.findByRole('button', { name: 'Open Fix Prompt' }))
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
    expect(screen.getByText('Finding · Review #42')).toBeInTheDocument()
    expect(screen.getByText(/Repository state changed/)).toBeInTheDocument()
    const editor = screen.getByRole('textbox', { name: 'Fix prompt' })
    expect(editor).toHaveValue('Original fix prompt')
    fireEvent.change(editor, { target: { value: '수정한 프롬프트\n붙여넣은 내용' } })
    expect(editor).toHaveValue('수정한 프롬프트\n붙여넣은 내용')
    const dialog = screen.getByRole('dialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Copy' }))
    expect(clipboard).toHaveBeenCalledWith('수정한 프롬프트\n붙여넣은 내용')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Close' }))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('falls back to the editable fix prompt when clipboard copy fails', async () => {
    vi.spyOn(navigator.clipboard, 'writeText').mockRejectedValue(new Error('permission denied'))
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/reviews?')) return response(jobs)
      if (path === '/api/reviews/42') return response(jobs[0])
      if (path.startsWith('/api/reviews/42/fix-prompt?')) return response({ text: 'Fallback prompt', jobId: 42, stale: false })
      return response({ success: true })
    }))
    render(<App />)
    await userEvent.click(await screen.findByText(/#42/))
    await userEvent.click(await screen.findByRole('button', { name: 'Copy Fix Prompt' }))
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: 'Fix prompt' })).toHaveValue('Fallback prompt')
  })

  it('ignores a fix prompt response after the review detail closes', async () => {
    const prompt = deferred<Response>()
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/reviews?')) return response(jobs)
      if (path === '/api/reviews/42') return response(jobs[0])
      if (path.startsWith('/api/reviews/42/fix-prompt?')) return prompt.promise
      return response({ success: true })
    }))
    render(<App />)
    await userEvent.click(await screen.findByText(/#42/))
    await userEvent.click(await screen.findByRole('button', { name: 'Open Fix Prompt' }))
    await userEvent.click(screen.getByRole('button', { name: 'Close detail' }))
    prompt.resolve(new Response(JSON.stringify({ text: 'Stale prompt', jobId: 42, stale: false }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(screen.queryByText('Stale prompt')).not.toBeInTheDocument()
    expect(screen.queryByText(/Fix prompt for review/)).not.toBeInTheDocument()
  })

  it.each([
    ['success', 200, { receipt: { job_id: 99 }, target: { ...bootstrap.repositories[0], branch: 'stale-branch', head: 'aaaaaaaaaaaa', headShort: 'aaaaaaaa' } }],
    ['error', 409, { error: 'stale enqueue failed' }],
  ])('ignores a stale Review HEAD %s after switching repositories', async (_kind, status, payload) => {
    const enqueue = deferred<Response>()
    const otherRepository = { identity: 'repo-2', name: 'other', path: 'E:\\other', branch: 'feature/current', head: '222222222222', headShort: '22222222', commitSubject: 'Other', dirty: false, hasConfig: true }
    const initial = { ...bootstrap, repositories: [...bootstrap.repositories, otherRepository] }
    const switched = { ...initial, selectedRepository: otherRepository.path, selectedRepositoryIdentity: otherRepository.identity }
    const otherJobs = [{ ...jobs[0], id: 84, git_ref: otherRepository.head, commit_subject: 'Other repository review' }]
    let bootstrapCalls = 0
    let enqueueCalls = 0
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(++bootstrapCalls === 1 ? initial : switched)
      if (path === '/api/reviews' && init?.method === 'POST') { enqueueCalls++; return enqueue.promise }
      if (path === '/api/repos/select' && init?.method === 'POST') return response({ success: true })
      if (path.includes(encodeURIComponent('E:\\other'))) return response(otherJobs)
      if (path.startsWith('/api/reviews?')) return response(jobs)
      return response({ success: true })
    }))
    render(<App />)
    await waitFor(() => expect(screen.getByRole('button', { name: 'Review HEAD' })).toBeEnabled())
    await userEvent.click(screen.getByRole('button', { name: 'Review HEAD' }))
    await waitFor(() => expect(enqueueCalls).toBe(1))
    fireEvent.change(screen.getByRole('combobox', { name: 'Current repository' }), { target: { value: otherRepository.identity } })
    expect(await screen.findByText(/#84/)).toBeInTheDocument()
    enqueue.resolve(new Response(JSON.stringify(payload), { status, headers: { 'Content-Type': 'application/json' } }))
    await new Promise(resolve => setTimeout(resolve, 0))
    await waitFor(() => expect(screen.getByRole('combobox', { name: 'Current repository' })).toHaveValue(otherRepository.identity))
    expect(screen.getByText('feature/current')).toBeInTheDocument()
    expect(screen.getByText('22222222')).toBeInTheDocument()
    expect(screen.getByText(/#84/)).toBeInTheDocument()
    expect(screen.queryByText(/#42/)).not.toBeInTheDocument()
    expect(screen.queryByText('aaaaaaaa')).not.toBeInTheDocument()
    expect(screen.queryByText('New HEAD review enqueued for aaaaaaaa.')).not.toBeInTheDocument()
    expect(screen.queryByText('stale enqueue failed')).not.toBeInTheDocument()
  })

  it('deduplicates background failures inline while direct action failures still toast', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    let reviewCalls = 0
    let runtimeCalls = 0
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path === '/api/reviews' && init?.method === 'POST') return response({ error: 'enqueue rejected' }, 409)
      if (path.startsWith('/api/reviews?')) { reviewCalls++; return response({ error: 'review refresh failed' }, 502) }
      if (path === '/api/runtime') { runtimeCalls++; return response({ error: 'runtime refresh failed' }, 502) }
      if (path.startsWith('/api/panel-refines?')) return response(refineEnvelope([]))
      return response({ success: true })
    }))
    render(<App />)
    expect(await screen.findByRole('alert')).toHaveTextContent('review refresh failed')
    await vi.advanceTimersByTimeAsync(10_100)
    expect(reviewCalls).toBeGreaterThan(1)
    expect(runtimeCalls).toBe(1)
    expect(screen.getAllByRole('alert')).toHaveLength(1)
    expect(document.querySelector('.toast.error')).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Review HEAD' }))
    expect(await screen.findByText('enqueue rejected')).toHaveClass('toast', 'error')
    expect(screen.getAllByRole('alert')).toHaveLength(1)
  })

  it('reconnects to an active refine after bootstrap and serializes only active polling', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const slow = deferred<Response>()
    let refineCalls = 0
    const active = refineRun()
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/panel-refines?')) { refineCalls++; return refineCalls === 1 ? response(refineEnvelope([active])) : refineCalls === 2 ? slow.promise : response(refineEnvelope([{ ...active, currentHead: 'bbbbbbbbbbbb' }])) }
      if (path.startsWith('/api/reviews?')) return response(jobs)
      if (path === '/api/reviews/42') return response(jobs[0])
      return response({ success: true })
    }))
    render(<App />)
    await openFixRefine()
    expect(await screen.findByText('Reviewing')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Review HEAD' })).toBeDisabled()
    await userEvent.click(screen.getByRole('button', { name: '#42' }))
    expect(await screen.findByRole('heading', { name: 'Initial dashboard checkpoint' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Rerun same target\/config/ })).not.toBeInTheDocument()
    await vi.advanceTimersByTimeAsync(4_000)
    expect(refineCalls).toBe(2)
    await vi.advanceTimersByTimeAsync(12_000)
    expect(refineCalls).toBe(2)
    slow.resolve(new Response(JSON.stringify(refineEnvelope([active])), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await vi.advanceTimersByTimeAsync(0)
    await vi.waitFor(() => expect(refineCalls).toBe(3), { timeout: 5_000, interval: 100 })
  })

  it('keeps the active card through a transient refine refresh failure and refreshes bootstrap at terminal state', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    let refineCalls = 0
    let bootstrapCalls = 0
    const active = refineRun()
    const passed = refineRun({ phase: 'passed', currentHead: 'fedcba654321', fixAttempts: 1, completedAt: '2026-08-18T00:00:00Z' })
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') { bootstrapCalls++; return response(bootstrapCalls === 1 ? bootstrap : { ...bootstrap, repositories: [{ ...bootstrap.repositories[0], head: 'fedcba654321', headShort: 'fedcba65' }] }) }
      if (path.startsWith('/api/panel-refines?')) { refineCalls++; if (refineCalls === 1) return response(refineEnvelope([active])); if (refineCalls === 2) return Promise.reject(new Error('refine refresh failed')); return response(refineEnvelope([passed])) }
      if (path.startsWith('/api/reviews?')) return response([])
      return response({ success: true })
    }))
    render(<App />)
    await openFixRefine()
    expect(await screen.findByText('Reviewing')).toBeInTheDocument()
    await vi.advanceTimersByTimeAsync(4_000)
    expect(await screen.findByRole('alert')).toHaveTextContent('refine refresh failed')
    expect(screen.getByText('Reviewing')).toBeInTheDocument()
    await vi.advanceTimersByTimeAsync(4_000)
    expect(await screen.findByText('Panel Refine passed')).toBeInTheDocument()
    await vi.waitFor(() => expect(bootstrapCalls).toBe(2))
    expect(screen.getByText('fedcba654321')).toBeInTheDocument()
  })

  it('retries an initial Panel Refine refresh with backoff and recovers options', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    let refineCalls = 0
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/panel-refines?')) {
        refineCalls++
        return refineCalls === 1 ? Promise.reject(new Error('initial refine refresh failed')) : response(refineEnvelope([]))
      }
      if (path.startsWith('/api/reviews?')) return response([])
      return response({ success: true })
    }))
    render(<App />)
    await openFixRefine()
    await waitFor(() => expect(refineCalls).toBe(1))
    expect(await screen.findByRole('alert')).toHaveTextContent('initial refine refresh failed')
    await vi.advanceTimersByTimeAsync(2_000)
    await waitFor(() => expect(refineCalls).toBe(2))
    expect(await screen.findByRole('button', { name: 'Panel Refine' })).toBeEnabled()
    expect(screen.queryByText('initial refine refresh failed')).not.toBeInTheDocument()
  })

  it('shows only the switched repository refine and suppresses stale old-repository success or error', async () => {
    const otherRepository = { identity: 'repo-2', name: 'other', path: 'E:\\other', branch: 'feature/current', head: '222222222222', headShort: '22222222', commitSubject: 'Other', dirty: false, hasConfig: true }
    const initial = { ...bootstrap, repositories: [...bootstrap.repositories, otherRepository] }
    const switched = { ...initial, selectedRepository: otherRepository.path, selectedRepositoryIdentity: otherRepository.identity }
    const old = deferred<Response>()
    let bootstrapCalls = 0
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(++bootstrapCalls === 1 ? initial : switched)
      if (path === '/api/repos/select' && init?.method === 'POST') return response({ success: true })
      if (path.startsWith('/api/panel-refines?')) return path.includes(encodeURIComponent(otherRepository.path)) ? response(refineEnvelope([{ ...refineRun(), id: 'refine-2', repositoryIdentity: 'repo-2', repositoryPath: otherRepository.path, branch: otherRepository.branch, currentHead: otherRepository.head }])) : old.promise
      if (path.startsWith('/api/reviews?')) return response([])
      return response({ success: true })
    }))
    render(<App />)
    await openFixRefine()
    await waitFor(() => expect(screen.getByRole('combobox', { name: 'Current repository' })).toHaveValue('repo-1'))
    fireEvent.change(screen.getByRole('combobox', { name: 'Current repository' }), { target: { value: otherRepository.identity } })
    await waitFor(() => expect(screen.getByText('222222222222')).toBeInTheDocument())
    old.reject(new Error('stale old repository failure'))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(screen.queryByText('stale old repository failure')).not.toBeInTheDocument()
    expect(screen.getByText('222222222222')).toBeInTheDocument()
  })

  it('does not open a stale Panel Refine review after switching repositories', async () => {
    const otherRepository = { identity: 'repo-2', name: 'other', path: 'E:\\other', branch: 'feature/current', head: '222222222222', headShort: '22222222', commitSubject: 'Other', dirty: false, hasConfig: true }
    const initial = { ...bootstrap, repositories: [...bootstrap.repositories, otherRepository] }
    const switched = { ...initial, selectedRepository: otherRepository.path, selectedRepositoryIdentity: otherRepository.identity }
    const staleDetail = deferred<Response>()
    let bootstrapCalls = 0
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(++bootstrapCalls === 1 ? initial : switched)
      if (path === '/api/repos/select' && init?.method === 'POST') return response({ success: true })
      if (path.startsWith('/api/panel-refines?')) {
        if (path.includes(encodeURIComponent(otherRepository.path))) return response(refineEnvelope([]))
        return response(refineEnvelope([refineRun({ phase: 'passed', currentReviewJobId: 123 })]))
      }
      if (path === '/api/reviews/123') return staleDetail.promise
      if (path.startsWith('/api/reviews?')) return response([])
      return response({ success: true })
    }))
    render(<App />)
    await openFixRefine()
    await userEvent.click(await screen.findByRole('button', { name: 'Open Final Review' }))
    fireEvent.change(screen.getByRole('combobox', { name: 'Current repository' }), { target: { value: otherRepository.identity } })
    await waitFor(() => expect(screen.getByRole('combobox', { name: 'Current repository' })).toHaveValue(otherRepository.identity))
    staleDetail.resolve(new Response(JSON.stringify({ id: 123, job_id: 123, subject: 'Stale Repo A detail', status: 'done', verdict: 'F' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(screen.queryByText('Stale Repo A detail')).not.toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: /Review detail/ })).not.toBeInTheDocument()
  })

  it('accepts a panel refine response when the opaque repository identity matches despite path spelling', async () => {
    const caseVariantRun = refineRun({ repositoryPath: 'e:\REPO' })
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/panel-refines?')) return response(refineEnvelope([caseVariantRun]))
      if (path.startsWith('/api/reviews?')) return response([])
      return response({ success: true })
    }))
    render(<App />)
    await openFixRefine()
    expect(await screen.findByText('Reviewing')).toBeInTheDocument()
  })

  it('disables Panel Refine when the daemon or typed fix capability is unavailable', async () => {
    const unsupported = { ...bootstrap, roborev: { ...bootstrap.roborev, status: { running: false }, capabilities: { ...bootstrap.roborev.capabilities, fixCommand: false } } }
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(unsupported)
      if (path.startsWith('/api/panel-refines?')) return response(refineEnvelope([]))
      if (path.startsWith('/api/reviews?')) return response([])
      return response({ success: true })
    }))
    render(<App />)
    await openFixRefine()
    const trigger = await screen.findByRole('button', { name: 'Panel Refine' })
    expect(trigger).toBeDisabled()
    expect(screen.getByRole('status')).toHaveTextContent('daemon is not running')
  })

  it('keeps frozen rerun unavailable until the initial Panel Refine request settles', async () => {
    const pending = deferred<Response>()
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/panel-refines?')) return pending.promise
      if (path.startsWith('/api/reviews?')) return response(jobs)
      if (path === '/api/reviews/42') return response(jobs[0])
      return response({ success: true })
    }))
    render(<App />)
    await userEvent.click(await screen.findByText(/#42/))
    expect(await screen.findByRole('heading', { name: 'Initial dashboard checkpoint' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Rerun same target\/config/ })).not.toBeInTheDocument()
    pending.resolve(new Response(JSON.stringify(refineEnvelope([])), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    expect(await screen.findByRole('button', { name: /Rerun same target\/config/ })).toBeInTheDocument()
  })

  it('keeps direct start and cancel failures as actionable toasts', async () => {
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/panel-refines?')) return response(refineEnvelope([]))
      if (path === '/api/panel-refines' && init?.method === 'POST') return response({ error: 'start rejected' }, 409)
      if (path.startsWith('/api/reviews?')) return response([])
      return response({ success: true })
    }))
    render(<App />)
    await openFixRefine()
    const trigger = await screen.findByRole('button', { name: 'Panel Refine' })
    await waitFor(() => expect(trigger).toBeEnabled())
    await userEvent.click(trigger)
    await userEvent.click(screen.getByRole('button', { name: 'Start Panel Refine' }))
    expect(await screen.findByText('start rejected')).toHaveClass('toast', 'error')
  })

  it('keeps cancel failure as an actionable toast', async () => {
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/panel-refines?')) return response(refineEnvelope([refineRun()]))
      if (path.startsWith('/api/panel-refines/') && init?.method === 'POST') return response({ error: 'cancel rejected' }, 409)
      if (path.startsWith('/api/reviews?')) return response([])
      return response({ success: true })
    }))
    render(<App />)
    await openFixRefine()
    await screen.findByText('Reviewing')
    await userEvent.click(screen.getByRole('button', { name: 'Cancel Panel Refine' }))
    expect(await screen.findByText('cancel rejected')).toHaveClass('toast', 'error')
  })

  it('does not post an agent check until confirmation and sends only the canonical agent', async () => {
    const calls: Array<{ path: string; init?: RequestInit }> = []
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      calls.push({ path, init })
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path === '/api/setup/roborev') return response(setupReady)
      if (path.startsWith('/api/panel-refines?')) return response(refineEnvelope([]))
      if (path.startsWith('/api/reviews?')) return response([])
      if (path === '/api/agents') return response(setupAgents)
      if (path === '/api/runtime-control') return response(setupRuntime)
      if (path === '/api/runtime') return response(bootstrap.roborev)
      if (path === '/api/agents/check' && init?.method === 'POST') return response({ ...setupAgents, configured: [{ ...setupAgents.configured[0], health: 'passed' }] })
      return response({ success: true })
    }))
    render(<App />)
    await userEvent.click(await screen.findByRole('button', { name: 'Agents & Setup' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Run check' }))
    expect(calls.some(call => call.path === '/api/agents/check')).toBe(false)
    const dialog = screen.getByRole('dialog')
    expect(dialog).toHaveTextContent('small real request through RoboRev')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Run check' }))
    await waitFor(() => expect(calls.filter(call => call.path === '/api/agents/check')).toHaveLength(1))
    expect(JSON.parse(String(calls.find(call => call.path === '/api/agents/check')?.init?.body))).toEqual({ agent: 'claude-code', repositoryIdentity: 'repo-1' })
  })

  it('invalidates repository A agent cards immediately and ignores a late A response', async () => {
    const staleA = deferred<Response>()
    const bAgents = deferred<Response>()
    const other = { identity: 'repo-2', name: 'other', path: 'E:\\other', branch: 'feature', head: '222222222222', headShort: '22222222', commitSubject: 'Other', dirty: false, hasConfig: true }
    const initial = { ...bootstrap, repositories: [...bootstrap.repositories, other] }
    const switched = { ...initial, selectedRepository: other.path, selectedRepositoryIdentity: other.identity }
    let selectionRequested = false
    let agentCalls = 0
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(selectionRequested ? switched : initial)
      if (path === '/api/setup/roborev') return response(setupReady)
      if (path.startsWith('/api/panel-refines?')) return response(refineEnvelope([]))
      if (path.startsWith('/api/reviews?')) return response([])
      if (path === '/api/runtime-control') return response(setupRuntime)
      if (path === '/api/runtime') return response(bootstrap.roborev)
      if (path === '/api/repos/select' && init?.method === 'POST') { selectionRequested = true; return response({ success: true }) }
      if (path === '/api/agents') {
        agentCalls += 1
        if (agentCalls === 1) return response(setupAgents)
        if (agentCalls === 2) return staleA.promise
        return bAgents.promise
      }
      return response({ success: true })
    }))
    render(<App />)
    await userEvent.click(await screen.findByRole('button', { name: 'Agents & Setup' }))
    expect(await screen.findByText('Claude Code')).toBeInTheDocument()
    const agentRefresh = within(screen.getByRole('region', { name: 'Configured Review Agents' })).getByRole('button', { name: 'Refresh' })
    expect(agentRefresh).not.toBeDisabled()
    fireEvent.click(agentRefresh)
    await waitFor(() => expect(agentCalls).toBe(2))

    fireEvent.change(screen.getByRole('combobox', { name: 'Current repository' }), { target: { value: other.identity } })
    await waitFor(() => expect(screen.queryByText('Claude Code')).not.toBeInTheDocument())
    expect(screen.queryByRole('button', { name: 'Run check' })).not.toBeInTheDocument()
    staleA.resolve(new Response(JSON.stringify(setupAgents), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(screen.queryByText('Claude Code')).not.toBeInTheDocument()

    await waitFor(() => expect(agentCalls).toBeGreaterThanOrEqual(3))
    bAgents.resolve(new Response(JSON.stringify(setupAgentsB), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    expect(await screen.findByText('Gemini CLI')).toBeInTheDocument()
    expect(screen.queryByText('Claude Code')).not.toBeInTheDocument()
  })

  it('does not let a repository A check completion reload or toast repository B', async () => {
    const checkA = deferred<Response>()
    const bBootstrap = deferred<Response>()
    const other = { identity: 'repo-2', name: 'other', path: 'E:\\other', branch: 'feature', head: '222222222222', headShort: '22222222', commitSubject: 'Other', dirty: false, hasConfig: true }
    const initial = { ...bootstrap, repositories: [...bootstrap.repositories, other] }
    const switched = { ...initial, selectedRepository: other.path, selectedRepositoryIdentity: other.identity }
    let selectionRequested = false
    let agentCalls = 0
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return selectionRequested ? bBootstrap.promise : response(initial)
      if (path === '/api/setup/roborev') return response(setupReady)
      if (path.startsWith('/api/panel-refines?')) return response(refineEnvelope([]))
      if (path.startsWith('/api/reviews?')) return response([])
      if (path === '/api/runtime-control') return response(setupRuntime)
      if (path === '/api/runtime') return response(bootstrap.roborev)
      if (path === '/api/repos/select' && init?.method === 'POST') { selectionRequested = true; return response({ success: true }) }
      if (path === '/api/agents') { agentCalls += 1; return response(agentCalls === 1 ? setupAgents : setupAgentsB) }
      if (path === '/api/agents/check' && init?.method === 'POST') return checkA.promise
      return response({ success: true })
    }))
    render(<App />)
    await userEvent.click(await screen.findByRole('button', { name: 'Agents & Setup' }))
    await userEvent.click(await screen.findByText('Run check'))
    await userEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Run check' }))
    expect(screen.getByRole('combobox', { name: 'Current repository' })).toBeInTheDocument()

    fireEvent.change(screen.getByRole('combobox', { name: 'Current repository' }), { target: { value: other.identity } })
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(screen.queryByText('Gemini CLI')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Run check' })).not.toBeInTheDocument()
    expect(agentCalls).toBe(1)
    bBootstrap.resolve(new Response(JSON.stringify(switched), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    expect(await screen.findByText('Gemini CLI')).toBeInTheDocument()
    checkA.resolve(new Response(JSON.stringify(setupAgents), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(await screen.findByText('Gemini CLI')).toBeInTheDocument()
    expect(screen.queryByText('Claude Code')).not.toBeInTheDocument()
    expect(screen.queryByText('Agent check completed for claude-code.')).not.toBeInTheDocument()
    expect(agentCalls).toBe(2)
  })

  it('keeps a same-repository health check completion after an overlapping refresh', async () => {
    const refreshAgents = deferred<Response>()
    const finalAgents = deferred<Response>()
    const check = deferred<Response>()
    const calls: Array<{ path: string; init?: RequestInit }> = []
    let agentCalls = 0
    const checkedAgents = { ...setupAgents, configured: [{ ...setupAgents.configured[0], health: 'passed' }] }
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      calls.push({ path, init })
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path === '/api/setup/roborev') return response(setupReady)
      if (path.startsWith('/api/panel-refines?')) return response(refineEnvelope([]))
      if (path.startsWith('/api/reviews?')) return response([])
      if (path === '/api/runtime-control') return response(setupRuntime)
      if (path === '/api/runtime') return response(bootstrap.roborev)
      if (path === '/api/agents') {
        agentCalls += 1
        if (agentCalls === 1) return response(setupAgents)
        if (agentCalls === 2) return refreshAgents.promise
        return finalAgents.promise
      }
      if (path === '/api/agents/check' && init?.method === 'POST') return check.promise
      return response({ success: true })
    }))
    render(<App />)
    await userEvent.click(await screen.findByRole('button', { name: 'Agents & Setup' }))
    await screen.findByText('Claude Code')
    const refresh = within(screen.getByRole('region', { name: 'Configured Review Agents' })).getByRole('button', { name: 'Refresh' })
    await userEvent.click(refresh)
    await waitFor(() => expect(agentCalls).toBe(2))

    await userEvent.click(screen.getByRole('button', { name: 'Run check' }))
    await userEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Run check' }))
    await waitFor(() => expect(calls.some(call => call.path === '/api/agents/check')).toBe(true))
    expect(refresh).toBeDisabled()

    check.resolve(new Response(JSON.stringify(checkedAgents), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await waitFor(() => expect(agentCalls).toBe(3))
    refreshAgents.resolve(new Response(JSON.stringify(setupAgentsB), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(screen.queryByText('Agent check completed for claude-code.')).not.toBeInTheDocument()
    finalAgents.resolve(new Response(JSON.stringify(checkedAgents), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    expect(await screen.findByText('Agent check completed for claude-code.')).toBeInTheDocument()
    expect(agentCalls).toBe(3)
    expect(within(screen.getByRole('region', { name: 'Configured Review Agents' })).getByText('Passed')).toBeInTheDocument()
  })

  it('proves a superseded health reload only after a newer same-repository response applies', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const initialRefresh = deferred<Response>()
    const olderReload = deferred<Response>()
    const newerReload = deferred<Response>()
    const checkedAgents = { ...setupAgents, configured: [{ ...setupAgents.configured[0], health: 'passed' }] }
    let agentCalls = 0
    let runtimeCalls = 0
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path === '/api/setup/roborev') return response(setupReady)
      if (path.startsWith('/api/panel-refines?')) return response(refineEnvelope([]))
      if (path.startsWith('/api/reviews?')) return response([])
      if (path === '/api/runtime-control') {
        runtimeCalls += 1
        return response(runtimeCalls === 1 ? { ...setupRuntime, operationActive: true, daemonOperation: 'starting' } : setupRuntime)
      }
      if (path === '/api/runtime') return response(bootstrap.roborev)
      if (path === '/api/repos/select' && init?.method === 'POST') return response({ success: true })
      if (path === '/api/agents') {
        agentCalls += 1
        if (agentCalls === 1) return response(setupAgents)
        if (agentCalls === 2) return initialRefresh.promise
        if (agentCalls === 3) return olderReload.promise
        return newerReload.promise
      }
      if (path === '/api/agents/check' && init?.method === 'POST') return response({ success: true })
      return response({ success: true })
    }))
    render(<App />)
    await userEvent.click(await screen.findByRole('button', { name: 'Agents & Setup' }))
    await screen.findByText('Claude Code')
    const refresh = within(screen.getByRole('region', { name: 'Configured Review Agents' })).getByRole('button', { name: 'Refresh' })
    await userEvent.click(refresh)
    await waitFor(() => expect(agentCalls).toBe(2))
    await userEvent.click(screen.getByRole('button', { name: 'Run check' }))
    await userEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Run check' }))
    await waitFor(() => expect(agentCalls).toBe(3))
    // The background refresh is a same-scope request while the ordinary
    // manual refresh and post-check request remain in flight.
    await waitFor(() => expect(agentCalls).toBe(4))
    initialRefresh.resolve(new Response(JSON.stringify(setupAgents), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    newerReload.resolve(new Response(JSON.stringify(checkedAgents), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await waitFor(() => expect(within(screen.getByRole('region', { name: 'Configured Review Agents' })).getByText('Passed')).toBeInTheDocument())
    olderReload.resolve(new Response(JSON.stringify(setupAgents), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    expect(await screen.findByText('Agent check completed for claude-code.')).toBeInTheDocument()
    expect(within(screen.getByRole('region', { name: 'Configured Review Agents' })).getByText('Passed')).toBeInTheDocument()
  })

  it('does not treat a superseded reload as successful when the newer response is rejected', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const initialRefresh = deferred<Response>()
    const olderReload = deferred<Response>()
    const checkedAgents = { ...setupAgents, configured: [{ ...setupAgents.configured[0], health: 'passed' }] }
    let agentCalls = 0
    let runtimeCalls = 0
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path === '/api/setup/roborev') return response(setupReady)
      if (path.startsWith('/api/panel-refines?')) return response(refineEnvelope([]))
      if (path.startsWith('/api/reviews?')) return response([])
      if (path === '/api/runtime-control') {
        runtimeCalls += 1
        return response(runtimeCalls < 3 ? { ...setupRuntime, operationActive: true, daemonOperation: 'starting' } : setupRuntime)
      }
      if (path === '/api/runtime') return response(bootstrap.roborev)
      if (path === '/api/agents') {
        agentCalls += 1
        if (agentCalls === 1) return response(setupAgents)
        if (agentCalls === 2) return initialRefresh.promise
        if (agentCalls === 3) return olderReload.promise
        if (agentCalls === 4) return response(setupAgents)
        return response(setupAgentsB)
      }
      if (path === '/api/agents/check' && init?.method === 'POST') return response({ success: true })
      return response({ success: true })
    }))
    render(<App />)
    await userEvent.click(await screen.findByRole('button', { name: 'Agents & Setup' }))
    await screen.findByText('Claude Code')
    const refresh = within(screen.getByRole('region', { name: 'Configured Review Agents' })).getByRole('button', { name: 'Refresh' })
    await userEvent.click(refresh)
    await waitFor(() => expect(agentCalls).toBe(2))
    await userEvent.click(screen.getByRole('button', { name: 'Run check' }))
    await userEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Run check' }))
    await waitFor(() => expect(agentCalls).toBe(3))
    // The ordinary manual refresh is still in flight while the post-check
    // request runs. Background polling then creates g4/g5 without switching
    // repositories or clearing the stale A cards.
    initialRefresh.resolve(new Response(JSON.stringify(setupAgents), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await waitFor(() => expect(agentCalls).toBe(4))
    await waitFor(() => expect(agentCalls).toBe(5))
    olderReload.resolve(new Response(JSON.stringify(checkedAgents), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(screen.queryByText('Agent check completed for claude-code.')).not.toBeInTheDocument()
    expect(screen.queryByText('Passed')).not.toBeInTheDocument()
    expect(screen.getByText('Claude Code')).toBeInTheDocument()
    expect(screen.getByText('Not checked')).toBeInTheDocument()
  })

  it('does not let a background same-repository reload suppress health completion', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const backgroundAgents = deferred<Response>()
    const check = deferred<Response>()
    let runtimeCalls = 0
    let agentCalls = 0
    const checkedAgents = { ...setupAgents, configured: [{ ...setupAgents.configured[0], health: 'passed' }] }
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path === '/api/setup/roborev') return response(setupReady)
      if (path.startsWith('/api/panel-refines?')) return response(refineEnvelope([]))
      if (path.startsWith('/api/reviews?')) return response([])
      if (path === '/api/runtime-control') {
        runtimeCalls += 1
        return response(runtimeCalls === 1 ? { ...setupRuntime, operationActive: true, daemonOperation: 'starting' } : setupRuntime)
      }
      if (path === '/api/runtime') return response(bootstrap.roborev)
      if (path === '/api/agents') {
        agentCalls += 1
        if (agentCalls === 1) return response(setupAgents)
        if (agentCalls === 2) return backgroundAgents.promise
        return response(checkedAgents)
      }
      if (path === '/api/agents/check' && init?.method === 'POST') return check.promise
      return response({ success: true })
    }))
    render(<App />)
    await userEvent.click(await screen.findByRole('button', { name: 'Agents & Setup' }))
    await screen.findByText('Claude Code')
    await userEvent.click(screen.getByRole('button', { name: 'Run check' }))
    await userEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Run check' }))
    await waitFor(() => expect(agentCalls).toBe(2))
    const refresh = within(screen.getByRole('region', { name: 'Configured Review Agents' })).getByRole('button', { name: 'Refresh' })
    expect(refresh).toBeDisabled()
    backgroundAgents.resolve(new Response(JSON.stringify(setupAgents), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    check.resolve(new Response(JSON.stringify(checkedAgents), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    expect(await screen.findByText('Agent check completed for claude-code.')).toBeInTheDocument()
    expect(agentCalls).toBe(3)
  })

  it('does not report success or replace repository A cards when the fresh reload belongs to B', async () => {
    const check = deferred<Response>()
    let agentCalls = 0
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path === '/api/setup/roborev') return response(setupReady)
      if (path.startsWith('/api/panel-refines?')) return response(refineEnvelope([]))
      if (path.startsWith('/api/reviews?')) return response([])
      if (path === '/api/runtime-control') return response(setupRuntime)
      if (path === '/api/runtime') return response(bootstrap.roborev)
      if (path === '/api/agents') {
        agentCalls += 1
        return response(agentCalls === 1 ? setupAgents : setupAgentsB)
      }
      if (path === '/api/agents/check' && init?.method === 'POST') return check.promise
      return response({ success: true })
    }))
    render(<App />)
    await userEvent.click(await screen.findByRole('button', { name: 'Agents & Setup' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Run check' }))
    await userEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Run check' }))
    check.resolve(new Response(JSON.stringify(setupAgents), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await waitFor(() => expect(agentCalls).toBe(2))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(screen.getByText('Claude Code')).toBeInTheDocument()
    expect(screen.queryByText('Gemini CLI')).not.toBeInTheDocument()
    expect(screen.queryByText('Agent check completed for claude-code.')).not.toBeInTheDocument()
  })

  it('restores repository A agent cards after a failed selection bootstrap', async () => {
    const other = { identity: 'repo-2', name: 'other', path: 'E:\\other', branch: 'feature', head: '222222222222', headShort: '22222222', commitSubject: 'Other', dirty: false, hasConfig: true }
    const initial = { ...bootstrap, repositories: [...bootstrap.repositories, other] }
    let agentCalls = 0
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(initial)
      if (path === '/api/setup/roborev') return response(setupReady)
      if (path.startsWith('/api/panel-refines?')) return response(refineEnvelope([]))
      if (path.startsWith('/api/reviews?')) return response([])
      if (path === '/api/runtime-control') return response(setupRuntime)
      if (path === '/api/runtime') return response(bootstrap.roborev)
      if (path === '/api/repos/select' && init?.method === 'POST') return response({ error: 'selection rejected' }, 409)
      if (path === '/api/agents') { agentCalls += 1; return response(setupAgents) }
      return response({ success: true })
    }))
    render(<App />)
    await userEvent.click(await screen.findByRole('button', { name: 'Agents & Setup' }))
    await screen.findByText('Claude Code')
    fireEvent.change(screen.getByRole('combobox', { name: 'Current repository' }), { target: { value: other.identity } })
    await waitFor(() => expect(screen.getByText('selection rejected')).toBeInTheDocument())
    expect(await screen.findByText('Claude Code')).toBeInTheDocument()
    expect(agentCalls).toBeGreaterThanOrEqual(2)
  })

  it('does not report daemon success when the terminal runtime state contains an error', async () => {
    let runtimeControlCalls = 0
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path === '/api/setup/roborev') return response(setupReady)
      if (path.startsWith('/api/panel-refines?')) return response(refineEnvelope([]))
      if (path.startsWith('/api/reviews?')) return response([])
      if (path === '/api/agents') return response(setupAgents)
      if (path === '/api/runtime-control') {
        runtimeControlCalls += 1
        return response(runtimeControlCalls === 1 ? setupRuntime : runtimeControlCalls === 2 ? { ...setupRuntime, operationActive: true, daemonOperation: 'stopping' } : { ...setupRuntime, operationActive: false, error: 'daemon stop failed' })
      }
      if (path === '/api/runtime') return response(bootstrap.roborev)
      if (path === '/api/daemon/stop' && init?.method === 'POST') return response({ accepted: true }, 202)
      return response({ success: true })
    }))
    render(<App />)
    await userEvent.click(await screen.findByRole('button', { name: 'Agents & Setup' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Stop daemon' }))
    await userEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Stop daemon' }))
    await waitFor(() => expect(screen.getAllByText('daemon stop failed').some(element => element.closest('.toast.error'))).toBe(true))
    expect(screen.queryByText('RoboRev daemon stopped.')).not.toBeInTheDocument()
  })

  it('polls an accepted daemon stop and refreshes runtime, agents, panels, and reviews', async () => {
    const calls: Array<{ path: string; init?: RequestInit }> = []
    let runtimeControlCalls = 0
    let panelCalls = 0
    let reviewCalls = 0
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      calls.push({ path, init })
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path === '/api/setup/roborev') return response(setupReady)
      if (path.startsWith('/api/panel-refines?')) return response(refineEnvelope([]))
      if (path.startsWith('/api/reviews?')) { reviewCalls += 1; return response([]) }
      if (path === '/api/agents') return response(setupAgents)
      if (path === '/api/runtime-control') {
        runtimeControlCalls += 1
        return response(runtimeControlCalls === 1 ? setupRuntime : runtimeControlCalls === 2 ? { ...setupRuntime, operationActive: true, daemonOperation: 'stopping' } : { ...setupRuntime, daemonRunning: false, operationActive: false, queueState: 'unknown', canStart: true, canStop: false, canRestart: false })
      }
      if (path === '/api/runtime') return response({ ...bootstrap.roborev, status: { running: runtimeControlCalls < 3 } })
      if (path === '/api/daemon/stop' && init?.method === 'POST') return response({ accepted: true }, 202)
      return response({ success: true })
    }, () => { panelCalls += 1; return response(panelConfiguration()) }))
    render(<App />)
    await userEvent.click(await screen.findByRole('button', { name: 'Agents & Setup' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Stop daemon' }))
    expect(screen.getByRole('dialog')).toHaveTextContent('Active jobs may be interrupted; queued jobs remain queued.')
    expect(screen.getByRole('dialog')).toHaveTextContent(JSON.stringify(['C:\\Tools\\roborev.exe', 'daemon', 'stop']))
    const beforePanels = panelCalls
    const beforeReviews = reviewCalls
    await userEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Stop daemon' }))
    await waitFor(() => expect(calls.some(call => call.path === '/api/daemon/stop' && call.init?.method === 'POST')).toBe(true))
    await waitFor(() => expect(runtimeControlCalls).toBeGreaterThanOrEqual(4), { timeout: 4000 })
    await waitFor(() => expect(panelCalls).toBeGreaterThan(beforePanels))
    await waitFor(() => expect(reviewCalls).toBeGreaterThan(beforeReviews))
  })

  it('keeps polling an accepted daemon operation beyond ten seconds until terminal success', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const startedAt = Date.now()
    let runtimeControlCalls = 0
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path === '/api/setup/roborev') return response(setupReady)
      if (path.startsWith('/api/panel-refines?')) return response(refineEnvelope([]))
      if (path.startsWith('/api/reviews?')) return response([])
      if (path === '/api/agents') return response(setupAgents)
      if (path === '/api/runtime-control') {
        runtimeControlCalls += 1
        if (runtimeControlCalls === 1) return response(setupRuntime)
        return Date.now() - startedAt < 12_000 ? response({ ...setupRuntime, operationActive: true, daemonOperation: 'stopping' }) : response({ ...setupRuntime, daemonRunning: false, operationActive: false, queueState: 'unknown', canStart: true, canStop: false, canRestart: false })
      }
      if (path === '/api/runtime') return response({ ...bootstrap.roborev, status: { running: Date.now() - startedAt < 12_000 } })
      if (path === '/api/daemon/stop' && init?.method === 'POST') return response({ accepted: true }, 202)
      return response({ success: true })
    }))
    render(<App />)
    await userEvent.click(await screen.findByRole('button', { name: 'Agents & Setup' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Stop daemon' }))
    await userEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Stop daemon' }))
    await vi.advanceTimersByTimeAsync(10_500)
    expect(runtimeControlCalls).toBeGreaterThan(10)
    expect(screen.queryByText('RoboRev daemon stopped.')).not.toBeInTheDocument()
    await vi.advanceTimersByTimeAsync(3_000)
    await waitFor(() => expect(screen.getByText('RoboRev daemon stopped.')).toHaveClass('toast', 'success'))
  })

  it('uses a sanitized SSE event only to trigger the authoritative review GET', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    vi.stubGlobal('EventSource', FakeEventSource)
    const authoritative = deferred<Response>()
    const updated = { ...jobs[0], verdict: 'P', review: { output: '# Authoritative pass' } }
    let reviewCalls = 0
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/reviews?')) {
        reviewCalls += 1
        return reviewCalls === 1 ? response(jobs) : authoritative.promise
      }
      if (path === '/api/reviews/42') return response(jobs[0])
      return response({ success: true })
    }))
    render(<App />)
    expect(await screen.findByText(/#42/)).toBeInTheDocument()
    const source = FakeEventSource.instances[0]
    expect(source.url).toBe('/api/events?repositoryIdentity=repo-1')
    source.emit({ repositoryIdentity: 'repo-1', sequence: 1, kind: 'jobs-changed', verdict: 'P', output: 'event payload must not render' })
    await vi.advanceTimersByTimeAsync(75)
    await waitFor(() => expect(reviewCalls).toBe(2))
    expect(screen.queryByText('Pass')).not.toBeInTheDocument()
    authoritative.resolve(new Response(JSON.stringify([updated]), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    expect(await screen.findByText('Pass')).toBeInTheDocument()
    expect(screen.queryByText('event payload must not render')).not.toBeInTheDocument()
  })

  it('coalesces an event burst and allows only one trailing refresh after an in-flight GET', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    vi.stubGlobal('EventSource', FakeEventSource)
    const eventRefresh = deferred<Response>()
    let reviewCalls = 0
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/reviews?')) {
        reviewCalls += 1
        return reviewCalls === 1 || reviewCalls === 3 ? response(jobs) : eventRefresh.promise
      }
      return response({ success: true })
    }))
    render(<App />)
    expect(await screen.findByText(/#42/)).toBeInTheDocument()
    const source = FakeEventSource.instances[0]
    source.emit({ repositoryIdentity: 'repo-1', sequence: 1, kind: 'jobs-changed' })
    await vi.advanceTimersByTimeAsync(75)
    await waitFor(() => expect(reviewCalls).toBe(2))
    for (let sequence = 2; sequence <= 21; sequence += 1) source.emit({ repositoryIdentity: 'repo-1', sequence, kind: 'jobs-changed' })
    await vi.advanceTimersByTimeAsync(75)
    expect(reviewCalls).toBe(2)
    eventRefresh.resolve(new Response(JSON.stringify(jobs), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await waitFor(() => expect(reviewCalls).toBe(3))
    await vi.advanceTimersByTimeAsync(75)
    expect(reviewCalls).toBe(3)
  })

  it('closes the old source and ignores late old events and GET responses after a repository switch', async () => {
    const oldReview = deferred<Response>()
    const other = { identity: 'repo-2', name: 'other', path: 'E:\\other', branch: 'feature', head: '222222222222', headShort: '22222222', commitSubject: 'Other', dirty: false, hasConfig: true }
    const initial = { ...bootstrap, repositories: [...bootstrap.repositories, other] }
    const switched = { ...initial, selectedRepository: other.path, selectedRepositoryIdentity: other.identity }
    let bootstrapCalls = 0
    let oldReviewCalls = 0
    let newReviewCalls = 0
    vi.stubGlobal('EventSource', FakeEventSource)
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(++bootstrapCalls === 1 ? initial : switched)
      if (path === '/api/repos/select' && init?.method === 'POST') return response({ success: true })
      if (path.startsWith('/api/reviews?')) {
        if (path.includes(encodeURIComponent('E:\\other'))) { newReviewCalls += 1; return response([{ ...jobs[0], id: 84, commit_subject: 'Other repository review' }]) }
        oldReviewCalls += 1
        return oldReview.promise
      }
      return response({ success: true })
    }))
    render(<App />)
    const selector = await screen.findByRole('combobox', { name: 'Current repository' })
    await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1))
    const sourceA = FakeEventSource.instances[0]
    await waitFor(() => expect(oldReviewCalls).toBe(1))
    fireEvent.change(selector, { target: { value: other.identity } })
    await waitFor(() => expect(selector).toHaveValue(other.identity))
    await waitFor(() => expect(sourceA.closed).toBe(true))
    const callsAfterSwitch = oldReviewCalls
    sourceA.emit({ repositoryIdentity: 'repo-1', sequence: 99, kind: 'jobs-changed' })
    oldReview.resolve(new Response(JSON.stringify(jobs), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(oldReviewCalls).toBe(callsAfterSwitch)
    expect(screen.queryByText(/#42/)).not.toBeInTheDocument()
    expect(await screen.findByText(/#84/)).toBeInTheDocument()
    expect(newReviewCalls).toBeGreaterThan(0)
  })

  it('keeps repository A stream and polling alive when the switch is rejected', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const other = { identity: 'repo-2', name: 'other', path: 'E:\\other', branch: 'feature', head: '222222222222', headShort: '22222222', commitSubject: 'Other', dirty: false, hasConfig: true }
    const initial = { ...bootstrap, repositories: [...bootstrap.repositories, other] }
    let reviewCalls = 0
    vi.stubGlobal('EventSource', FakeEventSource)
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(initial)
      if (path === '/api/repos/select' && init?.method === 'POST') return response({ error: 'selection rejected' }, 409)
      if (path.startsWith('/api/reviews?')) { reviewCalls += 1; return response(jobs) }
      return response({ success: true })
    }))
    render(<App />)
    const selector = await screen.findByRole('combobox', { name: 'Current repository' })
    await waitFor(() => expect(FakeEventSource.instances).toHaveLength(1))
    const sourceA = FakeEventSource.instances[0]
    fireEvent.change(selector, { target: { value: other.identity } })
    await waitFor(() => expect(screen.getByText('selection rejected')).toBeInTheDocument())
    expect(selector).toHaveValue('repo-1')
    expect(sourceA.closed).toBe(false)
    const beforeEvent = reviewCalls
    sourceA.emit({ repositoryIdentity: 'repo-1', sequence: 1, kind: 'jobs-changed' })
    await vi.advanceTimersByTimeAsync(75)
    await waitFor(() => expect(reviewCalls).toBeGreaterThan(beforeEvent))
  })

  it('closes on SSE error, reconnects with bounded backoff, and keeps polling reviews', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    vi.stubGlobal('EventSource', FakeEventSource)
    let reviewCalls = 0
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/reviews?')) { reviewCalls += 1; return response(jobs) }
      return response({ success: true })
    }))
    render(<App />)
    expect(await screen.findByText(/#42/)).toBeInTheDocument()
    const sourceA = FakeEventSource.instances[0]
    sourceA.fail()
    expect(sourceA.closed).toBe(true)
    await vi.advanceTimersByTimeAsync(999)
    expect(FakeEventSource.instances).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(1)
    await waitFor(() => expect(FakeEventSource.instances).toHaveLength(2))
    await vi.advanceTimersByTimeAsync(4_000)
    expect(reviewCalls).toBeGreaterThan(1)
  })

  it.each([
    ['missing capability', { ...bootstrap, roborev: { ...bootstrap.roborev, capabilities: { ...bootstrap.roborev.capabilities, streamEvents: undefined } } }],
    ['explicitly disabled capability', { ...bootstrap, roborev: { ...bootstrap.roborev, capabilities: { ...bootstrap.roborev.capabilities, streamEvents: false } } }],
  ])('keeps polling without creating EventSource when stream capability is %s', async (_label, unsupported) => {
    vi.stubGlobal('EventSource', FakeEventSource)
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(unsupported)
      if (path.startsWith('/api/reviews?')) return response(jobs)
      return response({ success: true })
    }))
    render(<App />)
    expect(await screen.findByText(/#42/)).toBeInTheDocument()
    expect(FakeEventSource.instances).toHaveLength(0)
  })

  it('keeps the open detail comment form and one SSE subscription across locale changes', async () => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, 'en')
    vi.stubGlobal('EventSource', FakeEventSource)
    const calls: Array<{ path: string; method: string }> = []
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      calls.push({ path, method: init?.method ?? 'GET' })
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/reviews?')) return response(jobs)
      if (path === '/api/reviews/42') return response(jobs[0])
      return response({ success: true })
    }))
    render(<I18nProvider><App /></I18nProvider>)
    await userEvent.click(await screen.findByText(/#42/))
    await screen.findByRole('heading', { name: 'Initial dashboard checkpoint' })
    await userEvent.click(screen.getByRole('button', { name: 'Comments' }))
    const comment = screen.getByRole('textbox', { name: 'Comment' })
    await userEvent.type(comment, 'Preserve this draft')
    const beforeCalls = calls.length
    const source = FakeEventSource.instances[0]
    fireEvent.change(screen.getAllByLabelText('Select language')[0], { target: { value: 'ko' } })
    expect(screen.getByRole('button', { name: '댓글' })).toHaveClass('active')
    expect(screen.getByRole('textbox', { name: '댓글' })).toHaveValue('Preserve this draft')
    expect(FakeEventSource.instances).toHaveLength(1)
    expect(source.closed).toBe(false)
    expect(calls.length).toBe(beforeCalls)
    expect(calls.some(call => call.method === 'POST')).toBe(false)
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, 'en')
  })

  it.each(['reviewing', 'needs_attention'])('shows only a compact Reviews summary for an active or attention refine and navigates to Fix & Refine', async phase => {
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/reviews?')) return response(jobs)
      if (path.startsWith('/api/panel-refines?')) return response(refineEnvelope([refineRun({ phase })]))
      if (path.startsWith('/api/fixes?')) return response({ operations: [], operation: null })
      return response({ success: true })
    }))
    render(<I18nProvider><App /></I18nProvider>)
    await screen.findByText(/#42/)
    expect(screen.queryByRole('heading', { name: 'Panel Refine' })).not.toBeInTheDocument()
    await userEvent.click(await screen.findByRole('button', { name: 'Open Fix & Refine' }))
    expect(await screen.findByRole('heading', { name: 'Fix & Refine' })).toBeInTheDocument()
    expect(screen.getAllByRole('heading', { name: 'Panel Refine' }).length).toBeGreaterThan(0)
    expect(screen.queryByText('Checkpoint roadmap')).not.toBeInTheDocument()
  })

  it('preselects a parent finding in Fix & Refine without executing a fix', async () => {
    const calls: Array<{ path: string; method: string }> = []
    const fixPreview = { repositoryIdentity: 'repo-1', repositoryPath: 'E:\\repo', target: { jobId: 42, gitRef: jobs[0].git_ref, branch: 'main', jobType: 'synthesis', panelRole: 'synthesis', panelName: 'dashboard_cross' }, agent: 'codex', command: { executable: 'C:\\Tools\\roborev.exe', args: ['fix', '--agent', 'codex', '--quiet', '42'] }, startingHead: jobs[0].git_ref, startingBranch: 'main', previewToken: 'preview-42' }
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      calls.push({ path, method: init?.method ?? 'GET' })
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/reviews?')) return response(jobs)
      if (path === '/api/reviews/42') return response(jobs[0])
      if (path.startsWith('/api/fixes/preview')) return response(fixPreview)
      if (path.startsWith('/api/fixes?')) return response({ operations: [], operation: null })
      return response({ success: true })
    }))
    render(<I18nProvider><App /></I18nProvider>)
    await userEvent.click(await screen.findByText(/#42/))
    await userEvent.click(await screen.findByRole('button', { name: 'Fix' }))
    expect(await screen.findByRole('heading', { name: 'Fix & Refine' })).toBeInTheDocument()
    expect(await screen.findByRole('combobox', { name: 'Finding to fix' })).toHaveValue('42')
    expect(calls.filter(call => call.method === 'POST')).toHaveLength(0)
  })

  it('preselects an omitted-role single finding from Reviews without executing a fix', async () => {
    const single = { ...jobs[0], job_type: 'review', panel_role: undefined, panel_name: undefined, panel_run_uuid: undefined }
    const fixPreview = { repositoryIdentity: 'repo-1', repositoryPath: 'E:\\repo', target: { jobId: 42, gitRef: single.git_ref, branch: 'main', jobType: 'review', panelRole: '' }, agent: 'codex', command: { executable: 'C:\\Tools\\roborev.exe', args: ['fix', '--agent', 'codex', '--quiet', '42'] }, startingHead: single.git_ref, startingBranch: 'main', previewToken: 'preview-single-42' }
    const calls: Array<{ path: string; method: string }> = []
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      calls.push({ path, method: init?.method ?? 'GET' })
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/reviews?')) return response([single])
      if (path === '/api/reviews/42') return response(single)
      if (path.startsWith('/api/fixes/preview')) return response(fixPreview)
      if (path.startsWith('/api/fixes?')) return response({ operations: [], operation: null })
      return response({ success: true })
    }))
    render(<I18nProvider><App /></I18nProvider>)
    await userEvent.click(await screen.findByText(/#42/))
    await userEvent.click(await screen.findByRole('button', { name: 'Fix' }))
    expect(await screen.findByRole('heading', { name: 'Fix & Refine' })).toBeInTheDocument()
    expect(await screen.findByRole('combobox', { name: 'Finding to fix' })).toHaveValue('42')
    expect(calls.filter(call => call.method === 'POST')).toHaveLength(0)
  })

  it('hides executable Fix action while viewing a panel member', async () => {
    const parent = jobs[0]
    const member = { id: 43, job_type: 'review', panel_role: 'member', panel_name: parent.panel_name, panel_run_uuid: parent.panel_run_uuid, panel_member_index: 0, panel_member_name: 'Security reviewer', status: 'done', verdict: 'F', agent: 'codex' }
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/reviews?')) return response([parent])
      if (path === '/api/reviews/42') return response(parent)
      if (path.includes('/members/43')) return response(memberShow(member, 'member output'))
      if (path.startsWith('/api/panels/')) return response({ jobs: [member] })
      return response({ success: true })
    }))
    render(<I18nProvider><App /></I18nProvider>)
    await userEvent.click(await screen.findByText(/#42/))
    expect(await screen.findByRole('button', { name: 'Fix' })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Panel members' }))
    await userEvent.click(await screen.findByRole('button', { name: /Security reviewer/ }))
    await screen.findByText('member output')
    expect(screen.queryByRole('button', { name: 'Fix' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Confirm and run fix' })).not.toBeInTheDocument()
  })
})

describe('CommentBox', () => {
  it('accepts pasted multiline text and sends Comment & Close once', async () => {
    const mutate = vi.fn().mockResolvedValue(undefined)
    render(<CommentBox jobId={42} mutate={mutate} busy={false} />)
    const textarea = screen.getByLabelText('Comment')
    fireEvent.change(textarea, { target: { value: '첫 줄\n둘째 줄' } })
    await userEvent.click(screen.getByRole('button', { name: 'Comment & Close' }))
    await waitFor(() => expect(mutate).toHaveBeenCalledWith('/api/reviews/42/comment-close', { comment: '첫 줄\n둘째 줄' }))
  })
})
