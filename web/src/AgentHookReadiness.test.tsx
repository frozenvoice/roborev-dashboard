import { StrictMode } from 'react'
import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AgentHookReadiness, resetAgentHookReadinessForTests } from './AgentHookReadiness'
import { normalizeAgentHookManagementPreview, normalizeAgentHookManagementResult, normalizeAgentHookReadiness } from './api'
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

const preview = {
  token: 'preview-token',
  agent: 'claude',
  action: 'install-update',
  summary: 'Install or update RoboRev hooks for Claude',
  command: {
    executable: 'roborev',
    args: ['agent-hook', 'install', '--agent', 'claude', '--binary', '<current-v0.64.0-binary>'],
  },
  expiresAt: '2030-01-01T00:00:00Z',
}

const verifiedResult = { status: 'verified-success', agent: 'claude' }
const verifiedWarningResult = { status: 'verified-success', agent: 'claude', errorCode: 'cli-failed' }
const failedResult = { status: 'failed-unverified', agent: 'claude', errorCode: 'readback-unavailable' }

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

describe('Agent Hook Management API normalization', () => {
  it('accepts the exact sanitized preview and terminal result DTOs', () => {
    expect(normalizeAgentHookManagementPreview(preview, 'claude')).toEqual(preview)
    expect(normalizeAgentHookManagementResult(verifiedResult, 'claude')).toEqual(verifiedResult)
    expect(normalizeAgentHookManagementResult(verifiedWarningResult, 'claude')).toEqual(verifiedWarningResult)
    expect(normalizeAgentHookManagementResult(failedResult, 'claude')).toEqual(failedResult)
  })

  it('rejects private paths, raw process fields, wrong agents, and extra keys', () => {
    const privateFields = {
      path: 'C:\\Users\\private',
      binary: 'C:\\fake\\very-secret\\roborev.exe',
      stdout: 'raw stdout',
      stderr: 'raw stderr',
      command: 'raw command',
      argv: ['raw argv'],
    }
    expect(normalizeAgentHookManagementPreview({ ...preview, ...privateFields }, 'claude')).toBeNull()
    expect(normalizeAgentHookManagementPreview({ ...preview, agent: 'codex' }, 'claude')).toBeNull()
    expect(normalizeAgentHookManagementPreview({ ...preview, summary: 'C:\\Users\\private' }, 'claude')).toBeNull()
    expect(normalizeAgentHookManagementPreview({ ...preview, command: { ...preview.command, args: [...preview.command.args, 'extra'] } }, 'claude')).toBeNull()
    expect(normalizeAgentHookManagementResult({ ...verifiedResult, ...privateFields }, 'claude')).toBeNull()
    expect(normalizeAgentHookManagementResult({ status: 'verified-success', agent: 'claude', errorCode: '' }, 'claude')).toBeNull()
    expect(normalizeAgentHookManagementResult({ status: 'verified-success', agent: 'claude', errorCode: 'readback-unavailable' }, 'claude')).toBeNull()
    expect(normalizeAgentHookManagementResult({ status: 'failed-unverified', agent: 'claude' }, 'claude')).toBeNull()
    expect(normalizeAgentHookManagementResult({ status: 'failed-unverified', agent: 'claude', errorCode: 'binding-drift' }, 'claude')).toEqual({ status: 'failed-unverified', agent: 'claude', errorCode: 'binding-drift' })
    expect(normalizeAgentHookManagementResult({ status: 'failed-unverified', agent: 'claude', errorCode: 'private-cli-output' }, 'claude')).toBeNull()
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
    expect(within(card).getAllByRole('button')).toHaveLength(2)
    expect(within(card).getByRole('button', { name: 'Refresh agent hook readiness' })).toBeInTheDocument()
    expect(within(card).getByRole('button', { name: 'Install / Update' })).toBeInTheDocument()
    for (const forbidden of [/install all/i, /update all/i, /remove/i, /uninstall/i, /reset/i, /start/i, /restart/i, /stop/i, /daemon/i]) {
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

function managementFetchMock(result: unknown = verifiedResult) {
  const counts = { readiness: 0, preview: 0, execute: 0 }
  const fetchMock = vi.fn((input: unknown, init?: RequestInit) => {
    const path = String(input)
    if (path.endsWith('/api/agent-hooks/readiness')) {
      counts.readiness += 1
      return Promise.resolve(jsonResponse(ready))
    }
    if (path.endsWith('/api/agent-hooks/preview')) {
      counts.preview += 1
      return Promise.resolve(jsonResponse(preview))
    }
    if (path.endsWith('/api/agent-hooks/execute')) {
      counts.execute += 1
      return Promise.resolve(jsonResponse(result))
    }
    throw new Error(`Unexpected request ${path} ${init?.method ?? 'GET'}`)
  })
  return { fetchMock, counts }
}

describe('Agent Hook Management v2', () => {
  it.each([
    ['codex', 0],
    ['claude', 1],
    ['grok', 2],
    ['droid', 3],
  ] as const)('offers one independent Install / Update CTA for %s', async (agent, index) => {
    const fixture = { ...ready, agents: ready.agents.map((item, itemIndex) => ({ ...item, status: itemIndex === index ? 'needs-update' : 'current' })) }
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(fixture))
    vi.stubGlobal('fetch', fetchMock)
    renderReadiness()
    await waitFor(() => expect(screen.getByText('Factory Droid')).toBeInTheDocument())
    const card = screen.getByRole('region', { name: 'Agent Hook Readiness' })
    const labels = { codex: 'Codex', claude: 'Claude', grok: 'Grok Build', droid: 'Factory Droid' } as const
    const targetRow = [...card.querySelectorAll('[role="listitem"]')].find(item => item.textContent?.includes(labels[agent]))
    expect(targetRow).toBeTruthy()
    expect(within(targetRow as HTMLElement).getByRole('button', { name: 'Install / Update' })).toBeInTheDocument()
    expect(within(card).queryByRole('button', { name: /install all|update all|remove|uninstall|reset|daemon/i })).not.toBeInTheDocument()
  })

  it('sends exactly one preview request from the scoped CTA and keeps the command semantic', async () => {
    const { fetchMock, counts } = managementFetchMock()
    vi.stubGlobal('fetch', fetchMock)
    renderReadiness()
    await waitFor(() => expect(screen.getByText('Factory Droid')).toBeInTheDocument())
    const card = screen.getByRole('region', { name: 'Agent Hook Readiness' })
    await userEvent.click(within(card).getByRole('button', { name: 'Install / Update' }))
    await waitFor(() => expect(screen.getByRole('dialog')).toBeInTheDocument())
    expect(counts.preview).toBe(1)
    expect(counts.execute).toBe(0)
    const call = fetchMock.mock.calls.find(call => String(call[0]).endsWith('/api/agent-hooks/preview'))
    expect(JSON.parse(String((call?.[1] as RequestInit).body))).toEqual({ agent: 'claude' })
    expect(screen.getByRole('dialog')).toHaveTextContent('["roborev","agent-hook","install","--agent","claude","--binary","<current-v0.64.0-binary>"]')
    expect(screen.queryByText(/C:\\Users|very-secret|stdout|stderr|argv/i)).not.toBeInTheDocument()
  })

  it('cancels without execute, traps focus, handles Escape, and restores CTA focus', async () => {
    const { fetchMock, counts } = managementFetchMock()
    vi.stubGlobal('fetch', fetchMock)
    const user = userEvent.setup()
    renderReadiness()
    await waitFor(() => expect(screen.getByText('Factory Droid')).toBeInTheDocument())
    const cta = screen.getByRole('button', { name: 'Install / Update' })
    await user.click(cta)
    await waitFor(() => expect(screen.getByRole('dialog')).toBeInTheDocument())
    expect(screen.getByRole('button', { name: 'Cancel' })).toHaveFocus()
    await user.keyboard('{Shift>}{Tab}{/Shift}')
    expect(screen.getByRole('button', { name: 'Close agent hook confirmation' })).toHaveFocus()
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(counts.execute).toBe(0)
    expect(cta).toHaveFocus()
  })

  it('confirms exactly once even when the confirmation control is double-clicked', async () => {
    const { fetchMock, counts } = managementFetchMock()
    vi.stubGlobal('fetch', fetchMock)
    const user = userEvent.setup()
    renderReadiness()
    await waitFor(() => expect(screen.getByText('Factory Droid')).toBeInTheDocument())
    await user.click(screen.getByRole('button', { name: 'Install / Update' }))
    await waitFor(() => expect(screen.getByRole('dialog')).toBeInTheDocument())
    await user.dblClick(within(screen.getByRole('dialog')).getByRole('button', { name: 'Install / Update' }))
    await waitFor(() => expect(screen.getByText('Verified success')).toBeInTheDocument())
    expect(counts.execute).toBe(1)
    expect(counts.readiness).toBe(2)
    expect(fetchMock.mock.calls.filter(call => String(call[0]).endsWith('/api/agent-hooks/execute'))).toHaveLength(1)
  })

  it('renders a safe failed-unverified receipt and reloads readiness once', async () => {
    const { fetchMock, counts } = managementFetchMock(failedResult)
    vi.stubGlobal('fetch', fetchMock)
    const user = userEvent.setup()
    renderReadiness()
    await waitFor(() => expect(screen.getByText('Factory Droid')).toBeInTheDocument())
    await user.click(screen.getByRole('button', { name: 'Install / Update' }))
    await waitFor(() => expect(screen.getByRole('dialog')).toBeInTheDocument())
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Install / Update' }))
    await waitFor(() => expect(screen.getByText('Failed · unverified')).toBeInTheDocument())
    expect(counts.execute).toBe(1)
    expect(counts.readiness).toBe(2)
    expect(screen.queryByText('readback-unavailable')).not.toBeInTheDocument()
  })

  it('distinguishes a verified CLI warning from an unverified post-check', async () => {
    const { fetchMock, counts } = managementFetchMock(verifiedWarningResult)
    vi.stubGlobal('fetch', fetchMock)
    const user = userEvent.setup()
    renderReadiness()
    await waitFor(() => expect(screen.getByText('Factory Droid')).toBeInTheDocument())
    await user.click(screen.getByRole('button', { name: 'Install / Update' }))
    await waitFor(() => expect(screen.getByRole('dialog')).toBeInTheDocument())
    await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Install / Update' }))
    await waitFor(() => expect(screen.getByText('Verified success')).toBeInTheDocument())
    expect(screen.getByText(/reported an error, but the fresh post-check verified/i)).toBeInTheDocument()
    expect(screen.queryByText(/could not verify the final hook state/i)).not.toBeInTheDocument()
    expect(counts).toEqual({ readiness: 2, preview: 1, execute: 1 })
  })

  it('does not leak private paths, binaries, or raw process fields from malformed preview or result', async () => {
    const markers = ['C:\\Users\\private', 'C:\\fake\\very-secret\\roborev.exe', 'raw stdout', 'raw stderr', 'raw command', 'raw argv']
    const malformedPreview = { ...preview, path: markers[0], binary: markers[1], stdout: markers[2], stderr: markers[3], commandText: markers[4], argv: [markers[5]] }
    const fetchMock = vi.fn((input: unknown) => {
      const path = String(input)
      if (path.endsWith('/readiness')) return Promise.resolve(jsonResponse(ready))
      if (path.endsWith('/preview')) return Promise.resolve(jsonResponse(malformedPreview))
      return Promise.resolve(jsonResponse({ ...verifiedResult, path: markers[0], stdout: markers[2] }))
    })
    vi.stubGlobal('fetch', fetchMock)
    renderReadiness()
    await waitFor(() => expect(screen.getByText('Factory Droid')).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: 'Install / Update' }))
    await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument())
    for (const marker of markers) expect(document.body).not.toHaveTextContent(marker)
  })

  it('keeps locale changes and repository-key remounts request-free after the cached readiness load', async () => {
    const { fetchMock, counts } = managementFetchMock()
    vi.stubGlobal('fetch', fetchMock)
    const user = userEvent.setup()
    const { rerender } = render(<I18nProvider><><KeyedReadiness repositoryKey="repo-a" /><LanguageSelector /></></I18nProvider>)
    await waitFor(() => expect(screen.getByText('Factory Droid')).toBeInTheDocument())
    await user.selectOptions(screen.getByRole('combobox', { name: 'Select language' }), 'ko')
    rerender(<I18nProvider><><KeyedReadiness repositoryKey="repo-b" /><LanguageSelector /></></I18nProvider>)
    await waitFor(() => expect(screen.getByRole('region', { name: '에이전트 Hook 준비 상태' })).toBeInTheDocument())
    expect(counts.readiness).toBe(1)
  })

  it('ignores a late preview completion after the card unmounts', async () => {
    let resolvePreview!: (value: Response) => void
    const fetchMock = vi.fn((input: unknown) => {
      const path = String(input)
      if (path.endsWith('/readiness')) return Promise.resolve(jsonResponse(ready))
      return new Promise<Response>(resolve => { resolvePreview = resolve })
    })
    vi.stubGlobal('fetch', fetchMock)
    const view = renderReadiness()
    await waitFor(() => expect(screen.getByText('Factory Droid')).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: 'Install / Update' }))
    view.unmount()
    resolvePreview(jsonResponse(preview))
    await Promise.resolve()
    expect(document.body).not.toHaveTextContent('Install / Update RoboRev Agent Hook')
  })

  it('ignores a late execute completion after unmount without starting a readiness reload', async () => {
    let resolveExecute!: (value: Response) => void
    const counts = { readiness: 0, preview: 0, execute: 0 }
    const fetchMock = vi.fn((input: unknown) => {
      const path = String(input)
      if (path.endsWith('/readiness')) {
        counts.readiness += 1
        return Promise.resolve(jsonResponse(ready))
      }
      if (path.endsWith('/preview')) {
        counts.preview += 1
        return Promise.resolve(jsonResponse(preview))
      }
      if (path.endsWith('/execute')) {
        counts.execute += 1
        return new Promise<Response>(resolve => { resolveExecute = resolve })
      }
      throw new Error(`Unexpected request ${path}`)
    })
    vi.stubGlobal('fetch', fetchMock)
    const view = renderReadiness()
    await waitFor(() => expect(screen.getByText('Factory Droid')).toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: 'Install / Update' }))
    await waitFor(() => expect(screen.getByRole('dialog')).toBeInTheDocument())
    await userEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Install / Update' }))
    await waitFor(() => expect(counts.execute).toBe(1))
    view.unmount()
    resolveExecute(jsonResponse(verifiedResult))
    await Promise.resolve()
    await Promise.resolve()
    expect(counts).toEqual({ readiness: 1, preview: 1, execute: 1 })
  })
})
