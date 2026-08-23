import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { moveAvailability, Repositories } from './Repositories'
import type { Bootstrap } from './api'
import { I18nProvider, useI18n } from './i18n'

const capabilities = { reviewEnqueueApi: true, frozenRerunApi: true, jobListApi: true, jobOutputStatusApi: true, panelReview: true, fixCommand: true, panelMembersApi: true, promptLazyLoad: true, rawLog: true, repositoryInventory: true }
const bootstrap: Bootstrap = {
  csrfToken: 'csrf', selectedRepository: 'E:\\repo', selectedRepositoryIdentity: 'repo-1',
  repositories: [{ identity: 'repo-1', name: 'registered', path: 'E:\\repo', branch: 'main', head: 'abc', headShort: 'abc', commitSubject: 'Initial', dirty: false, hasConfig: true }],
  roborev: { installed: true, version: 'v0.64.0', status: { running: true }, capabilities },
}
const renameBootstrap: Bootstrap = { ...bootstrap, roborev: { ...bootstrap.roborev, capabilities: { ...bootstrap.roborev.capabilities, repositoryRename: true } } }
const renameOfflineBootstrap: Bootstrap = { ...renameBootstrap, roborev: { ...renameBootstrap.roborev, status: { running: false } } }
const renameUnsupportedBootstrap: Bootstrap = { ...renameBootstrap, roborev: { ...renameBootstrap.roborev, version: 'v0.63.0', status: { running: false }, capabilities: { ...renameBootstrap.roborev.capabilities, repositoryInventory: false, repositoryRename: false } } }
const renameNotInstalledBootstrap: Bootstrap = { ...renameBootstrap, roborev: { ...renameBootstrap.roborev, installed: false, status: { running: false }, capabilities: { ...renameBootstrap.roborev.capabilities, repositoryInventory: false, repositoryRename: false } } }
const renameDefensiveMixedBootstrap: Bootstrap = { ...renameBootstrap, roborev: { ...renameBootstrap.roborev, status: { running: true }, capabilities: { ...renameBootstrap.roborev.capabilities, repositoryInventory: true, repositoryRename: false } } }
const moveBootstrap: Bootstrap = { ...renameBootstrap, roborev: { ...renameBootstrap.roborev, capabilities: { ...renameBootstrap.roborev.capabilities, repositoryMove: true } } }
const moveUnsupportedBootstrap: Bootstrap = { ...moveBootstrap, roborev: { ...moveBootstrap.roborev, version: 'v0.63.0', status: { running: false }, capabilities: { ...moveBootstrap.roborev.capabilities, repositoryInventory: false, repositoryRename: false, repositoryMove: false } } }
const moveNotInstalledBootstrap: Bootstrap = { ...moveBootstrap, roborev: { ...moveBootstrap.roborev, installed: false, status: { running: false }, capabilities: { ...moveBootstrap.roborev.capabilities, repositoryInventory: false, repositoryRename: false, repositoryMove: false } } }
const moveOfflineBootstrap: Bootstrap = { ...moveBootstrap, roborev: { ...moveBootstrap.roborev, status: { running: false }, capabilities: { ...moveBootstrap.roborev.capabilities, repositoryInventory: true, repositoryRename: true, repositoryMove: true } } }
const moveDefensiveMixedBootstrap: Bootstrap = { ...moveBootstrap, roborev: { ...moveBootstrap.roborev, status: { running: true }, capabilities: { ...moveBootstrap.roborev.capabilities, repositoryInventory: true, repositoryRename: true, repositoryMove: false } } }

const renamePreview = { path: 'E:\\tracked', currentName: 'tracked', newName: 'Renamed 저장소', jobCount: 3, dashboardRegistered: false, command: { executable: 'roborev.exe', args: ['repo', 'rename', 'E:\\tracked', 'Renamed 저장소'] }, token: 'rename-token', expiresAt: '2026-08-23T01:02:03Z' }
const movePreview = { sourcePath: 'E:/stale', sourceName: 'stale', jobCount: 2, destinationRepositoryIdentity: 'repo-1', destinationPath: 'E:\\repo', destinationName: 'registered', command: { executable: 'roborev.exe', args: ['repo', 'move', 'E:/stale', '.'], workingDirectory: 'E:\\repo' }, token: 'move-token', expiresAt: '2026-08-23T01:02:03Z' }

function response(value: unknown, status = 200) {
  return Promise.resolve(new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } }))
}

const trackedInventory = { repositories: [{ name: 'tracked', path: 'E:\\tracked', jobCount: 1, dashboardRegistered: true }], repositoryCount: 1, totalJobCount: 1 }

async function prepareReadyRename() {
  await userEvent.click(await screen.findByRole('button', { name: 'Rename' }))
  await userEvent.type(screen.getByRole('textbox', { name: 'New display name' }), 'Renamed 저장소')
  await userEvent.click(screen.getByRole('button', { name: 'Prepare rename' }))
  return screen.findByRole('button', { name: 'Confirm rename' })
}

function Harness({ registerRepository = async () => true }: { registerRepository?: (path: string) => Promise<boolean> }) {
  const { setLocale } = useI18n()
  return <><Repositories bootstrap={bootstrap} registerRepository={registerRepository} busy={false} /><button onClick={() => setLocale('ko')}>한국어</button></>
}

beforeEach(() => { window.localStorage.setItem('roborev-dashboard-language', 'en') })
afterEach(() => { cleanup(); vi.restoreAllMocks() })

