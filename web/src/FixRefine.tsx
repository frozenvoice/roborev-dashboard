import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { AlertTriangle, Check, CircleStop, Code2, Play, RefreshCw, ShieldCheck, X, XCircle } from 'lucide-react'
import { type FixOperation, type FixPreview, normalizeFixOperation, normalizeFixOperationsResponse, normalizeFixPreview, type PanelConfiguration, type PanelRefineOptions, type PanelRefineRun, type ReviewJob, request } from './api'
import { PanelRefine, type PanelRefineProps } from './PanelRefine'
import { LanguageSelector, useI18n } from './i18n'

type Toast = (kind: 'success' | 'error', message: string) => void

export function fixJobId(job: ReviewJob) {
  const value = job.id ?? job.job_id
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0 ? value : null
}

function finding(job: ReviewJob) {
  return job.status === 'done' && job.verdict === 'F'
}

function open(job: ReviewJob) { return job.closed === false && job.addressed !== true }

/** Only a single review or the panel synthesis parent can be fixed. */
export function isFixTarget(job: ReviewJob) {
  const optionalEmpty = (value: unknown) => value === undefined || value === ''
  const roleEmpty = optionalEmpty(job.panel_role)
  const jobType = typeof job.job_type === 'string' ? job.job_type : null
  const panelName = typeof job.panel_name === 'string' ? job.panel_name : ''
  const panelRunUUID = typeof job.panel_run_uuid === 'string' ? job.panel_run_uuid : ''
  const single = jobType === 'review' && roleEmpty && optionalEmpty(job.panel_name) && optionalEmpty(job.panel_run_uuid)
  const synthesis = jobType === 'synthesis' && job.panel_role === 'synthesis' && Boolean(panelName && panelRunUUID)
  return fixJobId(job) !== null && finding(job) && open(job) && (single || synthesis)
}
function isSynthesisJob(job: ReviewJob) { return job.job_type === 'synthesis' && job.panel_role === 'synthesis' }
function canonicalOptional(value: unknown) { return value === undefined || value === '' ? '' : value }
function fixTargetSignature(job: ReviewJob | null) {
  if (!job) return ''
  // Keep this scalar and explicit: review polling creates fresh job objects,
  // while the preview must be invalidated only when target-relevant data
  // changes (not when the locale or an unrelated field changes).
  return JSON.stringify([
    fixJobId(job), canonicalOptional(job.status), canonicalOptional(job.verdict), canonicalOptional(job.closed), canonicalOptional(job.addressed),
    canonicalOptional(job.job_type), canonicalOptional(job.panel_role), canonicalOptional(job.panel_name), canonicalOptional(job.panel_run_uuid),
    canonicalOptional(job.git_ref), canonicalOptional(job.branch), canonicalOptional(job.repo_path), canonicalOptional(job.skipped),
  ])
}

function short(value?: string) { return value ? value.slice(0, 12) : '—' }
function active(operation: FixOperation | null) { return Boolean(operation && ['starting', 'running'].includes(operation.phase)) }
function phaseLabel(operation: FixOperation, t: ReturnType<typeof useI18n>['t']) {
  if (operation.phase === 'succeeded') return t('status.pass')
  if (operation.phase === 'failed') return t('status.error')
  if (operation.phase === 'cancelled') return t('status.canceled')
  return operation.phase === 'starting' ? t('fix.starting') : t('status.running')
}
function phaseTone(operation: FixOperation) { return operation.phase === 'succeeded' ? 'pass' : operation.phase === 'failed' ? 'failed' : operation.phase === 'cancelled' ? 'cancelled' : 'running' }

export type PanelRefineSummaryProps = { run: PanelRefineRun; onOpen: () => void }
export function PanelRefineSummary({ run, onOpen }: PanelRefineSummaryProps) {
  const { t } = useI18n()
  const attention = run.phase === 'needs_attention'
  return <section className={`panel-summary ${attention ? 'attention' : 'running'}`} aria-label={t('review.panelRefineSummary')}>
    <div><strong>{attention ? t('panel.titleNeedsAttention') : t('review.panelRefineActive')}</strong><p>{attention ? t('review.panelRefineNeedsAttention') : t('review.panelRefineWorking')}</p></div>
    <button className="button" onClick={onOpen}>{t('review.openFixRefine')}</button>
  </section>
}

