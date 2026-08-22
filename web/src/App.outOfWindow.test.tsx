import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { App } from './App'
import { I18nProvider } from './i18n'
import { TASK_HISTORY_LIMIT } from './api'

// Reuse helpers from App.test pattern.

const bootstrapBase = {
  csrfToken: 'csrf',
  selectedRepository: 'E:\\repo',
  selectedRepositoryIdentity: 'repo-1',
  repositories: [{ identity: 'repo-1', name: 'repo', path: 'E:\\repo', branch: 'main', head: 'abcdef123456', headShort: 'abcdef12', commitSubject: 'Initial', dirty: false, hasConfig: true }],
  roborev: { installed: true, version: 'v0.64.0', status: { running: true }, capabilities: { reviewEnqueueApi: true, frozenRerunApi: true, jobListApi: true, jobOutputStatusApi: true, panelReview: true, fixCommand: true, panelMembersApi: true, promptLazyLoad: true, rawLog: true, streamEvents: false } },
}

function response(value: unknown, status = 200) {
  return Promise.resolve(new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } }))
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

function panelConfiguration() {
  return { repositoryIdentity: 'repo-1', panels: [{ name: 'reviewers', kind: 'panel', members: ['codex'], origin: 'global', valid: true }], selectedPanel: 'reviewers', selectionRequired: false }
}

function withPanels(handler: (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>) {
  return vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const path = String(input)
    if (path.startsWith('/api/repos/panels?')) return response(panelConfiguration())
    return handler(input, init)
  })
}

afterEach(() => { cleanup(); vi.restoreAllMocks() })

