import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { App } from './App'
import { Analyze } from './Analyze'
import { normalizeAnalysisReceipt } from './api'
import { I18nProvider, LANGUAGE_STORAGE_KEY, LanguageSelector } from './i18n'

const repo = { identity: 'repo-1', path: 'E:\\repo' }
const catalog = { types: [
  { name: 'refactor', description: 'Suggest refactoring opportunities' },
  { name: 'security', description: 'Find concrete security risks in existing code' },
] }
const preview = {
  repositoryIdentity: repo.identity, repositoryPath: repo.path, head: 'abcdef123456', branch: 'main', analysisType: 'refactor', scope: 'branch',
  command: { executable: 'C:\\Tools\\roborev.exe', args: ['analyze', 'refactor', '--branch=HEAD', '--json'] }, previewToken: 'preview-1',
}
const receipt = { jobs: [{ id: 181, agent: 'codex' }], analysis_type: 'refactor', files: ['src/main.go'] }
const appBootstrap = {
  csrfToken: 'csrf', selectedRepository: repo.path, selectedRepositoryIdentity: repo.identity,
  repositories: [{ identity: repo.identity, name: 'repo', path: repo.path, branch: 'main', head: 'abcdef123456', headShort: 'abcdef12', commitSubject: 'Initial', dirty: false, hasConfig: true }],
  roborev: { installed: true, version: 'v0.64.0', status: { running: true, known: true }, capabilities: { reviewEnqueueApi: false, frozenRerunApi: false, jobListApi: false, jobOutputStatusApi: false, panelReview: false, fixCommand: false, panelMembersApi: false, promptLazyLoad: false, rawLog: false, streamEvents: false, analyzeCommand: true } },
}

function response(value: unknown, status = 200) { return Promise.resolve(new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } })) }
function props(overrides: Record<string, unknown> = {}) {
  return { repoIdentity: repo.identity, repoPath: repo.path, jobs: [], installed: true, daemonRunning: true, analyzeAvailable: true, busy: false, onOpenReview: vi.fn(), ...overrides }
}
function renderAnalyze(overrides: Record<string, unknown> = {}) { return render(<I18nProvider><Analyze {...props(overrides)} /></I18nProvider>) }

afterEach(() => { cleanup(); window.localStorage.setItem(LANGUAGE_STORAGE_KEY, 'en'); vi.restoreAllMocks() })

