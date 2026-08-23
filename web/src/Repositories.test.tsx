import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { Repositories } from './Repositories'
import type { Bootstrap } from './api'
import { I18nProvider, useI18n } from './i18n'

const capabilities = { reviewEnqueueApi: true, frozenRerunApi: true, jobListApi: true, jobOutputStatusApi: true, panelReview: true, fixCommand: true, panelMembersApi: true, promptLazyLoad: true, rawLog: true, repositoryInventory: true }
const bootstrap: Bootstrap = {
  csrfToken: 'csrf', selectedRepository: 'E:\\repo', selectedRepositoryIdentity: 'repo-1',
  repositories: [{ identity: 'repo-1', name: 'registered', path: 'E:\\repo', branch: 'main', head: 'abc', headShort: 'abc', commitSubject: 'Initial', dirty: false, hasConfig: true }],
  roborev: { installed: true, version: 'v0.64.0', status: { running: true }, capabilities },
}

function response(value: unknown, status = 200) {
  return Promise.resolve(new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } }))
}

function Harness({ registerRepository = async () => true }: { registerRepository?: (path: string) => Promise<boolean> }) {
  const { setLocale } = useI18n()
  return <><Repositories bootstrap={bootstrap} registerRepository={registerRepository} busy={false} /><button onClick={() => setLocale('ko')}>한국어</button></>
}

beforeEach(() => { window.localStorage.setItem('roborev-dashboard-language', 'en') })
afterEach(() => { cleanup(); vi.restoreAllMocks() })

describe('Repositories v2 inventory', () => {
  it('loads exactly once when mounted and preserves registered form/cards', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => { calls.push(String(input)); return response({ repositories: [{ name: 'tracked', path: 'E:\\tracked', jobCount: 2, dashboardRegistered: true }, { name: 'tracked', path: 'E:\\other', jobCount: 0, dashboardRegistered: false }], repositoryCount: 2, totalJobCount: 2 }) }))
    render(<I18nProvider><Repositories bootstrap={bootstrap} registerRepository={async () => true} busy={false} /></I18nProvider>)
    expect(await screen.findAllByText('tracked')).toHaveLength(2)
    expect(calls).toEqual(['/api/repository-inventory'])
    expect(screen.getByRole('textbox', { name: 'Add local Git repository' })).toBeInTheDocument()
    expect(screen.getByText('registered')).toBeInTheDocument()
    expect(screen.getByText('E:\\other')).toBeInTheDocument()
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
})
