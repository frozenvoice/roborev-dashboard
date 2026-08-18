import { useEffect, useState } from 'react'
import { Check, CircleStop, Play, ShieldCheck, X, XCircle } from 'lucide-react'
import { type PanelConfiguration, type PanelInfo, type PanelRefineIteration, type PanelRefineOptions, type PanelRefinePhase, type PanelRefineRun } from './api'

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

function phaseLabel(phase: PanelRefinePhase) {
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
function finalPanelResult(run: PanelRefineRun) {
  if (!run.finalPanel) return 'Not configured'
  const iteration = [...run.iterations].reverse().find(item => item.panel === run.finalPanel)
  if (!iteration) return 'Not run'
  return iteration.error ? 'Failed' : reviewResult(iteration.reviewResult)
}
function validNamedPanels(configuration: PanelConfiguration | null): PanelInfo[] {
  return configuration?.panels.filter(panel => panel.valid && panel.kind === 'panel' && panel.name !== 'none') ?? []
}

function PhasePill({ phase }: { phase: PanelRefinePhase }) {
  return <span className={`refine-phase ${phaseTone(phase)}`}><span className="status-dot" />{phaseLabel(phase)}</span>
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
  const result = iterationResult(item)
  return <li className="refine-timeline-item">
    <span className={'refine-timeline-mark ' + iterationTone(item)}>{result === 'Pass' ? <Check size={13} /> : result === 'Finding' || result === 'Error' ? <XCircle size={13} /> : result === 'Cancelled' ? <CircleStop size={13} /> : <span />}</span>
    <div><strong>{item.panel}</strong><span>Review {item.targetHead ? head(item.targetHead) : '—'} · {result}</span>{item.fixAttempted && <span>Fix #{fixAttemptNumber ?? '—'} · {item.fixAgent || 'codex'}{item.headAfterFix ? ' · ' + head(item.headAfterFix) : ''}</span>}{item.error && <small className="refine-error">{item.error}</small>}</div>
    {item.reviewJobID && <button className="button ghost compact" onClick={() => onOpenReview?.(item.reviewJobID!)}>Open Review</button>}
  </li>
}
function isActivePhase(item: PanelRefineIteration) { return !item.reviewResult && !item.error }

export function PanelRefine({ repoPath, panelConfiguration, options, run, available = true, disabledReason = '', loading = false, error = '', busy = false, onStart, onCancel, onOpenReview }: PanelRefineProps) {
  const [dialogOpen, setDialogOpen] = useState(false)
  const panels = validNamedPanels(panelConfiguration)
  const defaultPanel = panels.some(panel => panel.name === panelConfiguration?.selectedPanel) ? panelConfiguration?.selectedPanel ?? panels[0]?.name ?? '' : panels[0]?.name ?? ''
  const [iterationPanel, setIterationPanel] = useState(defaultPanel)
  const [finalPanel, setFinalPanel] = useState('')
  const [maxFixAttempts, setMaxFixAttempts] = useState(3)
  const defaultAttempts = options?.defaultMaxFixAttempts ?? 3

  useEffect(() => {
    if (dialogOpen) return
    setIterationPanel(defaultPanel)
  }, [defaultPanel, dialogOpen])

  useEffect(() => {
    if (!dialogOpen) return
    setFinalPanel('')
    setMaxFixAttempts(defaultAttempts)
  }, [defaultAttempts, dialogOpen])

  const start = async () => {
    if (!repoPath || !iterationPanel) return
    try {
      await onStart({ repoPath, panel: iterationPanel, fixAgent: 'codex', maxFixAttempts, ...(finalPanel ? { finalPanel } : {}) })
      setDialogOpen(false)
    } catch {
      // The parent owns direct-action feedback; keep the dialog open so the
      // user can inspect or adjust the request after a rejected start.
    }
  }
  const active = isActive(run)
  const terminal = Boolean(run && terminalPhases.includes(run.phase))
  const optionsReady = Boolean(options && options.executable && options.args.length && options.allowedFixAgents.includes('codex') && options.defaultFixAgent === 'codex' && options.defaultMaxFixAttempts >= options.minFixAttempts && options.defaultMaxFixAttempts <= options.maxFixAttempts)
  const canOpen = Boolean(available && optionsReady && panels.length && repoPath && !loading)
  const canStart = Boolean(canOpen && panels.some(panel => panel.name === iterationPanel) && maxFixAttempts >= (options?.minFixAttempts ?? 1) && maxFixAttempts <= (options?.maxFixAttempts ?? 10))
  const title = run?.phase === 'passed' ? 'Panel Refine passed' : run?.phase === 'needs_attention' ? 'Panel Refine needs attention' : run?.phase === 'failed' ? 'Panel Refine failed' : run?.phase === 'cancelled' ? 'Panel Refine cancelled' : 'Panel Refine'
  return <section className="panel-refine" aria-label="Panel Refine">
    {!available && disabledReason && <p className="refine-disabled-reason" role="status">Panel Refine unavailable: {disabledReason}</p>}
    <div className="section-heading refine-heading"><div><span className="eyebrow">Automated quality gate</span><h2>Panel Refine</h2><p>Repeat the selected panel review, apply a Codex fix, and verify each new HEAD.</p></div><button className="button primary" disabled={busy || active || !canOpen} onClick={() => setDialogOpen(true)}><ShieldCheck size={16} />Panel Refine</button></div>
    {error && <p className="refine-inline-error" role="alert">Panel Refine could not be refreshed: {error}</p>}
    {loading && !run && <div className="skeleton" />}
    {run && <article className={`refine-card ${phaseTone(run.phase)}`}>
      <header className="refine-card-header"><div><span className="eyebrow">Run #{run.id}</span><h3>{title}</h3></div><PhasePill phase={run.phase} /></header>
      <div className="refine-grid"><span><strong>Total reviews</strong>{run.iterations.length}</span><span><strong>Fix attempts</strong>{run.fixAttempts} / {run.maxFixAttempts}</span><span><strong>Iteration panel</strong>{run.iterationPanel}</span><span><strong>Final panel</strong>{run.finalPanel || 'None'}</span><span><strong>Final panel result</strong>{finalPanelResult(run)}</span><span><strong>Fix agent</strong>{run.fixAgent}</span><span><strong>Started HEAD</strong><code>{head(run.startingHead)}</code></span><span><strong>Current HEAD</strong><code>{head(run.currentHead)}</code></span>{terminal && <span><strong>Elapsed</strong>{durationLabel(run.startedAt, run.completedAt)}</span>}{run.currentReviewJobID && <span><strong>Current review</strong><button className="link-button" onClick={() => onOpenReview?.(run.currentReviewJobID!)}>#{run.currentReviewJobID}</button></span>}</div>
      {(run.error || run.stopReason) && <p className="refine-reason" role="alert">{run.error || run.stopReason}</p>}
      {run.cancelState && <div className="refine-cancel-state" role="status"><strong>Repository state after cancellation</strong>{run.cancelState.available ? <span>HEAD: {head(run.cancelState.head)} · Branch: {run.cancelState.branch || '—'} · {run.cancelState.dirty ? 'Dirty working tree' : 'Clean working tree'}</span> : <span>HEAD / branch state unavailable</span>}{run.cancelState.error && <small>{run.cancelState.error}</small>}{!run.cancelState.available ? <b>Warning: the final repository state could not be verified. Inspect the working tree before continuing.</b> : run.cancelState.dirty && <b>Warning: cancellation may have left partial repository changes. Inspect the working tree before continuing.</b>}</div>}
      <div className="refine-timeline"><strong>Timeline</strong><ol>{run.iterations.map((item, index) => <IterationRow key={item.number + '-' + index} item={item} fixAttemptNumber={item.fixAttempted ? run.iterations.slice(0, index + 1).filter(candidate => candidate.fixAttempted).length : undefined} onOpenReview={onOpenReview} />)}</ol></div>
      <footer className="refine-card-actions">{terminal && run.currentReviewJobID && <button className="button" onClick={() => onOpenReview?.(run.currentReviewJobID!)}>{run.phase === 'passed' ? 'Open Final Review' : 'Open Latest Review'}</button>}{active && <button className="button danger" disabled={busy} onClick={() => { void onCancel(run.id).catch(() => undefined) }}><CircleStop size={15} />Cancel Panel Refine</button>}</footer>
    </article>}
    {dialogOpen && <div className="prompt-overlay" role="presentation"><section className="prompt-dialog refine-dialog" role="dialog" aria-modal="true" aria-labelledby="panel-refine-title"><header><div><span className="eyebrow">Automated quality gate</span><h2 id="panel-refine-title">Panel Refine</h2><p>Panel Refine repeatedly reviews the current HEAD with the selected panel, uses the failed synthesis review to create a fix, commits the fix, and reviews the new HEAD until it passes or reaches the limit.</p></div><button className="icon-button" aria-label="Close Panel Refine" onClick={() => setDialogOpen(false)}><X /></button></header><div className="refine-form"><label htmlFor="refine-iteration-panel">Iteration panel<select id="refine-iteration-panel" value={iterationPanel} onChange={event => setIterationPanel(event.target.value)}>{panels.map(panel => <option key={panel.name} value={panel.name}>{panel.name}</option>)}</select></label><label htmlFor="refine-fix-agent">Fix agent<select id="refine-fix-agent" value="codex" disabled><option value="codex">Codex</option></select></label><label htmlFor="refine-attempts">Max fix attempts<select id="refine-attempts" value={maxFixAttempts} onChange={event => setMaxFixAttempts(Number(event.target.value))}>{Array.from({ length: (options?.maxFixAttempts ?? 10) - (options?.minFixAttempts ?? 1) + 1 }, (_, index) => index + (options?.minFixAttempts ?? 1)).map(value => <option key={value} value={value}>{value}{value === defaultAttempts ? ' (default)' : ''}</option>)}</select></label><label htmlFor="refine-final-panel">Final deep panel<select id="refine-final-panel" value={finalPanel} onChange={event => setFinalPanel(event.target.value)}><option value="">None</option>{panels.map(panel => <option key={panel.name} value={panel.name}>{panel.name}</option>)}</select></label></div><div className="refine-preview"><strong>Execution preview</strong><code>POST /api/panel-refines</code><code>{JSON.stringify({ repoPath, panel: iterationPanel, fixAgent: 'codex', maxFixAttempts, ...(finalPanel ? { finalPanel } : {}) })}</code><code>{options?.executable ? JSON.stringify([options.executable, ...options.args]) : 'Loading backend command options…'}</code><small>Server-side fix adapter uses the backend-provided executable and exact typed argv. No shell command or executable is accepted from the browser.</small></div><footer><button className="button" onClick={() => setDialogOpen(false)}>Cancel</button><button className="button primary" disabled={!canStart || busy} onClick={() => void start()}><Play size={16} />Start Panel Refine</button></footer></section></div>}
  </section>
}
