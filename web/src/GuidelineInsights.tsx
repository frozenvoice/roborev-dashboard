import { Clipboard, Play, Sparkles, X } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { request, normalizeGuidelineInsightsLaunch, normalizeGuidelineInsightsPreview, normalizeGuidelineInsightsResult, normalizeGuidelineInsightsStatus, type GuidelineInsightsLaunch, type GuidelineInsightsPreview, type GuidelineInsightsResult, type GuidelineInsightsStatus, type InsightsBranchScope, type InsightsWindow } from './api'
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
  const [copyState, setCopyState] = useState(false)
  const generation = useRef(0)
  const abortRef = useRef<AbortController | null>(null)
  const currentIdentity = useRef(selectedRepositoryIdentity)
  currentIdentity.current = selectedRepositoryIdentity
  const enabled = capabilities.guidelineInsights === true && daemonRunning && Boolean(selectedRepositoryIdentity) && !repoSwitching
  const activeRun = status?.status === 'queued' || status?.status === 'running' || (launch?.status === 'queued' && status === null)

  useEffect(() => {
    const epoch = ++generation.current
    abortRef.current?.abort()
    abortRef.current = null
    setPreview(null)
    setModalOpen(false)
    setPreparing(false)
    setConfirming(false)
    setError('')
    setLaunch(null)
    setStatus(null)
    setResult(null)
    return () => { if (generation.current === epoch) abortRef.current?.abort() }
  }, [selectedRepositoryIdentity])

  useEffect(() => {
    if (!repoSwitching) return
    // A switch is a launch/status ownership boundary even when the selected
    // identity has not changed yet. Abort in-flight work and invalidate late
    // responses, but retain a queued launch so polling can restart once the
    // same identity is stable again.
    generation.current++
    abortRef.current?.abort()
    abortRef.current = null
    setPreview(null)
    setModalOpen(false)
    setPreparing(false)
    setConfirming(false)
    setError('')
  }, [repoSwitching])

  const prepare = async () => {
    if (suspended || !enabled || preparing) return
    const epoch = generation.current
    const controller = new AbortController()
    abortRef.current?.abort()
    abortRef.current = controller
    setPreparing(true)
    setError('')
    setLaunch(null)
    setStatus(null)
    setResult(null)
    try {
      const value = normalizeGuidelineInsightsPreview(await request<unknown>('/api/insights/preview', { method: 'POST', signal: controller.signal, body: JSON.stringify({ repositoryIdentity: selectedRepositoryIdentity, window: windowValue, branchScope }) }))
      if (controller.signal.aborted || epoch !== generation.current || currentIdentity.current !== selectedRepositoryIdentity) return
      if (!value || value.repositoryIdentity !== selectedRepositoryIdentity || value.window !== windowValue || value.branchScope !== branchScope) throw new Error(t('stats.insightsMalformed'))
      setPreview(value)
      setModalOpen(true)
    } catch (caught) {
      if (!controller.signal.aborted && epoch === generation.current && currentIdentity.current === selectedRepositoryIdentity) {
        setError((caught as Error).message)
      }
    } finally {
      if (epoch === generation.current) setPreparing(false)
    }
  }

  const run = async () => {
    if (suspended || !preview || confirming) return
    const epoch = generation.current
    const controller = new AbortController()
    abortRef.current?.abort()
    abortRef.current = controller
    setConfirming(true)
    setError('')
    try {
      const value = normalizeGuidelineInsightsLaunch(await request<unknown>('/api/insights', { method: 'POST', signal: controller.signal, body: JSON.stringify({ token: preview.token }) }))
      if (controller.signal.aborted || epoch !== generation.current || currentIdentity.current !== selectedRepositoryIdentity) return
      if (!value) throw new Error(t('stats.insightsMalformed'))
      setLaunch(value)
      setModalOpen(false)
      setPreview(null)
      if (value.status === 'queued' && value.jobId) setStatus({ jobId: value.jobId, status: 'queued' })
    } catch (caught) {
      if (!controller.signal.aborted && epoch === generation.current && currentIdentity.current === selectedRepositoryIdentity) {
        setLaunch({ status: 'unverified', message: t('stats.guidelineAmbiguous') })
        setStatus(null)
        setResult(null)
        setModalOpen(false)
        setPreview(null)
        setError('')
      }
    } finally {
      if (epoch === generation.current) setConfirming(false)
    }
  }

  useEffect(() => {
    if (repoSwitching) return
    const jobId = launch?.status === 'queued' ? launch.jobId : undefined
    if (!jobId || !selectedRepositoryIdentity) return
    const epoch = generation.current
    const controller = new AbortController()
    abortRef.current?.abort()
    abortRef.current = controller
    let timer: ReturnType<typeof setTimeout> | undefined
    let stopped = false
    let lastStatus: GuidelineInsightsStatus['status'] = 'queued'
    const current = () => !stopped && !controller.signal.aborted && epoch === generation.current && currentIdentity.current === selectedRepositoryIdentity
    const schedule = () => {
      if (current() && (lastStatus === 'queued' || lastStatus === 'running')) timer = setTimeout(poll, 2000)
    }
    const poll = async () => {
      try {
        const next = normalizeGuidelineInsightsStatus(await request<unknown>(`/api/insights/${jobId}?repositoryIdentity=${encodeURIComponent(selectedRepositoryIdentity)}`, { signal: controller.signal }))
        if (!current()) return
        if (!next || next.jobId !== jobId) {
          setStatus(null)
          setResult(null)
          setError(t('stats.insightsMalformed'))
          return
        }
        lastStatus = next.status
        setStatus(next)
        setError('')
        if (next.status === 'done') {
          const completed = normalizeGuidelineInsightsResult(await request<unknown>(`/api/insights/${jobId}/result?repositoryIdentity=${encodeURIComponent(selectedRepositoryIdentity)}`, { signal: controller.signal }))
          if (!current()) return
          if (!completed || completed.jobId !== jobId) {
            setResult(null)
            setError(t('stats.insightsMalformed'))
            return
          }
          setResult(completed)
          setError('')
          return
        }
        schedule()
      } catch (caught) {
        if (current() && (lastStatus === 'queued' || lastStatus === 'running')) {
          setError((caught as Error).message)
          schedule()
        }
      }
    }
    void poll()
    return () => { stopped = true; controller.abort(); if (timer) clearTimeout(timer); generation.current++ }
  }, [launch?.jobId, launch?.status, selectedRepositoryIdentity, repoSwitching])

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
    {result && <div className="guideline-result"><div className="text-toolbar"><button className="button ghost compact" onClick={() => void copy()}><Clipboard size={14} />{copyState ? t('common.copied') : t('common.copy')}</button></div><Markdown>{result.markdown}</Markdown></div>}
    {error && <p role="alert" className="stats-error">{error}</p>}
    <button className="button primary" disabled={suspended || !enabled || preparing || activeRun || Boolean(preview)} onClick={() => void prepare()}><Play size={15} />{preparing ? t('common.loading') : t('stats.guidelinePrepare')}</button>
    {modalOpen && preview && <div className="prompt-overlay guideline-overlay"><section className="prompt-dialog guideline-dialog" role="dialog" aria-modal="true" aria-labelledby="guideline-title"><header><div><span className="eyebrow">{t('stats.guidelineConfirmEyebrow')}</span><h2 id="guideline-title">{t('stats.guidelineConfirmTitle')}</h2></div><button className="icon-button" aria-label={t('common.close')} onClick={closePreview}><X /></button></header><dl><div><dt>{t('stats.guidelineRepository')}</dt><dd>{repositoryName || preview.repositoryName}</dd></div><div><dt>{t('stats.window')}</dt><dd>{t(`stats.window.${preview.window}` as any)}</dd></div><div><dt>{t('stats.branch')}</dt><dd>{preview.branchScope === 'current' ? preview.branch : t('stats.branch.all')}</dd></div><div><dt>{t('stats.guidelineAgent')}</dt><dd>{t('stats.guidelineDefaultAgent')}</dd></div><div><dt>{t('stats.guidelineExecutable')}</dt><dd><code>{preview.command.executable}</code></dd></div><div><dt>{t('stats.guidelineArguments')}</dt><dd><ol className="guideline-argv">{preview.command.args.map((argument, index) => <li key={`${index}:${argument}`}><span>{index}</span><code>{JSON.stringify(argument)}</code></li>)}</ol></dd></div></dl><p>{t('stats.guidelineDisclosure')}</p>{preview.warnings.map(warning => <p className="stats-warning" key={warning}>{warningLabel(warning)}</p>)}<p className="stats-muted">{t('stats.guidelineLifetime')}</p><footer><button className="button" disabled={confirming} onClick={closePreview}>{t('common.cancel')}</button><button className="button primary" disabled={suspended || confirming} onClick={() => void run()}>{confirming ? t('common.loading') : t('stats.guidelineRun')}</button></footer></section></div>}
  </section>
}