type FixRefineProps = {
  repoPath: string
  repoIdentity: string
  jobs: ReviewJob[]
  selectedJobId: number | null
  onSelectedJobIdChange: (id: number | null) => void
  onOpenReview: (id: number) => void
  onTerminal: () => Promise<void>
  onToast?: Toast
  panelRefine: PanelRefineProps
}

export function FixRefine({ repoPath, repoIdentity, jobs, selectedJobId, onSelectedJobIdChange, onOpenReview, onTerminal, onToast, panelRefine }: FixRefineProps) {
  const { t } = useI18n()
  const translateRef = useRef(t)
  translateRef.current = t
  const [preview, setPreview] = useState<FixPreview | null>(null)
  const [previewLoading, setPreviewLoading] = useState(false)
  const [previewError, setPreviewError] = useState('')
  const [dialogOpen, setDialogOpen] = useState(false)
  const [operation, setOperation] = useState<FixOperation | null>(null)
  const [operationError, setOperationError] = useState('')
  const [operationLoading, setOperationLoading] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [canceling, setCanceling] = useState(false)
  const previewGeneration = useRef(0)
  const operationGeneration = useRef(0)
  const terminalNotified = useRef<string | number | null>(null)
  const targetJobs = useMemo(() => jobs.filter(isFixTarget), [jobs])
  const selected = targetJobs.find(job => fixJobId(job) === selectedJobId) ?? null
  const selectedSignature = fixTargetSignature(selected)

  useEffect(() => {
    if (!selectedJobId || !selected) {
      if (selectedJobId !== null && !selected) onSelectedJobIdChange(null)
      setPreview(null)
      setPreviewError('')
      setDialogOpen(false)
      return
    }
    const generation = ++previewGeneration.current
    setPreview(null)
    setPreviewError('')
    setDialogOpen(false)
    setPreviewLoading(true)
    const url = `/api/fixes/preview?repositoryIdentity=${encodeURIComponent(repoIdentity)}&targetJobId=${encodeURIComponent(String(selectedJobId))}&agent=codex`
    request<unknown>(url).then(value => {
      if (generation !== previewGeneration.current) return
      const next = normalizeFixPreview(value)
      if (!next || next.repositoryIdentity !== repoIdentity || next.target.jobId !== selectedJobId || next.agent !== 'codex') throw new Error(translateRef.current('error.fixPreviewMalformed'))
      setPreview(next)
    }).catch(error => {
      if (generation === previewGeneration.current) setPreviewError((error as Error).message)
    }).finally(() => {
      if (generation === previewGeneration.current) setPreviewLoading(false)
    })
    return () => { previewGeneration.current++ }
  }, [repoIdentity, selectedJobId, selectedSignature])

  const loadOperations = useCallback(async () => {
    if (!repoIdentity) return null
    const generation = ++operationGeneration.current
    setOperationLoading(true)
    try {
      const value = normalizeFixOperationsResponse(await request<unknown>(`/api/fixes?repositoryIdentity=${encodeURIComponent(repoIdentity)}`))
      if (generation !== operationGeneration.current) return null
      const scoped = value.operation && value.operation.repositoryIdentity === repoIdentity ? value.operation : null
      setOperation(scoped)
      setOperationError('')
      return scoped
    } catch (error) {
      if (generation === operationGeneration.current) setOperationError((error as Error).message)
      return null
    } finally {
      if (generation === operationGeneration.current) setOperationLoading(false)
    }
  }, [repoIdentity])
  const loadOperation = useCallback(async (id: number | string) => {
    const raw = await request<unknown>(`/api/fixes/${encodeURIComponent(String(id))}`)
    const value = raw && typeof raw === 'object' && !Array.isArray(raw) && 'operation' in raw ? (raw as { operation?: unknown }).operation : raw
    return normalizeFixOperation(value)
  }, [])

  useEffect(() => {
    terminalNotified.current = null
    setOperation(null)
    setOperationError('')
    setPreview(null)
    setPreviewError('')
    if (!repoIdentity) return
    let cancelled = false
    const load = async () => {
      const next = await loadOperations()
      if (cancelled || !next || active(next) || terminalNotified.current === next.id || !['succeeded', 'failed', 'cancelled'].includes(next.phase)) return
      terminalNotified.current = next.id
      await onTerminal().catch(() => undefined)
    }
    void load()
    return () => { cancelled = true; operationGeneration.current++ }
  }, [loadOperations, onTerminal, repoIdentity])

  useEffect(() => {
    if (!operation || !active(operation)) return
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    const poll = async () => {
      let next: FixOperation | null
      try {
        next = await loadOperation(operation.id)
      } catch (error) {
        if (!cancelled) {
          setOperationError((error as Error).message)
          timer = setTimeout(poll, 1500)
        }
        return
      }
      if (cancelled) return
      if (next && next.repositoryIdentity === repoIdentity) setOperation(next)
      if (next && !active(next) && terminalNotified.current !== next.id && ['succeeded', 'failed', 'cancelled'].includes(next.phase)) {
        terminalNotified.current = next.id
        await onTerminal().catch(() => undefined)
      }
      // Keep the same operation attached across a transient detail response
      // failure. The backend remains the source of truth; a missing/malformed
      // poll result must not silently strand the UI in a running state.
      if (!cancelled && (!next || active(next))) timer = setTimeout(poll, 1500)
    }
    void poll()
    return () => { cancelled = true; if (timer) clearTimeout(timer) }
  }, [loadOperation, onTerminal, operation?.id, operation?.phase, repoIdentity])

  const submit = async () => {
    if (!preview || submitting || active(operation)) return
    setSubmitting(true)
    setOperationError('')
    try {
      const value = await request<unknown>('/api/fixes', { method: 'POST', body: JSON.stringify({ repositoryIdentity: preview.repositoryIdentity, targetJobId: preview.target.jobId, agent: 'codex', previewToken: preview.previewToken }) })
      const next = normalizeFixOperation((value && typeof value === 'object' && !Array.isArray(value) && 'operation' in value) ? (value as { operation?: unknown }).operation : value)
      if (!next) throw new Error(t('error.fixOperationMalformed'))
      setOperation(next)
      setDialogOpen(false)
      onToast?.('success', t('fix.started'))
      if (!active(next) && ['succeeded', 'failed', 'cancelled'].includes(next.phase) && terminalNotified.current !== next.id) {
        terminalNotified.current = next.id
        await onTerminal().catch(() => undefined)
      }
    } catch (error) {
      setOperationError((error as Error).message)
      onToast?.('error', (error as Error).message)
    } finally { setSubmitting(false) }
  }
  const cancel = async () => {
    if (!operation || canceling || !active(operation)) return
    setCanceling(true)
    try {
      await request(`/api/fixes/${encodeURIComponent(String(operation.id))}/cancel`, { method: 'POST' })
      onToast?.('success', t('fix.cancelRequested'))
      const next = await loadOperations()
      if (next && !active(next) && ['succeeded', 'failed', 'cancelled'].includes(next.phase) && terminalNotified.current !== next.id) {
        terminalNotified.current = next.id
        await onTerminal().catch(() => undefined)
      }
    } catch (error) {
      setOperationError((error as Error).message)
      onToast?.('error', (error as Error).message)
    } finally { setCanceling(false) }
  }
  const targetLabel = (job: ReviewJob) => `${job.commit_subject || job.subject || job.git_ref || `#${fixJobId(job)}`} · ${isSynthesisJob(job) ? t('fix.synthesis') : t('fix.single')}`
  return <>
    <section className="fix-refine-page" aria-label={t('fix.aria')}>
      <header className="section-heading"><div><h1>{t('fix.title')}</h1><p>{t('fix.description')}</p></div><span className="fix-agent-badge"><ShieldCheck size={15} />Codex</span></header>
      <section className="fix-workflow-card">
        <div className="fix-target-header"><div><h2>{t('fix.target')}</h2><p>{t('fix.targetDescription')}</p></div>{selected && <span className="status status-finding"><span className="status-dot" />{t('status.finding')}</span>}</div>
        {targetJobs.length ? <label className="fix-target-select" htmlFor="fix-target"><span>{t('fix.selectTarget')}</span><select id="fix-target" value={selectedJobId ?? ''} onChange={event => onSelectedJobIdChange(event.target.value ? Number(event.target.value) : null)} disabled={previewLoading || submitting}><option value="">{t('fix.chooseTarget')}</option>{targetJobs.map(job => <option key={fixJobId(job)} value={fixJobId(job) ?? ''}>{targetLabel(job)}</option>)}</select></label> : <div className="fix-empty"><Code2 size={19} /><div><strong>{t('fix.noTargets')}</strong><p>{t('fix.noTargetsBody')}</p></div></div>}
        {selected && <div className="fix-target-facts"><span><b>{t('fix.job')}</b>#{fixJobId(selected)}</span><span><b>{t('fix.gitRef')}</b><code>{short(selected.git_ref)}</code></span><span><b>{t('fix.branch')}</b>{selected.branch || '—'}</span><span><b>{t('fix.targetKind')}</b>{isSynthesisJob(selected) ? t('fix.synthesis') : t('fix.single')}</span></div>}
        {previewError && <p className="fix-error" role="alert">{t('fix.previewFailed', { error: previewError })}</p>}
        {selected && <div className="fix-actions"><button className="button primary" disabled={!preview || previewLoading || Boolean(previewError) || active(operation)} onClick={() => setDialogOpen(true)}>{previewLoading ? <RefreshCw className="spin" size={15} /> : <Play size={15} />}{previewLoading ? t('fix.loadingPreview') : t('fix.reviewPreview')}</button>{preview && <small>{t('fix.previewReady')}</small>}</div>}
      </section>
      {operationLoading && !operation && <div className="skeleton" />}
      {operationError && !previewError && <p className="fix-error" role="alert">{operationError} <button className="button ghost compact" onClick={() => void loadOperations()}>{t('common.retry')}</button></p>}
      {operation && <article className={`fix-operation-card ${phaseTone(operation)}`} aria-live="polite"><header><div><span className="fix-operation-id">{t('fix.operation', { id: operation.id })}</span><h2>{phaseLabel(operation, t)}</h2></div><span className={`fix-phase ${phaseTone(operation)}`}><span className="status-dot" />{phaseLabel(operation, t)}</span></header><div className="fix-operation-facts"><span><b>{t('fix.target')}</b>#{operation.target.jobId}</span><span><b>{t('fix.startingHead')}</b><code>{short(operation.startingHead)}</code></span><span><b>{t('fix.endingHead')}</b><code>{short(operation.endingHead)}</code></span><span><b>{t('fix.endingBranch')}</b>{operation.endingBranch || '—'}</span>{operation.endingDirty !== undefined && <span><b>{t('fix.endingDirty')}</b>{operation.endingDirty ? t('status.dirty') : t('status.clean')}</span>}</div>{operation.error && <p className="fix-error" role="alert">{operation.error}{operation.errorCode && <small> · {operation.errorCode}</small>}</p>}<footer>{operation.phase === 'succeeded' && <button className="button" onClick={() => onOpenReview(operation.target.jobId)}>{t('fix.openReview')}</button>}{active(operation) && <button className="button danger" disabled={canceling} onClick={() => void cancel()}><CircleStop size={15} />{canceling ? t('fix.canceling') : t('fix.cancel')}</button>}</footer></article>}
      <PanelRefine {...panelRefine} />
    </section>
    {dialogOpen && preview && <div className="prompt-overlay" role="presentation"><section className="prompt-dialog fix-dialog" role="dialog" aria-modal="true" aria-labelledby="fix-confirm-title"><header><div><h2 id="fix-confirm-title">{t('fix.confirmTitle')}</h2><p>{t('fix.confirmDescription')}</p></div><div className="modal-header-actions"><LanguageSelector compact /><button className="icon-button" aria-label={t('fix.closePreview')} onClick={() => setDialogOpen(false)} disabled={submitting}><X /></button></div></header><div className="fix-preview-facts"><span><b>{t('fix.repository')}</b><code>{preview.repositoryPath}</code></span><span><b>{t('fix.target')}</b>#{preview.target.jobId} · {preview.target.gitRef}</span><span><b>{t('fix.startingHead')}</b><code>{preview.startingHead}</code> · {preview.startingBranch}</span></div><div className="fix-command-preview"><strong>{t('fix.command')}</strong><code className="fix-executable">{preview.command.executable}</code><ol>{preview.command.args.map((arg, index) => <li key={`${index}-${arg}`}><span>argv[{index}]</span><code>{arg}</code></li>)}</ol></div><p className="fix-safety"><AlertTriangle size={16} />{t('fix.safety')}</p><footer><button className="button" onClick={() => setDialogOpen(false)} disabled={submitting}>{t('common.cancel')}</button><button className="button primary" onClick={() => void submit()} disabled={submitting}>{submitting ? t('fix.confirming') : <><Check size={15} />{t('fix.confirmAction')}</>}</button></footer></section></div>}
  </>
}
