import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { App, ReviewDetail } from './App'
import { InstallConfirmation, Setup, SetupBanner, AgentCheckConfirmation } from './Setup'
import { PanelRefine } from './PanelRefine'
import { I18nProvider, LANGUAGE_STORAGE_KEY, LanguageSelector } from './i18n'
import type { SetupState } from './api'

const setupState: SetupState = {
  status: 'idle', phase: 'idle', operationActive: false, installed: false, recommendedVersion: 'v0.64.0', compatible: true, panelRefineCompatible: true,
  daemonRunning: false, blocked: false, retryable: false, installAvailable: true, canInstall: true, canRetry: false, canStartDaemon: false,
  architecture: 'windows-amd64', sourceURL: 'https://github.com/kenn-io/roborev/releases/tag/v0.64.0', sourceLabel: 'Official GitHub release',
  assetName: 'roborev-v0.64.0-windows-amd64.zip', sha256: '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef', targetPath: 'C:\\Tools\\roborev.exe', downloadedBytes: 0, totalBytes: 1200,
}

const runtime = {
  daemonRunning: true, operationActive: false, queueState: 'processing' as const, canPause: true, canResume: false, canStart: false, canStop: true, canRestart: true,
  daemonCommands: {
    start: { executable: 'C:\\Tools\\roborev.exe', args: ['daemon', 'start'] }, stop: { executable: 'C:\\Tools\\roborev.exe', args: ['daemon', 'stop'] }, restart: { executable: 'C:\\Tools\\roborev.exe', args: ['daemon', 'restart'] },
  },
}

function withProvider(node: ReactNode) {
  return <I18nProvider>{node}</I18nProvider>
}

afterEach(() => { cleanup(); vi.restoreAllMocks(); window.localStorage.clear() })

