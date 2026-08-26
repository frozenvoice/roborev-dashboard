import { StrictMode } from 'react'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { GitHookStatus } from './GitHookStatus'
import { normalizeGitHookManagement, normalizeGitHookManagementPreview, normalizeGitHookManagementResult, normalizeGitHookStatus } from './api'
import { I18nProvider, LanguageSelector } from './i18n'

function jsonResponse(value: unknown, status = 200) {
  return Promise.resolve(new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } }))
}

function payload(repositoryIdentity: string, status: string, postCommit: string, postRewrite: string, extra: Record<string, unknown> = {}) {
  return { repositoryIdentity, status, postCommit: { status: postCommit }, postRewrite: { status: postRewrite }, ...extra }
}

function managementPayload(repositoryIdentity: string, overrides: Record<string, unknown> = {}) {
  const hook = (name: 'post-commit' | 'post-rewrite', classification: string, status: string, managed: boolean) => ({ name, status, classification, managed, shell: managed, binaryCurrent: classification === 'managed-current' })
  return {
    repositoryIdentity,
    postCommit: hook('post-commit', 'absent', 'not-installed', false),
    postRewrite: hook('post-rewrite', 'absent', 'not-installed', false),
    install: { available: true, reason: 'eligible' },
    repair: { available: false, reason: 'no-managed-hook-needs-repair' },
    remove: { available: false, reason: 'no-managed-hook' },
    safety: { hooksDirectorySafe: true, configurationMutationRequired: false, unmanagedHookPresent: false, nonShellHookPresent: false, ambiguousRoborevContent: false, unreadableHookPresent: false },
    summaryCode: 'not-installed',
    warningCodes: [],
    ...overrides,
  }
}

function hookPreview(repositoryIdentity: string, action: 'install' | 'repair' | 'remove', preflight = managementPayload(repositoryIdentity)) {
  const executable = 'C:\\Program Files\\RoboRev\\roborev.exe'
  const args = action === 'install' ? ['install-hook', '--binary', executable] : action === 'repair' ? ['install-hook', 'repair', '--binary', executable] : ['uninstall-hook']
  return { token: 'preview-token', action, repository: 'repo-one', preflight, summary: action, warningCodes: [], command: { executable, args }, expiresAt: '2030-01-01T00:00:00Z' }
}

