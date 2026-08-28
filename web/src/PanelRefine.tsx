import { Check, CircleStop, ShieldCheck, XCircle } from 'lucide-react'
import { type PanelConfiguration, type PanelRefineIteration, type PanelRefineOptions, type PanelRefinePhase, type PanelRefineRun } from './api'
import { useI18n } from './i18n'

type RefineStart = { repoPath: string; panel: string; fixAgent: 'codex'; maxFixAttempts: number; finalPanel?: string }

export type PanelRefineProps = {
  repoPath: string
  panelConfiguration: PanelConfiguration | null
  options: PanelRefineOptions | null
  run: PanelRefineRun | null
  available?: boolean
  disabledReason?: string
  loading?: boolean
  error?: string
  busy?: boolean
  onStart: (request: RefineStart) => Promise<void>
  onCancel: (id: number | string) => Promise<void>
  onOpenReview?: (id: number) => void
}
const activePhases: PanelRefinePhase[] = ['starting', 'reviewing', 'evaluating', 'fixing', 'final_reviewing']
const terminalPhases: PanelRefinePhase[] = ['passed', 'needs_attention', 'failed', 'cancelled']

function phaseLabel(phase: PanelRefinePhase, t?: ReturnType<typeof useI18n>['t']) {
  if (t) {
    if (phase === 'passed') return t('status.pass')
    if (phase === 'needs_attention') return t('panel.needsAttention')
    if (phase === 'failed') return t('status.error')
    if (phase === 'cancelled') return t('status.canceled')
    if (phase === 'final_reviewing') return t('panel.finalReview')
    if (phase === 'starting') return t('panel.phaseStarting')
    if (phase === 'reviewing') return t('panel.phaseReviewing')
    if (phase === 'evaluating') return t('panel.phaseEvaluating')
    if (phase === 'fixing') return t('panel.phaseFixing')
  }
  return phase === 'final_reviewing' ? 'Final review' : phase === 'needs_attention' ? 'Needs attention' : phase.replaceAll('_', ' ').replace(/\b\w/g, value => value.toUpperCase())
}
function phaseTone(phase: PanelRefinePhase) {
  if (phase === 'passed') return 'pass'
  if (phase === 'needs_attention') return 'attention'
  if (phase === 'failed' || phase === 'cancelled') return 'failed'
  return 'running'
}
function isActive(run: PanelRefineRun | null) { return Boolean(run && activePhases.includes(run.phase)) }
function head(value?: string) { return value ? value.slice(0, 12) : '—' }
function reviewResult(value?: string) { return value === 'P' || value === 'pass' || value === 'passed' ? 'Pass' : value === 'F' || value === 'fail' || value === 'failed' ? 'Finding' : value || 'Pending' }
function reviewResultLabel(value: string, t: ReturnType<typeof useI18n>['t']) {
  if (value === 'Pass') return t('status.pass')
  if (value === 'Finding') return t('status.finding')
  if (value === 'Pending') return t('panel.pending')
  if (value === 'Cancelled') return t('panel.resultCancelled')
  if (value === 'Error') return t('panel.resultError')
  return value
}
function durationLabel(start?: string, end?: string) {
  const startTime = start ? Date.parse(start) : Number.NaN
  const endTime = end ? Date.parse(end) : Number.NaN
  if (!Number.isFinite(startTime) || !Number.isFinite(endTime) || endTime < startTime) return '—'
  const totalSeconds = Math.floor((endTime - startTime) / 1000)
  const hours = Math.floor(totalSeconds / 3600)
  const minutes = Math.floor((totalSeconds % 3600) / 60)
  const seconds = totalSeconds % 60
  return hours ? `${hours}h ${minutes}m ${seconds}s` : minutes ? `${minutes}m ${seconds}s` : `${seconds}s`
}
function finalPanelResult(run: PanelRefineRun, t: ReturnType<typeof useI18n>['t']) {
  if (!run.finalPanel) return t('panel.notConfigured')
  const iteration = [...run.iterations].reverse().find(item => item.panel === run.finalPanel)
  if (!iteration) return t('panel.notRun')
  return iteration.error ? t('panel.failed') : reviewResultLabel(reviewResult(iteration.reviewResult), t)
}
function PhasePill({ phase }: { phase: PanelRefinePhase }) {
  const { t } = useI18n()
  return <span className={`refine-phase ${phaseTone(phase)}`}><span className="status-dot" />{phaseLabel(phase, t)}</span>
}

function isCancellationError(error?: string) { return Boolean(error && /cancel(?:led|ed)|context (?:done|cancel)/i.test(error)) }
function iterationResult(item: PanelRefineIteration) {
  if (item.reviewResult === 'P' || item.reviewResult === 'pass' || item.reviewResult === 'passed') return 'Pass'
  if (item.reviewResult === 'F' || item.reviewResult === 'fail' || item.reviewResult === 'failed') return 'Finding'
  if (item.error) return isCancellationError(item.error) ? 'Cancelled' : 'Error'
  return 'Pending'
}
function iterationTone(item: PanelRefineIteration) {
  const result = iterationResult(item)
  if (result === 'Pass') return 'pass'
  if (result === 'Finding') return 'failed'
  if (result === 'Error') return 'error'
  if (result === 'Cancelled') return 'cancelled'
  return isActivePhase(item) ? 'running' : ''
}
function IterationRow({ item, fixAttemptNumber, onOpenReview }: { item: PanelRefineIteration; fixAttemptNumber?: number; onOpenReview?: (id: number) => void }) {
  const { t } = useI18n()
  const result = iterationResult(item)
  return <li className="refine-timeline-item">
    <span className={'refine-timeline-mark ' + iterationTone(item)}>{result === 'Pass' ? <Check size={13} /> : result === 'Finding' || result === 'Error' ? <XCircle size={13} /> : result === 'Cancelled' ? <CircleStop size={13} /> : <span />}</span>
    <div><strong>{item.panel}</strong><span>{t('panel.reviewAt', { head: item.targetHead ? head(item.targetHead) : '—' })} · {reviewResultLabel(result, t)}</span>{item.fixAttempted && <span>{t('panel.fixAt', { attempt: fixAttemptNumber ?? '—', agent: item.fixAgent || 'codex' })}{item.headAfterFix ? ' · ' + head(item.headAfterFix) : ''}</span>}{item.error && <small className="refine-error">{item.error}</small>}</div>
    {item.reviewJobID && <button className="button ghost compact" onClick={() => onOpenReview?.(item.reviewJobID!)}>{t('panel.openReview')}</button>}
  </li>
}
function isActivePhase(item: PanelRefineIteration) { return !item.reviewResult && !item.error }

