import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider, LanguageSelector } from './i18n'
import { Tasks } from './Tasks'

const repository = { identity: 'repo-1', name: 'repo', path: 'E:\\repo', branch: 'main', head: 'abcdef1234567890', headShort: 'abcdef12', commitSubject: 'Initial', dirty: false, hasConfig: true }

function response(value: unknown, status = 200) { return Promise.resolve(new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } })) }

afterEach(() => { cleanup(); vi.restoreAllMocks() })

const historyJobs = [
  { id: 51, uuid: '550e8400-e29b-41d4-a716-446655440051', job_type: 'task', git_ref: 'run', status: 'queued', repo_path: 'E:\\repo' },
  { id: 50, uuid: '550e8400-e29b-41d4-a716-446655440050', job_type: 'task', git_ref: 'run', status: 'running', repo_path: 'E:\\repo' },
  { id: 49, uuid: '550e8400-e29b-41d4-a716-446655440049', job_type: 'task', git_ref: 'run', status: 'done', repo_path: 'E:\\repo' },
]

describe('Tasks workspace with disabled execution', () => {
  it('shows existing task history with live statuses and opens results without any network call', () => {
    const onOpenReview = vi.fn()
    const view = render(<I18nProvider><Tasks repository={repository} jobs={historyJobs} onOpenReview={onOpenReview} /></I18nProvider>)
    expect(screen.getByText('Queued')).toBeInTheDocument()
    expect(screen.getByText('Running')).toBeInTheDocument()
    expect(screen.getByText('Done')).toBeInTheDocument()
    const viewButtons = screen.getAllByRole('button', { name: 'View result' })
    expect(viewButtons).toHaveLength(historyJobs.length)
    fireEvent.click(viewButtons[0])
    expect(onOpenReview).toHaveBeenCalledWith(51)
    view.rerender(<I18nProvider><Tasks repository={repository} jobs={[historyJobs[2]]} onOpenReview={onOpenReview} /></I18nProvider>)
    fireEvent.click(screen.getByRole('button', { name: 'View result' }))
    expect(onOpenReview).toHaveBeenLastCalledWith(49)
  })

  it('renders backend-scoped history rows regardless of daemon path spelling', () => {
    // The daemon reports repo_path with forward slashes while the registry
    // uses Windows backslashes; the browser must not re-compare raw paths.
    render(<I18nProvider><Tasks repository={repository} jobs={[{ id: 51, uuid: '550e8400-e29b-41d4-a716-446655440051', job_type: 'task', git_ref: 'run', status: 'done', repo_path: 'E:/repo' }]} onOpenReview={vi.fn()} /></I18nProvider>)
    expect(screen.getByText('#51')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'View result' })).toBeInTheDocument()
  })

  it('never guesses a missing agent as codex and preserves recorded agents verbatim', () => {
    const { rerender } = render(<I18nProvider><Tasks repository={repository} jobs={[{ id: 51, uuid: '550e8400-e29b-41d4-a716-446655440051', job_type: 'task', git_ref: 'run', status: 'done' }]} onOpenReview={vi.fn()} /></I18nProvider>)
    expect(screen.getByText('Unknown agent')).toBeInTheDocument()
    rerender(<I18nProvider><Tasks repository={repository} jobs={[{ id: 51, uuid: '550e8400-e29b-41d4-a716-446655440051', job_type: 'task', git_ref: 'run', status: 'done', agent: 'claude-code' }]} onOpenReview={vi.fn()} /></I18nProvider>)
    expect(screen.getByText('claude-code')).toBeInTheDocument()
    expect(screen.queryByText('codex')).not.toBeInTheDocument()
  })

  it('renders an inspection-only composer with the fail-closed explanation and never requests execution', () => {
    const fetchMock = vi.fn(() => response([]))
    vi.stubGlobal('fetch', fetchMock)
    render(<I18nProvider><Tasks repository={repository} jobs={[]} onOpenReview={vi.fn()} /></I18nProvider>)
    expect(screen.getByRole('heading', { name: 'Custom Task' })).toBeInTheDocument()
    expect(screen.getByLabelText('Task prompt')).toBeDisabled()
    expect(screen.getAllByText('Execution disabled').length).toBeGreaterThanOrEqual(1)
    expect(screen.getByText(/machine-readable contract/i)).toBeInTheDocument()
    // No execution CTA exists at all, and no read-only guarantee badge remains.
    expect(screen.queryByRole('button', { name: /run task|prepare task|execute/i })).not.toBeInTheDocument()
    expect(screen.queryByText('Read-only')).not.toBeInTheDocument()
    expect(document.querySelector('.task-safety-copy')?.textContent).not.toMatch(/guarantee|보장/)
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('explains exactly which daemon safety state cannot be attested in English and Korean', () => {
    const { rerender } = render(<I18nProvider><LanguageSelector /><Tasks repository={repository} jobs={[]} onOpenReview={vi.fn()} /></I18nProvider>)
    expect(screen.getByText(/allow_unsafe_agents \/ disable_codex_sandbox state or global config identity/)).toBeInTheDocument()
    fireEvent.change(screen.getByRole('combobox', { name: 'Select language' }), { target: { value: 'ko' } })
    expect(screen.getByText(/effective allow_unsafe_agents \/ disable_codex_sandbox 상태나 global config identity를 machine-readable 계약으로 제공하지 않/)).toBeInTheDocument()
    expect(screen.getAllByText('실행 비활성화').length).toBeGreaterThanOrEqual(1)
    rerender(<I18nProvider><LanguageSelector /><Tasks repository={repository} jobs={[historyJobs[2]]} onOpenReview={vi.fn()} /></I18nProvider>)
    expect(screen.getByRole('button', { name: '결과 보기' })).toBeInTheDocument()
  })
})
