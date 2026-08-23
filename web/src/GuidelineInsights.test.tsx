import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { GuidelineInsights } from './GuidelineInsights'
import { I18nProvider, LanguageSelector, LANGUAGE_STORAGE_KEY } from './i18n'

const identity = 'repo-a'
const preview = { repositoryIdentity: identity, repositoryName: 'repo', window: '30d', branchScope: 'all', command: { executable: 'roborev.exe', args: ['insights', '--repo', 'E:\\repo', '--since', '30d', '--json'] }, warnings: ['quota_cost', 'read_only'], token: 'token', expiresAt: '2026-08-23T12:03:00Z' }
function response(value: unknown, status = 200) { return Promise.resolve(new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } })) }
function renderCard(props: Partial<React.ComponentProps<typeof GuidelineInsights>> = {}) { return render(<I18nProvider><GuidelineInsights selectedRepositoryIdentity={identity} capabilities={{ guidelineInsights: true }} daemonRunning {...props} /></I18nProvider>) }
afterEach(() => { cleanup(); vi.restoreAllMocks(); localStorage.removeItem(LANGUAGE_STORAGE_KEY) })

describe('GuidelineInsights', () => {
  it('does not launch or fetch on mount, scope changes, suspension, or locale-independent rerenders', async () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    const { rerender } = renderCard()
    expect(fetchMock).not.toHaveBeenCalled()
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Insights window' }), '90d')
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Insights branch scope' }), 'current')
    expect(fetchMock).not.toHaveBeenCalled()
    rerender(<I18nProvider><GuidelineInsights selectedRepositoryIdentity={identity} capabilities={{ guidelineInsights: true }} daemonRunning suspended /></I18nProvider>)
    expect(fetchMock).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: 'Prepare Insights' })).toBeDisabled()
  })

  it('does not POST when only the selected repository changes', () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    const { rerender } = renderCard()
    rerender(<I18nProvider><GuidelineInsights selectedRepositoryIdentity="repo-b" capabilities={{ guidelineInsights: true }} daemonRunning /></I18nProvider>)
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('invalidates a late preview response when repository switching starts', async () => {
    let resolvePreview: ((value: Response) => void) | undefined
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(resolve => { resolvePreview = resolve })))
    const { rerender } = renderCard()
    await userEvent.click(screen.getByRole('button', { name: 'Prepare Insights' }))
    rerender(<I18nProvider><GuidelineInsights selectedRepositoryIdentity={identity} capabilities={{ guidelineInsights: true }} daemonRunning repoSwitching /></I18nProvider>)
    resolvePreview?.(new Response(JSON.stringify(preview), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('does not display a late confirm response after repository switching starts', async () => {
    let resolveLaunch: ((value: Response) => void) | undefined
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => String(input) === '/api/insights/preview'
      ? response(preview)
      : new Promise<Response>(resolve => { resolveLaunch = resolve })))
    const { rerender } = renderCard()
    await userEvent.click(screen.getByRole('button', { name: 'Prepare Insights' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Run Insights' }))
    rerender(<I18nProvider><GuidelineInsights selectedRepositoryIdentity={identity} capabilities={{ guidelineInsights: true }} daemonRunning repoSwitching /></I18nProvider>)
    resolveLaunch?.(new Response(JSON.stringify({ status: 'queued', jobId: 42 }), { status: 202, headers: { 'Content-Type': 'application/json' } }))
    await waitFor(() => expect(screen.queryByText(/Insights job #42/)).not.toBeInTheDocument())
  })

  it('cancels a prepared modal without launching and allows a fresh prepare', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => String(input) === '/api/insights/preview' ? response(preview) : response({ status: 'queued', jobId: 42, agent: 'codex', since: '2026-08-23T12:00:00Z' }, 202))
    vi.stubGlobal('fetch', fetchMock)
    renderCard()
    await userEvent.click(screen.getByRole('button', { name: 'Prepare Insights' }))
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(fetchMock.mock.calls.some(call => String(call[0]) === '/api/insights')).toBe(false)
    await userEvent.click(screen.getByRole('button', { name: 'Prepare Insights' }))
    expect(fetchMock.mock.calls.filter(call => String(call[0]) === '/api/insights/preview')).toHaveLength(2)
  })

  it('prepares first, shows the exact command and warnings, then confirms once', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      calls.push(path)
      if (path === '/api/insights/preview') return response(preview)
      if (path === '/api/insights') return response({ status: 'queued', jobId: 42, agent: 'codex', since: '2026-08-23T12:00:00Z' }, 202)
      return response({ jobId: 42, status: 'running' })
    }))
    renderCard()
    await userEvent.click(screen.getByRole('button', { name: 'Prepare Insights' }))
    expect(await screen.findByText('roborev.exe')).toBeInTheDocument()
    expect(screen.getByText(JSON.stringify('insights'))).toBeInTheDocument()
    expect(screen.getByText(JSON.stringify('--repo'))).toBeInTheDocument()
    expect(screen.getAllByText('LLM quota or provider cost may be used.').length).toBeGreaterThanOrEqual(2)
    expect(screen.getByText('Use RoboRev configured default agent/model/reasoning')).toBeInTheDocument()
    const run = screen.getByRole('button', { name: 'Run Insights' })
    await userEvent.click(run)
    await userEvent.click(run)
    await waitFor(() => expect(calls.filter(path => path === '/api/insights')).toHaveLength(1))
    expect(calls.filter(path => path === '/api/insights/preview')).toHaveLength(1)
    await waitFor(() => expect(screen.getByRole('combobox', { name: 'Insights window' })).toBeDisabled())
  })

  it('shows a localized unverified outcome for a non-aborted confirm failure without retrying', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/insights/preview') return response(preview)
      return Promise.reject(new Error('backend English failure'))
    })
    vi.stubGlobal('fetch', fetchMock)
    renderCard()
    await userEvent.click(screen.getByRole('button', { name: 'Prepare Insights' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Run Insights' }))
    expect(await screen.findByText('Launch outcome could not be verified. A RoboRev Insights job may already have been queued. Do not retry automatically; prepare a new run if needed.')).toBeInTheDocument()
    expect(screen.queryByText('backend English failure')).not.toBeInTheDocument()
    expect(fetchMock.mock.calls.filter(call => String(call[0]) === '/api/insights')).toHaveLength(1)
    expect(screen.getByRole('button', { name: 'Prepare Insights' })).toBeEnabled()
  })

  it.each(['expired', 'branch drift', 'daemon rejected'] as const)('treats a definite %s API rejection as an error instead of unverified', async reason => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => String(input) === '/api/insights/preview'
      ? response(preview)
      : response({ error: reason }, reason === 'daemon rejected' ? 503 : 409))
    vi.stubGlobal('fetch', fetchMock)
    renderCard()
    await userEvent.click(screen.getByRole('button', { name: 'Prepare Insights' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Run Insights' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(reason)
    expect(screen.queryByText('Launch outcome could not be verified. A RoboRev Insights job may already have been queued. Do not retry automatically; prepare a new run if needed.')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Prepare Insights' })).toBeEnabled()
    expect(fetchMock.mock.calls.filter(call => String(call[0]) === '/api/insights')).toHaveLength(1)
  })

  it.each([
    ['authoritative 202 unverified', response({ status: 'unverified', message: 'server could not confirm' }, 202)],
    ['malformed successful response', response({}, 202)],
  ] as const)('keeps %s as an unverified launch outcome', async (_label, launchResponse) => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => String(input) === '/api/insights/preview' ? response(preview) : launchResponse)
    vi.stubGlobal('fetch', fetchMock)
    renderCard()
    await userEvent.click(screen.getByRole('button', { name: 'Prepare Insights' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Run Insights' }))
    expect(await screen.findByText('Launch outcome could not be verified. A RoboRev Insights job may already have been queued. Do not retry automatically; prepare a new run if needed.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Prepare Insights' })).toBeEnabled()
    expect(fetchMock.mock.calls.filter(call => String(call[0]) === '/api/insights')).toHaveLength(1)
  })

  it('tracks a valid receipt through running to done and renders bounded Markdown', async () => {
    let statusCalls = 0
    const realSetTimeout = globalThis.setTimeout
    vi.spyOn(globalThis, 'setTimeout').mockImplementation(((callback: TimerHandler, delay?: number, ...args: any[]) => {
      if (delay === 2000) { queueMicrotask(() => { if (typeof callback === 'function') callback(...args) }); return 0 as any }
      return realSetTimeout(callback, delay, ...args)
    }) as typeof setTimeout)
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/insights/preview') return response(preview)
      if (path === '/api/insights') return response({ status: 'queued', jobId: 42, agent: 'codex', since: '2026-08-23T12:00:00Z' }, 202)
      if (path.includes('/result')) return response({ jobId: 42, status: 'done', markdown: '# Recommendations' })
      statusCalls++
      return response({ jobId: 42, status: statusCalls === 1 ? 'running' : 'done' })
    })
    vi.stubGlobal('fetch', fetchMock)
    renderCard()
    await userEvent.click(screen.getByRole('button', { name: 'Prepare Insights' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Run Insights' }))
    await waitFor(() => expect(statusCalls).toBeGreaterThanOrEqual(2))
    expect(screen.getByText('Done')).toBeInTheDocument()
    expect(await screen.findByText('Recommendations')).toBeInTheDocument()
    expect(screen.getByText('Current Insights job: #42. The daemon continues independently of this page.')).toBeInTheDocument()
  })

  it('loads a terminal result separately and allows a fresh Prepare without hanging', async () => {
    let resultCalls = 0
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/insights/preview') return response(preview)
      if (path === '/api/insights') return response({ status: 'queued', jobId: 42, agent: 'codex', since: '2026-08-23T12:00:00Z' }, 202)
      if (path.includes('/result')) { resultCalls++; return response({ jobId: 42, status: 'done', markdown: '# Ready' }) }
      return response({ jobId: 42, status: 'done' })
    })
    vi.stubGlobal('fetch', fetchMock)
    renderCard()
    await userEvent.click(screen.getByRole('button', { name: 'Prepare Insights' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Run Insights' }))
    expect(await screen.findByText('Ready')).toBeInTheDocument()
    expect(resultCalls).toBe(1)
    await userEvent.click(screen.getByRole('button', { name: 'Prepare Insights' }))
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
    expect(screen.queryByText('Loading…')).not.toBeInTheDocument()
    expect(fetchMock.mock.calls.filter(call => String(call[0]) === '/api/insights')).toHaveLength(1)
  })

  it.each(['failed', 'canceled'] as const)('treats %s as terminal and stops polling', async terminal => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      calls.push(path)
      if (path === '/api/insights/preview') return response(preview)
      if (path === '/api/insights') return response({ status: 'queued', jobId: 42, agent: 'codex', since: '2026-08-23T12:00:00Z' }, 202)
      return response({ jobId: 42, status: terminal })
    }))
    renderCard()
    await userEvent.click(screen.getByRole('button', { name: 'Prepare Insights' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Run Insights' }))
    await waitFor(() => expect(screen.getByText(terminal === 'canceled' ? 'Canceled' : 'Failed')).toBeInTheDocument())
    expect(screen.getByText('Current Insights job: #42. The daemon continues independently of this page.')).toBeInTheDocument()
    const statusCount = calls.filter(path => path.includes('/api/insights/42?')).length
    await new Promise(resolve => setTimeout(resolve, 30))
    expect(calls.filter(path => path.includes('/api/insights/42?')).length).toBe(statusCount)
  })

  it.each(['failed', 'canceled'] as const)('allows a fresh Prepare after a %s terminal state', async terminal => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/insights/preview') return response(preview)
      if (path === '/api/insights') return response({ status: 'queued', jobId: 42, agent: 'codex', since: '2026-08-23T12:00:00Z' }, 202)
      return response({ jobId: 42, status: terminal })
    })
    vi.stubGlobal('fetch', fetchMock)
    renderCard()
    await userEvent.click(screen.getByRole('button', { name: 'Prepare Insights' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Run Insights' }))
    await screen.findByText(terminal === 'canceled' ? 'Canceled' : 'Failed')
    await userEvent.click(screen.getByRole('button', { name: 'Prepare Insights' }))
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
    expect(screen.queryByText('Loading…')).not.toBeInTheDocument()
    expect(fetchMock.mock.calls.filter(call => String(call[0]) === '/api/insights/preview')).toHaveLength(2)
  })

  it('retains Done after a result GET failure and retries the same result without another enqueue', async () => {
    let resultCalls = 0
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/insights/preview') return response(preview)
      if (path === '/api/insights') return response({ status: 'queued', jobId: 42, agent: 'codex', since: '2026-08-23T12:00:00Z' }, 202)
      if (path.includes('/result')) {
        resultCalls++
        return resultCalls === 1 ? response({ error: 'result temporarily unavailable' }, 503) : response({ jobId: 42, status: 'done', markdown: '# Recovered' })
      }
      return response({ jobId: 42, status: 'done' })
    })
    vi.stubGlobal('fetch', fetchMock)
    renderCard()
    await userEvent.click(screen.getByRole('button', { name: 'Prepare Insights' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Run Insights' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('result temporarily unavailable')
    expect(screen.getByText('Done')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Retry result' }))
    expect(await screen.findByText('Recovered')).toBeInTheDocument()
    expect(resultCalls).toBe(2)
    expect(fetchMock.mock.calls.filter(call => String(call[0]) === '/api/insights')).toHaveLength(1)
  })

  it('reports malformed result data without comparing localized strings', async () => {
    let resultCalls = 0
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/insights/preview') return response(preview)
      if (path === '/api/insights') return response({ status: 'queued', jobId: 42, agent: 'codex', since: '2026-08-23T12:00:00Z' }, 202)
      if (path.includes('/result')) {
        resultCalls++
        return response({ jobId: 43, status: 'done', markdown: '# Wrong job' })
      }
      return response({ jobId: 42, status: 'done' })
    })
    vi.stubGlobal('fetch', fetchMock)
    renderCard()
    await userEvent.click(screen.getByRole('button', { name: 'Prepare Insights' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Run Insights' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('The Dashboard could not validate the Insights response.')
    expect(screen.queryByText('Wrong job')).not.toBeInTheDocument()
    expect(resultCalls).toBe(1)
    expect(fetchMock.mock.calls.filter(call => String(call[0]) === '/api/insights')).toHaveLength(1)
  })

  it.each([
    ['a network exception', () => Promise.reject(new TypeError('network unavailable')), 'The Insights result could not be loaded. Try again when the daemon is available.'],
    ['an APIError response', () => response({ error: 'result endpoint rejected' }, 503), 'result endpoint rejected'],
  ] as const)('distinguishes %s from malformed result data', async (_label, resultResponse, expectedError) => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/insights/preview') return response(preview)
      if (path === '/api/insights') return response({ status: 'queued', jobId: 42, agent: 'codex', since: '2026-08-23T12:00:00Z' }, 202)
      if (path.includes('/result')) return resultResponse()
      return response({ jobId: 42, status: 'done' })
    })
    vi.stubGlobal('fetch', fetchMock)
    renderCard()
    await userEvent.click(screen.getByRole('button', { name: 'Prepare Insights' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Run Insights' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(expectedError)
    expect(screen.queryByText('The Dashboard could not validate the Insights response.')).not.toBeInTheDocument()
    expect(fetchMock.mock.calls.filter(call => String(call[0]) === '/api/insights')).toHaveLength(1)
  })

  it('shows a validation error and clears stale state for a malformed status', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/insights/preview') return response(preview)
      if (path === '/api/insights') return response({ status: 'queued', jobId: 42, agent: 'codex', since: '2026-08-23T12:00:00Z' }, 202)
      return response({ jobId: 42, status: 'not-a-status' })
    }))
    renderCard()
    await userEvent.click(screen.getByRole('button', { name: 'Prepare Insights' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Run Insights' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('The Dashboard could not validate the Insights response.')
    expect(screen.queryByText('Queued')).not.toBeInTheDocument()
    expect(screen.getByText('Current Insights job: #42. The daemon continues independently of this page.')).toBeInTheDocument()
  })

  it('treats skip as a normal empty outcome and does not poll or offer Apply/config actions', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => String(input) === '/api/insights/preview' ? response(preview) : response({ status: 'skipped', skipped: true, reason: 'no failing reviews', since: '2026-08-23T12:00:00Z' }, 202))
    vi.stubGlobal('fetch', fetchMock)
    renderCard()
    await userEvent.click(screen.getByRole('button', { name: 'Prepare Insights' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Run Insights' }))
    expect(await screen.findByText('No failing reviews found in this time window. No LLM job was launched.')).toBeInTheDocument()
    expect(fetchMock.mock.calls.some(call => String(call[0]).includes('/api/insights/42'))).toBe(false)
    expect(screen.queryByRole('button', { name: /Apply|Save/ })).not.toBeInTheDocument()
    expect(fetchMock.mock.calls.some(call => String(call[0]).includes('/api/configuration'))).toBe(false)
  })

  it('gates confirmation while suspended and does not launch on resume', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => String(input) === '/api/insights/preview' ? response(preview) : response({ status: 'queued', jobId: 42, agent: 'codex', since: '2026-08-23T12:00:00Z' }, 202))
    vi.stubGlobal('fetch', fetchMock)
    const { rerender } = renderCard()
    await userEvent.click(screen.getByRole('button', { name: 'Prepare Insights' }))
    rerender(<I18nProvider><GuidelineInsights selectedRepositoryIdentity={identity} capabilities={{ guidelineInsights: true }} daemonRunning suspended /></I18nProvider>)
    expect(screen.getByRole('button', { name: 'Run Insights' })).toBeDisabled()
    rerender(<I18nProvider><GuidelineInsights selectedRepositoryIdentity={identity} capabilities={{ guidelineInsights: true }} daemonRunning /></I18nProvider>)
    expect(fetchMock.mock.calls.some(call => String(call[0]) === '/api/insights')).toBe(false)
  })

  it('does not launch during locale, narrow viewport, or dark-mode rerenders', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => String(input) === '/api/insights/preview' ? response(preview) : response({ status: 'queued', jobId: 42, agent: 'codex', since: '2026-08-23T12:00:00Z' }, 202))
    vi.stubGlobal('fetch', fetchMock)
    const { rerender } = render(<I18nProvider><><LanguageSelector /><GuidelineInsights selectedRepositoryIdentity={identity} capabilities={{ guidelineInsights: true }} daemonRunning /></></I18nProvider>)
    await userEvent.click(screen.getByRole('button', { name: 'Prepare Insights' }))
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Select language' }), 'ko')
    document.documentElement.classList.add('dark')
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 390 })
    rerender(<I18nProvider><><LanguageSelector /><GuidelineInsights selectedRepositoryIdentity={identity} capabilities={{ guidelineInsights: true }} daemonRunning /></></I18nProvider>)
    expect(fetchMock.mock.calls.some(call => String(call[0]) === '/api/insights')).toBe(false)
    document.documentElement.classList.remove('dark')
  })

  it('discards stale status and result responses after a repository switch', async () => {
    let resolveStatus: ((value: Response) => void) | undefined
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/insights/preview') return response(preview)
      if (path === '/api/insights') return response({ status: 'queued', jobId: 42, agent: 'codex', since: '2026-08-23T12:00:00Z' }, 202)
      if (path.includes('/result')) return response({ jobId: 42, status: 'done', markdown: '# stale' })
      return new Promise<Response>(resolve => { resolveStatus = resolve })
    }))
    const { rerender } = renderCard()
    await userEvent.click(screen.getByRole('button', { name: 'Prepare Insights' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Run Insights' }))
    rerender(<I18nProvider><GuidelineInsights selectedRepositoryIdentity="repo-b" capabilities={{ guidelineInsights: true }} daemonRunning /></I18nProvider>)
    resolveStatus?.(new Response(JSON.stringify({ jobId: 42, status: 'done' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await waitFor(() => expect(screen.queryByText('stale')).not.toBeInTheDocument())
  })

  it('discards a late result response after a repository switch starts', async () => {
    let resolveResult: ((value: Response) => void) | undefined
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/insights/preview') return response(preview)
      if (path === '/api/insights') return response({ status: 'queued', jobId: 42, agent: 'codex', since: '2026-08-23T12:00:00Z' }, 202)
      if (path.includes('/result')) return new Promise<Response>(resolve => { resolveResult = resolve })
      return response({ jobId: 42, status: 'done' })
    }))
    const { rerender } = renderCard()
    await userEvent.click(screen.getByRole('button', { name: 'Prepare Insights' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Run Insights' }))
    await waitFor(() => expect(resolveResult).toBeDefined())
    rerender(<I18nProvider><GuidelineInsights selectedRepositoryIdentity="repo-b" capabilities={{ guidelineInsights: true }} daemonRunning /></I18nProvider>)
    resolveResult?.(new Response(JSON.stringify({ jobId: 42, status: 'done', markdown: '# stale result' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await waitFor(() => expect(screen.queryByText('stale result')).not.toBeInTheDocument())
  })

  it('discards a pending result synchronously when repoSwitching starts for the same identity', async () => {
    let resolveResult: ((value: Response) => void) | undefined
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path === '/api/insights/preview') return response(preview)
      if (path === '/api/insights') return response({ status: 'queued', jobId: 42, agent: 'codex', since: '2026-08-23T12:00:00Z' }, 202)
      if (path.includes('/result')) return new Promise<Response>(resolve => { resolveResult = resolve })
      return response({ jobId: 42, status: 'done' })
    }))
    const { rerender } = renderCard()
    await userEvent.click(screen.getByRole('button', { name: 'Prepare Insights' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Run Insights' }))
    await waitFor(() => expect(resolveResult).toBeDefined())
    rerender(<I18nProvider><GuidelineInsights selectedRepositoryIdentity={identity} capabilities={{ guidelineInsights: true }} daemonRunning repoSwitching /></I18nProvider>)
    resolveResult?.(new Response(JSON.stringify({ jobId: 42, status: 'done', markdown: '# stale same-scope result' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await waitFor(() => expect(screen.queryByText('stale same-scope result')).not.toBeInTheDocument())
  })
})
