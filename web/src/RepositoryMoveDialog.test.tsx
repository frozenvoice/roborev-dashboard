import userEvent from '@testing-library/user-event'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { StrictMode, type ComponentProps } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { RepositoryMoveDialog, type RepositoryMoveAvailability } from './RepositoryMoveDialog'
import type { Repository, RepositoryInventoryRow } from './api'
import { I18nProvider } from './i18n'

const source: RepositoryInventoryRow = { name: 'old-name', path: '/old/path', jobCount: 10, dashboardRegistered: false }
const destinations: Repository[] = [
  { identity: 'destination-1', name: 'new-name', path: 'E:\\new\\project', branch: 'main', head: 'abc', headShort: 'abc', commitSubject: 'Initial', dirty: false, hasConfig: true },
  { identity: 'same-path', name: 'same', path: 'e:\\old\\project', branch: 'main', head: 'def', headShort: 'def', commitSubject: 'Same', dirty: false, hasConfig: true },
]
const available: RepositoryMoveAvailability = { canMove: true, reason: null }
const preview = {
  sourcePath: '/fresh/path', sourceName: 'fresh-name', jobCount: 11,
  destinationRepositoryIdentity: 'destination-1', destinationPath: 'E:\\new\\project', destinationName: 'new-name',
  command: { executable: 'roborev', args: ['repo', 'move', '/fresh/path', '.'], workingDirectory: 'E:\\new\\project' },
  token: 'move-token', expiresAt: '2026-08-23T01:02:03Z',
}

function response(value: unknown, status = 200) {
  return Promise.resolve(new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } }))
}

function renderDialog(overrides: Partial<ComponentProps<typeof RepositoryMoveDialog>> = {}) {
  const props: ComponentProps<typeof RepositoryMoveDialog> = {
    source, destinations, availability: available, onClose: vi.fn(), onSuccess: vi.fn(), onRefresh: vi.fn(), ...overrides,
  }
  return render(<I18nProvider><RepositoryMoveDialog {...props} /></I18nProvider>)
}

beforeEach(() => { window.localStorage.setItem('roborev-dashboard-language', 'en') })
afterEach(() => { cleanup(); vi.restoreAllMocks() })

