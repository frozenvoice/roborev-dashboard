import { Clipboard, Play, Sparkles, X } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { APIError, request, normalizeGuidelineInsightsLaunch, normalizeGuidelineInsightsPreview, normalizeGuidelineInsightsResult, normalizeGuidelineInsightsStatus, type GuidelineInsightsLaunch, type GuidelineInsightsPreview, type GuidelineInsightsResult, type GuidelineInsightsStatus, type InsightsBranchScope, type InsightsWindow } from './api'
import { useI18n } from './i18n'
import { Markdown } from './Markdown'

export type GuidelineInsightsProps = {
  selectedRepositoryIdentity: string
  repositoryName?: string
  capabilities?: Record<string, unknown>
  daemonRunning: boolean
  repoSwitching?: boolean
  suspended?: boolean
}

export function GuidelineInsights({ selectedRepositoryIdentity, repositoryName, capabilities = {}, daemonRunning, repoSwitching = false, suspended = false }: GuidelineInsightsProps) {
  const { t } = useI18n()
  const [windowValue, setWindowValue] = useState<InsightsWindow>('30d')
  const [branchScope, setBranchScope] = useState<InsightsBranchScope>('all')
  const [preview, setPreview] = useState<GuidelineInsightsPreview | null>(null)
  const [modalOpen, setModalOpen] = useState(false)
  const [preparing, setPreparing] = useState(false)
  const [confirming, setConfirming] = useState(false)
  const [error, setError] = useState('')
  const [launch, setLaunch] = useState<GuidelineInsightsLaunch | null>(null)
  const [status, setStatus] = useState<GuidelineInsightsStatus | null>(null)
  const [result, setResult] = useState<GuidelineInsightsResult | null>(null)
  const [resultLoading, setResultLoading] = useState(false)
  const [resultError, setResultError] = useState('')
  const [resultReload, setResultReload] = useState(0)
  const [copyState, setCopyState] = useState(false)
  const interactionGeneration = useRef(0)
  const interactionAbort = useRef<AbortController | null>(null)
  const pollGeneration = useRef(0)
  const pollAbort = useRef<AbortController | null>(null)
  const resultGeneration = useRef(0)
  const resultAbort = useRef<AbortController | null>(null)
  const scopeGeneration = useRef(0)
  const currentIdentity = useRef(selectedRepositoryIdentity)
  const currentRepoSwitching = useRef(repoSwitching)
  currentIdentity.current = selectedRepositoryIdentity
  // Keep the scope guard synchronous with render. The authoritative
  // repo-switch effect aborts work after commit, but a late promise can settle
  // between render and that effect; predicates must reject it immediately.
  currentRepoSwitching.current = repoSwitching
  const enabled = capabilities.guidelineInsights === true && daemonRunning && Boolean(selectedRepositoryIdentity) && !repoSwitching
  const activeRun = status?.status === 'queued' || status?.status === 'running' || (launch?.status === 'queued' && status === null)

  const invalidateInteraction = () => {
    interactionGeneration.current++
    interactionAbort.current?.abort()
    interactionAbort.current = null
  }
  const invalidatePoll = () => {
    pollGeneration.current++
    pollAbort.current?.abort()
    pollAbort.current = null
  }
  const invalidateResult = () => {
    resultGeneration.current++
    resultAbort.current?.abort()
    resultAbort.current = null
  }
  const invalidateAll = () => {
    scopeGeneration.current++
    invalidateInteraction()
    invalidatePoll()
    invalidateResult()
  }
  const clearLocalState = () => {
    setPreview(null)
    setModalOpen(false)
    setPreparing(false)
    setConfirming(false)
    setError('')
    setLaunch(null)
    setStatus(null)
    setResult(null)
    setResultLoading(false)
    setResultError('')
    setResultReload(0)
  }

  useEffect(() => {
    invalidateAll()
    clearLocalState()
  }, [selectedRepositoryIdentity])

  useEffect(() => {
    if (!repoSwitching) return
    // A switch is an authoritative scope boundary. All local Insights state
    // belongs to the old repository until a new Prepare explicitly starts it.
    invalidateAll()
    clearLocalState()
  }, [repoSwitching])

  useEffect(() => () => {
    invalidateAll()
  }, [])

  const prepare = async () => {
    if (suspended || !enabled || preparing) return
    invalidatePoll()
    invalidateResult()
    const epoch = ++interactionGeneration.current
    interactionAbort.current?.abort()
    const controller = new AbortController()
    interactionAbort.current = controller
    const scopeEpoch = scopeGeneration.current
    const identity = selectedRepositoryIdentity
    setPreparing(true)
    setError('')
    setLaunch(null)
    setStatus(null)
    setResult(null)
    setResultLoading(false)
    setResultError('')
    setResultReload(0)
    try {
      const value = normalizeGuidelineInsightsPreview(await request<unknown>('/api/insights/preview', { method: 'POST', signal: controller.signal, body: JSON.stringify({ repositoryIdentity: selectedRepositoryIdentity, window: windowValue, branchScope }) }))
      if (controller.signal.aborted || epoch !== interactionGeneration.current || scopeEpoch !== scopeGeneration.current || currentIdentity.current !== identity || currentRepoSwitching.current) return
      if (!value || value.repositoryIdentity !== selectedRepositoryIdentity || value.window !== windowValue || value.branchScope !== branchScope) throw new Error(t('stats.insightsMalformed'))
      setPreview(value)
      setModalOpen(true)
    } catch (caught) {
      if (!controller.signal.aborted && epoch === interactionGeneration.current && scopeEpoch === scopeGeneration.current && currentIdentity.current === identity && !currentRepoSwitching.current) {
        setError((caught as Error).message)
      }
    } finally {
      if (epoch === interactionGeneration.current && scopeEpoch === scopeGeneration.current && !currentRepoSwitching.current) setPreparing(false)
    }
  }

  const run = async () => {
    if (suspended || !preview || confirming) return
    invalidatePoll()
    invalidateResult()
    const epoch = ++interactionGeneration.current
    interactionAbort.current?.abort()
    const controller = new AbortController()
    interactionAbort.current = controller
    const scopeEpoch = scopeGeneration.current
    const identity = selectedRepositoryIdentity
    const prepared = preview
    setConfirming(true)
    setError('')
    try {
      const value = normalizeGuidelineInsightsLaunch(await request<unknown>('/api/insights', { method: 'POST', signal: controller.signal, body: JSON.stringify({ token: prepared.token }) }))
      if (controller.signal.aborted || epoch !== interactionGeneration.current || scopeEpoch !== scopeGeneration.current || currentIdentity.current !== identity || currentRepoSwitching.current) return
      if (!value) throw new Error(t('stats.insightsMalformed'))
      setLaunch(value)
      setModalOpen(false)
      setPreview(null)
      if (value.status === 'queued' && value.jobId) setStatus({ jobId: value.jobId, status: 'queued' })
    } catch (caught) {
      if (!controller.signal.aborted && epoch === interactionGeneration.current && scopeEpoch === scopeGeneration.current && currentIdentity.current === identity && !currentRepoSwitching.current) {
        setStatus(null)
        setResult(null)
        setResultLoading(false)
        setResultError('')
        setModalOpen(false)
        setPreview(null)
        if (caught instanceof APIError) {
          setLaunch(null)
          setError(caught.message)
        } else {
          setLaunch({ status: 'unverified', message: t('stats.guidelineAmbiguous') })
          setError('')
        }
      }
    } finally {
      if (epoch === interactionGeneration.current && scopeEpoch === scopeGeneration.current && !currentRepoSwitching.current) setConfirming(false)
    }
  }

  useEffect(() => {
    if (repoSwitching) return
    const jobId = launch?.status === 'queued' ? launch.jobId : undefined
    if (!jobId || !selectedRepositoryIdentity) return
    const epoch = ++pollGeneration.current
    pollAbort.current?.abort()
    const controller = new AbortController()
    pollAbort.current = controller
    const scopeEpoch = scopeGeneration.current
    const identity = selectedRepositoryIdentity
    let timer: ReturnType<typeof setTimeout> | undefined
    let stopped = false
    let lastStatus: GuidelineInsightsStatus['status'] = 'queued'
    const current = () => !stopped && !controller.signal.aborted && epoch === pollGeneration.current && scopeEpoch === scopeGeneration.current && currentIdentity.current === identity && !currentRepoSwitching.current
    const schedule = () => {
      if (current() && (lastStatus === 'queued' || lastStatus === 'running')) timer = setTimeout(poll, 2000)
    }
    const poll = async () => {
      try {
        const next = normalizeGuidelineInsightsStatus(await request<unknown>(`/api/insights/${jobId}?repositoryIdentity=${encodeURIComponent(selectedRepositoryIdentity)}`, { signal: controller.signal }))
        if (!current()) return
        if (!next || next.jobId !== jobId) {
          setStatus(null)
          setError(t('stats.insightsMalformed'))
          return
        }
        lastStatus = next.status
        setStatus(next)
        setError('')
        schedule()
      } catch (caught) {
        if (current() && (lastStatus === 'queued' || lastStatus === 'running')) {
          setError((caught as Error).message)
          schedule()
        }
      }
    }
    void poll()
    return () => {
      stopped = true
      controller.abort()
      if (timer) clearTimeout(timer)
      if (pollGeneration.current === epoch) pollGeneration.current++
      if (pollAbort.current === controller) pollAbort.current = null
    }
  }, [launch?.jobId, launch?.status, selectedRepositoryIdentity, repoSwitching])

  useEffect(() => {
    if (repoSwitching || status?.status !== 'done' || !selectedRepositoryIdentity) return
    const jobId = status.jobId
    const epoch = ++resultGeneration.current
    resultAbort.current?.abort()
    const controller = new AbortController()
    resultAbort.current = controller
    const scopeEpoch = scopeGeneration.current
    const identity = selectedRepositoryIdentity
    let stopped = false
    const current = () => !stopped && !controller.signal.aborted && epoch === resultGeneration.current && scopeEpoch === scopeGeneration.current && currentIdentity.current === identity && !currentRepoSwitching.current
    setResultLoading(true)
    setResultError('')
    const load = async () => {
      try {
        const completed = normalizeGuidelineInsightsResult(await request<unknown>(`/api/insights/${jobId}/result?repositoryIdentity=${encodeURIComponent(identity)}`, { signal: controller.signal }))
        if (!current()) return
        if (!completed || completed.jobId !== jobId) throw new Error(t('stats.insightsMalformed'))
        setResult(completed)
        setResultError('')
      } catch (caught) {
        if (!current()) return
        setResultError(caught instanceof APIError ? caught.message : (caught as Error).message === t('stats.insightsMalformed') ? t('stats.insightsMalformed') : t('stats.guidelineResultFailed'))
      } finally {
        if (current()) setResultLoading(false)
      }
    }
    void load()
    return () => {
      stopped = true
      controller.abort()
      if (resultGeneration.current === epoch) resultGeneration.current++
      if (resultAbort.current === controller) resultAbort.current = null
    }
  }, [status?.jobId, status?.status, selectedRepositoryIdentity, repoSwitching, resultReload])

  const copy = async () => {
    if (!result) return
    try { await navigator.clipboard.writeText(result.markdown); setCopyState(true); setTimeout(() => setCopyState(false), 1300) } catch { setError(t('stats.insightsCopyFailed')) }
  }

  const closePreview = () => { setModalOpen(false); setPreview(null) }
  const warningLabel = (warning: string) => warning === 'quota_cost' ? t('stats.guidelineQuota') : warning === 'read_only' ? t('stats.guidelineNoConfig') : t('stats.guidelineWarningUnknown')

  const currentState = status?.status ?? (launch?.status === 'unverified' ? 'unverified' : launch?.status === 'skipped' ? 'skipped' : '')
  const stateLabel = currentState ? t(`stats.guidelineState.${currentState}` as any) : ''
  const receiptJobID = launch?.jobId ?? status?.jobId
  return <section className="stats-panel stats-insights guideline-insights" aria-label={t('stats.insights')}>
    <header><div><span className="eyebrow"><Sparkles size={14} />{t('stats.insights')}</span><h2>{t('stats.guidelineTitle')}</h2></div>{currentState && <span className={`insights-state insights-${currentState}`}>{stateLabel}</span>}</header>
    <p>{t('stats.guidelineDescription')}</p>
    {receiptJobID && <p className="stats-muted">{t('stats.guidelineReceipt', { id: receiptJobID })}</p>}
    <div className="guideline-controls"><label>{t('stats.window')}<select aria-label={t('stats.guidelineWindow')} value={windowValue} disabled={Boolean(preview) || activeRun || !enabled} onChange={event => setWindowValue(event.target.value as InsightsWindow)}><option value="7d">{t('stats.window.7d')}</option><option value="30d">{t('stats.window.30d')}</option><option value="90d">{t('stats.window.90d')}</option></select></label><label>{t('stats.branch')}<select aria-label={t('stats.guidelineBranch')} value={branchScope} disabled={Boolean(preview) || activeRun || !enabled} onChange={event => setBranchScope(event.target.value as InsightsBranchScope)}><option value="all">{t('stats.branch.all')}</option><option value="current">{t('stats.branch.current')}</option></select></label></div>
    <p className="stats-warning">{t('stats.guidelineQuota')}</p><p className="stats-muted">{t('stats.guidelineNoConfig')}</p><p className="stats-muted">{t('stats.guidelineLifetime')}</p>
    {launch?.status === 'skipped' && <p className="stats-empty">{t('stats.guidelineSkipped')}</p>}
    {launch?.status === 'unverified' && <p className="stats-warning">{t('stats.guidelineUnverified')}</p>}
    {status && (status.status === 'queued' || status.status === 'running') && <p className="stats-muted">{t('stats.guidelineJobRunning', { id: status.jobId, status: stateLabel })}</p>}
    {resultLoading && <p className="stats-muted">{t('common.loading')}</p>}
    {resultError && <div><p role="alert" className="stats-error">{resultError}</p><button className="button" disabled={resultLoading} onClick={() => setResultReload(value => value + 1)}>{t('stats.guidelineRetryResult')}</button></div>}
    {result && <div className="guideline-result"><div className="text-toolbar"><button className="button ghost compact" onClick={() => void copy()}><Clipboard size={14} />{copyState ? t('common.copied') : t('common.copy')}</button></div><Markdown>{result.markdown}</Markdown></div>}
    {error && <p role="alert" className="stats-error">{error}</p>}
    <button className="button primary" disabled={suspended || !enabled || preparing || activeRun || Boolean(preview)} onClick={() => void prepare()}><Play size={15} />{preparing ? t('common.loading') : t('stats.guidelinePrepare')}</button>
    {modalOpen && preview && <div className="prompt-overlay guideline-overlay"><section className="prompt-dialog guideline-dialog" role="dialog" aria-modal="true" aria-labelledby="guideline-title"><header><div><span className="eyebrow">{t('stats.guidelineConfirmEyebrow')}</span><h2 id="guideline-title">{t('stats.guidelineConfirmTitle')}</h2></div><button className="icon-button" aria-label={t('common.close')} onClick={closePreview}><X /></button></header><dl><div><dt>{t('stats.guidelineRepository')}</dt><dd>{repositoryName || preview.repositoryName}</dd></div><div><dt>{t('stats.window')}</dt><dd>{t(`stats.window.${preview.window}` as any)}</dd></div><div><dt>{t('stats.branch')}</dt><dd>{preview.branchScope === 'current' ? preview.branch : t('stats.branch.all')}</dd></div><div><dt>{t('stats.guidelineAgent')}</dt><dd>{t('stats.guidelineDefaultAgent')}</dd></div><div><dt>{t('stats.guidelineExecutable')}</dt><dd><code>{preview.command.executable}</code></dd></div><div><dt>{t('stats.guidelineArguments')}</dt><dd><ol className="guideline-argv">{preview.command.args.map((argument, index) => <li key={`${index}:${argument}`}><span>{index}</span><code>{JSON.stringify(argument)}</code></li>)}</ol></dd></div></dl><p>{t('stats.guidelineDisclosure')}</p>{preview.warnings.map(warning => <p className="stats-warning" key={warning}>{warningLabel(warning)}</p>)}<p className="stats-muted">{t('stats.guidelineLifetime')}</p><footer><button className="button" disabled={confirming} onClick={closePreview}>{t('common.cancel')}</button><button className="button primary" disabled={suspended || confirming} onClick={() => void run()}>{confirming ? t('common.loading') : t('stats.guidelineRun')}</button></footer></section></div>}
  </section>
}
