import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { App, CommentBox } from './App'

const bootstrap = {
  csrfToken: 'csrf', selectedRepository: 'E:\\repo', selectedRepositoryIdentity: 'repo-1', repositories: [{ identity: 'repo-1', name: 'repo', path: 'E:\\repo', branch: 'main', head: 'abcdef123456', headShort: 'abcdef12', commitSubject: 'Initial', dirty: false, hasConfig: true }],
  roborev: { installed: true, version: 'v0.64.0', status: { running: true }, capabilities: { reviewEnqueueApi: true, frozenRerunApi: true, jobListApi: true, panelReview: true, panelMembersApi: true, promptLazyLoad: true, rawLog: true } },
}
const jobs = [{ id: 42, git_ref: 'abcdef123456', commit_subject: 'Initial dashboard checkpoint', branch: 'main', status: 'done', verdict: 'F', panel_name: 'dashboard_cross', panel_run_uuid: '986d3bfa-05da-4621-aeca-66f493a19ea3', enqueued_at: '2026-08-17T00:00:00Z', started_at: '2026-08-17T00:00:05Z', finished_at: '2026-08-17T00:00:30Z', review: { output: '# Finding\nActionable issue' } }]

function response(value: unknown, status = 200) { return Promise.resolve(new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } })) }
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => { resolve = resolvePromise; reject = rejectPromise })
  return { promise, resolve, reject }
}

afterEach(() => { cleanup(); vi.useRealTimers(); vi.restoreAllMocks() })

describe('App review workflow', () => {
  it('keeps Review HEAD distinct from frozen rerun', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
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
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => String(input) === '/api/bootstrap' ? response(bootstrap) : response(jobs)))
    render(<App />)
    expect((await screen.findAllByText('Finding')).length).toBeGreaterThan(0)
  })

  it('searches real commit subjects and normalized verdict labels and renders elapsed timestamps', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => String(input) === '/api/bootstrap' ? response(bootstrap) : response(jobs)))
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
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
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

  it('renders panel member refresh errors inline', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/reviews?')) return response(jobs)
      if (path === '/api/reviews/42') return response(jobs[0])
      if (path.includes('/api/panels/')) return response({ error: 'daemon unavailable' }, 502)
      return response({ success: true })
    }))
    render(<App />)
    await userEvent.click(await screen.findByText(/#42/))
    await userEvent.click(await screen.findByRole('button', { name: 'Panel members' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('daemon unavailable')
  })

  it('keeps the list job ID after loading a real show response', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
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
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
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
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => String(input) === '/api/bootstrap' ? response(unsupported) : response([])))
    render(<App />)
    expect(await screen.findByRole('button', { name: 'Review HEAD' })).toBeDisabled()
    expect(await screen.findByText('RoboRev upgrade required')).toBeInTheDocument()
    expect(await screen.findByText('No reviews found')).toBeInTheDocument()
  })

  it('renders the normal empty queue without polling when the daemon is offline', async () => {
    const offline = { ...bootstrap, roborev: { ...bootstrap.roborev, status: { running: false } } }
    let reviewCalls = 0
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
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
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
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
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
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
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
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
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
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
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/reviews?')) return failed.promise
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
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
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
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => String(input) === '/api/bootstrap' ? response(distinct) : response([])))
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
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
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

  it('uses the server-pinned Review HEAD target in the toast and repository metadata', async () => {
    const pinned = { ...bootstrap.repositories[0], branch: 'feature/fresh', head: 'fedcba987654', headShort: 'fedcba98', commitSubject: 'Fresh commit', dirty: true }
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path === '/api/reviews' && init?.method === 'POST') return response({ receipt: { job_id: 99 }, target: pinned })
      if (path.startsWith('/api/reviews?')) return response([])
      return response({ success: true })
    }))
    render(<App />)
    await userEvent.click(await screen.findByRole('button', { name: 'Review HEAD' }))
    expect(await screen.findByText('New HEAD review enqueued for fedcba98.')).toBeInTheDocument()
    expect(screen.getByText('feature/fresh')).toBeInTheDocument()
    expect(screen.getByText('fedcba98')).toBeInTheDocument()
    expect(screen.getByText('Dirty')).toBeInTheDocument()
    expect(screen.queryByText('RoboRev state updated.')).not.toBeInTheDocument()
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
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(++bootstrapCalls === 1 ? initial : switched)
      if (path === '/api/reviews' && init?.method === 'POST') { enqueueCalls++; return enqueue.promise }
      if (path === '/api/repos/select' && init?.method === 'POST') return response({ success: true })
      if (path.includes(encodeURIComponent('E:\\other'))) return response(otherJobs)
      if (path.startsWith('/api/reviews?')) return response(jobs)
      return response({ success: true })
    }))
    render(<App />)
    await userEvent.click(await screen.findByRole('button', { name: 'Review HEAD' }))
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
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path === '/api/reviews' && init?.method === 'POST') return response({ error: 'enqueue rejected' }, 409)
      if (path.startsWith('/api/reviews?')) { reviewCalls++; return response({ error: 'review refresh failed' }, 502) }
      if (path === '/api/runtime') { runtimeCalls++; return response({ error: 'runtime refresh failed' }, 502) }
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
