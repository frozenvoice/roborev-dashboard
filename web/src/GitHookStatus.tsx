import { CircleAlert, RefreshCw } from 'lucide-react'
import { normalizeGitHookStatus, request, type GitHookOverallStatus, type GitHookPostCommitStatus, type GitHookPostRewriteStatus, type GitHookStatus } from './api'
import { useI18n } from './i18n'
import { useEffect, useRef, useState } from 'react'

type GitHookStatusProps = { repositoryIdentity?: string }
type GitHookPhase = 'unavailable' | 'loading' | 'loaded' | 'error'

// StrictMode re-runs effects on the first surface entry. Keep only unresolved
// requests here so that the second effect subscribes to the same read without
// turning a single entry into two GETs. Refresh deliberately bypasses it.
const pendingRequests = new Map<string, Promise<unknown>>()

function requestHooks(repositoryIdentity: string, force = false) {
  const existing = pendingRequests.get(repositoryIdentity)
  if (existing && !force) return existing
  const controller = new AbortController()
  const promise = request<unknown>(`/api/repositories/hooks?repositoryIdentity=${encodeURIComponent(repositoryIdentity)}`, { signal: controller.signal })
  pendingRequests.set(repositoryIdentity, promise)
  const clear = () => { if (pendingRequests.get(repositoryIdentity) === promise) pendingRequests.delete(repositoryIdentity) }
  void promise.then(clear, clear)
  return { promise, controller }
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

export function GitHookStatus({ repositoryIdentity = '' }: GitHookStatusProps) {
  const { t } = useI18n()
  const [phase, setPhase] = useState<GitHookPhase>('unavailable')
  const [data, setData] = useState<GitHookStatus | null>(null)
  const generationRef = useRef(0)
  const mountedRef = useRef(true)
  const controllerRef = useRef<AbortController | null>(null)

  const load = (force = false) => {
    const identity = repositoryIdentity.trim()
    if (!identity || !mountedRef.current) return
    const generation = ++generationRef.current
    controllerRef.current?.abort()
    const result = requestHooks(identity, force)
    const promise = 'promise' in result ? result.promise : result
    if ('controller' in result) controllerRef.current = result.controller
    setPhase('loading')
    void promise.then(value => {
      const normalized = normalizeGitHookStatus(value, identity)
      if (!normalized) throw new Error('invalid-git-hook-status')
      if (!mountedRef.current || generation !== generationRef.current || repositoryIdentity.trim() !== identity) return
      setData(normalized)
      setPhase('loaded')
    }).catch(() => {
      if (!mountedRef.current || generation !== generationRef.current || repositoryIdentity.trim() !== identity) return
      setData(null)
      setPhase('error')
    }).finally(() => {
      if (mountedRef.current && generation === generationRef.current && controllerRef.current && 'controller' in result && controllerRef.current === result.controller) controllerRef.current = null
    })
  }

  useEffect(() => {
    mountedRef.current = true
    const identity = repositoryIdentity.trim()
    if (!identity) {
      generationRef.current += 1
      setData(null)
      setPhase('unavailable')
    } else {
      load()
    }
    return () => {
      mountedRef.current = false
      generationRef.current += 1
      // Do not abort here: StrictMode immediately replays this effect and
      // must coalesce onto the same pending read. Generation guards late data.
      controllerRef.current = null
    }
  }, [repositoryIdentity])

  const titleId = 'git-hook-status-title'
  const loading = phase === 'loading'
  const unavailable = phase === 'unavailable' || phase === 'error' || !repositoryIdentity.trim()
  return <section className="git-hook-status" aria-labelledby={titleId} aria-busy={loading}>
    <header className="git-hook-status-heading">
      <div>
        <span className="eyebrow">{t('setup.gitHooksEyebrow')}</span>
        <h2 id={titleId}>{t('setup.gitHooksTitle')}</h2>
        <p>{t('setup.gitHooksDescription')}</p>
      </div>
      <button className="button ghost compact" disabled={!repositoryIdentity.trim()} onClick={() => load(true)} aria-label={t('setup.gitHooksRefresh')}>
        <RefreshCw size={14} className={loading ? 'spin' : ''} />{t('common.refresh')}
      </button>
    </header>
    {loading && !data && <div className="git-hook-status-state" aria-live="polite"><RefreshCw size={17} className="spin" aria-hidden="true" /><span>{t('setup.gitHooksLoading')}</span></div>}
    {unavailable && <div className="git-hook-status-state git-hook-status-unavailable" role={phase === 'error' ? 'alert' : 'status'}><CircleAlert size={17} aria-hidden="true" /><div><strong>{t('setup.gitHooksUnavailable')}</strong><p>{t('setup.gitHooksUnavailableBody')}</p></div></div>}
    {phase === 'loaded' && data && <>
      <div className="git-hook-status-summary"><StatusValue status={data.status} label={overallLabel(data.status, t)} /></div>
      <dl className="git-hook-status-list">
        <div className="git-hook-status-row"><dt>{t('setup.gitHooksPostCommit')}</dt><dd><StatusValue status={data.postCommit.status} label={postCommitLabel(data.postCommit.status, t)} /></dd></div>
        <div className="git-hook-status-row"><dt>{t('setup.gitHooksPostRewrite')}</dt><dd><StatusValue status={data.postRewrite.status} label={postRewriteLabel(data.postRewrite.status, t)} /></dd></div>
      </dl>
    </>}
  </section>
}

export default GitHookStatus
