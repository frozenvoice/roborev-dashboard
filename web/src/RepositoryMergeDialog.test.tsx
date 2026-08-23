import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { RepositoryMergeDialog } from './RepositoryMergeDialog'
import type { RepositoryMergeCandidate, RepositoryMergeSnapshot } from './api'
import { I18nProvider } from './i18n'

const source: RepositoryMergeSnapshot = { path: 'E:/old/project', name: 'old-project', jobCount: 7 }
const targets: RepositoryMergeCandidate[] = [
  { repositoryIdentity: 'target-a', path: 'E:\\new\\project', name: 'new-project', jobCount: 3 },
  { repositoryIdentity: 'target-b', path: 'E:/old/project', name: 'same-looking-path', jobCount: 4 },
]
const preview = {
  source: { path: 'E:/fresh/source', name: 'fresh-source', jobCount: 8 },
  target: { repositoryIdentity: 'target-b', path: 'E:\\fresh\\target', name: 'fresh-target', jobCount: 4 },
  command: { executable: 'roborev.exe', args: ['repo', 'merge', '--yes', 'E:/fresh/source', 'E:\\fresh\\target'], workingDirectory: '' },
  token: 'merge-token', expiresAt: '2026-08-23T01:02:03Z',
}

function response(value: unknown, status = 200) {
  return Promise.resolve(new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } }))
}

function renderDialog(overrides: Partial<React.ComponentProps<typeof RepositoryMergeDialog>> = {}) {
  return render(<I18nProvider><RepositoryMergeDialog source={source} targets={targets} availability={{ canMerge: true, reason: null }} onClose={vi.fn()} onSuccess={vi.fn()} onRefresh={vi.fn()} {...overrides} /></I18nProvider>)
}

beforeEach(() => { window.localStorage.setItem('roborev-dashboard-language', 'en') })
afterEach(() => { cleanup(); vi.restoreAllMocks() })

