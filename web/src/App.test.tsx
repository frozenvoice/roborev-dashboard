import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { App, CommentBox } from './App'

const bootstrap = {
  csrfToken: 'csrf', selectedRepository: 'E:\\repo', repositories: [{ name: 'repo', path: 'E:\\repo', branch: 'main', head: 'abcdef123456', headShort: 'abcdef12', commitSubject: 'Initial', dirty: false, hasConfig: true }],
  roborev: { installed: true, version: 'v0.64.0', status: { running: true }, capabilities: { reviewEnqueueApi: true, frozenRerunApi: true, panelReview: true, promptLazyLoad: true, rawLog: true } },
  reviewHeadCommand: { executable: 'roborev.exe', args: ['review', 'abcdef123456', '--panel', 'dashboard_cross'] },
}
const jobs = [{ id: 42, git_ref: 'abcdef123456', branch: 'main', status: 'done', verdict: 'finding', panel_name: 'dashboard_cross', review: { output: '# Finding\nActionable issue' } }]

function response(value: unknown, status = 200) { return Promise.resolve(new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } })) }

afterEach(() => { cleanup(); vi.restoreAllMocks() })

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
    expect(await screen.findByRole('button', { name: /Rerun same target\/config/ })).toBeInTheDocument()
  })

  it('renders finding state and safe review text', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => String(input) === '/api/bootstrap' ? response(bootstrap) : response(jobs)))
    render(<App />)
    expect((await screen.findAllByText('Finding')).length).toBeGreaterThan(0)
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
