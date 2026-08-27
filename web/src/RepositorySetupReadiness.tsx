import { AlertTriangle, Check, RefreshCw, ShieldAlert, Settings2, X } from 'lucide-react'
import { useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from 'react'
import { executeSnapshotIgnore, fetchRepositorySetupReadiness, fetchSnapshotIgnorePreview, type RepositorySetupReadiness, type SnapshotIgnoreExecuteResult, type SnapshotIgnorePreview } from './api'
import { useI18n } from './i18n'

type Props = { repositoryIdentity?: string }

function statusClass(status: string) {
  if (['ready', 'online', 'tracked'].includes(status)) return 'status-pass'
  if (['unknown', 'unavailable', 'unsafe', 'not-ignored', 'tracked-files', 'offline', 'not-tracked', 'needs-attention', 'not-installed'].includes(status)) return status === 'offline' || status === 'not-tracked' || status === 'not-installed' || status === 'needs-attention' || status === 'not-ignored' || status === 'tracked-files' ? 'status-failed' : 'status-queued'
  return 'status-queued'
}

function statusLabel(status: string, t: ReturnType<typeof useI18n>['t']) {
  const labels: Record<string, string> = {
    ready: t('setup.repositoryReadinessReady'), online: t('setup.repositoryReadinessReady'), tracked: t('setup.repositoryReadinessReady'),
    'needs-attention': t('setup.repositoryReadinessNeedsAttention'), 'not-ignored': t('setup.repositoryReadinessNeedsAttention'), 'tracked-files': t('setup.repositoryReadinessNeedsAttention'), 'not-installed': t('setup.repositoryReadinessNeedsAttention'), offline: t('setup.repositoryReadinessNeedsAttention'), 'not-tracked': t('setup.repositoryReadinessNeedsAttention'),
    unavailable: t('setup.repositoryReadinessUnavailable'), unsafe: t('setup.repositoryReadinessUnavailable'), unknown: t('setup.repositoryReadinessUnavailable'),
  }
  return labels[status] || t('setup.repositoryReadinessUnavailable')
}

function bodyLabel(status: RepositorySetupReadiness['overall'], t: ReturnType<typeof useI18n>['t']) {
  if (status === 'ready') return t('setup.repositoryReadinessReadyBody')
  if (status === 'needs-attention') return t('setup.repositoryReadinessNeedsAttentionBody')
  return t('setup.repositoryReadinessUnavailableBody')
}

function statusDescription(key: string, status: string, t: ReturnType<typeof useI18n>['t']) {
  if (key === 'configuration') return t(status === 'ready' ? 'setup.repositoryReadinessConfigurationReady' : 'setup.repositoryReadinessConfigurationUnavailable')
  if (key === 'snapshot') {
    if (status === 'ready') return t('setup.repositoryReadinessSnapshotReady')
    if (status === 'not-ignored') return t('setup.repositoryReadinessSnapshotNotIgnored')
    if (status === 'tracked-files') return t('setup.repositoryReadinessSnapshotTracked')
    if (status === 'unsafe') return t('setup.repositoryReadinessSnapshotUnsafe')
    return t('setup.repositoryReadinessSnapshotUnavailable')
  }
  if (key === 'hooks') return t(status === 'ready' ? 'setup.repositoryReadinessHooksReady' : status === 'not-installed' ? 'setup.repositoryReadinessHooksNotInstalled' : status === 'needs-attention' ? 'setup.repositoryReadinessHooksNeedsAttention' : 'setup.repositoryReadinessHooksUnknown')
  if (key === 'daemon') return t(status === 'online' ? 'setup.repositoryReadinessDaemonOnline' : status === 'offline' ? 'setup.repositoryReadinessDaemonOffline' : 'setup.repositoryReadinessDaemonUnavailable')
  return t(status === 'tracked' ? 'setup.repositoryReadinessTrackingTracked' : status === 'not-tracked' ? 'setup.repositoryReadinessTrackingNotTracked' : 'setup.repositoryReadinessTrackingUnavailable')
}

export function RepositorySetupReadiness({ repositoryIdentity = '' }: Props) {
  const { t } = useI18n()
  const [result, setResult] = useState<RepositorySetupReadiness | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(false)
  const [attempted, setAttempted] = useState(false)
  const controllerRef = useRef<AbortController | null>(null)
  const previewControllerRef = useRef<AbortController | null>(null)
  const requestLock = useRef(false)
  const previewLock = useRef(false)
  const confirmingRef = useRef(false)
  const identityRef = useRef(repositoryIdentity)
  const mountedRef = useRef(true)
  const generationRef = useRef(0)
  const [previewPhase, setPreviewPhase] = useState<'idle' | 'loading' | 'success' | 'error'>('idle')
  const [preview, setPreview] = useState<SnapshotIgnorePreview | null>(null)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [executePhase, setExecutePhase] = useState<'idle' | 'executing'>('idle')
  const [executeResult, setExecuteResult] = useState<SnapshotIgnoreExecuteResult | null>(null)
  const [tokenConsumed, setTokenConsumed] = useState(false)

  useEffect(() => {
    ++generationRef.current
    controllerRef.current?.abort()
    previewControllerRef.current?.abort()
    controllerRef.current = null
    previewControllerRef.current = null
    requestLock.current = false
    previewLock.current = false
    confirmingRef.current = false
    setResult(null)
    setError(false)
    setAttempted(false)
    setLoading(false)
    setPreviewPhase('idle')
    setPreview(null)
    setDialogOpen(false)
    setExecutePhase('idle')
    setExecuteResult(null)
    setTokenConsumed(false)
    identityRef.current = repositoryIdentity
  }, [repositoryIdentity])

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      ++generationRef.current
      controllerRef.current?.abort()
      previewControllerRef.current?.abort()
      controllerRef.current = null
      previewControllerRef.current = null
      requestLock.current = false
      previewLock.current = false
      confirmingRef.current = false
    }
  }, [])

  const check = async () => {
    if (!repositoryIdentity || requestLock.current || loading || executePhase === 'executing') return
    const selectedIdentity = repositoryIdentity
    const generation = generationRef.current
    requestLock.current = true
    controllerRef.current?.abort()
    const controller = new AbortController()
    controllerRef.current = controller
    setLoading(true)
    setError(false)
    setAttempted(true)
    // A readiness refresh supersedes any browser-held preview authority. Do
    // not allow a preview that was prepared against the previous evidence to
    // reopen a dialog after this fresh check settles.
    previewControllerRef.current?.abort()
    previewControllerRef.current = null
    previewLock.current = false
    setPreviewPhase('idle')
    setPreview(null)
    setDialogOpen(false)
    setExecuteResult(null)
    setTokenConsumed(false)
    try {
      const next = await fetchRepositorySetupReadiness(selectedIdentity, controller.signal)
      if (identityRef.current === selectedIdentity && generation === generationRef.current && !controller.signal.aborted && mountedRef.current) {
        setResult(next)
        // A new readiness result is the only way to restore a consumed or
        // failed mutation affordance. The status remains server-owned; this
        // does not locally turn `not-ignored` into `ready`.
        setPreviewPhase('idle')
        setPreview(null)
        setDialogOpen(false)
        setExecuteResult(null)
        setTokenConsumed(false)
      }
    } catch {
      if (!controller.signal.aborted && generation === generationRef.current && identityRef.current === selectedIdentity && mountedRef.current) setError(true)
    } finally {
      if (controllerRef.current === controller && generation === generationRef.current && identityRef.current === selectedIdentity && mountedRef.current) {
        setLoading(false)
        requestLock.current = false
      }
    }
  }

  const rows = result ? [
    ['configuration', t('setup.repositoryReadinessConfiguration'), result.configuration.status],
    ['snapshot', t('setup.repositoryReadinessSnapshot'), result.snapshot.status],
    ['hooks', t('setup.repositoryReadinessHooks'), result.gitHooks.status],
    ['daemon', t('setup.repositoryReadinessDaemon'), result.daemon.status],
    ['tracking', t('setup.repositoryReadinessTracking'), result.tracking.status],
  ] as const : []

  const readinessAlias = !error && result?.snapshot.status === 'not-ignored' ? result.repositoryIdentity : null
  const visiblePreview = preview && readinessAlias && preview.repositoryIdentity === readinessAlias ? preview : null

  const addIgnoreRule = async () => {
    const alias = readinessAlias
    const selectedIdentity = repositoryIdentity
    if (!alias || !selectedIdentity || loading || previewLock.current || executePhase === 'executing' || tokenConsumed || previewPhase !== 'idle' || !mountedRef.current) return
    const generation = generationRef.current
    const controller = new AbortController()
    previewControllerRef.current?.abort()
    previewControllerRef.current = controller
    previewLock.current = true
    setPreviewPhase('loading')
    setPreview(null)
    setDialogOpen(false)
    setExecuteResult(null)
    try {
      const next = await fetchSnapshotIgnorePreview(alias, controller.signal)
      if (!mountedRef.current || generation !== generationRef.current || controller.signal.aborted || identityRef.current !== selectedIdentity || result?.repositoryIdentity !== alias) return
      setPreview(next)
      setPreviewPhase('success')
      setDialogOpen(true)
    } catch {
      if (!mountedRef.current || generation !== generationRef.current || controller.signal.aborted || identityRef.current !== selectedIdentity) return
      setPreview(null)
      setPreviewPhase('error')
    } finally {
      if (previewControllerRef.current === controller) previewControllerRef.current = null
      if (generation === generationRef.current) previewLock.current = false
    }
  }

  const openIgnoreDialog = () => {
    if (visiblePreview && previewPhase === 'success' && !tokenConsumed && !executeResult) {
      setDialogOpen(true)
      return
    }
    void addIgnoreRule()
  }

  const cancelIgnore = () => {
    if (!confirmingRef.current) setDialogOpen(false)
  }

  const confirmIgnore = async () => {
    if (!visiblePreview || tokenConsumed || confirmingRef.current || executePhase === 'executing' || !mountedRef.current) return
    const token = visiblePreview.token
    const generation = generationRef.current
    confirmingRef.current = true
    // Remove the only browser-held authority before making the execute call.
    // The server remains responsible for single-use enforcement; this client
    // never offers a retry after an ambiguous result.
    setTokenConsumed(true)
    setPreview(null)
    setDialogOpen(false)
    setExecuteResult(null)
    setExecutePhase('executing')
    try {
      const next = await executeSnapshotIgnore(token)
      if (!mountedRef.current || generation !== generationRef.current) return
      setExecuteResult(next)
    } catch {
      if (!mountedRef.current || generation !== generationRef.current) return
      setExecuteResult({ status: 'failed-unverified' })
    } finally {
      confirmingRef.current = false
      if (mountedRef.current && generation === generationRef.current) setExecutePhase('idle')
    }
  }

  return <section className="repository-setup-readiness" aria-labelledby="repository-setup-readiness-title">
    <header className="setup-subheading">
      <div><h2 id="repository-setup-readiness-title">{t('setup.repositoryReadinessTitle')}</h2><p>{t('setup.repositoryReadinessDescription')}</p></div>
      <button className="button ghost compact" disabled={!repositoryIdentity || loading} onClick={() => void check()}><RefreshCw size={14} className={loading ? 'spin' : ''} />{attempted ? t('setup.repositoryReadinessRefresh') : t('setup.repositoryReadinessCheck')}</button>
    </header>
    <div className="repository-setup-readiness-live" role="status" aria-live="polite" aria-busy={loading}>
      {loading && <span><RefreshCw size={15} className="spin" />{t('setup.repositoryReadinessLoading')}</span>}
      {!loading && error && <span className="repository-setup-readiness-error" role="alert"><ShieldAlert size={15} />{t('setup.repositoryReadinessError')}</span>}
      {!loading && !error && !result && <span><Settings2 size={15} />{t('setup.repositoryReadinessNotChecked')}</span>}
      {!loading && !error && result && <><span className={`status ${statusClass(result.overall)}`}><span className="status-dot" />{statusLabel(result.overall, t)}</span><p className="repository-setup-readiness-summary">{bodyLabel(result.overall, t)}</p></>}
    </div>
    {result && <div className="repository-setup-readiness-list">{rows.map(([key, label, status]) => <div className="repository-setup-readiness-row" key={key}><div><strong>{label}</strong><p>{statusDescription(key, status, t)}</p></div><span className={`status ${statusClass(status)}`}><span className="status-dot" />{statusLabel(status, t)}</span></div>)}</div>}
    {readinessAlias && !loading && !tokenConsumed && !executeResult && (previewPhase === 'idle' || (previewPhase === 'success' && !dialogOpen && !!visiblePreview)) && <div className="repository-setup-readiness-action"><button type="button" className="button primary" onClick={openIgnoreDialog}><Check size={14} />{t('setup.snapshotIgnoreAdd')}</button></div>}
    {previewPhase === 'loading' && <p className="repository-setup-readiness-mutation-state" role="status"><RefreshCw size={15} className="spin" />{t('setup.snapshotIgnorePreviewing')}</p>}
    {previewPhase === 'error' && <p className="repository-setup-readiness-mutation-error" role="alert"><AlertTriangle size={15} aria-hidden="true" />{t('setup.snapshotIgnorePreviewFailed')}</p>}
    {executePhase === 'executing' && <p className="repository-setup-readiness-mutation-state" role="status"><RefreshCw size={15} className="spin" />{t('setup.snapshotIgnoreApplying')}</p>}
    {executeResult?.status === 'verified-success' && <div className="repository-setup-readiness-mutation-result verified-success" role="status"><strong>{t('setup.snapshotIgnoreSuccess')}</strong><span>{t('setup.snapshotIgnoreSuccessNext')}</span><span>{t('setup.snapshotIgnoreSuccessCheck')}</span></div>}
    {executeResult?.status === 'failed-unverified' && <p className="repository-setup-readiness-mutation-result failed-unverified" role="alert">{t('setup.snapshotIgnoreFailure')}</p>}
    {dialogOpen && visiblePreview && !tokenConsumed && <SnapshotIgnoreDialog onCancel={cancelIgnore} onConfirm={() => void confirmIgnore()} />}
    {result && <span className="sr-only">{result.overall}</span>}
  </section>
}

