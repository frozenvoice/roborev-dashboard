import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { App } from './App'
import { Analyze } from './Analyze'
import { I18nProvider, LANGUAGE_STORAGE_KEY, LanguageSelector } from './i18n'

const repo = { identity: 'repo-1', path: 'E:\\repo' }
const catalog = { types: [
  { name: 'refactor', description: 'Suggest refactoring opportunities' },
  { name: 'security', description: 'Find concrete security risks in existing code' },
] }
const appBootstrap = {
  csrfToken: 'csrf', selectedRepository: repo.path, selectedRepositoryIdentity: repo.identity,
  repositories: [{ identity: repo.identity, name: 'repo', path: repo.path, branch: 'main', head: 'abcdef123456', headShort: 'abcdef12', commitSubject: 'Initial', dirty: false, hasConfig: true }],
  roborev: { installed: true, version: 'v0.64.0', status: { running: true, known: true }, capabilities: { reviewEnqueueApi: false, frozenRerunApi: false, jobListApi: false, jobOutputStatusApi: false, panelReview: false, fixCommand: false, panelMembersApi: false, promptLazyLoad: false, rawLog: false, streamEvents: false },
  },
}

function response(value: unknown, status = 200) { return Promise.resolve(new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } })) }
function renderAnalyze(repoIdentity = repo.identity) { return render(<I18nProvider><Analyze repoIdentity={repoIdentity} /></I18nProvider>) }

afterEach(() => { cleanup(); window.localStorage.setItem(LANGUAGE_STORAGE_KEY, 'en'); vi.restoreAllMocks(); vi.unstubAllGlobals() })