describe('out-of-window selected review mutation', () => {
  it('A. normal in-window review Close updates authoritative list row', async () => {
    const job = { id: 42, git_ref: 'abcdef123456', commit_subject: 'Initial', branch: 'main', status: 'done', verdict: 'F', job_type: 'review', closed: false, enqueued_at: '2026-08-17T00:00:00Z', started_at: '2026-08-17T00:00:05Z', finished_at: '2026-08-17T00:00:30Z', review: { output: '# Finding' } }
    let closed = false
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrapBase)
      if (path.startsWith('/api/reviews?') && !path.includes(`limit=${TASK_HISTORY_LIMIT}`)) {
        return response([{ ...job, closed }])
      }
      if (path.startsWith('/api/reviews?') && path.includes(`limit=${TASK_HISTORY_LIMIT}`)) return response([{ ...job, closed }])
      if (path === '/api/reviews/42') return response({ ...job, closed })
      if (path === '/api/reviews/42/close' && init?.method === 'POST') { closed = true; return response({ success: true }) }
      return response({ success: true })
    }))

    render(<I18nProvider><App /></I18nProvider>)
    await waitFor(() => expect(screen.getByText(/#42/)).toBeInTheDocument())
    await userEvent.click(screen.getByText(/#42/))
    expect(await screen.findByRole('button', { name: 'Close' })).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: 'Close' }))

    await waitFor(() => expect(screen.getByRole('button', { name: 'Reopen' })).toBeInTheDocument())
    expect(screen.queryByRole('button', { name: 'Close' })).not.toBeInTheDocument()
  })

  it('B. task outside 100 list Close fetches authoritative detail when absent from recent list', async () => {
    const taskJob = { id: 91, uuid: '550e8400-e29b-41d4-a716-446655440091', job_type: 'task', git_ref: 'run', status: 'done', verdict: 'F', closed: false, branch: 'main', review: { output: '# Task finding' }, enqueued_at: '2026-08-17T00:00:00Z', started_at: '2026-08-17T00:00:05Z', finished_at: '2026-08-17T00:00:30Z' }
    const recentReview = { id: 42, git_ref: 'abcdef123456', status: 'done', verdict: 'P', closed: false }
    let taskClosed = false

    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrapBase)
      if (path.startsWith('/api/reviews?') && path.includes(`limit=${TASK_HISTORY_LIMIT}`)) {
        return response([{ ...taskJob, closed: taskClosed }])
      }
      if (path.startsWith('/api/reviews?') && !path.includes(`limit=${TASK_HISTORY_LIMIT}`)) {
        return response([recentReview])
      }
      if (path === '/api/reviews/91/close' && init?.method === 'POST') {
        taskClosed = true
        return response({ success: true })
      }
      if (path === '/api/reviews/91') {
        return response({ ...taskJob, closed: taskClosed, output: '# Task finding', review: { output: '# Task finding' } })
      }
      return response({ success: true })
    }))

    render(<I18nProvider><App /></I18nProvider>)

    // Wait for overview/reviews to load recent list (does not contain 91)
    await waitFor(() => expect(screen.getByText(/#42/)).toBeInTheDocument())

    // Navigate to Tasks and open the out-of-window task
    await userEvent.click(screen.getByRole('button', { name: 'Tasks' }))
    // Wait for history to load and show View result for #91
    const viewButton = await screen.findByRole('button', { name: 'View result' })
    expect(viewButton).toBeInTheDocument()

    // Clicking View result opens ReviewDetail for 91 via App.openReviewById (which GETs /api/reviews/91)
    await userEvent.click(viewButton)

    // The task's detail should show Close (since closed false and terminal)
    expect(await screen.findByRole('button', { name: 'Close' })).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: 'Close' }))

    // After POST success + recent refresh (still not containing 91) + fresh GET (closed:true), detail should flip to Reopen
    await waitFor(() => expect(screen.getByRole('button', { name: 'Reopen' })).toBeInTheDocument())
    expect(screen.queryByRole('button', { name: 'Close' })).not.toBeInTheDocument()
  })

  it('C. out-of-window Reopen restores Close after fresh detail', async () => {
    const taskJob = { id: 92, uuid: '550e8400-e29b-41d4-a716-446655440092', job_type: 'task', git_ref: 'run', status: 'done', verdict: 'F', closed: true, branch: 'main', review: { output: '# Task finding closed' } }
    let taskClosed = true
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrapBase)
      if (path.startsWith('/api/reviews?') && path.includes(`limit=${TASK_HISTORY_LIMIT}`)) return response([{ ...taskJob, closed: taskClosed }])
      if (path.startsWith('/api/reviews?') && !path.includes(`limit=${TASK_HISTORY_LIMIT}`)) return response([])
      if (path === '/api/reviews/92') return response({ ...taskJob, closed: taskClosed, output: '# Task finding', review: { output: '# Task finding' } })
      if (path === '/api/reviews/92/reopen' && init?.method === 'POST') { taskClosed = false; return response({ success: true }) }
      return response({ success: true })
    }))

    render(<I18nProvider><App /></I18nProvider>)
    await userEvent.click(await screen.findByRole('button', { name: 'Tasks' }))
    const viewButton = await screen.findByRole('button', { name: 'View result' })
    await userEvent.click(viewButton)

    expect(await screen.findByRole('button', { name: 'Reopen' })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Reopen' }))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Close' })).toBeInTheDocument())
    expect(screen.queryByRole('button', { name: 'Reopen' })).not.toBeInTheDocument()
  })

  it('D. stale out-of-window response does not overwrite newly selected review', async () => {
    const taskA = { id: 93, uuid: '550e8400-e29b-41d4-a716-446655440093', job_type: 'task', git_ref: 'run', status: 'done', verdict: 'F', closed: false, branch: 'main', review: { output: '# Task A' } }
    const reviewB = { id: 44, git_ref: 'abcdef123456', commit_subject: 'Review B', branch: 'main', status: 'done', verdict: 'P', closed: false, review: { output: '# Review B' } }
    let taskAClosed = false
    const freshADeferred = deferred<Response>()
    let freshAFetchStarted = false

    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrapBase)
      if (path.startsWith('/api/reviews?') && path.includes(`limit=${TASK_HISTORY_LIMIT}`)) return response([taskA])
      if (path.startsWith('/api/reviews?') && !path.includes(`limit=${TASK_HISTORY_LIMIT}`)) return response([reviewB])
      if (path === '/api/reviews/93') {
        if (freshAFetchStarted) return freshADeferred.promise
        return response({ ...taskA, closed: taskAClosed, output: '# Task A', review: { output: '# Task A' } })
      }
      if (path === '/api/reviews/44') return response({ ...reviewB, output: '# Review B', review: { output: '# Review B' } })
      if (path === '/api/reviews/93/close' && init?.method === 'POST') {
        taskAClosed = true
        freshAFetchStarted = true
        return response({ success: true })
      }
      return response({ success: true })
    }))

    render(<I18nProvider><App /></I18nProvider>)
    await waitFor(() => expect(screen.getByText(/#44/)).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: 'Tasks' }))
    const viewButtons = await screen.findAllByRole('button', { name: 'View result' })
    await userEvent.click(viewButtons[0])

    expect(await screen.findByRole('heading', { name: /Task A|Review B/ })).toBeInTheDocument()
    // Detail for A shows Close
    expect(await screen.findByRole('button', { name: 'Close' })).toBeInTheDocument()

    // Start mutation for A (will trigger fresh fetch that stays pending)
    const closePromise = userEvent.click(screen.getByRole('button', { name: 'Close' }))

    // Before fresh fetch resolves, switch to B via review list
    // Need to navigate back to Reviews and select B
    await userEvent.click(screen.getByRole('button', { name: 'Reviews' }))
    await waitFor(() => expect(screen.getByText(/#44/)).toBeInTheDocument())
    await userEvent.click(screen.getByText(/#44/))

    expect((await screen.findAllByText('Review B')).length).toBeGreaterThanOrEqual(1)

    // Now resolve stale A fresh fetch with closed:true – it must NOT overwrite B
    freshADeferred.resolve(new Response(JSON.stringify({ ...taskA, closed: true, output: '# Task A closed', review: { output: '# Task A closed' } }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await new Promise(resolve => setTimeout(resolve, 0))

    // Still showing B, not A
    expect((await screen.findAllByText('Review B')).length).toBeGreaterThanOrEqual(1)
    expect(screen.queryByText('Task A closed')).not.toBeInTheDocument()
    // B's detail should not have been switched to closed state of A
    expect(screen.getAllByRole('heading', { name: 'Review B' }).length).toBeGreaterThanOrEqual(1)
  })

  it('E. invalid selected IDs are rejected by positiveJobId helper', async () => {
    const { positiveJobId } = await import('./App')
    expect(positiveJobId(undefined)).toBeNull()
    expect(positiveJobId(null)).toBeNull()
    expect(positiveJobId('')).toBeNull()
    expect(positiveJobId(NaN)).toBeNull()
    expect(positiveJobId(0)).toBeNull()
    expect(positiveJobId(-1)).toBeNull()
    expect(positiveJobId(-5)).toBeNull()
    expect(positiveJobId(Number.MAX_SAFE_INTEGER + 1)).toBeNull()
    expect(positiveJobId('abc')).toBeNull()
    expect(positiveJobId('   ')).toBeNull()
    expect(positiveJobId(42)).toBe(42)
    expect(positiveJobId('42')).toBe(42)
    expect(positiveJobId(' 42 ')).toBe(42)
    // fresh fetch guard integration: App's mutate would skip these, so verify no NaN fetch occurs via direct helper check
  })

  it('E2. fresh response ID mismatch is ignored', async () => {
    const taskJob = { id: 96, job_type: 'task', git_ref: 'run', status: 'done', verdict: 'F', closed: false, branch: 'main', review: { output: '# Task' } }
    const recent = { id: 42, git_ref: 'abcdef123456', status: 'done', verdict: 'P', closed: false }
    let taskClosed = false
    let detailCalls = 0
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrapBase)
      if (path.startsWith('/api/reviews?') && path.includes(`limit=${TASK_HISTORY_LIMIT}`)) return response([{ ...taskJob, closed: taskClosed }])
      if (path.startsWith('/api/reviews?') && !path.includes(`limit=${TASK_HISTORY_LIMIT}`)) return response([recent])
      if (path === '/api/reviews/96/close' && init?.method === 'POST') { taskClosed = true; return response({ success: true }) }
      if (path === '/api/reviews/96') {
        detailCalls++
        if (detailCalls === 1) {
          return response({ ...taskJob, closed: false, output: '# Task', review: { output: '# Task' } })
        }
        return response({ ...taskJob, id: 999, job_id: 999, closed: true, output: '# Mismatched', review: { output: '# Mismatched' } })
      }
      return response({ success: true })
    }))
    render(<I18nProvider><App /></I18nProvider>)
    await waitFor(() => expect(screen.getByText(/#42/)).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: 'Tasks' }))
    const viewButton = await screen.findByRole('button', { name: 'View result' })
    await userEvent.click(viewButton)
    expect(await screen.findByRole('button', { name: 'Close' })).toBeInTheDocument()
    expect(await screen.findByText('Task')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Close' }))
    await new Promise(resolve => setTimeout(resolve, 500))
    expect(screen.getByRole('button', { name: 'Close' })).toBeInTheDocument()
    expect(screen.queryByText('Mismatched')).not.toBeInTheDocument()
  })

  it('F. valid positive ID still triggers authoritative fetch when out-of-window', async () => {
    const validJob = { id: 95, job_type: 'task', git_ref: 'run', status: 'done', verdict: 'F', closed: false, branch: 'main', review: { output: '# Valid' } }
    const recent = { id: 42, git_ref: 'abcdef123456', status: 'done', verdict: 'P', closed: false }
    let validClosed = false
    const calls: string[] = []
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      calls.push(path)
      if (path === '/api/bootstrap') return response(bootstrapBase)
      if (path.startsWith('/api/reviews?') && path.includes(`limit=${TASK_HISTORY_LIMIT}`)) return response([{ ...validJob, closed: validClosed }])
      if (path.startsWith('/api/reviews?') && !path.includes(`limit=${TASK_HISTORY_LIMIT}`)) return response([recent])
      if (path === '/api/reviews/95/close' && init?.method === 'POST') { validClosed = true; return response({ success: true }) }
      if (path === '/api/reviews/95') return response({ ...validJob, closed: validClosed, output: '# Valid', review: { output: '# Valid' } })
      return response({ success: true })
    }))
    render(<I18nProvider><App /></I18nProvider>)
    await waitFor(() => expect(screen.getByText(/#42/)).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: 'Tasks' }))
    const viewButton = await screen.findByRole('button', { name: 'View result' })
    await userEvent.click(viewButton)
    expect(await screen.findByRole('button', { name: 'Close' })).toBeInTheDocument()
    calls.length = 0
    await userEvent.click(screen.getByRole('button', { name: 'Close' }))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Reopen' })).toBeInTheDocument())
    expect(calls.some(c => c === '/api/reviews/95')).toBe(true)
  })

  it('G. fresh response with id/job_id precedence still applies close', async () => {
    const taskJob = { id: 97, job_id: 97, job_type: 'task', git_ref: 'run', status: 'done', verdict: 'F', closed: false, branch: 'main', review: { output: '# Task' } }
    const recent = { id: 42, git_ref: 'abcdef123456', status: 'done', verdict: 'P', closed: false }
    let taskClosed = false
    let detailCalls = 0
    vi.stubGlobal('fetch', withPanels((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrapBase)
      if (path.startsWith('/api/reviews?') && path.includes(`limit=${TASK_HISTORY_LIMIT}`)) return response([{ ...taskJob, closed: taskClosed }])
      if (path.startsWith('/api/reviews?') && !path.includes(`limit=${TASK_HISTORY_LIMIT}`)) return response([recent])
      if (path === '/api/reviews/97/close' && init?.method === 'POST') { taskClosed = true; return response({ success: true }) }
      if (path === '/api/reviews/97') {
        detailCalls++
        if (detailCalls === 1) {
          return response({ ...taskJob, closed: false, output: '# Task', review: { output: '# Task' } })
        }
        // Return mismatched top-level id (review row) but correct job_id
        return response({ id: 12345, job_id: 97, closed: true, output: '# Task closed', review: { output: '# Task closed' }, job: { id: 97 } } as any)
      }
      return response({ success: true })
    }))
    render(<I18nProvider><App /></I18nProvider>)
    await waitFor(() => expect(screen.getByText(/#42/)).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: 'Tasks' }))
    const viewButton = await screen.findByRole('button', { name: 'View result' })
    await userEvent.click(viewButton)
    expect(await screen.findByRole('button', { name: 'Close' })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Close' }))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Reopen' })).toBeInTheDocument())
    expect(screen.queryByRole('button', { name: 'Close' })).not.toBeInTheDocument()
  })
})
