import { cleanup, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { StrictMode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider, LanguageSelector } from './i18n'
import { normalizeSkillsReadiness } from './api'
import { resetSkillsReadinessForTests, SkillsReadiness } from './SkillsReadiness'

const allCurrent = {
  version: 'v0.64.0',
  agents: [
    { agent: 'codex', status: 'current', current: 10, outdated: 0, missing: 0, total: 10 },
    { agent: 'claude', status: 'current', current: 10, outdated: 0, missing: 0, total: 10 },
    { agent: 'grok', status: 'current', current: 10, outdated: 0, missing: 0, total: 10 },
    { agent: 'droid', status: 'current', current: 10, outdated: 0, missing: 0, total: 10 },
  ],
}

const mixed = {
  version: 'v0.64.0',
  agents: [
    { agent: 'codex', status: 'current', current: 10, outdated: 0, missing: 0, total: 10 },
    { agent: 'claude', status: 'needs-install-update', current: 8, outdated: 1, missing: 1, total: 10 },
    { agent: 'grok', status: 'config-unavailable', current: 0, outdated: 0, missing: 0, total: 10 },
    { agent: 'droid', status: 'needs-install-update', current: 9, outdated: 0, missing: 1, total: 10 },
  ],
}

function jsonResponse(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } })
}

function renderSkills() {
  return render(<I18nProvider><SkillsReadiness /></I18nProvider>)
}

function KeyedSkills({ repositoryKey }: { repositoryKey: string }) {
  return <SkillsReadiness key={repositoryKey} />
}

beforeEach(() => {
  resetSkillsReadinessForTests()
  window.localStorage.clear()
})

