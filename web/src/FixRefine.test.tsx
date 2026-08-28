import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { FixRefine, isFixTarget } from './FixRefine'
import { normalizeFixOperationsResponse, normalizeFixPreview, type PanelConfiguration, type PanelRefineOptions, type PanelRefineRun, type ReviewJob } from './api'
import { I18nProvider, LANGUAGE_STORAGE_KEY, LanguageSelector } from './i18n'

const panelConfiguration: PanelConfiguration = { repositoryIdentity: 'repo-1', panels: [], selectedPanel: '', selectionRequired: false }
const panelOptions: PanelRefineOptions | null = null
const panelRun: PanelRefineRun | null = null
const panelProps = { repoPath: 'C:\\repo', panelConfiguration, options: panelOptions, run: panelRun, available: false, disabledReason: 'unavailable', onStart: vi.fn(async () => undefined), onCancel: vi.fn(async () => undefined), onOpenReview: vi.fn() }
const jobs: ReviewJob[] = [
  { id: 41, status: 'done', verdict: 'F', closed: false, git_ref: 'abc123', branch: 'main', subject: 'single finding', job_type: 'review', panel_role: '' },
  { id: 42, status: 'done', verdict: 'F', closed: false, git_ref: 'def456', branch: 'feature', subject: 'synthesis finding', job_type: 'synthesis', panel_role: 'synthesis', panel_run_uuid: 'panel-1', panel_name: 'cross' },
  { id: 43, status: 'done', verdict: 'F', closed: false, git_ref: 'member', branch: 'main', job_type: 'review', panel_role: 'member', panel_run_uuid: 'panel-1', panel_name: 'cross' },
  { id: 44, status: 'done', verdict: 'F', closed: true, git_ref: 'closed', branch: 'main', job_type: 'review', panel_role: '' },
  { id: 45, status: 'running', verdict: 'F', closed: false, git_ref: 'running', branch: 'main', job_type: 'review', panel_role: '' },
]
const preview = { repositoryIdentity: 'repo-1', target: { jobId: 41, gitRef: 'abc123', branch: 'main', jobType: 'review', panelRole: '', panelName: '' }, agent: 'codex', command: { executable: 'roborev', args: ['fix', '--agent', 'codex', '--quiet', '41'] }, startingHead: 'abc123456789', startingBranch: 'main', previewToken: 'token-1' }
const operation = { id: 'fix-1', repositoryIdentity: 'repo-1', target: preview.target, agent: 'codex', command: preview.command, phase: 'starting', startingHead: preview.startingHead, startingBranch: 'main' }
const operationFor = (phase: string) => ({ ...operation, phase })

function renderFix(selectedJobId: number | null = null) {
  return render(<I18nProvider><FixRefine repoPath="C:\\repo" repoIdentity="repo-1" jobs={jobs} selectedJobId={selectedJobId} onSelectedJobIdChange={vi.fn()} onOpenReview={vi.fn()} onTerminal={vi.fn(async () => undefined)} panelRefine={panelProps} /></I18nProvider>)
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => { resolve = resolvePromise; reject = rejectPromise })
  return { promise, resolve, reject }
}

