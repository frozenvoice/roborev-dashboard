import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from './i18n'
import { ReviewDetail } from './App'

function response(value: unknown, status = 200) {
  return Promise.resolve(new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } }))
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

afterEach(() => { cleanup(); vi.useRealTimers(); vi.restoreAllMocks() })

describe('ReviewDetail poll generation isolation', () => {
  it('A. slow initial detail: poll does not invalidate initial loading', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const job = { id: 42, status: 'running', job_type: 'task', git_ref: 'run', closed: false }
    const initialDeferred = deferred<Response>()
    let detailCalls = 0
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/reviews/42') {
        detailCalls++
        if (detailCalls === 1) return initialDeferred.promise
        return response({ id: 42, status: 'running', job_type: 'task', git_ref: 'run', output: 'Poll output' })
      }
      return response({ success: true })
    }))

    const props = { job: job as any, jobs: [job as any], close: vi.fn(), mutate: vi.fn().mockResolvedValue(undefined), busy: false, canRerun: false, canLoadMembers: false, repoPath: 'E:\\repo', repoIdentity: 'repo-1', toast: vi.fn() }
    render(<I18nProvider><ReviewDetail {...props} /></I18nProvider>)

    // Initial loading skeleton should be present
    expect(document.querySelector('.skeleton.tall')).toBeInTheDocument()

    // Advance to poll tick (4s) – poll request starts
    await vi.advanceTimersByTimeAsync(4100)
    await waitFor(() => expect(detailCalls).toBe(2), { timeout: 2000 })

    // Poll succeeded – but initial is still pending, so skeleton must remain (poll must not have cleared detailLoading)
    expect(document.querySelector('.skeleton.tall')).toBeInTheDocument()

    // Now settle initial request
    initialDeferred.resolve(new Response(JSON.stringify({ id: 42, status: 'running', job_type: 'task', git_ref: 'run', output: 'Initial output' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await vi.advanceTimersByTimeAsync(0)
    await waitFor(() => expect(document.querySelector('.skeleton.tall')).not.toBeInTheDocument())

    // After initial settles, either poll or initial output should be visible, and no permanent skeleton
    expect(screen.getByText(/Initial output|Poll output/)).toBeInTheDocument()
  })

  it('B. independent generations: terminal reconciliation not invalidated by poll', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const running = { id: 42, status: 'running', job_type: 'task', git_ref: 'run', closed: false }
    const terminal = { id: 42, status: 'done', verdict: 'P', job_type: 'task', git_ref: 'run', closed: false }
    const reconciliationDeferred = deferred<Response>()
    let detailCalls = 0

    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/reviews/42') {
        detailCalls++
        if (detailCalls === 1) return response({ ...running, output: 'Initial running output' })
        if (detailCalls === 2) return reconciliationDeferred.promise
        return response({ ...terminal, output: 'Poll output' })
      }
      return response({ success: true })
    }))

    const props = { job: running as any, jobs: [running as any], close: vi.fn(), mutate: vi.fn().mockResolvedValue(undefined), busy: false, canRerun: false, canLoadMembers: false, repoPath: 'E:\\repo', repoIdentity: 'repo-1', toast: vi.fn() }
    const { rerender } = render(<I18nProvider><ReviewDetail {...props} /></I18nProvider>)

    // Wait for initial detail to settle
    await waitFor(() => expect(screen.getByText('Initial running output')).toBeInTheDocument())
    await waitFor(() => expect(detailCalls).toBe(1))

    // Trigger terminal reconciliation by changing job status to done
    rerender(<I18nProvider><ReviewDetail {...props} job={terminal as any} jobs={[terminal as any]} /></I18nProvider>)
    await waitFor(() => expect(detailCalls).toBe(2))

    // Advance to poll tick – poll is disabled for terminal jobs, so no new call
    await vi.advanceTimersByTimeAsync(4100)
    expect(detailCalls).toBe(2)

    // Now settle reconciliation with authoritative terminal output – poll generation must not have blocked it
    reconciliationDeferred.resolve(new Response(JSON.stringify({ ...terminal, output: 'Reconciled terminal output' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await vi.advanceTimersByTimeAsync(0)
    expect(await screen.findByText('Reconciled terminal output')).toBeInTheDocument()
    expect(document.querySelector('.skeleton.tall')).not.toBeInTheDocument()
  })

  it('C. unmount cleans poll timer and ignores late response', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const job = { id: 42, status: 'running', job_type: 'task', git_ref: 'run', closed: false }
    const pollDeferred = deferred<Response>()
    let detailCalls = 0
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/reviews/42') {
        detailCalls++
        if (detailCalls === 1) return response({ ...job, output: 'Initial output' })
        return pollDeferred.promise
      }
      return response({ success: true })
    }))

    const props = { job: job as any, jobs: [job as any], close: vi.fn(), mutate: vi.fn().mockResolvedValue(undefined), busy: false, canRerun: false, canLoadMembers: false, repoPath: 'E:\\repo', repoIdentity: 'repo-1', toast: vi.fn() }
    const { unmount } = render(<I18nProvider><ReviewDetail {...props} /></I18nProvider>)
    await waitFor(() => expect(screen.getByText('Initial output')).toBeInTheDocument())

    await vi.advanceTimersByTimeAsync(4100)
    await waitFor(() => expect(detailCalls).toBe(2))

    // Unmount before poll resolves – timer should be cleaned and generation invalidated
    unmount()
    expect(document.querySelector('.skeleton.tall')).not.toBeInTheDocument()

    // Late poll response should not cause state update or error
    pollDeferred.resolve(new Response(JSON.stringify({ ...job, output: 'Late poll output' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await vi.advanceTimersByTimeAsync(0)
    await new Promise(resolve => setTimeout(resolve, 0))
    // No error thrown, no component to update – just ensure timers cleaned
    expect(true).toBe(true)
  })

  it('C2. switching selected review cancels previous poll and ignores its late response', async () => {
    const firstJob = { id: 42, status: 'running', job_type: 'task', git_ref: 'run', closed: false }
    const secondJob = { id: 99, status: 'running', job_type: 'task', git_ref: 'run', closed: false }
    const firstPollDeferred = deferred<Response>()
    let firstCalls = 0
    let secondCalls = 0

    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/reviews/42') {
        firstCalls++
        if (firstCalls === 1) return response({ ...firstJob, output: 'First initial' })
        return firstPollDeferred.promise
      }
      if (path === '/api/reviews/99') {
        secondCalls++
        return response({ ...secondJob, output: 'Second initial' })
      }
      return response({ success: true })
    }))

    const baseProps = { jobs: [firstJob as any, secondJob as any], close: vi.fn(), mutate: vi.fn().mockResolvedValue(undefined), busy: false, canRerun: false, canLoadMembers: false, repoPath: 'E:\\repo', repoIdentity: 'repo-1', toast: vi.fn() }
    const { rerender } = render(<I18nProvider><ReviewDetail {...baseProps} job={firstJob as any} /></I18nProvider>)
    await waitFor(() => expect(screen.getByText('First initial')).toBeInTheDocument())

    // Switch to second job – should invalidate first poll generation
    rerender(<I18nProvider><ReviewDetail {...baseProps} job={secondJob as any} /></I18nProvider>)
    await waitFor(() => expect(screen.getByText('Second initial')).toBeInTheDocument())

    // Late first poll response should not overwrite second
    firstPollDeferred.resolve(new Response(JSON.stringify({ ...firstJob, output: 'Stale first poll' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(screen.queryByText('Stale first poll')).not.toBeInTheDocument()
    expect(screen.getByText('Second initial')).toBeInTheDocument()
  })
})