afterEach(() => {
  cleanup()
  resetSkillsReadinessForTests()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('Skills Readiness API normalization', () => {
  it('accepts the exact v0.64.0 fixed order and count semantics', () => {
    expect(normalizeSkillsReadiness(mixed)).toEqual(mixed)
  })

  it('rejects status/count inconsistencies, extra fields, private fields, and catalog drift', () => {
    const invalid: unknown[] = [
      null,
      { ...mixed, version: 'v0.63.0' },
      { ...mixed, extra: true },
      { ...mixed, agents: mixed.agents.slice(0, 3) },
      { ...mixed, agents: [{ ...mixed.agents[1] }, ...mixed.agents.slice(1)] },
      { ...mixed, agents: mixed.agents.map((agent, index) => index === 0 ? { ...agent, agent: 'unknown' } : agent) },
      { ...mixed, agents: mixed.agents.map((agent, index) => index === 0 ? { ...agent, total: 9 } : agent) },
      { ...mixed, agents: mixed.agents.map((agent, index) => index === 0 ? { ...agent, current: 9 } : agent) },
      { ...mixed, agents: mixed.agents.map((agent, index) => index === 1 ? { ...agent, status: 'installed' } : agent) },
      { ...mixed, agents: mixed.agents.map((agent, index) => index === 1 ? { ...agent, status: 'unknown' } : agent) },
      { ...mixed, agents: mixed.agents.map((agent, index) => index === 2 ? { ...agent, current: 1 } : agent) },
      { ...mixed, agents: mixed.agents.map((agent, index) => index === 3 ? { ...agent, missing: -1 } : agent) },
      { ...mixed, agents: mixed.agents.map((agent, index) => index === 1 ? { ...agent, current: Number.MAX_SAFE_INTEGER + 1 } : agent) },
      { ...mixed, agents: mixed.agents.map((agent, index) => index === 1 ? { ...agent, current: 1.5 } : agent) },
    ]
    for (const value of invalid) expect(normalizeSkillsReadiness(value)).toBeNull()
  })

  it('rejects privacy-sensitive fields without retaining them', () => {
    const privateFields = { configPath: 'C:\\Users\\private', binary: 'C:\\fake\\roborev.exe', stdout: 'PRIVATE_DESCRIPTION_MARKER', stderr: 'secret' }
    expect(normalizeSkillsReadiness({ ...mixed, agents: mixed.agents.map((agent, index) => index === 0 ? { ...agent, ...privateFields } : agent) })).toBeNull()
  })
})

describe('SkillsReadiness lifecycle and privacy', () => {
  it('renders fixed agent order, aggregate statuses, and sanitized counts', async () => {
    const fetchMock = vi.fn().mockReturnValue(Promise.resolve(jsonResponse(mixed)))
    vi.stubGlobal('fetch', fetchMock)
    renderSkills()
    const card = await screen.findByRole('region', { name: 'Skills Readiness' })
    await waitFor(() => expect(within(card).getByText('Factory Droid')).toBeInTheDocument())
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect([...card.querySelectorAll('h3')].map(element => element.textContent)).toEqual(['Codex', 'Claude Code', 'Grok Build', 'Factory Droid'])
    expect(within(card).getAllByText('Current')).toHaveLength(1)
    expect(within(card).getAllByText('Needs install/update')).toHaveLength(2)
    expect(within(card).getByText('Agent config not detected')).toBeInTheDocument()
    expect(within(card).getByText('10 / 10 current')).toBeInTheDocument()
    expect(within(card).getByText('8 current · 1 outdated · 1 missing')).toBeInTheDocument()
    expect(within(card).getByText('9 current · 0 outdated · 1 missing')).toBeInTheDocument()
    expect(within(card).getAllByRole('button')).toHaveLength(1)
    expect(within(card).getByRole('button', { name: 'Refresh skills readiness' })).toBeInTheDocument()
    for (const forbidden of [/install/i, /update/i, /remove/i, /uninstall/i, /open/i, /content/i]) {
      expect(within(card).queryByRole('button', { name: forbidden })).not.toBeInTheDocument()
    }
  })

  it('renders all Current states without mutation controls', async () => {
    const fetchMock = vi.fn().mockReturnValue(Promise.resolve(jsonResponse(allCurrent)))
    vi.stubGlobal('fetch', fetchMock)
    renderSkills()
    const card = await screen.findByRole('region', { name: 'Skills Readiness' })
    await waitFor(() => expect(within(card).getAllByText('Current')).toHaveLength(4))
    expect(within(card).queryByText(/install|update|remove|uninstall|open file|show content/i)).not.toBeInTheDocument()
  })

  it('coalesces StrictMode effects into exactly one GET', async () => {
    const fetchMock = vi.fn().mockReturnValue(Promise.resolve(jsonResponse(allCurrent)))
    vi.stubGlobal('fetch', fetchMock)
    render(<ReactStrictSkills />)
    await screen.findByText('Factory Droid')
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('refreshes once and shares the global settled result across locale and repository remounts', async () => {
    const fetchMock = vi.fn().mockReturnValue(Promise.resolve(jsonResponse(allCurrent)))
    vi.stubGlobal('fetch', fetchMock)
    const { rerender } = render(<I18nProvider><><KeyedSkills repositoryKey="repo-a" /><LanguageSelector /></></I18nProvider>)
    await screen.findByText('Factory Droid')
    await userEvent.click(screen.getByRole('button', { name: 'Refresh skills readiness' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Select language' }), 'ko')
    rerender(<I18nProvider><><KeyedSkills repositoryKey="repo-b" /><LanguageSelector /></></I18nProvider>)
    await screen.findByText('Factory Droid')
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('does not poll after the initial read', async () => {
    const fetchMock = vi.fn().mockReturnValue(Promise.resolve(jsonResponse(allCurrent)))
    vi.stubGlobal('fetch', fetchMock)
    renderSkills()
    await screen.findByText('Factory Droid')
    vi.useFakeTimers()
    await vi.advanceTimersByTimeAsync(60_000)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    vi.useRealTimers()
  })

  it('discards a superseded refresh response', async () => {
    let resolveFirst!: (value: Response) => void
    let resolveSecond!: (value: Response) => void
    let resolveThird!: (value: Response) => void
    const fetchMock = vi.fn()
      .mockReturnValueOnce(new Promise<Response>(resolve => { resolveFirst = resolve }))
      .mockReturnValueOnce(new Promise<Response>(resolve => { resolveSecond = resolve }))
      .mockReturnValueOnce(new Promise<Response>(resolve => { resolveThird = resolve }))
    vi.stubGlobal('fetch', fetchMock)
    renderSkills()
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    await userEvent.click(screen.getByRole('button', { name: 'Refresh skills readiness' }))
    await userEvent.click(screen.getByRole('button', { name: 'Refresh skills readiness' }))
    expect(fetchMock).toHaveBeenCalledTimes(3)
    resolveThird(jsonResponse(mixed))
    await screen.findByText('Agent config not detected')
    resolveSecond(jsonResponse(allCurrent))
    resolveFirst(jsonResponse({ ...allCurrent, agents: allCurrent.agents.map(agent => ({ ...agent, status: 'config-unavailable', current: 0 })) }))
    await Promise.resolve()
    expect(screen.getAllByText('Needs install/update')).toHaveLength(2)
    expect(screen.getAllByText('10 / 10 current')).toHaveLength(1)
  })

  it('ignores a valid late response after unmount', async () => {
    let resolve!: (value: Response) => void
    const fetchMock = vi.fn().mockReturnValue(new Promise<Response>(done => { resolve = done }))
    vi.stubGlobal('fetch', fetchMock)
    const view = renderSkills()
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    view.unmount()
    resolve(jsonResponse(allCurrent))
    await Promise.resolve()
    expect(document.body).not.toHaveTextContent('Factory Droid')
  })
})

function ReactStrictSkills() {
  // Kept as a component helper so the StrictMode import remains local to this
  // test file's render expression without changing production code.
  return <StrictMode><I18nProvider><SkillsReadiness /></I18nProvider></StrictMode>
}