describe('Analyze', () => {
  it('mounts Analyze from App navigation without a soon badge or roadmap placeholder', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/bootstrap') return response(appBootstrap)
      if (path.startsWith('/api/repos/panels?')) return response({ repositoryIdentity: repo.identity, panels: [{ name: 'none', kind: 'single', members: [], origin: 'builtin', valid: true }], selectedPanel: 'none', selectionRequired: false })
      if (path.startsWith('/api/panel-refines?')) return response({ runs: [], options: null })
      if (path === '/api/setup/roborev') return response({})
      return response({})
    }))
    render(<I18nProvider><App /></I18nProvider>)
    const analyzeNav = await screen.findByRole('button', { name: 'Analyze' })
    expect(analyzeNav).not.toHaveTextContent('Soon')
    await userEvent.click(analyzeNav)
    expect(await screen.findByRole('heading', { name: 'Analyze' })).toBeInTheDocument()
    expect(screen.queryByText('Checkpoint roadmap')).not.toBeInTheDocument()
  })

  it('discovers types on mount, keeps Analyze out of soon chrome, and loads prompts only on click', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input); calls.push(path)
      if (path === '/api/analyses/types') return response(catalog)
      if (path.startsWith('/api/analyses/prompt?')) return response({ analysis_type: 'refactor', prompt: 'Use the refactor analysis rubric.' })
      return response({})
    }))
    render(<I18nProvider><LanguageSelector /><Analyze {...props()} /></I18nProvider>)
    expect(await screen.findByRole('option', { name: 'refactor' })).toBeInTheDocument()
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
    await userEvent.click(screen.getByRole('button', { name: 'View prompt template' }))
    expect(await screen.findByText('Security prompt')).toBeInTheDocument()
  })

  it('prepares an exact command, opens confirmation without POST, and confirms once', async () => {
    const calls: Array<{ path: string; method: string; body?: string }> = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input); calls.push({ path, method: init?.method ?? 'GET', body: init?.body as string | undefined })
      if (path === '/api/analyses/types') return response(catalog)
      if (path.startsWith('/api/analyses/preview?')) return response(preview)
      if (path === '/api/analyses') return response({ receipt, repositoryIdentity: repo.identity, analysisType: 'refactor', scope: 'branch' })
      return response({})
    }))
    renderAnalyze()
    await screen.findByRole('option', { name: 'refactor' })
    await userEvent.click(screen.getByRole('button', { name: 'Prepare analysis' }))
    expect(await screen.findByRole('dialog', { name: 'Confirm analysis' })).toBeInTheDocument()
    expect(screen.getByText('C:\\Tools\\roborev.exe')).toBeInTheDocument()
    expect(screen.getByText('--branch=HEAD')).toBeInTheDocument()
    expect(calls.filter(call => call.method === 'POST')).toHaveLength(0)
    await userEvent.click(screen.getByRole('button', { name: 'Run analysis' }))
    await waitFor(() => expect(calls.filter(call => call.method === 'POST')).toHaveLength(1))
    const body = JSON.parse(calls.find(call => call.method === 'POST')?.body ?? '{}')
    expect(body).toEqual({ repositoryIdentity: 'repo-1', analysisType: 'refactor', scope: 'branch', previewToken: 'preview-1' })
    expect(JSON.stringify(body)).not.toContain('--fix')
  })

  it('reconciles receipt status from authoritative jobs and opens existing ReviewDetail', async () => {
    const onOpenReview = vi.fn()
    const job = { id: 181, status: 'running', agent: 'codex' }
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/analyses/types') return response(catalog)
      if (path.startsWith('/api/analyses/preview?')) return response(preview)
      if (path === '/api/analyses') return response({ receipt, repositoryIdentity: repo.identity, analysisType: 'refactor', scope: 'branch' })
      return response({})
    }))
    renderAnalyze({ jobs: [job], onOpenReview })
    await screen.findByRole('option', { name: 'refactor' })
    await userEvent.click(screen.getByRole('button', { name: 'Prepare analysis' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Run analysis' }))
    expect(await screen.findByText('Running')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'View result' }))
    expect(onOpenReview).toHaveBeenCalledWith(181)
  })

  it('supports both scopes and generates the repository argv preview without posting', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input); calls.push(path)
      if (path === '/api/analyses/types') return response(catalog)
      if (path.includes('scope=repository')) return response({ ...preview, scope: 'repository', command: { executable: preview.command.executable, args: ['analyze', 'refactor', './...', '--json'] } })
      return response({})
    }))
    renderAnalyze()
    await screen.findByRole('option', { name: 'refactor' })
    await userEvent.click(screen.getByRole('radio', { name: /Entire repository/ }))
    expect(screen.getByText(/may include uncommitted working-tree content/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Prepare analysis' }))
    const dialog = await screen.findByRole('dialog', { name: 'Confirm analysis' })
    expect(within(dialog).getByText('./...')).toBeInTheDocument()
    expect(within(dialog).getAllByText(/agentic operation|model usage/i).length).toBeGreaterThan(0)
    expect(calls.some(path => path.includes('scope=repository'))).toBe(true)
    expect(calls.filter(path => path === '/api/analyses')).toHaveLength(0)
  })

  it('does not request or reset analysis state when locale changes', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => { calls.push(String(input)); return response(catalog) }))
    render(<I18nProvider><LanguageSelector /><Analyze {...props()} /></I18nProvider>)
    await screen.findByRole('option', { name: 'refactor' })
    const before = calls.length
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Select language' }), 'ko')
    expect(calls).toHaveLength(before)
    expect(screen.getByRole('heading', { name: '분석' })).toBeInTheDocument()
  })

  it('drops old repository catalog and receipt state on repository remount', async () => {
    const oldCatalog = { types: [{ name: 'refactor', description: 'Old repository type' }] }
    const newCatalog = { types: [{ name: 'security', description: 'New repository type' }] }
    let catalogCalls = 0
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      if (String(input) === '/api/analyses/types') return response(catalogCalls++ === 0 ? oldCatalog : newCatalog)
      return response({})
    }))
    const view = renderAnalyze()
    expect(await screen.findByRole('option', { name: 'refactor' })).toBeInTheDocument()
    view.rerender(<I18nProvider><Analyze {...props({ repoIdentity: 'repo-2', repoPath: 'E:\\other' })} /></I18nProvider>)
    expect(await screen.findByRole('option', { name: 'security' })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: 'refactor' })).not.toBeInTheDocument()
  })

  it('ignores a late catalog and prompt response from the previous repository', async () => {
    let resolveOldCatalog!: (value: Response) => void
    let resolveOldPrompt!: (value: Response) => void
    const oldCatalog = new Promise<Response>(resolve => { resolveOldCatalog = resolve })
    const oldPrompt = new Promise<Response>(resolve => { resolveOldPrompt = resolve })
    let catalogCalls = 0
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/analyses/types') return catalogCalls++ === 0 ? oldCatalog : response({ types: [{ name: 'security', description: 'New repository type' }] })
      if (path.includes('/api/analyses/prompt')) return oldPrompt
      return response({})
    }))
    const view = renderAnalyze()
    view.rerender(<I18nProvider><Analyze {...props({ repoIdentity: 'repo-2', repoPath: 'E:\\other' })} /></I18nProvider>)
    expect(await screen.findByRole('option', { name: 'security' })).toBeInTheDocument()
    resolveOldCatalog(new Response(JSON.stringify({ types: [{ name: 'refactor', description: 'Stale repository type' }] }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(screen.queryByRole('option', { name: 'refactor' })).not.toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: 'View prompt template' }))
    view.rerender(<I18nProvider><Analyze {...props({ repoIdentity: 'repo-3', repoPath: 'E:\\third' })} /></I18nProvider>)
    resolveOldPrompt(new Response(JSON.stringify({ analysisType: 'security', prompt: 'STALE REPOSITORY PROMPT' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(screen.queryByText('STALE REPOSITORY PROMPT')).not.toBeInTheDocument()
  })

  it('clears receipt and open preview state on repository switch', async () => {
    let catalogCalls = 0
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/analyses/types') return response(catalogCalls++ === 0 ? catalog : { types: [{ name: 'security', description: 'New repository type' }] })
      if (path.startsWith('/api/analyses/preview?')) return response(preview)
      if (path === '/api/analyses') return response({ receipt, repositoryIdentity: repo.identity, analysisType: 'refactor', scope: 'branch' })
      return response({})
    }))
    const view = renderAnalyze({ jobs: [{ id: 181, status: 'running', agent: 'codex' }] })
    await screen.findByRole('option', { name: 'refactor' })
    await userEvent.click(screen.getByRole('button', { name: 'Prepare analysis' }))
    expect(await screen.findByRole('dialog', { name: 'Confirm analysis' })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Run analysis' }))
    expect(await screen.findByText('#181 · refactor')).toBeInTheDocument()
    view.rerender(<I18nProvider><Analyze {...props({ repoIdentity: 'repo-2', repoPath: 'E:\\other', jobs: [] })} /></I18nProvider>)
    await screen.findByRole('option', { name: 'security' })
    expect(screen.queryByText('#181 · refactor')).not.toBeInTheDocument()
    expect(screen.queryByRole('dialog', { name: 'Confirm analysis' })).not.toBeInTheDocument()
  })

  it('preserves prompt and confirmation across locale changes without new requests', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input); calls.push(path)
      if (path === '/api/analyses/types') return response(catalog)
      if (path.startsWith('/api/analyses/prompt')) return response({ analysisType: 'refactor', prompt: 'Stable prompt template' })
      if (path.startsWith('/api/analyses/preview')) return response(preview)
      return response({})
    }))
    render(<I18nProvider><LanguageSelector /><Analyze {...props()} /></I18nProvider>)
    await screen.findByRole('option', { name: 'refactor' })
    await userEvent.click(screen.getByRole('button', { name: 'View prompt template' }))
    expect(await screen.findByText('Stable prompt template')).toBeInTheDocument()
    const beforePromptLocale = calls.length
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Select language' }), 'ko')
    expect(screen.getByText('Stable prompt template')).toBeInTheDocument()
    expect(calls).toHaveLength(beforePromptLocale)

    await userEvent.click(screen.getByRole('button', { name: '분석 준비' }))
    const dialog = await screen.findByRole('dialog', { name: '분석 확인' })
    const beforeDialogLocale = calls.length
    await userEvent.selectOptions(within(dialog).getByRole('combobox', { name: '언어 선택' }), 'en')
    expect(screen.getByRole('dialog', { name: 'Confirm analysis' })).toBeInTheDocument()
    expect(screen.getByText('--branch=HEAD')).toBeInTheDocument()
    expect(calls).toHaveLength(beforeDialogLocale)
  })

  it('accepts the backend receipt file limit and rejects one over the limit', () => {
    const files = Array.from({ length: 4096 }, (_, index) => `file-${index}.go`)
    const valid = normalizeAnalysisReceipt({ jobs: [{ id: 1, agent: 'codex' }], analysis_type: 'refactor', files }, 'refactor')
    expect(valid?.files).toHaveLength(4096)
    expect(normalizeAnalysisReceipt({ jobs: [{ id: 1, agent: 'codex' }], analysis_type: 'refactor', files: [...files, 'overflow.go'] }, 'refactor')).toBeNull()
  })

  it('keeps the Analyze task controls available at a 390px mobile viewport', async () => {
    const previousWidth = window.innerWidth
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 390 })
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => String(input) === '/api/analyses/types' ? response(catalog) : response({})))
    try {
      const view = renderAnalyze()
      await screen.findByRole('option', { name: 'refactor' })
      expect(view.container.querySelector('.analyze-grid')).toBeInTheDocument()
      expect(screen.getByRole('radio', { name: /Current branch changes/ })).toBeVisible()
      expect(screen.getByRole('button', { name: 'Prepare analysis' })).toBeVisible()
    } finally {
      Object.defineProperty(window, 'innerWidth', { configurable: true, value: previousWidth })
    }
  })
})