describe('Fix & Refine frontend contract', () => {
  afterEach(() => { cleanup(); vi.unstubAllGlobals(); window.localStorage.setItem(LANGUAGE_STORAGE_KEY, 'en') })
  it('normalizes the strict privacy-safe fix contract without accepting malformed commands', () => {
    expect(normalizeFixPreview({ repositoryIdentity: 'repo-1', target: { jobId: 41, gitRef: 'abc', branch: 'main', jobType: 'review', panelRole: '' }, agent: 'codex', command: { executable: 'roborev', args: ['fix', '--agent', 'codex', '--quiet', '41'] }, startingHead: 'abc', startingBranch: 'main', previewToken: 'token' })).toMatchObject({ repositoryIdentity: 'repo-1', target: { jobId: 41 }, command: { executable: 'roborev', args: ['fix', '--agent', 'codex', '--quiet', '41'] } })
    expect(normalizeFixPreview({ ...preview, command: { ...preview.command, args: [1] } })).toBeNull()
    expect(normalizeFixPreview({ ...preview, command: { ...preview.command, executable: 'C:\\fake\\very-secret\\roborev.exe' } })).toBeNull()
    expect(normalizeFixPreview({ ...preview, repositoryPath: 'C:\\Users\\private\\source\\secret-project' })).toBeNull()
    expect(normalizeFixPreview({ ...preview, target: { ...preview.target, jobId: 0 } })).toBeNull()
    expect(normalizeFixPreview({ ...preview, target: { ...preview.target, jobId: '9007199254740992' } })).toBeNull()
    expect(normalizeFixOperationsResponse({ operations: [operation], operation: null }).operation?.id).toBe('fix-1')
    expect(normalizeFixOperationsResponse({ operations: [{ ...operation, target: { ...operation.target, jobId: '9007199254740992' } }], operation: null }).operations).toHaveLength(0)
    expect(normalizeFixOperationsResponse({ operations: [{ ...operation, errorCode: 'PRIVATE_FIX_STDERR' }], operation: null }).operations).toHaveLength(0)
    expect(normalizeFixOperationsResponse({ operations: [{ ...operation, errorCode: 42 }], operation: null }).operations).toHaveLength(0)
  })

  it.each([
    ['repositoryPath', { ...preview, repositoryPath: 'C:\\Users\\private\\source\\secret-project' }],
    ['path', { ...preview, target: { ...preview.target, path: 'C:\\Users\\private\\source\\secret-project' } }],
    ['binary', { ...preview, binary: 'C:\\fake\\very-secret\\roborev.exe' }],
    ['binaryPath', { ...preview, command: { ...preview.command, binaryPath: 'C:\\fake\\very-secret\\roborev.exe' } }],
    ['cwd', { ...preview, command: { ...preview.command, cwd: 'C:\\Users\\private\\source\\secret-project' } }],
    ['env', { ...preview, command: { ...preview.command, env: { PRIVATE_FIX_STDERR: 'PRIVATE_FIX_OUTPUT' } } }],
    ['stdout', { ...preview, stdout: 'PRIVATE_FIX_OUTPUT' }],
    ['stderr', { ...preview, stderr: 'PRIVATE_FIX_STDERR' }],
    ['rawError', { ...preview, rawError: 'PRIVATE_FIX_STDERR' }],
    ['escaped raw_error', { ...preview, raw_error: 'C:\\\\Users\\\\private\\\\source\\\\secret-project' }],
    ['unknown command execution field', { ...preview, command: { ...preview.command, workingDirectory: 'C:\\Users\\private\\source\\secret-project' } }],
  ])('rejects forbidden or unknown %s fields', (_name, value) => {
    expect(normalizeFixPreview(value)).toBeNull()
  })

  it('rejects private fields recursively from historical operations too', () => {
    expect(normalizeFixOperationsResponse({ operations: [{ ...operation, command: { ...operation.command, dir: 'C:\\Users\\private\\source\\secret-project' } }], operation: null }).operations).toHaveLength(0)
    expect(normalizeFixOperationsResponse({ operations: [{ ...operation, rawError: 'PRIVATE_FIX_STDERR' }], operation: null }).operations).toHaveLength(0)
  })

  it('keeps private preview and historical markers out of the DOM', async () => {
    const privateMarkers = [
      'C:\\Users\\private\\source\\secret-project',
      'C:\\fake\\very-secret\\roborev.exe',
      'PRIVATE_FIX_STDERR',
      'PRIVATE_FIX_OUTPUT',
      'C:\\\\Users\\\\private\\\\source\\\\secret-project',
    ]
    const privatePreview = { ...preview, repositoryPath: privateMarkers[0], command: { ...preview.command, executable: privateMarkers[1] }, stderr: privateMarkers[2], stdout: privateMarkers[3], raw_error: privateMarkers[4] }
    const privateOperation = { ...operation, path: privateMarkers[0], command: { ...operation.command, executable: privateMarkers[1] }, stderr: privateMarkers[2], stdout: privateMarkers[3], rawError: privateMarkers[4] }
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input)
      if (path.includes('/api/fixes/preview')) return new Response(JSON.stringify(privatePreview), { status: 200 })
      if (path.includes('/api/fixes?')) return new Response(JSON.stringify({ operations: [privateOperation], operation: privateOperation }), { status: 200 })
      return new Response('{}', { status: 200 })
    }))
    renderFix(41)
    await screen.findByText(/Could not prepare this fix/)
    for (const marker of privateMarkers) expect(document.body.textContent).not.toContain(marker)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('renders only an allowlisted historical failure code, never raw diagnostics', async () => {
    const safeOperation = { ...operation, phase: 'failed', errorCode: 'fix_command_failed' }
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input)
      if (path.includes('/api/fixes?')) return new Response(JSON.stringify({ operations: [safeOperation], operation: safeOperation }), { status: 200 })
      return new Response('{}', { status: 200 })
    }))
    render(<I18nProvider><FixRefine repoPath="C:\\Users\\private\\source\\secret-project" repoIdentity="repo-1" jobs={jobs} selectedJobId={null} onSelectedJobIdChange={vi.fn()} onOpenReview={vi.fn()} onTerminal={vi.fn(async () => undefined)} panelRefine={panelProps} /></I18nProvider>)
    expect(await screen.findByText('Failure code: fix_command_failed')).toBeInTheDocument()
    expect(normalizeFixOperationsResponse({ operations: [{ ...safeOperation, errorCode: 'PRIVATE_FIX_STDERR' }], operation: null }).operations).toHaveLength(0)
    for (const marker of ['C:\\Users\\private\\source\\secret-project', 'C:\\fake\\very-secret\\roborev.exe', 'PRIVATE_FIX_STDERR', 'PRIVATE_FIX_OUTPUT']) {
      expect(document.body.textContent).not.toContain(marker)
    }
  })

  it('mirrors backend eligibility and rejects missing or malformed target identity', () => {
    expect(isFixTarget(jobs[0])).toBe(true)
    expect(isFixTarget({ ...jobs[0], job_type: undefined })).toBe(false)
    expect(isFixTarget({ ...jobs[0], panel_role: undefined })).toBe(true)
    expect(isFixTarget({ ...jobs[0], panel_role: undefined, panel_name: 'cross' })).toBe(false)
    expect(isFixTarget({ ...jobs[0], panel_role: undefined, panel_run_uuid: 'panel-1' })).toBe(false)
    expect(isFixTarget({ ...jobs[0], closed: undefined })).toBe(false)
    expect(isFixTarget({ ...jobs[0], addressed: true })).toBe(false)
    expect(isFixTarget({ ...jobs[0], status: 'failed' })).toBe(false)
    expect(isFixTarget({ ...jobs[1], panel_name: '' })).toBe(false)
    expect(isFixTarget({ ...jobs[1], panel_run_uuid: '' })).toBe(false)
    expect(isFixTarget({ ...jobs[1], job_type: 'panel_synthesis' })).toBe(false)
    expect(isFixTarget({ ...jobs[2], panel_role: 'member' })).toBe(false)
    expect(isFixTarget({ ...jobs[0], id: 0 })).toBe(false)
  })

  it('offers only open terminal single/synthesis findings and excludes members', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ operations: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } })))
    renderFix()
    const select = await screen.findByLabelText('Finding to fix')
    expect(screen.getAllByRole('option').map(option => option.textContent)).toEqual(['Choose an open finding', expect.stringContaining('single review'), expect.stringContaining('panel synthesis')])
    expect(select).not.toHaveValue('43')
  })

  it('displays the safe semantic command but keeps the unsafe mutation confirmation disabled', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      calls.push(`${init?.method ?? 'GET'} ${path}`)
      if (path.includes('/api/fixes/preview')) return new Response(JSON.stringify(preview), { status: 200 })
      if (path.includes('/api/fixes?')) return new Response(JSON.stringify({ operations: [], operation: null }), { status: 200 })
      if (path === '/api/fixes' && init?.method === 'POST') return new Response(JSON.stringify({ operation }), { status: 202 })
      return new Response('{}', { status: 200 })
    }))
    const onSelected = vi.fn()
    const view = render(<I18nProvider><FixRefine repoPath="C:\\Users\\private\\source\\secret-project" repoIdentity="repo-1" jobs={jobs} selectedJobId={null} onSelectedJobIdChange={onSelected} onOpenReview={vi.fn()} onTerminal={vi.fn(async () => undefined)} panelRefine={panelProps} /></I18nProvider>)
    fireEvent.change(await screen.findByLabelText('Finding to fix'), { target: { value: '41' } })
    expect(onSelected).toHaveBeenCalledWith(41)
    // A controlled selector is intentionally used by the App; rerender with the selected ID to model that owner update.
    view.rerender(<I18nProvider><FixRefine repoPath="C:\\Users\\private\\source\\secret-project" repoIdentity="repo-1" jobs={jobs} selectedJobId={41} onSelectedJobIdChange={onSelected} onOpenReview={vi.fn()} onTerminal={vi.fn(async () => undefined)} panelRefine={panelProps} /></I18nProvider>)
    await screen.findByRole('button', { name: 'Review execution preview' })
    fireEvent.click(screen.getByRole('button', { name: 'Review execution preview' }))
    const dialog = screen.getByRole('dialog')
    expect(await screen.findByText('Selected repository')).toBeInTheDocument()
    expect(screen.getByText('roborev')).toBeInTheDocument()
    for (const marker of ['C:\\Users\\private\\source\\secret-project', 'C:\\fake\\very-secret\\roborev.exe', 'PRIVATE_FIX_STDERR', 'PRIVATE_FIX_OUTPUT', 'C:\\\\Users\\\\private\\\\source\\\\secret-project']) {
      expect(dialog.textContent).not.toContain(marker)
    }
    expect(screen.getByText('argv[0]')).toBeInTheDocument()
    const confirm = screen.getByRole('button', { name: 'Confirm and run fix' })
    expect(confirm).toBeDisabled()
    fireEvent.click(confirm)
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(calls.filter(call => call === 'POST /api/fixes')).toHaveLength(0)
  })

  it('preserves target, modal, and operation state across locale changes without refetch or POST', async () => {
    let previewCalls = 0
    let postCalls = 0
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path.includes('/api/fixes/preview')) { previewCalls++; return new Response(JSON.stringify(preview), { status: 200 }) }
      if (path.includes('/api/fixes?')) return new Response(JSON.stringify({ operations: [], operation: null }), { status: 200 })
      if (path === '/api/fixes' && init?.method === 'POST') { postCalls++; return new Response(JSON.stringify({ operation }), { status: 202 }) }
      if (path.includes('/api/fixes/fix-1')) return new Response(JSON.stringify(operation), { status: 200 })
      return new Response('{}', { status: 200 })
    }))
    render(<I18nProvider><LanguageSelector /><FixRefine repoPath="C:\repo" repoIdentity="repo-1" jobs={jobs} selectedJobId={41} onSelectedJobIdChange={vi.fn()} onOpenReview={vi.fn()} onTerminal={vi.fn(async () => undefined)} panelRefine={panelProps} /></I18nProvider>)
    await screen.findByRole('button', { name: 'Review execution preview' })
    fireEvent.click(screen.getByRole('button', { name: 'Review execution preview' }))
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
    fireEvent.change(screen.getAllByRole('combobox', { name: 'Select language' })[0], { target: { value: 'ko' } })
    await waitFor(() => expect(screen.getByRole('dialog')).toBeInTheDocument())
    expect(screen.getAllByRole('combobox', { name: '언어 선택' })[0]).toHaveValue('ko')
    expect(screen.getByRole('combobox', { name: '수정할 문제' })).toHaveValue('41')
    expect(previewCalls).toBe(1)
    expect(postCalls).toBe(0)
    expect(screen.getByRole('button', { name: '확인하고 수정 실행' })).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: '확인하고 수정 실행' }))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(postCalls).toBe(0)
    expect(screen.getByRole('combobox', { name: '수정할 문제' })).toHaveValue('41')
  })

  it('closes stale confirmation and clears the target when a same-ID finding disappears', async () => {
    let previewCalls = 0
    let postCalls = 0
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path.includes('/api/fixes/preview')) { previewCalls++; return new Response(JSON.stringify(preview), { status: 200 }) }
      if (path.includes('/api/fixes?')) return new Response(JSON.stringify({ operations: [], operation: null }), { status: 200 })
      if (path === '/api/fixes' && init?.method === 'POST') { postCalls++; return new Response(JSON.stringify({ operation }), { status: 202 }) }
      return new Response('{}', { status: 200 })
    }))
    const onSelected = vi.fn()
    const view = render(<I18nProvider><FixRefine repoPath="C:\\repo" repoIdentity="repo-1" jobs={jobs} selectedJobId={41} onSelectedJobIdChange={onSelected} onOpenReview={vi.fn()} onTerminal={vi.fn(async () => undefined)} panelRefine={panelProps} /></I18nProvider>)
    await screen.findByRole('button', { name: 'Review execution preview' })
    fireEvent.click(screen.getByRole('button', { name: 'Review execution preview' }))
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
    view.rerender(<I18nProvider><FixRefine repoPath="C:\\repo" repoIdentity="repo-1" jobs={[{ ...jobs[0], closed: true }]} selectedJobId={41} onSelectedJobIdChange={onSelected} onOpenReview={vi.fn()} onTerminal={vi.fn(async () => undefined)} panelRefine={panelProps} /></I18nProvider>)
    await waitFor(() => expect(onSelected).toHaveBeenCalledWith(null))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Confirm and run fix' })).not.toBeInTheDocument()
    expect(previewCalls).toBe(1)
    expect(postCalls).toBe(0)
  })

  it('refetches preview when the selected target metadata changes without using object identity', async () => {
    let previewCalls = 0
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input)
      if (path.includes('/api/fixes/preview')) { previewCalls++; return new Response(JSON.stringify({ ...preview, startingHead: previewCalls === 1 ? 'abc123456789' : 'changed-head', previewToken: `token-${previewCalls}` }), { status: 200 }) }
      if (path.includes('/api/fixes?')) return new Response(JSON.stringify({ operations: [], operation: null }), { status: 200 })
      return new Response('{}', { status: 200 })
    }))
    const view = render(<I18nProvider><FixRefine repoPath="C:\\repo" repoIdentity="repo-1" jobs={jobs} selectedJobId={41} onSelectedJobIdChange={vi.fn()} onOpenReview={vi.fn()} onTerminal={vi.fn(async () => undefined)} panelRefine={panelProps} /></I18nProvider>)
    await screen.findByRole('button', { name: 'Review execution preview' })
    view.rerender(<I18nProvider><FixRefine repoPath="C:\\repo" repoIdentity="repo-1" jobs={[{ ...jobs[0], panel_role: undefined }]} selectedJobId={41} onSelectedJobIdChange={vi.fn()} onOpenReview={vi.fn()} onTerminal={vi.fn(async () => undefined)} panelRefine={panelProps} /></I18nProvider>)
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(previewCalls).toBe(1)
    view.rerender(<I18nProvider><FixRefine repoPath="C:\\repo" repoIdentity="repo-1" jobs={[{ ...jobs[0], git_ref: 'changed-ref' }]} selectedJobId={41} onSelectedJobIdChange={vi.fn()} onOpenReview={vi.fn()} onTerminal={vi.fn(async () => undefined)} panelRefine={panelProps} /></I18nProvider>)
    await waitFor(() => expect(previewCalls).toBe(2))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it.each([
    ['starting', 'Starting'], ['running', 'Running'], ['succeeded', 'Pass'], ['failed', 'Error'], ['cancelled', 'Canceled'],
  ])('renders %s operation status', async (phase, label) => {
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input)
      if (path.includes('/api/fixes?')) return new Response(JSON.stringify({ operations: [operationFor(phase)], operation: operationFor(phase) }), { status: 200 })
      if (path.includes('/api/fixes/fix-1')) return new Response(JSON.stringify(operationFor(phase)), { status: 200 })
      return new Response('{}', { status: 200 })
    }))
    const onTerminal = vi.fn(async () => undefined)
    render(<I18nProvider><FixRefine repoPath="C:\repo" repoIdentity="repo-1" jobs={jobs} selectedJobId={null} onSelectedJobIdChange={vi.fn()} onOpenReview={vi.fn()} onTerminal={onTerminal} panelRefine={panelProps} /></I18nProvider>)
    expect(await screen.findByRole('heading', { name: label })).toBeInTheDocument()
    if (['succeeded', 'failed', 'cancelled'].includes(phase)) await waitFor(() => expect(onTerminal).toHaveBeenCalledTimes(1))
  })

  it('refreshes authoritatively after cancellation reaches a terminal operation', async () => {
    let listCalls = 0
    const onTerminal = vi.fn(async () => undefined)
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path.includes('/api/fixes?')) {
        listCalls++
        const current = listCalls === 1 ? operationFor('running') : operationFor('cancelled')
        return new Response(JSON.stringify({ operations: [current], operation: current }), { status: 200 })
      }
      if (path.includes('/api/fixes/fix-1')) return new Response(JSON.stringify(operationFor('running')), { status: 200 })
      if (path.includes('/cancel') && init?.method === 'POST') return new Response(JSON.stringify({ operation: operationFor('cancelled') }), { status: 202 })
      return new Response('{}', { status: 200 })
    }))
    render(<I18nProvider><FixRefine repoPath="C:\repo" repoIdentity="repo-1" jobs={jobs} selectedJobId={null} onSelectedJobIdChange={vi.fn()} onOpenReview={vi.fn()} onTerminal={onTerminal} panelRefine={panelProps} /></I18nProvider>)
    await userEvent.click(await screen.findByRole('button', { name: 'Cancel fix' }))
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Canceled' })).toBeInTheDocument())
    expect(onTerminal).toHaveBeenCalledTimes(1)
  })

  it('drops old target and operation UI and suppresses stale detail errors after repository switch', async () => {
    const staleDetail = deferred<Response>()
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input)
      if (path.includes('/api/fixes?repositoryIdentity=repo-a')) { const current = { ...operationFor('running'), repositoryIdentity: 'repo-a' }; return new Response(JSON.stringify({ operations: [current], operation: current }), { status: 200 }) }
      if (path.includes('/api/fixes?repositoryIdentity=repo-b')) return new Response(JSON.stringify({ operations: [], operation: null }), { status: 200 })
      if (path.includes('/api/fixes/fix-1')) return staleDetail.promise
      return new Response('{}', { status: 200 })
    }))
    const view = render(<I18nProvider><FixRefine repoPath="C:\repo" repoIdentity="repo-a" jobs={jobs} selectedJobId={41} onSelectedJobIdChange={vi.fn()} onOpenReview={vi.fn()} onTerminal={vi.fn(async () => undefined)} panelRefine={panelProps} /></I18nProvider>)
    expect(await screen.findByRole('heading', { name: 'Running' })).toBeInTheDocument()
    view.rerender(<I18nProvider><FixRefine repoPath="D:\repo-b" repoIdentity="repo-b" jobs={[]} selectedJobId={null} onSelectedJobIdChange={vi.fn()} onOpenReview={vi.fn()} onTerminal={vi.fn(async () => undefined)} panelRefine={panelProps} /></I18nProvider>)
    await waitFor(() => expect(screen.queryByText('Running')).not.toBeInTheDocument())
    staleDetail.reject(new Error('stale detail failure'))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(screen.queryByText('stale detail failure')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Review execution preview' })).not.toBeInTheDocument()
  })
})
