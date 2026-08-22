import { RefreshCw, TriangleAlert } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { request, type StatsBranchScope, type StatsCostResponse, type StatsSummaryResponse, type StatsWindow } from './api'
import { useI18n } from './i18n'

export type StatsInsightsProps = {
  selectedRepositoryIdentity: string
  capabilities?: Record<string, unknown>
  daemonRunning: boolean
  repoSwitching?: boolean
  suspended?: boolean
  onOpenSetup?: () => void
}

type LoadState<T> = { status: 'idle' | 'loading' | 'success' | 'error' | 'unavailable'; data: T | null; error: string }

export function statsCompletionAllowed(suspended: boolean, aborted: boolean, generation: number, currentGeneration: number) {
  return !suspended && !aborted && generation === currentGeneration
}

const windows: StatsWindow[] = ['24h', '7d', '30d', '90d']
const branchScopes: StatsBranchScope[] = ['all', 'current']

function formatDuration(seconds?: number) {
  if (seconds === undefined) return '—'
  if (seconds < 60) return `${seconds.toFixed(seconds % 1 ? 1 : 0)}s`
  const minutes = seconds / 60
  if (minutes < 60) return `${minutes.toFixed(minutes % 1 ? 1 : 0)}m`
  return `${(minutes / 60).toFixed(1)}h`
}
function formatCost(value?: number) { return value === undefined ? '—' : `$${value.toFixed(2)}` }
function statusFor<T>(state: LoadState<T>, t: (key: any, values?: any) => string) {
  if (state.status === 'loading') return <p className="stats-muted">{t('stats.loading')}</p>
  if (state.status === 'error') return <div className="stats-error" role="alert">{state.error}</div>
  if (state.status === 'unavailable') return <div className="stats-unavailable"><strong>{t('stats.unavailable')}</strong><span>{t('stats.unavailableBody')}</span></div>
  return null
}

