import { RefreshCw, ShieldAlert, Settings2 } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { fetchRepositorySetupReadiness, type RepositorySetupReadiness } from './api'
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
  const requestLock = useRef(false)
  const identityRef = useRef(repositoryIdentity)
  const mountedRef = useRef(true)

  useEffect(() => {
    controllerRef.current?.abort()
    controllerRef.current = null
    requestLock.current = false
    setResult(null)
    setError(false)
    setAttempted(false)
    setLoading(false)
    identityRef.current = repositoryIdentity
    mountedRef.current = true
    return () => { mountedRef.current = false; controllerRef.current?.abort() }
  }, [repositoryIdentity])

  const check = async () => {
    if (!repositoryIdentity || requestLock.current || loading) return
    requestLock.current = true
    controllerRef.current?.abort()
    const controller = new AbortController()
    controllerRef.current = controller
    setLoading(true)
    setError(false)
    setAttempted(true)
    try {
      const next = await fetchRepositorySetupReadiness(repositoryIdentity, controller.signal)
      if (identityRef.current === repositoryIdentity && !controller.signal.aborted) setResult(next)
    } catch {
      if (!controller.signal.aborted && identityRef.current === repositoryIdentity && mountedRef.current) setError(true)
    } finally {
      if (controllerRef.current === controller && identityRef.current === repositoryIdentity && mountedRef.current) {
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
    {result && <span className="sr-only">{result.overall}</span>}
  </section>
}
