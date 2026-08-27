import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { setCSRF } from './api'
import { RepositorySetupReadiness } from './RepositorySetupReadiness'
import { I18nProvider, LanguageSelector } from './i18n'

const aliasA = 'A'.repeat(43)
const aliasB = 'B'.repeat(43)
const ready = {
  version: 'v0.64.0', repositoryIdentity: aliasA, overall: 'ready',
  configuration: { status: 'ready' }, snapshot: { status: 'ready' },
  gitHooks: { status: 'ready' }, daemon: { status: 'online' }, tracking: { status: 'tracked' },
}

function jsonResponse(value: unknown) {
  return new Response(JSON.stringify(value), { status: 200, headers: { 'Content-Type': 'application/json' } })
}

afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); window.localStorage.clear() })

describe('RepositorySetupReadiness', () => {
  it('does not fetch initially, guards loading double clicks, and refreshes once after settlement', async () => {
    setCSRF('csrf')
    let resolve!: (response: Response) => void
    let attempts = 0
    const fetchMock = vi.fn().mockImplementation(() => {
      attempts += 1
      return attempts === 1 ? new Promise<Response>(done => { resolve = done }) : Promise.resolve(jsonResponse(ready))
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<I18nProvider><RepositorySetupReadiness repositoryIdentity="repo-a" /></I18nProvider>)
    expect(fetchMock).not.toHaveBeenCalled()
    expect(screen.getByText('Repository setup has not been checked.')).toBeInTheDocument()
    const button = screen.getByRole('button', { name: 'Check Setup' })
    fireEvent.click(button)
    fireEvent.click(button)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(button).toBeDisabled()
    expect(screen.getByText('Checking repository setup…')).toBeInTheDocument()
    resolve(jsonResponse(ready))
    await waitFor(() => expect(screen.getByText('All repository setup checks passed.')).toBeInTheDocument())
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeEnabled()
    await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('renders every component row with path-free status descriptions and no mutation CTA', async () => {
    const value = { ...ready, overall: 'unavailable', configuration: { status: 'unavailable' }, snapshot: { status: 'unavailable' }, gitHooks: { status: 'unknown' }, daemon: { status: 'unavailable' }, tracking: { status: 'unavailable' } }
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(value)))
    render(<I18nProvider><RepositorySetupReadiness repositoryIdentity="repo-a" /></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Check Setup' }))
    await waitFor(() => expect(screen.getByText('Some setup checks could not be verified safely.')).toBeInTheDocument())
    for (const label of ['Configuration', 'Snapshot protection', 'Git Hooks', 'Daemon', 'RoboRev tracking']) expect(screen.getByText(label)).toBeInTheDocument()
    expect(screen.getByText('Configuration could not be safely resolved.')).toBeInTheDocument()
    expect(screen.getByText('Snapshot protection could not be verified.')).toBeInTheDocument()
    expect(screen.getByText('Git hooks could not be verified.')).toBeInTheDocument()
    expect(screen.getByText('RoboRev daemon could not be verified.')).toBeInTheDocument()
    expect(screen.getByText('RoboRev tracking could not be verified.')).toBeInTheDocument()
    for (const forbidden of ['Initialize', 'Fix All', 'Apply', 'Register', 'Ignore', 'Install', 'Start']) expect(screen.queryByText(forbidden)).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeInTheDocument()
  })

  it.each([
    ['snapshot not ignored', { ...ready, overall: 'needs-attention', snapshot: { status: 'not-ignored' } }, 'Snapshot directory is not ignored by Git.'],
    ['snapshot tracked files', { ...ready, overall: 'needs-attention', snapshot: { status: 'tracked-files' } }, 'Snapshot directory contains tracked files.'],
    ['hook not installed', { ...ready, overall: 'needs-attention', gitHooks: { status: 'not-installed' } }, 'RoboRev Git hooks are not installed.'],
    ['daemon offline', { ...ready, overall: 'needs-attention', daemon: { status: 'offline' }, tracking: { status: 'unavailable' } }, 'RoboRev daemon is offline.'],
    ['tracking not tracked', { ...ready, overall: 'needs-attention', tracking: { status: 'not-tracked' } }, 'This repository is not tracked by RoboRev.'],
    ['snapshot unsafe', { ...ready, overall: 'unavailable', snapshot: { status: 'unsafe' } }, 'Snapshot directory failed the safety check.'],
  ] as const)('renders the %s component state', async (_name, value, description) => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(value)))
    render(<I18nProvider><RepositorySetupReadiness repositoryIdentity="repo-a" /></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Check Setup' }))
    expect(await screen.findByText(description)).toBeInTheDocument()
  })

  it('clears and aborts on repository switch without auto-fetch; a new request owns the lock', async () => {
    const deferred: Array<{ resolve: (response: Response) => void, signal?: AbortSignal }> = []
    const fetchMock = vi.fn().mockImplementation((_path: string, init: RequestInit) => new Promise<Response>(resolve => {
      deferred.push({ resolve, signal: init.signal as AbortSignal })
    }))
    vi.stubGlobal('fetch', fetchMock)
    const { rerender } = render(<I18nProvider><RepositorySetupReadiness repositoryIdentity="repo-a" /></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Check Setup' }))
    expect(fetchMock).toHaveBeenCalledTimes(1)
    rerender(<I18nProvider><RepositorySetupReadiness repositoryIdentity="repo-b" /></I18nProvider>)
    expect(screen.getByText('Repository setup has not been checked.')).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(deferred[0].signal?.aborted).toBe(true)
    await userEvent.click(screen.getByRole('button', { name: 'Check Setup' }))
    expect(fetchMock).toHaveBeenCalledTimes(2)
    deferred[1].resolve(jsonResponse({ ...ready, repositoryIdentity: aliasB }))
    await waitFor(() => expect(screen.getByText('All repository setup checks passed.')).toBeInTheDocument())
    deferred[0].resolve(jsonResponse({
      ...ready,
      repositoryIdentity: aliasA,
      overall: 'unavailable',
      configuration: { status: 'unavailable' },
      snapshot: { status: 'unavailable' },
    }))
    await act(async () => { await Promise.resolve() })
    expect(screen.queryByText('Some setup checks could not be verified safely.')).not.toBeInTheDocument()
    expect(screen.getByText('All repository setup checks passed.')).toBeInTheDocument()
  })

  it('keeps typed state across locale changes without a request', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(ready))
    vi.stubGlobal('fetch', fetchMock)
    render(<I18nProvider><LanguageSelector /><RepositorySetupReadiness repositoryIdentity="repo-a" /></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Check Setup' }))
    await waitFor(() => expect(screen.getByText('All repository setup checks passed.')).toBeInTheDocument())
    const count = fetchMock.mock.calls.length
    await userEvent.selectOptions(screen.getByRole('combobox'), 'ko')
    expect(fetchMock).toHaveBeenCalledTimes(count)
    expect(screen.queryByText('All repository setup checks passed.')).not.toBeInTheDocument()
  })

  it('aborts and ignores a genuinely late completion after unmount', async () => {
    let resolve!: (response: Response) => void
    let signal: AbortSignal | undefined
    const fetchMock = vi.fn().mockImplementation((_path: string, init: RequestInit) => {
      signal = init.signal as AbortSignal
      return new Promise<Response>(done => { resolve = done })
    })
    vi.stubGlobal('fetch', fetchMock)
    const { unmount } = render(<I18nProvider><RepositorySetupReadiness repositoryIdentity="repo-a" /></I18nProvider>)
    fireEvent.click(screen.getByRole('button', { name: 'Check Setup' }))
    expect(fetchMock).toHaveBeenCalledTimes(1)
    unmount()
    expect(signal?.aborted).toBe(true)
    await act(async () => { resolve(jsonResponse(ready)); await Promise.resolve() })
  })

  it('announces a settled error accessibly and never starts a timer or stream', async () => {
    const interval = vi.spyOn(globalThis, 'setInterval')
    const eventSource = vi.fn()
    vi.stubGlobal('EventSource', eventSource)
    const privateMarkers = [
      `C:\\Users\\private\\source\\secret-project`,
      `C:\\Users\\private\\.roborev`,
      `C:\\fake\\very-secret\\roborev.exe`,
      'PRIVATE_SNAPSHOT_DIR_MARKER',
      'PRIVATE_GITIGNORE_MARKER',
      'PRIVATE_STDERR_MARKER',
    ]
    const fetchMock = vi.fn().mockRejectedValue(new Error(privateMarkers.join(' ')))
    vi.stubGlobal('fetch', fetchMock)
    render(<I18nProvider><RepositorySetupReadiness repositoryIdentity="repo-a" /></I18nProvider>)
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Check Setup' }))
      await Promise.resolve()
    })
    expect(screen.getByRole('alert')).toHaveTextContent('Repository setup readiness could not be verified.')
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeEnabled()
    expect(interval).not.toHaveBeenCalled()
    expect(eventSource).not.toHaveBeenCalled()
    for (const marker of privateMarkers) expect(document.body.textContent).not.toContain(marker)
  })

  it.each([
    ['ready', ready],
    ['tracked files', { ...ready, overall: 'needs-attention', snapshot: { status: 'tracked-files' } }],
    ['unsafe', { ...ready, overall: 'unavailable', snapshot: { status: 'unsafe' } }],
    ['unavailable', { ...ready, overall: 'unavailable', snapshot: { status: 'unavailable' } }],
  ] as const)('only exposes the ignore CTA for a fresh not-ignored result (%s)', async (_name, value) => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(value)))
    render(<I18nProvider><RepositorySetupReadiness repositoryIdentity="repo-a" /></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Check Setup' }))
    await waitFor(() => expect(screen.getByText(value.snapshot.status === 'ready' ? 'Snapshot directory is ignored by Git.' : value.snapshot.status === 'tracked-files' ? 'Snapshot directory contains tracked files.' : value.snapshot.status === 'unsafe' ? 'Snapshot directory failed the safety check.' : 'Snapshot protection could not be verified.')).toBeInTheDocument())
    expect(screen.queryByRole('button', { name: 'Add Ignore Rule' })).not.toBeInTheDocument()
  })

  it('runs one preview, opens a no-content confirmation, and executes exactly once after double confirm', async () => {
    const preview = { version: 'v0.64.0', repositoryIdentity: aliasA, action: 'append-snapshot-ignore', target: '.gitignore', summary: 'Add the RoboRev snapshot directory to Git ignore rules.', token: 'opaque-snapshot-token', expiresAt: '2099-01-01T00:00:00.000Z' }
    const calls: Array<{ path: string; init?: RequestInit }> = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      calls.push({ path, init })
      if (path === '/api/repositories/setup-readiness') return jsonResponse({ ...ready, snapshot: { status: 'not-ignored' }, overall: 'needs-attention' })
      if (path === '/api/snapshot-ignore/preview') return jsonResponse(preview)
      if (path === '/api/snapshot-ignore/execute') return jsonResponse({ status: 'verified-success' })
      throw new Error(`unexpected path ${path}`)
    }))
    render(<I18nProvider><RepositorySetupReadiness repositoryIdentity="legacy-selected-repository" /></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Check Setup' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Add Ignore Rule' }))
    await waitFor(() => expect(screen.getByRole('dialog')).toBeInTheDocument())
    expect(calls.filter(call => call.path === '/api/snapshot-ignore/preview')).toHaveLength(1)
    expect(calls.filter(call => call.path === '/api/snapshot-ignore/execute')).toHaveLength(0)
    expect(screen.getByRole('dialog')).toHaveTextContent('Add Snapshot Ignore Rule')
    expect(screen.getByRole('dialog')).toHaveTextContent('Target')
    expect(screen.getByRole('dialog')).toHaveTextContent('.gitignore')
    expect(screen.getByRole('dialog')).toHaveTextContent('Append the RoboRev snapshot ignore rule.')
    expect(screen.getByRole('dialog')).toHaveTextContent('Modify existing lines, overwrite the file, stage, commit, or push.')
    for (const forbidden of ['PRIVATE_SNAPSHOT_PATH', 'PRIVATE_GITIGNORE_MARKER_DO_NOT_EXPOSE', 'C:\\Users\\private\\source\\secret-project', 'C:\\fake\\very-secret\\roborev.exe']) expect(document.body.textContent).not.toContain(forbidden)
    const confirm = screen.getByRole('button', { name: 'Confirm and append' })
    fireEvent.click(confirm)
    fireEvent.click(confirm)
    await waitFor(() => expect(screen.getByText('Snapshot ignore rule added.')).toBeInTheDocument())
    expect(calls.filter(call => call.path === '/api/snapshot-ignore/execute')).toHaveLength(1)
    const execute = calls.find(call => call.path === '/api/snapshot-ignore/execute')!
    expect(JSON.parse(String(execute.init?.body))).toEqual({ token: 'opaque-snapshot-token' })
    expect(Object.keys(JSON.parse(String(execute.init?.body)))).toEqual(['token'])
    expect(calls.filter(call => call.path === '/api/repositories/setup-readiness')).toHaveLength(1)
    expect(screen.queryByRole('button', { name: 'Add Ignore Rule' })).not.toBeInTheDocument()
    expect(screen.getByText('Review and commit .gitignore with Git.')).toBeInTheDocument()
    expect(screen.getByText('Check Setup again to verify all repository setup.')).toBeInTheDocument()
  })

  it('keeps cancel, close, and Escape execute-free', async () => {
    const preview = { version: 'v0.64.0', repositoryIdentity: aliasA, action: 'append-snapshot-ignore', target: '.gitignore', summary: 'Add the RoboRev snapshot directory to Git ignore rules.', token: 'opaque-snapshot-token', expiresAt: '2099-01-01T00:00:00.000Z' }
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(jsonResponse({ ...ready, snapshot: { status: 'not-ignored' }, overall: 'needs-attention' }))
      .mockResolvedValueOnce(jsonResponse(preview))
    vi.stubGlobal('fetch', fetchMock)
    render(<I18nProvider><RepositorySetupReadiness repositoryIdentity="legacy-selected-repository" /></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Check Setup' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Add Ignore Rule' }))
    const dialog = await screen.findByRole('dialog')
    fireEvent.keyDown(dialog, { key: 'Escape' })
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(2)
    await userEvent.click(await screen.findByRole('button', { name: 'Add Ignore Rule' }))
    const reopened = await screen.findByRole('dialog')
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(reopened).not.toBeInTheDocument()
    expect(fetchMock.mock.calls.some(call => String(call[0]) === '/api/snapshot-ignore/execute')).toBe(false)
  })

  it('aborts a preview and ignores late completion after repository switch', async () => {
    let resolvePreview!: (response: Response) => void
    let previewSignal: AbortSignal | undefined
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/repositories/setup-readiness') return Promise.resolve(jsonResponse({ ...ready, snapshot: { status: 'not-ignored' }, overall: 'needs-attention' }))
      if (path === '/api/snapshot-ignore/preview') {
        previewSignal = init?.signal as AbortSignal
        return new Promise<Response>(resolve => { resolvePreview = resolve })
      }
      return Promise.reject(new Error(`unexpected path ${path}`))
    })
    vi.stubGlobal('fetch', fetchMock)
    const view = render(<I18nProvider><RepositorySetupReadiness repositoryIdentity="repo-a" /></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Check Setup' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Add Ignore Rule' }))
    expect(fetchMock).toHaveBeenCalledTimes(2)
    view.rerender(<I18nProvider><RepositorySetupReadiness repositoryIdentity="repo-b" /></I18nProvider>)
    expect(previewSignal?.aborted).toBe(true)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Add Ignore Rule' })).not.toBeInTheDocument()
    resolvePreview(jsonResponse({ version: 'v0.64.0', repositoryIdentity: aliasA, action: 'append-snapshot-ignore', target: '.gitignore', summary: 'Add the RoboRev snapshot directory to Git ignore rules.', token: 'late-token', expiresAt: '2099-01-01T00:00:00.000Z' }))
    await act(async () => { await Promise.resolve() })
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(fetchMock.mock.calls.some(call => String(call[0]) === '/api/snapshot-ignore/execute')).toBe(false)
  })

  it('does not request on locale switch and retains the open confirmation', async () => {
    const preview = { version: 'v0.64.0', repositoryIdentity: aliasA, action: 'append-snapshot-ignore', target: '.gitignore', summary: 'Add the RoboRev snapshot directory to Git ignore rules.', token: 'opaque-snapshot-token', expiresAt: '2099-01-01T00:00:00.000Z' }
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(jsonResponse({ ...ready, snapshot: { status: 'not-ignored' }, overall: 'needs-attention' }))
      .mockResolvedValueOnce(jsonResponse(preview))
    vi.stubGlobal('fetch', fetchMock)
    render(<I18nProvider><LanguageSelector /><RepositorySetupReadiness repositoryIdentity="legacy-selected-repository" /></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Check Setup' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Add Ignore Rule' }))
    await screen.findByRole('dialog')
    const count = fetchMock.mock.calls.length
    await userEvent.selectOptions(screen.getByRole('combobox'), 'ko')
    expect(fetchMock).toHaveBeenCalledTimes(count)
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })

  it('sanitizes ambiguous execute failure, consumes the token, and offers no immediate retry', async () => {
    const preview = { version: 'v0.64.0', repositoryIdentity: aliasA, action: 'append-snapshot-ignore', target: '.gitignore', summary: 'Add the RoboRev snapshot directory to Git ignore rules.', token: 'opaque-snapshot-token', expiresAt: '2099-01-01T00:00:00.000Z' }
    const privateMarkers = ['C:\\Users\\private\\source\\secret-project', 'C:\\Users\\private\\.roborev', 'C:\\fake\\very-secret\\roborev.exe', 'PRIVATE_SNAPSHOT_PATH', 'PRIVATE_GITIGNORE_MARKER_DO_NOT_EXPOSE', 'PRIVATE_STDERR_MARKER']
    const calls: Array<{ path: string; init?: RequestInit }> = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      calls.push({ path, init })
      if (path === '/api/repositories/setup-readiness') return jsonResponse({ ...ready, snapshot: { status: 'not-ignored' }, overall: 'needs-attention' })
      if (path === '/api/snapshot-ignore/preview') return jsonResponse(preview)
      if (path === '/api/snapshot-ignore/execute') return Promise.reject(new Error(privateMarkers.join(' ')))
      throw new Error(`unexpected path ${path}`)
    }))
    render(<I18nProvider><RepositorySetupReadiness repositoryIdentity="legacy-selected-repository" /></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Check Setup' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Add Ignore Rule' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Confirm and append' }))
    await waitFor(() => expect(screen.getByText('Snapshot ignore update could not be verified. Inspect .gitignore before retrying.')).toBeInTheDocument())
    expect(calls.filter(call => call.path === '/api/repositories/setup-readiness')).toHaveLength(1)
    expect(calls.filter(call => call.path === '/api/snapshot-ignore/preview')).toHaveLength(1)
    expect(calls.filter(call => call.path === '/api/snapshot-ignore/execute')).toHaveLength(1)
    expect(screen.queryByRole('button', { name: 'Add Ignore Rule' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /retry/i })).not.toBeInTheDocument()
    for (const marker of privateMarkers) expect(document.body.textContent).not.toContain(marker)
  })

  it('discards a late execute result after repository switch without refreshing or mutating UI state', async () => {
    const preview = { version: 'v0.64.0', repositoryIdentity: aliasA, action: 'append-snapshot-ignore', target: '.gitignore', summary: 'Add the RoboRev snapshot directory to Git ignore rules.', token: 'opaque-snapshot-token', expiresAt: '2099-01-01T00:00:00.000Z' }
    let resolveExecute!: (response: Response) => void
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      calls.push(path)
      if (path === '/api/repositories/setup-readiness') return Promise.resolve(jsonResponse({ ...ready, snapshot: { status: 'not-ignored' }, overall: 'needs-attention' }))
      if (path === '/api/snapshot-ignore/preview') return Promise.resolve(jsonResponse(preview))
      if (path === '/api/snapshot-ignore/execute') return new Promise<Response>(resolve => { resolveExecute = resolve })
      return Promise.reject(new Error(`unexpected path ${path}`))
    }))
    const view = render(<I18nProvider><RepositorySetupReadiness repositoryIdentity="legacy-selected-repository" /></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Check Setup' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Add Ignore Rule' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Confirm and append' }))
    await waitFor(() => expect(calls.filter(path => path === '/api/snapshot-ignore/execute')).toHaveLength(1))
    view.rerender(<I18nProvider><RepositorySetupReadiness repositoryIdentity="new-legacy-repository" /></I18nProvider>)
    expect(screen.queryByText('Snapshot ignore rule added.')).not.toBeInTheDocument()
    expect(screen.queryByText('Snapshot ignore update could not be verified. Inspect .gitignore before retrying.')).not.toBeInTheDocument()
    expect(calls.filter(path => path === '/api/repositories/setup-readiness')).toHaveLength(1)
    resolveExecute(jsonResponse({ status: 'verified-success' }))
    await act(async () => { await Promise.resolve(); await Promise.resolve() })
    expect(screen.queryByText('Snapshot ignore rule added.')).not.toBeInTheDocument()
    expect(screen.queryByText('Snapshot ignore update could not be verified. Inspect .gitignore before retrying.')).not.toBeInTheDocument()
    expect(calls.filter(path => path === '/api/repositories/setup-readiness')).toHaveLength(1)
    expect(calls.filter(path => path === '/api/snapshot-ignore/execute')).toHaveLength(1)
  })

  it.each([
    ['online and not tracked', { ...ready, overall: 'needs-attention', tracking: { status: 'not-tracked' } }, true],
    ['online and not tracked with unrelated attention', { ...ready, overall: 'unavailable', snapshot: { status: 'unsafe' }, gitHooks: { status: 'unknown' }, tracking: { status: 'not-tracked' } }, true],
    ['already tracked', { ...ready, tracking: { status: 'tracked' } }, false],
    ['tracking unavailable', { ...ready, overall: 'unavailable', tracking: { status: 'unavailable' } }, false],
    ['daemon offline', { ...ready, overall: 'needs-attention', daemon: { status: 'offline' }, tracking: { status: 'unavailable' } }, false],
    ['daemon unavailable', { ...ready, overall: 'unavailable', daemon: { status: 'unavailable' }, tracking: { status: 'unavailable' } }, false],
  ] as const)('exposes tracking CTA only for %s', async (_name, value, eligible) => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(jsonResponse(value)))
    render(<I18nProvider><RepositorySetupReadiness repositoryIdentity="repo-a" /></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Check Setup' }))
    await waitFor(() => expect(screen.getByText(statusDescriptionForTest(value)).textContent).toBeTruthy())
    if (eligible) expect(screen.getByRole('button', { name: 'Register with RoboRev' })).toBeInTheDocument()
    else expect(screen.queryByRole('button', { name: 'Register with RoboRev' })).not.toBeInTheDocument()
  })

  it('previews and confirms repository tracking exactly once with a token-only execute', async () => {
    const preview = { version: 'v0.64.0', repositoryIdentity: aliasA, action: 'register-repository', target: 'RoboRev tracking', summary: 'Register the selected repository with the running RoboRev daemon.', token: 'opaque-tracking-token', expiresAt: '2099-01-01T00:00:00.000Z' }
    const calls: Array<{ path: string; init?: RequestInit }> = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      calls.push({ path, init })
      if (path === '/api/repositories/setup-readiness') return jsonResponse({ ...ready, overall: 'needs-attention', tracking: { status: 'not-tracked' } })
      if (path === '/api/repository-tracking/preview') return jsonResponse(preview)
      if (path === '/api/repository-tracking/execute') return jsonResponse({ status: 'verified-success' })
      throw new Error(`unexpected path ${path}`)
    }))
    render(<I18nProvider><RepositorySetupReadiness repositoryIdentity="repo-a" /></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Check Setup' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Register with RoboRev' }))
    await waitFor(() => expect(screen.getByRole('dialog')).toBeInTheDocument())
    expect(calls.filter(call => call.path === '/api/repository-tracking/preview')).toHaveLength(1)
    expect(calls.filter(call => call.path === '/api/repository-tracking/execute')).toHaveLength(0)
    const dialog = screen.getByRole('dialog')
    expect(dialog).toHaveTextContent('Register Repository with RoboRev')
    expect(dialog).toHaveTextContent('Register the selected repository with the currently running RoboRev v0.64.0 daemon.')
    expect(dialog).toHaveTextContent('Start or restart the daemon, modify repository files, run a review, or commit or push anything.')
    for (const forbidden of ['C:\\Users\\private\\source\\secret-project', 'PRIVATE_REPOSITORY_PATH_DO_NOT_EXPOSE', 'PRIVATE_DAEMON_ENDPOINT', 'PRIVATE_ROBOREV_BINARY', 'PRIVATE_INVENTORY_ROOT', 'PRIVATE_REPO_IDENTITY', 'PRIVATE_DAEMON_ERROR']) expect(document.body.textContent).not.toContain(forbidden)
    const confirm = screen.getByRole('button', { name: 'Confirm and register' })
    fireEvent.click(confirm)
    fireEvent.click(confirm)
    await waitFor(() => expect(screen.getByText('Repository registered with RoboRev.')).toBeInTheDocument())
    expect(calls.filter(call => call.path === '/api/repository-tracking/execute')).toHaveLength(1)
    const execute = calls.find(call => call.path === '/api/repository-tracking/execute')!
    expect(JSON.parse(String(execute.init?.body))).toEqual({ token: 'opaque-tracking-token' })
    expect(Object.keys(JSON.parse(String(execute.init?.body)))).toEqual(['token'])
    expect(calls.filter(call => call.path === '/api/repositories/setup-readiness')).toHaveLength(1)
    expect(screen.getByText('Run Check Setup again to verify the complete repository setup.')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /retry/i })).not.toBeInTheDocument()
  })

  it('keeps tracking cancel execute-free and reports failure without an immediate retry', async () => {
    const preview = { version: 'v0.64.0', repositoryIdentity: aliasA, action: 'register-repository', target: 'RoboRev tracking', summary: 'Register the selected repository with the running RoboRev daemon.', token: 'opaque-tracking-token', expiresAt: '2099-01-01T00:00:00.000Z' }
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(jsonResponse({ ...ready, overall: 'needs-attention', tracking: { status: 'not-tracked' } }))
      .mockResolvedValueOnce(jsonResponse(preview))
      .mockRejectedValueOnce(new Error('PRIVATE_DAEMON_ERROR'))
    vi.stubGlobal('fetch', fetchMock)
    render(<I18nProvider><RepositorySetupReadiness repositoryIdentity="repo-a" /></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Check Setup' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Register with RoboRev' }))
    fireEvent.keyDown(await screen.findByRole('dialog'), { key: 'Escape' })
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(2)
    await userEvent.click(screen.getByRole('button', { name: 'Register with RoboRev' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Confirm and register' }))
    await waitFor(() => expect(screen.getByText('Repository registration could not be verified. Run Check Setup before trying again.')).toBeInTheDocument())
    expect(fetchMock).toHaveBeenCalledTimes(3)
    expect(screen.queryByRole('button', { name: /retry/i })).not.toBeInTheDocument()
    expect(document.body.textContent).not.toContain('PRIVATE_DAEMON_ERROR')
  })

  it('does not fetch on locale change and discards a late tracking execute after repository switch', async () => {
    const preview = { version: 'v0.64.0', repositoryIdentity: aliasA, action: 'register-repository', target: 'RoboRev tracking', summary: 'Register the selected repository with the running RoboRev daemon.', token: 'opaque-tracking-token', expiresAt: '2099-01-01T00:00:00.000Z' }
    let resolveExecute!: (response: Response) => void
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      calls.push(path)
      if (path === '/api/repositories/setup-readiness') return Promise.resolve(jsonResponse({ ...ready, overall: 'needs-attention', tracking: { status: 'not-tracked' } }))
      if (path === '/api/repository-tracking/preview') return Promise.resolve(jsonResponse(preview))
      if (path === '/api/repository-tracking/execute') return new Promise<Response>(resolve => { resolveExecute = resolve })
      return Promise.reject(new Error(`unexpected path ${path}`))
    }))
    const view = render(<I18nProvider><LanguageSelector /><RepositorySetupReadiness repositoryIdentity="repo-a" /></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Check Setup' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Register with RoboRev' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Confirm and register' }))
    await waitFor(() => expect(calls.filter(path => path === '/api/repository-tracking/execute')).toHaveLength(1))
    const count = calls.length
    view.rerender(<I18nProvider><LanguageSelector /><RepositorySetupReadiness repositoryIdentity="repo-b" /></I18nProvider>)
    await userEvent.selectOptions(screen.getByRole('combobox'), 'ko')
    expect(calls).toHaveLength(count)
    resolveExecute(jsonResponse({ status: 'verified-success' }))
    await act(async () => { await Promise.resolve(); await Promise.resolve() })
    expect(screen.queryByText('Repository registered with RoboRev.')).not.toBeInTheDocument()
    expect(screen.queryByText('저장소를 RoboRev에 등록했습니다.')).not.toBeInTheDocument()
  })

  it('locks duplicate tracking previews and clears the open dialog authority on repository switch', async () => {
    const preview = { version: 'v0.64.0', repositoryIdentity: aliasA, action: 'register-repository', target: 'RoboRev tracking', summary: 'Register the selected repository with the running RoboRev daemon.', token: 'opaque-tracking-token', expiresAt: '2099-01-01T00:00:00.000Z' }
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      calls.push(path)
      if (path === '/api/repositories/setup-readiness') return jsonResponse({ ...ready, overall: 'needs-attention', tracking: { status: 'not-tracked' } })
      if (path === '/api/repository-tracking/preview') return jsonResponse(preview)
      throw new Error(`unexpected path ${path}`)
    }))
    const view = render(<I18nProvider><RepositorySetupReadiness repositoryIdentity="repo-a" /></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Check Setup' }))
    const register = await screen.findByRole('button', { name: 'Register with RoboRev' })
    fireEvent.click(register)
    fireEvent.click(register)
    await waitFor(() => expect(screen.getByRole('dialog')).toBeInTheDocument())
    expect(calls.filter(path => path === '/api/repository-tracking/preview')).toHaveLength(1)
    expect(calls.filter(path => path === '/api/repository-tracking/execute')).toHaveLength(0)

    view.rerender(<I18nProvider><RepositorySetupReadiness repositoryIdentity="repo-b" /></I18nProvider>)
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(screen.queryByText('Repository registered with RoboRev.')).not.toBeInTheDocument()
    expect(calls.filter(path => path === '/api/repository-tracking/execute')).toHaveLength(0)
  })

  it('discards a late tracking preview after repository switch', async () => {
    const preview = { version: 'v0.64.0', repositoryIdentity: aliasA, action: 'register-repository', target: 'RoboRev tracking', summary: 'Register the selected repository with the running RoboRev daemon.', token: 'opaque-tracking-token', expiresAt: '2099-01-01T00:00:00.000Z' }
    let resolvePreview!: (response: Response) => void
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      calls.push(path)
      if (path === '/api/repositories/setup-readiness') return Promise.resolve(jsonResponse({ ...ready, overall: 'needs-attention', tracking: { status: 'not-tracked' } }))
      if (path === '/api/repository-tracking/preview') return new Promise<Response>(resolve => { resolvePreview = resolve })
      return Promise.reject(new Error(`unexpected path ${path}`))
    }))
    const view = render(<I18nProvider><RepositorySetupReadiness repositoryIdentity="repo-a" /></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Check Setup' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Register with RoboRev' }))
    await waitFor(() => expect(calls.filter(path => path === '/api/repository-tracking/preview')).toHaveLength(1))
    view.rerender(<I18nProvider><RepositorySetupReadiness repositoryIdentity="repo-b" /></I18nProvider>)
    resolvePreview(jsonResponse(preview))
    await act(async () => { await Promise.resolve(); await Promise.resolve() })
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(screen.queryByText('Registering repository with RoboRev…')).not.toBeInTheDocument()
    expect(calls.filter(path => path === '/api/repository-tracking/execute')).toHaveLength(0)
  })
})

function statusDescriptionForTest(value: { daemon: { status: string }; tracking: { status: string } }) {
  if (value.daemon.status !== 'online') return value.daemon.status === 'offline' ? 'RoboRev daemon is offline.' : 'RoboRev daemon could not be verified.'
  if (value.tracking.status === 'not-tracked') return 'This repository is not tracked by RoboRev.'
  if (value.tracking.status === 'tracked') return 'This repository is tracked by RoboRev.'
  return 'RoboRev tracking could not be verified.'
}
