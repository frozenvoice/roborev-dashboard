import { StrictMode } from 'react'
import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AgentHookReadiness, resetAgentHookReadinessForTests } from './AgentHookReadiness'
import { normalizeAgentHookReadiness } from './api'
import { I18nProvider, LanguageSelector } from './i18n'

const ready = {
  version: 'v0.64.0',
  agents: [
    { agent: 'codex', status: 'current' },
    { agent: 'claude', status: 'needs-update' },
    { agent: 'grok', status: 'unavailable' },
    { agent: 'droid', status: 'current' },
  ],
}

function jsonResponse(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } })
}

function renderReadiness() {
  return render(<I18nProvider><AgentHookReadiness /></I18nProvider>)
}

function KeyedReadiness({ repositoryKey }: { repositoryKey: string }) {
  return <AgentHookReadiness key={repositoryKey} />
}

beforeEach(() => {
  resetAgentHookReadinessForTests()
  window.localStorage.clear()
})

afterEach(() => {
  cleanup()
  vi.useRealTimers()
  resetAgentHookReadinessForTests()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('Agent Hook Readiness API normalization', () => {
  it('accepts only the exact v0.64.0 fixed agent order and statuses', () => {
    expect(normalizeAgentHookReadiness(ready)).toEqual(ready)
    expect(normalizeAgentHookReadiness({ ...ready, agents: ready.agents.map(agent => ({ ...agent })) })).toEqual(ready)
  })

  it('rejects extra, reordered, unknown, and privacy-sensitive fields', () => {
    const privateMarkers = {
      path: 'C:\\Users\\private',
      binary: 'C:\\fake\\very-secret\\roborev.exe',
      stdout: 'raw stdout marker',
      stderr: 'raw stderr marker',
      command: 'raw command marker',
      argv: ['raw argv marker'],
    }
    const invalid: unknown[] = [
      null,
      { ...ready, version: 'v0.63.0' },
      { ...ready, extra: 'C:\\secret\\binary.exe' },
      { ...ready, agents: ready.agents.slice(0, 3) },
      { ...ready, agents: [{ ...ready.agents[1] }, ...ready.agents.slice(1)] },
      { ...ready, agents: ready.agents.map((agent, index) => index === 0 ? { ...agent, ...privateMarkers } : agent) },
      { ...ready, agents: ready.agents.map(agent => ({ ...agent, stdout: 'secret-token', stderr: 'command' })) },
      { ...ready, agents: ready.agents.map(agent => ({ ...agent, status: 'current', command: 'redacted-command' })) },
      { ...ready, agents: ready.agents.map(agent => ({ ...agent, agent: 'unknown' })) },
      { ...ready, agents: ready.agents.map(agent => ({ ...agent, status: 'installed' })) },
    ]
    for (const value of invalid) expect(normalizeAgentHookReadiness(value)).toBeNull()
  })
})

describe('AgentHookReadiness lifecycle and privacy', () => {
  it('fetches once and renders exact rows, order, and status labels', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(ready))
    vi.stubGlobal('fetch', fetchMock)
    renderReadiness()
    await waitFor(() => expect(screen.getByText('Factory Droid')).toBeInTheDocument())
    const card = screen.getByRole('region', { name: 'Agent Hook Readiness' })
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock).toHaveBeenCalledWith('/api/agent-hooks/readiness', expect.objectContaining({ signal: expect.any(AbortSignal) }))
    expect([...card.querySelectorAll('h3')].map(element => element.textContent)).toEqual(['Codex', 'Claude', 'Grok Build', 'Factory Droid'])
    expect(within(card).getAllByText('Current')).toHaveLength(2)
    expect(within(card).getByText('Needs install/update')).toBeInTheDocument()
    expect(within(card).getByText('Unavailable')).toBeInTheDocument()
    expect(within(card).getAllByRole('button')).toHaveLength(1)
    expect(within(card).getByRole('button', { name: 'Refresh agent hook readiness' })).toBeInTheDocument()
    for (const forbidden of [/install/i, /update/i, /reset/i, /start/i, /restart/i, /stop/i, /daemon/i]) {
      expect(within(card).queryByRole('button', { name: forbidden })).not.toBeInTheDocument()
    }
  })

  it('renders all four Current statuses when every agent is current', async () => {
    const allCurrent = { ...ready, agents: ready.agents.map(agent => ({ ...agent, status: 'current' })) }
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(allCurrent))
    vi.stubGlobal('fetch', fetchMock)
    renderReadiness()
    await waitFor(() => expect(screen.getByText('Factory Droid')).toBeInTheDocument())
    const card = screen.getByRole('region', { name: 'Agent Hook Readiness' })
    expect(within(card).getAllByText('Current')).toHaveLength(4)
    expect(within(card).getAllByRole('status')).toHaveLength(4)
  })

  it('coalesces StrictMode effects into exactly one GET', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(ready))
    vi.stubGlobal('fetch', fetchMock)
    render(<StrictMode><I18nProvider><AgentHookReadiness /></I18nProvider></StrictMode>)
    await waitFor(() => expect(screen.getByText('Factory Droid')).toBeInTheDocument())
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('refreshes exactly once and does not refetch after an explicit repository-key remount', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(ready))
    vi.stubGlobal('fetch', fetchMock)
    const { rerender } = render(<I18nProvider><KeyedReadiness repositoryKey="repo-a" /></I18nProvider>)
    await waitFor(() => expect(screen.getByText('Factory Droid')).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: 'Refresh agent hook readiness' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    rerender(<I18nProvider><KeyedReadiness repositoryKey="repo-b" /></I18nProvider>)
    await waitFor(() => expect(screen.getByText('Factory Droid')).toBeInTheDocument())
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('switches locale through the real LanguageSelector without another GET', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(ready))
    vi.stubGlobal('fetch', fetchMock)
    render(<I18nProvider><><AgentHookReadiness /><LanguageSelector /></></I18nProvider>)
    await waitFor(() => expect(screen.getByText('Factory Droid')).toBeInTheDocument())
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Select language' }), 'ko')
    expect(screen.getByRole('region', { name: '에이전트 Hook 준비 상태' })).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('remains steady without timer polling after the initial settled GET', async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(ready))
    vi.stubGlobal('fetch', fetchMock)
    renderReadiness()
    await waitFor(() => expect(screen.getByText('Factory Droid')).toBeInTheDocument())
    vi.useFakeTimers()
    await vi.advanceTimersByTimeAsync(60_000)
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('supersedes an older refresh and discards its late result', async () => {
    let resolveFirst!: (value: Response) => void
    let resolveSecond!: (value: Response) => void
    let resolveThird!: (value: Response) => void
    const fetchMock = vi.fn()
      .mockReturnValueOnce(new Promise<Response>(resolve => { resolveFirst = resolve }))
      .mockReturnValueOnce(new Promise<Response>(resolve => { resolveSecond = resolve }))
      .mockReturnValueOnce(new Promise<Response>(resolve => { resolveThird = resolve }))
    vi.stubGlobal('fetch', fetchMock)
    renderReadiness()
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    await userEvent.click(screen.getByRole('button', { name: 'Refresh agent hook readiness' }))
    await userEvent.click(screen.getByRole('button', { name: 'Refresh agent hook readiness' }))
    expect(fetchMock).toHaveBeenCalledTimes(3)
    resolveThird(jsonResponse({ ...ready, agents: ready.agents.map(agent => ({ ...agent, status: 'needs-update' })) }))
    await waitFor(() => expect(screen.getByText('Factory Droid')).toBeInTheDocument())
    resolveSecond(jsonResponse(ready))
    resolveFirst(jsonResponse({ ...ready, agents: ready.agents.map(agent => ({ ...agent, status: 'unavailable' })) }))
    await Promise.resolve()
    expect(screen.getAllByText('Needs install/update')).toHaveLength(4)
    expect(screen.queryByText('Current')).not.toBeInTheDocument()
  })

  it('caches failure, renders a safe retry state, and retries only on Refresh', async () => {
    const fetchMock = vi.fn().mockRejectedValueOnce(new Error('C:\\secret\\stdout-token')).mockResolvedValueOnce(jsonResponse(ready))
    vi.stubGlobal('fetch', fetchMock)
    renderReadiness()
    await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument())
    expect(screen.getByText('Readiness could not be verified. Refresh to try again.')).toBeInTheDocument()
    expect(screen.queryByText(/secret|stdout-token/i)).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Refresh agent hook readiness' }))
    await waitFor(() => expect(screen.getByText('Factory Droid')).toBeInTheDocument())
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('ignores a late response after unmount', async () => {
    let resolve!: (value: Response) => void
    const fetchMock = vi.fn().mockReturnValue(new Promise<Response>(done => { resolve = done }))
    vi.stubGlobal('fetch', fetchMock)
    const view = renderReadiness()
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    view.unmount()
    resolve(jsonResponse(ready))
    await Promise.resolve()
    expect(document.body).not.toHaveTextContent('Factory Droid')
  })

  it('rejects privacy fields without putting them in the DOM', async () => {
    const privateMarkers = ['C:\\Users\\private', 'C:\\fake\\very-secret\\roborev.exe', 'raw stdout marker', 'raw stderr marker', 'raw command marker', 'raw argv marker']
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ ...ready, agents: ready.agents.map((agent, index) => index === 0 ? { ...agent, path: privateMarkers[0], binary: privateMarkers[1], stdout: privateMarkers[2], stderr: privateMarkers[3], command: privateMarkers[4], argv: [privateMarkers[5]] } : agent) }))
    vi.stubGlobal('fetch', fetchMock)
    renderReadiness()
    await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument())
    for (const marker of privateMarkers) expect(document.body).not.toHaveTextContent(marker)
  })
})