export function PanelRefine({ repoPath, panelConfiguration, options, run, available = true, disabledReason = '', loading = false, error = '', busy = false, onStart, onCancel, onOpenReview }: PanelRefineProps) {
  const { t } = useI18n()
  const active = isActive(run)
  const terminal = Boolean(run && terminalPhases.includes(run.phase))
  const title = run?.phase === 'passed' ? t('panel.titlePassed') : run?.phase === 'needs_attention' ? t('panel.titleNeedsAttention') : run?.phase === 'failed' ? t('panel.titleFailed') : run?.phase === 'cancelled' ? t('panel.titleCanceled') : t('panel.start')
  return <section className="panel-refine" aria-label={t('panel.aria')}>
    <p className="refine-disabled-reason" role="status" id="panel-refine-disabled-reason">{t('panel.executionDisabled')}</p>
    {!available && disabledReason && <p className="refine-disabled-reason" role="status">{t('panel.refineUnavailable', { reason: disabledReason })}</p>}
    <div className="section-heading refine-heading"><div><span className="eyebrow">{t('panel.automatedGate')}</span><h2>Panel Refine</h2><p>{t('panel.description')}</p></div><button className="button primary" type="button" disabled aria-describedby="panel-refine-disabled-reason"><ShieldCheck size={16} />{t('panel.executionDisabledCta')}</button></div>
    {error && <p className="refine-inline-error" role="alert">{t('panel.refreshedError', { error })}</p>}
    {loading && !run && <div className="skeleton" />}
    {run && <article className={`refine-card ${phaseTone(run.phase)}`}>
      <header className="refine-card-header"><div><span className="eyebrow">{t('panel.run', { id: run.id })}</span><h3>{title}</h3></div><PhasePill phase={run.phase} /></header>
      <div className="refine-grid"><span><strong>{t('panel.totalReviews')}</strong>{run.iterations.length}</span><span><strong>{t('panel.fixAttempts')}</strong>{run.fixAttempts} / {run.maxFixAttempts}</span><span><strong>{t('panel.iterationPanel')}</strong>{run.iterationPanel}</span><span><strong>{t('panel.finalPanel')}</strong>{run.finalPanel || t('panel.none')}</span><span><strong>{t('panel.finalResult')}</strong>{finalPanelResult(run, t)}</span><span><strong>{t('panel.fixAgent')}</strong>{run.fixAgent}</span><span><strong>{t('panel.startedHead')}</strong><code>{head(run.startingHead)}</code></span><span><strong>{t('panel.currentHead')}</strong><code>{head(run.currentHead)}</code></span>{terminal && <span><strong>{t('panel.elapsed')}</strong>{durationLabel(run.startedAt, run.completedAt)}</span>}{run.currentReviewJobID && <span><strong>{t('panel.currentReview')}</strong><button className="link-button" onClick={() => onOpenReview?.(run.currentReviewJobID!)}>#{run.currentReviewJobID}</button></span>}</div>
      {(run.error || run.stopReason) && <p className="refine-reason" role="alert">{run.error || run.stopReason}</p>}
      {run.cancelState && <div className="refine-cancel-state" role="status"><strong>{t('panel.cancelState')}</strong>{run.cancelState.available ? <span>HEAD: {head(run.cancelState.head)} · {t('panel.branch')}: {run.cancelState.branch || '—'} · {run.cancelState.dirty ? t('panel.dirtyTree') : t('panel.cleanTree')}</span> : <span>{t('panel.branchStateUnavailable')}</span>}{run.cancelState.error && <small>{run.cancelState.error}</small>}{!run.cancelState.available ? <b>{t('panel.warningFinalState')}</b> : run.cancelState.dirty && <b>{t('panel.warningPartialChanges')}</b>}</div>}
      <div className="refine-timeline"><strong>{t('panel.timeline')}</strong><ol>{run.iterations.map((item, index) => <IterationRow key={item.number + '-' + index} item={item} fixAttemptNumber={item.fixAttempted ? run.iterations.slice(0, index + 1).filter(candidate => candidate.fixAttempted).length : undefined} onOpenReview={onOpenReview} />)}</ol></div>
      <footer className="refine-card-actions">{terminal && run.currentReviewJobID && <button className="button" onClick={() => onOpenReview?.(run.currentReviewJobID!)}>{run.phase === 'passed' ? t('panel.openFinalReview') : t('panel.openLatestReview')}</button>}{active && <button className="button danger" disabled={busy} onClick={() => { void onCancel(run.id).catch(() => undefined) }}><CircleStop size={15} />{t('panel.cancel')}</button>}</footer>
    </article>}
  </section>
}
