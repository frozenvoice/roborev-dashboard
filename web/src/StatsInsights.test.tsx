import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { StatsInsights } from './StatsInsights'
import { I18nProvider, LANGUAGE_STORAGE_KEY, LanguageSelector } from './i18n'

const identity = 'repo-a'
const capabilities = { summaryStats: true, costStats: true }
const summary = {
  repositoryIdentity: identity, window: '7d', branchScope: 'all',
  summary: {
    since: '2026-08-16T00:00:00Z',
    overview: { total: 7, queued: 0, running: 0, done: 2, failed: 2, canceled: 0, applied: 1, rebased: 1 },
    verdicts: { total: 5, passed: 3, failed: 2, addressed: 1, pass_rate: 0.6, resolution_rate: 0.5 },
    duration: { review_p50_secs: 60, review_p90_secs: 180, review_p99_secs: 3600, queue_p50_secs: 30, queue_p90_secs: 90, queue_p99_secs: 300 },
    agents: [{ agent: 'codex', total: 4, passed: 2, failed: 1, errors: 1, pass_rate: 0.5, median_duration_secs: 90 }],
    job_types: [{ type: 'review', count: 5, applied: 1, rebased: 1 }, { type: 'task', count: 2 }],
    failures: { total: 2, retries: 1, errors: { timeout: 2 } },
    cost: { total_usd: 0, jobs_with_cost: 0, jobs_total: 7, complete: false },
  },
}
const cost = { repositoryIdentity: identity, branchScope: 'all', cost: { total_usd: 0, jobs_with_cost: 4, jobs_total: 7, complete: false } }
function response(value: unknown, status = 200) { return Promise.resolve(new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } })) }
function renderStats(props: Partial<React.ComponentProps<typeof StatsInsights>> = {}) { return render(<I18nProvider><StatsInsights selectedRepositoryIdentity={identity} capabilities={capabilities} daemonRunning {...props} /></I18nProvider>) }
afterEach(() => { cleanup(); vi.restoreAllMocks(); window.localStorage.setItem(LANGUAGE_STORAGE_KEY, 'en') })

