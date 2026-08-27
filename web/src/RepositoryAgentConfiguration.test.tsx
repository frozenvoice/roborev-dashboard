import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { RepositoryAgentConfiguration } from './RepositoryAgentConfiguration'
import { I18nProvider, LanguageSelector } from './i18n'
import { setCSRF } from './api'

const alias = 'A'.repeat(43)
const status = { version: 'v0.64.0', repositoryIdentity: alias, global: { status: 'unset' }, repository: { status: 'custom' } }
const preview = { version: 'v0.64.0', repositoryIdentity: alias, scope: 'repository', agent: 'claude-code', currentStatus: 'custom', action: 'set-agent', target: 'Repository agent', summary: 'Set the selected repository RoboRev agent.', token: 'token-1', expiresAt: '2099-01-01T00:00:00.000Z', command: { executable: 'roborev', args: ['config', 'set', 'agent', 'claude-code', '--local'] } }

function response(value: unknown) { return new Response(JSON.stringify(value), { status: 200, headers: { 'Content-Type': 'application/json' } }) }

afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); window.localStorage.clear() })

describe('RepositoryAgentConfiguration', () => {
  it('does not request on mount and checks only after the explicit button', async () => {
    setCSRF('csrf')
    const fetchMock = vi.fn().mockResolvedValue(response(status))
    vi.stubGlobal('fetch', fetchMock)
    render(<I18nProvider><RepositoryAgentConfiguration repositoryIdentity="legacy-selected" /></I18nProvider>)
    expect(fetchMock).not.toHaveBeenCalled()
    await userEvent.click(screen.getByRole('button', { name: 'Check Agent Selection' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    expect(JSON.parse(String((fetchMock.mock.calls[0] as [string, RequestInit])[1].body))).toEqual({ repositoryIdentity: 'legacy-selected' })
    expect(screen.getByText('Custom value')).toBeInTheDocument()
    expect(screen.getByText('Not configured')).toBeInTheDocument()
  })

  it('uses the server alias for preview, requires confirmation, and executes once with token only', async () => {
    setCSRF('csrf')
    const fetchMock = vi.fn().mockImplementation((path: string) => Promise.resolve(response(path.endsWith('/status') ? status : path.endsWith('/preview') ? preview : { status: 'verified-success' })))
    vi.stubGlobal('fetch', fetchMock)
    render(<I18nProvider><RepositoryAgentConfiguration repositoryIdentity="legacy-selected" /></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Check Agent Selection' }))
    await screen.findByText('Custom value')
    const repositorySelect = screen.getByRole('combobox', { name: 'Repository agent Agent' })
    await userEvent.selectOptions(repositorySelect, 'claude-code')
    await userEvent.click(screen.getAllByRole('button', { name: 'Preview change' })[1])
    await screen.findByRole('dialog')
    expect(JSON.parse(String((fetchMock.mock.calls[1] as [string, RequestInit])[1].body))).toEqual({ repositoryIdentity: alias, scope: 'repository', agent: 'claude-code' })
    const confirm = screen.getByRole('button', { name: 'Confirm and set agent' })
    expect(confirm).toBeDisabled()
    await userEvent.click(screen.getByRole('checkbox'))
    await userEvent.click(confirm)
    await userEvent.click(confirm).catch(() => undefined)
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3))
    expect(JSON.parse(String((fetchMock.mock.calls[2] as [string, RequestInit])[1].body))).toEqual({ token: 'token-1' })
    expect(screen.getByText('Agent configuration was verified successfully.')).toBeInTheDocument()
  })

  it('does not execute a no-op preview and keeps state through locale changes', async () => {
    setCSRF('csrf')
    const noop = { version: 'v0.64.0', repositoryIdentity: alias, scope: 'global', agent: 'codex', currentStatus: 'canonical', currentAgent: 'codex', action: 'already-configured', target: 'Global default agent', summary: 'Set the global default RoboRev agent.' }
    const fetchMock = vi.fn().mockImplementation((path: string) => Promise.resolve(response(path.endsWith('/status') ? { ...status, global: { status: 'canonical', agent: 'codex' } } : noop)))
    vi.stubGlobal('fetch', fetchMock)
    render(<I18nProvider><RepositoryAgentConfiguration repositoryIdentity="legacy-selected" /><LanguageSelector /></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Check Agent Selection' }))
    await screen.findByText('Configured · Codex')
    await userEvent.click(screen.getAllByRole('button', { name: 'Preview change' })[0])
    expect(await screen.findByText('Already configured.')).toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(2)
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Select language' }), 'ko')
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('cancels without executing', async () => {
    setCSRF('csrf')
    const fetchMock = vi.fn().mockImplementation((path: string) => Promise.resolve(response(path.endsWith('/status') ? status : preview)))
    vi.stubGlobal('fetch', fetchMock)
    render(<I18nProvider><RepositoryAgentConfiguration repositoryIdentity="legacy-selected" /></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Check Agent Selection' }))
    await screen.findByText('Custom value')
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Repository agent Agent' }), 'claude-code')
    await userEvent.click(screen.getAllByRole('button', { name: 'Preview change' })[1])
    await screen.findByRole('dialog')
    expect(screen.getByRole('dialog')).toHaveTextContent('["roborev","config","set","agent","claude-code","--local"]')
    expect(screen.getByRole('dialog')).toHaveTextContent('The existing scoped agent setting will be replaced.')
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })
})