describe('localized confirmation surfaces', () => {
  it('keeps the collapsed-sidebar language control a keyboard-usable native select', () => {
    render(withProvider(<aside className="sidebar"><div className="sidebar-foot"><LanguageSelector /></div></aside>))
    const selector = screen.getByRole('combobox', { name: 'Select language' })
    expect(selector.tagName).toBe('SELECT')
    expect(selector).toBeEnabled()
    expect(selector.parentElement).toHaveClass('language-selector')
  })

  it('uses the localized official source label for validated GitHub sources', async () => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, 'ko')
    const user = userEvent.setup()
    render(withProvider(<InstallConfirmation state={{ ...setupState, sourceLabel: 'Official GitHub release' }} onCancel={vi.fn()} onInstall={vi.fn()} />))
    expect(screen.getByRole('dialog')).toHaveTextContent('공식 GitHub 릴리스')
    expect(screen.getByRole('dialog')).not.toHaveTextContent('Official GitHub release')
    await user.selectOptions(screen.getByLabelText('언어 선택'), 'en')
    expect(screen.getByRole('dialog')).toHaveTextContent('Official GitHub release')
    cleanup()
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, 'ko')
    render(withProvider(<Setup state={setupState} />))
    expect(screen.getAllByText('공식 GitHub 릴리스').length).toBeGreaterThan(0)
    expect(screen.queryByText('Official GitHub release')).not.toBeInTheDocument()
  })

  it('keeps the pre-install Korean banner and setup progress copy available', () => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, 'ko')
    const active = { ...setupState, phase: 'downloading' as const, status: 'downloading' as const, operationActive: true, downloadedBytes: 1024, totalBytes: 2048 }
    const { rerender } = render(withProvider(<SetupBanner state={setupState} onInstall={vi.fn()} />))
    expect(screen.getByRole('status')).toHaveTextContent('RoboRev가 설치되어 있지 않습니다')
    rerender(withProvider(<SetupBanner state={active} onInstall={vi.fn()} />))
    expect(screen.getByRole('status')).toHaveTextContent('50%')
    expect(screen.getByRole('status')).toHaveTextContent('1.0 KiB')
  })

  it('localizes daemon health attention and retry copy', async () => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, 'ko')
    const state: SetupState = { ...setupState, phase: 'failed', status: 'failed', installed: true, daemonRunning: true, canRetry: true, canInstall: false, installAvailable: false, failureCode: 'daemon_version_mismatch' }
    render(withProvider(<><LanguageSelector /><SetupBanner state={state} onRetry={vi.fn()} /></>))
    expect(screen.getByText('RoboRev 데몬 확인 필요')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '데몬 상태 다시 확인' })).toBeInTheDocument()
    await userEvent.selectOptions(screen.getByLabelText('언어 선택'), 'en')
    expect(screen.getByText('RoboRev daemon needs attention')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Retry daemon health' })).toBeInTheDocument()
  })

  it('formats valid agent check times by locale and preserves invalid raw timestamps', () => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, 'en')
    const agents = { repositoryIdentity: 'repo-1', configured: [
      { name: 'Claude Code', agent: 'claude-code', command: 'claude', detected: true, health: 'passed' as const, lastCheckedAt: '2026-08-19T01:02:03Z' },
      { name: 'Other', agent: 'other', command: 'other', detected: true, health: 'passed' as const, lastCheckedAt: 'not-a-date' },
    ] }
    render(withProvider(<Setup state={setupState} agents={agents} />))
    expect(screen.getByText(/Aug/)).toBeInTheDocument()
    expect(screen.getByText(/Last checked not-a-date/)).toBeInTheDocument()
  })

  it('switches an open install confirmation from Korean to English without resetting technical facts', async () => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, 'ko')
    const onInstall = vi.fn()
    const user = userEvent.setup()
    render(withProvider(<InstallConfirmation state={setupState} onCancel={vi.fn()} onInstall={onInstall} />))
    const dialog = screen.getByRole('dialog')
    expect(dialog).toHaveTextContent('RoboRev를 설치하고 시작할까요?')
    expect(dialog).toHaveTextContent(setupState.sha256!)
    await user.selectOptions(screen.getByLabelText('언어 선택'), 'en')
    expect(screen.getByRole('dialog')).toHaveTextContent('Install & Start RoboRev?')
    expect(screen.getByRole('dialog')).toHaveTextContent(setupState.sha256!)
    expect(onInstall).not.toHaveBeenCalled()
  })

  it('switches agent and daemon confirmations in place while preserving callbacks and previews', async () => {
    const user = userEvent.setup()
    const agent = { name: 'Claude Code', agent: 'claude-code', model: 'sonnet', command: 'claude', commandSource: 'config', detected: true, detectionReason: 'Found in PATH', health: 'not_checked' as const }
    const onCheck = vi.fn()
    const { rerender } = render(withProvider(<AgentCheckConfirmation agent={agent} onCancel={vi.fn()} onConfirm={onCheck} />))
    await user.selectOptions(screen.getByLabelText('Select language'), 'ko')
    expect(screen.getByRole('dialog')).toHaveTextContent('에이전트 상태를 확인할까요?')
    expect(screen.getByRole('dialog')).toHaveTextContent('claude-code')
    expect(onCheck).not.toHaveBeenCalled()

    const onDaemon = vi.fn()
    rerender(withProvider(<Setup state={{ ...setupState, installed: true, daemonRunning: true, status: 'ready', phase: 'ready', installAvailable: false, canInstall: false }} runtimeControl={runtime} onDaemonAction={onDaemon} />))
    await user.click(screen.getByRole('button', { name: '데몬 중지' }))
    expect(screen.getByRole('dialog')).toHaveTextContent('데몬을 중지합니다.')
    await user.selectOptions(screen.getByLabelText('언어 선택'), 'en')
    expect(screen.getByRole('dialog')).toHaveTextContent('This stops the daemon.')
    expect(screen.getByRole('dialog').textContent).toContain('roborev.exe')
    expect(onDaemon).not.toHaveBeenCalled()
  })

  it('keeps Panel Refine start disabled across a language switch without network', async () => {
    const user = userEvent.setup()
    const onStart = vi.fn(async () => undefined)
    const panelConfiguration = { repositoryIdentity: 'repo-1', panels: [{ name: 'dashboard_cross', kind: 'panel' as const, members: ['codex'], origin: 'global' as const, valid: true }], selectedPanel: 'dashboard_cross', selectionRequired: false }
    const options = { executable: 'C:\\Tools\\roborev.exe', args: ['fix', '--agent', 'codex'], allowedFixAgents: ['codex'], defaultFixAgent: 'codex', defaultMaxFixAttempts: 3, minFixAttempts: 1, maxFixAttempts: 10 }
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    render(withProvider(<><LanguageSelector /><PanelRefine repoPath="E:\\repo" panelConfiguration={panelConfiguration} options={options} run={null} onStart={onStart} onCancel={vi.fn(async () => undefined)} /></>))
    expect(screen.getByRole('button', { name: 'Execution disabled' })).toBeDisabled()
    expect(screen.getByRole('status')).toHaveTextContent("RoboRev v0.64.0's fix command can implicitly manage daemon lifecycle")
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
    await user.selectOptions(screen.getByLabelText('Select language'), 'ko')
    expect(screen.getByRole('button', { name: '실행 비활성화' })).toBeDisabled()
    expect(screen.getByRole('status')).toHaveTextContent('RoboRev v0.64.0의 fix 명령은 daemon 수명 주기를 암묵적으로 관리할 수 있으므로 Dashboard 실행을 비활성화했습니다.')
    expect(onStart).not.toHaveBeenCalled()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('switches ReviewDetail from English to Korean without network, mutation, or tab/form reset', async () => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, 'en')
    const review = { id: 42, status: 'done', verdict: 'P', commit_subject: 'Technical subject', branch: 'main', review: { output: 'Review output' } }
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      calls.push(String(input))
      return Promise.resolve(new Response(JSON.stringify(review), { headers: { 'Content-Type': 'application/json' } }))
    }))
    const mutate = vi.fn(async () => undefined)
    const user = userEvent.setup()
    render(withProvider(<ReviewDetail job={review} jobs={[review]} close={vi.fn()} mutate={mutate} busy={false} canRerun={false} canLoadMembers={false} repoPath="E:\\repo" repoIdentity="repo-1" toast={vi.fn()} />))
    await user.click(screen.getByRole('button', { name: 'Comments' }))
    const comment = screen.getByRole('textbox', { name: 'Comment' })
    await user.type(comment, 'Preserve this comment')
    const before = calls.length
    await user.selectOptions(screen.getByLabelText('Select language'), 'ko')
    expect(screen.getByRole('button', { name: '댓글' })).toHaveClass('active')
    expect(screen.getByRole('textbox', { name: '댓글' })).toHaveValue('Preserve this comment')
    expect(screen.getByText('Technical subject')).toBeInTheDocument()
    expect(calls.length).toBe(before)
    expect(mutate).not.toHaveBeenCalled()
  })
})