describe('Repositories v2 inventory', () => {
  it.each([
    ['binary/install absent', moveNotInstalledBootstrap, false, 'runtime-unavailable'],
    ['unsupported RoboRev version', moveUnsupportedBootstrap, false, 'unsupported'],
    ['exact v0.64 daemon offline', moveOfflineBootstrap, false, 'runtime-unavailable'],
    ['exact v0.64 daemon online', moveBootstrap, true, null],
    ['defensive mixed capability state', moveDefensiveMixedBootstrap, false, 'unsupported'],
  ] as const)('uses the production Bootstrap truth table for Move availability: %s', (_label, fixture, canMove, reason) => {
    expect(moveAvailability(fixture)).toMatchObject({ canMove, reason })
  })

  it('loads exactly once when mounted and preserves registered form/cards', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => { calls.push(String(input)); return response({ repositories: [{ name: 'tracked', path: 'E:\\tracked', jobCount: 2, dashboardRegistered: true }, { name: 'tracked', path: 'E:\\other', jobCount: 0, dashboardRegistered: false }], repositoryCount: 2, totalJobCount: 2 }) }))
    render(<I18nProvider><Repositories bootstrap={bootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    expect(await screen.findAllByText('tracked')).toHaveLength(2)
    expect(calls).toEqual(['/api/repository-inventory'])
    expect(screen.getByRole('textbox', { name: 'Add local Git repository' })).toBeInTheDocument()
    expect(screen.getByText('registered')).toBeInTheDocument()
    expect(screen.getAllByText('E:\\other')[0]).toBeInTheDocument()
    expect(screen.getByText('Tracked repositories 2, Jobs 2')).toBeInTheDocument()
    const trackedTable = document.querySelector('.tracked-table')
    expect(trackedTable?.querySelectorAll('tbody tr')).toHaveLength(2)
    expect(trackedTable?.querySelectorAll('button')).toHaveLength(0)
    expect(screen.getByText('Dashboard registered')).toBeInTheDocument()
    expect(screen.getByText('Not registered')).toBeInTheDocument()
  })

  it('refreshes once per explicit click, ignores stale responses, and does not refetch on locale change', async () => {
    let resolveFirst!: (value: Response) => void
    let resolveSecond!: (value: Response) => void
    const first = new Promise<Response>(resolve => { resolveFirst = resolve })
    const second = new Promise<Response>(resolve => { resolveSecond = resolve })
    let count = 0
    vi.stubGlobal('fetch', vi.fn(() => ++count === 1 ? first : second))
    render(<I18nProvider><Harness /></I18nProvider>)
    await waitFor(() => expect(count).toBe(1))
    const refresh = screen.getByRole('button', { name: 'Refresh' })
    await userEvent.click(refresh)
    expect(count).toBe(2)
    resolveSecond(await response({ repositories: [{ name: 'newest', path: 'E:\\newest', jobCount: 4, dashboardRegistered: false }], repositoryCount: 1, totalJobCount: 4 }))
    expect(await screen.findByText('newest')).toBeInTheDocument()
    resolveFirst(await response({ repositories: [{ name: 'stale', path: 'E:\\stale', jobCount: 1, dashboardRegistered: true }], repositoryCount: 1, totalJobCount: 1 }))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(screen.queryByText('stale')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '한국어' }))
    await waitFor(() => expect(screen.getByText('RoboRev 추적 저장소')).toBeInTheDocument())
    expect(count).toBe(2)
  })

  it('keeps the registry UI available when inventory is unsupported', async () => {
    const unsupported = { ...bootstrap, roborev: { ...bootstrap.roborev, capabilities: { ...capabilities, repositoryInventory: false } } }
    const fetchSpy = vi.fn()
    vi.stubGlobal('fetch', fetchSpy)
    render(<I18nProvider><Repositories bootstrap={unsupported} registerRepository={async () => true} busy={false} /></I18nProvider>)
    expect(screen.getByRole('textbox', { name: 'Add local Git repository' })).toBeInTheDocument()
    expect(screen.getByText('RoboRev inventory unavailable')).toBeInTheDocument()
    expect(fetchSpy).not.toHaveBeenCalled()
  })

  it('renders a valid empty state and keeps registry controls through HTTP and malformed errors', async () => {
    const fetchSpy = vi.fn()
      .mockImplementationOnce(() => response({ error: 'daemon unavailable' }, 503))
      .mockImplementationOnce(() => response({ repositories: [], repositoryCount: 1, totalJobCount: 0 }))
      .mockImplementationOnce(() => response({ repositories: [], repositoryCount: 0, totalJobCount: 0 }))
    vi.stubGlobal('fetch', fetchSpy)
    const { unmount } = render(<I18nProvider><Repositories bootstrap={bootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    expect(await screen.findByRole('alert')).toHaveTextContent('daemon unavailable')
    expect(screen.getByRole('textbox', { name: 'Add local Git repository' })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    expect(await screen.findByText('The Dashboard could not validate the RoboRev repository inventory.')).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: 'Add local Git repository' })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    expect(await screen.findByText('No repositories are tracked by RoboRev.')).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: 'Add local Git repository' })).toBeInTheDocument()
    unmount()
  })

  it('refreshes inventory independently after successful registration', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => { calls.push(String(input)); return response({ repositories: [], repositoryCount: 0, totalJobCount: 0 }) }))
    const registerRepository = vi.fn(async () => true)
    render(<I18nProvider><Repositories bootstrap={bootstrap} registerRepository={registerRepository} busy={false} /></I18nProvider>)
    await waitFor(() => expect(calls).toHaveLength(1))
    const input = screen.getByRole('textbox', { name: 'Add local Git repository' })
    await userEvent.type(input, 'E:\\new')
    await userEvent.click(screen.getByRole('button', { name: 'Register' }))
    await waitFor(() => expect(registerRepository).toHaveBeenCalledWith('E:\\new'))
    await waitFor(() => expect(calls).toHaveLength(2))
    expect(input).toHaveValue('')
  })

  it('does not refresh after failed registration and preserves the path', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => { calls.push(String(input)); return response({ repositories: [], repositoryCount: 0, totalJobCount: 0 }) }))
    const registerRepository = vi.fn(async () => false)
    render(<I18nProvider><Repositories bootstrap={bootstrap} registerRepository={registerRepository} busy={false} /></I18nProvider>)
    await waitFor(() => expect(calls).toHaveLength(1))
    const input = screen.getByRole('textbox', { name: 'Add local Git repository' })
    await userEvent.type(input, 'E:\\failed')
    await userEvent.click(screen.getByRole('button', { name: 'Register' }))
    await waitFor(() => expect(registerRepository).toHaveBeenCalledWith('E:\\failed'))
    expect(calls).toHaveLength(1)
    expect(input).toHaveValue('E:\\failed')
  })

  it('keeps registration success isolated when its inventory refresh fails', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => { calls.push(String(input)); return calls.length === 1 ? response({ repositories: [], repositoryCount: 0, totalJobCount: 0 }) : response({ error: 'refresh failed' }, 503) }))
    const registerRepository = vi.fn(async () => true)
    render(<I18nProvider><Repositories bootstrap={bootstrap} registerRepository={registerRepository} busy={false} /></I18nProvider>)
    await waitFor(() => expect(calls).toHaveLength(1))
    const input = screen.getByRole('textbox', { name: 'Add local Git repository' })
    await userEvent.type(input, 'E:\\registered')
    await userEvent.click(screen.getByRole('button', { name: 'Register' }))
    await waitFor(() => expect(registerRepository).toHaveBeenCalledWith('E:\\registered'))
    await waitFor(() => expect(calls).toHaveLength(2))
    expect(input).toHaveValue('')
    expect(await screen.findByRole('alert')).toHaveTextContent('refresh failed')
  })

  it('aborts inventory on unmount and ignores a late response', async () => {
    let resolve!: (value: Response) => void
    let signal: AbortSignal | undefined
    vi.stubGlobal('fetch', vi.fn((_input: RequestInfo | URL, init?: RequestInit) => { signal = init?.signal ?? undefined; return new Promise<Response>(promiseResolve => { resolve = promiseResolve }) }))
    const { unmount } = render(<I18nProvider><Repositories bootstrap={bootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    await waitFor(() => expect(signal).toBeDefined())
    unmount()
    expect(signal?.aborted).toBe(true)
    resolve(await response({ repositories: [{ name: 'late', path: 'E:\\late', jobCount: 1, dashboardRegistered: true }], repositoryCount: 1, totalJobCount: 1 }))
  })

  it('does not poll while the Repositories page remains mounted', async () => {
    const fetchSpy = vi.fn(() => response({ repositories: [], repositoryCount: 0, totalJobCount: 0 }))
    vi.stubGlobal('fetch', fetchSpy)
    render(<I18nProvider><Repositories bootstrap={bootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    await waitFor(() => expect(fetchSpy).toHaveBeenCalledTimes(1))
    await new Promise(resolve => setTimeout(resolve, 80))
    expect(fetchSpy).toHaveBeenCalledTimes(1)
  })

  it('offers rename only for tracked rows, captures the exact duplicate-name row path, and shows the fresh preview', async () => {
    const calls: Array<{ input: string; init?: RequestInit }> = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ input: String(input), init })
      if (calls.length === 1) return response({ repositories: [{ name: 'tracked', path: 'E:\\tracked', jobCount: 3, dashboardRegistered: false }, { name: 'tracked', path: 'E:\\other', jobCount: 1, dashboardRegistered: true }], repositoryCount: 2, totalJobCount: 4 })
      return response({ ...renamePreview, path: 'E:\\other', currentName: 'tracked', newName: '새 이름', jobCount: 9, dashboardRegistered: false, command: { executable: 'roborev.exe', args: ['repo', 'rename', 'E:\\other', '새 이름'] } })
    }))
    render(<I18nProvider><Repositories bootstrap={renameBootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    expect(await screen.findAllByRole('button', { name: 'Rename' })).toHaveLength(2)
    expect(screen.getByText('registered')).toBeInTheDocument()
    await userEvent.click(screen.getAllByRole('button', { name: 'Rename' })[1])
    const input = screen.getByRole('textbox', { name: 'New display name' })
    await userEvent.type(input, ' 새 이름 ')
    await userEvent.click(screen.getByRole('button', { name: 'Prepare rename' }))
    await waitFor(() => expect(screen.getByText('Exact executable and argv')).toBeInTheDocument())
    expect(calls[1].input).toBe('/api/repository-renames/preview')
    expect(JSON.parse(String(calls[1].init?.body))).toEqual({ path: 'E:\\other', newName: '새 이름' })
    expect(screen.getAllByText('E:\\other')[0]).toBeInTheDocument()
    expect(screen.getByText('9')).toBeInTheDocument()
    expect(screen.getAllByText('Not registered')[0]).toBeInTheDocument()
    expect(screen.queryByText('Move')).not.toBeInTheDocument()
    expect(screen.queryByText('Delete')).not.toBeInTheDocument()
    expect(screen.queryByText('Merge')).not.toBeInTheDocument()
  })

  it('rejects invalid names without preview and cancel never confirms', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => { calls.push(String(input)); return response({ repositories: [{ name: 'tracked', path: 'E:\\tracked', jobCount: 1, dashboardRegistered: true }], repositoryCount: 1, totalJobCount: 1 }) }))
    render(<I18nProvider><Repositories bootstrap={renameBootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    await userEvent.click(await screen.findByRole('button', { name: 'Rename' }))
    const input = screen.getByRole('textbox', { name: 'New display name' })
    await userEvent.type(input, '-unsafe')
    await userEvent.click(screen.getByRole('button', { name: 'Prepare rename' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Names beginning with')
    expect(screen.getByRole('button', { name: 'Prepare rename' })).toBeEnabled()
    expect(calls).toEqual(['/api/repository-inventory'])
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(calls).toEqual(['/api/repository-inventory'])
  })

  it('confirms an issued ready preview after the rename capability disappears and surfaces a 503', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input)
      calls.push(url)
      if (url === '/api/repository-inventory') return response(trackedInventory)
      if (url.endsWith('/preview')) return response(renamePreview)
      return response({ error: 'RoboRev is unavailable' }, 503)
    }))
    const view = render(<I18nProvider><Repositories bootstrap={renameBootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    await prepareReadyRename()
    view.rerender(<I18nProvider><Repositories bootstrap={renameDefensiveMixedBootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    expect(screen.getByRole('button', { name: 'Confirm rename' })).toBeEnabled()
    await userEvent.click(screen.getByRole('button', { name: 'Confirm rename' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('RoboRev is unavailable')
    expect(screen.getByRole('button', { name: 'Prepare rename' })).toBeDisabled()
    expect(screen.getByText('The installed RoboRev does not support repository rename.')).toBeInTheDocument()
    expect(screen.queryByText('RoboRev is currently unavailable. When it becomes available again, prepare a new Rename Preview.')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeEnabled()
    expect(calls.filter(url => url === '/api/repository-renames')).toHaveLength(1)
  })

  it('recovers an unavailable confirm failure only through an explicit new preview', async () => {
    const calls: string[] = []
    let previewCount = 0
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input)
      calls.push(url)
      if (url === '/api/repository-inventory') return response(trackedInventory)
      if (url.endsWith('/preview')) { previewCount += 1; return response(renamePreview) }
      return response({ error: 'RoboRev is unavailable' }, 503)
    }))
    const view = render(<I18nProvider><Repositories bootstrap={renameBootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    await prepareReadyRename()
    view.rerender(<I18nProvider><Repositories bootstrap={renameDefensiveMixedBootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Confirm rename' }))
    await screen.findByText('The installed RoboRev does not support repository rename.')
    view.rerender(<I18nProvider><Repositories bootstrap={renameBootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    expect(screen.getByRole('button', { name: 'Prepare rename' })).toBeEnabled()
    expect(previewCount).toBe(1)
    expect(screen.queryByText('The installed RoboRev does not support repository rename.')).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Prepare rename' }))
    await waitFor(() => expect(previewCount).toBe(2))
    expect(calls.filter(url => url === '/api/repository-renames')).toHaveLength(1)
  })

  it('confirms an issued ready preview after daemon availability disappears and handles renamed success', async () => {
    const calls: string[] = []
    const daemonUnavailable = { ...renameBootstrap, roborev: { ...renameBootstrap.roborev, status: { running: false } } }
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input)
      calls.push(url)
      if (url === '/api/repository-inventory') return response(trackedInventory)
      if (url.endsWith('/preview')) return response(renamePreview)
      return response({ status: 'renamed', path: 'E:\\tracked', previousName: 'tracked', name: 'Renamed 저장소' })
    }))
    const view = render(<I18nProvider><Repositories bootstrap={renameBootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    await prepareReadyRename()
    view.rerender(<I18nProvider><Repositories bootstrap={daemonUnavailable} registerRepository={async () => true} busy={false} /></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Confirm rename' }))
    expect(await screen.findByText('RoboRev inventory unavailable')).toBeInTheDocument()
    expect(calls.filter(url => url === '/api/repository-renames')).toHaveLength(1)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('keeps an issued ready preview confirmable after capability loss when the server returns unverified', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input)
      calls.push(url)
      if (url === '/api/repository-inventory') return response(trackedInventory)
      if (url.endsWith('/preview')) return response(renamePreview)
      return response({ status: 'unverified', path: 'E:\\tracked', message: 'Readback unavailable' })
    }))
    const view = render(<I18nProvider><Repositories bootstrap={renameBootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    await prepareReadyRename()
    view.rerender(<I18nProvider><Repositories bootstrap={renameDefensiveMixedBootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Confirm rename' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Readback unavailable')
    expect(screen.getAllByRole('button', { name: 'Refresh' })[1]).toBeInTheDocument()
    expect(calls.filter(url => url === '/api/repository-renames')).toHaveLength(1)
  })

  it.each([
    ['runtime unavailable', renameOfflineBootstrap, 'RoboRev is currently unavailable. When it becomes available again, prepare a new Rename Preview.', 'The installed RoboRev does not support repository rename.'],
    ['rename unsupported', renameUnsupportedBootstrap, 'The installed RoboRev does not support repository rename.', 'RoboRev is currently unavailable. When it becomes available again, prepare a new Rename Preview.'],
  ] as const)('distinguishes %s while the rename dialog is editing', async (_label, unavailableBootstrap, hint, otherHint) => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => String(input) === '/api/repository-inventory' ? response(trackedInventory) : response(renamePreview)))
    const view = render(<I18nProvider><Repositories bootstrap={renameBootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    await userEvent.click(await screen.findByRole('button', { name: 'Rename' }))
    view.rerender(<I18nProvider><Repositories bootstrap={unavailableBootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    expect(screen.getByRole('button', { name: 'Prepare rename' })).toBeDisabled()
    expect(screen.getByText(hint)).toBeInTheDocument()
    expect(screen.queryByText(otherHint)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Confirm rename' })).not.toBeInTheDocument()
  })

  it('keeps Prepare enabled and hides both availability hints when all rename capabilities are available', async () => {
    vi.stubGlobal('fetch', vi.fn(() => response(trackedInventory)))
    render(<I18nProvider><Repositories bootstrap={renameBootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    await userEvent.click(await screen.findByRole('button', { name: 'Rename' }))
    await userEvent.type(screen.getByRole('textbox', { name: 'New display name' }), 'Renamed 저장소')
    expect(screen.getByRole('button', { name: 'Prepare rename' })).toBeEnabled()
    expect(screen.queryByText('RoboRev is currently unavailable. When it becomes available again, prepare a new Rename Preview.')).not.toBeInTheDocument()
    expect(screen.queryByText('The installed RoboRev does not support repository rename.')).not.toBeInTheDocument()
  })

  it.each([
    ['v0.64 online', renameBootstrap, null],
    ['v0.64 offline', renameOfflineBootstrap, 'runtime-unavailable'],
    ['not installed', renameNotInstalledBootstrap, 'runtime-unavailable'],
    ['v0.63 unsupported', renameUnsupportedBootstrap, 'unsupported'],
    ['defensive mixed capability state', renameDefensiveMixedBootstrap, 'unsupported'],
  ] as const)('matches the rename availability truth table for %s', async (_label, fixture, expectedReason) => {
    const runtimeHint = 'RoboRev is currently unavailable. When it becomes available again, prepare a new Rename Preview.'
    const unsupportedHint = 'The installed RoboRev does not support repository rename.'
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input)
      if (url === '/api/repository-inventory') return response(trackedInventory)
      if (url.endsWith('/preview')) return response(renamePreview)
      return response({ status: 'unverified', path: 'E:\\tracked', message: 'Readback unavailable' })
    }))
    const view = render(<I18nProvider><Repositories bootstrap={renameBootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    await userEvent.click(await screen.findByRole('button', { name: 'Rename' }))
    await userEvent.type(screen.getByRole('textbox', { name: 'New display name' }), 'Renamed 저장소')

    if (expectedReason === null) {
      expect(screen.getByRole('button', { name: 'Prepare rename' })).toBeEnabled()
      expect(screen.queryByText(runtimeHint)).not.toBeInTheDocument()
      expect(screen.queryByText(unsupportedHint)).not.toBeInTheDocument()
      return
    }

    await userEvent.click(screen.getByRole('button', { name: 'Prepare rename' }))
    await screen.findByRole('button', { name: 'Confirm rename' })
    view.rerender(<I18nProvider><Repositories bootstrap={fixture} registerRepository={async () => true} busy={false} /></I18nProvider>)
    expect(screen.getByRole('button', { name: 'Confirm rename' })).toBeEnabled()
    expect(screen.queryByText(runtimeHint)).not.toBeInTheDocument()
    expect(screen.queryByText(unsupportedHint)).not.toBeInTheDocument()

    const input = screen.getByRole('textbox', { name: 'New display name' })
    await userEvent.clear(input)
    await userEvent.type(input, 'Edited name')
    expect(screen.getByRole('button', { name: 'Prepare rename' })).toBeDisabled()
    expect(screen.getByText(expectedReason === 'unsupported' ? unsupportedHint : runtimeHint)).toBeInTheDocument()
    expect(screen.queryByText(expectedReason === 'unsupported' ? runtimeHint : unsupportedHint)).not.toBeInTheDocument()
  })

  it.each([
    ['runtime unavailable', renameOfflineBootstrap, 'RoboRev is currently unavailable. When it becomes available again, prepare a new Rename Preview.', 'The installed RoboRev does not support repository rename.'],
    ['rename unsupported', renameUnsupportedBootstrap, 'The installed RoboRev does not support repository rename.', 'RoboRev is currently unavailable. When it becomes available again, prepare a new Rename Preview.'],
  ] as const)('keeps the %s hint alongside a preview error', async (_label, unavailableBootstrap, hint, otherHint) => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input)
      if (url === '/api/repository-inventory') return response(trackedInventory)
      return response({ error: 'preview failed' }, 502)
    }))
    const view = render(<I18nProvider><Repositories bootstrap={renameBootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    await userEvent.click(await screen.findByRole('button', { name: 'Rename' }))
    await userEvent.type(screen.getByRole('textbox', { name: 'New display name' }), 'Renamed 저장소')
    await userEvent.click(screen.getByRole('button', { name: 'Prepare rename' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('preview failed')
    view.rerender(<I18nProvider><Repositories bootstrap={unavailableBootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    expect(screen.getByRole('button', { name: 'Prepare rename' })).toBeDisabled()
    expect(screen.getByText(hint)).toBeInTheDocument()
    expect(screen.queryByText(otherHint)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Confirm rename' })).not.toBeInTheDocument()
  })

  it('does not expose a new rename or preview while rename availability is false', async () => {
    const calls: string[] = []
    const unavailable = { ...renameBootstrap, roborev: { ...renameBootstrap.roborev, status: { running: false } } }
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      calls.push(String(input))
      return response(trackedInventory)
    }))
    render(<I18nProvider><Repositories bootstrap={unavailable} registerRepository={async () => true} busy={false} /></I18nProvider>)
    await waitFor(() => expect(screen.getByText('RoboRev inventory unavailable')).toBeInTheDocument())
    expect(screen.queryByRole('button', { name: 'Rename' })).not.toBeInTheDocument()
    expect(calls).toEqual([])
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('confirms once, closes on verified success, toasts, and refreshes inventory independently', async () => {
    const calls: string[] = []
    let inventoryCount = 0
    const toast = vi.fn()
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input)
      calls.push(url)
      if (url === '/api/repository-inventory') { inventoryCount += 1; return response({ repositories: [{ name: inventoryCount > 1 ? 'Renamed 저장소' : 'tracked', path: 'E:\\tracked', jobCount: 1, dashboardRegistered: true }], repositoryCount: 1, totalJobCount: 1 }) }
      if (url.endsWith('/preview')) return response(renamePreview)
      return response({ status: 'renamed', path: 'E:\\tracked', previousName: 'tracked', name: 'Renamed 저장소' })
    }))
    render(<I18nProvider><Repositories bootstrap={renameBootstrap} registerRepository={async () => true} busy={false} onToast={toast} /></I18nProvider>)
    await userEvent.click(await screen.findByRole('button', { name: 'Rename' }))
    await userEvent.type(screen.getByRole('textbox', { name: 'New display name' }), 'Renamed 저장소')
    await userEvent.click(screen.getByRole('button', { name: 'Prepare rename' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Confirm rename' }))
    await waitFor(() => expect(toast).toHaveBeenCalledWith('success', 'Repository display name changed.'))
    expect(calls.filter(url => url === '/api/repository-renames')).toHaveLength(1)
    expect(calls.filter(url => url === '/api/repository-inventory')).toHaveLength(2)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('keeps ready confirm enabled when availability is false and sends exactly one request', async () => {
    const calls: string[] = []
    const unavailable = { ...renameBootstrap, roborev: { ...renameBootstrap.roborev, status: { running: false } } }
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input)
      calls.push(url)
      if (url === '/api/repository-inventory') return response(trackedInventory)
      if (url.endsWith('/preview')) return response(renamePreview)
      return response({ status: 'unverified', path: 'E:\\tracked', message: 'Readback unavailable' })
    }))
    const view = render(<I18nProvider><Repositories bootstrap={renameBootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    await prepareReadyRename()
    view.rerender(<I18nProvider><Repositories bootstrap={unavailable} registerRepository={async () => true} busy={false} /></I18nProvider>)
    expect(screen.getByRole('button', { name: 'Confirm rename' })).toBeEnabled()
    await userEvent.click(screen.getByRole('button', { name: 'Confirm rename' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Readback unavailable')
    expect(calls.filter(url => url === '/api/repository-renames')).toHaveLength(1)
  })

  it('recovers runtime availability only through an explicit new preview without an automatic preview', async () => {
    let previewCount = 0
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input)
      calls.push(url)
      if (url === '/api/repository-inventory') return response(trackedInventory)
      if (url.endsWith('/preview')) { previewCount += 1; return response(renamePreview) }
      return response({ error: 'RoboRev is unavailable' }, 503)
    }))
    const view = render(<I18nProvider><Repositories bootstrap={renameBootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    await prepareReadyRename()
    view.rerender(<I18nProvider><Repositories bootstrap={renameOfflineBootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Confirm rename' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('RoboRev is unavailable')
    expect(screen.getByText('RoboRev is currently unavailable. When it becomes available again, prepare a new Rename Preview.')).toBeInTheDocument()
    expect(screen.queryByText('The installed RoboRev does not support repository rename.')).not.toBeInTheDocument()
    view.rerender(<I18nProvider><Repositories bootstrap={renameBootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    expect(screen.getByRole('button', { name: 'Prepare rename' })).toBeEnabled()
    expect(previewCount).toBe(1)
    await userEvent.click(screen.getByRole('button', { name: 'Prepare rename' }))
    await waitFor(() => expect(previewCount).toBe(2))
    expect(calls.filter(url => url.endsWith('/preview'))).toHaveLength(2)
  })

  it('disables prepare after editing a ready preview when availability is false', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input)
      calls.push(url)
      if (url === '/api/repository-inventory') return response(trackedInventory)
      if (url.endsWith('/preview')) return response(renamePreview)
      return response({ status: 'unverified', path: 'E:\\tracked', message: 'Readback unavailable' })
    }))
    const view = render(<I18nProvider><Repositories bootstrap={renameBootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    await prepareReadyRename()
    const input = screen.getByRole('textbox', { name: 'New display name' })
    await userEvent.clear(input)
    await userEvent.type(input, 'Edited name')
    expect(screen.queryByRole('button', { name: 'Confirm rename' })).not.toBeInTheDocument()
    view.rerender(<I18nProvider><Repositories bootstrap={renameDefensiveMixedBootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    expect(screen.getByRole('button', { name: 'Prepare rename' })).toBeDisabled()
    expect(screen.getByText('The installed RoboRev does not support repository rename.')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Prepare rename' }))
    expect(calls.filter(url => url.endsWith('/preview'))).toHaveLength(1)
  })

  it('guards a double confirm click to exactly one confirm request', async () => {
    const calls: string[] = []
    let resolveConfirm!: (value: Response) => void
    const confirmResponse = new Promise<Response>(resolve => { resolveConfirm = resolve })
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input); calls.push(url)
      if (url === '/api/repository-inventory') return response({ repositories: [{ name: 'tracked', path: 'E:\\tracked', jobCount: 1, dashboardRegistered: true }], repositoryCount: 1, totalJobCount: 1 })
      if (url.endsWith('/preview')) return response(renamePreview)
      return confirmResponse
    }))
    render(<I18nProvider><Repositories bootstrap={renameBootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    await userEvent.click(await screen.findByRole('button', { name: 'Rename' }))
    await userEvent.type(screen.getByRole('textbox', { name: 'New display name' }), 'Renamed 저장소')
    await userEvent.click(screen.getByRole('button', { name: 'Prepare rename' }))
    const confirm = await screen.findByRole('button', { name: 'Confirm rename' })
    fireEvent.click(confirm)
    fireEvent.click(confirm)
    await waitFor(() => expect(calls.filter(url => url === '/api/repository-renames')).toHaveLength(1))
    resolveConfirm(await response({ status: 'unverified', path: 'E:\\tracked', message: 'Readback unavailable' }))
  })

  it('keeps an accepted confirm alive when inventory availability is lost and renders Renaming until it resolves', async () => {
    let confirmSignal: AbortSignal | undefined
    let resolveConfirm!: (value: Response) => void
    let inventoryReads = 0
    const confirmResponse = new Promise<Response>(resolve => { resolveConfirm = resolve })
    const toast = vi.fn()
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url === '/api/repository-inventory') {
        inventoryReads += 1
        return response({ repositories: [{ name: 'tracked', path: 'E:\\tracked', jobCount: 1, dashboardRegistered: true }], repositoryCount: 1, totalJobCount: 1 })
      }
      if (url.endsWith('/preview')) return response(renamePreview)
      confirmSignal = init?.signal ?? undefined
      return confirmResponse
    }))
    const unavailable = { ...renameBootstrap, roborev: { ...renameBootstrap.roborev, status: { running: false } } }
    const view = render(<I18nProvider><Repositories bootstrap={renameBootstrap} registerRepository={async () => true} busy={false} onToast={toast} /></I18nProvider>)
    await userEvent.click(await screen.findByRole('button', { name: 'Rename' }))
    await userEvent.type(screen.getByRole('textbox', { name: 'New display name' }), 'Renamed 저장소')
    await userEvent.click(screen.getByRole('button', { name: 'Prepare rename' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Confirm rename' }))
    await waitFor(() => expect(confirmSignal).toBeDefined())
    expect(screen.getByRole('button', { name: 'Renaming…' })).toBeDisabled()
    expect(screen.queryByRole('button', { name: 'Prepare rename' })).not.toBeInTheDocument()
    view.rerender(<I18nProvider><Repositories bootstrap={unavailable} registerRepository={async () => true} busy={false} onToast={toast} /></I18nProvider>)
    expect(confirmSignal?.aborted).toBe(false)
    expect(screen.getByRole('button', { name: 'Renaming…' })).toBeDisabled()
    expect(screen.queryByRole('button', { name: 'Prepare rename' })).not.toBeInTheDocument()
    resolveConfirm(await response({ status: 'renamed', path: 'E:\\tracked', previousName: 'tracked', name: 'Renamed 저장소' }))
    await waitFor(() => expect(toast).toHaveBeenCalledWith('success', 'Repository display name changed.'))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(inventoryReads).toBe(1)
    expect(screen.getByText('RoboRev inventory unavailable')).toBeInTheDocument()
  })

  it('aborts inventory on availability loss without aborting an independent preview', async () => {
    let inventorySignal: AbortSignal | undefined
    let previewSignal: AbortSignal | undefined
    let resolvePreview!: (value: Response) => void
    let inventoryReads = 0
    let confirmCalls = 0
    const previewResponse = new Promise<Response>(resolve => { resolvePreview = resolve })
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url === '/api/repository-inventory') {
        inventoryReads += 1
        if (inventoryReads === 1) return response({ repositories: [{ name: 'tracked', path: 'E:\\tracked', jobCount: 1, dashboardRegistered: true }], repositoryCount: 1, totalJobCount: 1 })
        inventorySignal = init?.signal ?? undefined
        return new Promise<Response>(() => {})
      }
      if (url.endsWith('/preview')) {
        previewSignal = init?.signal ?? undefined
        return previewResponse
      }
      confirmCalls += 1
      return response({ status: 'renamed', path: 'E:\\tracked', previousName: 'tracked', name: 'Renamed 저장소' })
    }))
    const unavailable = { ...renameBootstrap, roborev: { ...renameBootstrap.roborev, status: { running: false } } }
    const view = render(<I18nProvider><Repositories bootstrap={renameBootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    await screen.findByText('tracked')
    await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(inventorySignal).toBeDefined())
    await userEvent.click(await screen.findByRole('button', { name: 'Rename' }))
    await userEvent.type(screen.getByRole('textbox', { name: 'New display name' }), 'Renamed 저장소')
    await userEvent.click(screen.getByRole('button', { name: 'Prepare rename' }))
    await waitFor(() => expect(previewSignal).toBeDefined())
    view.rerender(<I18nProvider><Repositories bootstrap={unavailable} registerRepository={async () => true} busy={false} /></I18nProvider>)
    expect(inventorySignal?.aborted).toBe(true)
    expect(previewSignal?.aborted).toBe(false)
    resolvePreview(await response(renamePreview))
    await userEvent.click(await screen.findByRole('button', { name: 'Confirm rename' }))
    await waitFor(() => expect(confirmCalls).toBe(1))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('keeps an unverified confirm terminal after availability loss without retrying or refreshing', async () => {
    let confirmSignal: AbortSignal | undefined
    let resolveConfirm!: (value: Response) => void
    let inventoryReads = 0
    const confirmResponse = new Promise<Response>(resolve => { resolveConfirm = resolve })
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      calls.push(url)
      if (url === '/api/repository-inventory') {
        inventoryReads += 1
        return response({ repositories: [{ name: 'tracked', path: 'E:\\tracked', jobCount: 1, dashboardRegistered: true }], repositoryCount: 1, totalJobCount: 1 })
      }
      if (url.endsWith('/preview')) return response(renamePreview)
      confirmSignal = init?.signal ?? undefined
      return confirmResponse
    }))
    const unavailable = { ...renameBootstrap, roborev: { ...renameBootstrap.roborev, status: { running: false } } }
    const view = render(<I18nProvider><Repositories bootstrap={renameBootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    await userEvent.click(await screen.findByRole('button', { name: 'Rename' }))
    await userEvent.type(screen.getByRole('textbox', { name: 'New display name' }), 'Renamed 저장소')
    await userEvent.click(screen.getByRole('button', { name: 'Prepare rename' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Confirm rename' }))
    await waitFor(() => expect(confirmSignal).toBeDefined())
    view.rerender(<I18nProvider><Repositories bootstrap={unavailable} registerRepository={async () => true} busy={false} /></I18nProvider>)
    expect(confirmSignal?.aborted).toBe(false)
    expect(screen.getByRole('button', { name: 'Renaming…' })).toBeDisabled()
    expect(screen.queryByRole('button', { name: 'Prepare rename' })).not.toBeInTheDocument()
    resolveConfirm(await response({ status: 'unverified', path: 'E:\\tracked', message: 'Readback unavailable' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Readback unavailable')
    expect(screen.getByRole('button', { name: 'Refresh' })).toBeInTheDocument()
    expect(calls.filter(url => url === '/api/repository-inventory')).toHaveLength(1)
    expect(screen.queryByRole('button', { name: 'Renaming…' })).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Close' }))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(inventoryReads).toBe(1)
  })

  it('releases confirm ownership after a rejected request so the rename can recover', async () => {
    let previewCount = 0
    let confirmCount = 0
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input)
      if (url === '/api/repository-inventory') return response({ repositories: [{ name: 'tracked', path: 'E:\\tracked', jobCount: 1, dashboardRegistered: true }], repositoryCount: 1, totalJobCount: 1 })
      if (url.endsWith('/preview')) {
        previewCount += 1
        return response(renamePreview)
      }
      confirmCount += 1
      return confirmCount === 1 ? Promise.reject(new Error('network failed')) : response({ status: 'unverified', path: 'E:\\tracked', message: 'Readback unavailable' })
    }))
    render(<I18nProvider><Repositories bootstrap={renameBootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    await userEvent.click(await screen.findByRole('button', { name: 'Rename' }))
    await userEvent.type(screen.getByRole('textbox', { name: 'New display name' }), 'Renamed 저장소')
    await userEvent.click(screen.getByRole('button', { name: 'Prepare rename' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Confirm rename' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('network failed')
    expect(screen.getByRole('button', { name: 'Prepare rename' })).toBeEnabled()
    await userEvent.click(screen.getByRole('button', { name: 'Prepare rename' }))
    await waitFor(() => expect(previewCount).toBe(2))
    await userEvent.click(await screen.findByRole('button', { name: 'Confirm rename' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Readback unavailable')
    expect(confirmCount).toBe(2)
  })

  it('allows the browser confirm request to be aborted only by actual unmount', async () => {
    let confirmSignal: AbortSignal | undefined
    let resolveConfirm!: (value: Response) => void
    const confirmResponse = new Promise<Response>(resolve => { resolveConfirm = resolve })
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url === '/api/repository-inventory') return response({ repositories: [{ name: 'tracked', path: 'E:\\tracked', jobCount: 1, dashboardRegistered: true }], repositoryCount: 1, totalJobCount: 1 })
      if (url.endsWith('/preview')) return response(renamePreview)
      confirmSignal = init?.signal ?? undefined
      return confirmResponse
    }))
    const view = render(<I18nProvider><Repositories bootstrap={renameBootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    await userEvent.click(await screen.findByRole('button', { name: 'Rename' }))
    await userEvent.type(screen.getByRole('textbox', { name: 'New display name' }), 'Renamed 저장소')
    await userEvent.click(screen.getByRole('button', { name: 'Prepare rename' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Confirm rename' }))
    await waitFor(() => expect(confirmSignal).toBeDefined())
    expect(confirmSignal?.aborted).toBe(false)
    view.unmount()
    expect(confirmSignal?.aborted).toBe(true)
    resolveConfirm(await response({ status: 'unverified', path: 'E:\\tracked', message: 'Readback unavailable' }))
  })

  it('shows preview failure as a definite error and never offers confirm', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input); calls.push(url)
      if (url === '/api/repository-inventory') return response({ repositories: [{ name: 'tracked', path: 'E:\\tracked', jobCount: 1, dashboardRegistered: true }], repositoryCount: 1, totalJobCount: 1 })
      return response({ error: 'preview failed' }, 502)
    }))
    render(<I18nProvider><Repositories bootstrap={renameBootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    await userEvent.click(await screen.findByRole('button', { name: 'Rename' }))
    await userEvent.type(screen.getByRole('textbox', { name: 'New display name' }), 'Renamed 저장소')
    await userEvent.click(screen.getByRole('button', { name: 'Prepare rename' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('preview failed')
    expect(screen.queryByRole('button', { name: 'Confirm rename' })).not.toBeInTheDocument()
    expect(calls.filter(url => url === '/api/repository-renames')).toHaveLength(0)
  })

  it('shows confirm failure without retrying or refreshing inventory', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input); calls.push(url)
      if (url === '/api/repository-inventory') return response({ repositories: [{ name: 'tracked', path: 'E:\\tracked', jobCount: 1, dashboardRegistered: true }], repositoryCount: 1, totalJobCount: 1 })
      if (url.endsWith('/preview')) return response(renamePreview)
      return response({ error: 'confirm failed' }, 502)
    }))
    render(<I18nProvider><Repositories bootstrap={renameBootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    await userEvent.click(await screen.findByRole('button', { name: 'Rename' }))
    await userEvent.type(screen.getByRole('textbox', { name: 'New display name' }), 'Renamed 저장소')
    await userEvent.click(screen.getByRole('button', { name: 'Prepare rename' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Confirm rename' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('confirm failed')
    expect(calls.filter(url => url === '/api/repository-renames')).toHaveLength(1)
    expect(calls.filter(url => url === '/api/repository-inventory')).toHaveLength(1)
    expect(screen.queryByRole('button', { name: 'Confirm rename' })).not.toBeInTheDocument()
  })

  it('keeps registered cards unchanged when verified success is followed by inventory failure', async () => {
    const calls: string[] = []
    const toast = vi.fn()
    let inventoryReads = 0
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input); calls.push(url)
      if (url === '/api/repository-inventory') {
        inventoryReads += 1
        return inventoryReads === 1
          ? response({ repositories: [{ name: 'tracked', path: 'E:\\tracked', jobCount: 1, dashboardRegistered: true }], repositoryCount: 1, totalJobCount: 1 })
          : response({ error: 'refresh failed' }, 503)
      }
      if (url.endsWith('/preview')) return response(renamePreview)
      return response({ status: 'renamed', path: 'E:\\tracked', previousName: 'tracked', name: 'Renamed 저장소' })
    }))
    render(<I18nProvider><Repositories bootstrap={renameBootstrap} registerRepository={async () => true} busy={false} onToast={toast} /></I18nProvider>)
    await userEvent.click(await screen.findByRole('button', { name: 'Rename' }))
    await userEvent.type(screen.getByRole('textbox', { name: 'New display name' }), 'Renamed 저장소')
    await userEvent.click(screen.getByRole('button', { name: 'Prepare rename' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Confirm rename' }))
    await waitFor(() => expect(toast).toHaveBeenCalledWith('success', 'Repository display name changed.'))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(screen.getByText('registered')).toBeInTheDocument()
    expect(document.querySelector('.repo-cards article button')).toBeNull()
    expect(await screen.findByRole('alert')).toHaveTextContent('refresh failed')
    expect(calls.filter(url => url === '/api/repository-inventory')).toHaveLength(2)
  })

  it('leaves registered cards without rename controls while tracked rows have one control each', async () => {
    vi.stubGlobal('fetch', vi.fn(() => response({ repositories: [{ name: 'tracked', path: 'E:\\tracked', jobCount: 1, dashboardRegistered: true }], repositoryCount: 1, totalJobCount: 1 })))
    render(<I18nProvider><Repositories bootstrap={renameBootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    expect(await screen.findByText('registered')).toBeInTheDocument()
    expect(document.querySelector('.repo-cards article button')).toBeNull()
    expect(screen.getAllByRole('button', { name: 'Rename' })).toHaveLength(1)
  })

  it('keeps unverified warning without retry or automatic refresh and offers explicit refresh', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input); calls.push(url)
      if (url === '/api/repository-inventory') return response({ repositories: [{ name: 'tracked', path: 'E:\\tracked', jobCount: 1, dashboardRegistered: true }], repositoryCount: 1, totalJobCount: 1 })
      if (url.endsWith('/preview')) return response(renamePreview)
      return response({ status: 'unverified', path: 'E:\\tracked', message: 'Readback unavailable' })
    }))
    render(<I18nProvider><Repositories bootstrap={renameBootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    await userEvent.click(await screen.findByRole('button', { name: 'Rename' }))
    await userEvent.type(screen.getByRole('textbox', { name: 'New display name' }), 'Renamed 저장소')
    await userEvent.click(screen.getByRole('button', { name: 'Prepare rename' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Confirm rename' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Readback unavailable')
    expect(calls.filter(url => url === '/api/repository-inventory')).toHaveLength(1)
    expect(calls.filter(url => url === '/api/repository-renames')).toHaveLength(1)
    await userEvent.click(screen.getAllByRole('button', { name: 'Refresh' })[1])
    await waitFor(() => expect(calls.filter(url => url === '/api/repository-inventory')).toHaveLength(2))
  })

  it('shows Move only for stale inventory rows when the exact v0.64 move capability is available', async () => {
    vi.stubGlobal('fetch', vi.fn(() => response({ repositories: [
      { name: 'stale', path: 'E:/old/project', jobCount: 2, dashboardRegistered: false },
      { name: 'registered', path: 'E:/new/project', jobCount: 3, dashboardRegistered: true },
    ], repositoryCount: 2, totalJobCount: 5 })))
    render(<I18nProvider><Repositories bootstrap={moveBootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    await screen.findByText('stale')
    expect(screen.getAllByRole('button', { name: 'Move' })).toHaveLength(1)
    const rows = document.querySelectorAll('.tracked-table tbody tr')
    expect(rows).toHaveLength(2)
    expect(rows[0].querySelectorAll('td')).toHaveLength(rows[1].querySelectorAll('td').length)
    expect(rows[1].querySelector('.tracked-move')).toBeNull()
    await userEvent.click(screen.getByRole('button', { name: 'Move' }))
    expect(screen.getByRole('dialog', { name: 'Repair RoboRev repository path' })).toBeInTheDocument()
  })

  it('keeps verified Move success and toast when the follow-up inventory refresh fails', async () => {
    let inventoryReads = 0
    const calls: string[] = []
    const toast = vi.fn()
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const url = String(input)
      calls.push(url)
      if (url === '/api/repository-inventory') {
        inventoryReads += 1
        return inventoryReads === 1
          ? response({ repositories: [{ name: 'stale', path: 'E:/stale', jobCount: 2, dashboardRegistered: false }], repositoryCount: 1, totalJobCount: 2 })
          : response({ error: 'refresh failed' }, 503)
      }
      if (url.endsWith('/preview')) return response(movePreview)
      return response({ status: 'moved', sourcePath: 'E:/stale', destinationPath: 'E:\\repo' })
    }))
    render(<I18nProvider><Repositories bootstrap={moveBootstrap} registerRepository={async () => true} busy={false} onToast={toast} /></I18nProvider>)
    await userEvent.click(await screen.findByRole('button', { name: 'Move' }))
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Registered destination' }), 'repo-1')
    await userEvent.click(screen.getByRole('button', { name: 'Prepare move' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Confirm move' }))
    await waitFor(() => expect(toast).toHaveBeenCalledWith('success', 'RoboRev repository path repaired.'))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(await screen.findByRole('alert')).toHaveTextContent('refresh failed')
    expect(calls.filter(url => url === '/api/repository-moves')).toHaveLength(1)
  })
})
