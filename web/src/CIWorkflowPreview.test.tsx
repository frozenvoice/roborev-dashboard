import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { CIWorkflowPreview } from './CIWorkflowPreview'
import { I18nProvider, LanguageSelector, LANGUAGE_STORAGE_KEY } from './i18n'
import { StrictMode } from 'react'
import { Setup } from './Setup'

const content = 'name: RoboRev\non:\n  pull_request:\n    branches: [main]\n\n# ${{ secrets.OPENAI_API_KEY }}\n'
const response = { version: 'v0.64.0', repositoryIdentity: 'repo-1', target: '.github/workflows/roborev.yml', workflow: { content, sha256: 'a'.repeat(64) }, write: { status: 'available' as const, token: 'opaque-create-token', expiresAt: '2099-01-01T00:00:00.000Z' } }

function jsonResponse(value: unknown, status = 200) {
  return Promise.resolve(new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } }))
}

function renderPreview(identity = 'repo-1') {
  return render(<I18nProvider><CIWorkflowPreview repositoryIdentity={identity} /></I18nProvider>)
}

afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); window.localStorage.removeItem(LANGUAGE_STORAGE_KEY) })

describe('CIWorkflowPreview', () => {
  it('does not request on initial render and sends one exact POST after Generate', async () => {
    const calls: Array<{ path: string; init?: RequestInit }> = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => { calls.push({ path: String(input), init }); return jsonResponse(response) }))
    renderPreview()
    expect(calls).toHaveLength(0)
    await userEvent.click(screen.getByRole('button', { name: 'Generate Preview' }))
    await screen.findByText('.github/workflows/roborev.yml')
    expect(calls).toHaveLength(1)
    expect(calls[0].path).toBe('/api/ci-workflow/preview')
    expect(calls[0].init?.method).toBe('POST')
    expect(JSON.parse(String(calls[0].init?.body))).toEqual({ repositoryIdentity: 'repo-1' })
    expect(JSON.parse(String(calls[0].init?.body))).not.toHaveProperty('path')
    expect(JSON.parse(String(calls[0].init?.body))).not.toHaveProperty('version')
  })

  it('is inserted directly after Git Hooks in Setup without an automatic preview request', () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => { calls.push(String(input)); return jsonResponse({}) }))
    const { container } = render(<I18nProvider><Setup state={null} repositoryIdentity="repo-1" /></I18nProvider>)
    const order = Array.from(container.querySelectorAll('.git-hook-status,.ci-workflow-preview')).map(element => element.className)
    expect(order).toEqual(['git-hook-status', 'ci-workflow-preview'])
    expect(calls.filter(path => path === '/api/ci-workflow/preview')).toHaveLength(0)
  })

  it('guards double-clicks while one preview is in flight', async () => {
    let resolve!: (value: Response) => void
    const pending = new Promise<Response>(done => { resolve = done })
    const fetchMock = vi.fn().mockReturnValue(pending)
    vi.stubGlobal('fetch', fetchMock)
    renderPreview()
    const generate = screen.getByRole('button', { name: 'Generate Preview' })
    fireEvent.click(generate)
    fireEvent.click(generate)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    resolve(new Response(JSON.stringify(response), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await screen.findByText('.github/workflows/roborev.yml')
  })

  it('remains requestable through React StrictMode effect replay', async () => {
    const fetchMock = vi.fn().mockImplementation(() => jsonResponse(response))
    vi.stubGlobal('fetch', fetchMock)
    render(<StrictMode><I18nProvider><CIWorkflowPreview repositoryIdentity="repo-1" /></I18nProvider></StrictMode>)
    await userEvent.click(screen.getByRole('button', { name: 'Generate Preview' }))
    await screen.findByText('.github/workflows/roborev.yml')
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('renders generated YAML as escaped plain text and exposes only Copy', async () => {
    vi.stubGlobal('fetch', vi.fn().mockImplementation(() => jsonResponse(response)))
    renderPreview()
    await userEvent.click(screen.getByRole('button', { name: 'Generate Preview' }))
    const yaml = await waitFor(() => {
      const element = document.querySelector('.ci-workflow-preview-yaml code')
      if (!element) throw new Error('workflow not rendered')
      return element
    })
    expect(yaml.textContent).toBe(content)
    expect(yaml.innerHTML).not.toContain('<script')
    expect(document.querySelector('.ci-workflow-preview-yaml')?.textContent).toContain('${{ secrets.OPENAI_API_KEY }}')
    expect(screen.getByText('.github/workflows/roborev.yml')).toBeInTheDocument()
    expect(screen.getByText('v0.64.0')).toBeInTheDocument()
    expect(screen.getByText('Only RoboRev itself is pinned to v0.64.0; agent CLIs may use @latest packages or external installers. Review the YAML before committing it.')).toBeInTheDocument()
    expect(document.body.textContent).not.toContain('C:\\temp')
    expect(document.body.textContent).not.toContain('roborev.exe')
    expect(document.body.textContent).not.toContain('stdout')
    expect(document.body.textContent).not.toContain('stderr')
    expect(document.body.textContent).not.toContain('SUPER_PRIVATE_OPENAI_VALUE')
    const buttons = screen.getAllByRole('button').map(button => button.textContent?.trim())
    expect(buttons).toContain('Copy')
    expect(buttons.some(label => /save|apply|install|write|overwrite|force|commit|push|open github/i.test(label || ''))).toBe(false)
    await new Promise(resolve => setTimeout(resolve, 20))
    expect(fetch).toHaveBeenCalledTimes(1)
  })

  it('copies the exact content on success and preserves it on clipboard failure', async () => {
    vi.stubGlobal('fetch', vi.fn().mockImplementation(() => jsonResponse(response)))
    const clipboard = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue(undefined)
    renderPreview()
    await userEvent.click(screen.getByRole('button', { name: 'Generate Preview' }))
    await userEvent.click(screen.getByRole('button', { name: 'Copy' }))
    expect(clipboard).toHaveBeenCalledWith(content)
    expect(screen.getByRole('button', { name: /Copied/ })).toBeInTheDocument()
    clipboard.mockRejectedValueOnce(new Error('denied'))
    // The success state is intentionally terminal for this exact preview, so
    // remount to exercise a rejected clipboard call without a second request.
    cleanup()
    renderPreview()
    await userEvent.click(screen.getByRole('button', { name: 'Generate Preview' }))
    await userEvent.click(screen.getByRole('button', { name: 'Copy' }))
    expect(screen.getByText('The workflow could not be copied because clipboard access was unavailable.')).toBeInTheDocument()
    expect(document.querySelector('.ci-workflow-preview-yaml code')?.textContent).toBe(content)
  })

  it('discards a late response after switching repositories and performs no automatic request', async () => {
    let resolve!: (value: Response) => void
    const pending = new Promise<Response>(done => { resolve = done })
    const fetchMock = vi.fn().mockReturnValue(pending)
    vi.stubGlobal('fetch', fetchMock)
    const { rerender } = renderPreview('repo-1')
    await userEvent.click(screen.getByRole('button', { name: 'Generate Preview' }))
    rerender(<I18nProvider><CIWorkflowPreview repositoryIdentity="repo-2" /></I18nProvider>)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(screen.queryByRole('code')).not.toBeInTheDocument()
    resolve(new Response(JSON.stringify({ ...response, write: { status: 'exists' }, existing: { comparison: 'different' } }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await waitFor(() => expect(screen.queryByRole('code')).not.toBeInTheDocument())
    expect(screen.queryByText('Existing workflow differs byte-for-byte from this generated preview.')).not.toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('switches the localized comparison from EN to KO without another request', async () => {
    vi.stubGlobal('fetch', vi.fn().mockImplementation(() => jsonResponse({ ...response, write: { status: 'exists' }, existing: { comparison: 'different' } })))
    render(<I18nProvider><LanguageSelector /><CIWorkflowPreview repositoryIdentity="repo-1" /></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Generate Preview' }))
    expect(await screen.findByText('Existing workflow differs byte-for-byte from this generated preview.')).toBeInTheDocument()
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Select language' }), 'ko')
    expect(await screen.findByText('기존 워크플로가 이번에 생성된 프리뷰와 바이트 단위로 다릅니다.')).toBeInTheDocument()
    expect(document.querySelector('.ci-workflow-preview-yaml code')?.textContent).toBe(content)
    expect(fetch).toHaveBeenCalledTimes(1)
  })

  it('shows only a generic localized error when the server response is malformed', async () => {
    const payload = { ...response, write: { status: 'exists' }, existing: { comparison: 'different', content: 'EXISTING_PRIVATE_WORKFLOW_MARKER_DO_NOT_EXPOSE' }, privatePath: 'C:\\secret', stdout: 'binary path', stderr: 'secret-value', workflow: { ...response.workflow, secretValue: 'OPENAI_REAL_SECRET' } }
    vi.stubGlobal('fetch', vi.fn().mockImplementation(() => jsonResponse(payload)))
    renderPreview()
    await userEvent.click(screen.getByRole('button', { name: 'Generate Preview' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('The CI workflow preview could not be prepared.')
    expect(document.body.textContent).not.toContain('EXISTING_PRIVATE_WORKFLOW_MARKER_DO_NOT_EXPOSE')
    expect(document.body.textContent).not.toContain('C:\\secret')
    expect(document.body.textContent).not.toContain('binary path')
    expect(document.body.textContent).not.toContain('secret-value')
    expect(document.body.textContent).not.toContain('OPENAI_REAL_SECRET')
  })

  it('does not update after unmounting while the request is pending', async () => {
    let resolve!: (value: Response) => void
    const pending = new Promise<Response>(done => { resolve = done })
    vi.stubGlobal('fetch', vi.fn().mockReturnValue(pending))
    const { unmount } = renderPreview()
    await userEvent.click(screen.getByRole('button', { name: 'Generate Preview' }))
    unmount()
    resolve(new Response(JSON.stringify(response), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await pending
    expect(document.body.textContent).not.toContain(content)
  })

  it('shows create-only eligibility messages and no create CTA for blocked states', async () => {
    for (const status of ['exists', 'unsafe', 'ignored', 'dirty'] as const) {
      vi.stubGlobal('fetch', vi.fn().mockImplementation(() => jsonResponse({ ...response, write: { status }, ...(status === 'exists' ? { existing: { comparison: 'exact-match' } } : {}) })))
      renderPreview()
      await userEvent.click(screen.getByRole('button', { name: 'Generate Preview' }))
      await screen.findByText('.github/workflows/roborev.yml')
      expect(screen.queryByRole('button', { name: 'Create Workflow' })).not.toBeInTheDocument()
      expect(screen.queryByText(/overwrite is not supported|ignored by Git|must be clean|target is unavailable/i)).toBeInTheDocument()
      cleanup()
      vi.restoreAllMocks()
    }
  })

  it('renders each neutral existing-workflow byte comparison without mutation CTAs', async () => {
    const cases = [
      ['exact-match', 'Existing workflow exactly matches this generated preview.'],
      ['different', 'Existing workflow differs byte-for-byte from this generated preview.'],
      ['unavailable', 'The existing workflow could not be compared safely.'],
    ] as const
    for (const [comparison, message] of cases) {
      const clipboard = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue(undefined)
      vi.stubGlobal('fetch', vi.fn().mockImplementation(() => jsonResponse({ ...response, write: { status: 'exists' }, existing: { comparison } })))
      renderPreview()
      await userEvent.click(screen.getByRole('button', { name: 'Generate Preview' }))
      expect(await screen.findByText(message)).toBeInTheDocument()
      expect(screen.queryByRole('button', { name: /Create Workflow|Overwrite|Update|Replace|Show Diff|View existing|Sync|Reset/i })).not.toBeInTheDocument()
      expect(document.querySelector('.ci-workflow-preview-yaml code')?.textContent).toBe(content)
      await userEvent.click(screen.getByRole('button', { name: 'Copy' }))
      expect(clipboard).toHaveBeenCalledWith(content)
      expect(screen.getByRole('button', { name: /Copied/ })).toBeInTheDocument()
      cleanup()
      vi.restoreAllMocks()
    }
  })

  it('keeps the comparison localized without another request', async () => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, 'ko')
    vi.stubGlobal('fetch', vi.fn().mockImplementation(() => jsonResponse({ ...response, write: { status: 'exists' }, existing: { comparison: 'different' } })))
    renderPreview()
    await userEvent.click(screen.getByRole('button', { name: '미리보기 생성' }))
    expect(await screen.findByText('기존 워크플로가 이번에 생성된 프리뷰와 바이트 단위로 다릅니다.')).toBeInTheDocument()
    expect(fetch).toHaveBeenCalledTimes(1)
  })

  it('opens confirmation without network, cancels without execute, and posts only the token once', async () => {
    const calls: Array<{ path: string; init?: RequestInit }> = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ path: String(input), init })
      return jsonResponse(String(input).endsWith('/execute') ? { status: 'verified-created' } : response)
    }))
    renderPreview()
    await userEvent.click(screen.getByRole('button', { name: 'Generate Preview' }))
    await screen.findByRole('button', { name: 'Create Workflow' })
    await userEvent.click(screen.getByRole('button', { name: 'Create Workflow' }))
    expect(calls).toHaveLength(1)
    expect(screen.getByRole('dialog')).toHaveTextContent('The exact YAML shown in this preview will be created in the selected repository.')
    expect(screen.getByRole('dialog')).toHaveTextContent('Existing files are never overwritten.')
    expect(screen.getByRole('dialog')).toHaveTextContent('The repository is not committed or pushed automatically.')
    expect(screen.getByRole('dialog')).toHaveTextContent('Only RoboRev itself is pinned to v0.64.0')
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(calls).toHaveLength(1)
    await userEvent.click(screen.getByRole('button', { name: 'Create Workflow' }))
    const confirm = screen.getByRole('dialog').querySelector('footer .button.primary') as HTMLButtonElement
    fireEvent.click(confirm)
    fireEvent.click(confirm)
    await screen.findByText('Workflow created')
    expect(calls).toHaveLength(2)
    expect(calls[1].path).toBe('/api/ci-workflow/execute')
    expect(JSON.parse(String(calls[1].init?.body))).toEqual({ token: 'opaque-create-token' })
    expect(screen.queryByRole('button', { name: 'Create Workflow' })).not.toBeInTheDocument()
    expect(screen.getAllByText('.github/workflows/roborev.yml').length).toBeGreaterThan(0)
    expect(document.body.textContent).not.toContain('C:\\Users\\private')
  })

  it('supports Escape cancellation and renders a generic failed-unverified result without retry', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      calls.push(String(input))
      return jsonResponse(String(input).endsWith('/execute') ? { status: 'failed-unverified' } : response)
    }))
    renderPreview()
    await userEvent.click(screen.getByRole('button', { name: 'Generate Preview' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Create Workflow' }))
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' })
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(calls).toHaveLength(1)
    await userEvent.click(screen.getByRole('button', { name: 'Create Workflow' }))
    await userEvent.click(screen.getByRole('dialog').querySelector('footer .button.primary') as HTMLButtonElement)
    expect(await screen.findByRole('alert')).toHaveTextContent('Workflow creation could not be verified. Inspect the repository before retrying.')
    expect(calls).toHaveLength(2)
    expect(screen.queryByRole('button', { name: 'Create Workflow' })).not.toBeInTheDocument()
  })
})