function managementResult(repositoryIdentity: string, status: 'verified-success' | 'partial' | 'failed-unverified', action: 'install' | 'repair' | 'remove' = 'install') {
  return { status, action, preflight: managementPayload(repositoryIdentity), ...(status === 'partial' ? { errorCode: 'cli-failed' } : {}) }
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

  it('fails closed for management safety, warning, action, and cross-field invariants', () => {
    const ready = managementPayload('repo-1')
    expect(normalizeGitHookManagement(ready, 'repo-1')).not.toBeNull()
    expect(normalizeGitHookManagement({ ...ready, safety: { ...ready.safety, unreadableHookPresent: undefined } }, 'repo-1')).toBeNull()
    expect(normalizeGitHookManagement({ ...ready, warningCodes: ['unsafe-hooks-directory', 'unsafe-hooks-directory'] }, 'repo-1')).toBeNull()
    expect(normalizeGitHookManagement({ ...ready, install: { available: false, reason: 'made-up' } }, 'repo-1')).toBeNull()
    expect(normalizeGitHookManagement({ ...ready, postCommit: { ...ready.postCommit, classification: 'managed-current' } }, 'repo-1')).toBeNull()

    const preview = hookPreview('repo-1', 'install', ready)
    expect(normalizeGitHookManagementPreview({ ...preview, action: 'remove' }, 'repo-1', 'install')).toBeNull()
    expect(normalizeGitHookManagementPreview({ ...preview, warningCodes: ['unsafe-hooks-directory'] }, 'repo-1', 'install')).toBeNull()
    expect(normalizeGitHookManagementResult({ ...managementResult('repo-1', 'partial'), errorCode: 'not-a-backend-code' }, 'repo-1', 'install')).toBeNull()
  })

  it('accepts backend managed-outdated and managed unreadable variants', () => {
    const oldMarker = managementPayload('repo-1', {
      postCommit: { name: 'post-commit', status: 'outdated', classification: 'managed-outdated', managed: true, shell: true, binaryCurrent: true },
      install: { available: false, reason: 'repair-recommended' },
      repair: { available: true, recommended: true, reason: 'managed-hook-needs-repair' },
      remove: { available: true, reason: 'managed-hook-present' },
      summaryCode: 'repair-recommended',
    })
    expect(normalizeGitHookManagement(oldMarker, 'repo-1')).not.toBeNull()

    const unreadableManaged = managementPayload('repo-1', {
      postCommit: { name: 'post-commit', status: 'unknown', classification: 'unreadable', managed: true, shell: false, binaryCurrent: false },
      install: { available: false, reason: 'unreadable-hook' },
      repair: { available: false, reason: 'unreadable-hook' },
      remove: { available: false, reason: 'unreadable-hook' },
      safety: { hooksDirectorySafe: true, configurationMutationRequired: false, unmanagedHookPresent: false, nonShellHookPresent: true, ambiguousRoborevContent: false, unreadableHookPresent: true },
      summaryCode: 'blocked',
      warningCodes: ['non-shell-hook'],
    })
    expect(normalizeGitHookManagement(unreadableManaged, 'repo-1')).not.toBeNull()
    expect(normalizeGitHookManagement({ ...unreadableManaged, safety: { ...unreadableManaged.safety, nonShellHookPresent: false }, warningCodes: [] }, 'repo-1')).toBeNull()
  })

  it('requires backend-derived safety, warning order, and summary truth', () => {
    const nonShellUnmanaged = managementPayload('repo-1', {
      postCommit: { name: 'post-commit', status: 'not-installed', classification: 'unmanaged', managed: false, shell: false, binaryCurrent: false },
      safety: { hooksDirectorySafe: true, configurationMutationRequired: false, unmanagedHookPresent: true, nonShellHookPresent: true, ambiguousRoborevContent: false, unreadableHookPresent: false },
      install: { available: false, reason: 'non-shell-hook' },
      repair: { available: false, reason: 'non-shell-hook' },
      remove: { available: false, reason: 'non-shell-hook' },
      summaryCode: 'blocked',
      warningCodes: ['non-shell-hook'],
    })
    expect(normalizeGitHookManagement(nonShellUnmanaged, 'repo-1')).not.toBeNull()
    expect(normalizeGitHookManagement({ ...nonShellUnmanaged, warningCodes: [] }, 'repo-1')).toBeNull()
    expect(normalizeGitHookManagement({ ...nonShellUnmanaged, warningCodes: ['unsafe-hooks-directory', 'non-shell-hook'] }, 'repo-1')).toBeNull()
    expect(normalizeGitHookManagement({ ...nonShellUnmanaged, safety: { ...nonShellUnmanaged.safety, unmanagedHookPresent: false } }, 'repo-1')).toBeNull()
    expect(normalizeGitHookManagement({ ...nonShellUnmanaged, summaryCode: 'attention' }, 'repo-1')).toBeNull()

    const allWarnings = managementPayload('repo-1', {
      postCommit: { name: 'post-commit', status: 'not-installed', classification: 'unmanaged', managed: false, shell: false, binaryCurrent: false },
      postRewrite: { name: 'post-rewrite', status: 'outdated', classification: 'ambiguous', managed: false, shell: true, binaryCurrent: false },
      safety: { hooksDirectorySafe: false, configurationMutationRequired: true, unmanagedHookPresent: true, nonShellHookPresent: true, ambiguousRoborevContent: true, unreadableHookPresent: false },
      install: { available: false, reason: 'unsafe-hooks-directory' },
      repair: { available: false, reason: 'unsafe-hooks-directory' },
      remove: { available: false, reason: 'unsafe-hooks-directory' },
      summaryCode: 'blocked',
      warningCodes: ['relative-hooks-path-normalization', 'non-shell-hook', 'ambiguous-roborev-content', 'unsafe-hooks-directory'],
    })
    expect(normalizeGitHookManagement(allWarnings, 'repo-1')).not.toBeNull()
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
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('coalesces the first StrictMode effect into exactly one GET', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockReturnValue(jsonResponse(payload('repo-1', 'ready', 'current', 'current')))
    render(<StrictMode><GitHookStatus repositoryIdentity="repo-1" /></StrictMode>)
    await waitFor(() => expect(screen.getByText('Ready')).toBeInTheDocument())
    expect(fetchMock).toHaveBeenCalledTimes(2)
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
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    rerender(<GitHookStatus repositoryIdentity="repo-2" />)
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(4))
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
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(() => fetchMock.mock.calls.length < 2 ? first : second)
    render(<GitHookStatus repositoryIdentity="repo-1" />)
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    fireEvent.click(screen.getByRole('button', { name: 'Refresh Git hook status' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(4))
    resolveSecond(await jsonResponse(payload('repo-1', 'ready', 'current', 'current')))
    await waitFor(() => expect(screen.getByText('Ready')).toBeInTheDocument())
    resolveFirst(await jsonResponse(payload('repo-1', 'needs-attention', 'outdated', 'current')))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(screen.getByText('Ready')).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(4)
  })

  it('does not fetch on locale switch and renders translated status text', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockReturnValue(jsonResponse(payload('repo-1', 'needs-attention', 'current', 'missing')))
    render(<I18nProvider><><LanguageSelector /><GitHookStatus repositoryIdentity="repo-1" /></></I18nProvider>)
    await waitFor(() => expect(screen.getByText('Needs attention')).toBeInTheDocument())
    expect(fetchMock).toHaveBeenCalledTimes(2)
    fireEvent.change(screen.getByRole('combobox', { name: 'Select language' }), { target: { value: 'ko' } })
    expect(screen.getByText('확인 필요')).toBeInTheDocument()
    expect(screen.getByText('누락됨')).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(2)
    fireEvent.change(screen.getByRole('combobox', { name: '언어 선택' }), { target: { value: 'en' } })
  })

  it('ignores a late response after unmount', async () => {
    let resolve!: (value: Response) => void
    const pending = new Promise<Response>(done => { resolve = done })
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockReturnValue(pending)
    const view = render(<GitHookStatus repositoryIdentity="repo-1" />)
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
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

  it.each([
    ['install', managementPayload('repo-1')],
    ['repair', managementPayload('repo-1', {
      postCommit: { name: 'post-commit', status: 'outdated', classification: 'managed-outdated', managed: true, shell: true, binaryCurrent: false },
      install: { available: false, reason: 'repair-recommended' },
      repair: { available: true, recommended: true, reason: 'managed-hook-needs-repair' },
      summaryCode: 'repair-recommended',
    })],
    ['remove', managementPayload('repo-1', {
      postCommit: { name: 'post-commit', status: 'current', classification: 'managed-current', managed: true, shell: true, binaryCurrent: true },
      postRewrite: { name: 'post-rewrite', status: 'current', classification: 'managed-current', managed: true, shell: true, binaryCurrent: true },
      install: { available: false, reason: 'already-current' },
      remove: { available: true, reason: 'managed-hook-present' },
      summaryCode: 'managed-current',
    })],
  ] as const)('%s is authoritative from management preflight', async (action, management) => {
    const status = payload('repo-1', action === 'install' ? 'not-installed' : 'needs-attention', action === 'install' ? 'not-installed' : 'outdated', action === 'install' ? 'not-installed' : 'current')
    vi.spyOn(globalThis, 'fetch').mockImplementation(input => {
      const path = String(input)
      if (path.includes('/management')) return jsonResponse(management)
      return jsonResponse(status)
    })
    render(<GitHookStatus repositoryIdentity="repo-1" />)
    const button = await screen.findByRole('button', { name: action === 'install' ? 'Install' : action === 'repair' ? 'Repair' : 'Remove' })
    expect(button).toBeEnabled()
  })

  it.each([
    ['unsafe directory', { hooksDirectorySafe: false, nonShellHookPresent: false, ambiguousRoborevContent: false }, 'Blocked: the effective hooks directory is not a safe Git directory.'],
    ['non-shell hook', { hooksDirectorySafe: true, nonShellHookPresent: true, ambiguousRoborevContent: false }, 'Blocked: a non-shell hook cannot be safely merged.'],
  ] as const)('blocks mutation for %s', async (label, safety, reason) => {
    const actionReason = label === 'non-shell hook' ? 'non-shell-hook' : 'unsafe-hooks-directory'
    const management = managementPayload('repo-1', { ...(label === 'non-shell hook' ? { postCommit: { name: 'post-commit', status: 'not-installed', classification: 'unmanaged', managed: false, shell: false, binaryCurrent: false } } : {}), safety: { configurationMutationRequired: false, unmanagedHookPresent: label === 'non-shell hook', unreadableHookPresent: false, ...safety }, install: { available: false, reason: actionReason }, repair: { available: false, reason: actionReason }, remove: { available: false, reason: actionReason }, summaryCode: 'blocked', warningCodes: label === 'non-shell hook' ? ['non-shell-hook'] : ['unsafe-hooks-directory'] })
    vi.spyOn(globalThis, 'fetch').mockImplementation(input => String(input).includes('/management') ? jsonResponse(management) : jsonResponse(payload('repo-1', 'needs-attention', 'outdated', 'current')))
    render(<GitHookStatus repositoryIdentity="repo-1" />)
    const install = await screen.findByRole('button', { name: 'Install' })
    expect(install).toBeDisabled()
    expect(screen.getAllByText(reason).length).toBeGreaterThan(0)
  })

  it('uses the action availability reason before a broader safety warning', async () => {
    const management = managementPayload('repo-1', {
      safety: { hooksDirectorySafe: true, configurationMutationRequired: true, unmanagedHookPresent: false, nonShellHookPresent: false, ambiguousRoborevContent: false, unreadableHookPresent: false },
      install: { available: false, reason: 'relative-hooks-path-normalization' },
      remove: { available: false, reason: 'no-managed-hook' },
      summaryCode: 'not-installed',
      warningCodes: ['relative-hooks-path-normalization'],
    })
    vi.spyOn(globalThis, 'fetch').mockImplementation(input => String(input).includes('/management') ? jsonResponse(management) : jsonResponse(payload('repo-1', 'not-installed', 'not-installed', 'not-installed')))
    render(<GitHookStatus repositoryIdentity="repo-1" />)
    await screen.findByRole('button', { name: 'Remove' })
    expect(screen.getAllByText('No managed RoboRev hook is present.').length).toBeGreaterThan(0)
  })

  it('opens preview only after CTA, cancel never executes, and keeps forbidden controls out of the dialog', async () => {
    const management = managementPayload('repo-1')
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(input => {
      const path = String(input)
      if (path.includes('/management')) return jsonResponse(management)
      if (path.includes('/preview')) return jsonResponse(hookPreview('repo-1', 'install', management))
      if (path.includes('/execute')) return jsonResponse(managementResult('repo-1', 'verified-success'))
      return jsonResponse(payload('repo-1', 'not-installed', 'not-installed', 'not-installed'))
    })
    render(<GitHookStatus repositoryIdentity="repo-1" />)
    await screen.findByRole('button', { name: 'Install' })
    expect(fetchMock.mock.calls.some(call => String(call[0]).includes('/preview'))).toBe(false)
    fireEvent.click(screen.getByRole('button', { name: 'Install' }))
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
    expect(screen.getByText('repo-one')).toBeInTheDocument()
    expect(screen.getByText(/install-hook/)).toBeInTheDocument()
    expect(screen.queryByText(/agent-hook|--force|--registered/i)).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(fetchMock.mock.calls.filter(call => String(call[0]).includes('/execute')).length).toBe(0)
  })

  it('uses safe initial focus, contains Tab focus, and restores the invoking CTA on Escape', async () => {
    const management = managementPayload('repo-1')
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(input => {
      const path = String(input)
      if (path.includes('/management')) return jsonResponse(management)
      if (path.includes('/preview')) return jsonResponse(hookPreview('repo-1', 'install', management))
      return jsonResponse(payload('repo-1', 'not-installed', 'not-installed', 'not-installed'))
    })
    render(<GitHookStatus repositoryIdentity="repo-1" />)
    const opener = await screen.findByRole('button', { name: 'Install' })
    await userEvent.click(opener)
    const dialog = await screen.findByRole('dialog')
    const cancel = screen.getByRole('button', { name: 'Cancel' })
    const confirm = screen.getAllByRole('button', { name: 'Install' }).find(button => button !== opener)!
    expect(document.activeElement).toBe(cancel)
    await userEvent.tab()
    expect(document.activeElement).toBe(confirm)
    await userEvent.tab()
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Close Git hook confirmation' }))
    await userEvent.tab({ shift: true })
    expect(document.activeElement).toBe(confirm)
    fireEvent.keyDown(dialog, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(document.activeElement).toBe(opener)
    expect(fetchMock.mock.calls.filter(call => String(call[0]).includes('/execute')).length).toBe(0)
  })

  it('does not cancel or close while execute is in flight', async () => {
    const management = managementPayload('repo-1')
    let resolveExecute!: (value: Response) => void
    const execute = new Promise<Response>(resolve => { resolveExecute = resolve })
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(input => {
      const path = String(input)
      if (path.includes('/management')) return jsonResponse(management)
      if (path.includes('/preview')) return jsonResponse(hookPreview('repo-1', 'install', management))
      if (path.includes('/execute')) return execute
      return jsonResponse(payload('repo-1', 'not-installed', 'not-installed', 'not-installed'))
    })
    render(<GitHookStatus repositoryIdentity="repo-1" />)
    const opener = await screen.findByRole('button', { name: 'Install' })
    await userEvent.click(opener)
    await screen.findByRole('dialog')
    const confirm = screen.getAllByRole('button', { name: 'Install' }).find(button => button !== opener)!
    await userEvent.click(confirm)
    await waitFor(() => expect(confirm).toBeDisabled())
    const dialog = screen.getByRole('dialog')
    fireEvent.keyDown(dialog, { key: 'Escape' })
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(fetchMock.mock.calls.filter(call => String(call[0]).includes('/execute')).length).toBe(1)
    resolveExecute(await jsonResponse(managementResult('repo-1', 'verified-success')))
    await screen.findByText('Verified success')
  })

  it('confirms exactly once and reloads status plus management exactly once', async () => {
    const management = managementPayload('repo-1')
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(input => {
      const path = String(input)
      if (path.includes('/management')) return jsonResponse(management)
      if (path.includes('/preview')) return jsonResponse(hookPreview('repo-1', 'install', management))
      if (path.includes('/execute')) return jsonResponse(managementResult('repo-1', 'partial'))
      return jsonResponse(payload('repo-1', 'not-installed', 'not-installed', 'not-installed'))
    })
    render(<GitHookStatus repositoryIdentity="repo-1" />)
    await screen.findByRole('button', { name: 'Install' })
    const statusBefore = fetchMock.mock.calls.filter(call => String(call[0]).includes('/api/repositories/hooks?')).length
    const managementBefore = fetchMock.mock.calls.filter(call => String(call[0]).includes('/management')).length
    fireEvent.click(screen.getByRole('button', { name: 'Install' }))
    await screen.findByRole('dialog')
    const confirm = screen.getAllByRole('button', { name: 'Install' }).find(button => !button.hasAttribute('disabled'))!
    fireEvent.click(confirm)
    fireEvent.click(confirm)
    await screen.findByText('Partial mutation')
    expect(fetchMock.mock.calls.filter(call => String(call[0]).includes('/execute')).length).toBe(1)
    expect(fetchMock.mock.calls.filter(call => String(call[0]).includes('/api/repositories/hooks?')).length).toBe(statusBefore + 1)
    expect(fetchMock.mock.calls.filter(call => String(call[0]).includes('/management')).length).toBe(managementBefore + 1)
  })

  it('keeps mutation lifecycle network-free on locale switch', async () => {
    const management = managementPayload('repo-1')
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(input => String(input).includes('/management') ? jsonResponse(management) : jsonResponse(payload('repo-1', 'not-installed', 'not-installed', 'not-installed')))
    render(<I18nProvider><><LanguageSelector /><GitHookStatus repositoryIdentity="repo-1" /></></I18nProvider>)
    await screen.findByRole('button', { name: 'Install' })
    const requests = fetchMock.mock.calls.length
    fireEvent.change(screen.getByRole('combobox', { name: 'Select language' }), { target: { value: 'ko' } })
    expect(screen.getByRole('button', { name: '설치' })).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(requests)
  })
})