type SnapshotIgnoreDialogProps = { onCancel: () => void; onConfirm: () => void }
const dialogFocusableSelector = 'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

function SnapshotIgnoreDialog({ onCancel, onConfirm }: SnapshotIgnoreDialogProps) {
  const { t } = useI18n()
  const dialogRef = useRef<HTMLElement | null>(null)
  const cancelRef = useRef<HTMLButtonElement | null>(null)
  useEffect(() => { cancelRef.current?.focus() }, [])
  const onDialogKeyDown = (event: ReactKeyboardEvent<HTMLElement>) => {
    if (event.key === 'Escape') { event.preventDefault(); onCancel(); return }
    if (event.key !== 'Tab') return
    const dialog = dialogRef.current
    if (!dialog) return
    const focusable = Array.from(dialog.querySelectorAll<HTMLElement>(dialogFocusableSelector)).filter(element => !element.hidden)
    if (focusable.length === 0) { event.preventDefault(); dialog.focus(); return }
    const active = document.activeElement
    const index = active instanceof HTMLElement ? focusable.indexOf(active) : -1
    if (event.shiftKey ? index <= 0 : index === focusable.length - 1 || index < 0) { event.preventDefault(); focusable[event.shiftKey ? focusable.length - 1 : 0].focus() }
  }
  return <div className="prompt-overlay repository-setup-readiness-overlay" role="presentation">
    <section className="prompt-dialog repository-setup-readiness-dialog" role="dialog" aria-modal="true" aria-labelledby="snapshot-ignore-confirm-title" aria-describedby="snapshot-ignore-confirm-description" tabIndex={-1} ref={dialogRef} onKeyDown={onDialogKeyDown}>
      <header><div><h2 id="snapshot-ignore-confirm-title">{t('setup.snapshotIgnoreTitle')}</h2><p id="snapshot-ignore-confirm-description">{t('setup.snapshotIgnoreDescription')}</p></div><button className="icon-button" type="button" aria-label={t('setup.snapshotIgnoreClose')} onClick={onCancel}><X /></button></header>
      <dl className="repository-setup-readiness-confirm-facts"><div><dt>{t('setup.snapshotIgnoreTarget')}</dt><dd><code>.gitignore</code></dd></div><div><dt>{t('setup.snapshotIgnoreWillDo')}</dt><dd>{t('setup.snapshotIgnoreAction')}</dd></div><div><dt>{t('setup.snapshotIgnoreWillNotDo')}</dt><dd>{t('setup.snapshotIgnoreNoMutation')}</dd></div></dl>
      <p className="repository-setup-readiness-confirm-note"><AlertTriangle size={15} aria-hidden="true" />{t('setup.snapshotIgnoreConfirmationNote')}</p>
      <footer><button className="button" type="button" ref={cancelRef} onClick={onCancel}>{t('common.cancel')}</button><button className="button primary" type="button" onClick={onConfirm}><Check size={15} />{t('setup.snapshotIgnoreConfirm')}</button></footer>
    </section>
  </div>
}
