import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { normalizeSetupState, type AgentsResponse, type RuntimeControlState, type SetupState } from './api'
import { Setup, SetupBanner } from './Setup'
import { App } from './App'
import { I18nProvider, LANGUAGE_STORAGE_KEY } from './i18n'

const baseState: SetupState = {
  status: 'idle', phase: 'idle', operationActive: false, installed: false, recommendedVersion: 'v0.64.0', compatible: true, panelRefineCompatible: true, daemonRunning: false, blocked: false, retryable: false, installAvailable: true, canInstall: true, canRetry: false, canStartDaemon: false,
  architecture: 'windows-amd64', sourceURL: 'https://github.com/kenn-io/roborev/releases/tag/v0.64.0', sourceLabel: 'Official GitHub release', assetName: 'roborev-v0.64.0-windows-amd64.zip', sha256: '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef', targetPath: 'C:\\Tools\\roborev\\roborev.exe', downloadedBytes: 0, totalBytes: 1200,
}
const appBootstrap = {
  csrfToken: 'csrf', selectedRepository: 'E:\\repo', selectedRepositoryIdentity: 'repo-1', repositories: [{ identity: 'repo-1', name: 'repo', path: 'E:\\repo', branch: 'main', head: 'abcdef123456', headShort: 'abcdef12', commitSubject: 'Initial', dirty: false, hasConfig: true }],
  roborev: { installed: false, version: '', status: { running: false }, capabilities: { reviewEnqueueApi: false, frozenRerunApi: false, jobListApi: false, jobOutputStatusApi: false, panelReview: false, fixCommand: false, panelMembersApi: false, promptLazyLoad: false, rawLog: false } },
}
const readyBootstrap = {
  ...appBootstrap,
  roborev: { installed: true, version: 'v0.64.0', status: { running: true }, capabilities: { reviewEnqueueApi: true, frozenRerunApi: true, jobListApi: true, jobOutputStatusApi: true, panelReview: true, fixCommand: true, panelMembersApi: true, promptLazyLoad: true, rawLog: true } },
}
function jsonResponse(value: unknown, status = 200) { return Promise.resolve(new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } })) }

afterEach(() => { cleanup(); vi.restoreAllMocks(); window.localStorage.clear() })

