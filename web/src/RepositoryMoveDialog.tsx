import { useEffect, useRef, useState, type FormEvent } from 'react'
import { AlertTriangle, RefreshCw, X } from 'lucide-react'
import { normalizeRepositoryMovePreview, normalizeRepositoryMoveResult, request, type Repository, type RepositoryInventoryRow, type RepositoryMovePreview, type RepositoryMoveResult } from './api'
import { useI18n } from './i18n'

export type RepositoryMoveAvailability = {
  canMove: boolean
  reason: 'runtime-unavailable' | 'unsupported' | null
}

export type RepositoryMoveDialogProps = {
  source: RepositoryInventoryRow
  destinations: Repository[]
  availability: RepositoryMoveAvailability
  onClose: () => void
  onSuccess: (result: Extract<RepositoryMoveResult, { status: 'moved' }>) => void
  onRefresh: () => Promise<void> | void
}

type MoveState = 'editing' | 'previewing' | 'ready' | 'confirming' | 'error' | 'unverified'

function sameRepositoryPath(left: string, right: string): boolean {
  const normalized = (value: string) => value.replace(/[\\/]+/g, '/').replace(/\/$/, '')
  return normalized(left).toLocaleLowerCase() === normalized(right).toLocaleLowerCase()
}

export function RepositoryMoveDialog({ source, destinations, availability, onClose, onSuccess, onRefresh }: RepositoryMoveDialogProps) {
  const { t } = useI18n()
  const [destinationIdentity, setDestinationIdentity] = useState('')
  const [state, setState] = useState<MoveState>('editing')
  const [preview, setPreview] = useState<RepositoryMovePreview | null>(null)
  const [error, setError] = useState('')
  const [unverifiedMessage, setUnverifiedMessage] = useState('')
  const previewGeneration = useRef(0)
  const previewAbort = useRef<AbortController | null>(null)
  const confirmGeneration = useRef(0)
  const confirmAbort = useRef<AbortController | null>(null)
  const confirming = useRef(false)
  const mounted = useRef(true)

  const options = destinations.filter(repo => !sameRepositoryPath(repo.path, source.path))
  const selected = options.find(repo => repo.identity === destinationIdentity) ?? null

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

  function updateDestination(identity: string) {
    if (state === 'confirming' || state === 'unverified') return
    setDestinationIdentity(identity)
    setPreview(null)
    setError('')
    setUnverifiedMessage('')
    setState('editing')
  }

  async function prepare(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!availability.canMove || !selected || state === 'previewing' || state === 'confirming') return
    const generation = ++previewGeneration.current
    previewAbort.current?.abort()
    const controller = new AbortController()
    previewAbort.current = controller
    setState('previewing')
    setError('')
    setPreview(null)
    try {
      const payload = await request<unknown>('/api/repository-moves/preview', {
        method: 'POST',
        body: JSON.stringify({ sourcePath: source.path, destinationRepositoryIdentity: selected.identity }),
        signal: controller.signal,
      })
      const normalized = normalizeRepositoryMovePreview(payload)
      if (!normalized) throw new Error(t('repo.moveMalformed'))
      if (!mounted.current || generation !== previewGeneration.current) return
      setPreview(normalized)
      setState('ready')
    } catch (cause) {
      if (controller.signal.aborted || !mounted.current || generation !== previewGeneration.current) return
      setError(cause instanceof Error && cause.message ? cause.message : t('repo.moveMalformed'))
      setState('error')
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
      const payload = await request<unknown>('/api/repository-moves', {
        method: 'POST',
        body: JSON.stringify({ token: preview.token }),
        signal: controller.signal,
      })
      const normalized = normalizeRepositoryMoveResult(payload)
      if (!normalized) throw new Error(t('repo.moveMalformed'))
      if (!mounted.current || generation !== confirmGeneration.current) return
      if (normalized.status === 'unverified') {
        setUnverifiedMessage(normalized.message)
        setState('unverified')
        return
      }
      onSuccess(normalized)
      onClose()
    } catch (cause) {
      if (controller.signal.aborted || !mounted.current || generation !== confirmGeneration.current) return
      setError(cause instanceof Error && cause.message ? cause.message : t('repo.moveMalformed'))
      setState('error')
    } finally {
      if (mounted.current && generation === confirmGeneration.current) {
        confirming.current = false
        if (confirmAbort.current === controller) confirmAbort.current = null
      }
    }
  }

  const unavailableHint = availability.reason === 'unsupported' ? t('repo.moveUnsupported') : t('repo.moveUnavailable')
  return <div className="prompt-overlay" role="presentation">
    <section className="prompt-dialog move-dialog" role="dialog" aria-modal="true" aria-labelledby="repository-move-title">
      <header><div><span className="eyebrow">{t('repo.move')}</span><h2 id="repository-move-title">{t('repo.moveTitle')}</h2><p>{t('repo.moveDescription')}</p></div><button className="icon-button" type="button" aria-label={t('repo.moveClose')} onClick={close} disabled={state === 'confirming'}><X /></button></header>
      <form onSubmit={prepare}>
        <div className="move-facts"><span><b>{t('repo.moveSource')}</b><strong>{source.name || t('common.none')}</strong></span><span><b>{t('repo.moveSourcePath')}</b><code>{source.path}</code></span><span><b>{t('repo.moveJobs')}</b><strong>{source.jobCount}</strong></span><span><b>{t('repo.moveStatus')}</b><strong>{t('repo.trackedNotRegistered')}</strong></span></div>
        <label className="move-destination-label" htmlFor="repository-move-destination">{t('repo.moveDestination')}<select id="repository-move-destination" value={destinationIdentity} onChange={event => updateDestination(event.target.value)} disabled={state === 'previewing' || state === 'confirming' || state === 'unverified'}><option value="">{t('repo.moveDestinationPlaceholder')}</option>{options.map(repo => <option key={repo.identity} value={repo.identity}>{repo.name} · {repo.path}</option>)}</select></label>
        {preview && <div className="move-preview-facts"><span><b>{t('repo.moveDestinationPath')}</b><code>{preview.destinationPath}</code></span><span><b>{t('repo.moveDestinationName')}</b><strong>{preview.destinationName || t('common.none')}</strong></span><span><b>{t('repo.moveWorkingDirectory')}</b><code>{preview.command.workingDirectory}</code></span></div>}
        {preview && <div className="move-command-preview"><strong>{t('repo.moveCommand')}</strong><code className="move-executable">{preview.command.executable}</code><code className="move-working-directory">{t('repo.moveWorkingDirectory')}: {preview.command.workingDirectory}</code><ol>{preview.command.args.map((arg, index) => <li key={`${index}:${arg}`}><span>argv[{index}]</span><code>{arg}</code></li>)}</ol></div>}
        <p className="move-warning"><AlertTriangle size={16} /><span>{t('repo.moveWarning')}<br />{t('repo.moveRegistrationWarning')}</span></p>
        {state !== 'ready' && state !== 'confirming' && state !== 'unverified' && !availability.canMove && <p className="move-availability" role="status">{unavailableHint}</p>}
        {state === 'unverified' && <p className="move-unverified" role="alert">{unverifiedMessage || t('repo.moveUnverified')}</p>}
        {error && <p className="move-error" role="alert">{error}</p>}
        <footer>{state === 'unverified' ? <><button className="button" type="button" onClick={onClose}>{t('common.close')}</button><button className="button primary" type="button" onClick={() => { onClose(); void onRefresh() }}><RefreshCw size={15} />{t('repo.trackedRefresh')}</button></> : <><button className="button" type="button" onClick={close} disabled={state === 'confirming'}>{t('common.cancel')}</button>{state === 'confirming' ? <button className="button primary" type="button" disabled>{t('repo.moveConfirming')}</button> : state === 'ready' ? <button className="button primary" type="button" onClick={() => { void confirm() }}>{t('repo.moveConfirm')}</button> : <button className="button primary" type="submit" disabled={!availability.canMove || !selected || state === 'previewing'}>{state === 'previewing' ? t('repo.movePreparing') : t('repo.movePrepare')}</button>}</>}</footer>
      </form>
    </section>
  </div>
}
