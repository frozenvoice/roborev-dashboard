import { Component, type ErrorInfo, type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Activity, AlertTriangle, Bot, Check, ChevronRight, CircleStop, Clipboard, Clock3, Code2, GitBranch, GitCommit, LayoutDashboard, ListChecks, Menu, MessageSquareText, Play, Plus, RefreshCw, RotateCcw, Search, Server, Settings, ShieldCheck, SlidersHorizontal, TerminalSquare, X, XCircle } from 'lucide-react'
import { APIError, normalizeAgentsResponse, normalizePanelRefineResponse, normalizeRuntimeControl, normalizeSetupState, parseReviewInvalidationEvent, reviewEventsURL, type AgentsResponse, type Bootstrap, type FixPromptResponse, type Installation, type PanelConfiguration, type PanelInfo, type PanelRefineOptions, type PanelRefineRun, type Repository, type ReviewEnqueueResponse, type ReviewJob, type RuntimeControlState, type SetupState, jobsFromPayload, request, setCSRF } from './api'
import { Markdown } from './Markdown'
import { jobStatus, jobStatusLabel } from './jobStatus'
import { FixRefine, PanelRefineSummary, isFixTarget } from './FixRefine'
import { Analyze } from './Analyze'
import { Tasks } from './Tasks'
import { InstallConfirmation, Setup, SetupBanner, setupIsActive, type DaemonAction } from './Setup'
import { I18nContext, LanguageSelector, useI18n, type MessageKey } from './i18n'

type Toast = { id: number; kind: 'success' | 'error'; message: string }
type Tab = 'review' | 'members' | 'log' | 'prompt' | 'comments' | 'metadata'
type AgentsLoadOutcome = 'applied' | 'superseded-applied' | 'superseded' | 'rejected' | 'failed'
type AppliedAgentsRequest = { generation: number; identity: string; repositoryEpoch: number }
type MemberLoadState = 'not-requested' | 'pending' | 'success-empty' | 'loaded' | 'error'

const navigation = [
  ['Overview', LayoutDashboard], ['Reviews', ListChecks], ['Fix & Refine', ShieldCheck],
  ['Analyze', Search], ['Tasks', TerminalSquare], ['Repositories', GitBranch],
  ['Agents & Setup', Bot], ['Configuration', Settings], ['Stats & Insights', Activity], ['Advanced', SlidersHorizontal],
] as const

const navigationLabelKeys: Record<string, MessageKey> = {
  Overview: 'nav.overview', Reviews: 'nav.reviews', 'Fix & Refine': 'nav.fixRefine', Analyze: 'nav.analyze', Tasks: 'nav.tasks',
  Repositories: 'nav.repositories', 'Agents & Setup': 'nav.agentsSetup', Configuration: 'nav.configuration', 'Stats & Insights': 'nav.stats', Advanced: 'nav.advanced',
}
function navigationLabel(value: string, t: ReturnType<typeof useI18n>['t']) {
  const key = navigationLabelKeys[value]
  return key ? t(key) : value
}

const tabLabelKeys: Record<Tab, MessageKey> = {
  review: 'review.tabReview', members: 'review.panelMembers', log: 'review.tabLog', prompt: 'review.tabPrompt', comments: 'review.tabComments', metadata: 'review.tabMetadata',
}
function tabLabel(value: Tab, t: ReturnType<typeof useI18n>['t']) { return t(tabLabelKeys[value]) }

function statusOf(job: ReviewJob) { return jobStatus(job) }
function statusLabel(value: string, t: ReturnType<typeof useI18n>['t']) {
  return jobStatusLabel(value, t)
}
function outputOf(job: ReviewJob | null) {
  if (!job) return ''
  if (typeof job.review === 'string') return job.review
  return String(job.review?.output || job.output || '')
}
function idOf(job: ReviewJob) { return Number(job.id || job.job_id) }
function positiveJobId(value: unknown) {
  const id = typeof value === 'number' || typeof value === 'string' && value.trim() ? Number(value) : NaN
  return Number.isSafeInteger(id) && id > 0 ? id : null
}
function memberIdOf(job: ReviewJob) { return positiveJobId(job.id ?? job.job_id) }
function panelMembersFromPayload(value: unknown, panelRunUUID: string) {
  return jobsFromPayload(value)
    .filter(member => memberIdOf(member) !== null && member.panel_role === 'member' && member.panel_run_uuid === panelRunUUID)
    .sort((a, b) => (a.panel_member_index ?? 0) - (b.panel_member_index ?? 0))
}
function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value && typeof value === 'object' && !Array.isArray(value))
}
function validatePanelMemberDetail(value: unknown, expected: { id: number; panelRunUUID: string; panelName?: string }) {
  if (!isRecord(value)) throw new Error('Panel member detail was not an object')
  const job = value.job
  if (!isRecord(job)) throw new Error('Panel member detail job identity is missing')
  const topLevelID = positiveJobId(value.job_id)
  const nestedID = positiveJobId(job.id)
  if (topLevelID === null || nestedID === null || topLevelID !== expected.id || nestedID !== expected.id) throw new Error('Panel member detail job identity mismatch')
  if (typeof job.repo_path !== 'string' || !job.repo_path.trim()) throw new Error('Panel member detail repository path mismatch')
  if (job.panel_run_uuid !== expected.panelRunUUID) throw new Error('Panel member detail panel run mismatch')
  if (job.panel_role !== 'member') throw new Error('Panel member detail role mismatch')
  if (expected.panelName && job.panel_name !== expected.panelName) throw new Error('Panel member detail panel mismatch')
  return value as ReviewJob
}
function isRunning(job: ReviewJob) { return ['queued', 'running'].includes(jobStatus(job)) }
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
function panelLabel(panel: PanelInfo, t: ReturnType<typeof useI18n>['t']) { return panel.name === 'none' ? t('review.singleAgent') : t('review.crossReview', { panel: panel.name }) }
function validPanelConfiguration(value: PanelConfiguration, identity: string) {
  return value?.repositoryIdentity === identity && Array.isArray(value.panels) && typeof value.selectedPanel === 'string' && typeof value.selectionRequired === 'boolean' && value.panels.every(panel => panel && typeof panel.name === 'string' && ['panel', 'single'].includes(panel.kind) && Array.isArray(panel.members) && ['global', 'local', 'builtin'].includes(panel.origin) && typeof panel.valid === 'boolean')
}
function reviewStreamSupported(installation?: Installation) {
  return installation?.capabilities?.streamEvents === true
}
export class ErrorBoundary extends Component<{ children: ReactNode }, { error?: Error }> {
  state: { error?: Error } = {}
  static getDerivedStateFromError(error: Error) { return { error } }
  componentDidCatch(error: Error, info: ErrorInfo) { console.error(error, info) }
  render() {
    if (this.state.error) return <I18nContext.Consumer>{({ t }) => <main className="fatal"><XCircle /><h1>{t('error.dashboardFailed')}</h1><p>{this.state.error?.message}</p><button onClick={() => location.reload()}>{t('error.reload')}</button></main>}</I18nContext.Consumer>
    return this.props.children
  }
}

function StatusPill({ job }: { job: ReviewJob }) {
  const { t } = useI18n()
  const status = statusOf(job)
  return <span className={`status status-${status}`}><span className="status-dot" />{statusLabel(status, t)}</span>
}

function CopyButton({ value }: { value: string }) {
  const { t } = useI18n()
  const [copied, setCopied] = useState(false)
  return <button className="button ghost compact" onClick={async () => { await navigator.clipboard.writeText(value); setCopied(true); setTimeout(() => setCopied(false), 1200) }}><Clipboard size={14} />{copied ? t('common.copied') : t('common.copy')}</button>
}

export function CommentBox({ jobId, mutate, busy }: { jobId: number; mutate: (path: string, body?: unknown) => Promise<void>; busy: boolean }) {
  const { t } = useI18n()
  const [comment, setComment] = useState('')
  const submit = async (close: boolean) => {
    await mutate(`/api/reviews/${jobId}/${close ? 'comment-close' : 'comment'}`, { comment })
    setComment('')
  }
  return <section className="comment-box">
    <label htmlFor="review-comment">{t('review.comment')}</label>
    <textarea id="review-comment" value={comment} onChange={event => setComment(event.target.value)} placeholder={t('review.commentPlaceholder')} rows={6} />
    <div className="button-row">
      <button className="button" disabled={busy || !comment.trim()} onClick={() => submit(false)}><MessageSquareText size={16} />{t('review.addComment')}</button>
      <button className="button primary" disabled={busy || !comment.trim()} onClick={() => submit(true)}><Check size={16} />{t('review.commentClose')}</button>
    </div>
    <p className="hint">{t('review.commentSavedFirst')}</p>
  </section>
}