describe('Analyze', () => {
  it('mounts from App navigation without a soon badge or roadmap placeholder', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(appBootstrap)
      if (path.startsWith('/api/repos/panels?')) return response({ repositoryIdentity: repo.identity, panels: [{ name: 'none', kind: 'single', members: [], origin: 'builtin', valid: true }], selectedPanel: 'none', selectionRequired: false })
      if (path.startsWith('/api/panel-refines?')) return response({ runs: [], options: null })
      if (path === '/api/setup/roborev') return response({})
      if (path === '/api/analyses/types') return response(catalog)
      return response({})
    }))
    render(<I18nProvider><App /></I18nProvider>)
    const analyzeNav = await screen.findByRole('button', { name: 'Analyze' })
    expect(analyzeNav).not.toHaveTextContent('Soon')
    await userEvent.click(analyzeNav)
    expect(await screen.findByRole('heading', { name: 'Analyze' })).toBeInTheDocument()
    expect(screen.queryByText('Checkpoint roadmap')).not.toBeInTheDocument()
  })

  it('discovers the catalog once and loads a prompt only after an explicit click', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input); calls.push(path)
      if (path === '/api/analyses/types') return response(catalog)
      if (path.startsWith('/api/analyses/prompt?')) return response({ analysis_type: 'refactor', prompt: 'Use the refactor analysis rubric.' })
      return response({})
    }))
    renderAnalyze()
    expect(await screen.findByRole('option', { name: 'refactor' })).toBeInTheDocument()
    expect(screen.getByText('Suggest refactoring opportunities')).toBeInTheDocument()
    expect(calls.filter(path => path === '/api/analyses/types')).toHaveLength(1)
    expect(calls.some(path => path.includes('/api/analyses/prompt'))).toBe(false)
    await userEvent.click(screen.getByRole('button', { name: 'View prompt template' }))
    expect(await screen.findByText('Use the refactor analysis rubric.')).toBeInTheDocument()
    expect(calls.filter(path => path.includes('/api/analyses/prompt'))).toHaveLength(1)
  })

  it('discards a stale prompt after changing analysis type', async () => {
    let resolvePrompt!: (value: Response) => void
    const prompt = new Promise<Response>(resolve => { resolvePrompt = resolve })
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/analyses/types') return response(catalog)
      if (path.includes('type=refactor')) return prompt
      return response({ analysis_type: 'security', prompt: 'Security prompt' })
    }))
    renderAnalyze()
    await screen.findByRole('option', { name: 'refactor' })
    await userEvent.click(screen.getByRole('button', { name: 'View prompt template' }))
    fireEvent.change(screen.getByRole('combobox', { name: 'Analysis type' }), { target: { value: 'security' } })
    resolvePrompt(new Response(JSON.stringify({ analysis_type: 'refactor', prompt: 'STALE PROMPT' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(screen.queryByText('STALE PROMPT')).not.toBeInTheDocument()
  })

  it('copies the exact prompt source and reports success without refetching', async () => {
    const promptText = 'Use the refactor analysis rubric.\nPreserve every RoboRev instruction.'
    const clipboard = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue(undefined)
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input); calls.push(path)
      if (path === '/api/analyses/types') return response(catalog)
      if (path.startsWith('/api/analyses/prompt?')) return response({ analysis_type: 'refactor', prompt: promptText })
      return response({})
    }))
    renderAnalyze()
    await screen.findByRole('option', { name: 'refactor' })
    await userEvent.click(screen.getByRole('button', { name: 'View prompt template' }))
    await screen.findByRole('button', { name: 'Copy prompt' })
    await userEvent.click(screen.getByRole('button', { name: 'Copy prompt' }))
    expect(await screen.findByRole('status')).toHaveTextContent('Prompt template copied.')
    expect(clipboard).toHaveBeenCalledWith(promptText)
    expect(calls.filter(path => path === '/api/analyses/types')).toHaveLength(1)
    expect(calls.filter(path => path.includes('/api/analyses/prompt'))).toHaveLength(1)
  })

  it('reports clipboard rejection without changing or refetching the prompt', async () => {
    const promptText = 'Prompt remains unchanged when clipboard access is denied.'
    const clipboard = vi.spyOn(navigator.clipboard, 'writeText').mockRejectedValue(new Error('permission denied'))
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input); calls.push(path)
      if (path === '/api/analyses/types') return response(catalog)
      if (path.startsWith('/api/analyses/prompt?')) return response({ analysis_type: 'refactor', prompt: promptText })
      return response({})
    }))
    renderAnalyze()
    await screen.findByRole('option', { name: 'refactor' })
    await userEvent.click(screen.getByRole('button', { name: 'View prompt template' }))
    await screen.findByRole('button', { name: 'Copy prompt' })
    await userEvent.click(screen.getByRole('button', { name: 'Copy prompt' }))
    expect(await screen.findByRole('status')).toHaveTextContent('Prompt template could not be copied. Clipboard access was denied.')
    expect(screen.getByText(content => content.includes('Prompt remains unchanged when clipboard access is denied.'))).toBeInTheDocument()
    expect(clipboard).toHaveBeenCalledWith(promptText)
    expect(calls.filter(path => path.includes('/api/analyses/prompt'))).toHaveLength(1)
  })

  it('uses the latest locale for copy feedback while a copy is pending', async () => {
    const promptText = 'Locale-stable prompt source.'
    let resolveCopy!: () => void
    const copyPending = new Promise<void>(resolve => { resolveCopy = resolve })
    const clipboard = vi.spyOn(navigator.clipboard, 'writeText').mockReturnValue(copyPending)
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input); calls.push(path)
      if (path === '/api/analyses/types') return response(catalog)
      if (path.startsWith('/api/analyses/prompt?')) return response({ analysis_type: 'refactor', prompt: promptText })
      return response({})
    }))
    render(<I18nProvider><LanguageSelector /><Analyze repoIdentity={repo.identity} /></I18nProvider>)
    await screen.findByRole('option', { name: 'refactor' })
    await userEvent.click(screen.getByRole('button', { name: 'View prompt template' }))
    await screen.findByRole('button', { name: 'Copy prompt' })
    await userEvent.click(screen.getByRole('button', { name: 'Copy prompt' }))
    expect(screen.getByRole('status')).toHaveTextContent('Copying prompt…')
    fireEvent.change(screen.getByRole('combobox', { name: 'Select language' }), { target: { value: 'ko' } })
    expect(screen.getByRole('status')).toHaveTextContent('Prompt Template 복사 중…')
    resolveCopy()
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Prompt Template을 복사했습니다.'))
    expect(clipboard).toHaveBeenCalledWith(promptText)
    expect(calls.filter(path => path === '/api/analyses/types')).toHaveLength(1)
    expect(calls.filter(path => path.includes('/api/analyses/prompt'))).toHaveLength(1)
  })

  it('keeps execution disabled and never requests preview or POST endpoints', async () => {
    const calls: Array<{ path: string; method?: string }> = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input); calls.push({ path, method: init?.method })
      if (path === '/api/analyses/types') return response(catalog)
      return response({})
    }))
    renderAnalyze()
    await screen.findByRole('option', { name: 'refactor' })
    const disabled = screen.getByRole('button', { name: 'Execution disabled' })
    expect(disabled).toBeDisabled()
    expect(screen.queryByRole('radio')).not.toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(screen.queryByText(/receipt|view result/i)).not.toBeInTheDocument()
    expect(calls.some(call => call.path.includes('/api/analyses/preview'))).toBe(false)
    expect(calls.some(call => call.path === '/api/analyses' && call.method === 'POST')).toBe(false)
  })

  it('shows the exact agentic risk and disabled execution copy in English and Korean', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => String(input) === '/api/analyses/types' ? response(catalog) : response({})))
    render(<I18nProvider><LanguageSelector /><Analyze repoIdentity={repo.identity} /></I18nProvider>)
    await screen.findByRole('option', { name: 'refactor' })
    expect(screen.getByText('RoboRev v0.64 Analyze is an agentic job. The configured agent may modify files and execute commands.')).toBeInTheDocument()
    expect(screen.getByText('Dashboard cannot safely own the working-tree lifecycle, so execution is disabled. Catalog and prompt inspection remain available.')).toBeInTheDocument()
    expect(document.body.textContent).not.toMatch(/fix/i)
    fireEvent.change(screen.getByRole('combobox', { name: 'Select language' }), { target: { value: 'ko' } })
    expect(screen.getByText('RoboRev v0.64 Analyze는 에이전트 작업입니다. 구성된 에이전트가 파일을 수정하고 명령을 실행할 수 있습니다.')).toBeInTheDocument()
    expect(screen.getByText('Dashboard가 작업 트리 수명 주기를 안전하게 통제할 수 없어 v0.64 Analyze 실행을 비활성화했습니다. 분석 유형과 Prompt Template 조회는 계속 사용할 수 있습니다.')).toBeInTheDocument()
  })

  it('does not refetch on locale changes and reports a late malformed prompt in the latest locale', async () => {
    let resolvePrompt!: (value: Response) => void
    const prompt = new Promise<Response>(resolve => { resolvePrompt = resolve })
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input); calls.push(path)
      if (path === '/api/analyses/types') return response(catalog)
      if (path.startsWith('/api/analyses/prompt?')) return prompt
      return response({})
    }))
    render(<I18nProvider><LanguageSelector /><Analyze repoIdentity={repo.identity} /></I18nProvider>)
    await screen.findByRole('option', { name: 'refactor' })
    await userEvent.click(screen.getByRole('button', { name: 'View prompt template' }))
    fireEvent.change(screen.getByRole('combobox', { name: 'Select language' }), { target: { value: 'ko' } })
    expect(calls.filter(path => path === '/api/analyses/types')).toHaveLength(1)
    expect(calls.filter(path => path.includes('/api/analyses/prompt'))).toHaveLength(1)
    resolvePrompt(new Response(JSON.stringify({ malformed: true }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('RoboRev Prompt Template을 검증하지 못했습니다.'))
    expect(calls.filter(path => path === '/api/analyses/types')).toHaveLength(1)
    expect(calls.filter(path => path.includes('/api/analyses/prompt'))).toHaveLength(1)
  })

  it('does not refetch the global catalog on repository switch and ignores the old prompt response', async () => {
    let resolvePrompt!: (value: Response) => void
    const prompt = new Promise<Response>(resolve => { resolvePrompt = resolve })
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input); calls.push(path)
      if (path === '/api/analyses/types') return response(catalog)
      if (path.startsWith('/api/analyses/prompt?')) return prompt
      return response({})
    }))
    const view = renderAnalyze('repo-1')
    await screen.findByRole('option', { name: 'refactor' })
    await userEvent.click(screen.getByRole('button', { name: 'View prompt template' }))
    view.rerender(<I18nProvider><Analyze repoIdentity="repo-2" /></I18nProvider>)
    expect(screen.queryByText('Prompt template')).not.toBeInTheDocument()
    resolvePrompt(new Response(JSON.stringify({ analysis_type: 'refactor', prompt: 'STALE REPOSITORY PROMPT' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(screen.queryByText('STALE REPOSITORY PROMPT')).not.toBeInTheDocument()
    expect(calls.filter(path => path === '/api/analyses/types')).toHaveLength(1)
  })

  it('keeps introspection available without daemon or installation gates', async () => {
    const offlineBootstrap = { ...appBootstrap, roborev: { ...appBootstrap.roborev, installed: false, status: { running: false, known: true } } }
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(offlineBootstrap)
      if (path.startsWith('/api/repos/panels?')) return response({ repositoryIdentity: repo.identity, panels: [], selectedPanel: '', selectionRequired: false })
      if (path.startsWith('/api/panel-refines?')) return response({ runs: [], options: null })
      if (path === '/api/setup/roborev') return response({})
      if (path === '/api/analyses/types') return response(catalog)
      return response({})
    }))
    render(<I18nProvider><App /></I18nProvider>)
    await userEvent.click(await screen.findByRole('button', { name: 'Analyze' }))
    expect(await screen.findByRole('option', { name: 'refactor' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Execution disabled' })).toBeDisabled()
  })
})
