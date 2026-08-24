import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from './i18n'
import { ReviewDetail } from './App'

const job = { id: 42, status: 'done', verdict: 'P', closed: false, output: 'review' }
const props = { job, jobs: [job], close: vi.fn(), mutate: vi.fn().mockResolvedValue(undefined), busy: false, canRerun: false, canLoadMembers: false, repoPath: 'E:\\repo', repoIdentity: 'repo-1', toast: vi.fn() }

function response(value: unknown, status = 200) {
  return Promise.resolve(new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } }))
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

function pendingUntilAborted(signal: AbortSignal | undefined): Promise<Response> {
  return new Promise((_, reject) => {
    if (!signal) {
      reject(new Error('lazy request did not receive an AbortSignal'))
      return
    }
    const abort = () => reject(new DOMException('The operation was aborted.', 'AbortError'))
    if (signal.aborted) abort()
    else signal.addEventListener('abort', abort, { once: true })
  })
}

afterEach(() => { cleanup(); vi.restoreAllMocks() })

describe('ReviewDetail lazy Prompt/Log ownership', () => {
  it('does not read content on mount, loads Prompt once, and copies exact empty-safe text', async () => {
    const clipboard = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue(undefined)
    let promptCalls = 0
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/reviews/42/prompt') { promptCalls++; return response({ text: 'Prompt\nwith exact bytes' }) }
      if (path === '/api/reviews/42/log') throw new Error('log must remain unloaded')
      return response({ ...job })
    }))
    render(<I18nProvider><ReviewDetail {...props} /></I18nProvider>)
    await waitFor(() => expect(screen.getByText('review')).toBeInTheDocument())
    expect(promptCalls).toBe(0)
    await userEvent.click(screen.getByRole('button', { name: 'Prompt' }))
    expect(await screen.findByText('Prompt')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Review' }))
    await userEvent.click(screen.getByRole('button', { name: 'Prompt' }))
    expect(promptCalls).toBe(1)
    await userEvent.click(screen.getByRole('button', { name: 'Copy' }))
    expect(clipboard).toHaveBeenCalledWith('Prompt\nwith exact bytes')
  })

  it('retries one failed Log request and keeps Prompt/Log sibling lifetimes independent', async () => {
    let logCalls = 0
    const logRetry = response({ text: 'retry log' })
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/reviews/42/log') { logCalls++; return logCalls === 1 ? Promise.reject(new Error('offline')) : logRetry }
      if (path === '/api/reviews/42/prompt') return response({ text: 'prompt' })
      return response({ ...job })
    }))
    render(<I18nProvider><ReviewDetail {...props} /></I18nProvider>)
    await waitFor(() => expect(screen.getByText('review')).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: 'Log' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('offline')
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('retry log')).toBeInTheDocument()
    expect(logCalls).toBe(2)
    await userEvent.click(screen.getByRole('button', { name: 'Prompt' }))
    expect(await screen.findByText('prompt')).toBeInTheDocument()
    expect(logCalls).toBe(2)
  })

  it('retries one failed Prompt request exactly once', async () => {
    let promptCalls = 0
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/reviews/42/prompt') {
        promptCalls++
        return promptCalls === 1 ? Promise.reject(new Error('prompt unavailable')) : response({ text: 'retried prompt' })
      }
      return response({ ...job })
    }))
    render(<I18nProvider><ReviewDetail {...props} /></I18nProvider>)
    await userEvent.click(await screen.findByRole('button', { name: 'Prompt' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('prompt unavailable')
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('retried prompt')).toBeInTheDocument()
    expect(promptCalls).toBe(2)
  })

  it('discards stale lazy responses after job switch and unmount', async () => {
    const first = deferred<Response>()
    const second = deferred<Response>()
    const unmountedLog = deferred<Response>()
    const firstJob = { ...job, id: 42 }
    const secondJob = { ...job, id: 99 }
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/reviews/42/prompt') return first.promise
      if (path === '/api/reviews/99/prompt') return second.promise
      if (path === '/api/reviews/99/log') return unmountedLog.promise
      return response({ ...job })
    }))
    const { rerender, unmount } = render(<I18nProvider><ReviewDetail {...props} job={firstJob} jobs={[firstJob]} /></I18nProvider>)
    await userEvent.click(await screen.findByRole('button', { name: 'Prompt' }))
    rerender(<I18nProvider><ReviewDetail {...props} job={secondJob} jobs={[secondJob]} /></I18nProvider>)
    await userEvent.click(await screen.findByRole('button', { name: 'Prompt' }))
    second.resolve(new Response(JSON.stringify({ text: 'second prompt' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    expect(await screen.findByText('second prompt')).toBeInTheDocument()
    first.resolve(new Response(JSON.stringify({ text: 'stale first prompt' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(screen.queryByText('stale first prompt')).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Log' }))
    unmount()
    unmountedLog.resolve(new Response(JSON.stringify({ text: 'late unmounted log' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(screen.queryByText('late unmounted log')).not.toBeInTheDocument()
  })

  it('passes lazy signals and aborts them on job switch, capability disable, and unmount', async () => {
    const promptSignals: AbortSignal[] = []
    const firstJob = { ...job, id: 42 }
    const secondJob = { ...job, id: 99 }
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/reviews/42/prompt' || path === '/api/reviews/99/prompt') {
        if (!init?.signal) throw new Error('lazy prompt signal missing')
        promptSignals.push(init.signal)
        return pendingUntilAborted(init.signal)
      }
      return response({ ...job })
    }))
    const { rerender, unmount } = render(<I18nProvider><ReviewDetail {...props} job={firstJob} jobs={[firstJob]} /></I18nProvider>)
    await userEvent.click(await screen.findByRole('button', { name: 'Prompt' }))
    await waitFor(() => expect(promptSignals).toHaveLength(1))

    rerender(<I18nProvider><ReviewDetail {...props} job={secondJob} jobs={[secondJob]} /></I18nProvider>)
    await waitFor(() => expect(promptSignals[0].aborted).toBe(true))
    await userEvent.click(await screen.findByRole('button', { name: 'Prompt' }))
    await waitFor(() => expect(promptSignals).toHaveLength(2))

    rerender(<I18nProvider><ReviewDetail {...props} job={secondJob} jobs={[secondJob]} canLoadPrompt={false} /></I18nProvider>)
    await waitFor(() => expect(promptSignals[1].aborted).toBe(true))
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()

    rerender(<I18nProvider><ReviewDetail {...props} job={secondJob} jobs={[secondJob]} canLoadPrompt /></I18nProvider>)
    await userEvent.click(await screen.findByRole('button', { name: 'Prompt' }))
    await waitFor(() => expect(promptSignals).toHaveLength(3))
    unmount()
    expect(promptSignals[2].aborted).toBe(true)
  })

  it('aborts an in-flight log before terminal refresh and ignores AbortError', async () => {
    const running = { ...job, status: 'running' }
    const terminal = { ...job, status: 'done', output: 'terminal review' }
    const logSignals: AbortSignal[] = []
    let logCalls = 0
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/reviews/42/log') {
        if (!init?.signal) throw new Error('lazy log signal missing')
        logSignals.push(init.signal)
        logCalls++
        if (logCalls === 1) return pendingUntilAborted(init.signal)
        return response({ text: 'terminal log' })
      }
      if (path === '/api/reviews/42') return response(terminal)
      return response({ ...running })
    }))
    const { rerender } = render(<I18nProvider><ReviewDetail {...props} job={running} jobs={[running]} /></I18nProvider>)
    await userEvent.click(await screen.findByRole('button', { name: 'Log' }))
    await waitFor(() => expect(logSignals).toHaveLength(1))

    rerender(<I18nProvider><ReviewDetail {...props} job={terminal} jobs={[terminal]} /></I18nProvider>)
    await waitFor(() => expect(logSignals).toHaveLength(2))
    expect(logSignals[0].aborted).toBe(true)
    expect(await screen.findByText('terminal log')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('keeps empty Prompt/Log successful states and copies the exact log text', async () => {
    const clipboard = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue(undefined)
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/reviews/42/prompt') return response({ text: '' })
      if (path === '/api/reviews/42/log') return response({ text: '' })
      return response({ ...job })
    }))
    render(<I18nProvider><ReviewDetail {...props} /></I18nProvider>)
    await userEvent.click(await screen.findByRole('button', { name: 'Prompt' }))
    expect(await screen.findByText('No Prompt is available.')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Log' }))
    expect(await screen.findByText('No Log is available.')).toBeInTheDocument()
    const copyButtons = screen.getAllByRole('button', { name: 'Copy' })
    await userEvent.click(copyButtons[copyButtons.length - 1])
    expect(clipboard).toHaveBeenCalledWith('')
  })

  it('does not reload Log when status is unchanged', async () => {
    let logCalls = 0
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/reviews/42/log') { logCalls++; return response({ text: 'stable log' }) }
      return response({ ...job })
    }))
    const { rerender } = render(<I18nProvider><ReviewDetail {...props} /></I18nProvider>)
    await userEvent.click(await screen.findByRole('button', { name: 'Log' }))
    expect(await screen.findByText('stable log')).toBeInTheDocument()
    rerender(<I18nProvider><ReviewDetail {...props} job={{ ...job, output: 'new review' }} jobs={[{ ...job, output: 'new review' }]} /></I18nProvider>)
    expect(logCalls).toBe(1)
  })

  it('reloads Log exactly once when a running job becomes terminal', async () => {
    const running = { ...job, status: 'running' }
    const terminal = { ...job, status: 'done', output: 'terminal review' }
    let logCalls = 0
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/reviews/42/log') {
        logCalls++
        return response({ text: logCalls === 1 ? 'running log' : 'terminal log' })
      }
      if (path === '/api/reviews/42') return response(logCalls > 1 ? terminal : running)
      return response({ ...running })
    }))
    const { rerender } = render(<I18nProvider><ReviewDetail {...props} job={running} jobs={[running]} /></I18nProvider>)
    await userEvent.click(await screen.findByRole('button', { name: 'Log' }))
    expect(await screen.findByText('running log')).toBeInTheDocument()
    expect(logCalls).toBe(1)
    rerender(<I18nProvider><ReviewDetail {...props} job={terminal} jobs={[terminal]} /></I18nProvider>)
    expect(await screen.findByText('terminal log')).toBeInTheDocument()
    expect(logCalls).toBe(2)
    rerender(<I18nProvider><ReviewDetail {...props} job={{ ...terminal, output: 'unchanged status' }} jobs={[{ ...terminal, output: 'unchanged status' }]} /></I18nProvider>)
    expect(logCalls).toBe(2)
  })

  it('does not issue transport when the corresponding capability is unsupported', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      calls.push(String(input))
      return response({ ...job })
    }))
    render(<I18nProvider><ReviewDetail {...props} canLoadPrompt={false} canLoadLog={false} /></I18nProvider>)
    await waitFor(() => expect(screen.getByText('review')).toBeInTheDocument())
    expect(screen.queryByRole('button', { name: 'Prompt' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Log' })).not.toBeInTheDocument()
    expect(calls).toEqual(['/api/reviews/42'])
  })
})