export function ReviewDetail({ job, jobs, close, mutate, busy, canRerun, canLoadMembers, repoPath, repoIdentity, toast, onPrepareFix }: { job: ReviewJob; jobs: ReviewJob[]; close: () => void; mutate: (path: string, body?: unknown) => Promise<void>; busy: boolean; canRerun: boolean; canLoadMembers: boolean; repoPath: string; repoIdentity: string; toast: (kind: Toast['kind'], message: string) => void; onPrepareFix?: (job: ReviewJob) => void }) {
  const { t, locale } = useI18n()
  const [tab, setTab] = useState<Tab>('review')
  const [detail, setDetail] = useState<ReviewJob | null>(null)
  const [lazy, setLazy] = useState<Record<string, string>>({})
  const [lazyLoaded, setLazyLoaded] = useState<Record<string, boolean>>({})
  const [lazyError, setLazyError] = useState<Record<string, string>>({})
  const [lazyLoading, setLazyLoading] = useState<Record<string, boolean>>({})
  const [detailError, setDetailError] = useState('')
  const [memberJobs, setMemberJobs] = useState<ReviewJob[]>([])
  const [memberLoadState, setMemberLoadState] = useState<MemberLoadState>('not-requested')
  const [memberError, setMemberError] = useState('')
  const [selectedMemberId, setSelectedMemberId] = useState<number | null>(null)
  const [memberDetail, setMemberDetail] = useState<ReviewJob | null>(null)
  const [memberDetailError, setMemberDetailError] = useState('')
  const [memberDetailLoading, setMemberDetailLoading] = useState(false)
  const [detailLoading, setDetailLoading] = useState(false)
  const [memberLoading, setMemberLoading] = useState(false)
  const [fixPrompt, setFixPrompt] = useState<FixPromptResponse | null>(null)
  const [fixPromptText, setFixPromptText] = useState('')
  const [fixPromptLoading, setFixPromptLoading] = useState(false)
  const previousStatus = useRef(job.status)
  const detailGeneration = useRef(0)
  const lazyGeneration = useRef<Record<string, number>>({})
  const memberDetailGeneration = useRef(0)
  const memberPollGeneration = useRef(0)
  const selectedMemberIdRef = useRef<number | null>(null)
  const memberSnapshot = useRef<Map<number, string>>(new Map())
  const memberScopeRef = useRef('')
  const memberPollScopeRef = useRef('')
  const memberRepositoryIdentity = useRef(repoIdentity)
  const fixPromptGeneration = useRef(0)
  const fixPromptTarget = useRef<{ jobId: number; member: boolean } | null>(null)
  const id = idOf(job)
  const members = useMemo(() => memberJobs, [memberJobs])
  const terminalMemberCount = members.filter(member => !isRunning(member)).length
  const memberProgress = members.length ? Math.round((terminalMemberCount / members.length) * 100) : 0
  const setSelectedMember = (value: number | null) => { selectedMemberIdRef.current = value; setSelectedMemberId(value) }
  const memberQuery = `parentJobId=${encodeURIComponent(String(id))}&repositoryIdentity=${encodeURIComponent(repoIdentity)}`
  const memberListURL = job.panel_run_uuid ? `/api/panels/${encodeURIComponent(job.panel_run_uuid)}/members?${memberQuery}` : ''
  const memberDetailURL = (memberId: number) => job.panel_run_uuid ? `/api/panels/${encodeURIComponent(job.panel_run_uuid)}/members/${encodeURIComponent(String(memberId))}?${memberQuery}` : ''
  const loadMemberDetail = useCallback(async (member: ReviewJob, select = true) => {
    const memberId = memberIdOf(member)
    const panelRunUUID = job.panel_run_uuid
    if (memberId === null || !repoIdentity || !memberListURL || !panelRunUUID || member.panel_role !== 'member' || member.panel_run_uuid !== panelRunUUID) return
    const generation = ++memberDetailGeneration.current
    memberRepositoryIdentity.current = repoIdentity
    if (select) setSelectedMember(memberId)
    setMemberDetail(null)
    setMemberDetailError('')
    setMemberDetailLoading(true)
    try {
      const value = await request<unknown>(memberDetailURL(memberId))
      if (generation !== memberDetailGeneration.current || memberRepositoryIdentity.current !== repoIdentity) return
      setMemberDetail(validatePanelMemberDetail(value, { id: memberId, panelRunUUID, panelName: job.panel_name }))
    } catch (error) {
      if (generation === memberDetailGeneration.current && memberRepositoryIdentity.current === repoIdentity) setMemberDetailError((error as Error).message)
    } finally {
      if (generation === memberDetailGeneration.current && memberRepositoryIdentity.current === repoIdentity) setMemberDetailLoading(false)
    }
  }, [job.panel_run_uuid, memberListURL, repoIdentity])

  useEffect(() => {
    const generation = ++detailGeneration.current
    lazyGeneration.current = { prompt: (lazyGeneration.current.prompt ?? 0) + 1, log: (lazyGeneration.current.log ?? 0) + 1 }
    memberDetailGeneration.current++
    memberRepositoryIdentity.current = repoIdentity
    memberSnapshot.current = new Map()
    selectedMemberIdRef.current = null
    setDetail(null); setDetailError(''); setLazy({}); setLazyLoaded({}); setLazyError({}); setLazyLoading({}); setMemberJobs([]); setMemberLoadState('not-requested'); setMemberError(''); setSelectedMemberId(null); setMemberDetail(null); setMemberDetailError(''); setMemberDetailLoading(false); setMemberLoading(false); setTab(canLoadMembers && Boolean(job.panel_run_uuid) && isRunning(job) ? 'members' : 'review'); setDetailLoading(true)
    request<ReviewJob>(`/api/reviews/${id}`).then(value => { if (generation === detailGeneration.current) setDetail({ ...value, id, job_id: id }) }).catch(error => { if (generation === detailGeneration.current) { setDetail(null); setDetailError((error as Error).message) } }).finally(() => { if (generation === detailGeneration.current) setDetailLoading(false) })
    return () => { detailGeneration.current++; lazyGeneration.current.prompt++; lazyGeneration.current.log++ }
  }, [id])
  useEffect(() => {
    const scope = `${repoIdentity}\u0000${id}\u0000${job.panel_run_uuid || ''}`
    if (memberScopeRef.current === scope) return
    const hadScope = Boolean(memberScopeRef.current)
    memberScopeRef.current = scope
    memberPollScopeRef.current = ''
    memberRepositoryIdentity.current = repoIdentity
    memberPollGeneration.current++
    memberDetailGeneration.current++
    selectedMemberIdRef.current = null
    memberSnapshot.current = new Map()
    setSelectedMemberId(null)
    setMemberJobs([])
    setMemberLoadState('not-requested')
    setMemberLoading(false)
    setMemberError('')
    setMemberDetail(null)
    setMemberDetailError('')
    setMemberDetailLoading(false)
    if (hadScope) setTab(canLoadMembers && Boolean(job.panel_run_uuid) && isRunning(job) ? 'members' : 'review')
  }, [canLoadMembers, id, job.panel_run_uuid, repoIdentity])
  useEffect(() => {
    const wasRunning = ['queued', 'running'].includes(String(previousStatus.current).toLowerCase())
    previousStatus.current = job.status
    if (!wasRunning || isRunning(job)) return
    const detailRequest = ++detailGeneration.current
    setDetailLoading(true)
    const refreshDetail = async (attempt: number): Promise<void> => {
      try {
        const value = await request<ReviewJob>(`/api/reviews/${id}`)
        if (detailRequest === detailGeneration.current) {
          setDetail({ ...value, id, job_id: id })
          setDetailError('')
        }
      } catch (error) {
        if (detailRequest !== detailGeneration.current) return
        if (attempt < 1) {
          await new Promise<void>(resolve => setTimeout(resolve, 1200))
          if (detailRequest !== detailGeneration.current) return
          return refreshDetail(attempt + 1)
        }
        setDetailError((error as Error).message)
      }
    }
    void refreshDetail(0).finally(() => {
      if (detailRequest === detailGeneration.current) setDetailLoading(false)
    })
    if (lazyLoaded.log) {
      const logRequest = (lazyGeneration.current.log ?? 0) + 1
      lazyGeneration.current.log = logRequest
      setLazyLoading(old => ({ ...old, log: true }))
      setLazyError(old => ({ ...old, log: '' }))
      request<{ text: string }>(`/api/reviews/${id}/log`).then(value => { if (logRequest === lazyGeneration.current.log) { setLazy(old => ({ ...old, log: value.text })); setLazyError(old => ({ ...old, log: '' })) } }).catch(error => { if (logRequest === lazyGeneration.current.log) setLazyError(old => ({ ...old, log: (error as Error).message })) }).finally(() => { if (logRequest === lazyGeneration.current.log) setLazyLoading(old => ({ ...old, log: false })) })
    }
  }, [id, job.status, lazyLoaded.log])
  useEffect(() => {
    if (!['prompt', 'log'].includes(tab) || lazyLoaded[tab]) return
    const generation = (lazyGeneration.current[tab] ?? 0) + 1
    lazyGeneration.current[tab] = generation
    setLazyLoaded(old => ({ ...old, [tab]: true }))
    setLazyError(old => ({ ...old, [tab]: '' }))
    setLazyLoading(old => ({ ...old, [tab]: true }))
    request<{ text: string }>(`/api/reviews/${id}/${tab}`).then(value => { if (generation === lazyGeneration.current[tab]) setLazy(old => ({ ...old, [tab]: value.text })) }).catch(error => { if (generation === lazyGeneration.current[tab]) setLazyError(old => ({ ...old, [tab]: (error as Error).message })) }).finally(() => { if (generation === lazyGeneration.current[tab]) setLazyLoading(old => ({ ...old, [tab]: false })) })
  }, [id, lazyLoaded, tab])
  useEffect(() => {
    if (tab !== 'members' || !job.panel_run_uuid) return
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    const generation = ++memberPollGeneration.current
    const panelRunUUID = job.panel_run_uuid
    const identity = repoIdentity
    const scope = `${identity}\u0000${id}\u0000${panelRunUUID}`
    const firstScopeRequest = memberPollScopeRef.current !== scope
    let first = firstScopeRequest
    const loadMembers = async () => {
      if (first) {
        setMemberLoading(true)
        setMemberLoadState('pending')
      }
      try {
        const value = await request<unknown>(memberListURL)
        if (cancelled || generation !== memberPollGeneration.current || identity !== memberRepositoryIdentity.current) return
        const next = panelMembersFromPayload(value, panelRunUUID)
        const activeTarget = fixPromptTarget.current
        if (activeTarget?.member && !next.some(member => idOf(member) === activeTarget.jobId && isFinding(member))) {
          fixPromptGeneration.current++
          fixPromptTarget.current = null
          setFixPromptLoading(false)
        }
        const selectedId = selectedMemberIdRef.current
        const selectedMember = selectedId === null ? undefined : next.find(member => idOf(member) === selectedId)
        if (selectedId !== null && !selectedMember) {
          memberDetailGeneration.current++
          selectedMemberIdRef.current = null
          setSelectedMemberId(null)
          setMemberDetail(null)
          setMemberDetailError('')
          setMemberDetailLoading(false)
        } else if (selectedMember && selectedMemberIdRef.current === selectedId) {
          const previousStatus = memberSnapshot.current.get(selectedId!)
          if (previousStatus && ['queued', 'running'].includes(previousStatus) && !isRunning(selectedMember)) void loadMemberDetail(selectedMember, false)
        }
        memberSnapshot.current = new Map(next.map(member => [idOf(member), String(member.status || '').toLowerCase()]))
        setMemberJobs(next)
        setMemberLoadState(next.length ? 'loaded' : 'success-empty')
        setMemberError('')
        if (isRunning(job) || next.some(isRunning)) timer = setTimeout(loadMembers, 4000)
      } catch (error) {
        if (cancelled || generation !== memberPollGeneration.current || identity !== memberRepositoryIdentity.current) return
        setMemberLoadState('error')
        setMemberError((error as Error).message)
        if (isRunning(job)) timer = setTimeout(loadMembers, 4000)
      } finally {
        if (!cancelled && generation === memberPollGeneration.current && identity === memberRepositoryIdentity.current && first) {
          memberPollScopeRef.current = scope
          setMemberLoading(false)
        }
        first = false
      }
    }
    loadMembers()
    return () => { cancelled = true; if (timer) clearTimeout(timer); memberPollGeneration.current++ }
  }, [job.panel_run_uuid, job.status, memberListURL, repoIdentity, tab, loadMemberDetail])
  useEffect(() => {
    fixPromptGeneration.current++
    fixPromptTarget.current = null
    setFixPrompt(null)
    setFixPromptText('')
    setFixPromptLoading(false)
    return () => { fixPromptGeneration.current++ }
  }, [id, repoIdentity])

  // Fresh detail fields win over the possibly-stale slice row so a task that
  // finished never keeps rendering its outdated lifecycle status.
  const current = detail ? { ...job, ...detail, output: detail.output ?? job.output, review: detail.review ?? job.review, id, job_id: id } : job
  const text = outputOf(current)
  const memberText = outputOf(memberDetail)
  const terminal = !isRunning(current)
  const isTaskResult = current.job_type === 'task' || current.git_ref === 'run'
  // A running single job whose row lives only in the Tasks history slice has
  // no list polling to refresh it; poll its detail until terminal instead.
  const detailIsFresh = detail !== null
  const currentRunning = isRunning(current)
  useEffect(() => {
    if (!currentRunning || !detailIsFresh || job.panel_run_uuid) return
    const generation = ++detailGeneration.current
    const timer = setInterval(() => {
      void request<ReviewJob>(`/api/reviews/${id}`).then(value => {
        if (generation === detailGeneration.current) {
          setDetail({ ...value, id, job_id: id })
          setDetailError('')
        }
      }).catch(() => { /* transient transport errors keep the last good detail */ })
    }, 4000)
    return () => clearInterval(timer)
  }, [currentRunning, detailIsFresh, id, job.panel_run_uuid])
  const finding = terminal && isFinding(current)
  const fixTarget = selectedMemberId === null && isFixTarget(current)
  const openFixPrompt = (value: FixPromptResponse) => { setFixPrompt(value); setFixPromptText(value.text) }
  const loadFixPrompt = async (copy: boolean, targetJobId = id) => {
    if (!repoPath) return
    const generation = ++fixPromptGeneration.current
    fixPromptTarget.current = { jobId: targetJobId, member: targetJobId !== id }
    setFixPromptLoading(true)
    try {
      const value = await request<FixPromptResponse>(`/api/reviews/${targetJobId}/fix-prompt?repo=${encodeURIComponent(repoPath)}`)
      if (generation !== fixPromptGeneration.current) return
      if (!value || value.jobId !== targetJobId || typeof value.text !== 'string' || typeof value.stale !== 'boolean') throw new APIError(t('error.fixPromptMalformed'))
      if (!copy) { openFixPrompt(value); return }
      try {
        if (typeof navigator.clipboard?.writeText !== 'function') throw new Error('Clipboard unavailable')
        await navigator.clipboard.writeText(value.text)
        if (generation === fixPromptGeneration.current) toast('success', t('review.fixPromptCopied', { id: targetJobId }))
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
      toast('success', t('review.fixPromptCopied', { id: fixPrompt?.jobId ?? id }))
    } catch {
      toast('error', t('review.clipboardUnavailable'))
    }
  }
  const returnToMembers = () => {
    memberDetailGeneration.current++
    setSelectedMember(null)
    setMemberDetail(null)
    setMemberDetailError('')
    setMemberDetailLoading(false)
    setTab('review')
  }
  return <><aside className="detail-panel" aria-label={t('review.detailAria')}>
    <header className="detail-header"><div><span className="eyebrow">{t('review.title', { id })}</span><h2>{current.commit_subject || current.subject || current.git_ref || current.commit_sha || t('review.detail')}</h2></div><div className="modal-header-actions"><LanguageSelector compact /><button className="icon-button" aria-label={t('review.closeDetail')} onClick={close}><X /></button></div></header>
    <div className="detail-summary"><StatusPill job={current} /><span><Bot size={14} />{current.panel_name || current.agent || t('common.default')}</span><span><Clock3 size={14} />{elapsed(current)}</span></div>
    <nav className="tabs" aria-label={t('review.detailTabs')}>
      {(['review', ...(canLoadMembers ? ['members' as const] : []), 'log', 'prompt', 'comments', 'metadata'] as Tab[]).map(item => <button key={item} className={tab === item ? 'active' : ''} onClick={() => setTab(item)}>{tabLabel(item, t)}</button>)}
    </nav>
    <div className="detail-content">
      {detailLoading && tab === 'review' && <div className="skeleton tall" />}
      {!detailLoading && tab === 'review' && <>{detailError && <p role="alert" className="hint">{t('review.detailRefreshFailed', { error: detailError })}</p>}<div className="text-toolbar"><CopyButton value={text} /><span>{new Intl.NumberFormat(locale === 'ko' ? 'ko-KR' : 'en-US').format(text.length)} {t('common.chars')}</span></div>{text ? <Markdown>{text}</Markdown> : <Empty title={t('review.noOutput')} body={t('review.runningUpdates')} />}</>}
      {tab === 'members' && (memberLoading ? <div className="member-list member-tree member-list-loading" aria-label={t('common.loading')}><div className="skeleton tall" /></div> : <div className="member-list member-tree" role="tree" aria-label={t('review.panelMembers')}>{memberError && <p role="alert" className="hint">{t('review.membersRefreshFailed', { error: memberError })}</p>}{memberLoadState !== 'not-requested' && memberLoadState !== 'pending' && <div className="member-progress" aria-label={t('review.memberProgress', { terminal: terminalMemberCount, total: members.length })}><div className="member-progress-heading"><strong>{t('review.panelMembers')}</strong><span>{terminalMemberCount} / {members.length}</span></div><div className="member-progress-track"><div className="member-progress-value" style={{ width: String(memberProgress) + '%' }} /></div></div>}{members.length ? members.map(member => <article key={idOf(member)} className={selectedMemberId === idOf(member) ? 'member-item selected' : 'member-item'} role="treeitem" aria-expanded={selectedMemberId === idOf(member)}><button type="button" className="member-row" onClick={() => void loadMemberDetail(member)}><span className="member-row-primary"><StatusPill job={member} /><strong>#{idOf(member)}</strong><span>{member.panel_member_name || t('review.memberFallback', { id: idOf(member) })}</span></span><span className="member-row-facts"><span data-member-field="agent"><b>{t('review.memberAgent')}</b>{member.agent || t('common.default')}</span><span data-member-field="model"><b>{t('review.memberModel')}</b>{member.model || t('common.defaultModel')}</span><span data-member-field="status"><b>{t('review.memberStatus')}</b><em data-status={member.status || 'queued'}>{statusLabel(String(member.status || 'queued').toLowerCase(), t)}</em></span><span data-member-field="verdict"><b>{t('review.memberVerdict')}</b><em data-verdict={member.verdict || ''}>{member.verdict ? statusLabel(String(member.verdict).toLowerCase(), t) : '—'}</em></span><span data-member-field="duration"><b>{t('review.memberDuration')}</b>{elapsed(member)}</span></span></button>{isFinding(member) && <div className="member-actions"><button className="button ghost compact" disabled={fixPromptLoading} onClick={() => loadFixPrompt(true, idOf(member))}><Clipboard size={14} />{t('review.copyFixPrompt')}</button><button className="button ghost compact" disabled={fixPromptLoading} onClick={() => loadFixPrompt(false, idOf(member))}><Code2 size={14} />{t('review.openFixPrompt')}</button></div>}</article>) : memberLoadState === 'success-empty' && !memberError && <Empty title={t('review.noMemberJobs')} body={t('review.noMemberJobsBody')} />}{selectedMemberId !== null && <section className="member-detail" aria-label={t('review.memberDetail')}><button type="button" className="button ghost compact member-parent-return" onClick={returnToMembers}>{t('review.returnToSynthesis')}</button>{memberDetailLoading && <div className="skeleton tall" />}{!memberDetailLoading && memberDetailError && <p role="alert" className="hint">{t('review.detailRefreshFailed', { error: memberDetailError })}</p>}{!memberDetailLoading && !memberDetailError && memberDetail && <><header className="member-detail-header"><strong>#{idOf(memberDetail)} · {memberDetail.panel_member_name || memberDetail.agent || t('review.memberFallback', { id: idOf(memberDetail) })}</strong><span>{memberDetail.model || t('common.defaultModel')}</span></header><div className="text-toolbar"><CopyButton value={memberText} /><span>{new Intl.NumberFormat(locale === 'ko' ? 'ko-KR' : 'en-US').format(memberText.length)} {t('common.chars')}</span></div>{memberText ? <Markdown>{memberText}</Markdown> : <Empty title={t('review.noOutput')} body={t('review.runningUpdates')} />}</>}</section>}</div>)}
      {(tab === 'log' || tab === 'prompt') && <>{lazyLoading[tab] ? <div className="skeleton tall" /> : <>{lazyError[tab] && <p role="alert" className="hint">{t('review.tabLoadFailed', { tab: tabLabel(tab, t), error: lazyError[tab] })} <button className="button ghost compact" onClick={() => setLazyLoaded(old => ({ ...old, [tab]: false }))}>{t('common.retry')}</button></p>}<div className="text-toolbar"><CopyButton value={lazy[tab] || ''} /></div><pre className="long-text">{lazy[tab] || t('review.noContent', { tab: tabLabel(tab, t) })}</pre></>}</>}
      {tab === 'comments' && <CommentBox jobId={id} mutate={mutate} busy={busy} />}
      {tab === 'metadata' && <><div className="text-toolbar"><CopyButton value={JSON.stringify(current, null, 2)} /></div><pre className="long-text">{JSON.stringify(current, null, 2)}</pre></>}
    </div>
    <footer className="detail-actions">
      {fixTarget && <button className="button primary" disabled={busy} onClick={() => onPrepareFix?.(current)}><ShieldCheck size={16} />{t('review.fix')}</button>}
      {finding && <button className="button" disabled={fixPromptLoading} onClick={() => loadFixPrompt(true)}><Clipboard size={16} />{t('review.copyFixPrompt')}</button>}
      {finding && <button className="button" disabled={fixPromptLoading} onClick={() => loadFixPrompt(false)}><Code2 size={16} />{t('review.openFixPrompt')}</button>}
      {isRunning(current) && <button className="button danger" disabled={busy} onClick={() => mutate(`/api/reviews/${id}/cancel`)}><CircleStop size={16} />{t('review.cancel')}</button>}
      {terminal && !isClosed(current) && <button className="button" disabled={busy} onClick={() => mutate(`/api/reviews/${id}/close`)}><Check size={16} />{t('review.close')}</button>}
      {terminal && isClosed(current) && <button className="button" disabled={busy} onClick={() => mutate(`/api/reviews/${id}/reopen`)}><RotateCcw size={16} />{t('review.reopen')}</button>}
      {terminal && canRerun && !isTaskResult && <button className="button subtle rerun" disabled={busy} onClick={() => { if (confirm(t('review.rerunConfirm', { id }))) mutate(`/api/reviews/${id}/rerun`) }}><RefreshCw size={16} />{t('review.rerun')}</button>}
    </footer>
  </aside>{fixPrompt && <div className="prompt-overlay"><section className="prompt-dialog" role="dialog" aria-modal="true" aria-labelledby="fix-prompt-title"><header><div><span className="eyebrow">{t('review.finding', { id: fixPrompt.jobId })}</span><h2 id="fix-prompt-title">{t('review.fixPrompt')}</h2><p>{fixPrompt.stale ? t('review.fixPromptStale') : t('review.fixPromptGenerated')}</p></div><div className="modal-header-actions"><LanguageSelector compact /><button className="icon-button" aria-label={t('review.closeFixPrompt')} onClick={() => setFixPrompt(null)}><X /></button></div></header><textarea aria-label={t('review.fixPromptAria')} value={fixPromptText} onChange={event => setFixPromptText(event.target.value)} rows={20} /><footer><button className="button" onClick={() => setFixPrompt(null)}>{t('common.close')}</button><button className="button primary" onClick={copyEditedFixPrompt}><Clipboard size={16} />{t('common.copy')}</button></footer></section></div>}</>
}

function Empty({ title, body }: { title: string; body: string }) { return <div className="empty"><Code2 /><h3>{title}</h3><p>{body}</p></div> }

function ReviewList({ jobs, selected, onSelect }: { jobs: ReviewJob[]; selected?: number; onSelect: (job: ReviewJob) => void }) {
  const { t } = useI18n()
  const [query, setQuery] = useState('')
  const filtered = jobs.filter(job => JSON.stringify([job.id, job.git_ref, job.subject, job.commit_subject, job.agent, job.panel_name, job.status, job.verdict, statusLabel(statusOf(job), t)]).toLowerCase().includes(query.toLowerCase()))
  return <section className="review-section">
    <header className="section-heading"><div><span className="eyebrow">{t('review.operations')}</span><h1>{t('review.queue')}</h1><p>{t('review.liveJobs')}</p></div><div className="search"><Search size={16} /><input aria-label={t('review.search')} value={query} onChange={event => setQuery(event.target.value)} placeholder={t('review.searchPlaceholder')} /></div></header>
    <div className="table-wrap"><table><thead><tr><th>{t('review.status')}</th><th>{t('review.target')}</th><th>{t('review.reviewer')}</th><th>{t('review.branch')}</th><th>{t('review.elapsed')}</th><th>{t('review.state')}</th><th /></tr></thead><tbody>
      {filtered.map(job => <tr key={idOf(job)} className={selected === idOf(job) ? 'selected' : ''} onClick={() => onSelect(job)}><td><StatusPill job={job} /></td><td><strong>#{idOf(job)} · {job.commit_subject || job.subject || job.git_ref || job.commit_sha || t('review.customTask')}</strong><small>{job.job_type || (job.panel_name ? t('review.panelSynthesis') : t('review.reviewHead'))}</small></td><td><span className="agent"><Bot size={14} />{job.panel_name || job.agent || t('common.default')}</span><small>{job.model || job.reasoning || ''}</small></td><td>{job.branch || '—'}</td><td>{elapsed(job)}</td><td><span className={isClosed(job) ? 'muted' : 'open-state'}>{isClosed(job) ? t('status.closed') : t('status.open')}</span></td><td><ChevronRight size={17} /></td></tr>)}
    </tbody></table>{!filtered.length && <Empty title={t('review.noReviews')} body={query ? t('review.tryDifferentFilter') : t('review.createHead')} />}</div>
  </section>
}

export function App() {
  const { t } = useI18n()
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
  // Tasks history is a lazy, repository-scoped slice with a raised bounded
  // limit so older task rows are not silently truncated by the polling list.
  const [taskJobs, setTaskJobs] = useState<ReviewJob[]>([])
  const [loadedTaskIdentity, setLoadedTaskIdentity] = useState('')
  const [taskHistoryError, setTaskHistoryError] = useState('')
  const [taskReloadEpoch, setTaskReloadEpoch] = useState(0)
  const tasksVisibleRef = useRef(false)
  const loadedTaskIdentityRef = useRef('')
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
  const [fixTargetJobId, setFixTargetJobId] = useState<number | null>(null)
  const [setupState, setSetupState] = useState<SetupState | null>(null)
  const [setupError, setSetupError] = useState('')
  const [setupReloadEpoch, setSetupReloadEpoch] = useState(0)
  const [setupConfirming, setSetupConfirming] = useState(false)
  const [agents, setAgents] = useState<AgentsResponse | null>(null)
  const [loadedAgentsIdentity, setLoadedAgentsIdentity] = useState('')
  const [agentsLoading, setAgentsLoading] = useState(false)
  const [agentsError, setAgentsError] = useState('')
  const [agentsReloadEpoch, setAgentsReloadEpoch] = useState(0)
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
  const taskHistoryGeneration = useRef(0)
  const lastTaskSliceEpoch = useRef(-1)
  const setupGeneration = useRef(0)
  const setupReadyGeneration = useRef(0)
  const reviewEffectEpoch = useRef(0)
  const reviewCleanup = useRef<(() => void) | null>(null)
  const reviewSwitchInProgress = useRef(false)
  // Request generation only orders overlapping GET /api/agents calls. It is
  // intentionally independent from the repository epoch so a same-repo
  // refresh cannot make an in-flight health check look stale.
  const agentsRequestGeneration = useRef(0)
  const agentsRepositoryEpoch = useRef(0)
  const lastAppliedAgentsRequest = useRef<AppliedAgentsRequest | null>(null)
  const previousAgentsRepositoryIdentity = useRef<string | null>(null)
  const runtimeControlGeneration = useRef(0)
  const currentRepositoryIdentity = useRef('')
  const selectedRepositoryIdentity = bootstrap?.selectedRepositoryIdentity ?? ''
  const repo = bootstrap?.repositories.find(item => item.identity === selectedRepositoryIdentity)

  useEffect(() => {
    currentRepositoryIdentity.current = selectedRepositoryIdentity
    panelRefineReviewGeneration.current++
    if (previousAgentsRepositoryIdentity.current !== selectedRepositoryIdentity) {
      const hadPreviousSelection = previousAgentsRepositoryIdentity.current !== null
      previousAgentsRepositoryIdentity.current = selectedRepositoryIdentity
      if (hadPreviousSelection) agentsRepositoryEpoch.current++
    }
  }, [selectedRepositoryIdentity])

  useEffect(() => {
    // Repository-scoped agent discovery must be invalidated before a new
    // response can commit. This also removes the old cards immediately while
    // the selected repository is being restored by the backend.
    agentsRequestGeneration.current++
    setAgents(null)
    setLoadedAgentsIdentity('')
    setAgentsError('')
    setAgentsLoading(false)
  }, [selectedRepositoryIdentity])

  const toast = useCallback((kind: Toast['kind'], message: string) => { const id = Date.now(); setToasts(old => [...old, { id, kind, message }]); setTimeout(() => setToasts(old => old.filter(item => item.id !== id)), 4500) }, [])
  const loadReviews = useCallback(async (repo?: string, identity?: string, expectedEffectEpoch?: number) => {
    if (!repo || !identity) return []
    const generation = ++reviewGeneration.current
    const isCurrent = () => generation === reviewGeneration.current
      && identity === currentRepositoryIdentity.current
      && !reviewSwitchInProgress.current
      && (expectedEffectEpoch === undefined || expectedEffectEpoch === reviewEffectEpoch.current)
    try {
      const next = jobsFromPayload(await request<unknown>(`/api/reviews?repo=${encodeURIComponent(repo)}`))
      if (isCurrent()) {
        setJobs(next)
        setSelected(current => current ? next.find(job => idOf(job) === idOf(current)) ?? current : current)
        setReviewPollError('')
        setLoadedReviewIdentity(identity)
      }
      return isCurrent() ? next : []
    } catch (error) {
      if (isCurrent()) {
        setReviewPollError((error as Error).message)
        setLoadedReviewIdentity(identity)
      }
      return []
    }
  }, [])
  const loadTaskHistory = useCallback(async (repoPath: string, identity: string) => {
    // Same stale-response discipline as loadReviews: a generation counter plus
    // the switch-time repository identity reject superseded responses on both
    // the success and failure paths.
    const generation = ++taskHistoryGeneration.current
    try {
      const next = jobsFromPayload(await request<unknown>(`/api/reviews?repo=${encodeURIComponent(repoPath)}&limit=200`))
      if (generation !== taskHistoryGeneration.current || identity !== currentRepositoryIdentity.current) return
      setTaskJobs(next)
      setLoadedTaskIdentity(identity)
      loadedTaskIdentityRef.current = identity
      setTaskHistoryError('')
    } catch (error) {
      if (generation !== taskHistoryGeneration.current || identity !== currentRepositoryIdentity.current) return
      setTaskHistoryError((error as Error).message)
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
      if (!next) throw new APIError(t('error.setupMalformed'))
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
  const loadAgents = useCallback(async (expectedIdentity: string = currentRepositoryIdentity.current): Promise<AgentsLoadOutcome> => {
    const generation = ++agentsRequestGeneration.current
    const requestRepositoryEpoch = agentsRepositoryEpoch.current
    setAgentsLoading(true)
    try {
      const next = normalizeAgentsResponse(await request<unknown>('/api/agents'))
      if (!next) throw new APIError(t('error.agentsMalformed'))
      // Scope is authoritative even when this response lost the request
      // ordering race. A superseded response from another repository must
      // never be treated as a harmless same-repository refresh.
      if (!expectedIdentity || expectedIdentity !== currentRepositoryIdentity.current || next.repositoryIdentity !== expectedIdentity) return 'rejected'
      if (generation !== agentsRequestGeneration.current) {
        const applied = lastAppliedAgentsRequest.current
        return applied && applied.generation > generation && applied.generation === agentsRequestGeneration.current && applied.identity === expectedIdentity && applied.repositoryEpoch === requestRepositoryEpoch ? 'superseded-applied' : 'superseded'
      }
      setAgents(next)
      setLoadedAgentsIdentity(expectedIdentity)
      setAgentsError('')
      lastAppliedAgentsRequest.current = { generation, identity: expectedIdentity, repositoryEpoch: requestRepositoryEpoch }
      return 'applied'
    } catch (error) {
      if (generation === agentsRequestGeneration.current && expectedIdentity === currentRepositoryIdentity.current) setAgentsError((error as Error).message)
      return generation === agentsRequestGeneration.current ? 'failed' : 'superseded'
    } finally {
      if (generation === agentsRequestGeneration.current) setAgentsLoading(false)
    }
  }, [])
  const loadRuntimeControl = useCallback(async () => {
    const generation = ++runtimeControlGeneration.current
    setRuntimeControlLoading(true)
    try {
      const next = normalizeRuntimeControl(await request<unknown>('/api/runtime-control'))
      if (!next) throw new APIError(t('error.runtimeMalformed'))
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
      if (!validPanelConfiguration(configuration, identity)) throw new APIError(t('error.panelConfigurationMalformed'))
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
        if (generation === panelRefineGeneration.current) setPanelRefineError(t('error.panelRefineMalformed'))
        return { run: null, ok: false }
      }
      const envelope = raw as { runs: unknown[] }
      const payload = normalizePanelRefineResponse(envelope)
      if (envelope.runs.length > 0 && payload.runs.length === 0) throw new APIError(t('error.panelRefineNoValidRuns'))
      if (generation !== panelRefineGeneration.current) return { run: null, ok: false }
      const next = payload.run && payload.run.repositoryIdentity === identity ? payload.run : null
      if (payload.run && !next) throw new APIError(t('error.panelRefineWrongRepository'))
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
    const identity = selectedRepositoryIdentity
    if (!identity) return
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    const poll = async () => {
      const [runtime] = await Promise.all([loadRuntimeControl(), loadAgents(identity)])
      if (cancelled) return
      // Runtime-control operations are asynchronous. Poll only for the
      // bounded transition; idle setup pages do not generate background GETs.
      if (runtime?.operationActive) timer = setTimeout(poll, 800)
    }
    void poll()
    return () => { cancelled = true; runtimeControlGeneration.current++; if (timer) clearTimeout(timer) }
  }, [activeNav, agentsReloadEpoch, initialized, loadAgents, loadRuntimeControl, selectedRepositoryIdentity])
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
  const canStreamReviews = canPollReviews && reviewStreamSupported(bootstrap?.roborev)
  useEffect(() => {
    if (!initialized) return
    const effectEpoch = ++reviewEffectEpoch.current
    reviewGeneration.current++
    if (!canPollReviews) {
      setJobs([])
      setReviewPollError('')
      setLoadedReviewIdentity('')
      setTaskJobs([])
      loadedTaskIdentityRef.current = ''
      setLoadedTaskIdentity('')
      setTaskHistoryError('')
      return
    }
    const repoPath = bootstrap?.selectedRepository
    const repoIdentity = bootstrap?.selectedRepositoryIdentity
    if (!repoPath || !repoIdentity) return
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    let eventSource: EventSource | undefined
    let reconnectTimer: ReturnType<typeof setTimeout> | undefined
    let refreshTimer: ReturnType<typeof setTimeout> | undefined
    let authoritativeInFlight = false
    let refreshPending = false
    let reconnectDelay = 1_000
    let cleaned = false
    const scopeActive = () => !cancelled && effectEpoch === reviewEffectEpoch.current && repoIdentity === currentRepositoryIdentity.current
    const eventScopeActive = () => scopeActive() && !reviewSwitchInProgress.current

    // A stream message only invalidates the list.  Keep the leading refresh
    // short, coalesce a burst into one request, and permit one trailing
    // refresh when events arrive while the authoritative GET is in flight.
    const refreshReviews = async () => {
      if (!scopeActive()) return
      if (authoritativeInFlight) {
        refreshPending = true
        return
      }
      authoritativeInFlight = true
      try {
        await loadReviews(repoPath, repoIdentity, effectEpoch)
        // While the Tasks screen is visible, its raised-limit history slice
        // follows the same invalidation signals as the review list.
        if (tasksVisibleRef.current && loadedTaskIdentityRef.current === repoIdentity) void loadTaskHistory(repoPath, repoIdentity)
      } finally {
        authoritativeInFlight = false
        if (!cancelled && refreshPending) {
          refreshPending = false
          scheduleEventRefresh()
        }
      }
    }
    const scheduleEventRefresh = () => {
      if (!eventScopeActive() || refreshTimer) return
      refreshTimer = setTimeout(() => {
        refreshTimer = undefined
        void refreshReviews()
      }, 75)
    }
    const scheduleReconnect = () => {
      if (!scopeActive() || reconnectTimer) return
      const delay = reconnectDelay
      reconnectDelay = Math.min(30_000, reconnectDelay * 2)
      reconnectTimer = setTimeout(() => {
        reconnectTimer = undefined
        connectEvents()
      }, delay)
    }
    const connectEvents = () => {
      if (!scopeActive() || eventSource || typeof EventSource === 'undefined') return
      const eventsURL = reviewEventsURL(repoIdentity)
      if (!eventsURL) return
      try {
        const next = new EventSource(eventsURL)
        eventSource = next
        next.onopen = () => { reconnectDelay = 1_000 }
        next.onmessage = event => {
          if (!eventScopeActive() || eventSource !== next) return
          const signal = parseReviewInvalidationEvent(event.data)
          if (!signal || signal.repositoryIdentity !== repoIdentity || signal.kind !== 'jobs-changed') return
          scheduleEventRefresh()
        }
        next.onerror = () => {
          if (!scopeActive() || eventSource !== next) return
          // Native EventSource retries automatically. Close first so that we
          // own a single bounded retry timer and never duplicate connections.
          next.close()
          eventSource = undefined
          scheduleReconnect()
        }
      } catch {
        // An unavailable/unsupported SSE implementation must not affect the
        // ordinary four-second review polling fallback.
        eventSource = undefined
        scheduleReconnect()
      }
    }
    const poll = async () => {
      await refreshReviews()
      if (scopeActive()) timer = setTimeout(poll, 4000)
    }
    void poll()
    if (canStreamReviews) connectEvents()
    const cleanup = () => {
      if (cleaned) return
      cleaned = true
      cancelled = true
      reviewEffectEpoch.current++
      reviewGeneration.current++
      if (timer) clearTimeout(timer)
      if (refreshTimer) clearTimeout(refreshTimer)
      if (reconnectTimer) clearTimeout(reconnectTimer)
      eventSource?.close()
      eventSource = undefined
      refreshPending = false
    }
    reviewCleanup.current = cleanup
    return () => {
      if (reviewCleanup.current === cleanup) reviewCleanup.current = null
      cleanup()
    }
  }, [bootstrap?.selectedRepository, bootstrap?.selectedRepositoryIdentity, canPollReviews, canStreamReviews, initialized, loadReviews])
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
      toast('success', path.includes('comment-close') ? t('review.commentClosed') : t('review.stateUpdated'))
      const data = await loadBootstrap()
      await loadReviews(data.selectedRepository, data.selectedRepositoryIdentity)
    } catch (error) {
      const apiError = error as APIError
      toast('error', apiError.phase === 'close' ? t('review.partialSuccess', { error: apiError.message }) : apiError.message)
      throw error
    } finally { setBusy(false) }
  }, [loadBootstrap, loadReviews, toast])
  const setupMutation = useCallback(async (path: string) => {
    await mutate(path)
    setSetupReloadEpoch(epoch => epoch + 1)
  }, [mutate])
  const refreshRuntimeSurfaces = useCallback(async () => {
    const data = await loadBootstrap().catch(() => null)
    const target = data ?? bootstrap
    const targetRepo = target?.repositories.find(item => item.identity === target.selectedRepositoryIdentity)
    await Promise.allSettled([
      loadSetup(),
      targetRepo ? loadAgents(targetRepo.identity) : Promise.resolve(null),
      loadRuntimeControl(),
      request<Installation>('/api/runtime').then(runtime => {
        if (runtime && typeof runtime === 'object' && typeof runtime.installed === 'boolean') setBootstrap(current => current ? { ...current, roborev: runtime } : current)
      }),
    ])
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
    throw new APIError(t('error.daemonOperationTimeout'))
  }, [loadRuntimeControl])
  const runDaemonAction: DaemonAction = useCallback(async action => {
    setBusy(true)
    try {
      await request(`/api/daemon/${action}`, { method: 'POST' })
      await pollRuntimeOperation()
      await refreshRuntimeSurfaces()
      toast('success', action === 'start' ? t('setup.daemonStartedToast') : action === 'stop' ? t('setup.daemonStoppedToast') : t('setup.daemonRestartedToast'))
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
      toast('success', action === 'pause' ? `${t('setup.queuePaused')}.` : `${t('setup.resumeQueue')}.`)
    } catch (error) {
      toast('error', (error as Error).message)
      throw error
    } finally { setBusy(false) }
  }, [bootstrap?.selectedRepository, bootstrap?.selectedRepositoryIdentity, loadReviews, loadRuntimeControl, pollRuntimeOperation, toast])
  const runAgentCheck = useCallback(async (agent: string) => {
    const expectedIdentity = loadedAgentsIdentity
    const startRepositoryEpoch = agentsRepositoryEpoch.current
    if (!expectedIdentity || expectedIdentity !== selectedRepositoryIdentity) return
    setBusy(true)
    try {
      await request('/api/agents/check', { method: 'POST', body: JSON.stringify({ agent, repositoryIdentity: expectedIdentity }) })
      if (expectedIdentity !== currentRepositoryIdentity.current || startRepositoryEpoch !== agentsRepositoryEpoch.current) return
      // A same-repository GET may supersede this request, but that is only
      // request ordering. The health operation remains authoritative while
      // the repository identity/epoch is unchanged; the newest same-scope
      // GET owns the cards and completion still gets surfaced.
      const reloadOutcome = await loadAgents(expectedIdentity)
      if (expectedIdentity === currentRepositoryIdentity.current && startRepositoryEpoch === agentsRepositoryEpoch.current && (reloadOutcome === 'applied' || reloadOutcome === 'superseded-applied')) toast('success', t('review.agentCheckCompleted', { agent }))
    } catch (error) {
      if (expectedIdentity !== currentRepositoryIdentity.current || startRepositoryEpoch !== agentsRepositoryEpoch.current) return
      toast('error', (error as Error).message)
      throw error
    } finally { setBusy(false) }
  }, [loadAgents, loadedAgentsIdentity, selectedRepositoryIdentity, toast])

  const runningCount = jobs.filter(isRunning).length
  const daemonRunning = Boolean(bootstrap?.roborev?.status?.running)
  const selectedPanel = panelConfiguration?.selectedPanel ?? ''
  const selectedPanelInfo = panelConfiguration?.panels.find(panel => panel.name === selectedPanel)
  const panelSelectionValue = selectedPanelInfo?.valid ? selectedPanel : ''
  const panelReady = Boolean(repo && !panelLoading && !panelError && panelConfiguration?.repositoryIdentity === repo.identity && !panelConfiguration.selectionRequired && selectedPanelInfo?.valid)
  const panelMode = selectedPanelInfo ? panelLabel(selectedPanelInfo, t) : panelLoading ? t('panel.loading') : panelError ? t('panel.unavailable') : t('review.selectPanel')
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
      if (!target?.identity || !target.path || !target.headShort) throw new APIError(t('error.reviewTargetMissing'))
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
      toast('success', t('review.newHeadQueued', { head: target.headShort }))
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
      toast('success', t('panel.started'))
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
      toast('success', t('panel.cancelRequested'))
      setPanelRefineReloadEpoch(epoch => epoch + 1)
    } catch (error) {
      toast('error', (error as Error).message)
      throw error
    } finally { setBusy(false) }
  }
  const openReviewById = (id: number) => {
    setActiveNav('Reviews')
    setMenu(false)
    // The Tasks slice can contain rows beyond the polling list's window.
    const existing = jobs.find(job => idOf(job) === id) ?? taskJobs.find(job => idOf(job) === id)
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
    // Keep the old coordinator alive until the backend confirms the selected
    // repository. While that round trip is pending, suppress event refreshes
    // and prevent old GETs from committing; a failed switch can therefore
    // resume the same A coordinator without losing its fallback polling.
    reviewSwitchInProgress.current = true
    setupReadyGeneration.current++
    setRepoSwitching(true)
    reviewActionGeneration.current++
    panelRefineReviewGeneration.current++
    reviewGeneration.current++
    panelGeneration.current++
    panelRefineGeneration.current++
    // Invalidate the old repository's agent cards immediately, before the
    // asynchronous repository selection/bootstrap round trip completes. This
    // is request ordering only; the repository epoch changes only once the
    // authoritative bootstrap reports a different selected identity.
    agentsRequestGeneration.current++
    setAgents(null)
    setLoadedAgentsIdentity('')
    setAgentsError('')
    setAgentsLoading(false)
    setReviewPollError('')
    setLoadedReviewIdentity('')
    setPanelConfiguration(null)
    setPanelError('')
    setPanelRefineRun(null)
    setPanelRefineOptions(null)
    setPanelRefineError('')
    setPanelRefineLoading(true)
    setFixTargetJobId(null)
    setPanelLoading(true)
    setJobs([])
    setSelected(null)
    // Task history is scoped to one repository; a switch must not show the
    // previous repository's rows or its empty state while reloading, and any
    // in-flight history request for the old repository is invalidated.
    taskHistoryGeneration.current++
    setTaskJobs([])
    loadedTaskIdentityRef.current = ''
    setLoadedTaskIdentity('')
    setTaskHistoryError('')
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
      if (!restored) throw new APIError(t('error.bootstrapRepositoryMissing'))
      // Move the request scope forward before the eager review GET below;
      // React's identity effect will mirror this ref after the render.
      currentRepositoryIdentity.current = data.selectedRepositoryIdentity
      setPanelLoadEpoch(epoch => epoch + 1)
      setAgentsReloadEpoch(epoch => epoch + 1)
      if (selectError) toast('error', selectError.message)
      else toast('success', t('review.stateUpdated'))
      reviewSwitchInProgress.current = false
      await loadReviews(data.selectedRepository, data.selectedRepositoryIdentity)
    } catch (error) {
      if (generation !== repoSwitchGeneration.current) return
      toast('error', selectError?.message ?? (error as Error).message)
      setPanelError(t('error.repositoryRestoreFailed', { error: (error as Error).message }))
      setPanelLoading(false)
    } finally {
      if (generation === repoSwitchGeneration.current) {
        reviewSwitchInProgress.current = false
        setRepoSwitching(false)
      }
    }
  }

  const reviewSupported = Boolean(bootstrap?.roborev?.capabilities?.reviewEnqueueApi)
  const panelRefineActive = isPanelRefineActive(panelRefineRun)
  const setupOperationActive = setupIsActive(setupState)
  const panelOutputStatusAvailable = bootstrap?.roborev?.capabilities?.jobOutputStatusApi === true
  const panelRefineAvailable = Boolean(bootstrap?.roborev?.installed && daemonRunning && bootstrap.roborev.capabilities.panelReview && bootstrap.roborev.capabilities.fixCommand && panelOutputStatusAvailable)
  const panelRefineDisabledReason = !bootstrap?.roborev?.installed ? t('panel.unavailableNotInstalled') : !daemonRunning ? t('panel.unavailableDaemonOffline') : !bootstrap.roborev.capabilities.panelReview ? t('panel.versionNoPanel') : !bootstrap.roborev.capabilities.fixCommand ? t('panel.versionNoFix') : !panelOutputStatusAvailable ? t('panel.versionNoStatus') : ''
  const reviewsReady = !canPollReviews || !selectedRepositoryIdentity || loadedReviewIdentity === selectedRepositoryIdentity
  const reviewPageActive = activeNav === 'Reviews' || activeNav === 'Overview'
  const prepareFix = (job: ReviewJob) => {
    const id = idOf(job)
    if (!isFixTarget(job) || !Number.isSafeInteger(id) || id <= 0) return
    setFixTargetJobId(id)
    setSelected(null)
    setActiveNav('Fix & Refine')
    setMenu(false)
  }
  const refreshAfterFix = useCallback(async () => {
    const data = await loadBootstrap()
    const selectedRepo = data.repositories.find(item => item.identity === data.selectedRepositoryIdentity)
    if (!selectedRepo) return
    setPanelLoadEpoch(epoch => epoch + 1)
    setPanelRefineReloadEpoch(epoch => epoch + 1)
    await Promise.allSettled([loadReviews(selectedRepo.path, selectedRepo.identity), loadPanels(selectedRepo.path, selectedRepo.identity)])
  }, [loadBootstrap, loadPanels, loadReviews])
  // Mirror for the review polling effect, which must not re-subscribe SSE on
  // navigation changes just to learn whether the Tasks slice is visible.
  tasksVisibleRef.current = activeNav === 'Tasks'
  const loadedTaskHistoryForScope = loadedTaskIdentity === selectedRepositoryIdentity && !taskHistoryError
  useEffect(() => {
    if (activeNav !== 'Tasks' || repoSwitching) return
    const identity = selectedRepositoryIdentity
    const path = repo?.path
    if (!identity || !path) return
    // An explicit retry bumps the epoch so a scope that looks loaded does not
    // swallow the refetch after a failed refresh.
    if (loadedTaskHistoryForScope && lastTaskSliceEpoch.current === taskReloadEpoch) return
    lastTaskSliceEpoch.current = taskReloadEpoch
    void loadTaskHistory(path, identity)
  }, [activeNav, repo?.path, selectedRepositoryIdentity, loadedTaskHistoryForScope, taskReloadEpoch, repoSwitching, loadTaskHistory])
  const taskSliceInFlight = canPollReviews && Boolean(selectedRepositoryIdentity) && !loadedTaskHistoryForScope && !taskHistoryError
  return <div className="shell">
    <aside className={`sidebar ${menu ? 'open' : ''}`}><div className="brand"><span className="brand-mark"><Bot /></span><div><strong>RoboRev</strong><span>{t('brand.dashboard')}</span></div></div><nav>{navigation.map(([label, Icon]) => { const key = navigationLabelKeys[label]; return <button key={label} className={activeNav === label ? 'active' : ''} onClick={() => { setActiveNav(label); setMenu(false) }}><Icon />{t(key)}{!['Overview', 'Reviews', 'Fix & Refine', 'Analyze', 'Tasks', 'Repositories', 'Agents & Setup'].includes(label) && <small>{t('nav.soon')}</small>}</button> })}</nav><div className="sidebar-foot"><LanguageSelector /><span className={`health ${daemonRunning ? 'online' : ''}`} /><div><strong>{daemonRunning ? t('status.daemonOnline') : t('status.daemonOffline')}</strong><span>{bootstrap?.roborev?.version || t('status.setupRequired')}</span></div></div></aside>
    <main className="main"><header className="topbar"><button className="icon-button mobile-menu" aria-label={t('nav.openMenu')} onClick={() => setMenu(old => !old)}><Menu /></button><div className="repo-switch"><span>{t('repo.repository')}</span><select aria-label={t('repo.currentAria')} value={selectedRepositoryIdentity} disabled={repoSwitching || setupOperationActive} onChange={event => switchRepo(event.target.value)}>{bootstrap?.repositories.map(item => <option value={item.identity} key={item.identity}>{item.name} · {item.branch}</option>)}</select></div>{repo && <div className="repo-meta"><span><GitBranch size={15} />{repo.branch}</span><span><GitCommit size={15} />{repo.headShort}</span>{repo.dirty && <span className="dirty"><AlertTriangle size={14} />{t('status.dirty')}</span>}</div>}<div className="top-spacer" /><div className="panel-control"><span>{t('review.mode')}</span><select aria-label={t('review.panelAria')} value={panelSelectionValue} disabled={busy || setupOperationActive || repoSwitching || panelLoading || Boolean(panelError) || !repo} onChange={event => selectPanel(event.target.value)}>{!panelSelectionValue && <option value="">{t('review.selectPanel')}</option>}{panelConfiguration?.panels.map(panel => <option key={panel.name} value={panel.name} disabled={!panel.valid}>{panelLabel(panel, t)}</option>)}</select><small>{panelMode}</small></div><span className="running"><Activity size={15} />{t('review.runningCount', { count: runningCount })}</span><button className="button primary review-head" disabled={busy || setupOperationActive || repoSwitching || panelRefineActive || panelRefineLoading || !reviewsReady || !panelReady || !repo || !bootstrap?.roborev?.installed || !daemonRunning || !reviewSupported} onClick={reviewHead}><Play size={17} />{t('review.reviewHead')}</button></header>
      {!loading && bootstrap && setupState && activeNav !== 'Agents & Setup' && <SetupBanner state={setupState} busy={busy || setupOperationActive} onInstall={() => { setSetupConfirming(true); return Promise.resolve() }} onRetry={() => setupMutation('/api/setup/roborev/retry')} onStartDaemon={() => runDaemonAction('start')} />}
      {!loading && bootstrap && !setupState && (!bootstrap.roborev?.installed || !daemonRunning || !reviewSupported) && <section className="setup-banner"><Server /><div><strong>{!bootstrap.roborev?.installed ? t('setup.notInstalled') : !reviewSupported ? t('setup.upgradeRequired') : t('setup.daemonStopped')}</strong><p>{setupError || bootstrap.roborev?.error || (!reviewSupported ? t('setup.metadataContractUnavailable') : bootstrap.roborev?.installed ? t('setup.startDaemonHint') : t('setup.configureBinaryHint'))}</p></div>{bootstrap.roborev?.installed && reviewSupported && !daemonRunning && <button className="button" disabled={busy || setupOperationActive} onClick={() => runDaemonAction('start')}>{t('setup.daemonStart')}</button>}</section>}
      {panelError && <section className="setup-banner" role="alert"><AlertTriangle /><div><strong>{t('panel.configurationUnavailable')}</strong><p>{panelError}</p></div>{repo && <button className="button" disabled={panelLoading} onClick={() => loadPanels(repo.path, repo.identity)}>{t('panel.retryDiscovery')}</button>}</section>}
      {(reviewPollError || runtimePollError) && <section className="setup-banner" role="alert"><AlertTriangle /><div><strong>{t('error.backgroundRefresh')}</strong><p>{reviewPollError || runtimePollError}</p></div></section>}
      {loading || (reviewPageActive && !reviewsReady) ? <div className="page-loading"><div className="skeleton" /><div className="skeleton tall" /></div> : reviewPageActive ? <>{activeNav === 'Reviews' && panelRefineRun && (panelRefineActive || panelRefineRun.phase === 'needs_attention') && <PanelRefineSummary run={panelRefineRun} onOpen={() => { setActiveNav('Fix & Refine'); setMenu(false) }} />}<ReviewList jobs={jobs} selected={selected ? idOf(selected) : undefined} onSelect={setSelected} /></> : activeNav === 'Analyze' ? <Analyze repoIdentity={selectedRepositoryIdentity} /> : activeNav === 'Tasks' ? (taskSliceInFlight ? <div className="page-loading"><div className="skeleton" /><div className="skeleton tall" /></div> : <Tasks key={`${selectedRepositoryIdentity}:ready`} repository={repo ?? null} jobs={taskJobs} historyError={taskHistoryError} onRetryHistory={() => { setTaskHistoryError(''); setTaskReloadEpoch(epoch => epoch + 1) }} onOpenReview={openReviewById} />) : activeNav === 'Fix & Refine' ? <FixRefine key={`${selectedRepositoryIdentity}:${repoSwitching ? 'switching' : 'ready'}`} repoPath={repo?.path ?? ''} repoIdentity={repo?.identity ?? ''} jobs={jobs} selectedJobId={fixTargetJobId} onSelectedJobIdChange={setFixTargetJobId} onOpenReview={openReviewById} onTerminal={refreshAfterFix} onToast={toast} panelRefine={{ repoPath: repo?.path ?? '', panelConfiguration, options: panelRefineOptions, run: panelRefineRun, available: panelRefineAvailable, disabledReason: panelRefineDisabledReason, loading: panelRefineLoading, error: panelRefineError, busy: busy || setupOperationActive || repoSwitching, onStart: startPanelRefine, onCancel: cancelPanelRefine, onOpenReview: openReviewById }} /> : activeNav === 'Repositories' ? <Repositories bootstrap={bootstrap} mutate={mutate} busy={busy || setupOperationActive} /> : activeNav === 'Agents & Setup' ? <Setup key={`${selectedRepositoryIdentity}:${repoSwitching ? 'switching' : 'ready'}`} state={setupState} busy={busy || setupOperationActive || repoSwitching} onInstall={() => setupMutation('/api/setup/roborev/install')} onRetry={() => setupMutation('/api/setup/roborev/retry')} onStartDaemon={() => runDaemonAction('start')} agents={loadedAgentsIdentity === selectedRepositoryIdentity ? agents : null} agentsLoading={agentsLoading} agentsError={agentsError} onRefreshAgents={async () => { if (selectedRepositoryIdentity) await loadAgents(selectedRepositoryIdentity) }} onCheckAgent={runAgentCheck} runtimeControl={runtimeControl} runtimeLoading={runtimeControlLoading} runtimeError={runtimeControlError} onRefreshRuntime={async () => { await loadRuntimeControl() }} onDaemonAction={runDaemonAction} onQueueAction={runQueueAction} panelRefineActive={panelRefineActive || panelRefineLoading} /> : <section className="coming"><TerminalSquare /><span className="eyebrow">{t('roadmap.title')}</span><h1>{navigationLabel(activeNav, t)}</h1><p>{t('roadmap.body')}</p></section>}
    </main>
    {selected && <ReviewDetail key={idOf(selected)} job={selected} jobs={jobs} close={() => setSelected(null)} mutate={mutate} busy={busy || setupOperationActive || !canPollReviews} canRerun={Boolean(bootstrap?.roborev?.capabilities?.frozenRerunApi) && !setupOperationActive && !panelRefineActive && !panelRefineLoading} canLoadMembers={Boolean(bootstrap?.roborev?.capabilities?.panelMembersApi && selected.panel_run_uuid)} repoPath={repo?.path ?? ''} repoIdentity={repo?.identity ?? ''} toast={toast} onPrepareFix={prepareFix} />}
    {setupConfirming && setupState && !setupState.installed && !setupOperationActive && <InstallConfirmation state={setupState} busy={busy} onCancel={() => setSetupConfirming(false)} onInstall={() => { setSetupConfirming(false); void setupMutation('/api/setup/roborev/install') }} />}
    <div className="toasts" aria-live="polite">{toasts.map(item => <div key={item.id} className={`toast ${item.kind}`}>{item.kind === 'success' ? <Check /> : <XCircle />}{item.message}</div>)}</div>
  </div>
}

function Repositories({ bootstrap, mutate, busy }: { bootstrap: Bootstrap | null; mutate: (path: string, body?: unknown) => Promise<void>; busy: boolean }) {
  const { t } = useI18n()
  const [path, setPath] = useState('')
  return <section className="cards-page"><header className="section-heading"><div><span className="eyebrow">{t('repo.registeredTargets')}</span><h1>{t('nav.repositories')}</h1><p>{t('repo.onlyCanonical')}</p></div></header><div className="repo-cards">{bootstrap?.repositories.map(repo => <article key={repo.identity}><div className="repo-icon"><GitBranch /></div><div><h3>{repo.name}</h3><code>{repo.path}</code><p>{repo.branch} · {repo.headShort} {repo.dirty ? `· ${t('status.dirty').toLowerCase()}` : `· ${t('status.clean').toLowerCase()}`}</p></div></article>)}</div><form className="add-repo" onSubmit={event => { event.preventDefault(); mutate('/api/repos', { repoPath: path }).then(() => setPath('')) }}><label htmlFor="repo-path">{t('repo.addLocal')}</label><div><input id="repo-path" value={path} onChange={event => setPath(event.target.value)} placeholder={t('repo.pathPlaceholder')} /><button className="button primary" disabled={busy || !path.trim()}><Plus size={16} />{t('repo.register')}</button></div></form></section>
}
