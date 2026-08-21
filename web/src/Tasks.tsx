import { AlertTriangle, Play, ShieldCheck, TerminalSquare, X, XCircle } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { APIError, request, type Repository, type ReviewJob } from './api'
import { useI18n } from './i18n'

export const TASK_MAX_BYTES = 16 * 1024
// Match Go's unicode.IsControl for the Basic Latin/C1 ranges while keeping
// the prompt-friendly exceptions LF, CR, and TAB.
const taskUnsupportedControlPattern = /[\u0000-\u0008\u000B\u000C\u000E-\u001F\u007F-\u009F]/

/** Validate without normalising: the exact text is sent to RoboRev. */
export function validateTaskText(value: string): string | null {
  if (!value.trim()) return 'empty'
  if (new TextEncoder().encode(value).byteLength > TASK_MAX_BYTES) return 'oversize'
  if (taskUnsupportedControlPattern.test(value)) return 'control'
  return null
}

export type TaskSafety = {
  allowUnsafeAgents: boolean
  disableCodexSandbox: boolean
}

export type TaskPreview = {
  repositoryIdentity: string
  repositoryPath: string
  head: string
  branch: string
  agent: 'codex'
  mode: 'read-only'
  safety: TaskSafety
  command: { executable: string; args: ['run', '--agent', 'codex', '--json']; stdin: string }
  previewToken: string
}

export type CreatedTaskReceipt = {
  kind: 'created'
  jobId: number
  jobUUID: string
  gitRef: 'run'
  status: 'queued'
}

export type SkippedTaskReceipt = {
  kind: 'skipped'
  reason: string
}

export type TaskReceipt = CreatedTaskReceipt | SkippedTaskReceipt

function recordValue(value: unknown): Record<string, unknown> | null {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null
}
function alias(object: Record<string, unknown>, camel: string, snake: string) { return object[camel] ?? object[snake] }
function nonemptyString(value: unknown, max = 64 * 1024): string | null {
  return typeof value === 'string' && value.trim() && value.length <= max && !taskUnsupportedControlPattern.test(value) ? value : null
}
function positiveId(value: unknown) {
  const parsed = typeof value === 'number' ? value : typeof value === 'string' && /^\d+$/.test(value) ? Number(value) : NaN
  return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : null
}
function isUUID(value: string) {
  return /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value)
}

/** Normalize the server-owned exact command preview. This is intentionally strict. */
export function normalizeTaskPreview(value: unknown, expectedTask?: string): TaskPreview | null {
  const object = recordValue(value)
  if (!object) return null
  const repositoryIdentity = nonemptyString(alias(object, 'repositoryIdentity', 'repository_identity'), 4_096)
  const repositoryPath = nonemptyString(alias(object, 'repositoryPath', 'repository_path'), 32 * 1024)
  const head = nonemptyString(object.head, 256)
  const branch = nonemptyString(object.branch, 1_024)
  const agent = object.agent
  const mode = object.mode
  const safetyObject = recordValue(object.safety)
  const commandObject = recordValue(object.command)
  const previewToken = nonemptyString(alias(object, 'previewToken', 'preview_token'), 16 * 1024)
  if (!repositoryIdentity || !repositoryPath || !head || !branch || agent !== 'codex' || mode !== 'read-only' || !safetyObject || !commandObject || !previewToken) return null
  const allowUnsafeAgents = alias(safetyObject, 'allowUnsafeAgents', 'allow_unsafe_agents')
  const disableCodexSandbox = alias(safetyObject, 'disableCodexSandbox', 'disable_codex_sandbox')
  if (typeof allowUnsafeAgents !== 'boolean' || typeof disableCodexSandbox !== 'boolean') return null
  const executable = nonemptyString(commandObject.executable, 32 * 1024)
  const args = commandObject.args
  const stdin = commandObject.stdin
  const expectedArgs = ['run', '--agent', 'codex', '--json']
  if (!executable || !Array.isArray(args) || args.length !== expectedArgs.length || args.some((item, index) => item !== expectedArgs[index]) || typeof stdin !== 'string') return null
  if (new TextEncoder().encode(stdin).byteLength > TASK_MAX_BYTES || taskUnsupportedControlPattern.test(stdin) || (expectedTask !== undefined && stdin !== expectedTask)) return null
  return {
    repositoryIdentity,
    repositoryPath,
    head,
    branch,
    agent: 'codex',
    mode: 'read-only',
    safety: { allowUnsafeAgents, disableCodexSandbox },
    command: { executable, args: ['run', '--agent', 'codex', '--json'], stdin },
    previewToken,
  }
}