describe('RepositoryMoveDialog', () => {
  it('uses every registered opaque identity and has no free-text or merge controls', () => {
    renderDialog()
    const select = screen.getByRole('combobox', { name: 'Registered destination' })
    expect(select).toBeInTheDocument()
    expect(screen.getByRole('option', { name: 'new-name · E:\\new\\project' })).toHaveValue('destination-1')
    expect(screen.getByRole('option', { name: 'same · e:\\old\\project' })).toHaveValue('same-path')
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
    expect(screen.queryByText('Delete')).not.toBeInTheDocument()
    expect(screen.queryByText('Merge')).not.toBeInTheDocument()
  })

  it('keeps the selected opaque identity after destination path props drift', async () => {
    const calls: Array<{ url: string; init?: RequestInit }> = []
    const fetchSpy = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ url: String(input), init })
      return response(preview)
    })
    vi.stubGlobal('fetch', fetchSpy)
    const view = renderDialog()
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Registered destination' }), 'destination-1')
    view.rerender(<I18nProvider><RepositoryMoveDialog source={source} destinations={[{ ...destinations[0], path: 'E:\\old\\project' }]} availability={available} onClose={vi.fn()} onSuccess={vi.fn()} onRefresh={vi.fn()} /></I18nProvider>)
    expect(screen.getByRole('combobox', { name: 'Registered destination' })).toHaveValue('destination-1')
    expect(screen.getByRole('button', { name: 'Prepare move' })).toBeEnabled()
    await userEvent.click(screen.getByRole('button', { name: 'Prepare move' }))
    await screen.findByRole('button', { name: 'Confirm move' })
    expect(fetchSpy).toHaveBeenCalledOnce()
    expect(JSON.parse(String(calls[0].init?.body))).toEqual({ sourcePath: source.path, destinationRepositoryIdentity: 'destination-1' })
  })

  it('previews exactly once with source path and destination identity, rendering executable cwd and argv', async () => {
    const calls: Array<{ url: string; init?: RequestInit }> = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => { calls.push({ url: String(input), init }); return response(preview) }))
    renderDialog()
    const initialFacts = document.querySelector('.move-facts')
    expect(initialFacts).toHaveTextContent('old-name')
    expect(initialFacts).toHaveTextContent('/old/path')
    expect(initialFacts).toHaveTextContent('10')
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Registered destination' }), 'destination-1')
    await userEvent.click(screen.getByRole('button', { name: 'Prepare move' }))
    expect(await screen.findByRole('button', { name: 'Confirm move' })).toBeInTheDocument()
    const authoritativeFacts = document.querySelector('.move-facts')
    expect(authoritativeFacts).toHaveTextContent('fresh-name')
    expect(authoritativeFacts).toHaveTextContent('/fresh/path')
    expect(authoritativeFacts).toHaveTextContent('11')
    expect(authoritativeFacts).not.toHaveTextContent('old-name')
    expect(authoritativeFacts).not.toHaveTextContent('/old/path')
    expect(authoritativeFacts).not.toHaveTextContent('10')
    expect(calls).toHaveLength(1)
    expect(calls[0].url).toBe('/api/repository-moves/preview')
    expect(JSON.parse(String(calls[0].init?.body))).toEqual({ sourcePath: source.path, destinationRepositoryIdentity: 'destination-1' })
    expect(screen.getByText('roborev')).toBeInTheDocument()
    expect(screen.getByText('Working directory: E:\\new\\project')).toBeInTheDocument()
    expect(screen.getByText('argv[0]')).toBeInTheDocument()
    expect(screen.getByText('repo')).toBeInTheDocument()
    expect(screen.getByText('argv[3]')).toBeInTheDocument()
    expect(screen.getByText('.')).toBeInTheDocument()
  })

  it.each([
    ['/work/Repo', '/work/repo'],
    ['E:\\Repo', 'e:\\repo'],
  ])('keeps path variants as selectable registered destinations: %s and %s', async (sourcePath, destinationPath) => {
    const variantSource = { ...source, path: sourcePath }
    const variantDestination = { ...destinations[0], identity: 'path-variant', path: destinationPath }
    renderDialog({ source: variantSource, destinations: [variantDestination] })
    const option = screen.getByRole('option', { name: `new-name · ${destinationPath}` })
    expect(option).toHaveValue('path-variant')
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Registered destination' }), 'path-variant')
    expect(screen.getByRole('combobox', { name: 'Registered destination' })).toHaveValue('path-variant')
  })

  it('lets an exact same-path identity reach Preview and surfaces the backend rejection', async () => {
    const samePath = '/work/Repo'
    const samePathDestination = { ...destinations[0], identity: 'exact-same-path', path: samePath }
    const calls: Array<{ url: string; init?: RequestInit }> = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ url: String(input), init })
      return response({ error: 'source and destination paths must differ' }, 409)
    }))
    renderDialog({ source: { ...source, path: samePath }, destinations: [samePathDestination] })
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Registered destination' }), 'exact-same-path')
    await userEvent.click(screen.getByRole('button', { name: 'Prepare move' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('source and destination paths must differ')
    expect(calls).toHaveLength(1)
    expect(calls[0].url).toBe('/api/repository-moves/preview')
    expect(JSON.parse(String(calls[0].init?.body))).toEqual({ sourcePath: samePath, destinationRepositoryIdentity: 'exact-same-path' })
    expect(screen.queryByRole('button', { name: 'Confirm move' })).not.toBeInTheDocument()
  })

  it('does not call preview or confirm when canceled', async () => {
    const fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
    const onClose = vi.fn()
    renderDialog({ onClose })
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(onClose).toHaveBeenCalledOnce()
    expect(fetchSpy).not.toHaveBeenCalled()
  })

  it('confirms a ready preview exactly once even after browser availability disappears', async () => {
    let confirmResolve!: (value: Response) => void
    const calls: Array<{ url: string; init?: RequestInit }> = []
    const fetchSpy = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ url: String(input), init })
      return String(input).endsWith('/preview') ? response(preview) : new Promise<Response>(resolve => { confirmResolve = resolve })
    })
    vi.stubGlobal('fetch', fetchSpy)
    const onSuccess = vi.fn()
    const view = renderDialog({ onSuccess })
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Registered destination' }), 'destination-1')
    await userEvent.click(screen.getByRole('button', { name: 'Prepare move' }))
    await screen.findByRole('button', { name: 'Confirm move' })
    view.rerender(<I18nProvider><RepositoryMoveDialog source={source} destinations={destinations} availability={{ canMove: false, reason: 'runtime-unavailable' }} onClose={vi.fn()} onSuccess={onSuccess} onRefresh={vi.fn()} /></I18nProvider>)
    expect(screen.getByRole('button', { name: 'Confirm move' })).toBeEnabled()
    const confirmButton = screen.getByRole('button', { name: 'Confirm move' })
    fireEvent.click(confirmButton)
    fireEvent.click(confirmButton)
    expect(screen.getByRole('button', { name: 'Moving…' })).toBeDisabled()
    expect(fetchSpy).toHaveBeenCalledTimes(2)
    expect(calls[1].url).toBe('/api/repository-moves')
    expect(JSON.parse(String(calls[1].init?.body))).toEqual({ token: preview.token })
    expect(Object.keys(JSON.parse(String(calls[1].init?.body)))).toEqual(['token'])
    confirmResolve(await response({ status: 'moved', sourcePath: source.path, destinationPath: 'E:\\new\\project' }))
    await waitFor(() => expect(onSuccess).toHaveBeenCalledOnce())
  })

  it('keeps Prepare disabled with only the runtime reason after confirm fails during availability loss', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => String(input).endsWith('/preview') ? response(preview) : response({ error: 'daemon offline' }, 503)))
    const view = renderDialog()
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Registered destination' }), 'destination-1')
    await userEvent.click(screen.getByRole('button', { name: 'Prepare move' }))
    await screen.findByRole('button', { name: 'Confirm move' })
    view.rerender(<I18nProvider><RepositoryMoveDialog source={source} destinations={destinations} availability={{ canMove: false, reason: 'runtime-unavailable' }} onClose={vi.fn()} onSuccess={vi.fn()} onRefresh={vi.fn()} /></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Confirm move' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('daemon offline')
    expect(screen.getByRole('button', { name: 'Prepare move' })).toBeDisabled()
    expect(screen.getByText(/RoboRev is currently unavailable/)).toBeInTheDocument()
    expect(screen.queryByText(/installed RoboRev does not support repository path repair/)).not.toBeInTheDocument()
  })

  it('restores mounted state across StrictMode effect setup and cleanup', async () => {
    const fetchSpy = vi.fn(() => response(preview))
    vi.stubGlobal('fetch', fetchSpy)
    render(<StrictMode><I18nProvider><RepositoryMoveDialog source={source} destinations={destinations} availability={available} onClose={vi.fn()} onSuccess={vi.fn()} onRefresh={vi.fn()} /></I18nProvider></StrictMode>)
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Registered destination' }), 'destination-1')
    await userEvent.click(screen.getByRole('button', { name: 'Prepare move' }))
    await screen.findByRole('button', { name: 'Confirm move' })
    expect(fetchSpy).toHaveBeenCalledOnce()
  })

  it('keeps Prepare disabled and shows the exclusive runtime or unsupported reason', () => {
    const { rerender } = renderDialog({ availability: { canMove: false, reason: 'runtime-unavailable' } })
    expect(screen.getByRole('button', { name: 'Prepare move' })).toBeDisabled()
    expect(screen.getByText(/RoboRev is currently unavailable/)).toBeInTheDocument()
    rerender(<I18nProvider><RepositoryMoveDialog source={source} destinations={destinations} availability={{ canMove: false, reason: 'unsupported' }} onClose={vi.fn()} onSuccess={vi.fn()} onRefresh={vi.fn()} /></I18nProvider>)
    expect(screen.getByText(/installed RoboRev does not support repository path repair/)).toBeInTheDocument()
    expect(screen.queryByText(/RoboRev is currently unavailable/)).not.toBeInTheDocument()
  })

  it('offers close and refresh only after an unverified response and never retries', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => String(input).endsWith('/preview') ? response(preview) : response({ status: 'unverified', sourcePath: source.path, destinationPath: 'E:\\new\\project', message: 'refresh inventory' })))
    const onRefresh = vi.fn()
    const onClose = vi.fn()
    renderDialog({ onRefresh, onClose })
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Registered destination' }), 'destination-1')
    await userEvent.click(screen.getByRole('button', { name: 'Prepare move' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Confirm move' }))
    expect(await screen.findByText('refresh inventory')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Close' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Prepare move' })).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    expect(onRefresh).toHaveBeenCalledOnce()
  })
})
