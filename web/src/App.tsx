import { Component, type ErrorInfo, type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Activity, AlertTriangle, Bot, Check, ChevronRight, CircleStop, Clipboard, Clock3, Code2, GitBranch, GitCommit, LayoutDashboard, ListChecks, Menu, MessageSquareText, Play, Plus, RefreshCw, RotateCcw, Search, Server, Settings, ShieldCheck, SlidersHorizontal, TerminalSquare, X, XCircle } from 'lucide-react'
import { APIError, normalizeAgentsResponse, normalizePanelRefineResponse, normalizeRuntimeControl, normalizeSetupState, type AgentsResponse, type Bootstrap, type FixPromptResponse, type Installation, type PanelConfiguration, type PanelInfo, type PanelRefineOptions, type PanelRefineRun, type Repository, type ReviewEnqueueResponse, type ReviewJob, type RuntimeControlState, type SetupState, jobsFromPayload, request, setCSRF } from './api'
import { Markdown } from './Markdown'
import { PanelRefine } from './PanelRefine'
import { InstallConfirmation, Setup, SetupBanner, setupIsActive, type DaemonAction } from './Setup'

type Toast = { id: number; kind: 'success' | 'error'; message: string }
type Tab = 'review' | 'members' | 'log' | 'prompt' | 'comments' | 'metadata'

const navigation = [
  ['Overview', LayoutDashboard], ['Reviews', ListChecks], ['Fix & Refine', ShieldCheck],
  ['Analyze', Search], ['Tasks', TerminalSquare], ['Repositories', GitBranch],
  ['Agents & Setup', Bot], ['Configuration', Settings], ['Stats & Insights', Activity], ['Advanced', SlidersHorizontal],
] as const

function statusOf(job: ReviewJob) { return String(job.verdict || job.review && typeof job.review === 'object' && job.review.verdict || job.status || 'queued').toLowerCase() }
function statusLabel(value: string) {
  if (value === 'p' || value === 'pass' || value === 'passed') return 'Pass'
  if (value === 'f' || value === 'fail' || value === 'finding' || value === 'failed_review') return 'Finding'
  if (value === 'done') return 'Done'
  if (value === 'running' || value === 'queued') return value[0].toUpperCase() + value.slice(1)
  if (value === 'canceled' || value === 'cancelled') return 'Canceled'
  if (value === 'failed' || value === 'error') return 'Error'
  return value || 'Unknown'
}
function outputOf(job: ReviewJob | null) {
  if (!job) return ''
  if (typeof job.review === 'string') return job.review
  return String(job.review?.output || job.output || '')
}
function idOf(job: ReviewJob) { return Number(job.id || job.job_id) }
function isRunning(job: ReviewJob) { return ['queued', 'running'].includes(String(job.status).toLowerCase()) }
function isPanelRefineActive(run: PanelRefineRun | null) { return Boolean(run && ['starting', 'reviewing', 'evaluating', 'fixing', 'final_reviewing'].includes(run.phase)) }
function isClosed(job: ReviewJob) { return Boolean(job.addressed || job.closed) }
function isFinding(job: ReviewJob) { return !isRunning(job) && ['f', 'fail', 'finding', 'failed_review'].includes(statusOf(job)) }
function elapsed(job: ReviewJob) {
  if (job.duration_ms) return `${(job.duration_ms / 1000).toFixed(1)}s`
  const start = job.started_at || job.created_at || job.enqueued_at
  if (!start) return '—'
  const finished = job.finished_at || job.completed_at
  const end = finished ? Date.parse(finished) : Date.now()
  const seconds = Math.max(0, Math.floor((end - Date.parse(start)) / 1000))
  return seconds < 60 ? `${seconds}s` : `${Math.floor(seconds / 60)}m ${seconds % 60}s`
}
function panelLabel(panel: PanelInfo) { return panel.name === 'none' ? 'Single agent · no panel' : `Cross review · ${panel.name}` }
function validPanelConfiguration(value: PanelConfiguration, identity: string) {
  return value?.repositoryIdentity === identity && Array.isArray(value.panels) && typeof value.selectedPanel === 'string' && typeof value.selectionRequired === 'boolean' && value.panels.every(panel => panel && typeof panel.name === 'string' && ['panel', 'single'].includes(panel.kind) && Array.isArray(panel.members) && ['global', 'local', 'builtin'].includes(panel.origin) && typeof panel.valid === 'boolean')
}
export class ErrorBoundary extends Component<{ children: ReactNode }, { error?: Error }> {
  state: { error?: Error } = {}
  static getDerivedStateFromError(error: Error) { return { error } }
  componentDidCatch(error: Error, info: ErrorInfo) { console.error(error, info) }
  render() {
    if (this.state.error) return <main className="fatal"><XCircle /><h1>Dashboard UI failed</h1><p>{this.state.error.message}</p><button onClick={() => location.reload()}>Reload</button></main>
    return this.props.children
  }
}

function StatusPill({ job }: { job: ReviewJob }) {
  const status = statusOf(job)
  const visualStatus = status === 'p' ? 'pass' : status === 'f' ? 'finding' : status
  return <span className={`status status-${visualStatus}`}><span className="status-dot" />{statusLabel(status)}</span>
}

function CopyButton({ value, label = 'Copy' }: { value: string; label?: string }) {
  const [copied, setCopied] = useState(false)
  return <button className="button ghost compact" onClick={async () => { await navigator.clipboard.writeText(value); setCopied(true); setTimeout(() => setCopied(false), 1200) }}><Clipboard size={14} />{copied ? 'Copied' : label}</button>
}

export function CommentBox({ jobId, mutate, busy }: { jobId: number; mutate: (path: string, body?: unknown) => Promise<void>; busy: boolean }) {
  const [comment, setComment] = useState('')
  const submit = async (close: boolean) => {
    await mutate(`/api/reviews/${jobId}/${close ? 'comment-close' : 'comment'}`, { comment })
    setComment('')
  }
  return <section className="comment-box">
    <label htmlFor="review-comment">Comment</label>
    <textarea id="review-comment" value={comment} onChange={event => setComment(event.target.value)} placeholder="Paste a detailed resolution note…" rows={6} />
    <div className="button-row">
      <button className="button" disabled={busy || !comment.trim()} onClick={() => submit(false)}><MessageSquareText size={16} />Add comment</button>
      <button className="button primary" disabled={busy || !comment.trim()} onClick={() => submit(true)}><Check size={16} />Comment & Close</button>
    </div>
    <p className="hint">Comment is saved first. This review closes only after that succeeds.</p>
  </section>
}