/** Parse exactly one of RoboRev v0.64's created/skipped launch receipts. */
export function normalizeTaskReceipt(value: unknown): TaskReceipt | null {
  const object = recordValue(value)
  if (!object) return null
  if (object.skipped === true) {
    const reason = nonemptyString(object.reason, 4_096)
    return reason ? { kind: 'skipped', reason } : null
  }
  if (object.skipped !== undefined) return null
  const jobId = positiveId(alias(object, 'jobId', 'job_id'))
  const jobUUID = nonemptyString(alias(object, 'jobUUID', 'job_uuid'), 128)
  const gitRef = alias(object, 'gitRef', 'git_ref')
  const status = object.status
  if (jobId === null || !jobUUID || !isUUID(jobUUID) || gitRef !== 'run' || status !== 'queued') return null
  return { kind: 'created', jobId, jobUUID, gitRef: 'run', status: 'queued' }
}

function taskStatus(job: ReviewJob) {
  const value = String(job.status || '').toLowerCase()
  if (value === 'queued') return 'queued'
  if (['running', 'processing', 'started'].includes(value)) return 'running'
  if (['done', 'completed', 'succeeded', 'passed', 'pass', 'finding'].includes(value)) return 'done'
  if (value === 'failed') return 'failed'
  if (value === 'error') return 'error'
  if (value === 'canceled' || value === 'cancelled') return 'canceled'
  return value || 'queued'
}
function taskStatusLabel(value: string, t: ReturnType<typeof useI18n>['t']) {
  if (value === 'queued') return t('status.queued')
  if (value === 'running') return t('status.running')
  if (value === 'done') return t('status.done')
  if (value === 'failed' || value === 'error') return t('status.error')
  if (value === 'canceled') return t('status.canceled')
  return t('status.unknown')
}
function taskJobId(job: ReviewJob) {
  const value = job.id ?? job.job_id
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0 ? value : null
}
function taskJobUUID(job: ReviewJob) {
  const value = job.uuid ?? job.job_uuid
  return typeof value === 'string' && value ? value : undefined
}

export type TasksProps = {
  repository: Repository | null
  jobs: ReviewJob[]
  /** Dashboard passes the already-observed daemon health; backend rechecks it. */
  daemonReady?: boolean
  onOpenReview: (id: number) => void
  onQueued?: (receipt: CreatedTaskReceipt) => void | Promise<void>
}

