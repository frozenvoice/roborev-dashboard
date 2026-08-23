import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import { AlertTriangle, GitBranch, Plus, RefreshCw, X } from 'lucide-react'
import { normalizeRepositoryInventoryResponse, normalizeRepositoryRenamePreview, normalizeRepositoryRenameResult, request, validateRepositoryRenameName, type Bootstrap, type RepositoryInventoryResponse, type RepositoryInventoryRow, type RepositoryRenameNameError, type RepositoryRenamePreview } from './api'
import { useI18n, type MessageKey } from './i18n'

type InventoryState = 'idle' | 'loading' | 'loaded' | 'error' | 'unavailable'
type InventoryError = { kind: 'malformed' | 'request'; message?: string }
type RenameState = 'editing' | 'previewing' | 'ready' | 'confirming' | 'error' | 'unverified' | (string & {})

export type RepositoriesProps = {
  bootstrap: Bootstrap | null
  registerRepository: (repoPath: string) => Promise<boolean>
  busy: boolean
  onToast?: (kind: 'success' | 'error' | 'warning', message: string) => void
}

type RenameAvailabilityReason = 'runtime-unavailable' | 'unsupported'

function renameAvailability(bootstrap: Bootstrap | null) {
  const installed = bootstrap?.roborev?.installed === true
  const inventorySupported = bootstrap?.roborev?.capabilities?.repositoryInventory === true
  const renameSupported = bootstrap?.roborev?.capabilities?.repositoryRename === true
  const daemonRunning = bootstrap?.roborev?.status?.running === true
  const inventoryReady = installed && inventorySupported && daemonRunning
  const canRename = inventoryReady && renameSupported
  const reason: RenameAvailabilityReason | null = !installed
    ? 'runtime-unavailable'
    : !inventorySupported || !renameSupported
      ? 'unsupported'
      : !daemonRunning
        ? 'runtime-unavailable'
        : null
  return { installed, inventorySupported, renameSupported, daemonRunning, inventoryReady, canRename, reason }
}

function renameValidationMessage(error: RepositoryRenameNameError | undefined, t: (key: MessageKey, values?: Record<string, string | number>) => string) {
  if (error === 'empty') return t('repo.renameInvalidEmpty')
  if (error === 'tooLong') return t('repo.renameInvalidTooLong')
  if (error === 'invalidUtf8') return t('repo.renameInvalidUtf8')
  if (error === 'control') return t('repo.renameInvalidControl')
  if (error === 'leadingDash') return t('repo.renameInvalidLeadingDash')
  if (error === 'same') return t('repo.renameInvalidSame')
  return t('repo.renameFailed', { error: 'invalid name' })
}

