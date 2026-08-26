import { AlertTriangle, CheckCircle2, CircleAlert, RefreshCw, ShieldAlert, Trash2, Wrench, X } from 'lucide-react'
import { normalizeGitHookManagement, normalizeGitHookManagementPreview, normalizeGitHookManagementResult, normalizeGitHookStatus, request, type GitHookAction, type GitHookAvailability, type GitHookManagement, type GitHookManagementHook, type GitHookManagementPreview, type GitHookManagementResult, type GitHookOverallStatus, type GitHookPostCommitStatus, type GitHookPostRewriteStatus, type GitHookStatus } from './api'
import { useI18n } from './i18n'
import { useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from 'react'

type GitHookStatusProps = { repositoryIdentity?: string }
type GitHookPhase = 'unavailable' | 'loading' | 'loaded' | 'error'
type HookManagementPhase = 'unavailable' | 'loading' | 'loaded' | 'error'
type MutationPhase = 'idle' | 'previewing' | 'ready' | 'executing'

// StrictMode re-runs effects on the first surface entry. Keep only unresolved
// requests here so that the second effect subscribes to the same read without
// turning a single entry into two GETs. Refresh deliberately bypasses it.
const pendingStatusRequests = new Map<string, Promise<unknown>>()
const pendingManagementRequests = new Map<string, Promise<unknown>>()

function requestRead(map: Map<string, Promise<unknown>>, path: string, identity: string, force = false) {
  const existing = map.get(identity)
  if (existing && !force) return existing
  const controller = new AbortController()
  const promise = request<unknown>(path, { signal: controller.signal })
  map.set(identity, promise)
  const clear = () => { if (map.get(identity) === promise) map.delete(identity) }
  void promise.then(clear, clear)
  return promise
}

function overallLabel(status: GitHookOverallStatus, t: ReturnType<typeof useI18n>['t']) {
  if (status === 'ready') return t('setup.gitHooksReady')
  if (status === 'needs-attention') return t('setup.gitHooksNeedsAttention')
  if (status === 'not-installed') return t('setup.gitHooksNotInstalled')
  return t('setup.gitHooksUnavailable')
}

function postCommitLabel(status: GitHookPostCommitStatus, t: ReturnType<typeof useI18n>['t']) {
  if (status === 'current') return t('setup.gitHooksCurrent')
  if (status === 'outdated') return t('setup.gitHooksOutdated')
  if (status === 'not-installed') return t('setup.gitHooksNotInstalled')
  return t('setup.gitHooksUnknown')
}

function postRewriteLabel(status: GitHookPostRewriteStatus, t: ReturnType<typeof useI18n>['t']) {
  if (status === 'current') return t('setup.gitHooksCurrent')
  if (status === 'outdated') return t('setup.gitHooksOutdated')
  if (status === 'missing') return t('setup.gitHooksMissing')
  if (status === 'not-installed') return t('setup.gitHooksNotInstalled')
  return t('setup.gitHooksUnknown')
}

function statusClass(status: GitHookOverallStatus | GitHookPostCommitStatus | GitHookPostRewriteStatus) {
  if (status === 'current' || status === 'ready') return 'status-pass'
  if (status === 'outdated' || status === 'missing' || status === 'needs-attention') return 'status-queued'
  if (status === 'not-installed') return 'status-failed'
  return 'status-canceled'
}

function StatusValue({ status, label }: { status: GitHookOverallStatus | GitHookPostCommitStatus | GitHookPostRewriteStatus; label: string }) {
  return <span className={`status git-hook-status-value ${statusClass(status)}`}><span className="status-dot" />{label}</span>
}

function managementHookLabel(hook: GitHookManagementHook, t: ReturnType<typeof useI18n>['t']) {
  if (hook.classification === 'managed-stale-binary') return t('setup.gitHooksManagementStaleBinary')
  if (hook.classification === 'managed-outdated') return t('setup.gitHooksManagementOutdated')
  if (hook.classification === 'managed-current') return t('setup.gitHooksManagementCurrent')
  if (hook.classification === 'unmanaged') return t('setup.gitHooksManagementUnmanaged')
  if (hook.classification === 'ambiguous') return t('setup.gitHooksManagementAmbiguous')
  if (hook.classification === 'unreadable') return t('setup.gitHooksManagementUnreadable')
  return t('setup.gitHooksManagementAbsent')
}

function managementHookClass(hook: GitHookManagementHook) {
  if (hook.classification === 'managed-current') return 'status-pass'
  if (hook.classification === 'absent' || hook.classification === 'unreadable' || hook.classification === 'ambiguous') return 'status-failed'
  return 'status-queued'
}

function actionLabel(action: GitHookAction, t: ReturnType<typeof useI18n>['t']) {
  if (action === 'install') return t('setup.gitHooksInstall')
  if (action === 'repair') return t('setup.gitHooksRepair')
  return t('setup.gitHooksRemove')
}

function actionAvailability(management: GitHookManagement, action: GitHookAction): GitHookAvailability {
  return management[action]
}

function availabilityReason(management: GitHookManagement, action: GitHookAction, t: ReturnType<typeof useI18n>['t']) {
  const reason = actionAvailability(management, action).reason
  if (reason === 'already-current') return t('setup.gitHooksReasonAlreadyCurrent')
  if (reason === 'repair-recommended' || reason === 'managed-hook-needs-repair') return t('setup.gitHooksReasonRepairRecommended')
  if (reason === 'no-managed-hook-needs-repair') return t('setup.gitHooksReasonNoRepair')
  if (reason === 'no-managed-hook') return t('setup.gitHooksReasonNoManaged')
  if (reason === 'relative-hooks-path-normalization') return t('setup.gitHooksReasonRelativeHooksPath')
  if (reason === 'unsafe-hooks-directory') return t('setup.gitHooksReasonUnsafeDirectory')
  if (reason === 'non-shell-hook') return t('setup.gitHooksReasonNonShell')
  if (reason === 'ambiguous-roborev-content') return t('setup.gitHooksReasonAmbiguous')
  if (reason === 'unreadable-hook') return t('setup.gitHooksReasonUnreadable')
  const safety = management.safety
  if (!safety.hooksDirectorySafe) return t('setup.gitHooksReasonUnsafeDirectory')
  if (safety.configurationMutationRequired) return t('setup.gitHooksReasonRelativeHooksPath')
  if (safety.nonShellHookPresent) return t('setup.gitHooksReasonNonShell')
  if (safety.ambiguousRoborevContent) return t('setup.gitHooksReasonAmbiguous')
  if (safety.unreadableHookPresent) return t('setup.gitHooksReasonUnreadable')
  return t('setup.gitHooksReasonUnavailable')
}

function managementSummary(summaryCode: string, t: ReturnType<typeof useI18n>['t']) {
  if (summaryCode === 'managed-current') return t('setup.gitHooksManagementCurrentSummary')
  if (summaryCode === 'not-installed') return t('setup.gitHooksManagementNotInstalledSummary')
  if (summaryCode === 'repair-recommended') return t('setup.gitHooksManagementRepairSummary')
  if (summaryCode === 'blocked') return t('setup.gitHooksManagementBlockedSummary')
  return t('setup.gitHooksManagementAttentionSummary')
}

function warningLabel(code: string, t: ReturnType<typeof useI18n>['t']) {
  if (code === 'relative-hooks-path-normalization') return t('setup.gitHooksWarningRelativeHooksPath')
  if (code === 'non-shell-hook') return t('setup.gitHooksWarningNonShell')
  if (code === 'ambiguous-roborev-content') return t('setup.gitHooksWarningAmbiguous')
  if (code === 'unsafe-hooks-directory') return t('setup.gitHooksWarningUnsafeDirectory')
  return t('setup.gitHooksWarningUnknown')
}

function mutationOutcomeLabel(status: GitHookManagementResult['status'], t: ReturnType<typeof useI18n>['t']) {
  if (status === 'verified-success') return t('setup.gitHooksVerifiedSuccess')
  if (status === 'partial') return t('setup.gitHooksPartial')
  return t('setup.gitHooksFailedUnverified')
}

function mutationErrorLabel(code: string | undefined, t: ReturnType<typeof useI18n>['t']) {
  if (code === 'cli-failed') return t('setup.gitHooksCliFailed')
  if (code === 'cli-not-started') return t('setup.gitHooksCliNotStarted')
  if (code === 'readback-unavailable') return t('setup.gitHooksReadbackUnavailable')
  return t('setup.gitHooksResultNoDetails')
}

type HookManagementDialogProps = {
  action: GitHookAction
  preview: GitHookManagementPreview
  busy: boolean
  onCancel: () => void
  onConfirm: () => void
}

const dialogFocusableSelector = 'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

function HookManagementDialog({ action, preview, busy, onCancel, onConfirm }: HookManagementDialogProps) {
  const { t } = useI18n()
  const dialogRef = useRef<HTMLElement | null>(null)
  const cancelRef = useRef<HTMLButtonElement | null>(null)
  useEffect(() => { cancelRef.current?.focus() }, [])
  const onDialogKeyDown = (event: ReactKeyboardEvent<HTMLElement>) => {
    if (event.key === 'Escape') {
      if (!busy) {
        event.preventDefault()
        onCancel()
      }
      return
    }
    if (event.key !== 'Tab') return
    const dialog = dialogRef.current
    if (!dialog) return
    const focusable = Array.from(dialog.querySelectorAll<HTMLElement>(dialogFocusableSelector)).filter(element => !element.hidden)
    if (focusable.length === 0) {
      event.preventDefault()
      dialog.focus()
      return
    }
    const active = document.activeElement
    const index = active instanceof HTMLElement ? focusable.indexOf(active) : -1
    if (event.shiftKey ? index <= 0 : index === focusable.length - 1 || index < 0) {
      event.preventDefault()
      focusable[event.shiftKey ? focusable.length - 1 : 0].focus()
    }
  }
  const remove = action === 'remove'
  const title = action === 'install' ? t('setup.gitHooksInstallConfirmTitle') : action === 'repair' ? t('setup.gitHooksRepairConfirmTitle') : t('setup.gitHooksRemoveConfirmTitle')
  const description = action === 'install' ? t('setup.gitHooksInstallConfirmDescription') : action === 'repair' ? t('setup.gitHooksRepairConfirmDescription') : t('setup.gitHooksRemoveConfirmDescription')
  return <div className="prompt-overlay git-hook-management-overlay" role="presentation">
    <section className="prompt-dialog git-hook-management-dialog" role="dialog" aria-modal="true" aria-labelledby="git-hook-management-title" aria-describedby="git-hook-management-description" tabIndex={-1} ref={dialogRef} onKeyDown={onDialogKeyDown}>
      <header><div><span className="eyebrow">{t('setup.gitHooksExplicitConfirmation')}</span><h2 id="git-hook-management-title">{title}</h2><p id="git-hook-management-description">{description}</p></div><button className="icon-button" type="button" aria-label={t('setup.gitHooksCloseConfirmation')} onClick={onCancel} disabled={busy}><X /></button></header>
      <dl className="git-hook-management-facts"><div><dt>{t('setup.gitHooksRepository')}</dt><dd>{preview.repository}</dd></div><div><dt>{t('setup.gitHooksPostCommit')}</dt><dd><span className={`status ${managementHookClass(preview.preflight.postCommit)}`}><span className="status-dot" />{managementHookLabel(preview.preflight.postCommit, t)}</span></dd></div><div><dt>{t('setup.gitHooksPostRewrite')}</dt><dd><span className={`status ${managementHookClass(preview.preflight.postRewrite)}`}><span className="status-dot" />{managementHookLabel(preview.preflight.postRewrite, t)}</span></dd></div>{action === 'install' && <div><dt>{t('setup.gitHooksPreservation')}</dt><dd>{t('setup.gitHooksPreservesShellHooks')}</dd></div>}</dl>
      <div className="git-hook-command-preview"><strong>{t('setup.gitHooksCommandPreview')}</strong><code>{JSON.stringify([preview.command.executable, ...preview.command.args])}</code><small>{t('setup.gitHooksBackendCommand')}</small></div>
      {preview.warningCodes.map(code => <p className="git-hook-management-warning" key={code}><AlertTriangle size={15} aria-hidden="true" /><span>{warningLabel(code, t)}</span></p>)}
      <footer><button className="button" type="button" ref={cancelRef} onClick={onCancel} disabled={busy}>{t('common.cancel')}</button><button className={`button ${remove ? 'danger' : 'primary'}`} type="button" onClick={onConfirm} disabled={busy}>{remove ? <Trash2 size={15} /> : action === 'repair' ? <Wrench size={15} /> : <CheckCircle2 size={15} />}{busy ? t('setup.gitHooksExecuting') : actionLabel(action, t)}</button></footer>
    </section>
  </div>
}

export function GitHookStatus({ repositoryIdentity = '' }: GitHookStatusProps) {
  const { t } = useI18n()
  const [phase, setPhase] = useState<GitHookPhase>('unavailable')
  const [data, setData] = useState<GitHookStatus | null>(null)
  const [managementPhase, setManagementPhase] = useState<HookManagementPhase>('unavailable')
  const [management, setManagement] = useState<GitHookManagement | null>(null)
  const [mutationPhase, setMutationPhase] = useState<MutationPhase>('idle')
  const [preview, setPreview] = useState<GitHookManagementPreview | null>(null)
  const [mutationError, setMutationError] = useState('')
  const [mutationResult, setMutationResult] = useState<GitHookManagementResult | null>(null)
  const generationRef = useRef(0)
  const mountedRef = useRef(true)
  const previewControllerRef = useRef<AbortController | null>(null)
  const confirmingRef = useRef(false)
  const invokingCtaRef = useRef<HTMLElement | null>(null)

  useEffect(() => {
    if (preview !== null || mutationPhase !== 'idle') return
    const invokingCta = invokingCtaRef.current
    if (invokingCta && document.contains(invokingCta)) invokingCta.focus()
    invokingCtaRef.current = null
  }, [preview, mutationPhase])

  const load = (force = false) => {
    const identity = repositoryIdentity.trim()
    if (!identity || !mountedRef.current) return
    const generation = ++generationRef.current
    const statusPromise = requestRead(pendingStatusRequests, `/api/repositories/hooks?repositoryIdentity=${encodeURIComponent(identity)}`, identity, force)
    setPhase('loading')
    setManagementPhase('loading')
    void statusPromise.then(value => {
      const normalized = normalizeGitHookStatus(value, identity)
      if (!normalized) throw new Error('invalid-git-hook-status')
      if (!mountedRef.current || generation !== generationRef.current || repositoryIdentity.trim() !== identity) return
      setData(normalized)
      setPhase('loaded')
    }).catch(() => {
      if (!mountedRef.current || generation !== generationRef.current || repositoryIdentity.trim() !== identity) return
      setData(null)
      setPhase('error')
    })
    const managementPromise = requestRead(pendingManagementRequests, `/api/repositories/hooks/management?repositoryIdentity=${encodeURIComponent(identity)}`, identity, force)
    void managementPromise.then(value => {
      const normalized = normalizeGitHookManagement(value, identity)
      if (!normalized) throw new Error('invalid-git-hook-management')
      if (!mountedRef.current || generation !== generationRef.current || repositoryIdentity.trim() !== identity) return
      setManagement(normalized)
      setManagementPhase('loaded')
    }).catch(() => {
      if (!mountedRef.current || generation !== generationRef.current || repositoryIdentity.trim() !== identity) return
      setManagement(null)
      setManagementPhase('error')
    })
  }

  useEffect(() => {
    mountedRef.current = true
    const identity = repositoryIdentity.trim()
    setPreview(null)
    setMutationResult(null)
    setMutationError('')
    setMutationPhase('idle')
    confirmingRef.current = false
    invokingCtaRef.current = null
    if (!identity) {
      generationRef.current += 1
      setData(null)
      setManagement(null)
      setPhase('unavailable')
      setManagementPhase('unavailable')
    } else {
      load()
    }
    return () => {
      mountedRef.current = false
      generationRef.current += 1
      previewControllerRef.current?.abort()
      previewControllerRef.current = null
      confirmingRef.current = false
    }
  }, [repositoryIdentity])

  const closePreview = () => {
    if (mutationPhase === 'executing') return
    previewControllerRef.current?.abort()
    previewControllerRef.current = null
    setPreview(null)
    setMutationError('')
    setMutationPhase('idle')
  }

  const prepare = async (action: GitHookAction) => {
    const identity = repositoryIdentity.trim()
    if (!identity || !management || managementPhase !== 'loaded' || !actionAvailability(management, action).available || mutationPhase !== 'idle' || confirmingRef.current) return
    invokingCtaRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
    const generation = generationRef.current
    const controller = new AbortController()
    previewControllerRef.current?.abort()
    previewControllerRef.current = controller
    setMutationError('')
    setMutationResult(null)
    setMutationPhase('previewing')
    try {
      const payload = await request<unknown>('/api/repositories/hooks/preview', { method: 'POST', body: JSON.stringify({ repositoryIdentity: identity, action }), signal: controller.signal })
      const normalized = normalizeGitHookManagementPreview(payload, identity, action)
      if (!normalized) throw new Error('invalid-git-hook-management-preview')
      if (!mountedRef.current || generation !== generationRef.current || repositoryIdentity.trim() !== identity) return
      setPreview(normalized)
      setMutationPhase('ready')
    } catch (error) {
      if (controller.signal.aborted || !mountedRef.current || generation !== generationRef.current) return
      setMutationError(error instanceof Error && error.message === 'invalid-git-hook-management-preview' ? t('setup.gitHooksMalformed') : t('setup.gitHooksPreviewFailed'))
      setMutationPhase('idle')
    } finally {
      if (previewControllerRef.current === controller) previewControllerRef.current = null
    }
  }

  const confirm = async () => {
    if (!preview || mutationPhase !== 'ready' || confirmingRef.current || !mountedRef.current) return
    confirmingRef.current = true
    const identity = repositoryIdentity.trim()
    const generation = generationRef.current
    setMutationError('')
    setMutationPhase('executing')
    try {
      const payload = await request<unknown>('/api/repositories/hooks/execute', { method: 'POST', body: JSON.stringify({ token: preview.token }) })
      const normalized = normalizeGitHookManagementResult(payload, identity, preview.action)
      if (!normalized) throw new Error('invalid-git-hook-management-result')
      if (!mountedRef.current || generation !== generationRef.current || repositoryIdentity.trim() !== identity) return
      setMutationResult(normalized)
      setPreview(null)
      setMutationPhase('idle')
      // A completed mutation gets exactly one authoritative status and one
      // management read; there is no timer or optimistic state here.
      load(true)
    } catch (error) {
      if (!mountedRef.current || generation !== generationRef.current) return
      setPreview(null)
      setMutationError(error instanceof Error && error.message === 'invalid-git-hook-management-result' ? t('setup.gitHooksMalformed') : t('setup.gitHooksExecuteFailed'))
      setMutationPhase('idle')
    } finally {
      confirmingRef.current = false
    }
  }

  const titleId = 'git-hook-status-title'
  const loading = phase === 'loading' || managementPhase === 'loading'
  const unavailable = phase === 'unavailable' || phase === 'error' || !repositoryIdentity.trim()
  const actions: GitHookAction[] = ['install', 'repair', 'remove']
  return <section className="git-hook-status" aria-labelledby={titleId} aria-busy={loading}>
    <header className="git-hook-status-heading"><div><span className="eyebrow">{t('setup.gitHooksEyebrow')}</span><h2 id={titleId}>{t('setup.gitHooksTitle')}</h2><p>{t('setup.gitHooksDescription')}</p></div><button className="button ghost compact" disabled={!repositoryIdentity.trim() || mutationPhase === 'previewing' || mutationPhase === 'executing'} onClick={() => load(true)} aria-label={t('setup.gitHooksRefresh')}><RefreshCw size={14} className={loading ? 'spin' : ''} />{t('common.refresh')}</button></header>
    {loading && !data && <div className="git-hook-status-state" aria-live="polite"><RefreshCw size={17} className="spin" aria-hidden="true" /><span>{t('setup.gitHooksLoading')}</span></div>}
    {unavailable && <div className="git-hook-status-state git-hook-status-unavailable" role={phase === 'error' ? 'alert' : 'status'}><CircleAlert size={17} aria-hidden="true" /><div><strong>{t('setup.gitHooksUnavailable')}</strong><p>{t('setup.gitHooksUnavailableBody')}</p></div></div>}
    {phase === 'loaded' && data && <><div className="git-hook-status-summary"><StatusValue status={data.status} label={overallLabel(data.status, t)} /></div><dl className="git-hook-status-list"><div className="git-hook-status-row"><dt>{t('setup.gitHooksPostCommit')}</dt><dd><StatusValue status={data.postCommit.status} label={postCommitLabel(data.postCommit.status, t)} /></dd></div><div className="git-hook-status-row"><dt>{t('setup.gitHooksPostRewrite')}</dt><dd><StatusValue status={data.postRewrite.status} label={postRewriteLabel(data.postRewrite.status, t)} /></dd></div></dl></>}
    {managementPhase === 'loading' && !management && <div className="git-hook-management-state" role="status"><RefreshCw size={15} className="spin" />{t('setup.gitHooksManagementLoading')}</div>}
    {managementPhase === 'error' && <div className="git-hook-management-state git-hook-management-error" role="alert">{t('setup.gitHooksManagementUnavailable')}</div>}
    {managementPhase === 'loaded' && management && <div className="git-hook-management"><div className="git-hook-management-summary"><div><strong>{t('setup.gitHooksManagementTitle')}</strong><p>{managementSummary(management.summaryCode, t)}</p></div><span className={`status ${management.summaryCode === 'managed-current' ? 'status-pass' : management.summaryCode === 'blocked' ? 'status-failed' : 'status-queued'}`}><span className="status-dot" />{managementSummary(management.summaryCode, t)}</span></div><dl className="git-hook-management-list"><div><dt>{t('setup.gitHooksPostCommit')}</dt><dd><span className={`status ${managementHookClass(management.postCommit)}`}><span className="status-dot" />{managementHookLabel(management.postCommit, t)}</span></dd></div><div><dt>{t('setup.gitHooksPostRewrite')}</dt><dd><span className={`status ${managementHookClass(management.postRewrite)}`}><span className="status-dot" />{managementHookLabel(management.postRewrite, t)}</span></dd></div></dl><div className="git-hook-management-actions">{actions.map(action => { const availability = actionAvailability(management, action); const disabled = mutationPhase !== 'idle' || !availability.available; return <div className="git-hook-management-action" key={action}><button className={`button ${action === 'remove' ? 'danger' : action === 'install' ? 'primary' : ''}`} type="button" disabled={disabled} onClick={() => void prepare(action)}>{action === 'remove' ? <Trash2 size={14} /> : action === 'repair' ? <Wrench size={14} /> : <CheckCircle2 size={14} />}{actionLabel(action, t)}</button>{!availability.available && <small>{availabilityReason(management, action, t)}</small>}</div>})}</div>{management.warningCodes.map(code => <p className="git-hook-management-warning" key={code}><AlertTriangle size={15} aria-hidden="true" /><span>{warningLabel(code, t)}</span></p>)}</div>}
    {mutationError && <p className="git-hook-management-error git-hook-management-result-error" role="alert">{mutationError}</p>}
    {mutationResult && <div className={`git-hook-management-result ${mutationResult.status}`} role={mutationResult.status === 'failed-unverified' ? 'alert' : 'status'}><strong>{mutationOutcomeLabel(mutationResult.status, t)}</strong>{mutationResult.errorCode && <span>{mutationErrorLabel(mutationResult.errorCode, t)}</span>}</div>}
    {preview && (mutationPhase === 'ready' || mutationPhase === 'executing') && <HookManagementDialog action={preview.action} preview={preview} busy={mutationPhase === 'executing'} onCancel={closePreview} onConfirm={() => void confirm()} />}
  </section>
}

export default GitHookStatus