export function Tasks({ repository, jobs, daemonReady = true, onOpenReview, onQueued }: TasksProps) {
  const { t } = useI18n()
  const [task, setTask] = useState('')
  const [preview, setPreview] = useState<TaskPreview | null>(null)
  const [previewError, setPreviewError] = useState('')
  const [previewLoading, setPreviewLoading] = useState(false)
  const [confirming, setConfirming] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [receipt, setReceipt] = useState<CreatedTaskReceipt | null>(null)
  const [receiptTask, setReceiptTask] = useState('')
  const [outcome, setOutcome] = useState<'skipped' | 'ambiguous' | null>(null)
  const [outcomeMessage, setOutcomeMessage] = useState('')
  const generation = useRef(0)
  const submitLockRef = useRef(false)
  const previousRepositoryIdentity = useRef(repository?.identity ?? '')

  useEffect(() => {
    const nextIdentity = repository?.identity ?? ''
    if (nextIdentity === previousRepositoryIdentity.current) return
    previousRepositoryIdentity.current = nextIdentity
    generation.current++
    setPreview(null)
    setConfirming(false)
    setReceipt(null)
    setReceiptTask('')
    setOutcome(null)
    setOutcomeMessage('')
    setPreviewError('')
    submitLockRef.current = false
    setSubmitting(false)
  }, [repository?.identity])

  const validation = validateTaskText(task)
  const repositoryReady = Boolean(repository?.identity && repository.path)
  const canPrepare = repositoryReady && daemonReady
  const safe = Boolean(preview && !preview.safety.allowUnsafeAgents && !preview.safety.disableCodexSandbox)

  const onTaskChange = (next: string) => {
    generation.current++
    setTask(next)
    setPreview(null)
    setPreviewError('')
    setOutcome(null)
    setOutcomeMessage('')
    setConfirming(false)
    // A newly edited task is a new operation. Do not inherit the prior lock.
    submitLockRef.current = false
    setSubmitting(false)
  }

  const prepare = async () => {
    if (!repository || !canPrepare || validation) {
      setPreviewError(validation ? t(`tasks.validation.${validation}` as never) : !daemonReady ? t('tasks.daemonUnavailable') : t('tasks.repositoryUnavailable'))
      return
    }
    const expectedGeneration = generation.current
    const expectedTask = task
    const expectedIdentity = repository.identity
    setPreviewLoading(true)
    setPreviewError('')
    try {
      const query = new URLSearchParams({ repositoryIdentity: expectedIdentity, task: expectedTask })
      const value = normalizeTaskPreview(await request<unknown>(`/api/tasks/preview?${query.toString()}`), expectedTask)
      if (!value) throw new APIError(t('tasks.previewInvalid'))
      if (expectedGeneration !== generation.current || expectedIdentity !== repository.identity || expectedTask !== task) return
      setPreview(value)
      setConfirming(true)
    } catch (error) {
      if (expectedGeneration === generation.current) setPreviewError((error as Error).message)
    } finally {
      if (expectedGeneration === generation.current) setPreviewLoading(false)
    }
  }

  const confirm = async (event?: { preventDefault: () => void }) => {
    event?.preventDefault()
    if (!preview || !repository || !safe || submitLockRef.current || submitting) return
    submitLockRef.current = true
    setSubmitting(true)
    const expectedGeneration = generation.current
    const expectedIdentity = repository.identity
    try {
      const value = await request<unknown>('/api/tasks', { method: 'POST', body: JSON.stringify({ repositoryIdentity: expectedIdentity, task, previewToken: preview.previewToken }) })
      if (expectedGeneration !== generation.current || expectedIdentity !== repository.identity) return
      const parsed = normalizeTaskReceipt((recordValue(value)?.receipt ?? value))
      // A daemon may have accepted the task before a malformed/truncated
      // receipt reached the Dashboard. Treat receipt validation failures as
      // ambiguous enqueue results and never invite a blind retry.
      if (!parsed) throw new APIError(t('tasks.receiptInvalid'), undefined, 'ambiguous_enqueue')
      setConfirming(false)
      setPreview(null)
      if (parsed.kind === 'skipped') {
        setOutcome('skipped')
        setOutcomeMessage(parsed.reason)
        submitLockRef.current = false
        setSubmitting(false)
      } else {
        setReceipt(parsed)
        setReceiptTask(task)
        setOutcome(null)
        void onQueued?.(parsed)
      }
    } catch (error) {
      if (expectedGeneration !== generation.current || expectedIdentity !== repository.identity) return
      const apiError = error as APIError
      const ambiguous = apiError.code === 'ambiguous_enqueue' || apiError.code === 'ambiguous' || apiError.phase === 'ambiguous_enqueue' || apiError.phase === 'ambiguous' || /ambiguous/i.test(apiError.message)
      setConfirming(false)
      setPreview(null)
      if (ambiguous) {
        setOutcome('ambiguous')
        setOutcomeMessage(t('tasks.ambiguousBody'))
        // An enqueue may already have reached the daemon; never blindly retry.
      } else {
        setPreviewError(apiError.message)
        submitLockRef.current = false
        setSubmitting(false)
      }
    }
  }

  const taskJobs = jobs.filter(job => job.job_type === 'task' && job.git_ref === 'run' && (!job.repo_path || !repository || job.repo_path === repository.path))
  // Reconcile only when both immutable receipt identifiers agree. An
  // ID-only or UUID-only match can be stale or foreign.
  const receiptJob = receipt && taskJobs.find(job => taskJobId(job) === receipt.jobId && taskJobUUID(job) === receipt.jobUUID)
  const displayedJobs = receipt && !receiptJob ? [{ id: receipt.jobId, job_id: receipt.jobId, uuid: receipt.jobUUID, git_ref: 'run', job_type: 'task', status: 'queued', subject: receiptTask } as ReviewJob, ...taskJobs] : taskJobs

  return <section className="tasks-page">
    <header className="section-heading"><div><span className="eyebrow">{t('tasks.eyebrow')}</span><h1>{t('tasks.title')}</h1><p>{t('tasks.description')}</p></div></header>
    <div className="tasks-layout">
      <section className="task-composer" aria-labelledby="tasks-composer-title">
        <div className="task-card-heading"><div><h2 id="tasks-composer-title">{t('tasks.customTask')}</h2><p>{t('tasks.repositoryScoped')}</p></div><span className="task-badge"><ShieldCheck size={15} />{t('tasks.readOnly')}</span></div>
        <label htmlFor="task-input">{t('tasks.task')}</label>
        <textarea id="task-input" value={task} onChange={event => onTaskChange(event.target.value)} placeholder={t('tasks.placeholder')} rows={8} spellCheck="false" />
        <div className="task-input-meta"><span>{task ? `${new TextEncoder().encode(task).byteLength.toLocaleString()} / ${TASK_MAX_BYTES.toLocaleString()} ${t('common.bytes')}` : t('tasks.exactPreserved')}</span>{validation && <span className="task-validation" role="alert">{t(`tasks.validation.${validation}` as never)}</span>}</div>
        <p className="task-context-hint">{t('tasks.contextHint')}</p>
        <p className="task-safety-copy">{t('tasks.agenticWarning')} {t('tasks.safetyFailClosed')}</p>
        <p className="task-quota">{t('tasks.quotaWarning')}</p>
        <button className="button primary" disabled={Boolean(validation) || !canPrepare || previewLoading || submitLockRef.current || submitting} onClick={() => void prepare()}><Play size={16} />{previewLoading ? t('common.loading') : t('tasks.prepare')}</button>
        {previewError && <p className="task-error" role="alert"><XCircle size={15} />{previewError}</p>}
        {outcome === 'skipped' && <p className="task-warning" role="status"><AlertTriangle size={15} />{t('tasks.skipped')}: {outcomeMessage}</p>}
        {outcome === 'ambiguous' && <p className="task-warning" role="alert"><AlertTriangle size={15} />{t('tasks.ambiguous')}: {outcomeMessage}</p>}
      </section>
      <section className="task-history" aria-labelledby="tasks-history-title"><div className="task-card-heading"><div><h2 id="tasks-history-title">{t('tasks.history')}</h2><p>{t('tasks.historyDescription')}</p></div><TerminalSquare size={19} /></div>{displayedJobs.length === 0 ? <div className="task-empty"><TerminalSquare size={23} /><p>{t('tasks.noHistory')}</p></div> : <div className="task-list">{displayedJobs.map((job, index) => { const id = taskJobId(job); const status = taskStatus(job); const hasReceiptPair = Boolean(receipt && id === receipt.jobId && taskJobUUID(job) === receipt.jobUUID); const isSyntheticReceipt = Boolean(receipt && !receiptJob && index === 0); const isReceipt = hasReceiptPair || isSyntheticReceipt; const subject = isReceipt ? receiptTask : 'run'; return <article className={`task-row ${isReceipt ? 'receipt-row' : ''}`} key={`${id ?? 'task'}-${taskJobUUID(job) ?? index}`}><div className="task-row-main"><strong>#{id ?? '—'}</strong><span className="task-row-subject">{subject}</span><span className={`status status-${status}`}><span className="status-dot" />{taskStatusLabel(status, t)}</span></div><div className="task-row-meta"><span>run</span><span>{typeof job.agent === 'string' && job.agent ? job.agent : 'codex'}</span>{id && <button className="button compact" onClick={() => onOpenReview(id)}>{t('tasks.viewResult')}</button>}</div></article> })}</div>}</section>
    </div>
    {confirming && preview && <div className="modal-backdrop" role="presentation"><section className="task-modal" role="dialog" aria-modal="true" aria-labelledby="task-confirm-title"><header><div><span className="eyebrow">{t('tasks.confirmEyebrow')}</span><h2 id="task-confirm-title">{t('tasks.confirmTitle')}</h2></div><button className="icon-button" aria-label={t('common.close')} onClick={() => setConfirming(false)}><X size={17} /></button></header><div className="task-preview-grid"><span>{t('tasks.repository')}</span><code>{preview.repositoryPath}</code><span>HEAD</span><code>{preview.head}</code><span>{t('tasks.branch')}</span><code>{preview.branch}</code><span>{t('tasks.agent')}</span><code>codex</code><span>{t('tasks.mode')}</span><code>read-only</code><span>{t('tasks.safety')}</span><code>allow_unsafe_agents = {String(preview.safety.allowUnsafeAgents)}; disable_codex_sandbox = {String(preview.safety.disableCodexSandbox)}</code></div><div className="task-command-preview"><h3>{t('tasks.executable')}</h3><code>{preview.command.executable}</code><h3>{t('tasks.args')}</h3><pre>{preview.command.args.map((arg: string, index: number) => `argv[${index}] ${arg}`).join('\n')}</pre><h3>{t('tasks.stdin')}</h3><pre data-testid="task-preview-stdin">{preview.command.stdin}</pre></div>{!safe && <p className="task-warning" role="alert"><AlertTriangle size={15} />{preview.safety.allowUnsafeAgents ? t('tasks.unsafeAgents') : t('tasks.sandboxDisabled')}</p>}<footer><button className="button" disabled={submitting} onClick={() => setConfirming(false)}>{t('common.cancel')}</button><button className="button primary" disabled={!safe || submitting} onClick={event => void confirm(event)}>{submitting ? t('common.loading') : t('tasks.run')}</button></footer></section></div>}
  </section>
}
