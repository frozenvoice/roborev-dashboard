import { StrictMode } from 'react'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { GitHookStatus } from './GitHookStatus'
import { normalizeGitHookStatus } from './api'
import { I18nProvider, LanguageSelector } from './i18n'

function jsonResponse(value: unknown, status = 200) {
  return Promise.resolve(new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } }))
}

function payload(repositoryIdentity: string, status: string, postCommit: string, postRewrite: string, extra: Record<string, unknown> = {}) {
  return { repositoryIdentity, status, postCommit: { status: postCommit }, postRewrite: { status: postRewrite }, ...extra }
}

afterEach(() => { cleanup(); vi.restoreAllMocks(); window.localStorage.clear() })

describe('GitHookStatus', () => {
  it('normalizes only exact scoped DTOs and enforces summary invariants', () => {
    const ready = payload('repo-1', 'ready', 'current', 'current')
    expect(normalizeGitHookStatus(ready, 'repo-1')).toEqual(ready)
    expect(normalizeGitHookStatus({ ...ready, repositoryIdentity: 'repo-2' }, 'repo-1')).toBeNull()
    expect(normalizeGitHookStatus({ ...ready, status: 'not-installed' }, 'repo-1')).toBeNull()
    expect(normalizeGitHookStatus({ ...ready, path: 'C:\\repo' }, 'repo-1')).toBeNull()
    expect(normalizeGitHookStatus({ ...ready, postCommit: { status: 'current', content: 'secret' } }, 'repo-1')).toBeNull()
  })

  it('preserves factual post-rewrite states when post-commit is not installed', () => {
    expect(normalizeGitHookStatus(payload('repo-1', 'not-installed', 'not-installed', 'current'), 'repo-1')?.postRewrite.status).toBe('current')
    expect(normalizeGitHookStatus(payload('repo-1', 'not-installed', 'not-installed', 'outdated'), 'repo-1')?.postRewrite.status).toBe('outdated')
    expect(normalizeGitHookStatus(payload('repo-1', 'not-installed', 'not-installed', 'not-installed'), 'repo-1')?.status).toBe('not-installed')
  })

  it('keeps post-rewrite facts when post-commit is unknown and rejects false absence', () => {
    for (const postRewrite of ['current', 'outdated', 'unknown'] as const) {
      expect(normalizeGitHookStatus(payload('repo-1', 'unknown', 'unknown', postRewrite), 'repo-1')?.status).toBe('unknown')
    }
    expect(normalizeGitHookStatus(payload('repo-1', 'unknown', 'unknown', 'missing'), 'repo-1')).toBeNull()
    expect(normalizeGitHookStatus(payload('repo-1', 'unknown', 'unknown', 'not-installed'), 'repo-1')).toBeNull()
  })

  it.each([
    ['ready', 'current', 'current', 'Ready'],
    ['needs-attention', 'outdated', 'current', 'Needs attention'],
    ['needs-attention', 'current', 'missing', 'Needs attention'],
    ['not-installed', 'not-installed', 'not-installed', 'Not installed'],
    ['unknown', 'unknown', 'unknown', 'Unavailable'],
  ])('renders %s and both hook states', async (status, postCommit, postRewrite, summary) => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockReturnValue(jsonResponse(payload('repo-1', status, postCommit, postRewrite)))
    render(<GitHookStatus repositoryIdentity="repo-1" />)
    await waitFor(() => expect(screen.getAllByText(summary).length).toBeGreaterThan(0))
    expect(screen.getByText('Post-commit')).toBeInTheDocument()
    expect(screen.getByText('Post-rewrite')).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('coalesces the first StrictMode effect into exactly one GET', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockReturnValue(jsonResponse(payload('repo-1', 'ready', 'current', 'current')))
    render(<StrictMode><GitHookStatus repositoryIdentity="repo-1" /></StrictMode>)
    await waitFor(() => expect(screen.getByText('Ready')).toBeInTheDocument())
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock.mock.calls[0][0]).toBe('/api/repositories/hooks?repositoryIdentity=repo-1')
  })

  it('rejects extra path/content fields without rendering them', async () => {
    vi.spyOn(globalThis, 'fetch').mockReturnValue(jsonResponse(payload('repo-1', 'ready', 'current', 'current', { path: 'C:\\secret\\hooks', content: 'rm -rf' })))
    render(<GitHookStatus repositoryIdentity="repo-1" />)
    await waitFor(() => expect(screen.getByText('Unavailable')).toBeInTheDocument())
    expect(screen.queryByText(/secret|rm -rf/i)).not.toBeInTheDocument()
  })

  it('discards a stale repository response and reads the new identity once', async () => {
    let resolveFirst!: (value: Response) => void
    let resolveSecond!: (value: Response) => void
    const first = new Promise<Response>(resolve => { resolveFirst = resolve })
    const second = new Promise<Response>(resolve => { resolveSecond = resolve })
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation((input) => String(input).includes('repo-1') ? first : second)
    const { rerender } = render(<GitHookStatus repositoryIdentity="repo-1" />)
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    rerender(<GitHookStatus repositoryIdentity="repo-2" />)
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    resolveFirst(await jsonResponse(payload('repo-1', 'needs-attention', 'outdated', 'current')))
    resolveSecond(await jsonResponse(payload('repo-2', 'ready', 'current', 'current')))
    await waitFor(() => expect(screen.getByText('Ready')).toBeInTheDocument())
    expect(screen.queryByText('Needs attention')).not.toBeInTheDocument()
  })

  it('lets a newer refresh supersede the older response', async () => {
    let resolveFirst!: (value: Response) => void
    let resolveSecond!: (value: Response) => void
    const first = new Promise<Response>(resolve => { resolveFirst = resolve })
    const second = new Promise<Response>(resolve => { resolveSecond = resolve })
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation((_input, init) => init?.signal && fetchMock.mock.calls.length === 1 ? first : second)
    render(<GitHookStatus repositoryIdentity="repo-1" />)
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    fireEvent.click(screen.getByRole('button', { name: 'Refresh Git hook status' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    resolveSecond(await jsonResponse(payload('repo-1', 'ready', 'current', 'current')))
    await waitFor(() => expect(screen.getByText('Ready')).toBeInTheDocument())
    resolveFirst(await jsonResponse(payload('repo-1', 'needs-attention', 'outdated', 'current')))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(screen.getByText('Ready')).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('does not fetch on locale switch and renders translated status text', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockReturnValue(jsonResponse(payload('repo-1', 'needs-attention', 'current', 'missing')))
    render(<I18nProvider><><LanguageSelector /><GitHookStatus repositoryIdentity="repo-1" /></></I18nProvider>)
    await waitFor(() => expect(screen.getByText('Needs attention')).toBeInTheDocument())
    expect(fetchMock).toHaveBeenCalledTimes(1)
    fireEvent.change(screen.getByRole('combobox', { name: 'Select language' }), { target: { value: 'ko' } })
    expect(screen.getByText('확인 필요')).toBeInTheDocument()
    expect(screen.getByText('누락됨')).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(1)
    fireEvent.change(screen.getByRole('combobox', { name: '언어 선택' }), { target: { value: 'en' } })
  })

  it('ignores a late response after unmount', async () => {
    let resolve!: (value: Response) => void
    const pending = new Promise<Response>(done => { resolve = done })
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockReturnValue(pending)
    const view = render(<GitHookStatus repositoryIdentity="repo-1" />)
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    view.unmount()
    resolve(await jsonResponse(payload('repo-1', 'ready', 'current', 'current')))
    await new Promise(done => setTimeout(done, 0))
    expect(document.body).not.toHaveTextContent('Ready')
  })

  it('has no mutation CTA or raw hook detail', async () => {
    vi.spyOn(globalThis, 'fetch').mockReturnValue(jsonResponse(payload('repo-1', 'ready', 'current', 'current')))
    render(<GitHookStatus repositoryIdentity="repo-1" />)
    await waitFor(() => expect(screen.getByText('Ready')).toBeInTheDocument())
    expect(screen.queryByRole('button', { name: /install|repair|remove/i })).not.toBeInTheDocument()
    expect(screen.queryByText(/hook path|binary|script|content/i)).not.toBeInTheDocument()
  })
})
