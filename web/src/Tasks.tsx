import { Ban, RotateCcw, ShieldAlert, TerminalSquare } from 'lucide-react'
import { useI18n } from './i18n'
import { TASK_HISTORY_LIMIT, type Repository, type ReviewJob } from './api'
import { jobStatus, jobStatusLabel } from './jobStatus'

function taskJobId(job: ReviewJob) {
  const value = job.id ?? job.job_id
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0 ? value : null
}

export type TasksProps = {
  repository: Repository | null
  /** History slice loaded for exactly this repository at a raised bounded limit. */
  jobs: ReviewJob[]
  /** Load failure for the current slice; empty state is shown only without it. */
  historyError?: string
  onRetryHistory?: () => void
  onOpenReview: (id: number) => void
}

/**
 * Tasks shows existing RoboRev custom task history only. v0.64 execution is
 * fail-closed on the backend (`task-execution-disabled`) because the daemon
 * exposes no machine-readable attestation of its effective safety state, so
 * this screen never renders an execution CTA and never calls /api/tasks*.
 */
export function Tasks({ repository, jobs, historyError, onRetryHistory, onOpenReview }: TasksProps) {
  const { t } = useI18n()
  // jobs is the backend-scoped /api/reviews list for the selected repository;
  // raw repo_path strings are never re-compared in the browser because the
  // daemon's path spelling differs from the registry's canonical form. The
  // task identification predicate matches the backend rerun boundary and
  // ReviewDetail exactly (job_type OR git_ref), so a task job blocked from
  // rerun is always visible here.
  const taskJobs = jobs.filter(job => job.job_type === 'task' || job.git_ref === 'run')

  return <section className="tasks-page">
    <header className="section-heading"><div><span className="eyebrow">{t('tasks.eyebrow')}</span><h1>{t('tasks.title')}</h1><p>{t('tasks.description')}</p></div></header>
    <div className="tasks-layout">
      <section className="task-composer" aria-labelledby="tasks-composer-title">
        <div className="task-card-heading"><div><h2 id="tasks-composer-title">{t('tasks.customTask')}</h2><p>{t('tasks.executionDisabledScope')}</p></div><span className="task-badge"><Ban size={15} />{t('tasks.executionDisabledBadge')}</span></div>
        <label htmlFor="task-input">{t('tasks.promptLabel')}</label>
        <textarea id="task-input" rows={8} spellCheck="false" disabled aria-describedby="task-execution-disabled-reason" placeholder={t('tasks.executionDisabledPlaceholder')} />
        <p className="task-safety-copy" role="note"><ShieldAlert size={15} /><strong>{t('tasks.executionDisabledBadge')}</strong> {t('tasks.executionDisabledReason')}</p>
        <p className="task-context-hint" id="task-execution-disabled-reason">{t('tasks.executionDisabledHint')}</p>
      </section>
      <section className="task-history" aria-labelledby="tasks-history-title"><div className="task-card-heading"><div><h2 id="tasks-history-title">{t('tasks.history')}</h2><p>{t('tasks.historyDescription', { limit: TASK_HISTORY_LIMIT })}</p></div><TerminalSquare size={19} /></div>{historyError && <p className="task-safety-copy" role="alert"><ShieldAlert size={15} />{t('tasks.historyFailed', { error: historyError })}{onRetryHistory && <button type="button" className="button compact" onClick={onRetryHistory}><RotateCcw size={13} />{t('common.retry')}</button>}</p>}{!historyError && taskJobs.length === 0 ? <div className="task-empty"><TerminalSquare size={23} /><p>{t('tasks.noHistory', { limit: TASK_HISTORY_LIMIT })}</p></div> : <div className="task-list">{taskJobs.map(job => { const id = taskJobId(job); const status = jobStatus(job); return <article className="task-row" key={`${id ?? 'task'}-${job.uuid ?? job.job_uuid ?? id}`}><div className="task-row-main"><strong>#{id ?? '—'}</strong><span className="task-row-subject">run</span><span className={`status status-${status}`}><span className="status-dot" />{jobStatusLabel(status, t)}</span></div><div className="task-row-meta"><span>run</span><span>{typeof job.agent === 'string' && job.agent.trim() ? job.agent : t('status.unknownAgent')}</span>{id && <button className="button compact" onClick={() => onOpenReview(id)}>{t('tasks.viewResult')}</button>}</div></article> })}</div>}</section>
    </div>
  </section>
}
