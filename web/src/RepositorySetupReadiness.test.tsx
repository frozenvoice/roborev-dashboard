import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { setCSRF } from './api'
import { RepositorySetupReadiness } from './RepositorySetupReadiness'
import { I18nProvider, LanguageSelector } from './i18n'

const ready = {
  version: 'v0.64.0', repositoryIdentity: 'repo-a', overall: 'ready',
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
    deferred[1].resolve(jsonResponse({ ...ready, repositoryIdentity: 'repo-b' }))
    await waitFor(() => expect(screen.getByText('All repository setup checks passed.')).toBeInTheDocument())
    deferred[0].resolve(jsonResponse({
      ...ready,
      repositoryIdentity: 'repo-a',
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
})