describe('RoboRev setup', () => {
  const readySetup = { ...baseState, status: 'ready' as const, phase: 'ready' as const, operationActive: false, installed: true, canInstall: false, installAvailable: false, canStartDaemon: false, daemonRunning: true, compatible: true, panelRefineCompatible: true }
  const runningRuntime: RuntimeControlState = { daemonRunning: true, operationActive: false, queueState: 'processing', canPause: true, canResume: false, canStart: false, canStop: true, canRestart: true, daemonCommands: { start: { executable: 'C:\\Tools\\roborev.exe', args: ['daemon', 'start'] }, stop: { executable: 'C:\\Tools\\roborev.exe', args: ['daemon', 'stop'] }, restart: { executable: 'C:\\Tools\\roborev.exe', args: ['daemon', 'restart'] } } }
  const configuredAgents: AgentsResponse = { roborevVersion: 'v0.64.0', repositoryIdentity: 'repo-1', configured: [{ name: 'Claude Code', agent: 'claude-code', model: 'sonnet', command: 'claude', commandSource: 'config', detected: true, detectionReason: 'Found in PATH', resolvedPath: 'C:\\Tools\\claude.exe', health: 'not_checked' }] }

  it('wires setup theme surfaces and controls to their selectors in rendered DOM', async () => {
    const activeState = { ...readySetup, operationActive: true, phase: 'checking' as const, status: 'checking' as const, downloadedBytes: 512, totalBytes: 1024 }
    const { rerender } = render(<Setup state={activeState} agents={configuredAgents} runtimeControl={runningRuntime} onDaemonAction={vi.fn().mockResolvedValue(undefined)} />)
    expect(document.querySelector('.setup-runtime')).toBeInTheDocument()
    expect(document.querySelector('.configured-agents')).toBeInTheDocument()
    expect(document.querySelector('.setup-facts')).toBeInTheDocument()
    expect(document.querySelector('.setup-safety')).toBeInTheDocument()
    expect(document.querySelector('.setup-progress-track')).toBeInTheDocument()

    rerender(<Setup state={readySetup} agents={configuredAgents} runtimeControl={runningRuntime} onDaemonAction={vi.fn().mockResolvedValue(undefined)} />)
    await userEvent.click(screen.getByRole('button', { name: 'Stop daemon' }))
    expect(document.querySelector('.runtime-command-preview')).toBeInTheDocument()
  })

  it('does not health-check on render and checks only the confirmed canonical agent', async () => {
    const onCheck = vi.fn().mockResolvedValue(undefined)
    render(<Setup state={readySetup} agents={configuredAgents} onCheckAgent={onCheck} />)
    expect(onCheck).not.toHaveBeenCalled()
    await userEvent.click(screen.getByRole('button', { name: 'Run check' }))
    expect(screen.getByRole('dialog')).toHaveTextContent('small real request through RoboRev')
    expect(screen.getByRole('dialog')).toHaveTextContent('subscription/API quota')
    expect(onCheck).not.toHaveBeenCalled()
    await userEvent.click(screen.getByRole('dialog').querySelector('button.button.primary')!)
    await waitFor(() => expect(onCheck).toHaveBeenCalledWith('claude-code'))
  })

  it('disables configured-agent refresh while a health check is busy', () => {
    render(<Setup state={readySetup} agents={configuredAgents} busy onRefreshAgents={vi.fn().mockResolvedValue(undefined)} onCheckAgent={vi.fn().mockResolvedValue(undefined)} />)
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeDisabled()
  })

  it('keeps paused queue semantics and disables queue controls when offline or unknown', async () => {
    const onQueue = vi.fn().mockResolvedValue(undefined)
    const paused: RuntimeControlState = { ...runningRuntime, queueState: 'paused', canPause: false, canResume: true }
    const { rerender } = render(<Setup state={readySetup} runtimeControl={paused} onQueueAction={onQueue} />)
    expect(screen.getByText('Running jobs continue; new queued jobs will not start.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Pause queue' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Resume queue' })).toBeEnabled()
    rerender(<Setup state={{ ...readySetup, daemonRunning: false }} runtimeControl={{ ...paused, daemonRunning: false, queueState: 'unknown', canResume: false }} onQueueAction={onQueue} onRefreshRuntime={vi.fn().mockResolvedValue(undefined)} />)
    expect(screen.getByText('Queue state is unavailable while the daemon is offline or unknown.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Pause queue' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Resume queue' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeEnabled()
  })

  it.each([
    ['idle', 'Ready to accept typed review operations.'],
    ['start', 'Start daemon'],
    ['stop', 'Stop daemon'],
    ['restart', 'Restart daemon'],
    ['pause', 'Pause queue'],
    ['resume', 'Resume queue'],
    ['unknown-operation', 'unknown-operation'],
  ])('renders the runtime manager operation %s with a safe fallback', (_operation, expected) => {
    render(<I18nProvider><Setup state={readySetup} runtimeControl={{ ...runningRuntime, daemonOperation: _operation }} /></I18nProvider>)
    expect(document.querySelector('.runtime-control-card p')).toHaveTextContent(expected)
  })

  it.each([
    ['idle', 'typed 리뷰 작업을 받을 준비가 되었습니다.'],
    ['pause', '대기열 일시중지'],
    ['resume', '대기열 재개'],
  ])('renders the %s runtime operation in Korean', (operation, expected) => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, 'ko')
    render(<I18nProvider><Setup state={readySetup} runtimeControl={{ ...runningRuntime, daemonOperation: operation }} /></I18nProvider>)
    expect(document.querySelector('.runtime-control-card p')).toHaveTextContent(expected)
  })

  it('fails closed when a daemon command preview is missing or mismatched', async () => {
    const onDaemonAction = vi.fn().mockResolvedValue(undefined)
    const invalid = { ...runningRuntime, daemonCommands: { ...runningRuntime.daemonCommands!, stop: { executable: 'C:\\Tools\\roborev.exe', args: ['daemon', 'restart'] } } }
    render(<Setup state={readySetup} runtimeControl={invalid} onDaemonAction={onDaemonAction} />)
    expect(screen.getByRole('button', { name: 'Stop daemon' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Restart daemon' })).toBeEnabled()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('requires restrained stop/restart confirmation, gates active operations, and ignores double confirmation', async () => {
    const deferredAction = (() => { let resolve!: () => void; const promise = new Promise<void>(done => { resolve = done }); return { promise, resolve } })()
    const onDaemonAction = vi.fn().mockReturnValue(deferredAction.promise)
    const { rerender } = render(<Setup state={readySetup} runtimeControl={runningRuntime} onDaemonAction={onDaemonAction} />)
    await userEvent.click(screen.getByRole('button', { name: 'Stop daemon' }))
    expect(screen.getByRole('dialog')).toHaveTextContent('Active jobs may be interrupted; queued jobs remain queued.')
    expect(screen.getByRole('dialog')).toHaveTextContent(JSON.stringify(['C:\\Tools\\roborev.exe', 'daemon', 'stop']))
    expect(onDaemonAction).not.toHaveBeenCalled()
    const confirmStop = screen.getByRole('dialog').querySelector('button.button.danger')!
    fireEvent.click(confirmStop)
    fireEvent.click(confirmStop)
    expect(onDaemonAction).toHaveBeenCalledTimes(1)
    expect(onDaemonAction).toHaveBeenCalledWith('stop')
    deferredAction.resolve()
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    await userEvent.click(screen.getByRole('button', { name: 'Restart daemon' }))
    expect(screen.getByRole('dialog')).toHaveTextContent('This stops and starts the daemon.')
    await userEvent.click(screen.getByRole('dialog').querySelector('button.button.danger')!)
    await waitFor(() => expect(onDaemonAction).toHaveBeenCalledWith('restart'))
    const active = { ...runningRuntime, operationActive: true, disabledReason: 'Another daemon operation is active.' }
    rerender(<Setup state={readySetup} runtimeControl={active} onDaemonAction={onDaemonAction} panelRefineActive />)
    expect(screen.getByRole('button', { name: 'Start daemon' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Stop daemon' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Restart daemon' })).toBeDisabled()
    expect(screen.getByText(/Controls disabled: Panel Refine is active/)).toBeInTheDocument()
  })

  it('shows a missing banner but does not install on render, then confirms exact values', async () => {
    const install = vi.fn().mockResolvedValue(undefined)
    render(<Setup state={baseState} onInstall={install} />)
    expect(screen.getByText('RoboRev is not installed')).toBeInTheDocument()
    expect(install).not.toHaveBeenCalled()
    await userEvent.click(screen.getByRole('button', { name: 'Install RoboRev' }))
    expect(screen.getByRole('dialog')).toHaveTextContent('v0.64.0')
    expect(screen.getByRole('dialog')).toHaveTextContent(baseState.sha256!)
    expect(screen.getByRole('dialog')).toHaveTextContent('Official GitHub release')
    expect(screen.getByRole('dialog')).toHaveTextContent('Will not do')
    await userEvent.click(screen.getByRole('button', { name: 'Install & Start' }))
    await waitFor(() => expect(install).toHaveBeenCalledTimes(1))
  })

  it('renders active phase and bounded byte progress', () => {
    const state = { ...baseState, status: 'downloading' as const, phase: 'downloading' as const, operationActive: true, downloadedBytes: 512, totalBytes: 1024 }
    render(<Setup state={state} />)
    expect(screen.getByText('Downloading')).toBeInTheDocument()
    expect(screen.getAllByText(/50%/).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/serialized/).length).toBeGreaterThan(0)
  })

  it('exposes only verification retry for blocked state', async () => {
    const retry = vi.fn().mockResolvedValue(undefined)
    const state = { ...baseState, installed: true, installAvailable: false, canInstall: false, phase: 'blocked' as const, status: 'blocked' as const, blocked: true, canRetry: true }
    render(<SetupBanner state={state} onRetry={retry} onInstall={vi.fn()} onStartDaemon={vi.fn()} />)
    expect(screen.getByText(/Application Control/)).toBeInTheDocument()
    expect(screen.getByText(/Windows Security/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry verification' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Install RoboRev' })).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Retry verification' }))
    expect(retry).toHaveBeenCalledTimes(1)
  })

  it('fails closed on checksum failure even when the install is missing', () => {
    const state = { ...baseState, phase: 'failed' as const, status: 'failed' as const, failureCode: 'checksum_mismatch', canRetry: false }
    render(<SetupBanner state={state} onInstall={vi.fn()} onRetry={vi.fn()} />)
    expect(screen.getByText('RoboRev installation was rejected')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Install RoboRev' })).not.toBeInTheDocument()
  })

  it('does not offer retry for unsafe archive or redirect failures', () => {
    const state = { ...baseState, phase: 'failed' as const, status: 'failed' as const, failureCode: 'unsafe_redirect', canRetry: true }
    render(<SetupBanner state={state} onRetry={vi.fn()} />)
    expect(screen.queryByRole('button', { name: 'Retry installation' })).not.toBeInTheDocument()
  })

  it('warns for an existing newer or unsupported version without downgrade controls', () => {
    const state = { ...baseState, phase: 'unsupported' as const, status: 'unsupported' as const, installed: true, version: 'v0.65.0', compatible: false, panelRefineCompatible: false, canInstall: false, installAvailable: false }
    render(<SetupBanner state={state} onInstall={vi.fn()} />)
    expect(screen.getByText(/will not downgrade/i)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Install/ })).not.toBeInTheDocument()
  })

  it('fails safe on malformed setup state', () => {
    expect(normalizeSetupState({ status: 'not-a-phase' })).toBeNull()
    expect(normalizeSetupState({ phase: 'ready', operationActive: 'yes' })).toBeNull()
    expect(normalizeSetupState({ ...baseState, phase: 'idle', status: 'ready' })).toBeNull()
    expect(normalizeSetupState({ ...baseState, phase: 'idle', status: 'idle' })).toMatchObject({ recommendedVersion: 'v0.64.0', totalBytes: 1200 })
    expect(normalizeSetupState({ ...baseState, phase: 'canceled', status: 'canceled', canInstall: false })).toMatchObject({ phase: 'canceled', operationActive: false })
    expect(normalizeSetupState({ ...baseState, phase: 'blocked', status: 'blocked', installed: true, blocked: true, compatible: false, panelRefineCompatible: false, canInstall: false, installAvailable: false, canRetry: true, binaryPath: baseState.targetPath })).toMatchObject({ phase: 'blocked', installed: true, canRetry: true })
    expect(normalizeSetupState({ ...baseState, sourceURL: 'https://example.invalid/roborev.zip' })).toBeNull()
  })

  it('keeps the missing banner confirmation explicit and sends an exactly empty install POST', async () => {
    const calls: Array<{ path: string; init?: RequestInit }> = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      calls.push({ path, init })
      if (path === '/api/bootstrap') return jsonResponse(appBootstrap)
      if (path === '/api/setup/roborev') return jsonResponse(baseState)
      if (path.startsWith('/api/repos/panels?')) return jsonResponse({ repositoryIdentity: 'repo-1', panels: [{ name: 'reviewers', kind: 'panel', members: ['codex'], origin: 'global', valid: true }], selectedPanel: 'reviewers', selectionRequired: false })
      if (path.startsWith('/api/panel-refines?')) return jsonResponse({ runs: [], options: null })
      if (path.startsWith('/api/reviews?')) return jsonResponse([])
      if (path === '/api/setup/roborev/install') return jsonResponse({ ...baseState, phase: 'checking', status: 'checking', operationActive: true }, 202)
      return jsonResponse({ success: true })
    }))
    render(<App />)
    const install = await screen.findByRole('button', { name: 'Install RoboRev' })
    expect(calls.filter(call => call.path === '/api/setup/roborev/install')).toHaveLength(0)
    await userEvent.click(install)
    expect(screen.getByRole('dialog')).toHaveTextContent('Asset bytes')
    expect(calls.filter(call => call.path === '/api/setup/roborev/install')).toHaveLength(0)
    await userEvent.click(screen.getByRole('button', { name: 'Install & Start' }))
    await waitFor(() => expect(calls.filter(call => call.path === '/api/setup/roborev/install')).toHaveLength(1))
    const installCall = calls.find(call => call.path === '/api/setup/roborev/install')!
    expect(installCall.init?.method).toBe('POST')
    expect(installCall.init?.body).toBeUndefined()
  })

  it('observes terminal ready after the install action and refreshes runtime, reviews, and panel state', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const activeState = { ...baseState, phase: 'checking' as const, status: 'checking' as const, operationActive: true }
    const readyState = { ...baseState, phase: 'ready' as const, status: 'ready' as const, operationActive: false, installed: true, daemonRunning: true, compatible: true, panelRefineCompatible: true, canInstall: false, installAvailable: false, canStartDaemon: false }
    let setupGets = 0
    let bootstrapGets = 0
    let panelGets = 0
    let reviewGets = 0
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/bootstrap') { bootstrapGets++; return jsonResponse(bootstrapGets === 1 ? appBootstrap : readyBootstrap) }
      if (path === '/api/setup/roborev') { setupGets++; return jsonResponse(setupGets === 1 ? baseState : setupGets === 2 ? activeState : readyState) }
      if (path.startsWith('/api/repos/panels?')) { panelGets++; return jsonResponse({ repositoryIdentity: 'repo-1', panels: [{ name: 'reviewers', kind: 'panel', members: ['codex'], origin: 'global', valid: true }], selectedPanel: 'reviewers', selectionRequired: false }) }
      if (path.startsWith('/api/panel-refines?')) return jsonResponse({ runs: [], options: null })
      if (path.startsWith('/api/reviews?')) { reviewGets++; return jsonResponse([]) }
      if (path === '/api/runtime') return jsonResponse(readyBootstrap.roborev)
      if (path === '/api/setup/roborev/install') return jsonResponse(activeState, 202)
      return jsonResponse({ success: true })
    }))
    render(<App />)
    await userEvent.click(await screen.findByRole('button', { name: 'Install RoboRev' }))
    await userEvent.click(screen.getByRole('button', { name: 'Install & Start' }))
    await waitFor(() => {
      expect(setupGets).toBeGreaterThanOrEqual(3)
      expect(bootstrapGets).toBeGreaterThanOrEqual(3)
      expect(panelGets).toBeGreaterThanOrEqual(2)
      expect(reviewGets).toBeGreaterThanOrEqual(2)
      expect(screen.getByRole('combobox', { name: 'Current repository' })).toHaveValue('repo-1')
      expect(screen.getByRole('combobox', { name: 'Review panel' })).toHaveValue('reviewers')
    })
    vi.useRealTimers()
  })

  it('offers daemon retry for an installed failure without exposing install', async () => {
    const failedState = { ...baseState, phase: 'failed' as const, status: 'failed' as const, installed: true, daemonRunning: false, compatible: true, panelRefineCompatible: true, canInstall: false, installAvailable: false, canRetry: false, canStartDaemon: true, failureCode: 'daemon_start_failed' }
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      calls.push(`${init?.method ?? 'GET'} ${path}`)
      if (path === '/api/bootstrap') return jsonResponse(readyBootstrap)
      if (path === '/api/setup/roborev') return jsonResponse(failedState)
      if (path.startsWith('/api/repos/panels?')) return jsonResponse({ repositoryIdentity: 'repo-1', panels: [{ name: 'reviewers', kind: 'panel', members: ['codex'], origin: 'global', valid: true }], selectedPanel: 'reviewers', selectionRequired: false })
      if (path.startsWith('/api/panel-refines?')) return jsonResponse({ runs: [], options: null })
      if (path.startsWith('/api/reviews?')) return jsonResponse([])
      return jsonResponse({ success: true })
    }))
    render(<App />)
    await userEvent.click(await screen.findByRole('button', { name: 'Retry Start' }))
		await waitFor(() => expect(calls).toContain('POST /api/setup/roborev/retry'))
		expect(calls.some(call => call.includes('POST /api/daemon/start'))).toBe(false)
    expect(calls.some(call => call.includes('/api/setup/roborev/install'))).toBe(false)
  })
})