describe('StatsInsights', () => {
  it('is a real workspace with default 7d/all scope and renders upstream values', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => { const path = String(input); calls.push(path); const branch = path.includes('branchScope=current') ? 'current' : 'all'; const window = (path.match(/window=([^&]+)/)?.[1] ?? '7d') as '24h' | '7d' | '30d' | '90d'; return path.includes('/cost?') ? response({ ...cost, branchScope: branch }) : response({ ...summary, window, branchScope: branch }) }))
    renderStats()
    expect(screen.getByRole('combobox', { name: 'Window' })).toHaveValue('7d')
    expect(screen.getByRole('combobox', { name: 'Branch scope' })).toHaveValue('all')
    expect(Array.from(screen.getByRole('combobox', { name: 'Window' }).querySelectorAll('option')).map(option => option.getAttribute('value'))).toEqual(['24h', '7d', '30d', '90d'])
    expect(screen.queryByText('Soon')).not.toBeInTheDocument()
    expect(await screen.findByText('codex')).toBeInTheDocument()
    expect(screen.getAllByText('4').length).toBeGreaterThan(0)
    expect(screen.getAllByText('60.0%').length).toBeGreaterThan(0)
    expect(screen.getAllByText('$0.00').length).toBeGreaterThan(0)
    expect(screen.getByText('1m')).toBeInTheDocument()
    expect(screen.getAllByText('1.5m').length).toBeGreaterThan(0)
    expect(screen.getByText('1.0h')).toBeInTheDocument()
    expect(screen.getByText('timeout')).toBeInTheDocument()
    expect(screen.getByText('0 / 0')).toBeInTheDocument()
    expect(screen.getByText('Window estimate is a lower bound; 0/7 jobs have reported cost.')).toBeInTheDocument()
    expect(screen.getByText('Coverage: 4/7 jobs with reported cost.')).toBeInTheDocument()
    expect(screen.getAllByText('Some eligible jobs did not report cost; actual spend may be higher than this lower bound.').length).toBeGreaterThan(0)
    expect(calls.some(path => path.includes('window=7d') && path.includes('branchScope=all'))).toBe(true)
  })

  it('changes summary windows, reloads both for branch scope, and refreshes explicitly', async () => {
    const calls: string[] = []
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => { const path = String(input); calls.push(path); const branch = path.includes('branchScope=current') ? 'current' : 'all'; const window = (path.match(/window=([^&]+)/)?.[1] ?? '7d') as '24h' | '7d' | '30d' | '90d'; return path.includes('/cost?') ? response({ ...cost, branchScope: branch }) : response({ ...summary, window, branchScope: branch }) }))
    renderStats()
    await screen.findByText('codex')
    const window = screen.getByRole('combobox', { name: 'Window' })
    const branch = screen.getByRole('combobox', { name: 'Branch scope' })
    await userEvent.selectOptions(window, '30d')
    await waitFor(() => expect(calls.some(path => path.includes('window=30d'))).toBe(true))
    await userEvent.selectOptions(branch, 'current')
    await waitFor(() => expect(calls.some(path => path.includes('branchScope=current'))).toBe(true))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Refresh' })).toBeEnabled())
    const beforeRefresh = calls.length
    await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(calls.length).toBeGreaterThan(beforeRefresh))
  })

  it('keeps summary and cost errors independent and supports retry', async () => {
    let costFails = true
    let summaryFails = false
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      if (String(input).includes('/cost?') && costFails) return response({ error: 'cost unavailable' }, 503)
      if (!String(input).includes('/cost?') && summaryFails) return response({ error: 'summary unavailable' }, 503)
      return String(input).includes('/cost?') ? response(cost) : response(summary)
    }))
    renderStats()
    expect(await screen.findByText('cost unavailable')).toBeInTheDocument()
    expect(await screen.findByText('codex')).toBeInTheDocument()
    costFails = false
    await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(screen.getAllByText('$0.00').length).toBeGreaterThan(0))
    summaryFails = true
    await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    expect(await screen.findByText('summary unavailable')).toBeInTheDocument()
    expect(screen.getAllByText('$0.00').length).toBeGreaterThan(0)
    summaryFails = false
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
    await waitFor(() => expect(screen.queryByText('summary unavailable')).not.toBeInTheDocument())
  })

  it('does not request while unavailable and does not reload on locale rerender', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => String(input).includes('/cost?') ? response(cost) : response(summary))
    vi.stubGlobal('fetch', fetchMock)
    renderStats({ daemonRunning: false })
    expect(screen.getByText('Stats unavailable')).toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
    cleanup()
    render(<I18nProvider><LanguageSelector /><StatsInsights selectedRepositoryIdentity={identity} capabilities={capabilities} daemonRunning /></I18nProvider>)
    await screen.findByText('codex')
    const count = fetchMock.mock.calls.length
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Select language' }), 'ko')
    expect(screen.getByText('통계 및 인사이트')).toBeInTheDocument()
    expect(fetchMock.mock.calls.length).toBe(count)
  })

  it('distinguishes empty cost scope from reported free usage and marks partial coverage', async () => {
    const emptySummary = { ...summary, summary: { ...summary.summary, overview: { total: 0, queued: 0, running: 0, done: 0, failed: 0, canceled: 0, applied: 0, rebased: 0 }, verdicts: { total: 0, passed: 0, failed: 0, addressed: 0, pass_rate: 0, resolution_rate: 0 }, cost: { total_usd: 0, jobs_with_cost: 0, jobs_total: 0, complete: false } } }
    const emptyCost = { ...cost, cost: { total_usd: 0, jobs_with_cost: 0, jobs_total: 0, complete: false } }
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => String(input).includes('/cost?') ? response(emptyCost) : response(emptySummary)))
    renderStats()
    expect((await screen.findAllByText('No eligible cost-reporting jobs are in this scope yet.')).length).toBe(2)
    expect(screen.getAllByText('—').length).toBeGreaterThan(0)
  })

  it('discards a stale 7d summary after switching to 30d', async () => {
    let resolve7d!: (value: Response) => void
    const pending7d = new Promise<Response>(resolve => { resolve7d = resolve })
    const thirtyDaySummary = { ...summary, window: '30d', summary: { ...summary.summary, overview: { ...summary.summary.overview, total: 30, queued: 0, running: 0, failed: 0, canceled: 0 } } }
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      if (path.includes('/cost?')) return response(cost)
      if (path.includes('window=7d')) return pending7d
      return response(thirtyDaySummary)
    }))
    renderStats()
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Window' }), '30d')
    expect((await screen.findAllByText('30')).length).toBeGreaterThan(0)
    resolve7d(new Response(JSON.stringify(summary), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect((screen.getAllByText('30')).length).toBeGreaterThan(0)
  })

  it('rejects stale repository responses', async () => {
    let resolveA!: (value: Response) => void
    const pendingA = new Promise<Response>(resolve => { resolveA = resolve })
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      if (String(input).includes('repositoryIdentity=repo-a')) return pendingA
      return String(input).includes('/cost?') ? response({ ...cost, repositoryIdentity: 'repo-b' }) : response({ ...summary, repositoryIdentity: 'repo-b', summary: { ...summary.summary, overview: { ...summary.summary.overview, total: 2, done: 0, applied: 0, rebased: 0 } } })
    }))
    const { rerender } = renderStats()
    rerender(<I18nProvider><StatsInsights selectedRepositoryIdentity="repo-b" capabilities={capabilities} daemonRunning /></I18nProvider>)
    expect((await screen.findAllByText('2')).length).toBeGreaterThan(0)
    resolveA(new Response(JSON.stringify(summary), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(screen.getAllByText('2').length).toBeGreaterThan(0)
  })
})
