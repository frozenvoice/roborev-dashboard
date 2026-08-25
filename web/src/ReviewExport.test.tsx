import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from './i18n'
import { ReviewExport } from './ReviewExport'

const repository = { repositoryIdentity: 'opaque repo/한글', repositoryName: '非常に長い 저장소 이름 / repository' }

function exportResponse(status = 200, headers: Record<string, string> = {}, body = '{"reviews":[]}') {
  return new Response(body, {
    status,
    headers: {
      'Content-Type': 'application/json; charset=utf-8',
      'Content-Disposition': 'attachment; filename="roborev-reviews.json"',
      'X-RoboRev-Export-Truncated': 'false',
      ...headers,
    },
  })
}

function renderExport() {
  return render(<I18nProvider><ReviewExport repositoryIdentity={repository.repositoryIdentity} repositoryName={repository.repositoryName} /></I18nProvider>)
}

describe('ReviewExport', () => {
  let fetchMock: ReturnType<typeof vi.fn>
  let createObjectURL: ReturnType<typeof vi.fn>
  let revokeObjectURL: ReturnType<typeof vi.fn>
  let clickMock!: { mock: { calls: unknown[][] } }
  let downloadedNames: string[]

  beforeEach(() => {
    fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    createObjectURL = vi.fn(() => 'blob:review-export')
    revokeObjectURL = vi.fn()
    downloadedNames = []
    Object.defineProperty(window.URL, 'createObjectURL', { configurable: true, writable: true, value: createObjectURL })
    Object.defineProperty(window.URL, 'revokeObjectURL', { configurable: true, writable: true, value: revokeObjectURL })
    clickMock = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) { downloadedNames.push(this.download) })
    window.localStorage.clear()
  })

  afterEach(() => {
    vi.restoreAllMocks()
    cleanup()
    vi.unstubAllGlobals()
  })

  it('does not fetch on mount, opening, canceling, or locale change', async () => {
    renderExport()
    expect(fetchMock).not.toHaveBeenCalled()
    await userEvent.click(screen.getByRole('button', { name: 'Export reviews' }))
    expect(fetchMock).not.toHaveBeenCalled()
    expect(screen.getByRole('dialog', { name: 'Export completed reviews' })).toBeInTheDocument()
    expect(screen.getByText(repository.repositoryName)).toBeInTheDocument()
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Select language' }), 'ko')
    expect(screen.getByRole('dialog', { name: '완료된 리뷰 내보내기' })).toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
    await userEvent.click(screen.getByRole('button', { name: '취소' }))
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('confirms with exactly one GET for the opaque repository identity', async () => {
    fetchMock.mockResolvedValue(exportResponse())
    renderExport()
    await userEvent.click(screen.getByRole('button', { name: 'Export reviews' }))
    await userEvent.click(screen.getByRole('button', { name: 'Download export' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    expect(fetchMock).toHaveBeenCalledWith('/api/exports/reviews?repositoryIdentity=opaque%20repo%2F%ED%95%9C%EA%B8%80', expect.objectContaining({ method: 'GET', headers: { Accept: 'application/json' }, signal: expect.any(AbortSignal) }))
  })

  it('keeps a busy confirmation single-shot under a double click', async () => {
    let resolve!: (value: Response) => void
    fetchMock.mockReturnValue(new Promise<Response>(done => { resolve = done }))
    renderExport()
    await userEvent.click(screen.getByRole('button', { name: 'Export reviews' }))
    const confirm = screen.getByRole('button', { name: 'Download export' })
    await userEvent.dblClick(confirm)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(confirm).toBeDisabled()
    resolve(exportResponse())
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Review metadata export downloaded.'))
  })

  it('lets cancel abort a busy export without creating a download', async () => {
    let signal!: AbortSignal
    fetchMock.mockImplementation((_url: string, init: RequestInit) => {
      signal = init.signal as AbortSignal
      return new Promise<Response>(() => {})
    })
    renderExport()
    await userEvent.click(screen.getByRole('button', { name: 'Export reviews' }))
    await userEvent.click(screen.getByRole('button', { name: 'Download export' }))
    await waitFor(() => expect(signal).toBeDefined())
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(signal.aborted).toBe(true)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(createObjectURL).not.toHaveBeenCalled()
  })

  it('downloads a Blob with a safe backend filename and revokes its object URL', async () => {
    fetchMock.mockResolvedValue(exportResponse(200, { 'Content-Disposition': 'attachment; filename="reviews-2026.json"' }))
    renderExport()
    await userEvent.click(screen.getByRole('button', { name: 'Export reviews' }))
    await userEvent.click(screen.getByRole('button', { name: 'Download export' }))
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Review metadata export downloaded.'))
    expect(createObjectURL).toHaveBeenCalledWith(expect.objectContaining({ type: 'application/json;charset=utf-8' }))
    expect(clickMock).toHaveBeenCalledTimes(1)
    expect(downloadedNames).toEqual(['reviews-2026.json'])
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:review-export')
  })

  it('falls back when Content-Disposition contains an unsafe filename', async () => {
    fetchMock.mockResolvedValue(exportResponse(200, { 'Content-Disposition': 'attachment; filename="../../secret.html"' }))
    renderExport()
    await userEvent.click(screen.getByRole('button', { name: 'Export reviews' }))
    await userEvent.click(screen.getByRole('button', { name: 'Download export' }))
    await waitFor(() => expect(screen.getByRole('status')).toBeInTheDocument())
    expect(clickMock).toHaveBeenCalledTimes(1)
    expect(downloadedNames).toEqual(['roborev-reviews.json'])
  })

  it('shows a localized warning when the bounded export is truncated', async () => {
    fetchMock.mockResolvedValue(exportResponse(200, { 'X-RoboRev-Export-Truncated': 'true' }))
    renderExport()
    await userEvent.click(screen.getByRole('button', { name: 'Export reviews' }))
    await userEvent.click(screen.getByRole('button', { name: 'Download export' }))
    expect(await screen.findByRole('status')).toHaveTextContent('This export includes up to 500 completed reviews; additional data exists.')
    expect(screen.queryByText(/latest|recent/i)).not.toBeInTheDocument()
  })

  it('keeps an export-specific error in the dialog without exposing a response body', async () => {
    fetchMock.mockResolvedValue(new Response('private daemon failure', { status: 503, headers: { 'Content-Type': 'application/json' } }))
    renderExport()
    await userEvent.click(screen.getByRole('button', { name: 'Export reviews' }))
    await userEvent.click(screen.getByRole('button', { name: 'Download export' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Review export could not be downloaded. Try again.')
    expect(screen.queryByText('private daemon failure')).not.toBeInTheDocument()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })

  it('retries exactly once after an export error and downloads the successful response', async () => {
    fetchMock.mockResolvedValueOnce(new Response('private daemon failure', { status: 503, headers: { 'Content-Type': 'application/json' } })).mockResolvedValueOnce(exportResponse())
    renderExport()
    await userEvent.click(screen.getByRole('button', { name: 'Export reviews' }))
    await userEvent.click(screen.getByRole('button', { name: 'Download export' }))
    await screen.findByRole('alert')
    expect(fetchMock).toHaveBeenCalledTimes(1)
    await userEvent.click(screen.getByRole('button', { name: 'Download export' }))
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Review metadata export downloaded.'))
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('aborts an in-flight export on unmount and does not create a stale download', async () => {
    let signal!: AbortSignal
    fetchMock.mockImplementation((_url: string, init: RequestInit) => {
      signal = init.signal as AbortSignal
      return new Promise<Response>(() => {})
    })
    const view = renderExport()
    await userEvent.click(screen.getByRole('button', { name: 'Export reviews' }))
    await userEvent.click(screen.getByRole('button', { name: 'Download export' }))
    await waitFor(() => expect(signal).toBeDefined())
    view.unmount()
    expect(signal.aborted).toBe(true)
    expect(createObjectURL).not.toHaveBeenCalled()
  })

  it('drops a response when the selected repository changes before download', async () => {
    let resolve!: (value: Response) => void
    fetchMock.mockReturnValue(new Promise<Response>(done => { resolve = done }))
    const view = renderExport()
    await userEvent.click(screen.getByRole('button', { name: 'Export reviews' }))
    await userEvent.click(screen.getByRole('button', { name: 'Download export' }))
    view.rerender(<I18nProvider><ReviewExport repositoryIdentity="other-repository" repositoryName="Other repository" /></I18nProvider>)
    resolve(exportResponse())
    await Promise.resolve()
    expect(createObjectURL).not.toHaveBeenCalled()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })
})