export function StatsInsights({ selectedRepositoryIdentity, capabilities = {}, daemonRunning, repoSwitching = false, suspended = false, onOpenSetup }: StatsInsightsProps) {
  const { t } = useI18n()
  const [windowValue, setWindowValue] = useState<StatsWindow>('7d')
  const [branchScope, setBranchScope] = useState<StatsBranchScope>('all')
  const [summary, setSummary] = useState<LoadState<StatsSummaryResponse>>({ status: 'idle', data: null, error: '' })
  const [cost, setCost] = useState<LoadState<StatsCostResponse>>({ status: 'idle', data: null, error: '' })
  const summaryGeneration = useRef(0)
  const costGeneration = useRef(0)
  const summaryAbort = useRef<AbortController | null>(null)
  const costAbort = useRef<AbortController | null>(null)
  const suspendedRef = useRef(suspended)
  suspendedRef.current = suspended
  const summarySupported = capabilities.summaryStats === true
  const costSupported = capabilities.costStats === true
  const fetchUnavailable = !selectedRepositoryIdentity || !daemonRunning || repoSwitching
  const controlsDisabled = suspended

  const loadSummary = useCallback(() => {
    if (suspendedRef.current) return
    summaryAbort.current?.abort()
    const controller = new AbortController()
    summaryAbort.current = controller
    const generation = ++summaryGeneration.current
    if (fetchUnavailable || !summarySupported) { setSummary({ status: 'unavailable', data: null, error: '' }); return }
    setSummary(old => ({ ...old, status: 'loading', error: '' }))
    const path = `/api/stats/summary?repositoryIdentity=${encodeURIComponent(selectedRepositoryIdentity)}&window=${windowValue}&branchScope=${branchScope}`
    void request<StatsSummaryResponse>(path, { signal: controller.signal }).then(data => {
      if (!statsCompletionAllowed(suspendedRef.current, controller.signal.aborted, generation, summaryGeneration.current) || data.repositoryIdentity !== selectedRepositoryIdentity || data.window !== windowValue || data.branchScope !== branchScope) return
      setSummary({ status: 'success', data, error: '' })
    }).catch(error => {
      if (!statsCompletionAllowed(suspendedRef.current, controller.signal.aborted, generation, summaryGeneration.current)) return
      setSummary({ status: 'error', data: null, error: (error as Error).message })
    })
  }, [branchScope, fetchUnavailable, selectedRepositoryIdentity, summarySupported, windowValue])

  const loadCost = useCallback(() => {
    if (suspendedRef.current) return
    costAbort.current?.abort()
    const controller = new AbortController()
    costAbort.current = controller
    const generation = ++costGeneration.current
    if (fetchUnavailable || !costSupported) { setCost({ status: 'unavailable', data: null, error: '' }); return }
    setCost(old => ({ ...old, status: 'loading', error: '' }))
    const path = `/api/stats/cost?repositoryIdentity=${encodeURIComponent(selectedRepositoryIdentity)}&branchScope=${branchScope}`
    void request<StatsCostResponse>(path, { signal: controller.signal }).then(data => {
      if (!statsCompletionAllowed(suspendedRef.current, controller.signal.aborted, generation, costGeneration.current) || data.repositoryIdentity !== selectedRepositoryIdentity || data.branchScope !== branchScope) return
      setCost({ status: 'success', data, error: '' })
    }).catch(error => {
      if (!statsCompletionAllowed(suspendedRef.current, controller.signal.aborted, generation, costGeneration.current)) return
      setCost({ status: 'error', data: null, error: (error as Error).message })
    })
  }, [branchScope, costSupported, fetchUnavailable, selectedRepositoryIdentity])

  useEffect(() => {
    if (!suspended) return
    summaryAbort.current?.abort()
    costAbort.current?.abort()
    summaryGeneration.current++
    costGeneration.current++
    setSummary(old => old.status === 'loading' ? (old.data ? { ...old, status: 'success', error: '' } : { status: 'idle', data: null, error: '' }) : old)
    setCost(old => old.status === 'loading' ? (old.data ? { ...old, status: 'success', error: '' } : { status: 'idle', data: null, error: '' }) : old)
  }, [suspended])

  useEffect(() => { loadSummary(); return () => { summaryAbort.current?.abort(); summaryGeneration.current++ } }, [loadSummary])
  useEffect(() => { loadCost(); return () => { costAbort.current?.abort(); costGeneration.current++ } }, [loadCost])

  const summaryObject = summary.data?.summary
  const overview = summaryObject?.overview
  const verdicts = summaryObject?.verdicts
  const timing = summaryObject?.duration
  const agents = summaryObject?.agents ?? []
  const jobTypes = summaryObject?.job_types ?? []
  const failures = summaryObject?.failures
  const windowCostDetails = summaryObject?.cost
  const completed = overview ? overview.done + overview.applied + overview.rebased : undefined
  const totalJobs = overview?.total
  const percent = (value?: number) => value === undefined ? '—' : `${(value * 100).toFixed(1)}%`
  const refresh = () => { loadSummary(); loadCost() }
  const unavailableCopy = fetchUnavailable || (!summarySupported && !costSupported)
  const controlsUnavailable = unavailableCopy || controlsDisabled
  const summaryStatus = statusFor(summary, t)
  const costStatus = statusFor(cost, t)

  return <section className="stats-page" aria-label={t('stats.aria')}>
    <header className="stats-heading"><div><span className="eyebrow">{t('stats.eyebrow')}</span><h1>{t('stats.title')}</h1><p>{t('stats.description')}</p></div><div className="stats-actions"><button className="button" onClick={refresh} disabled={controlsUnavailable || summary.status === 'loading' || cost.status === 'loading'}><RefreshCw size={15} />{t('stats.refresh')}</button></div></header>
    <div className="stats-controls"><label>{t('stats.window')}<select aria-label={t('stats.window')} value={windowValue} disabled={controlsDisabled} onChange={event => setWindowValue(event.target.value as StatsWindow)}>{windows.map(value => <option key={value} value={value}>{t(`stats.window.${value}` as any)}</option>)}</select></label><label>{t('stats.branch')}<select aria-label={t('stats.branch')} value={branchScope} disabled={controlsDisabled} onChange={event => setBranchScope(event.target.value as StatsBranchScope)}>{branchScopes.map(value => <option key={value} value={value}>{t(`stats.branch.${value}` as any)}</option>)}</select></label></div>
    {unavailableCopy ? <div className="stats-unavailable stats-main-unavailable"><TriangleAlert size={18} /><div><strong>{t('stats.unavailable')}</strong><span>{t('stats.unavailableBody')}</span>{onOpenSetup && <button className="button" onClick={onOpenSetup}>{t('stats.openSetup')}</button>}</div></div> : <>
      <section className="stats-panel"><header><div><span className="eyebrow">{t('stats.overview')}</span><h2>{t('stats.summary')}</h2></div>{summaryStatus}{summary.status === 'error' && <button className="button" disabled={controlsDisabled} onClick={loadSummary}>{t('common.retry')}</button>}</header>{summary.status === 'success' && overview && verdicts && <><div className="stats-cards"><div><span>{t('stats.jobsTotal')}</span><strong>{totalJobs}</strong></div>{/* v0.64 done/applied/rebased are mutually exclusive review_jobs statuses; CLI completed is their sum. */}<div><span>{t('stats.completed')}</span><strong>{completed}</strong></div><div><span>{t('stats.passRate')}</span><strong>{verdicts.total > 0 ? percent(verdicts.pass_rate) : '—'}</strong></div><div><span>{t('stats.resolutionRate')}</span><strong>{verdicts.total > 0 ? percent(verdicts.resolution_rate) : '—'}</strong></div><div><span>{t('stats.findings')}</span><strong>{verdicts.failed}</strong></div><div><span>{t('stats.addressed')}</span><strong>{verdicts.addressed}</strong></div><div><span>{t('stats.failures')}</span><strong>{failures?.total ?? '—'}</strong></div>{windowCostDetails && windowCostDetails.jobs_total > 0 && <div><span>{t('stats.windowCost')}</span><strong>{formatCost(windowCostDetails.total_usd)}</strong></div>}</div>{windowCostDetails && windowCostDetails.jobs_total === 0 ? <p className="stats-empty">{t('stats.costEmpty')}</p> : windowCostDetails && <><p className="stats-muted">{t('stats.windowCostNote', { withCost: windowCostDetails.jobs_with_cost, total: windowCostDetails.jobs_total })}</p>{!windowCostDetails.complete && <p className="stats-warning">{t('stats.costIncomplete')}</p>}</>}</>}</section>
      {summary.status === 'success' && <section className="stats-panel"><header><div><span className="eyebrow">{t('stats.timing')}</span><h2>{t('stats.duration')}</h2></div></header>{timing && <div className="stats-table-scroll"><table><thead><tr><th>{t('stats.durationKind')}</th><th>P50</th><th>P90</th><th>P99</th></tr></thead><tbody><tr><td>{t('stats.reviewDuration')}</td><td>{formatDuration(timing.review_p50_secs)}</td><td>{formatDuration(timing.review_p90_secs)}</td><td>{formatDuration(timing.review_p99_secs)}</td></tr><tr><td>{t('stats.queueDuration')}</td><td>{formatDuration(timing.queue_p50_secs)}</td><td>{formatDuration(timing.queue_p90_secs)}</td><td>{formatDuration(timing.queue_p99_secs)}</td></tr></tbody></table></div>}</section>}
      {summary.status === 'success' && <div className="stats-two-column"><section className="stats-panel"><header><h2>{t('stats.agents')}</h2></header><div className="stats-table-scroll"><table><thead><tr><th>{t('stats.agent')}</th><th>{t('stats.jobs')}</th><th>{t('stats.passed')}</th><th>{t('stats.failed')}</th><th>{t('stats.errors')}</th><th>{t('stats.passRate')}</th><th>{t('stats.median')}</th></tr></thead><tbody>{agents.map(agent => <tr key={agent.agent}><td>{agent.agent}</td><td>{agent.total}</td><td>{agent.passed}</td><td>{agent.failed}</td><td>{agent.errors}</td><td>{percent(agent.pass_rate)}</td><td>{formatDuration(agent.median_duration_secs)}</td></tr>)}</tbody></table></div>{agents.length === 0 && <p className="stats-muted">{t('stats.noData')}</p>}</section><section className="stats-panel"><header><h2>{t('stats.jobTypes')}</h2></header><ul className="stats-list">{jobTypes.map(item => <li key={item.type}><span>{item.type}</span><strong>{item.count}</strong><small>{item.applied ?? 0} / {item.rebased ?? 0}</small></li>)}</ul>{jobTypes.length === 0 && <p className="stats-muted">{t('stats.noData')}</p>}</section></div>}
      {summary.status === 'success' && <section className="stats-panel"><header><h2>{t('stats.failures')}</h2></header><div className="stats-cards"><div><span>{t('stats.failureTotal')}</span><strong>{failures?.total ?? '—'}</strong></div><div><span>{t('stats.retries')}</span><strong>{failures?.retries ?? '—'}</strong></div></div><ul className="stats-list">{Object.entries(failures?.errors ?? {}).map(([name, count]) => <li key={name}><span>{name}</span><strong>{count}</strong></li>)}</ul>{failures && Object.keys(failures.errors).length === 0 && <p className="stats-muted">{t('stats.noFailures')}</p>}</section>}
      <section className="stats-panel"><header><div><span className="eyebrow">{t('stats.cost')}</span><h2>{t('stats.costTitle')}</h2></div>{costStatus}{cost.status === 'error' && <button className="button" disabled={controlsDisabled} onClick={loadCost}>{t('common.retry')}</button>}</header>{cost.status === 'success' && cost.data?.cost.jobs_total === 0 ? <p className="stats-empty">{t('stats.costEmpty')}</p> : cost.status === 'success' && cost.data?.cost && <><div className="stats-cards"><div><span>{t('stats.allTimeCost')}</span><strong>{formatCost(cost.data.cost.total_usd)}</strong></div><div><span>{t('stats.jobsWithCost')}</span><strong>{cost.data.cost.jobs_with_cost}</strong></div><div><span>{t('stats.costJobsTotal')}</span><strong>{cost.data.cost.jobs_total}</strong></div></div><p className="stats-muted">{t('stats.costCoverageCount', { withCost: cost.data.cost.jobs_with_cost, total: cost.data.cost.jobs_total })}</p></>}{cost.status === 'success' && cost.data?.cost && cost.data.cost.jobs_total > 0 && cost.data.cost.complete === false && <p className="stats-warning">{t('stats.costIncomplete')}</p>}{cost.status === 'success' && cost.data?.cost?.jobs_total !== 0 && <p className="stats-muted">{t('stats.costCoverage')}</p>}</section>
      <section className="stats-panel stats-insights"><header><h2>{t('stats.insights')}</h2></header><p>{t('stats.insightsDisabled')}</p></section>
    </>}
    {totalJobs === 0 && summary.status === 'success' && <p className="stats-empty">{t('stats.empty')}</p>}
  </section>
}

export { formatDuration }
