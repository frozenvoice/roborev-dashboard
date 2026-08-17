import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { App, CommentBox } from './App'

const bootstrap = {
  csrfToken: 'csrf', selectedRepository: 'E:\\repo', repositories: [{ name: 'repo', path: 'E:\\repo', branch: 'main', head: 'abcdef123456', headShort: 'abcdef12', commitSubject: 'Initial', dirty: false, hasConfig: true }],
  roborev: { installed: true, version: 'v0.64.0', status: { running: true }, capabilities: { reviewEnqueueApi: true, frozenRerunApi: true, jobListApi: true, panelReview: true, panelMembersApi: true, promptLazyLoad: true, rawLog: true } },
  reviewHeadCommand: { executable: 'roborev.exe', args: ['review', 'abcdef123456', '--panel', 'dashboard_cross'] },
}
const jobs = [{ id: 42, git_ref: 'abcdef123456', commit_subject: 'Initial dashboard checkpoint', branch: 'main', status: 'done', verdict: 'F', panel_name: 'dashboard_cross', panel_run_uuid: '986d3bfa-05da-4621-aeca-66f493a19ea3', enqueued_at: '2026-08-17T00:00:00Z', started_at: '2026-08-17T00:00:05Z', finished_at: '2026-08-17T00:00:30Z', review: { output: '# Finding\nActionable issue' } }]

function response(value: unknown, status = 200) { return Promise.resolve(new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } })) }

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
    expect(await screen.findByRole('button', { name: 'Review HEAD' })).toBeEnabled()
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
    const running = { ...jobs[0], status: 'running', verdict: undefined }
    const done = { ...jobs[0], status: 'done', verdict: 'P' }
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(bootstrap)
      if (path.startsWith('/api/reviews?')) { listCalls++; return response(listCalls === 1 ? [running] : [done]) }
      if (path === '/api/reviews/42') return response(listCalls === 1 ? running : done)
      return response({ success: true })
    }))
    render(<App />)
    fireEvent.click(await screen.findByText(/#42/))
    expect(await screen.findByRole('button', { name: 'Cancel' })).toBeInTheDocument()
    await vi.advanceTimersByTimeAsync(4100)
    expect((await screen.findAllByText('Pass')).length).toBeGreaterThan(0)
    expect(screen.queryByRole('button', { name: 'Cancel' })).not.toBeInTheDocument()
  })

  it('disables Review HEAD when the enqueue capability is unavailable', async () => {
    const unsupported = { ...bootstrap, roborev: { ...bootstrap.roborev, capabilities: { ...bootstrap.roborev.capabilities, reviewEnqueueApi: false } } }
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => String(input) === '/api/bootstrap' ? response(unsupported) : response([])))
    render(<App />)
    expect(await screen.findByRole('button', { name: 'Review HEAD' })).toBeDisabled()
    expect(await screen.findByText('RoboRev upgrade required')).toBeInTheDocument()
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
