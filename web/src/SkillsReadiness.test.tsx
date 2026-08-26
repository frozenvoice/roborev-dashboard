import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { StrictMode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider, LanguageSelector } from './i18n'
import { normalizeSkillsManagementPreview, normalizeSkillsManagementResult, normalizeSkillsReadiness } from './api'
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

type SupportedAgent = 'codex' | 'claude' | 'grok' | 'droid'

function readinessWithNeeds(agent: SupportedAgent) {
  return {
    version: 'v0.64.0',
    agents: allCurrent.agents.map(item => item.agent === agent ? { ...item, status: 'needs-install-update', current: 9, outdated: 1, missing: 0 } : item),
  }
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

function previewFor(agent: SupportedAgent) {
  const label = agent === 'codex' ? 'Codex' : agent === 'claude' ? 'Claude Code' : agent === 'grok' ? 'Grok Build' : 'Factory Droid'
  return {
    token: `token-${agent}`,
    agent,
    action: 'install-update',
    summary: `Install or update RoboRev skills for ${label}`,
    command: { executable: 'roborev', args: ['skills', 'install', '--path', '<agent-skills-directory>', '--agent', agent] },
    expiresAt: '2026-08-26T12:00:00Z',
  }
}

function resultFor(agent: SupportedAgent, status: 'verified-success' | 'partial' | 'failed-unverified' = 'verified-success', errorCode?: string) {
  return { status, agent, ...(errorCode ? { errorCode } : {}) }
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
    const privateFields = { configPath: 'C:\\Users\\private', skillsDir: 'C:\\fake-home\\.codex\\skills', HOME: 'C:\\fake-home', binary: 'C:\\fake\\roborev.exe', stdout: 'PRIVATE_DESCRIPTION_MARKER', stderr: 'secret' }
    expect(normalizeSkillsReadiness({ ...mixed, agents: mixed.agents.map((agent, index) => index === 0 ? { ...agent, ...privateFields } : agent) })).toBeNull()
  })

  it('accepts each supported agent preview with the exact semantic command', () => {
    for (const agent of ['codex', 'claude', 'grok', 'droid'] as const) {
      expect(normalizeSkillsManagementPreview(previewFor(agent), agent)).toEqual(previewFor(agent))
    }
  })

  it('rejects preview path, binary, content, extra-field, and non-strict timestamp leakage', () => {
    const base = previewFor('codex')
    const invalid: unknown[] = [
      { ...base, configPath: 'C:\\Users\\private\\.codex' },
      { ...base, skillsDir: 'C:\\fake-home\\.codex\\skills', HOME: 'C:\\fake-home' },
      { ...base, command: { ...base.command, executable: 'C:\\fake\\roborev.exe' } },
      { ...base, stdout: 'PRIVATE_SKILL_CONTENT' },
      { ...base, command: { ...base.command, args: [...base.command.args, '--output', 'secret'] } },
      { ...base, expiresAt: '2026-08-26' },
      { ...base, expiresAt: '2026-08-26T12:00:00' },
      { ...base, summary: 'Install or update RoboRev hooks for Codex' },
      { ...base, command: { ...base.command, args: ['skills', 'install', '--agent', 'codex'] } },
      { ...base, agent: 'all' },
    ]
    for (const value of invalid) expect(normalizeSkillsManagementPreview(value, 'codex')).toBeNull()
  })

  it('accepts only honest result states and error combinations', () => {
    expect(normalizeSkillsManagementResult(resultFor('codex'), 'codex')).toEqual(resultFor('codex'))
    expect(normalizeSkillsManagementResult(resultFor('codex', 'verified-success', 'cli-failed'), 'codex')).toEqual(resultFor('codex', 'verified-success', 'cli-failed'))
    expect(normalizeSkillsManagementResult(resultFor('codex', 'partial'), 'codex')).toEqual(resultFor('codex', 'partial'))
    expect(normalizeSkillsManagementResult(resultFor('codex', 'partial', 'cli-failed'), 'codex')).toEqual(resultFor('codex', 'partial', 'cli-failed'))
    for (const errorCode of ['cli-failed', 'cli-not-started', 'binding-drift', 'readback-unavailable', 'target-drift', 'readback-not-current', 'no-improvement']) {
      expect(normalizeSkillsManagementResult(resultFor('codex', 'failed-unverified', errorCode), 'codex')).not.toBeNull()
    }
    const invalid: unknown[] = [
      resultFor('codex', 'verified-success', 'binding-drift'),
      resultFor('codex', 'partial', 'no-improvement'),
      resultFor('codex', 'failed-unverified'),
      { ...resultFor('codex'), output: 'PRIVATE' },
      { ...resultFor('codex'), agent: 'claude' },
      { ...resultFor('codex'), errorCode: 'unknown' },
    ]
    for (const value of invalid) expect(normalizeSkillsManagementResult(value, 'codex')).toBeNull()
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
    expect(within(card).getAllByRole('button')).toHaveLength(3)
    expect(within(card).getByRole('button', { name: 'Refresh skills readiness' })).toBeInTheDocument()
    expect(within(card).getAllByRole('button', { name: 'Install / Update' })).toHaveLength(2)
    for (const forbidden of [/install all/i, /update all/i, /remove/i, /uninstall/i, /open/i, /content/i, /custom path/i, /browse/i]) {
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

  it('previews exactly once, shows only the semantic placeholder, and cancel never executes', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(jsonResponse(mixed))
      .mockResolvedValueOnce(jsonResponse(previewFor('claude')))
    vi.stubGlobal('fetch', fetchMock)
    renderSkills()
    const card = await screen.findByRole('region', { name: 'Skills Readiness' })
    const claudeRow = [...card.querySelectorAll('article')].find(row => row.textContent?.includes('Claude Code')) as HTMLElement
    const opener = within(claudeRow).getByRole('button', { name: 'Install / Update' })
    await userEvent.click(opener)
    const dialog = await screen.findByRole('dialog', { name: 'Install / Update RoboRev Skills' })
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(JSON.parse(fetchMock.mock.calls[1][1].body)).toEqual({ agent: 'claude' })
    expect(dialog).toHaveTextContent('roborev skills install --path <agent-skills-directory> --agent claude')
    expect(dialog).toHaveTextContent('SKILL.md files may be overwritten.')
    expect(dialog).toHaveTextContent('Codex policy files may also be updated')
    expect(dialog).toHaveTextContent('Unrelated skill directories are not targeted.')
    expect(dialog).toHaveTextContent('does not perform legacy cleanup')
    expect(dialog).not.toHaveTextContent('C:\\Users\\')
    expect(dialog).not.toHaveTextContent('C:\\fake-home\\.codex')
    expect(dialog).not.toHaveTextContent('HOME=')
    expect(dialog).not.toHaveTextContent('roborev.exe')
    expect(dialog).not.toHaveTextContent('PRIVATE')
    expect(screen.getByRole('button', { name: 'Cancel' })).toHaveFocus()
    await userEvent.keyboard('{Escape}')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(2)
    await waitFor(() => expect(opener).toHaveFocus())
  })

  it('gives each supported agent its own single CTA and exact preview body', async () => {
    for (const [index, agent] of (['codex', 'claude', 'grok', 'droid'] as const).entries()) {
      if (index > 0) {
        cleanup()
        resetSkillsReadinessForTests()
        vi.unstubAllGlobals()
      }
      const fetchMock = vi.fn()
        .mockResolvedValueOnce(jsonResponse(readinessWithNeeds(agent)))
        .mockResolvedValueOnce(jsonResponse(previewFor(agent)))
      vi.stubGlobal('fetch', fetchMock)
      renderSkills()
      const card = await screen.findByRole('region', { name: 'Skills Readiness' })
      await waitFor(() => expect(within(card).getAllByRole('button', { name: 'Install / Update' })).toHaveLength(1))
      const row = [...card.querySelectorAll('article')].find(item => item.textContent?.includes(agent === 'codex' ? 'Codex' : agent === 'claude' ? 'Claude Code' : agent === 'grok' ? 'Grok Build' : 'Factory Droid')) as HTMLElement
      await userEvent.click(within(row).getByRole('button', { name: 'Install / Update' }))
      await screen.findByRole('dialog')
      expect(fetchMock).toHaveBeenCalledTimes(2)
      expect(JSON.parse(fetchMock.mock.calls[1][1].body)).toEqual({ agent })
      expect(fetchMock.mock.calls.some(call => call[0] === '/api/skills/execute')).toBe(false)
    }
  })

  it('uses the Cancel button to avoid execute and restore the opener focus', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(jsonResponse(mixed))
      .mockResolvedValueOnce(jsonResponse(previewFor('droid')))
    vi.stubGlobal('fetch', fetchMock)
    renderSkills()
    const card = await screen.findByRole('region', { name: 'Skills Readiness' })
    const droidRow = [...card.querySelectorAll('article')].find(row => row.textContent?.includes('Factory Droid')) as HTMLElement
    const opener = within(droidRow).getByRole('button', { name: 'Install / Update' })
    await userEvent.click(opener)
    const dialog = await screen.findByRole('dialog')
    const cancel = within(dialog).getByRole('button', { name: 'Cancel' })
    expect(cancel).toHaveFocus()
    await userEvent.click(cancel)
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(fetchMock.mock.calls.some(call => call[0] === '/api/skills/execute')).toBe(false)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    await waitFor(() => expect(opener).toHaveFocus())
  })

  it('confirms exactly once on double click and forces exactly one readiness reload', async () => {
    let resolveExecute!: (response: Response) => void
    const executeResponse = new Promise<Response>(resolve => { resolveExecute = resolve })
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(jsonResponse(mixed))
      .mockResolvedValueOnce(jsonResponse(previewFor('droid')))
      .mockReturnValueOnce(executeResponse)
      .mockResolvedValueOnce(jsonResponse(allCurrent))
    vi.stubGlobal('fetch', fetchMock)
    renderSkills()
    const card = await screen.findByRole('region', { name: 'Skills Readiness' })
    const droidRow = [...card.querySelectorAll('article')].find(row => row.textContent?.includes('Factory Droid')) as HTMLElement
    await userEvent.click(within(droidRow).getByRole('button', { name: 'Install / Update' }))
    const dialog = await screen.findByRole('dialog')
    const confirm = within(dialog).getByRole('button', { name: 'Install / Update' })
    fireEvent.click(confirm)
    fireEvent.click(confirm)
    expect(fetchMock).toHaveBeenCalledTimes(3)
    expect(JSON.parse(fetchMock.mock.calls[2][1].body)).toEqual({ token: 'token-droid' })
    resolveExecute(jsonResponse(resultFor('droid', 'verified-success')))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(4))
    expect(fetchMock.mock.calls.filter(call => call[0] === '/api/skills/readiness')).toHaveLength(2)
    expect(screen.getByText('Verified success')).toBeInTheDocument()
  })

  it('keeps the confirmation modal locked while execute is pending', async () => {
    let resolveExecute!: (response: Response) => void
    const executeResponse = new Promise<Response>(resolve => { resolveExecute = resolve })
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(jsonResponse(mixed))
      .mockResolvedValueOnce(jsonResponse(previewFor('claude')))
      .mockReturnValueOnce(executeResponse)
      .mockResolvedValueOnce(jsonResponse(allCurrent))
    vi.stubGlobal('fetch', fetchMock)
    renderSkills()
    const card = await screen.findByRole('region', { name: 'Skills Readiness' })
    const claudeRow = [...card.querySelectorAll('article')].find(row => row.textContent?.includes('Claude Code')) as HTMLElement
    await userEvent.click(within(claudeRow).getByRole('button', { name: 'Install / Update' }))
    const dialog = await screen.findByRole('dialog')
    const confirm = within(dialog).getByRole('button', { name: 'Install / Update' })
    const close = within(dialog).getByRole('button', { name: 'Close Skills confirmation' })
    const cancel = within(dialog).getByRole('button', { name: 'Cancel' })
    fireEvent.click(confirm)
    expect(fetchMock).toHaveBeenCalledTimes(3)
    expect(confirm).toBeDisabled()
    expect(close).toBeDisabled()
    expect(cancel).toBeDisabled()
    fireEvent.keyDown(dialog, { key: 'Escape' })
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    fireEvent.click(confirm)
    expect(fetchMock).toHaveBeenCalledTimes(3)
    resolveExecute(jsonResponse(resultFor('claude')))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(4))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('renders partial and failed-unverified outcomes honestly, including CLI error verification', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(jsonResponse(mixed))
      .mockResolvedValueOnce(jsonResponse(previewFor('claude')))
      .mockResolvedValueOnce(jsonResponse(resultFor('claude', 'partial', 'cli-failed')))
      .mockResolvedValueOnce(jsonResponse(mixed))
    vi.stubGlobal('fetch', fetchMock)
    renderSkills()
    const card = await screen.findByRole('region', { name: 'Skills Readiness' })
    const claudeRow = [...card.querySelectorAll('article')].find(row => row.textContent?.includes('Claude Code')) as HTMLElement
    await userEvent.click(within(claudeRow).getByRole('button', { name: 'Install / Update' }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Install / Update' }))
    await waitFor(() => expect(screen.getByText('Partial mutation')).toBeInTheDocument())
    expect(screen.getByText(/CLI reported an error; a fresh readback found measurable progress/)).toBeInTheDocument()

    cleanup()
    resetSkillsReadinessForTests()
    const failedFetch = vi.fn()
      .mockResolvedValueOnce(jsonResponse(mixed))
      .mockResolvedValueOnce(jsonResponse(previewFor('claude')))
      .mockResolvedValueOnce(jsonResponse(resultFor('claude', 'failed-unverified', 'no-improvement')))
      .mockResolvedValueOnce(jsonResponse(mixed))
    vi.stubGlobal('fetch', failedFetch)
    renderSkills()
    const failedCard = await screen.findByRole('region', { name: 'Skills Readiness' })
    const failedRow = [...failedCard.querySelectorAll('article')].find(row => row.textContent?.includes('Claude Code')) as HTMLElement
    await userEvent.click(within(failedRow).getByRole('button', { name: 'Install / Update' }))
    const failedDialog = await screen.findByRole('dialog')
    await userEvent.click(within(failedDialog).getByRole('button', { name: 'Install / Update' }))
    await waitFor(() => expect(screen.getByText('Failed · unverified')).toBeInTheDocument())
    expect(screen.getByText('The post-mutation readback found no measurable improvement.')).toBeInTheDocument()
  })

  it('reloads once after an execute request failure and ignores late preview after unmount', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(jsonResponse(mixed))
      .mockResolvedValueOnce(jsonResponse(previewFor('claude')))
      .mockRejectedValueOnce(new Error('network down'))
      .mockResolvedValueOnce(jsonResponse(mixed))
    vi.stubGlobal('fetch', fetchMock)
    renderSkills()
    const card = await screen.findByRole('region', { name: 'Skills Readiness' })
    const claudeRow = [...card.querySelectorAll('article')].find(row => row.textContent?.includes('Claude Code')) as HTMLElement
    await userEvent.click(within(claudeRow).getByRole('button', { name: 'Install / Update' }))
    const dialog = await screen.findByRole('dialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Install / Update' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(4))
    expect(screen.getByText('The Skills Management action could not be completed or verified.')).toBeInTheDocument()

    cleanup()
    resetSkillsReadinessForTests()
    let resolvePreview!: (response: Response) => void
    const latePreview = new Promise<Response>(resolve => { resolvePreview = resolve })
    const lateFetch = vi.fn()
      .mockResolvedValueOnce(jsonResponse(mixed))
      .mockReturnValueOnce(latePreview)
    vi.stubGlobal('fetch', lateFetch)
    const view = renderSkills()
    const lateCard = await screen.findByRole('region', { name: 'Skills Readiness' })
    const lateRow = [...lateCard.querySelectorAll('article')].find(row => row.textContent?.includes('Claude Code')) as HTMLElement
    await userEvent.click(within(lateRow).getByRole('button', { name: 'Install / Update' }))
    await waitFor(() => expect(lateFetch).toHaveBeenCalledTimes(2))
    view.unmount()
    resolvePreview(jsonResponse(previewFor('claude')))
    await Promise.resolve()
    expect(document.body).not.toHaveTextContent('Preparing the Claude Code skills preview')
    expect(document.body).not.toHaveTextContent('Install / Update RoboRev Skills')
  })

  it('does not reload or update state when an execute response arrives after unmount', async () => {
    let resolveExecute!: (response: Response) => void
    const executeResponse = new Promise<Response>(resolve => { resolveExecute = resolve })
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(jsonResponse(mixed))
      .mockResolvedValueOnce(jsonResponse(previewFor('droid')))
      .mockReturnValueOnce(executeResponse)
    vi.stubGlobal('fetch', fetchMock)
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {})
    const view = renderSkills()
    const card = await screen.findByRole('region', { name: 'Skills Readiness' })
    const droidRow = [...card.querySelectorAll('article')].find(row => row.textContent?.includes('Factory Droid')) as HTMLElement
    await userEvent.click(within(droidRow).getByRole('button', { name: 'Install / Update' }))
    const dialog = await screen.findByRole('dialog')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Install / Update' }))
    expect(fetchMock).toHaveBeenCalledTimes(3)
    view.unmount()
    resolveExecute(jsonResponse(resultFor('droid')))
    await Promise.resolve()
    await Promise.resolve()
    expect(fetchMock).toHaveBeenCalledTimes(3)
    expect(fetchMock.mock.calls.some(call => call[0] === '/api/skills/readiness' && fetchMock.mock.calls.indexOf(call) > 0)).toBe(false)
    expect(consoleError).not.toHaveBeenCalled()
    consoleError.mockRestore()
  })
})

function ReactStrictSkills() {
  // Kept as a component helper so the StrictMode import remains local to this
  // test file's render expression without changing production code.
  return <StrictMode><I18nProvider><SkillsReadiness /></I18nProvider></StrictMode>
}