describe('localized App presentation', () => {
  it('switches an empty review page without a refetch or repository state reset', async () => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, 'en')
    const bootstrap = {
      csrfToken: 'csrf', selectedRepository: 'E:\\repo', selectedRepositoryIdentity: 'repo-1',
      repositories: [{ identity: 'repo-1', name: 'repo', path: 'E:\\repo', branch: 'main', head: 'abcdef123456', headShort: 'abcdef12', commitSubject: 'Initial', dirty: false, hasConfig: true }],
      roborev: { installed: true, version: 'v0.64.0', status: { running: true }, capabilities: { reviewEnqueueApi: true, frozenRerunApi: true, jobListApi: true, jobOutputStatusApi: true, panelReview: true, fixCommand: true, panelMembersApi: true, promptLazyLoad: true, rawLog: true } },
    }
    const panel = { repositoryIdentity: 'repo-1', panels: [{ name: 'reviewers', kind: 'panel', members: ['codex'], origin: 'global', valid: true }], selectedPanel: 'reviewers', selectionRequired: false }
    const setup = { ...setupState, status: 'ready', phase: 'ready', installed: true, canInstall: false, installAvailable: false, daemonRunning: true }
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input); calls.push(path)
      if (path === '/api/bootstrap') return Promise.resolve(new Response(JSON.stringify(bootstrap), { headers: { 'Content-Type': 'application/json' } }))
      if (path.startsWith('/api/reviews?')) return Promise.resolve(new Response('[]', { headers: { 'Content-Type': 'application/json' } }))
      if (path.startsWith('/api/repos/panels?')) return Promise.resolve(new Response(JSON.stringify(panel), { headers: { 'Content-Type': 'application/json' } }))
      if (path === '/api/setup/roborev') return Promise.resolve(new Response(JSON.stringify(setup), { headers: { 'Content-Type': 'application/json' } }))
      if (path === '/api/agents') return Promise.resolve(new Response(JSON.stringify({ repositoryIdentity: 'repo-1', roborevVersion: 'v0.64.0', configured: [] }), { headers: { 'Content-Type': 'application/json' } }))
      if (path === '/api/runtime') return Promise.resolve(new Response(JSON.stringify(runtime), { headers: { 'Content-Type': 'application/json' } }))
      if (path.includes('/api/panel-refines')) return Promise.resolve(new Response(JSON.stringify({ runs: [], options: { executable: 'C:\\Tools\\roborev.exe', args: ['fix', '--agent', 'codex'], allowedFixAgents: ['codex'], defaultFixAgent: 'codex', defaultMaxFixAttempts: 3, minFixAttempts: 1, maxFixAttempts: 10 } }), { headers: { 'Content-Type': 'application/json' } }))
      return Promise.resolve(new Response('{}', { headers: { 'Content-Type': 'application/json' } }))
    }))
    const user = userEvent.setup()
    render(withProvider(<App />))
    expect(await screen.findByText('No reviews found')).toBeInTheDocument()
    // The initial ready setup response triggers one authoritative dashboard
    // refresh. Wait for that bounded settling sequence before taking the
    // locale-switch request baseline; a fixed sleep can race it under the
    // full repository test load.
    await waitFor(() => {
      expect(calls.filter(path => path === '/api/bootstrap')).toHaveLength(2)
      expect(calls.filter(path => path === '/api/setup/roborev')).toHaveLength(1)
      expect(calls.filter(path => path.startsWith('/api/reviews?'))).toHaveLength(2)
      expect(calls.filter(path => path.startsWith('/api/repos/panels?'))).toHaveLength(3)
      expect(calls.filter(path => path.startsWith('/api/panel-refines?'))).toHaveLength(1)
      expect(calls.filter(path => path === '/api/runtime')).toHaveLength(1)
    })
    const before = calls.length
    await user.selectOptions(screen.getByLabelText('Select language'), 'ko')
    await waitFor(() => expect(screen.getByText('리뷰가 없습니다')).toBeInTheDocument())
    expect(calls.length).toBe(before)
    expect(screen.getByText('repo · main')).toBeInTheDocument()
  })
})