describe('RepositoryMergeDialog', () => {
  it('keeps every server candidate selectable and sends exact preview fields', async () => {
    const calls: Array<{ url: string; init?: RequestInit }> = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => { calls.push({ url: String(input), init }); return response(preview) }))
    renderDialog()
    const select = screen.getByRole('combobox', { name: 'Registered target' })
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
    expect(screen.getByRole('option', { name: /new-project/ })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: /same-looking-path/ })).toBeInTheDocument()
    expect(select.querySelectorAll('option')).toHaveLength(3)
    expect(screen.getByText('old-project')).toBeInTheDocument()
    expect(screen.getByText('E:/old/project')).toBeInTheDocument()
    expect(screen.getByText('7')).toBeInTheDocument()
    await userEvent.selectOptions(select, 'target-b')
    await userEvent.click(screen.getByRole('button', { name: 'Prepare merge' }))
    await screen.findByRole('button', { name: 'Confirm merge' })
    expect(calls).toHaveLength(1)
    expect(calls[0].url).toBe('/api/repository-merges/preview')
    expect(JSON.parse(String(calls[0].init?.body))).toEqual({ sourcePath: source.path, targetRepositoryIdentity: 'target-b' })
    expect(screen.getByText('fresh-source')).toBeInTheDocument()
    expect(screen.getByText('fresh-target')).toBeInTheDocument()
    const sourceFacts = document.querySelector('.merge-facts')
    const targetFacts = document.querySelector('.merge-preview-facts')
    expect(sourceFacts).toHaveTextContent('fresh-source')
    expect(sourceFacts).toHaveTextContent('E:/fresh/source')
    expect(sourceFacts).toHaveTextContent('8')
    expect(sourceFacts).not.toHaveTextContent('old-project')
    expect(sourceFacts).not.toHaveTextContent('E:/old/project')
    expect(sourceFacts).not.toHaveTextContent('7')
    expect(targetFacts).toHaveTextContent('fresh-target')
    expect(targetFacts).toHaveTextContent('E:\\fresh\\target')
    expect(targetFacts).toHaveTextContent('4')
    expect(targetFacts).not.toHaveTextContent('same-looking-path')
    const command = document.querySelector('.merge-command-preview')
    expect(command).toHaveTextContent('roborev.exe')
    expect(command).toHaveTextContent('repo')
    expect(command).toHaveTextContent('merge')
    expect(command).toHaveTextContent('--yes')
    expect(command).toHaveTextContent('E:/fresh/source')
    expect(command).toHaveTextContent('E:\\fresh\\target')
    expect(command).toHaveTextContent('Working directory: None')
  })

  it('confirms exactly once with the token only, including a double click', async () => {
    const calls: Array<{ url: string; init?: RequestInit }> = []
    let resolveConfirm!: (value: Response) => void
    const pending = new Promise<Response>(resolve => { resolveConfirm = resolve })
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ url: String(input), init })
      return String(input).endsWith('/preview') ? response(preview) : pending
    }))
    renderDialog()
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Registered target' }), 'target-a')
    await userEvent.click(screen.getByRole('button', { name: 'Prepare merge' }))
    const confirm = await screen.findByRole('button', { name: 'Confirm merge' })
    fireEvent.click(confirm)
    fireEvent.click(confirm)
    await waitFor(() => expect(calls.filter(call => call.url === '/api/repository-merges')).toHaveLength(1))
    expect(JSON.parse(String(calls[1].init?.body))).toEqual({ token: 'merge-token' })
    expect(screen.getByRole('button', { name: 'Merging…' })).toBeDisabled()
    resolveConfirm(await response({ status: 'merged' }))
  })

  it('preserves ready confirm when availability is lost and gates editing by reason precedence', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => String(input).endsWith('/preview') ? response(preview) : response({ status: 'merged' })))
    const view = renderDialog()
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Registered target' }), 'target-a')
    await userEvent.click(screen.getByRole('button', { name: 'Prepare merge' }))
    expect(await screen.findByRole('button', { name: 'Confirm merge' })).toBeEnabled()
    const unavailable = { canMerge: false, reason: 'runtime-unavailable' as const }
    const unsupported = { canMerge: false, reason: 'unsupported' as const }
    view.rerender(<I18nProvider><RepositoryMergeDialog source={source} targets={targets} availability={unavailable} onClose={vi.fn()} onSuccess={vi.fn()} onRefresh={vi.fn()} /></I18nProvider>)
    expect(screen.getByRole('button', { name: 'Confirm merge' })).toBeEnabled()
    expect(screen.queryByText(/RoboRev is currently unavailable/)).not.toBeInTheDocument()
    view.rerender(<I18nProvider><RepositoryMergeDialog source={source} targets={targets} availability={unsupported} onClose={vi.fn()} onSuccess={vi.fn()} onRefresh={vi.fn()} /></I18nProvider>)
    expect(screen.getByRole('button', { name: 'Confirm merge' })).toBeEnabled()
    expect(screen.queryByText(/does not support repository merge/)).not.toBeInTheDocument()

    cleanup()
    renderDialog({ availability: unavailable })
    expect(screen.getByRole('button', { name: 'Prepare merge' })).toBeDisabled()
    expect(screen.getByText(/RoboRev is currently unavailable/)).toBeInTheDocument()
    cleanup()
    renderDialog({ availability: unsupported })
    expect(screen.getByRole('button', { name: 'Prepare merge' })).toBeDisabled()
    expect(screen.getByText(/does not support repository merge/)).toBeInTheDocument()
  })

  it('shows backend preview errors together with the unavailable hint and does not retry', async () => {
    const calls: string[] = []
    let resolvePreview!: (value: Response) => void
    const pendingPreview = new Promise<Response>(resolve => { resolvePreview = resolve })
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => { calls.push(String(input)); return pendingPreview }))
    const view = renderDialog()
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Registered target' }), 'target-a')
    await userEvent.click(screen.getByRole('button', { name: 'Prepare merge' }))
    view.rerender(<I18nProvider><RepositoryMergeDialog source={source} targets={targets} availability={{ canMerge: false, reason: 'runtime-unavailable' }} onClose={vi.fn()} onSuccess={vi.fn()} onRefresh={vi.fn()} /></I18nProvider>)
    resolvePreview(await response({ error: 'merge conflict' }, 503))
    expect(await screen.findByRole('alert')).toHaveTextContent('merge conflict')
    expect(screen.getByText(/RoboRev is currently unavailable/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Prepare merge' })).toBeDisabled()
    expect(calls.filter(url => url.endsWith('/preview'))).toHaveLength(1)
    await new Promise(resolve => setTimeout(resolve, 20))
    expect(calls.filter(url => url.endsWith('/preview'))).toHaveLength(1)
  })

  it('does not preview or confirm when canceled and performs no automatic retry', async () => {
    const calls: string[] = []
    const onClose = vi.fn()
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => { calls.push(String(input)); return response(preview) }))
    renderDialog({ onClose })
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Registered target' }), 'target-a')
    expect(calls).toHaveLength(0)
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(onClose).toHaveBeenCalledOnce()
    expect(calls).toHaveLength(0)
  })

  it('aborts an in-flight Preview on unmount', async () => {
    let signal: AbortSignal | undefined
    vi.stubGlobal('fetch', vi.fn((_input: RequestInfo | URL, init?: RequestInit) => { signal = init?.signal ?? undefined; return new Promise<Response>(() => {}) }))
    const view = renderDialog()
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Registered target' }), 'target-a')
    await userEvent.click(screen.getByRole('button', { name: 'Prepare merge' }))
    await waitFor(() => expect(signal).toBeDefined())
    expect(signal?.aborted).toBe(false)
    view.unmount()
    expect(signal?.aborted).toBe(true)
  })

  it('does not abort an accepted Confirm on availability change, but aborts it on unmount', async () => {
    let confirmSignal: AbortSignal | undefined
    const pendingConfirm = new Promise<Response>(() => {})
    const onSuccess = vi.fn()
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).endsWith('/preview')) return response(preview)
      confirmSignal = init?.signal ?? undefined
      return pendingConfirm
    }))
    const view = renderDialog({ onSuccess })
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Registered target' }), 'target-a')
    await userEvent.click(screen.getByRole('button', { name: 'Prepare merge' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Confirm merge' }))
    await waitFor(() => expect(confirmSignal).toBeDefined())
    view.rerender(<I18nProvider><RepositoryMergeDialog source={source} targets={targets} availability={{ canMerge: false, reason: 'runtime-unavailable' }} onClose={vi.fn()} onSuccess={onSuccess} onRefresh={vi.fn()} /></I18nProvider>)
    expect(confirmSignal?.aborted).toBe(false)
    expect(screen.getByRole('button', { name: 'Merging…' })).toBeDisabled()
    view.unmount()
    expect(confirmSignal?.aborted).toBe(true)
  })

  it('shows unverified as terminal and offers only close and refresh without retry', async () => {
    const onClose = vi.fn()
    const onRefresh = vi.fn()
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => { calls.push(String(input)); return String(input).endsWith('/preview') ? response(preview) : response({ status: 'unverified', message: 'Inventory could not prove merge' }) }))
    renderDialog({ onClose, onRefresh })
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Registered target' }), 'target-a')
    await userEvent.click(screen.getByRole('button', { name: 'Prepare merge' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Confirm merge' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Inventory could not prove merge')
    expect(screen.getByRole('alert')).toHaveTextContent('The merge process started, but fresh RoboRev inventory could not verify the consolidated state.')
    expect(screen.getByRole('button', { name: 'Close' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Confirm merge' })).not.toBeInTheDocument()
    expect(calls.filter(url => url === '/api/repository-merges')).toHaveLength(1)
    await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    expect(onClose).toHaveBeenCalledOnce()
    expect(onRefresh).toHaveBeenCalledOnce()
    expect(calls.filter(url => url === '/api/repository-merges')).toHaveLength(1)
  })

  it('uses the localized unverified terminal copy in Korean while keeping Close and Refresh only', async () => {
    window.localStorage.setItem('roborev-dashboard-language', 'ko')
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => String(input).endsWith('/preview') ? response(preview) : response({ status: 'unverified', message: 'server detail' })))
    renderDialog()
    await userEvent.selectOptions(screen.getByRole('combobox', { name: '등록된 target' }), 'target-a')
    await userEvent.click(screen.getByRole('button', { name: '병합 준비' }))
    await userEvent.click(await screen.findByRole('button', { name: '병합 확인' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('병합 프로세스는 시작했지만 새 RoboRev inventory에서 통합 상태를 확인하지 못했습니다.')
    expect(alert).toHaveTextContent('server detail')
    expect(screen.getByRole('button', { name: '닫기' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '새로고침' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '병합 확인' })).not.toBeInTheDocument()
  })
})