export function Repositories({ bootstrap, registerRepository, busy, onToast }: RepositoriesProps) {
  const { t } = useI18n()
  const [path, setPath] = useState('')
  const [inventory, setInventory] = useState<RepositoryInventoryResponse | null>(null)
  const [inventoryState, setInventoryState] = useState<InventoryState>(() => renameAvailability(bootstrap).inventoryReady ? 'idle' : 'unavailable')
  const [inventoryError, setInventoryError] = useState<InventoryError | null>(null)
  const [renameTarget, setRenameTarget] = useState<RepositoryInventoryRow | null>(null)
  const [renameDraft, setRenameDraft] = useState('')
  const [renameState, setRenameState] = useState<RenameState>('editing')
  const [renamePreview, setRenamePreview] = useState<RepositoryRenamePreview | null>(null)
  const [renameError, setRenameError] = useState('')
  const [renameUnverifiedMessage, setRenameUnverifiedMessage] = useState('')
  const generation = useRef(0)
  const abortRef = useRef<AbortController | null>(null)
  const previewGeneration = useRef(0)
  const previewAbortRef = useRef<AbortController | null>(null)
  const confirmGeneration = useRef(0)
  const confirmAbortRef = useRef<AbortController | null>(null)
  const renameConfirming = useRef(false)
  const mounted = useRef(true)
  const { inventoryReady, canRename, reason: renameAvailabilityReason } = renameAvailability(bootstrap)
  const availableRef = useRef(inventoryReady)
  availableRef.current = inventoryReady

  const refreshInventory = useCallback(async () => {
    if (!availableRef.current || !mounted.current) return
    const currentGeneration = ++generation.current
    abortRef.current?.abort()
    const controller = new AbortController()
    abortRef.current = controller
    setInventoryState('loading')
    setInventoryError(null)
    try {
      const payload = await request<unknown>('/api/repository-inventory', { signal: controller.signal })
      const normalized = normalizeRepositoryInventoryResponse(payload)
      if (!normalized) throw new Error('repository-inventory-malformed')
      if (!mounted.current || currentGeneration !== generation.current) return
      setInventory(normalized)
      setInventoryState('loaded')
    } catch (error) {
      if (controller.signal.aborted || !mounted.current || currentGeneration !== generation.current) return
      setInventory(null)
      setInventoryState('error')
      setInventoryError(error instanceof Error && error.message === 'repository-inventory-malformed'
        ? { kind: 'malformed' }
        : { kind: 'request', message: error instanceof Error && error.message ? error.message : undefined })
    }
  }, [inventoryReady])

  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false

      generation.current += 1
      abortRef.current?.abort()
      abortRef.current = null

      previewGeneration.current += 1
      previewAbortRef.current?.abort()
      previewAbortRef.current = null

      confirmGeneration.current += 1
      confirmAbortRef.current?.abort()
      confirmAbortRef.current = null
      renameConfirming.current = false
    }
  }, [])

  useEffect(() => {
    if (inventoryReady) {
      void refreshInventory()
      return
    }
    generation.current += 1
    abortRef.current?.abort()
    abortRef.current = null
    setInventory(null)
    setInventoryError(null)
    setInventoryState('unavailable')
  }, [inventoryReady, refreshInventory])

  async function handleRegistration(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const applied = await registerRepository(path)
    if (!applied) return
    setPath('')
    void refreshInventory()
  }

  function closeRename() {
    previewGeneration.current += 1
    previewAbortRef.current?.abort()
    previewAbortRef.current = null
    setRenameTarget(null)
    setRenameDraft('')
    setRenamePreview(null)
    setRenameError('')
    setRenameUnverifiedMessage('')
    setRenameState('editing')
  }

  function openRename(row: RepositoryInventoryRow) {
    if (!canRename || !mounted.current) return
    previewGeneration.current += 1
    previewAbortRef.current?.abort()
    previewAbortRef.current = null
    setRenameTarget(row)
    setRenameDraft('')
    setRenamePreview(null)
    setRenameError('')
    setRenameUnverifiedMessage('')
    setRenameState('editing')
  }

  async function prepareRename(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!renameTarget || !canRename || renameState === 'previewing' || renameState === 'confirming') return
    const validation = validateRepositoryRenameName(renameDraft, renameTarget.name)
    if (!validation.value) {
      setRenameError(renameValidationMessage(validation.error, t))
      setRenameState('error')
      return
    }
    const currentGeneration = ++previewGeneration.current
    previewAbortRef.current?.abort()
    const controller = new AbortController()
    previewAbortRef.current = controller
    setRenameState('previewing')
    setRenameError('')
    setRenameUnverifiedMessage('')
    setRenamePreview(null)
    try {
      const payload = await request<unknown>('/api/repository-renames/preview', { method: 'POST', body: JSON.stringify({ path: renameTarget.path, newName: validation.value }), signal: controller.signal })
      const normalized = normalizeRepositoryRenamePreview(payload)
      if (!normalized) throw new Error(t('repo.renameMalformed'))
      if (!mounted.current || currentGeneration !== previewGeneration.current) return
      setRenamePreview(normalized)
      setRenameDraft(normalized.newName)
      setRenameState('ready')
    } catch (error) {
      if (controller.signal.aborted || !mounted.current || currentGeneration !== previewGeneration.current) return
      setRenameError(error instanceof Error && error.message ? error.message : t('repo.renameMalformed'))
      setRenameState('error')
    }
  }

  async function confirmRename() {
    if (!renameTarget || !renamePreview || renameState !== 'ready' || !mounted.current || renameConfirming.current) return
    renameConfirming.current = true
    const currentGeneration = ++confirmGeneration.current
    const controller = new AbortController()
    confirmAbortRef.current = controller
    setRenameState('confirming')
    setRenameError('')
    try {
      const payload = await request<unknown>('/api/repository-renames', { method: 'POST', body: JSON.stringify({ token: renamePreview.token }), signal: controller.signal })
      const normalized = normalizeRepositoryRenameResult(payload)
      if (!normalized) throw new Error(t('repo.renameMalformed'))
      if (!mounted.current || currentGeneration !== confirmGeneration.current) return
      if (normalized.status === 'unverified') {
        setRenameUnverifiedMessage(normalized.message)
        setRenameState('unverified')
        return
      }
      closeRename()
      onToast?.('success', t('repo.renameSuccess'))
      void refreshInventory()
    } catch (error) {
      if (controller.signal.aborted || !mounted.current || currentGeneration !== confirmGeneration.current) return
      setRenameError(error instanceof Error && error.message ? error.message : t('repo.renameMalformed'))
      setRenameState('error')
    } finally {
      if (mounted.current && currentGeneration === confirmGeneration.current) {
        renameConfirming.current = false
        if (confirmAbortRef.current === controller) confirmAbortRef.current = null
      }
    }
  }

  return <section className="cards-page">
    <header className="section-heading">
      <div><span className="eyebrow">{t('repo.registeredTargets')}</span><h1>{t('nav.repositories')}</h1><p>{t('repo.onlyCanonical')}</p></div>
    </header>
    <div className="repo-cards">{bootstrap?.repositories.map(repo => <article key={repo.identity}><div className="repo-icon"><GitBranch /></div><div><h3>{repo.name}</h3><code>{repo.path}</code><p>{repo.branch} · {repo.headShort} {repo.dirty ? `· ${t('status.dirty').toLowerCase()}` : `· ${t('status.clean').toLowerCase()}`}</p></div></article>)}</div>
    <form className="add-repo" onSubmit={handleRegistration}><label htmlFor="repo-path">{t('repo.addLocal')}</label><div><input id="repo-path" value={path} onChange={event => setPath(event.target.value)} placeholder={t('repo.pathPlaceholder')} /><button className="button primary" disabled={busy || !path.trim()}><Plus size={16} />{t('repo.register')}</button></div></form>

    <section className="tracked-repositories" aria-labelledby="tracked-repositories-heading">
      <header className="section-heading tracked-heading"><div><span className="eyebrow">{t('repo.trackedEyebrow')}</span><h2 id="tracked-repositories-heading">{t('repo.trackedTitle')}</h2><p>{t('repo.trackedDescription')}</p></div>{inventoryReady && <button className="button" type="button" onClick={() => { void refreshInventory() }}><RefreshCw size={15} className={inventoryState === 'loading' ? 'spin' : undefined} />{t('repo.trackedRefresh')}</button>}</header>
      {!inventoryReady || inventoryState === 'unavailable' ? <div className="tracked-state tracked-unavailable" role="status"><strong>{t('repo.trackedUnavailable')}</strong><span>{t('repo.trackedUnavailableBody')}</span></div> : inventoryState === 'loading' && !inventory ? <div className="tracked-state" role="status"><RefreshCw className="spin" size={17} /><span>{t('repo.trackedLoading')}</span></div> : inventoryState === 'error' ? <div className="tracked-state tracked-error" role="alert"><strong>{t('repo.trackedError')}</strong><span>{inventoryError?.kind === 'malformed' ? t('repo.trackedMalformed') : inventoryError?.message || t('repo.trackedError')}</span></div> : inventory && inventory.repositories.length === 0 ? <div className="tracked-state" role="status"><span>{t('repo.trackedEmpty')}</span></div> : inventory && <>
        <div className="tracked-summary" aria-label={t('repo.trackedSummary', { count: inventory.repositoryCount, jobs: inventory.totalJobCount })}>{t('repo.trackedSummary', { count: inventory.repositoryCount, jobs: inventory.totalJobCount })}</div>
        <div className="tracked-table-wrap"><table className="tracked-table"><thead><tr><th scope="col">{t('repo.trackedName')}</th><th scope="col">{t('repo.trackedPath')}</th><th scope="col">{t('repo.trackedJobs')}</th><th scope="col">{t('repo.trackedStatus')}</th>{canRename && <th scope="col">{t('repo.rename')}</th>}</tr></thead><tbody>{inventory.repositories.map((repo, index) => <tr key={`${repo.path}:${index}`}><td><strong>{repo.name}</strong></td><td><code>{repo.path}</code></td><td>{repo.jobCount}</td><td><span className={`status ${repo.dashboardRegistered ? 'status-pass' : 'status-canceled'}`}><span className="status-dot" />{repo.dashboardRegistered ? t('repo.trackedRegistered') : t('repo.trackedNotRegistered')}</span></td>{canRename && <td><button className="button tracked-rename" type="button" onClick={() => openRename(repo)} disabled={busy || renameState === 'previewing' || renameState === 'confirming'}>{t('repo.rename')}</button></td>}</tr>)}</tbody></table></div>
      </>}
    </section>

    {renameTarget && <div className="prompt-overlay" role="presentation"><section className="prompt-dialog rename-dialog" role="dialog" aria-modal="true" aria-labelledby="repository-rename-title"><header><div><span className="eyebrow">{t('repo.rename')}</span><h2 id="repository-rename-title">{t('repo.renameTitle')}</h2><p>{t('repo.renameDescription')}</p></div><button className="icon-button" type="button" aria-label={t('repo.renameClose')} onClick={closeRename} disabled={renameState === 'confirming'}><X /></button></header>
      <form onSubmit={prepareRename}>
        <div className="rename-facts"><span><b>{t('repo.renameCurrentName')}</b><strong>{(renamePreview?.currentName ?? renameTarget.name) || t('common.none')}</strong></span><span><b>{t('repo.renamePath')}</b><code>{renamePreview?.path || renameTarget.path}</code></span><span><b>{t('repo.renameJobs')}</b><strong>{renamePreview?.jobCount ?? renameTarget.jobCount}</strong></span><span><b>{t('repo.renameDashboardStatus')}</b><strong>{(renamePreview?.dashboardRegistered ?? renameTarget.dashboardRegistered) ? t('repo.trackedRegistered') : t('repo.trackedNotRegistered')}</strong></span></div>
        <label className="rename-input-label" htmlFor="repository-rename-new-name">{t('repo.renameNewName')}<input id="repository-rename-new-name" value={renameDraft} onChange={event => { setRenameDraft(event.target.value); if (renameState !== 'confirming') { setRenameState('editing'); setRenameError(''); setRenamePreview(null); setRenameUnverifiedMessage('') } }} disabled={renameState === 'previewing' || renameState === 'confirming' || renameState === 'unverified'} autoFocus /></label>
        {renamePreview && <div className="rename-command-preview"><strong>{t('repo.renameCommand')}</strong><code className="rename-executable">{renamePreview.command.executable}</code><ol>{renamePreview.command.args.map((arg, index) => <li key={`${index}:${arg}`}><span>argv[{index}]</span><code>{arg}</code></li>)}</ol></div>}
        <p className="rename-warning"><AlertTriangle size={16} />{t('repo.renameWarning')}</p>
        {renameState !== 'ready' && renameState !== 'confirming' && renameState !== 'unverified' && !canRename && <p className="rename-availability" role="status">{renameAvailabilityReason === 'unsupported' ? t('repo.renameUnsupported') : t('repo.renameUnavailable')}</p>}
        {renameState === 'unverified' && <p className="rename-unverified" role="alert">{renameUnverifiedMessage || t('repo.renameUnverified')}</p>}
        {renameError && <p className="rename-error" role="alert">{renameError}</p>}
        <footer>{renameState === 'unverified' ? <><button className="button" type="button" onClick={closeRename}>{t('common.close')}</button><button className="button primary" type="button" onClick={() => { closeRename(); void refreshInventory() }}>{t('repo.trackedRefresh')}</button></> : <><button className="button" type="button" onClick={closeRename} disabled={renameState === 'confirming'}>{t('common.cancel')}</button>{renameState === 'confirming' ? <button className="button primary" type="button" disabled>{t('repo.renameConfirming')}</button> : renameState === 'ready' ? <button className="button primary" type="button" onClick={() => { void confirmRename() }}>{t('repo.renameConfirm')}</button> : <button className="button primary" type="submit" disabled={!canRename || renameState === 'previewing'}>{renameState === 'previewing' ? t('repo.renamePreparing') : t('repo.renamePrepare')}</button>}</>}</footer>
      </form>
    </section></div>}
  </section>
}
