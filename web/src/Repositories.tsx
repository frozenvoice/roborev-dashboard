import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import { GitBranch, Plus, RefreshCw } from 'lucide-react'
import { normalizeRepositoryInventoryResponse, request, type Bootstrap, type RepositoryInventoryResponse } from './api'
import { useI18n } from './i18n'

type InventoryState = 'idle' | 'loading' | 'loaded' | 'error' | 'unavailable'
type InventoryError = { kind: 'malformed' | 'request'; message?: string }

export type RepositoriesProps = {
  bootstrap: Bootstrap | null
  registerRepository: (repoPath: string) => Promise<boolean>
  busy: boolean
}

function inventoryAvailable(bootstrap: Bootstrap | null) {
  return Boolean(bootstrap?.roborev?.installed && bootstrap.roborev.status?.running && bootstrap.roborev.capabilities?.repositoryInventory)
}

export function Repositories({ bootstrap, registerRepository, busy }: RepositoriesProps) {
  const { t } = useI18n()
  const [path, setPath] = useState('')
  const [inventory, setInventory] = useState<RepositoryInventoryResponse | null>(null)
  const [inventoryState, setInventoryState] = useState<InventoryState>(() => inventoryAvailable(bootstrap) ? 'idle' : 'unavailable')
  const [inventoryError, setInventoryError] = useState<InventoryError | null>(null)
  const generation = useRef(0)
  const abortRef = useRef<AbortController | null>(null)
  const mounted = useRef(true)
  const available = inventoryAvailable(bootstrap)

  const refreshInventory = useCallback(async () => {
    if (!available || !mounted.current) return
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
  }, [available])

  useEffect(() => {
    mounted.current = true
    if (available) void refreshInventory()
    else {
      generation.current += 1
      abortRef.current?.abort()
      setInventory(null)
      setInventoryError(null)
      setInventoryState('unavailable')
    }
    return () => {
      mounted.current = false
      generation.current += 1
      abortRef.current?.abort()
      abortRef.current = null
    }
  }, [available, refreshInventory])

  async function handleRegistration(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const applied = await registerRepository(path)
    if (!applied) return
    setPath('')
    // Inventory is an independent read. A stale/failing inventory refresh
    // must never change the already-successful registration outcome.
    void refreshInventory()
  }

  return <section className="cards-page">
    <header className="section-heading">
      <div><span className="eyebrow">{t('repo.registeredTargets')}</span><h1>{t('nav.repositories')}</h1><p>{t('repo.onlyCanonical')}</p></div>
    </header>
    <div className="repo-cards">{bootstrap?.repositories.map(repo => <article key={repo.identity}><div className="repo-icon"><GitBranch /></div><div><h3>{repo.name}</h3><code>{repo.path}</code><p>{repo.branch} · {repo.headShort} {repo.dirty ? `· ${t('status.dirty').toLowerCase()}` : `· ${t('status.clean').toLowerCase()}`}</p></div></article>)}</div>
    <form className="add-repo" onSubmit={handleRegistration}><label htmlFor="repo-path">{t('repo.addLocal')}</label><div><input id="repo-path" value={path} onChange={event => setPath(event.target.value)} placeholder={t('repo.pathPlaceholder')} /><button className="button primary" disabled={busy || !path.trim()}><Plus size={16} />{t('repo.register')}</button></div></form>

    <section className="tracked-repositories" aria-labelledby="tracked-repositories-heading">
      <header className="section-heading tracked-heading"><div><span className="eyebrow">{t('repo.trackedEyebrow')}</span><h2 id="tracked-repositories-heading">{t('repo.trackedTitle')}</h2><p>{t('repo.trackedDescription')}</p></div>{available && <button className="button" type="button" onClick={() => { void refreshInventory() }}><RefreshCw size={15} className={inventoryState === 'loading' ? 'spin' : undefined} />{t('repo.trackedRefresh')}</button>}</header>
      {!available || inventoryState === 'unavailable' ? <div className="tracked-state tracked-unavailable" role="status"><strong>{t('repo.trackedUnavailable')}</strong><span>{t('repo.trackedUnavailableBody')}</span></div> : inventoryState === 'loading' && !inventory ? <div className="tracked-state" role="status"><RefreshCw className="spin" size={17} /><span>{t('repo.trackedLoading')}</span></div> : inventoryState === 'error' ? <div className="tracked-state tracked-error" role="alert"><strong>{t('repo.trackedError')}</strong><span>{inventoryError?.kind === 'malformed' ? t('repo.trackedMalformed') : inventoryError?.message || t('repo.trackedError')}</span></div> : inventory && inventory.repositories.length === 0 ? <div className="tracked-state" role="status"><span>{t('repo.trackedEmpty')}</span></div> : inventory && <>
        <div className="tracked-summary" aria-label={t('repo.trackedSummary', { count: inventory.repositoryCount, jobs: inventory.totalJobCount })}>{t('repo.trackedSummary', { count: inventory.repositoryCount, jobs: inventory.totalJobCount })}</div>
        <div className="tracked-table-wrap"><table className="tracked-table"><thead><tr><th scope="col">{t('repo.trackedName')}</th><th scope="col">{t('repo.trackedPath')}</th><th scope="col">{t('repo.trackedJobs')}</th><th scope="col">{t('repo.trackedStatus')}</th></tr></thead><tbody>{inventory.repositories.map((repo, index) => <tr key={`${repo.path}:${index}`}><td><strong>{repo.name}</strong></td><td><code>{repo.path}</code></td><td>{repo.jobCount}</td><td><span className={`status ${repo.dashboardRegistered ? 'status-pass' : 'status-canceled'}`}><span className="status-dot" />{repo.dashboardRegistered ? t('repo.trackedRegistered') : t('repo.trackedNotRegistered')}</span></td></tr>)}</tbody></table></div>
      </>}
    </section>
  </section>
}