function ReviewDetail({ job, jobs, close, mutate, busy, canRerun, canLoadMembers, repoPath, repoIdentity, toast }: { job: ReviewJob; jobs: ReviewJob[]; close: () => void; mutate: (path: string, body?: unknown) => Promise<void>; busy: boolean; canRerun: boolean; canLoadMembers: boolean; repoPath: string; repoIdentity: string; toast: (kind: Toast['kind'], message: string) => void }) {
  const [tab, setTab] = useState<Tab>('review')
  const [detail, setDetail] = useState<ReviewJob | null>(null)
  const [lazy, setLazy] = useState<Record<string, string>>({})
  const [lazyLoaded, setLazyLoaded] = useState<Record<string, boolean>>({})
  const [lazyError, setLazyError] = useState<Record<string, string>>({})
  const [detailError, setDetailError] = useState('')
  const [memberJobs, setMemberJobs] = useState<ReviewJob[]>([])
  const [memberError, setMemberError] = useState('')
  const [loading, setLoading] = useState(false)
  const [fixPrompt, setFixPrompt] = useState<FixPromptResponse | null>(null)
  const [fixPromptText, setFixPromptText] = useState('')
  const [fixPromptLoading, setFixPromptLoading] = useState(false)
  const previousStatus = useRef(job.status)
  const detailGeneration = useRef(0)
  const lazyGeneration = useRef<Record<string, number>>({})
  const fixPromptGeneration = useRef(0)
  const fixPromptTarget = useRef<{ jobId: number; member: boolean } | null>(null)
  const id = idOf(job)
  const members = useMemo(() => memberJobs.length ? memberJobs : jobs.filter(candidate => candidate.parent_job_id === id || (job.member_job_ids ?? []).includes(idOf(candidate))), [id, job.member_job_ids, jobs, memberJobs])

  useEffect(() => {
    const generation = ++detailGeneration.current
    lazyGeneration.current = { prompt: (lazyGeneration.current.prompt ?? 0) + 1, log: (lazyGeneration.current.log ?? 0) + 1 }
    setDetail(null); setDetailError(''); setLazy({}); setLazyLoaded({}); setLazyError({}); setMemberJobs([]); setMemberError(''); setTab('review'); setLoading(true)
    request<ReviewJob>(`/api/reviews/${id}`).then(value => { if (generation === detailGeneration.current) setDetail({ ...value, id, job_id: id }) }).catch(error => { if (generation === detailGeneration.current) { setDetail(null); setDetailError((error as Error).message) } }).finally(() => { if (generation === detailGeneration.current) setLoading(false) })
    return () => { detailGeneration.current++; lazyGeneration.current.prompt++; lazyGeneration.current.log++ }
  }, [id])
  useEffect(() => {
    const wasRunning = ['queued', 'running'].includes(String(previousStatus.current).toLowerCase())
    previousStatus.current = job.status
    if (!wasRunning || isRunning(job)) return
    const detailRequest = ++detailGeneration.current
    const refreshDetail = (attempt: number) => request<ReviewJob>(`/api/reviews/${id}`).then(value => { if (detailRequest === detailGeneration.current) { setDetail({ ...value, id, job_id: id }); setDetailError('') } }).catch(error => { if (detailRequest !== detailGeneration.current) return; if (attempt < 1) setTimeout(() => refreshDetail(attempt + 1), 1200); else setDetailError((error as Error).message) }).finally(() => { if (detailRequest === detailGeneration.current) setLoading(false) })
    refreshDetail(0)
    if (lazyLoaded.log) {
      const logRequest = (lazyGeneration.current.log ?? 0) + 1
      lazyGeneration.current.log = logRequest
      request<{ text: string }>(`/api/reviews/${id}/log`).then(value => { if (logRequest === lazyGeneration.current.log) { setLazy(old => ({ ...old, log: value.text })); setLazyError(old => ({ ...old, log: '' })) } }).catch(error => { if (logRequest === lazyGeneration.current.log) setLazyError(old => ({ ...old, log: (error as Error).message })) }).finally(() => { if (logRequest === lazyGeneration.current.log) setLoading(false) })
    }
  }, [id, job.status, lazyLoaded.log])
  useEffect(() => {
    if (!['prompt', 'log'].includes(tab) || lazyLoaded[tab]) return
    const generation = (lazyGeneration.current[tab] ?? 0) + 1
    lazyGeneration.current[tab] = generation
    setLazyLoaded(old => ({ ...old, [tab]: true }))
    setLazyError(old => ({ ...old, [tab]: '' }))
    setLoading(true)
    request<{ text: string }>(`/api/reviews/${id}/${tab}`).then(value => { if (generation === lazyGeneration.current[tab]) setLazy(old => ({ ...old, [tab]: value.text })) }).catch(error => { if (generation === lazyGeneration.current[tab]) setLazyError(old => ({ ...old, [tab]: (error as Error).message })) }).finally(() => { if (generation === lazyGeneration.current[tab]) setLoading(false) })
  }, [id, lazyLoaded, tab])
  useEffect(() => {
    if (tab !== 'members' || !job.panel_run_uuid) return
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    let first = true
    const loadMembers = async () => {
      if (first) setLoading(true)
      try {
        const value = await request<unknown>(`/api/panels/${encodeURIComponent(job.panel_run_uuid!)}/members`)
        if (cancelled) return
        const next = jobsFromPayload(value).filter(member => member.panel_role === 'member').sort((a, b) => (a.panel_member_index ?? 0) - (b.panel_member_index ?? 0))
        const activeTarget = fixPromptTarget.current
        if (activeTarget?.member && !next.some(member => idOf(member) === activeTarget.jobId && isFinding(member))) {
          fixPromptGeneration.current++
          fixPromptTarget.current = null
          setFixPromptLoading(false)
        }
        setMemberJobs(next)
        setMemberError('')
        if (isRunning(job) || next.some(isRunning)) timer = setTimeout(loadMembers, 4000)
      } catch (error) {
        if (cancelled) return
        setMemberError((error as Error).message)
        if (isRunning(job)) timer = setTimeout(loadMembers, 4000)
      } finally {
        if (!cancelled && first) setLoading(false)
        first = false
      }
    }
    loadMembers()
    return () => { cancelled = true; if (timer) clearTimeout(timer) }
  }, [job.panel_run_uuid, job.status, tab])
  useEffect(() => {
    fixPromptGeneration.current++
    fixPromptTarget.current = null
    setFixPrompt(null)
    setFixPromptText('')
    setFixPromptLoading(false)
    return () => { fixPromptGeneration.current++ }
  }, [id, repoIdentity])

  const current = detail ? { ...detail, ...job, output: detail.output ?? job.output, review: detail.review ?? job.review, id, job_id: id } : job
  const text = outputOf(current)
  const terminal = !isRunning(current)
  const finding = terminal && isFinding(current)
  const openFixPrompt = (value: FixPromptResponse) => { setFixPrompt(value); setFixPromptText(value.text) }
  const loadFixPrompt = async (copy: boolean, targetJobId = id) => {
    if (!repoPath) return
    const generation = ++fixPromptGeneration.current
    fixPromptTarget.current = { jobId: targetJobId, member: targetJobId !== id }
    setFixPromptLoading(true)
    try {
      const value = await request<FixPromptResponse>(`/api/reviews/${targetJobId}/fix-prompt?repo=${encodeURIComponent(repoPath)}`)
      if (generation !== fixPromptGeneration.current) return
      if (!value || value.jobId !== targetJobId || typeof value.text !== 'string' || typeof value.stale !== 'boolean') throw new APIError('The fix prompt response was malformed.')
      if (!copy) { openFixPrompt(value); return }
      try {
        if (typeof navigator.clipboard?.writeText !== 'function') throw new Error('Clipboard unavailable')
        await navigator.clipboard.writeText(value.text)
        if (generation === fixPromptGeneration.current) toast('success', `Fix prompt for review #${targetJobId} copied.`)
      } catch {
        if (generation === fixPromptGeneration.current) openFixPrompt(value)
      }
    } catch (error) {
      if (generation === fixPromptGeneration.current) toast('error', (error as Error).message)
    } finally {
      if (generation === fixPromptGeneration.current) {
        fixPromptTarget.current = null
        setFixPromptLoading(false)
      }
    }
  }
  const copyEditedFixPrompt = async () => {
    try {
      if (typeof navigator.clipboard?.writeText !== 'function') throw new Error('Clipboard unavailable')
      await navigator.clipboard.writeText(fixPromptText)
      toast('success', `Fix prompt for review #${fixPrompt?.jobId ?? id} copied.`)
    } catch {
      toast('error', 'Clipboard is unavailable. The editable fix prompt remains open.')
    }
  }
  return <><aside className="detail-panel" aria-label="Review detail">
    <header className="detail-header"><div><span className="eyebrow">Review #{id}</span><h2>{current.commit_subject || current.subject || current.git_ref || current.commit_sha || 'Review detail'}</h2></div><button className="icon-button" aria-label="Close detail" onClick={close}><X /></button></header>
    <div className="detail-summary"><StatusPill job={current} /><span><Bot size={14} />{current.panel_name || current.agent || 'default'}</span><span><Clock3 size={14} />{elapsed(current)}</span></div>
    <nav className="tabs" aria-label="Review detail tabs">
      {(['review', ...(canLoadMembers ? ['members' as const] : []), 'log', 'prompt', 'comments', 'metadata'] as Tab[]).map(item => <button key={item} className={tab === item ? 'active' : ''} onClick={() => setTab(item)}>{item === 'members' ? 'Panel members' : item[0].toUpperCase() + item.slice(1)}</button>)}
    </nav>
    <div className="detail-content">
      {loading && <div className="skeleton tall" />}
      {!loading && detailError && <p role="alert" className="hint">Review detail could not be refreshed: {detailError}</p>}
      {!loading && tab === 'review' && <><div className="text-toolbar"><CopyButton value={text} /><span>{text.length.toLocaleString()} chars</span></div>{text ? <Markdown>{text}</Markdown> : <Empty title="No review output yet" body="Running reviews update automatically." />}</>}
      {!loading && tab === 'members' && <div className="member-list">{memberError && <p role="alert" className="hint">Panel members could not be refreshed: {memberError}</p>}{members.length ? members.map(member => <article key={idOf(member)}><StatusPill job={member} /><strong>{member.agent || `Member #${idOf(member)}`}</strong><span>{member.model || 'default model'}</span><span>{elapsed(member)}</span>{isFinding(member) && <div className="member-actions"><button className="button ghost compact" disabled={fixPromptLoading} onClick={() => loadFixPrompt(true, idOf(member))}><Clipboard size={14} />Copy Fix Prompt</button><button className="button ghost compact" disabled={fixPromptLoading} onClick={() => loadFixPrompt(false, idOf(member))}><Code2 size={14} />Open Fix Prompt</button></div>}</article>) : !memberError && <Empty title="No member jobs" body="This is a single-agent review or member metadata is unavailable." />}</div>}
      {!loading && (tab === 'log' || tab === 'prompt') && <>{lazyError[tab] && <p role="alert" className="hint">{tab} could not be loaded: {lazyError[tab]} <button className="button ghost compact" onClick={() => setLazyLoaded(old => ({ ...old, [tab]: false }))}>Retry</button></p>}<div className="text-toolbar"><CopyButton value={lazy[tab] || ''} /></div><pre className="long-text">{lazy[tab] || `No ${tab} is available.`}</pre></>}
      {tab === 'comments' && <CommentBox jobId={id} mutate={mutate} busy={busy} />}
      {tab === 'metadata' && <><div className="text-toolbar"><CopyButton value={JSON.stringify(current, null, 2)} /></div><pre className="long-text">{JSON.stringify(current, null, 2)}</pre></>}
    </div>
    <footer className="detail-actions">
      {finding && <button className="button" disabled={fixPromptLoading} onClick={() => loadFixPrompt(true)}><Clipboard size={16} />Copy Fix Prompt</button>}
      {finding && <button className="button" disabled={fixPromptLoading} onClick={() => loadFixPrompt(false)}><Code2 size={16} />Open Fix Prompt</button>}
      {isRunning(current) && <button className="button danger" disabled={busy} onClick={() => mutate(`/api/reviews/${id}/cancel`)}><CircleStop size={16} />Cancel</button>}
      {terminal && !isClosed(current) && <button className="button" disabled={busy} onClick={() => mutate(`/api/reviews/${id}/close`)}><Check size={16} />Close</button>}
      {terminal && isClosed(current) && <button className="button" disabled={busy} onClick={() => mutate(`/api/reviews/${id}/reopen`)}><RotateCcw size={16} />Reopen</button>}
      {terminal && canRerun && <button className="button subtle rerun" disabled={busy} onClick={() => { if (confirm(`Rerun the same frozen target and resolved configuration?\n\nActual RoboRev transport:\nPOST /api/job/rerun\n{"job_id":${id}}\n\nThis does not review the current HEAD.`)) mutate(`/api/reviews/${id}/rerun`) }}><RefreshCw size={16} />Rerun same target/config</button>}
    </footer>
  </aside>{fixPrompt && <div className="prompt-overlay"><section className="prompt-dialog" role="dialog" aria-modal="true" aria-labelledby="fix-prompt-title"><header><div><span className="eyebrow">Finding · Review #{fixPrompt.jobId}</span><h2 id="fix-prompt-title">Fix Prompt</h2><p>{fixPrompt.stale ? 'Repository state changed after this review; verify the prompt before use.' : 'Generated from the selected review finding.'}</p></div><button className="icon-button" aria-label="Close fix prompt" onClick={() => setFixPrompt(null)}><X /></button></header><textarea aria-label="Fix prompt" value={fixPromptText} onChange={event => setFixPromptText(event.target.value)} rows={20} /><footer><button className="button" onClick={() => setFixPrompt(null)}>Close</button><button className="button primary" onClick={copyEditedFixPrompt}><Clipboard size={16} />Copy</button></footer></section></div>}</>
}

function Empty({ title, body }: { title: string; body: string }) { return <div className="empty"><Code2 /><h3>{title}</h3><p>{body}</p></div> }

function ReviewList({ jobs, selected, onSelect }: { jobs: ReviewJob[]; selected?: number; onSelect: (job: ReviewJob) => void }) {
  const [query, setQuery] = useState('')
  const filtered = jobs.filter(job => JSON.stringify([job.id, job.git_ref, job.subject, job.commit_subject, job.agent, job.panel_name, job.status, job.verdict, statusLabel(statusOf(job))]).toLowerCase().includes(query.toLowerCase()))
  return <section className="review-section">
    <header className="section-heading"><div><span className="eyebrow">Review operations</span><h1>Review queue</h1><p>Live RoboRev jobs for the selected repository. Heavy content loads on demand.</p></div><div className="search"><Search size={16} /><input aria-label="Search reviews" value={query} onChange={event => setQuery(event.target.value)} placeholder="Search jobs, agents, refs…" /></div></header>
    <div className="table-wrap"><table><thead><tr><th>Status</th><th>Target</th><th>Reviewer</th><th>Branch</th><th>Elapsed</th><th>State</th><th /></tr></thead><tbody>
      {filtered.map(job => <tr key={idOf(job)} className={selected === idOf(job) ? 'selected' : ''} onClick={() => onSelect(job)}><td><StatusPill job={job} /></td><td><strong>#{idOf(job)} · {job.commit_subject || job.subject || job.git_ref || job.commit_sha || 'Custom task'}</strong><small>{job.job_type || (job.panel_name ? 'Panel synthesis' : 'Review')}</small></td><td><span className="agent"><Bot size={14} />{job.panel_name || job.agent || 'default'}</span><small>{job.model || job.reasoning || ''}</small></td><td>{job.branch || '—'}</td><td>{elapsed(job)}</td><td><span className={isClosed(job) ? 'muted' : 'open-state'}>{isClosed(job) ? 'Closed' : 'Open'}</span></td><td><ChevronRight size={17} /></td></tr>)}
    </tbody></table>{!filtered.length && <Empty title="No reviews found" body={query ? 'Try a different filter.' : 'Create a new review of HEAD to start.'} />}</div>
  </section>
}

export function App() {
  const [bootstrap, setBootstrap] = useState<Bootstrap | null>(null)
  const [jobs, setJobs] = useState<ReviewJob[]>([])
  const [selected, setSelected] = useState<ReviewJob | null>(null)
  const [activeNav, setActiveNav] = useState('Reviews')
  const [busy, setBusy] = useState(false)
  const [loading, setLoading] = useState(true)
  const [initialized, setInitialized] = useState(false)
  const [menu, setMenu] = useState(false)
  const [toasts, setToasts] = useState<Toast[]>([])
  const [reviewPollError, setReviewPollError] = useState('')
  const [runtimePollError, setRuntimePollError] = useState('')
  const [loadedReviewIdentity, setLoadedReviewIdentity] = useState('')
  const [panelConfiguration, setPanelConfiguration] = useState<PanelConfiguration | null>(null)
  const [panelLoading, setPanelLoading] = useState(false)
  const [panelError, setPanelError] = useState('')
  const [panelLoadEpoch, setPanelLoadEpoch] = useState(0)
  const [repoSwitching, setRepoSwitching] = useState(false)
  const [panelRefineRun, setPanelRefineRun] = useState<PanelRefineRun | null>(null)
  const [panelRefineLoading, setPanelRefineLoading] = useState(false)
  const [panelRefineError, setPanelRefineError] = useState('')
  const [panelRefineOptions, setPanelRefineOptions] = useState<PanelRefineOptions | null>(null)
  const [panelRefineReloadEpoch, setPanelRefineReloadEpoch] = useState(0)
  const [setupState, setSetupState] = useState<SetupState | null>(null)
  const [setupError, setSetupError] = useState('')
  const [setupReloadEpoch, setSetupReloadEpoch] = useState(0)
  const [setupConfirming, setSetupConfirming] = useState(false)
  const [agents, setAgents] = useState<AgentsResponse | null>(null)
  const [agentsLoading, setAgentsLoading] = useState(false)
  const [agentsError, setAgentsError] = useState('')
  const [runtimeControl, setRuntimeControl] = useState<RuntimeControlState | null>(null)
  const [runtimeControlLoading, setRuntimeControlLoading] = useState(false)
  const [runtimeControlError, setRuntimeControlError] = useState('')
  const panelRefineGeneration = useRef(0)
  const reviewGeneration = useRef(0)
  const reviewActionGeneration = useRef(0)
  const runtimeGeneration = useRef(0)
  const panelGeneration = useRef(0)
  const repoSwitchGeneration = useRef(0)
  const panelRefineReviewGeneration = useRef(0)
  const setupGeneration = useRef(0)
  const setupReadyGeneration = useRef(0)
  const runtimeControlGeneration = useRef(0)
  const currentRepositoryIdentity = useRef('')
  const selectedRepositoryIdentity = bootstrap?.selectedRepositoryIdentity ?? ''
  const repo = bootstrap?.repositories.find(item => item.identity === selectedRepositoryIdentity)

  useEffect(() => {
    currentRepositoryIdentity.current = selectedRepositoryIdentity
    panelRefineReviewGeneration.current++
  }, [selectedRepositoryIdentity])

  const toast = useCallback((kind: Toast['kind'], message: string) => { const id = Date.now(); setToasts(old => [...old, { id, kind, message }]); setTimeout(() => setToasts(old => old.filter(item => item.id !== id)), 4500) }, [])
  const loadReviews = useCallback(async (repo?: string, identity?: string) => {
    if (!repo || !identity) return []
    const generation = ++reviewGeneration.current
    try {
      const next = jobsFromPayload(await request<unknown>(`/api/reviews?repo=${encodeURIComponent(repo)}`))
      if (generation === reviewGeneration.current) {
        setJobs(next)
        setSelected(current => current ? next.find(job => idOf(job) === idOf(current)) ?? current : current)
        setReviewPollError('')
        setLoadedReviewIdentity(identity)
      }
      return generation === reviewGeneration.current ? next : []
    } catch (error) {
      if (generation === reviewGeneration.current) {
        setReviewPollError((error as Error).message)
        setLoadedReviewIdentity(identity)
      }
      return []
    }
  }, [])
  const loadBootstrap = useCallback(async () => {
    const data = await request<Bootstrap>('/api/bootstrap'); setCSRF(data.csrfToken); setBootstrap(data)
    return data
  }, [])
  const loadSetup = useCallback(async (signal?: AbortSignal) => {
    const generation = ++setupGeneration.current
    try {
      const raw = await request<unknown>('/api/setup/roborev', signal ? { signal } : {})
      const next = normalizeSetupState(raw)
      if (!next) throw new APIError('The RoboRev setup response was malformed.')
      if (generation !== setupGeneration.current) return null
      setSetupState(next)
      setSetupError('')
      return next
    } catch (error) {
      if ((error as Error).name === 'AbortError' || generation !== setupGeneration.current) return null
      setSetupState(null)
      setSetupError((error as Error).message)
      return null
    }
  }, [])
  const loadAgents = useCallback(async () => {
    setAgentsLoading(true)
    try {
      const next = normalizeAgentsResponse(await request<unknown>('/api/agents'))
      if (!next) throw new APIError('The configured-agent response was malformed.')
      setAgents(next)
      setAgentsError('')
      return next
    } catch (error) {
      setAgentsError((error as Error).message)
      return null
    } finally { setAgentsLoading(false) }
  }, [])
  const loadRuntimeControl = useCallback(async () => {
    const generation = ++runtimeControlGeneration.current
    setRuntimeControlLoading(true)
    try {
      const next = normalizeRuntimeControl(await request<unknown>('/api/runtime-control'))
      if (!next) throw new APIError('The runtime-control response was malformed.')
      if (generation === runtimeControlGeneration.current) {
        setRuntimeControl(next)
        setRuntimeControlError(next.error || '')
      }
      return next
    } catch (error) {
      if (generation === runtimeControlGeneration.current) setRuntimeControlError((error as Error).message)
      return null
    } finally {
      if (generation === runtimeControlGeneration.current) setRuntimeControlLoading(false)
    }
  }, [])
  const loadPanels = useCallback(async (repoPath: string, identity: string) => {
    const generation = ++panelGeneration.current
    setPanelLoading(true)
    setPanelError('')
    try {
      const configuration = await request<PanelConfiguration>(`/api/repos/panels?repo=${encodeURIComponent(repoPath)}`)
      if (generation !== panelGeneration.current) return
      if (!validPanelConfiguration(configuration, identity)) throw new APIError('The panel configuration response was malformed.')
      setPanelConfiguration(configuration)
      setPanelError(configuration.error || '')
    } catch (error) {
      if (generation !== panelGeneration.current) return
      setPanelConfiguration(null)
      setPanelError((error as Error).message)
    } finally {
      if (generation === panelGeneration.current) setPanelLoading(false)
    }
  }, [])
  const refreshDashboardAfterSetup = useCallback(async () => {
    const generation = ++setupReadyGeneration.current
    const targetIdentity = currentRepositoryIdentity.current
    try {
      const data = await loadBootstrap()
      if (generation !== setupReadyGeneration.current || (targetIdentity && data.selectedRepositoryIdentity !== targetIdentity)) return
      const selected = data.repositories.find(item => item.identity === data.selectedRepositoryIdentity)
      if (selected) {
        setPanelLoadEpoch(epoch => epoch + 1)
        await Promise.all([loadPanels(selected.path, selected.identity), loadReviews(selected.path, selected.identity)])
      }
      try {
        const runtime = await request<Installation>('/api/runtime')
        if (generation === setupReadyGeneration.current && (!targetIdentity || currentRepositoryIdentity.current === targetIdentity) && runtime && typeof runtime === 'object' && typeof runtime.installed === 'boolean') setBootstrap(current => current ? { ...current, roborev: runtime } : current)
      } catch { /* setup state remains authoritative when runtime is temporarily unavailable */ }
    } catch { /* the regular bootstrap/runtime poll will surface a typed error */ }
  }, [loadBootstrap, loadPanels, loadReviews])

  const loadPanelRefine = useCallback(async (repoPath: string, identity: string) => {
    const generation = ++panelRefineGeneration.current
    setPanelRefineLoading(true)
    try {
      const raw = await request<unknown>(`/api/panel-refines?repo=${encodeURIComponent(repoPath)}`)
      if (!raw || typeof raw !== 'object' || Array.isArray(raw) || !Array.isArray((raw as Record<string, unknown>).runs)) {
        if (generation === panelRefineGeneration.current) setPanelRefineError('The Panel Refine response was malformed.')
        return { run: null, ok: false }
      }
      const envelope = raw as { runs: unknown[] }
      const payload = normalizePanelRefineResponse(envelope)
      if (envelope.runs.length > 0 && payload.runs.length === 0) throw new APIError('The Panel Refine response contained no valid runs.')
      if (generation !== panelRefineGeneration.current) return { run: null, ok: false }
      const next = payload.run && payload.run.repositoryIdentity === identity ? payload.run : null
      if (payload.run && !next) throw new APIError('The Panel Refine response belonged to a different repository.')
      setPanelRefineRun(next)
      setPanelRefineOptions(payload.options)
      setPanelRefineError('')
      return { run: next, ok: true }
    } catch (error) {
      if (generation !== panelRefineGeneration.current) return null
      setPanelRefineError((error as Error).message)
      return { run: null, ok: false }
    } finally {
      if (generation === panelRefineGeneration.current) setPanelRefineLoading(false)
    }
  }, [])

  useEffect(() => {
    loadBootstrap()
      .catch(error => toast('error', (error as Error).message))
      .finally(() => { setInitialized(true); setLoading(false) })
  }, [loadBootstrap, toast])
  useEffect(() => {
    if (!initialized) return
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    let controller: AbortController | undefined
    const poll = async () => {
      controller?.abort()
      controller = new AbortController()
      const next = await loadSetup(controller.signal)
      if (cancelled) return
      if (setupIsActive(next)) timer = setTimeout(poll, 1000)
    }
    void poll()
    return () => {
      cancelled = true
      setupGeneration.current++
      controller?.abort()
      if (timer) clearTimeout(timer)
    }
  }, [initialized, loadSetup, setupReloadEpoch])
  useEffect(() => {
    if (!initialized || activeNav !== 'Agents & Setup') return
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    const poll = async () => {
      const [runtime] = await Promise.all([loadRuntimeControl(), loadAgents()])
      if (cancelled) return
      // Runtime-control operations are asynchronous. Poll only for the
      // bounded transition; idle setup pages do not generate background GETs.
      if (runtime?.operationActive) timer = setTimeout(poll, 800)
    }
    void poll()
    return () => { cancelled = true; runtimeControlGeneration.current++; if (timer) clearTimeout(timer) }
  }, [activeNav, initialized, loadAgents, loadRuntimeControl])
  const setupPhase = setupState?.phase
  const previousSetupPhase = useRef<SetupState['phase'] | undefined>(undefined)
  useEffect(() => {
    if (setupPhase === 'ready' && previousSetupPhase.current !== 'ready') void refreshDashboardAfterSetup()
    previousSetupPhase.current = setupPhase
  }, [refreshDashboardAfterSetup, setupPhase])
  useEffect(() => () => {
    setupGeneration.current++
    setupReadyGeneration.current++
  }, [])
  useEffect(() => () => { reviewActionGeneration.current++; repoSwitchGeneration.current++; panelRefineReviewGeneration.current++ }, [])
  useEffect(() => {
    panelGeneration.current++
    if (!initialized || !repo) {
      setPanelConfiguration(null)
      setPanelLoading(false)
      setPanelError('')
      return
    }
    void loadPanels(repo.path, repo.identity)
    return () => { panelGeneration.current++ }
  }, [initialized, loadPanels, panelLoadEpoch, repo?.identity, repo?.path])
  useEffect(() => {
    panelRefineGeneration.current++
    if (!initialized || !repo) {
      setPanelRefineRun(null)
      setPanelRefineOptions(null)
      setPanelRefineLoading(false)
      setPanelRefineError('')
      return
    }
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    let observedActive = isPanelRefineActive(panelRefineRun)
    let observedPhase = panelRefineRun?.phase
    let observedHead = panelRefineRun?.currentHead
    let retryDelay = 2_000
    const poll = async () => {
      const loaded = await loadPanelRefine(repo.path, repo.identity)
      if (cancelled) return
      if (!loaded?.ok) {
        timer = setTimeout(poll, retryDelay)
        retryDelay = Math.min(30_000, retryDelay * 2)
        return
      }
      retryDelay = 2_000
      const next = loaded.run
      const changed = Boolean(next && (next.phase !== observedPhase || next.currentHead !== observedHead))
      if (next && (changed || !isPanelRefineActive(next)) && (observedActive || next.phase === 'passed' || next.phase === 'needs_attention' || next.phase === 'failed' || next.phase === 'cancelled')) {
        void request<Bootstrap>('/api/bootstrap').then(data => {
          if (cancelled || data.selectedRepositoryIdentity !== repo.identity) return
          setCSRF(data.csrfToken)
          setBootstrap(current => current && current.selectedRepositoryIdentity === repo.identity ? data : current)
        }).catch(() => undefined)
      }
      if (next) { observedPhase = next.phase; observedHead = next.currentHead; observedActive = isPanelRefineActive(next) }
      if (!cancelled && (next ? isPanelRefineActive(next) : observedActive)) timer = setTimeout(poll, 4000)
    }
    void poll()
    return () => { cancelled = true; panelRefineGeneration.current++; if (timer) clearTimeout(timer) }
  // panelRefineRun is intentionally read only as the effect's pre-poll active snapshot.
  // Reloads are driven by repo identity and explicit start/cancel mutations.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initialized, panelRefineReloadEpoch, repo?.identity, repo?.path, loadPanelRefine])
  const canPollReviews = Boolean(bootstrap?.roborev?.installed && bootstrap.roborev?.capabilities?.jobListApi && bootstrap.roborev?.status?.running)
  useEffect(() => {
    if (!initialized) return
    reviewGeneration.current++
    if (!canPollReviews) {
      setJobs([])
      setReviewPollError('')
      setLoadedReviewIdentity('')
      return
    }
    const repoPath = bootstrap?.selectedRepository
    const repoIdentity = bootstrap?.selectedRepositoryIdentity
    if (!repoPath || !repoIdentity) return
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    const poll = async () => {
      await loadReviews(repoPath, repoIdentity)
      if (!cancelled) timer = setTimeout(poll, 4000)
    }
    void poll()
    return () => {
      cancelled = true
      reviewGeneration.current++
      if (timer) clearTimeout(timer)
    }
  }, [bootstrap?.selectedRepository, bootstrap?.selectedRepositoryIdentity, canPollReviews, initialized, loadReviews])
  useEffect(() => {
    if (!initialized) return
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    const poll = async () => {
      const generation = ++runtimeGeneration.current
      try {
        const runtime = await request<Installation>('/api/runtime')
        if (!cancelled && generation === runtimeGeneration.current) {
          setBootstrap(current => current ? { ...current, roborev: runtime } : current)
          setRuntimePollError('')
        }
      } catch (error) {
        if (!cancelled && generation === runtimeGeneration.current) setRuntimePollError((error as Error).message)
      } finally {
        if (!cancelled) timer = setTimeout(poll, 10_000)
      }
    }
    timer = setTimeout(poll, 10_000)
    return () => {
      cancelled = true
      runtimeGeneration.current++
      if (timer) clearTimeout(timer)
    }
  }, [initialized])

  const mutate = useCallback(async (path: string, body?: unknown) => {
    setBusy(true)
    try {
      await request(path, { method: 'POST', body: body === undefined ? undefined : JSON.stringify(body) })
      toast('success', path.includes('comment-close') ? 'Comment saved and review closed.' : 'RoboRev state updated.')
      const data = await loadBootstrap()
      await loadReviews(data.selectedRepository, data.selectedRepositoryIdentity)
    } catch (error) {
      const apiError = error as APIError
      toast('error', apiError.phase === 'close' ? `Partial success: ${apiError.message}` : apiError.message)
      throw error
    } finally { setBusy(false) }
  }, [loadBootstrap, loadReviews, toast])
  const setupMutation = useCallback(async (path: string) => {
    await mutate(path)
    setSetupReloadEpoch(epoch => epoch + 1)
  }, [mutate])
  const refreshRuntimeSurfaces = useCallback(async () => {
    const data = await loadBootstrap().catch(() => null)
    await Promise.allSettled([
      loadSetup(),
      loadAgents(),
      loadRuntimeControl(),
      request<Installation>('/api/runtime').then(runtime => {
        if (runtime && typeof runtime === 'object' && typeof runtime.installed === 'boolean') setBootstrap(current => current ? { ...current, roborev: runtime } : current)
      }),
    ])
    const target = data ?? bootstrap
    const targetRepo = target?.repositories.find(item => item.identity === target.selectedRepositoryIdentity)
    if (targetRepo) await Promise.allSettled([loadPanels(targetRepo.path, targetRepo.identity), loadReviews(targetRepo.path, targetRepo.identity)])
  }, [bootstrap, loadAgents, loadBootstrap, loadPanels, loadReviews, loadRuntimeControl, loadSetup])
  const pollRuntimeOperation = useCallback(async () => {
    let latest: RuntimeControlState | null = null
    // Daemon operations can include process teardown/startup on Windows. Keep
    // polling bounded just beyond RoboRev's two-minute operation ceiling.
    for (let attempt = 0; attempt < 125; attempt += 1) {
      latest = await loadRuntimeControl()
      if (latest?.error) throw new APIError(latest.error)
      if (latest && !latest.operationActive) return latest
      await new Promise(resolve => setTimeout(resolve, 1000))
    }
    throw new APIError('The RoboRev daemon operation did not reach a terminal state before the timeout.')
  }, [loadRuntimeControl])
  const runDaemonAction: DaemonAction = useCallback(async action => {
    setBusy(true)
    try {
      await request(`/api/daemon/${action}`, { method: 'POST' })
      await pollRuntimeOperation()
      await refreshRuntimeSurfaces()
      toast('success', action === 'start' ? 'RoboRev daemon started.' : action === 'stop' ? 'RoboRev daemon stopped.' : 'RoboRev daemon restarted.')
    } catch (error) {
      // A 202 transition can temporarily make bootstrap/review reads
      // unavailable. The operation itself is the authoritative result; only
      // a failed command request is surfaced as an error toast.
      toast('error', (error as Error).message)
      throw error
    } finally { setBusy(false) }
  }, [pollRuntimeOperation, refreshRuntimeSurfaces, toast])
  const runQueueAction = useCallback(async (action: 'pause' | 'resume') => {
    setBusy(true)
    try {
      await request(`/api/runtime-control/queue/${action}`, { method: 'POST' })
      await pollRuntimeOperation()
      await Promise.allSettled([loadRuntimeControl(), loadReviews(bootstrap?.selectedRepository, bootstrap?.selectedRepositoryIdentity)])
      toast('success', action === 'pause' ? 'Review queue paused.' : 'Review queue resumed.')
    } catch (error) {
      toast('error', (error as Error).message)
      throw error
    } finally { setBusy(false) }
  }, [bootstrap?.selectedRepository, bootstrap?.selectedRepositoryIdentity, loadReviews, loadRuntimeControl, pollRuntimeOperation, toast])
  const runAgentCheck = useCallback(async (agent: string) => {
    setBusy(true)
    try {
      await request('/api/agents/check', { method: 'POST', body: JSON.stringify({ agent }) })
      await loadAgents()
      toast('success', `Agent check completed for ${agent}.`)
    } catch (error) {
      toast('error', (error as Error).message)
      throw error
    } finally { setBusy(false) }
  }, [loadAgents, toast])

  const runningCount = jobs.filter(isRunning).length
  const daemonRunning = Boolean(bootstrap?.roborev?.status?.running)
  const selectedPanel = panelConfiguration?.selectedPanel ?? ''
  const selectedPanelInfo = panelConfiguration?.panels.find(panel => panel.name === selectedPanel)
  const panelSelectionValue = selectedPanelInfo?.valid ? selectedPanel : ''
  const panelReady = Boolean(repo && !panelLoading && !panelError && panelConfiguration?.repositoryIdentity === repo.identity && !panelConfiguration.selectionRequired && selectedPanelInfo?.valid)
  const panelMode = selectedPanelInfo ? panelLabel(selectedPanelInfo) : panelLoading ? 'Loading review panels…' : panelError ? 'Panel unavailable' : 'Select a review panel'
  const selectPanel = async (panel: string) => {
    if (!repo) return
    const generation = ++panelGeneration.current
    setPanelLoading(true)
    setPanelError('')
    try {
      await request('/api/repos/panel-selection', { method: 'POST', body: JSON.stringify({ repoPath: repo.path, panel }) })
      if (generation !== panelGeneration.current) return
      await loadPanels(repo.path, repo.identity)
    } catch (error) {
      if (generation === panelGeneration.current) {
        const message = (error as Error).message
        setPanelError(message)
        setPanelLoading(false)
        toast('error', message)
      }
    }
  }
  const reviewHead = async () => {
    if (!repo || !panelReady || !selectedPanel) return
    const generation = ++reviewActionGeneration.current
    setBusy(true)
    try {
      const result = await request<ReviewEnqueueResponse>('/api/reviews', { method: 'POST', body: JSON.stringify({ repoPath: repo.path, panel: selectedPanel }) })
      if (generation !== reviewActionGeneration.current) return
      const target = result?.target
      if (!target?.identity || !target.path || !target.headShort) throw new APIError('Review was enqueued, but its pinned target metadata was missing.')
      setBootstrap(current => {
        if (!current) return current
        const exists = current.repositories.some(item => item.identity === target.identity)
        return {
          ...current,
          selectedRepository: target.path,
          selectedRepositoryIdentity: target.identity,
          repositories: exists ? current.repositories.map(item => item.identity === target.identity ? target : item) : [...current.repositories, target],
        }
      })
      toast('success', `New HEAD review enqueued for ${target.headShort}.`)
      await loadReviews(target.path, target.identity)
    } catch (error) {
      if (generation === reviewActionGeneration.current) toast('error', (error as Error).message)
    } finally {
      if (generation === reviewActionGeneration.current) setBusy(false)
    }
  }
  const startPanelRefine = async (body: { repoPath: string; panel: string; fixAgent: 'codex'; maxFixAttempts: number; finalPanel?: string }) => {
    const targetIdentity = repo?.identity
    if (!repo || !targetIdentity) return
    setBusy(true)
    try {
      await request('/api/panel-refines', { method: 'POST', body: JSON.stringify(body) })
      toast('success', 'Panel Refine started.')
      setPanelRefineReloadEpoch(epoch => epoch + 1)
    } catch (error) {
      toast('error', (error as Error).message)
      throw error
    } finally { setBusy(false) }
  }
  const cancelPanelRefine = async (id: number | string) => {
    setBusy(true)
    try {
      await request(`/api/panel-refines/${encodeURIComponent(id)}/cancel`, { method: 'POST' })
      toast('success', 'Panel Refine cancellation requested.')
      setPanelRefineReloadEpoch(epoch => epoch + 1)
    } catch (error) {
      toast('error', (error as Error).message)
      throw error
    } finally { setBusy(false) }
  }
  const openPanelRefineReview = (id: number) => {
    const existing = jobs.find(job => idOf(job) === id)
    if (existing) { setSelected(existing); return }
    const identity = selectedRepositoryIdentity
    const generation = ++panelRefineReviewGeneration.current
    void request<ReviewJob>(`/api/reviews/${encodeURIComponent(id)}`).then(value => {
      if (generation === panelRefineReviewGeneration.current && identity === currentRepositoryIdentity.current) setSelected({ ...value, id, job_id: id })
    }).catch(error => { if (generation === panelRefineReviewGeneration.current && identity === currentRepositoryIdentity.current) toast('error', (error as Error).message) })
  }
  const switchRepo = async (identity: string) => {
    const target = bootstrap?.repositories.find(item => item.identity === identity)
    if (!target) return
    const generation = ++repoSwitchGeneration.current
    setupReadyGeneration.current++
    setRepoSwitching(true)
    reviewActionGeneration.current++
    panelRefineReviewGeneration.current++
    reviewGeneration.current++
    panelGeneration.current++
    panelRefineGeneration.current++
    setReviewPollError('')
    setLoadedReviewIdentity('')
    setPanelConfiguration(null)
    setPanelError('')
    setPanelRefineRun(null)
    setPanelRefineOptions(null)
    setPanelRefineError('')
    setPanelRefineLoading(true)
    setPanelLoading(true)
    setJobs([])
    setSelected(null)
    let selectError: Error | null = null
    try {
      try {
        await request('/api/repos/select', { method: 'POST', body: JSON.stringify({ repoPath: target.path }) })
      } catch (error) {
        if (generation !== repoSwitchGeneration.current) return
        selectError = error as Error
      }
      const data = await request<Bootstrap>('/api/bootstrap')
      if (generation !== repoSwitchGeneration.current) return
      setCSRF(data.csrfToken)
      setBootstrap(data)
      const restored = data.repositories.find(item => item.identity === data.selectedRepositoryIdentity)
      if (!restored) throw new APIError('The selected repository metadata was missing from bootstrap.')
      setPanelLoadEpoch(epoch => epoch + 1)
      if (selectError) toast('error', selectError.message)
      else toast('success', 'RoboRev state updated.')
      await loadReviews(data.selectedRepository, data.selectedRepositoryIdentity)
    } catch (error) {
      if (generation !== repoSwitchGeneration.current) return
      toast('error', selectError?.message ?? (error as Error).message)
      setPanelError(`Repository state could not be restored: ${(error as Error).message}`)
      setPanelLoading(false)
    } finally {
      if (generation === repoSwitchGeneration.current) setRepoSwitching(false)
    }
  }

  const reviewSupported = Boolean(bootstrap?.roborev?.capabilities?.reviewEnqueueApi)
  const panelRefineActive = isPanelRefineActive(panelRefineRun)
  const setupOperationActive = setupIsActive(setupState)
  const panelOutputStatusAvailable = bootstrap?.roborev?.capabilities?.jobOutputStatusApi === true
  const panelRefineAvailable = Boolean(bootstrap?.roborev?.installed && daemonRunning && bootstrap.roborev.capabilities.panelReview && bootstrap.roborev.capabilities.fixCommand && panelOutputStatusAvailable)
  const panelRefineDisabledReason = !bootstrap?.roborev?.installed ? 'RoboRev is not installed.' : !daemonRunning ? 'The RoboRev daemon is not running.' : !bootstrap.roborev.capabilities.panelReview ? 'This RoboRev version does not support panel review.' : !bootstrap.roborev.capabilities.fixCommand ? 'This RoboRev version does not support the typed fix command.' : !panelOutputStatusAvailable ? 'This RoboRev version does not support the bounded panel status contract.' : ''
  const reviewsReady = !canPollReviews || !selectedRepositoryIdentity || loadedReviewIdentity === selectedRepositoryIdentity
  const reviewPageActive = activeNav === 'Reviews' || activeNav === 'Overview'
  return <div className="shell">
    <aside className={`sidebar ${menu ? 'open' : ''}`}><div className="brand"><span className="brand-mark"><Bot /></span><div><strong>RoboRev</strong><span>Dashboard</span></div></div><nav>{navigation.map(([label, Icon]) => <button key={label} className={activeNav === label ? 'active' : ''} onClick={() => { setActiveNav(label); setMenu(false) }}><Icon />{label}{!['Overview', 'Reviews', 'Repositories', 'Agents & Setup'].includes(label) && <small>Soon</small>}</button>)}</nav><div className="sidebar-foot"><span className={`health ${daemonRunning ? 'online' : ''}`} /><div><strong>{daemonRunning ? 'Daemon online' : 'Daemon offline'}</strong><span>{bootstrap?.roborev?.version || 'Setup required'}</span></div></div></aside>
    <main className="main"><header className="topbar"><button className="icon-button mobile-menu" onClick={() => setMenu(old => !old)}><Menu /></button><div className="repo-switch"><span>Repository</span><select aria-label="Current repository" value={selectedRepositoryIdentity} disabled={repoSwitching || setupOperationActive} onChange={event => switchRepo(event.target.value)}>{bootstrap?.repositories.map(item => <option value={item.identity} key={item.identity}>{item.name} · {item.branch}</option>)}</select></div>{repo && <div className="repo-meta"><span><GitBranch size={15} />{repo.branch}</span><span><GitCommit size={15} />{repo.headShort}</span>{repo.dirty && <span className="dirty"><AlertTriangle size={14} />Dirty</span>}</div>}<div className="top-spacer" /><div className="panel-control"><span>Review mode</span><select aria-label="Review panel" value={panelSelectionValue} disabled={busy || setupOperationActive || repoSwitching || panelLoading || Boolean(panelError) || !repo} onChange={event => selectPanel(event.target.value)}>{!panelSelectionValue && <option value="">Select a review panel</option>}{panelConfiguration?.panels.map(panel => <option key={panel.name} value={panel.name} disabled={!panel.valid}>{panelLabel(panel)}</option>)}</select><small>{panelMode}</small></div><span className="running"><Activity size={15} />{runningCount} running</span><button className="button primary review-head" disabled={busy || setupOperationActive || repoSwitching || panelRefineActive || panelRefineLoading || !reviewsReady || !panelReady || !repo || !bootstrap?.roborev?.installed || !daemonRunning || !reviewSupported} onClick={reviewHead}><Play size={17} />Review HEAD</button></header>
      {!loading && bootstrap && setupState && activeNav !== 'Agents & Setup' && <SetupBanner state={setupState} busy={busy || setupOperationActive} onInstall={() => { setSetupConfirming(true); return Promise.resolve() }} onRetry={() => setupMutation('/api/setup/roborev/retry')} onStartDaemon={() => runDaemonAction('start')} />}
      {!loading && bootstrap && !setupState && (!bootstrap.roborev?.installed || !daemonRunning || !reviewSupported) && <section className="setup-banner"><Server /><div><strong>{!bootstrap.roborev?.installed ? 'RoboRev is not installed' : !reviewSupported ? 'RoboRev upgrade required' : 'RoboRev daemon is stopped'}</strong><p>{setupError || bootstrap.roborev?.error || (!reviewSupported ? 'This version does not expose the safe metadata and enqueue contracts required by the Dashboard.' : bootstrap.roborev?.installed ? 'Start the daemon to enqueue and view reviews.' : 'Configure a RoboRev binary to continue.')}</p></div>{bootstrap.roborev?.installed && reviewSupported && !daemonRunning && <button className="button" disabled={busy || setupOperationActive} onClick={() => runDaemonAction('start')}>Start daemon</button>}</section>}
      {panelError && <section className="setup-banner" role="alert"><AlertTriangle /><div><strong>Panel configuration unavailable</strong><p>{panelError}</p></div>{repo && <button className="button" disabled={panelLoading} onClick={() => loadPanels(repo.path, repo.identity)}>Retry panel discovery</button>}</section>}
      {(reviewPollError || runtimePollError) && <section className="setup-banner" role="alert"><AlertTriangle /><div><strong>Background refresh issue</strong><p>{reviewPollError || runtimePollError}</p></div></section>}
      {loading || (reviewPageActive && !reviewsReady) ? <div className="page-loading"><div className="skeleton" /><div className="skeleton tall" /></div> : reviewPageActive ? <><PanelRefine repoPath={repo?.path ?? ''} panelConfiguration={panelConfiguration} options={panelRefineOptions} run={panelRefineRun} available={panelRefineAvailable} disabledReason={panelRefineDisabledReason} loading={panelRefineLoading} error={panelRefineError} busy={busy || setupOperationActive || repoSwitching} onStart={startPanelRefine} onCancel={cancelPanelRefine} onOpenReview={openPanelRefineReview} /><ReviewList jobs={jobs} selected={selected ? idOf(selected) : undefined} onSelect={setSelected} /></> : activeNav === 'Repositories' ? <Repositories bootstrap={bootstrap} mutate={mutate} busy={busy || setupOperationActive} /> : activeNav === 'Agents & Setup' ? <Setup state={setupState} busy={busy || setupOperationActive} onInstall={() => setupMutation('/api/setup/roborev/install')} onRetry={() => setupMutation('/api/setup/roborev/retry')} onStartDaemon={() => runDaemonAction('start')} agents={agents} agentsLoading={agentsLoading} agentsError={agentsError} onRefreshAgents={async () => { await loadAgents() }} onCheckAgent={runAgentCheck} runtimeControl={runtimeControl} runtimeLoading={runtimeControlLoading} runtimeError={runtimeControlError} onRefreshRuntime={async () => { await loadRuntimeControl() }} onDaemonAction={runDaemonAction} onQueueAction={runQueueAction} panelRefineActive={panelRefineActive || panelRefineLoading} /> : <section className="coming"><TerminalSquare /><span className="eyebrow">Checkpoint roadmap</span><h1>{activeNav}</h1><p>This typed workflow is tracked in ROBorev-COVERAGE.md and will be added without a generic shell endpoint.</p></section>}
    </main>
    {selected && <ReviewDetail key={idOf(selected)} job={selected} jobs={jobs} close={() => setSelected(null)} mutate={mutate} busy={busy || setupOperationActive || !canPollReviews} canRerun={Boolean(bootstrap?.roborev?.capabilities?.frozenRerunApi) && !setupOperationActive && !panelRefineActive && !panelRefineLoading} canLoadMembers={Boolean(bootstrap?.roborev?.capabilities?.panelMembersApi && selected.panel_run_uuid)} repoPath={repo?.path ?? ''} repoIdentity={repo?.identity ?? ''} toast={toast} />}
    {setupConfirming && setupState && !setupState.installed && !setupOperationActive && <InstallConfirmation state={setupState} busy={busy} onCancel={() => setSetupConfirming(false)} onInstall={() => { setSetupConfirming(false); void setupMutation('/api/setup/roborev/install') }} />}
    <div className="toasts" aria-live="polite">{toasts.map(item => <div key={item.id} className={`toast ${item.kind}`}>{item.kind === 'success' ? <Check /> : <XCircle />}{item.message}</div>)}</div>
  </div>
}

function Repositories({ bootstrap, mutate, busy }: { bootstrap: Bootstrap | null; mutate: (path: string, body?: unknown) => Promise<void>; busy: boolean }) {
  const [path, setPath] = useState('')
  return <section className="cards-page"><header className="section-heading"><div><span className="eyebrow">Registered targets</span><h1>Repositories</h1><p>Only canonical Git roots registered here can be mutation targets.</p></div></header><div className="repo-cards">{bootstrap?.repositories.map(repo => <article key={repo.identity}><div className="repo-icon"><GitBranch /></div><div><h3>{repo.name}</h3><code>{repo.path}</code><p>{repo.branch} · {repo.headShort} {repo.dirty ? '· dirty' : '· clean'}</p></div></article>)}</div><form className="add-repo" onSubmit={event => { event.preventDefault(); mutate('/api/repos', { repoPath: path }).then(() => setPath('')) }}><label htmlFor="repo-path">Add local Git repository</label><div><input id="repo-path" value={path} onChange={event => setPath(event.target.value)} placeholder="E:\Dev\GitHub\project" /><button className="button primary" disabled={busy || !path.trim()}><Plus size={16} />Register</button></div></form></section>
}
