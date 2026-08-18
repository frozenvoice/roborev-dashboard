import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { App, CommentBox } from './App'
import type { PanelInfo } from './api'

const bootstrap = {
  csrfToken: 'csrf', selectedRepository: 'E:\\repo', selectedRepositoryIdentity: 'repo-1', repositories: [{ identity: 'repo-1', name: 'repo', path: 'E:\\repo', branch: 'main', head: 'abcdef123456', headShort: 'abcdef12', commitSubject: 'Initial', dirty: false, hasConfig: true }],
  roborev: { installed: true, version: 'v0.64.0', status: { running: true }, capabilities: { reviewEnqueueApi: true, frozenRerunApi: true, jobListApi: true, jobOutputStatusApi: true, panelReview: true, fixCommand: true, panelMembersApi: true, promptLazyLoad: true, rawLog: true } },
}
const jobs = [{ id: 42, git_ref: 'abcdef123456', commit_subject: 'Initial dashboard checkpoint', branch: 'main', status: 'done', verdict: 'F', panel_name: 'dashboard_cross', panel_run_uuid: '986d3bfa-05da-4621-aeca-66f493a19ea3', enqueued_at: '2026-08-17T00:00:00Z', started_at: '2026-08-17T00:00:05Z', finished_at: '2026-08-17T00:00:30Z', review: { output: '# Finding\nActionable issue' } }]
const defaultPanel: PanelInfo = { name: 'reviewers', kind: 'panel', members: ['codex', 'claude-code'], origin: 'global', valid: true }
const refineOptions = { executable: 'C:\\Tools\\roborev.exe', args: ['fix', '--agent', 'codex', '--quiet', '<synthesisJobID>'], allowedFixAgents: ['codex'], defaultFixAgent: 'codex', defaultMaxFixAttempts: 3, minFixAttempts: 1, maxFixAttempts: 10 }
function refineRun(overrides: Record<string, unknown> = {}) {
  return { id: 'refine-1', repositoryIdentity: 'repo-1', repositoryPath: 'E:\\repo', branch: 'main', iterationPanel: 'dashboard_cross', fixAgent: 'codex', maxFixAttempts: 3, fixAttempts: 0, phase: 'reviewing', startingHead: 'abcdef123456', currentHead: 'abcdef123456', currentReviewJobId: 42, iterations: [], command: refineOptions, ...overrides }
}
function refineEnvelope(runs: unknown[] = [refineRun()], options = refineOptions) { return { runs, options } }

function response(value: unknown, status = 200) { return Promise.resolve(new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } })) }
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

afterEach(() => { cleanup(); vi.useRealTimers(); vi.restoreAllMocks() })

describe('App review workflow', () => {
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
        { id: 43, panel_role: 'member', panel_member_index: 0, status: 'done', verdict: 'F', agent: 'codex' },
        { id: 44, panel_role: 'member', panel_member_index: 1, status: 'done', verdict: 'P', agent: 'claude-code' },
        { id: 45, panel_role: 'member', panel_member_index: 2, status: 'running', agent: 'grok' },
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

  it('copies and opens a finding member fix prompt across a routine member refresh', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const clipboard = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue(undefined)
    const slowOpen = deferred<Response>()
    const calls: Array<{ path: string; method: string }> = []
    let promptCalls = 0
    let memberCalls = 0
    const members = { jobs: [
      { id: 43, panel_role: 'member', panel_member_index: 0, status: 'done', verdict: 'F', agent: 'codex' },
      { id: 44, panel_role: 'member', panel_member_index: 1, status: 'done', verdict: 'P', agent: 'claude-code' },
      { id: 45, panel_role: 'member', panel_member_index: 2, status: 'running', agent: 'grok' },
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
          ? [{ id: 43, panel_role: 'member', status: 'done', verdict: 'F', agent: 'codex' }, { id: 45, panel_role: 'member', status: 'running', agent: 'grok' }]
          : [{ id: 45, panel_role: 'member', status: 'running', agent: 'grok' }] })
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
    expect(screen.getByRole('combobox', { name: 'Review panel' })).toHaveValue('alpha')
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
    await screen.findByText('Reviewing')
    await userEvent.click(screen.getByRole('button', { name: 'Cancel Panel Refine' }))
    expect(await screen.findByText('cancel rejected')).toHaveClass('toast', 'error')
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
