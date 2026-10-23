import { useEffect, useRef, useState, type FormEvent } from 'react'
import { AlertTriangle, RefreshCw, X } from 'lucide-react'
import { normalizeRepositoryMergePreview, normalizeRepositoryMergeResult, request, type RepositoryMergeCandidate, type RepositoryMergePreview, type RepositoryMergeSnapshot } from './api'
import { useI18n } from './i18n'
import { useModalFocus } from './useModalFocus'

export type RepositoryMergeAvailability = {
  canMerge: boolean
  reason: 'runtime-unavailable' | 'unsupported' | null
}

export type RepositoryMergeDialogProps = {
  source: RepositoryMergeSnapshot
  targets: RepositoryMergeCandidate[]
  availability: RepositoryMergeAvailability
  onClose: () => void
  onSuccess: () => void
  onRefresh: () => Promise<void> | void
}

type MergeState = 'editing' | 'previewing' | 'ready' | 'confirming' | 'error' | 'unverified'

export function RepositoryMergeDialog({ source, targets, availability, onClose, onSuccess, onRefresh }: RepositoryMergeDialogProps) {
  const { t } = useI18n()
  const [targetIdentity, setTargetIdentity] = useState('')
  const [state, setState] = useState<MergeState>('editing')
  const [preview, setPreview] = useState<RepositoryMergePreview | null>(null)
  const [error, setError] = useState('')
  const [unverifiedMessage, setUnverifiedMessage] = useState('')
  const previewGeneration = useRef(0)
  const previewAbort = useRef<AbortController | null>(null)
  const confirmGeneration = useRef(0)
  const confirmAbort = useRef<AbortController | null>(null)
  const confirming = useRef(false)
  const mounted = useRef(true)
  const modalFocus = useModalFocus(close, state === 'confirming')

  const selected = targets.find(target => target.repositoryIdentity === targetIdentity) ?? null

  useEffect(() => {
    mounted.current = true
    return () => {
    mounted.current = false
    previewGeneration.current += 1
    previewAbort.current?.abort()
    previewAbort.current = null
    confirmGeneration.current += 1
    confirmAbort.current?.abort()
    confirmAbort.current = null
    confirming.current = false
    }
  }, [])

  function close() {
    if (state === 'confirming') return
    onClose()
  }

  function updateTarget(identity: string) {
    if (state === 'confirming' || state === 'unverified') return
    setTargetIdentity(identity)
    setPreview(null)
    setError('')
    setUnverifiedMessage('')
    setState('editing')
  }

  async function prepare(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!availability.canMerge || !selected || state === 'previewing' || state === 'confirming') return
    const generation = ++previewGeneration.current
    previewAbort.current?.abort()
    const controller = new AbortController()
    previewAbort.current = controller
    setState('previewing')
    setError('')
    setPreview(null)
    try {
      const payload = await request<unknown>('/api/repository-merges/preview', {
        method: 'POST',
        body: JSON.stringify({ sourcePath: source.path, targetRepositoryIdentity: selected.repositoryIdentity }),
        signal: controller.signal,
      })
      const normalized = normalizeRepositoryMergePreview(payload)
      if (!normalized) throw new Error(t('repo.mergeMalformed'))
      if (!mounted.current || generation !== previewGeneration.current) return
      setPreview(normalized)
      setState('ready')
    } catch (cause) {
      if (controller.signal.aborted || !mounted.current || generation !== previewGeneration.current) return
      setError(cause instanceof Error && cause.message ? cause.message : t('repo.mergeMalformed'))
      setState('error')
    } finally {
      if (mounted.current && generation === previewGeneration.current && previewAbort.current === controller) previewAbort.current = null
    }
  }

  async function confirm() {
    if (!preview || state !== 'ready' || !mounted.current || confirming.current) return
    confirming.current = true
    const generation = ++confirmGeneration.current
    const controller = new AbortController()
    confirmAbort.current = controller
    setState('confirming')
    setError('')
    try {
      const payload = await request<unknown>('/api/repository-merges', {
        method: 'POST',
        body: JSON.stringify({ token: preview.token }),
        signal: controller.signal,
      })
      const normalized = normalizeRepositoryMergeResult(payload)
      if (!normalized) throw new Error(t('repo.mergeMalformed'))
      if (!mounted.current || generation !== confirmGeneration.current) return
      if (normalized.status === 'unverified') {
        setUnverifiedMessage(normalized.message)
        setState('unverified')
        return
      }
      onSuccess()
    } catch (cause) {
      if (controller.signal.aborted || !mounted.current || generation !== confirmGeneration.current) return
      setError(cause instanceof Error && cause.message ? cause.message : t('repo.mergeMalformed'))
      setState('error')
    } finally {
      if (mounted.current && generation === confirmGeneration.current) {
        confirming.current = false
        if (confirmAbort.current === controller) confirmAbort.current = null
      }
    }
  }

  const unavailableHint = availability.reason === 'unsupported' ? t('repo.mergeUnsupported') : t('repo.mergeUnavailable')
  const shownSource = preview?.source ?? source
  const shownTarget = preview?.target ?? selected
  return <div className="prompt-overlay" role="presentation">
    <section {...modalFocus} className="prompt-dialog merge-dialog" role="dialog" aria-modal="true" aria-labelledby="repository-merge-title">
      <header><div><span className="eyebrow">{t('repo.merge')}</span><h2 id="repository-merge-title">{t('repo.mergeTitle')}</h2><p>{t('repo.mergeDescription')}</p></div><button className="icon-button" type="button" aria-label={t('repo.mergeClose')} onClick={close} disabled={state === 'confirming'}><X /></button></header>
      <form onSubmit={prepare}>
        <div className="merge-facts"><span><b>{t('repo.mergeSource')}</b><strong>{shownSource.name || t('common.none')}</strong></span><span><b>{t('repo.mergeSourcePath')}</b><code>{shownSource.path}</code></span><span><b>{t('repo.mergeSourceJobs')}</b><strong>{shownSource.jobCount}</strong></span><span><b>{t('repo.mergeTargetJobs')}</b><strong>{shownTarget?.jobCount ?? t('common.none')}</strong></span></div>
        <label className="merge-target-label" htmlFor="repository-merge-target">{t('repo.mergeTarget')}<select id="repository-merge-target" value={targetIdentity} onChange={event => updateTarget(event.target.value)} disabled={state === 'previewing' || state === 'confirming' || state === 'unverified'}><option value="">{t('repo.mergeTargetPlaceholder')}</option>{targets.map(target => <option key={target.repositoryIdentity} value={target.repositoryIdentity}>{target.name} · {target.path}</option>)}</select></label>
        {shownTarget && <div className="merge-preview-facts"><span><b>{t('repo.mergeTargetName')}</b><strong>{shownTarget.name || t('common.none')}</strong></span><span><b>{t('repo.mergeTargetPath')}</b><code>{shownTarget.path}</code></span><span><b>{t('repo.mergeTargetJobs')}</b><strong>{shownTarget.jobCount}</strong></span></div>}
        {preview && <div className="merge-command-preview"><strong>{t('repo.mergeCommand')}</strong><code className="merge-executable">{preview.command.executable}</code><code className="merge-working-directory">{t('repo.mergeWorkingDirectory')}: {preview.command.workingDirectory || t('common.none')}</code><ol>{preview.command.args.map((arg, index) => <li key={`${index}:${arg}`}><span>argv[{index}]</span><code>{arg}</code></li>)}</ol></div>}
        <p className="merge-warning"><AlertTriangle size={16} /><span>{t('repo.mergeWarning')}<br />{t('repo.mergeWarningNoRollback')}</span></p>
        {(state === 'editing' || state === 'error') && !availability.canMerge && <p className="merge-availability" role="status">{unavailableHint}</p>}
        {state === 'unverified' && <p className="merge-unverified" role="alert"><span>{t('repo.mergeUnverified')}</span>{unverifiedMessage && <small>{unverifiedMessage}</small>}</p>}
        {error && <p className="merge-error" role="alert">{error}</p>}
        <footer>{state === 'unverified' ? <><button className="button" type="button" onClick={onClose}>{t('common.close')}</button><button className="button primary" type="button" onClick={() => { onClose(); void onRefresh() }}><RefreshCw size={15} />{t('repo.trackedRefresh')}</button></> : <><button className="button" type="button" onClick={close} disabled={state === 'confirming'}>{t('common.cancel')}</button>{state === 'confirming' ? <button className="button primary" type="button" disabled>{t('repo.mergeConfirming')}</button> : state === 'ready' ? <button className="button primary" type="button" onClick={() => { void confirm() }}>{t('repo.mergeConfirm')}</button> : <button className="button primary" type="submit" disabled={!availability.canMerge || !selected || state === 'previewing'}>{state === 'previewing' ? t('repo.mergePreparing') : t('repo.mergePrepare')}</button>}</>}</footer>
      </form>
    </section>
  </div>
}
