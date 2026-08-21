import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider, LanguageSelector } from './i18n'
import { normalizeTaskPreview, normalizeTaskReceipt, TASK_MAX_BYTES, Tasks, validateTaskText } from './Tasks'

const repository = { identity: 'repo-1', name: 'repo', path: 'E:\\repo', branch: 'main', head: 'abcdef1234567890', headShort: 'abcdef12', commitSubject: 'Initial', dirty: false, hasConfig: true }
const task = 'HTTP layer 구조를 설명해줘\r\n"quotes" & $(literal)'
const preview = { repositoryIdentity: repository.identity, repositoryPath: repository.path, head: repository.head, branch: repository.branch, agent: 'codex', mode: 'read-only', safety: { allowUnsafeAgents: false, disableCodexSandbox: false }, command: { executable: 'C:\\Tools\\roborev.exe', args: ['run', '--agent', 'codex', '--json'], stdin: task }, previewToken: 'preview-token' }

function response(value: unknown, status = 200) { return Promise.resolve(new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } })) }
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(next => { resolve = next })
  return { promise, resolve }
}

afterEach(() => { cleanup(); vi.restoreAllMocks() })

describe('Tasks normalizers and workspace', () => {
  it('enforces byte size and control policy while preserving normal prompt text', () => {
    expect(validateTaskText(' \t\r\n')).toBe('empty')
    expect(validateTaskText('한글\r\n\t"quotes" & | ;')).toBeNull()
    expect(validateTaskText('\u0000')).toBe('control')
    expect(validateTaskText(`a${String.fromCharCode(0x0b)}b`)).toBe('control')
    expect(validateTaskText(`a${String.fromCharCode(0x80)}b`)).toBe('control')
    expect(validateTaskText(`a${String.fromCharCode(0x9f)}b`)).toBe('control')
    expect(validateTaskText('x'.repeat(TASK_MAX_BYTES))).toBeNull()
    expect(validateTaskText('한'.repeat(TASK_MAX_BYTES))).toBe('oversize')
  })

  it('normalizes only the exact server command and launch receipt shapes', () => {
    expect(normalizeTaskPreview(preview, task)?.command).toEqual(preview.command)
    expect(normalizeTaskPreview({ ...preview, command: { ...preview.command, args: ['run', '--agentic'] } }, task)).toBeNull()
    expect(normalizeTaskPreview({ ...preview, command: { ...preview.command, stdin: '한'.repeat(9_000) } })).toBeNull()
    expect(normalizeTaskReceipt({ job_id: 42, job_uuid: '550e8400-e29b-41d4-a716-446655440000', git_ref: 'run', status: 'queued' })).toEqual({ kind: 'created', jobId: 42, jobUUID: '550e8400-e29b-41d4-a716-446655440000', gitRef: 'run', status: 'queued' })
    expect(normalizeTaskReceipt({ job_id: 43, job_uuid: '00000000-0000-0000-0000-000000000000', git_ref: 'run', status: 'queued' })?.kind).toBe('created')
    expect(normalizeTaskReceipt({ skipped: true, reason: 'already queued' })).toEqual({ kind: 'skipped', reason: 'already queued' })
    expect(normalizeTaskReceipt({ job_id: 42, job_uuid: 'not-a-uuid', git_ref: 'run', status: 'queued' })).toBeNull()
  })

  it('opens a preview without POST and submits exactly once', async () => {
    const postResponse = deferred<Response>()
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).startsWith('/api/tasks/preview?')) return response({ ...preview, command: { ...preview.command, stdin: task.replace(/\r\n/g, '\n') } })
      if (String(input) === '/api/tasks') return postResponse.promise
      return response([])
    })
    vi.stubGlobal('fetch', fetchMock)
    const onQueued = vi.fn()
    render(<I18nProvider><Tasks repository={repository} jobs={[]} onOpenReview={vi.fn()} onQueued={onQueued} /></I18nProvider>)
    fireEvent.change(screen.getByLabelText('Task'), { target: { value: task } })
    fireEvent.click(screen.getByRole('button', { name: 'Prepare task' }))
    await screen.findByRole('dialog')
    expect(fetchMock.mock.calls.some(call => String(call[0]) === '/api/tasks')).toBe(false)
    expect(screen.getByLabelText('Task')).toBeDisabled()
    expect(screen.getByTestId('task-preview-stdin').textContent).toBe(task.replace(/\r\n/g, '\n'))
    const run = screen.getByRole('button', { name: 'Run task' })
    fireEvent.click(run)
    fireEvent.click(run)
    await waitFor(() => expect(fetchMock.mock.calls.filter(call => String(call[0]) === '/api/tasks')).toHaveLength(1))
    const textarea = screen.getByLabelText('Task')
    expect(textarea).toBeDisabled()
    fireEvent.change(textarea, { target: { value: 'edited while enqueue is pending' } })
    expect((textarea as HTMLTextAreaElement).value).toBe(task.replace(/\r\n/g, '\n'))
    postResponse.resolve(await response({ job_id: 42, job_uuid: '550e8400-e29b-41d4-a716-446655440000', git_ref: 'run', status: 'queued' }))
    await waitFor(() => expect(onQueued).toHaveBeenCalledTimes(1))
  })

  it('treats an ambiguous_enqueue phase as uncertain and keeps retry locked', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      if (String(input).startsWith('/api/tasks/preview?')) return response({ ...preview, command: { ...preview.command, stdin: task.replace(/\r\n/g, '\n') } })
      if (String(input) === '/api/tasks') return response({ error: 'enqueue result unavailable', phase: 'ambiguous_enqueue' }, 502)
      return response([])
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<I18nProvider><Tasks repository={repository} jobs={[]} onOpenReview={vi.fn()} /></I18nProvider>)
    fireEvent.change(screen.getByLabelText('Task'), { target: { value: task } })
    fireEvent.click(screen.getByRole('button', { name: 'Prepare task' }))
    await screen.findByRole('dialog')
    fireEvent.click(screen.getByRole('button', { name: 'Run task' }))
    await screen.findByText(/The registration result could not be confirmed/)
    expect(screen.getByRole('button', { name: 'Prepare task' })).toBeDisabled()
    expect(fetchMock.mock.calls.filter(call => String(call[0]) === '/api/tasks')).toHaveLength(1)
  })

  it('ignores a late preview after task edit', async () => {
    const pendingPreview = deferred<Response>()
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      if (String(input).startsWith('/api/tasks/preview?')) return pendingPreview.promise
      return response([])
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<I18nProvider><Tasks repository={repository} jobs={[]} onOpenReview={vi.fn()} /></I18nProvider>)
    fireEvent.change(screen.getByLabelText('Task'), { target: { value: 'first task' } })
    fireEvent.click(screen.getByRole('button', { name: 'Prepare task' }))
    fireEvent.change(screen.getByLabelText('Task'), { target: { value: 'edited task' } })
    pendingPreview.resolve(await response({ ...preview, command: { ...preview.command, stdin: 'first task' } }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(fetchMock.mock.calls.filter(call => String(call[0]) === '/api/tasks')).toHaveLength(0)
  })

  it('does not expose stored subject text for non-current task history rows', () => {
    render(<I18nProvider><Tasks repository={repository} jobs={[{ id: 8, uuid: '550e8400-e29b-41d4-a716-446655440000', job_type: 'task', git_ref: 'run', status: 'done', subject: 'stored RoboRev prompt' }]} onOpenReview={vi.fn()} /></I18nProvider>)
    expect(screen.getAllByText('run').length).toBeGreaterThan(0)
    expect(screen.queryByText('stored RoboRev prompt')).not.toBeInTheDocument()
  })

  it('shows skipped receipt without fabricating a task history row', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      if (String(input).startsWith('/api/tasks/preview?')) return response({ ...preview, command: { ...preview.command, stdin: task.replace(/\r\n/g, '\n') } })
      if (String(input) === '/api/tasks') return response({ skipped: true, reason: 'duplicate task' })
      return response([])
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<I18nProvider><Tasks repository={repository} jobs={[]} onOpenReview={vi.fn()} /></I18nProvider>)
    fireEvent.change(screen.getByLabelText('Task'), { target: { value: task } })
    fireEvent.click(screen.getByRole('button', { name: 'Prepare task' }))
    await screen.findByRole('dialog')
    fireEvent.click(screen.getByRole('button', { name: 'Run task' }))
    expect(await screen.findByText(/Task skipped: duplicate task/)).toBeInTheDocument()
    expect(screen.queryByText('#42')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'View result' })).not.toBeInTheDocument()
  })

  it('reconciles queued, running, and done task jobs and opens their result', async () => {
    const job = { id: 51, uuid: '550e8400-e29b-41d4-a716-446655440051', job_type: 'task', git_ref: 'run', status: 'queued' }
    const onOpenReview = vi.fn()
    const view = render(<I18nProvider><Tasks repository={repository} jobs={[job]} onOpenReview={onOpenReview} /></I18nProvider>)
    expect(await screen.findByText('Queued')).toBeInTheDocument()
    view.rerender(<I18nProvider><Tasks repository={repository} jobs={[{ ...job, status: 'running' }]} onOpenReview={onOpenReview} /></I18nProvider>)
    expect(await screen.findByText('Running')).toBeInTheDocument()
    view.rerender(<I18nProvider><Tasks repository={repository} jobs={[{ ...job, status: 'done' }]} onOpenReview={onOpenReview} /></I18nProvider>)
    expect(await screen.findByText('Done')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'View result' }))
    expect(onOpenReview).toHaveBeenCalledWith(51)
  })

  it('switches locale without refetching preview or executing a task', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      if (String(input).startsWith('/api/tasks/preview?')) return response({ ...preview, command: { ...preview.command, stdin: task.replace(/\r\n/g, '\n') } })
      return response([])
    })
    vi.stubGlobal('fetch', fetchMock)
    render(<I18nProvider><LanguageSelector /><Tasks repository={repository} jobs={[]} onOpenReview={vi.fn()} /></I18nProvider>)
    fireEvent.change(screen.getByRole('combobox', { name: 'Select language' }), { target: { value: 'ko' } })
    expect(fetchMock).not.toHaveBeenCalled()
    fireEvent.change(screen.getByLabelText('Task'), { target: { value: task } })
    fireEvent.click(screen.getByRole('button', { name: 'Task 실행 준비' }))
    await screen.findByRole('dialog')
    const callCount = fetchMock.mock.calls.length
    fireEvent.change(screen.getByRole('combobox', { name: '언어 선택' }), { target: { value: 'en' } })
    await waitFor(() => expect(fetchMock.mock.calls).toHaveLength(callCount))
    expect(fetchMock.mock.calls.some(call => String(call[0]) === '/api/tasks')).toBe(false)
  })
})
