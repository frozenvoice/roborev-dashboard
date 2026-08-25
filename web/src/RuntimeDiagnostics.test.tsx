import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { StrictMode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider, useI18n } from './i18n'
import { RuntimeDiagnostics } from './RuntimeDiagnostics'

const healthy = {
  daemon: { running: true, version: 'v0.64.0', uptime: '2h 10m' },
  workers: { active: 1, max: 4 },
  queue: { paused: false, queued: 2, running: 1 },
  jobs: { completed: 100, failed: 3, skipped: 4 },
  health: { healthy: true, errorCount24h: 0, components: [{ name: 'database', healthy: true }, { name: 'workers', healthy: true }] },
}

const degraded = {
  ...healthy,
  health: { healthy: false, errorCount24h: 2, components: [{ name: 'database', healthy: true }, { name: 'workers', healthy: false }, { name: 'sync', healthy: true }] },
}

function jsonResponse(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } })
}

function LocaleSwitch() {
  const { setLocale } = useI18n()
  return <button onClick={() => setLocale('ko')}>한국어</button>
}

function renderDiagnostics(withLocaleSwitch = false) {
  return render(<I18nProvider><RuntimeDiagnostics />{withLocaleSwitch && <LocaleSwitch />}</I18nProvider>)
}

async function openDiagnostics() {
  await userEvent.click(screen.getByRole('button', { name: 'Diagnostics' }))
}

describe('RuntimeDiagnostics lifecycle', () => {
  let fetchMock: ReturnType<typeof vi.fn>

  beforeEach(() => {
    fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    window.localStorage.clear()
  })

  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
  })

  it('does not fetch on mount and fetches exactly once on first explicit open', async () => {
    fetchMock.mockResolvedValue(jsonResponse(healthy))
    renderDiagnostics()
    expect(fetchMock).not.toHaveBeenCalled()
    await openDiagnostics()
    await waitFor(() => expect(screen.getByText('v0.64.0')).toBeInTheDocument())
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock).toHaveBeenCalledWith('/api/runtime/diagnostics', expect.objectContaining({ signal: expect.any(AbortSignal) }))
  })

  it('keeps explicit open working after React StrictMode effect replay', async () => {
    fetchMock.mockResolvedValue(jsonResponse(healthy))
    render(<StrictMode><I18nProvider><RuntimeDiagnostics /></I18nProvider></StrictMode>)
    await openDiagnostics()
    await waitFor(() => expect(screen.getByText('v0.64.0')).toBeInTheDocument())
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('keeps the loaded DTO cached across collapse and reopen', async () => {
    fetchMock.mockResolvedValue(jsonResponse(healthy))
    renderDiagnostics()
    await openDiagnostics()
    await waitFor(() => expect(screen.getByText('v0.64.0')).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: 'Hide diagnostics' }))
    await userEvent.click(screen.getByRole('button', { name: 'Diagnostics' }))
    expect(screen.getByText('v0.64.0')).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('refreshes once and leaves Refresh usable while loading', async () => {
    const first = Promise.resolve(jsonResponse(healthy))
    const second = Promise.resolve(jsonResponse({ ...healthy, daemon: { ...healthy.daemon, uptime: '3h 10m' } }))
    fetchMock.mockReturnValueOnce(first).mockReturnValueOnce(second)
    renderDiagnostics()
    await openDiagnostics()
    await waitFor(() => expect(screen.getByText('v0.64.0')).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeEnabled()
    await waitFor(() => expect(screen.getByText('3h 10m')).toBeInTheDocument())
  })

  it('retries exactly once after an unavailable response', async () => {
    fetchMock.mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce(jsonResponse(healthy))
    renderDiagnostics()
    await openDiagnostics()
    await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument())
    expect(screen.getByText('Unavailable / Offline')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Retry diagnostics' }))
    await waitFor(() => expect(screen.getByText('v0.64.0')).toBeInTheDocument())
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('renders healthy, degraded, and count/component states as typed rows', async () => {
    fetchMock.mockResolvedValue(jsonResponse(degraded))
    renderDiagnostics()
    await openDiagnostics()
    await waitFor(() => expect(screen.getByText('Degraded')).toBeInTheDocument())
    expect(screen.getByText('1 / 4')).toBeInTheDocument()
    expect(screen.getByText('Processing')).toHaveClass('status-pass')
    expect(screen.getAllByText('2')).toHaveLength(2)
    expect(screen.getByText('100')).toBeInTheDocument()
    expect(screen.getByText('workers')).toBeInTheDocument()
    expect(screen.getByText('Unhealthy')).toBeInTheDocument()
  })

  it('renders paused queues with the incumbent amber queued status', async () => {
    fetchMock.mockResolvedValue(jsonResponse({ ...healthy, queue: { ...healthy.queue, paused: true } }))
    renderDiagnostics()
    await openDiagnostics()
    await waitFor(() => expect(screen.getByText('Paused')).toBeInTheDocument())
    expect(screen.getByText('Paused')).toHaveClass('status-queued')
  })

  it('shows offline safely without exposing arbitrary response bodies', async () => {
    fetchMock.mockResolvedValue(new Response('secret machine path', { status: 503 }))
    renderDiagnostics()
    await openDiagnostics()
    await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument())
    expect(screen.getByText('Unavailable / Offline')).toBeInTheDocument()
    expect(screen.queryByText('secret machine path')).not.toBeInTheDocument()
  })

  it('switches locale without fetching again or losing the loaded state', async () => {
    fetchMock.mockResolvedValue(jsonResponse(healthy))
    renderDiagnostics(true)
    await openDiagnostics()
    await waitFor(() => expect(screen.getByText('Runtime Diagnostics')).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: '한국어' }))
    expect(screen.getByText('런타임 진단')).toBeInTheDocument()
    expect(screen.getByText('v0.64.0')).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('discards an older response when a newer refresh completes first', async () => {
    let resolveFirst!: (value: Response) => void
    let resolveSecond!: (value: Response) => void
    fetchMock.mockReturnValueOnce(new Promise<Response>(resolve => { resolveFirst = resolve })).mockReturnValueOnce(new Promise<Response>(resolve => { resolveSecond = resolve }))
    renderDiagnostics()
    await openDiagnostics()
    await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    resolveSecond(jsonResponse({ ...healthy, daemon: { ...healthy.daemon, uptime: 'newer' } }))
    await waitFor(() => expect(screen.getByText('newer')).toBeInTheDocument())
    resolveFirst(jsonResponse({ ...healthy, daemon: { ...healthy.daemon, uptime: 'older' } }))
    await Promise.resolve()
    expect(screen.getByText('newer')).toBeInTheDocument()
    expect(screen.queryByText('older')).not.toBeInTheDocument()
  })

  it('discards a late response after unmount', async () => {
    let resolve!: (value: Response) => void
    fetchMock.mockReturnValue(new Promise<Response>(done => { resolve = done }))
    const view = renderDiagnostics()
    await openDiagnostics()
    view.unmount()
    resolve(jsonResponse(healthy))
    await Promise.resolve()
    expect(document.body).not.toHaveTextContent('v0.64.0')
  })
})
