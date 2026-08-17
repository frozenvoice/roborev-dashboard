import { Component, type ErrorInfo, type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Activity, AlertTriangle, Bot, Check, ChevronRight, CircleStop, Clipboard, Clock3, Code2, GitBranch, GitCommit, LayoutDashboard, ListChecks, Menu, MessageSquareText, Play, Plus, RefreshCw, RotateCcw, Search, Server, Settings, ShieldCheck, SlidersHorizontal, TerminalSquare, X, XCircle } from 'lucide-react'
import { APIError, type Bootstrap, type Installation, type Repository, type ReviewJob, jobsFromPayload, request, setCSRF } from './api'
import { Markdown } from './Markdown'

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
function isClosed(job: ReviewJob) { return Boolean(job.addressed || job.closed) }
function elapsed(job: ReviewJob) {
  if (job.duration_ms) return `${(job.duration_ms / 1000).toFixed(1)}s`
  const start = job.started_at || job.created_at || job.enqueued_at
  if (!start) return '—'
  const finished = job.finished_at || job.completed_at
  const end = finished ? Date.parse(finished) : Date.now()
  const seconds = Math.max(0, Math.floor((end - Date.parse(start)) / 1000))
  return seconds < 60 ? `${seconds}s` : `${Math.floor(seconds / 60)}m ${seconds % 60}s`
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

function ReviewDetail({ job, jobs, close, mutate, busy, canRerun, canLoadMembers }: { job: ReviewJob; jobs: ReviewJob[]; close: () => void; mutate: (path: string, body?: unknown) => Promise<void>; busy: boolean; canRerun: boolean; canLoadMembers: boolean }) {
  const [tab, setTab] = useState<Tab>('review')
  const [detail, setDetail] = useState<ReviewJob | null>(null)
  const [lazy, setLazy] = useState<Record<string, string>>({})
  const [memberJobs, setMemberJobs] = useState<ReviewJob[]>([])
  const [memberError, setMemberError] = useState('')
  const [loading, setLoading] = useState(false)
  const id = idOf(job)
  const members = useMemo(() => memberJobs.length ? memberJobs : jobs.filter(candidate => candidate.parent_job_id === id || (job.member_job_ids ?? []).includes(idOf(candidate))), [id, job.member_job_ids, jobs, memberJobs])

  useEffect(() => { setDetail(null); setLazy({}); setMemberJobs([]); setMemberError(''); setTab('review'); setLoading(true); request<ReviewJob>(`/api/reviews/${id}`).then(value => setDetail({ ...job, ...value, id, job_id: id })).catch(() => setDetail(job)).finally(() => setLoading(false)) }, [id])
  useEffect(() => {
    if (!['prompt', 'log'].includes(tab) || lazy[tab]) return
    setLoading(true)
    request<{ text: string }>(`/api/reviews/${id}/${tab}`).then(value => setLazy(old => ({ ...old, [tab]: value.text }))).finally(() => setLoading(false))
  }, [id, lazy, tab])
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

  const current = detail ?? job
  const text = outputOf(current)
  const terminal = !isRunning(current)
  return <aside className="detail-panel" aria-label="Review detail">
    <header className="detail-header"><div><span className="eyebrow">Review #{id}</span><h2>{current.commit_subject || current.subject || current.git_ref || current.commit_sha || 'Review detail'}</h2></div><button className="icon-button" aria-label="Close detail" onClick={close}><X /></button></header>
    <div className="detail-summary"><StatusPill job={current} /><span><Bot size={14} />{current.panel_name || current.agent || 'default'}</span><span><Clock3 size={14} />{elapsed(current)}</span></div>
    <nav className="tabs" aria-label="Review detail tabs">
      {(['review', ...(canLoadMembers ? ['members' as const] : []), 'log', 'prompt', 'comments', 'metadata'] as Tab[]).map(item => <button key={item} className={tab === item ? 'active' : ''} onClick={() => setTab(item)}>{item === 'members' ? 'Panel members' : item[0].toUpperCase() + item.slice(1)}</button>)}
    </nav>
    <div className="detail-content">
      {loading && <div className="skeleton tall" />}
      {!loading && tab === 'review' && <><div className="text-toolbar"><CopyButton value={text} /><span>{text.length.toLocaleString()} chars</span></div>{text ? <Markdown>{text}</Markdown> : <Empty title="No review output yet" body="Running reviews update automatically." />}</>}
      {!loading && tab === 'members' && <div className="member-list">{memberError && <p role="alert" className="hint">Panel members could not be refreshed: {memberError}</p>}{members.length ? members.map(member => <article key={idOf(member)}><StatusPill job={member} /><strong>{member.agent || `Member #${idOf(member)}`}</strong><span>{member.model || 'default model'}</span><span>{elapsed(member)}</span></article>) : !memberError && <Empty title="No member jobs" body="This is a single-agent review or member metadata is unavailable." />}</div>}
      {!loading && (tab === 'log' || tab === 'prompt') && <><div className="text-toolbar"><CopyButton value={lazy[tab] || ''} /></div><pre className="long-text">{lazy[tab] || `No ${tab} is available.`}</pre></>}
      {tab === 'comments' && <CommentBox jobId={id} mutate={mutate} busy={busy} />}
      {tab === 'metadata' && <><div className="text-toolbar"><CopyButton value={JSON.stringify(current, null, 2)} /></div><pre className="long-text">{JSON.stringify(current, null, 2)}</pre></>}
    </div>
    <footer className="detail-actions">
      {isRunning(current) && <button className="button danger" disabled={busy} onClick={() => mutate(`/api/reviews/${id}/cancel`)}><CircleStop size={16} />Cancel</button>}
      {terminal && !isClosed(current) && <button className="button" disabled={busy} onClick={() => mutate(`/api/reviews/${id}/close`)}><Check size={16} />Close</button>}
      {terminal && isClosed(current) && <button className="button" disabled={busy} onClick={() => mutate(`/api/reviews/${id}/reopen`)}><RotateCcw size={16} />Reopen</button>}
      {terminal && canRerun && <button className="button subtle rerun" disabled={busy} onClick={() => { if (confirm(`Rerun the same frozen target and resolved configuration?\n\nActual RoboRev transport:\nPOST /api/job/rerun\n{"job_id":${id}}\n\nThis does not review the current HEAD.`)) mutate(`/api/reviews/${id}/rerun`) }}><RefreshCw size={16} />Rerun same target/config</button>}
    </footer>
  </aside>
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
  const [menu, setMenu] = useState(false)
  const [toasts, setToasts] = useState<Toast[]>([])
	const reviewGeneration = useRef(0)

  const toast = useCallback((kind: Toast['kind'], message: string) => { const id = Date.now(); setToasts(old => [...old, { id, kind, message }]); setTimeout(() => setToasts(old => old.filter(item => item.id !== id)), 4500) }, [])
  const loadReviews = useCallback(async (repo?: string) => {
		if (!repo) return []
		const generation = ++reviewGeneration.current
		try {
			const next = jobsFromPayload(await request<unknown>(`/api/reviews?repo=${encodeURIComponent(repo)}`))
      if (generation === reviewGeneration.current) {
        setJobs(next)
        setSelected(current => current ? next.find(job => idOf(job) === idOf(current)) ?? current : current)
      }
			return generation === reviewGeneration.current ? next : []
		} catch (error) {
			if (!(error instanceof APIError && error.message.includes('daemon not running'))) toast('error', (error as Error).message)
			return []
		}
  }, [toast])
  const loadBootstrap = useCallback(async () => {
    const data = await request<Bootstrap>('/api/bootstrap'); setCSRF(data.csrfToken); setBootstrap(data)
    if (data.roborev.installed && data.roborev.capabilities.jobListApi && data.roborev.status?.running) await loadReviews(data.selectedRepository)
  }, [loadReviews])

  useEffect(() => { loadBootstrap().catch(error => toast('error', (error as Error).message)).finally(() => setLoading(false)) }, [loadBootstrap, toast])
  const canPollReviews = Boolean(bootstrap?.roborev.installed && bootstrap.roborev.capabilities.jobListApi && bootstrap.roborev.status?.running)
  useEffect(() => {
    if (!canPollReviews) return
    const timer = setInterval(() => loadReviews(bootstrap?.selectedRepository), 4000)
    return () => clearInterval(timer)
  }, [bootstrap?.selectedRepository, canPollReviews, loadReviews])
  useEffect(() => {
    const timer = setInterval(() => request<Installation>('/api/runtime').then(runtime => setBootstrap(current => current ? { ...current, roborev: runtime } : current)).catch(error => toast('error', (error as Error).message)), 10_000)
    return () => clearInterval(timer)
  }, [toast])

  const mutate = useCallback(async (path: string, body?: unknown) => {
    setBusy(true)
    try {
      await request(path, { method: 'POST', body: body === undefined ? undefined : JSON.stringify(body) })
      toast('success', path.includes('comment-close') ? 'Comment saved and review closed.' : 'RoboRev state updated.')
			const latest = await loadReviews(bootstrap?.selectedRepository)
			if (selected) setSelected(latest.find(job => idOf(job) === idOf(selected)) ?? selected)
      await loadBootstrap()
    } catch (error) {
      const apiError = error as APIError
      toast('error', apiError.phase === 'close' ? `Partial success: ${apiError.message}` : apiError.message)
      throw error
    } finally { setBusy(false) }
  }, [bootstrap?.selectedRepository, loadBootstrap, loadReviews, selected, toast])

  const repo = bootstrap?.repositories.find(item => item.path.toLowerCase() === bootstrap.selectedRepository.toLowerCase())
  const runningCount = jobs.filter(isRunning).length
  const daemonRunning = Boolean(bootstrap?.roborev.status?.running)
  const reviewHead = async () => {
    if (!repo) return
    await mutate('/api/reviews', { repoPath: repo.path, panel: 'dashboard_cross' })
    toast('success', `New HEAD review enqueued for ${repo.headShort}.`)
  }
  const switchRepo = async (path: string) => { await mutate('/api/repos/select', { repoPath: path }); setSelected(null) }

	const reviewSupported = Boolean(bootstrap?.roborev.capabilities.reviewEnqueueApi)
  return <div className="shell">
    <aside className={`sidebar ${menu ? 'open' : ''}`}><div className="brand"><span className="brand-mark"><Bot /></span><div><strong>RoboRev</strong><span>Dashboard</span></div></div><nav>{navigation.map(([label, Icon]) => <button key={label} className={activeNav === label ? 'active' : ''} onClick={() => { setActiveNav(label); setMenu(false) }}><Icon />{label}{!['Overview', 'Reviews', 'Repositories', 'Agents & Setup'].includes(label) && <small>Soon</small>}</button>)}</nav><div className="sidebar-foot"><span className={`health ${daemonRunning ? 'online' : ''}`} /><div><strong>{daemonRunning ? 'Daemon online' : 'Daemon offline'}</strong><span>{bootstrap?.roborev.version || 'Setup required'}</span></div></div></aside>
    <main className="main"><header className="topbar"><button className="icon-button mobile-menu" onClick={() => setMenu(old => !old)}><Menu /></button><div className="repo-switch"><span>Repository</span><select aria-label="Current repository" value={bootstrap?.selectedRepository || ''} onChange={event => switchRepo(event.target.value)}>{bootstrap?.repositories.map(item => <option value={item.path} key={item.path}>{item.name} · {item.branch}</option>)}</select></div>{repo && <div className="repo-meta"><span><GitBranch size={15} />{repo.branch}</span><span><GitCommit size={15} />{repo.headShort}</span>{repo.dirty && <span className="dirty"><AlertTriangle size={14} />Dirty</span>}</div>}<div className="top-spacer" /><span className="running"><Activity size={15} />{runningCount} running</span><button className="button primary review-head" disabled={busy || !repo || !bootstrap?.roborev.installed || !daemonRunning || !reviewSupported} onClick={reviewHead}><Play size={17} />Review HEAD</button></header>
      {!loading && bootstrap && (!bootstrap.roborev.installed || !daemonRunning || !reviewSupported) && <section className="setup-banner"><Server /><div><strong>{!bootstrap.roborev.installed ? 'RoboRev is not installed' : !reviewSupported ? 'RoboRev upgrade required' : 'RoboRev daemon is stopped'}</strong><p>{bootstrap.roborev.error || (!reviewSupported ? 'This version does not expose the safe metadata and enqueue contracts required by the Dashboard.' : bootstrap.roborev.installed ? 'Start the daemon to enqueue and view reviews.' : 'Configure a RoboRev binary to continue.')}</p></div>{bootstrap.roborev.installed && reviewSupported && !daemonRunning && <button className="button" disabled={busy} onClick={() => mutate('/api/daemon/start')}>Start daemon</button>}</section>}
      {loading ? <div className="page-loading"><div className="skeleton" /><div className="skeleton tall" /></div> : activeNav === 'Reviews' || activeNav === 'Overview' ? <ReviewList jobs={jobs} selected={selected ? idOf(selected) : undefined} onSelect={setSelected} /> : activeNav === 'Repositories' ? <Repositories bootstrap={bootstrap} mutate={mutate} busy={busy} /> : activeNav === 'Agents & Setup' ? <Setup installation={bootstrap?.roborev} mutate={mutate} busy={busy} /> : <section className="coming"><TerminalSquare /><span className="eyebrow">Checkpoint roadmap</span><h1>{activeNav}</h1><p>This typed workflow is tracked in ROBorev-COVERAGE.md and will be added without a generic shell endpoint.</p></section>}
    </main>
    {selected && <ReviewDetail key={`${idOf(selected)}-${selected.status}-${selected.closed}-${selected.addressed}`} job={selected} jobs={jobs} close={() => setSelected(null)} mutate={mutate} busy={busy} canRerun={Boolean(bootstrap?.roborev.capabilities.frozenRerunApi)} canLoadMembers={Boolean(bootstrap?.roborev.capabilities.panelMembersApi && selected.panel_run_uuid)} />}
    <div className="toasts" aria-live="polite">{toasts.map(item => <div key={item.id} className={`toast ${item.kind}`}>{item.kind === 'success' ? <Check /> : <XCircle />}{item.message}</div>)}</div>
  </div>
}

function Repositories({ bootstrap, mutate, busy }: { bootstrap: Bootstrap | null; mutate: (path: string, body?: unknown) => Promise<void>; busy: boolean }) {
  const [path, setPath] = useState('')
  return <section className="cards-page"><header className="section-heading"><div><span className="eyebrow">Registered targets</span><h1>Repositories</h1><p>Only canonical Git roots registered here can be mutation targets.</p></div></header><div className="repo-cards">{bootstrap?.repositories.map(repo => <article key={repo.path}><div className="repo-icon"><GitBranch /></div><div><h3>{repo.name}</h3><code>{repo.path}</code><p>{repo.branch} · {repo.headShort} {repo.dirty ? '· dirty' : '· clean'}</p></div></article>)}</div><form className="add-repo" onSubmit={event => { event.preventDefault(); mutate('/api/repos', { repoPath: path }).then(() => setPath('')) }}><label htmlFor="repo-path">Add local Git repository</label><div><input id="repo-path" value={path} onChange={event => setPath(event.target.value)} placeholder="E:\Dev\GitHub\project" /><button className="button primary" disabled={busy || !path.trim()}><Plus size={16} />Register</button></div></form></section>
}

function Setup({ installation, mutate, busy }: { installation?: Bootstrap['roborev']; mutate: (path: string, body?: unknown) => Promise<void>; busy: boolean }) {
  return <section className="cards-page"><header className="section-heading"><div><span className="eyebrow">Runtime health</span><h1>Agents & Setup</h1><p>Dashboard uses existing agent CLI authentication and never stores tokens.</p></div></header><article className="setup-card"><Server /><div><h3>RoboRev {installation?.version || 'not detected'}</h3><code>{installation?.binary || 'PATH lookup failed'}</code><p>{installation?.status?.running ? 'Daemon is running.' : installation?.error || 'Daemon is stopped.'}</p></div>{installation?.installed && !installation.status?.running && <button className="button primary" disabled={busy} onClick={() => mutate('/api/daemon/start')}>Start daemon</button>}</article></section>
}
