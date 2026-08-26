import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { CIWorkflowPreview } from './CIWorkflowPreview'
import { I18nProvider, LanguageSelector, LANGUAGE_STORAGE_KEY } from './i18n'
import { StrictMode } from 'react'
import { Setup } from './Setup'

const content = 'name: RoboRev\non:\n  pull_request:\n    branches: [main]\n\n# ${{ secrets.OPENAI_API_KEY }}\n'
const response = { version: 'v0.64.0', repositoryIdentity: 'repo-1', target: '.github/workflows/roborev.yml', workflow: { content, sha256: 'a'.repeat(64) } }

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
    resolve(new Response(JSON.stringify(response), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await waitFor(() => expect(screen.queryByRole('code')).not.toBeInTheDocument())
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('keeps preview through locale changes without another request', async () => {
    vi.stubGlobal('fetch', vi.fn().mockImplementation(() => jsonResponse(response)))
    render(<I18nProvider><LanguageSelector /><CIWorkflowPreview repositoryIdentity="repo-1" /></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Generate Preview' }))
    await waitFor(() => expect(document.querySelector('.ci-workflow-preview-yaml code')).toBeInTheDocument())
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Select language' }), 'ko')
    expect(document.querySelector('.ci-workflow-preview-yaml code')?.textContent).toBe(content)
    expect(fetch).toHaveBeenCalledTimes(1)
  })

  it('shows only a generic localized error when the server response is malformed', async () => {
    const payload = { ...response, privatePath: 'C:\\secret', stdout: 'binary path', stderr: 'secret-value', workflow: { ...response.workflow, secretValue: 'OPENAI_REAL_SECRET' } }
    vi.stubGlobal('fetch', vi.fn().mockImplementation(() => jsonResponse(payload)))
    renderPreview()
    await userEvent.click(screen.getByRole('button', { name: 'Generate Preview' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('The CI workflow preview could not be prepared.')
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
})
