export type Repository = {
  identity: string
  name: string
  path: string
  branch: string
  head: string
  headShort: string
  commitSubject: string
  dirty: boolean
  hasConfig: boolean
}

export type Installation = {
  installed: boolean
  binary?: string
  version?: string
  status?: { running?: boolean; known?: boolean; queued?: number; active?: number; [key: string]: unknown }
  error?: string
  capabilities: {
    reviewEnqueueApi: boolean
    frozenRerunApi: boolean
    jobListApi: boolean
    jobOutputStatusApi: boolean
    panelReview: boolean
    fixCommand: boolean
    panelMembersApi: boolean
    promptLazyLoad: boolean
    rawLog: boolean
  }
}

export type Bootstrap = {
  csrfToken: string
  repositories: Repository[]
  selectedRepository: string
  selectedRepositoryIdentity: string
  roborev: Installation
}

export type ReviewEnqueueResponse = { receipt: unknown; target: Repository }

export type PanelRefinePhase = 'idle' | 'starting' | 'reviewing' | 'evaluating' | 'fixing' | 'final_reviewing' | 'passed' | 'needs_attention' | 'failed' | 'cancelled'

export type PanelRefineIteration = {
  number: number
  targetHead: string
  panel: string
  reviewJobID?: number
  panelRunUUID?: string
  reviewResult?: string
  fixAttempted?: boolean
  fixAgent?: string
  headAfterFix?: string
  error?: string
}

export type PanelRefineRun = {
  id: number | string
  repositoryIdentity: string
  repositoryPath: string
  branch?: string
  iterationPanel: string
  finalPanel?: string
  fixAgent: string
  maxFixAttempts: number
  fixAttempts: number
  phase: PanelRefinePhase
  startingHead?: string
  currentHead?: string
  currentReviewJobID?: number
  currentPanelRunUUID?: string
  startedAt?: string
  updatedAt?: string
  completedAt?: string
  error?: string
  stopReason?: string
  command: { executable: string; args: string[] }
  cancelState?: { available: boolean; head?: string; branch?: string; dirty?: boolean; error?: string }
  iterations: PanelRefineIteration[]
}

export type PanelRefineOptions = {
  executable: string
  args: string[]
  allowedFixAgents: string[]
  defaultFixAgent: string
  defaultMaxFixAttempts: number
  minFixAttempts: number
  maxFixAttempts: number
}

export type PanelRefineResponse = {
  runs: PanelRefineRun[]
  run: PanelRefineRun | null
  options: PanelRefineOptions | null
}

function numberValue(value: unknown) {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}
function parsePanelRefineOptions(value: unknown): PanelRefineOptions | null {
  if (!value || typeof value !== 'object') return null
  const object = value as Record<string, unknown>
  if (typeof object.executable !== 'string' || !Array.isArray(object.args) || !object.args.every(item => typeof item === 'string') || !Array.isArray(object.allowedFixAgents) || !object.allowedFixAgents.every(item => typeof item === 'string') || typeof object.defaultFixAgent !== 'string') return null
  const defaultMax = numberValue(object.defaultMaxFixAttempts)
  const min = numberValue(object.minFixAttempts)
  const max = numberValue(object.maxFixAttempts)
  if (defaultMax === undefined || min === undefined || max === undefined || min < 1 || max > 10 || min > max || defaultMax < min || defaultMax > max) return null
  return { executable: object.executable, args: object.args as string[], allowedFixAgents: object.allowedFixAgents as string[], defaultFixAgent: object.defaultFixAgent, defaultMaxFixAttempts: defaultMax, minFixAttempts: min, maxFixAttempts: max }
}

function parsePanelRefineRun(value: unknown): PanelRefineRun | null {
  if (!value || typeof value !== 'object') return null
  const object = value as Record<string, unknown>
  const phases: PanelRefinePhase[] = ['idle', 'starting', 'reviewing', 'evaluating', 'fixing', 'final_reviewing', 'passed', 'needs_attention', 'failed', 'cancelled']
  const phase = object.phase
  const command = object.command
  const commandObject = command && typeof command === 'object' ? command as Record<string, unknown> : null
  const commandArgs = commandObject?.args
  if ((typeof object.id !== 'string' && typeof object.id !== 'number') || typeof object.repositoryIdentity !== 'string' || typeof object.repositoryPath !== 'string' || typeof object.iterationPanel !== 'string' || typeof object.fixAgent !== 'string' || !phases.includes(phase as PanelRefinePhase) || numberValue(object.maxFixAttempts) === undefined || numberValue(object.fixAttempts) === undefined || !Array.isArray(object.iterations) || !commandObject || typeof commandObject.executable !== 'string' || !Array.isArray(commandArgs) || !commandArgs.every((item: unknown) => typeof item === 'string')) return null
  const iterations: PanelRefineIteration[] = []
  for (const item of object.iterations) {
    if (!item || typeof item !== 'object') return null
    const entry = item as Record<string, unknown>
    if (numberValue(entry.number) === undefined || typeof entry.targetHead !== 'string' || typeof entry.panel !== 'string') return null
    iterations.push({ number: entry.number as number, targetHead: entry.targetHead, panel: entry.panel, reviewJobID: numberValue(entry.reviewJobId), panelRunUUID: typeof entry.panelRunUUID === 'string' ? entry.panelRunUUID : undefined, reviewResult: typeof entry.reviewResult === 'string' ? entry.reviewResult : undefined, fixAttempted: entry.fixAttempted === true, fixAgent: typeof entry.fixAgent === 'string' ? entry.fixAgent : undefined, headAfterFix: typeof entry.headAfterFix === 'string' ? entry.headAfterFix : undefined, error: typeof entry.error === 'string' ? entry.error : undefined })
  }
  const cancel = object.cancelState
  const cancelState = cancel && typeof cancel === 'object' ? { available: (cancel as Record<string, unknown>).available === true, head: typeof (cancel as Record<string, unknown>).head === 'string' ? (cancel as Record<string, unknown>).head as string : undefined, branch: typeof (cancel as Record<string, unknown>).branch === 'string' ? (cancel as Record<string, unknown>).branch as string : undefined, dirty: (cancel as Record<string, unknown>).dirty === true, error: typeof (cancel as Record<string, unknown>).error === 'string' ? (cancel as Record<string, unknown>).error as string : undefined } : undefined
  return { id: object.id as number | string, repositoryIdentity: object.repositoryIdentity, repositoryPath: object.repositoryPath, branch: typeof object.branch === 'string' ? object.branch : undefined, iterationPanel: object.iterationPanel, finalPanel: typeof object.finalPanel === 'string' ? object.finalPanel : undefined, fixAgent: object.fixAgent, maxFixAttempts: object.maxFixAttempts as number, fixAttempts: object.fixAttempts as number, phase: phase as PanelRefinePhase, startingHead: typeof object.startingHead === 'string' ? object.startingHead : undefined, currentHead: typeof object.currentHead === 'string' ? object.currentHead : undefined, currentReviewJobID: numberValue(object.currentReviewJobId), currentPanelRunUUID: typeof object.currentPanelRunUUID === 'string' ? object.currentPanelRunUUID : undefined, startedAt: typeof object.startedAt === 'string' ? object.startedAt : undefined, updatedAt: typeof object.updatedAt === 'string' ? object.updatedAt : undefined, completedAt: typeof object.completedAt === 'string' ? object.completedAt : undefined, error: typeof object.error === 'string' ? object.error : undefined, stopReason: typeof object.stopReason === 'string' ? object.stopReason : undefined, command: { executable: commandObject.executable as string, args: commandArgs as string[] }, cancelState, iterations }
}

export function normalizePanelRefineResponse(value: unknown): PanelRefineResponse {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return { runs: [], run: null, options: null }
  const object = value as Record<string, unknown>
  if (!Array.isArray(object.runs)) return { runs: [], run: null, options: null }
  const runs = object.runs.map(parsePanelRefineRun).filter((run): run is PanelRefineRun => Boolean(run))
  const active = (phase: PanelRefinePhase) => ['starting', 'reviewing', 'evaluating', 'fixing', 'final_reviewing'].includes(phase)
  runs.sort((left, right) => {
    if (active(left.phase) !== active(right.phase)) return active(left.phase) ? -1 : 1
    return String(right.updatedAt ?? right.startedAt ?? '').localeCompare(String(left.updatedAt ?? left.startedAt ?? ''))
  })
  return { runs, run: runs[0] ?? null, options: parsePanelRefineOptions(object.options) }
}

export type PanelInfo = {
  name: string
  kind: 'panel' | 'single'
  members: string[]
  synthesisAgent?: string
  synthesisBackupAgent?: string
  origin: 'global' | 'local' | 'builtin'
  valid: boolean
  error?: string
}

export type PanelConfiguration = {
  repositoryIdentity: string
  panels: PanelInfo[]
  selectedPanel: string
  selectionRequired: boolean
  error?: string
}

export type FixPromptResponse = { text: string; jobId: number; stale: boolean }

export type ReviewJob = {
  id: number
  job_id?: number
  uuid?: string
  repo_path?: string
  repo_name?: string
  git_ref?: string
  commit_sha?: string
  branch?: string
  subject?: string
  commit_subject?: string
  job_type?: string
  agent?: string
  model?: string
  reasoning?: string
  status?: string
  verdict?: string
  review?: { output?: string; verdict?: string; [key: string]: unknown } | string
  output?: string
  addressed?: boolean
  closed?: boolean
  created_at?: string
  completed_at?: string
  enqueued_at?: string
  started_at?: string
  finished_at?: string
  panel_name?: string
  panel_run_uuid?: string
  panel_role?: string
  parent_job_id?: number
  panel_member_index?: number
  member_job_ids?: number[]
  duration_ms?: number
  [key: string]: unknown
}

let csrfToken = ''
export function setCSRF(value: string) { csrfToken = value }

export class APIError extends Error {
  phase?: string
  constructor(message: string, phase?: string) { super(message); this.phase = phase }
}

export async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const headers = new Headers(options.headers)
  if (options.body) headers.set('Content-Type', 'application/json')
  if ((options.method ?? 'GET') !== 'GET') headers.set('X-CSRF-Token', csrfToken)
  const response = await fetch(path, { ...options, headers })
  const text = await response.text()
  let payload: unknown = undefined
  if (text) {
    try { payload = JSON.parse(text) } catch { payload = text }
  }
  if (!response.ok) {
    const problem = payload as { error?: string; phase?: string } | undefined
    throw new APIError(problem?.error ?? `Request failed (${response.status})`, problem?.phase)
  }
  return payload as T
}

export function jobsFromPayload(value: unknown): ReviewJob[] {
  if (Array.isArray(value)) return value as ReviewJob[]
  if (value && typeof value === 'object') {
    const object = value as Record<string, unknown>
    for (const key of ['jobs', 'reviews', 'items']) if (Array.isArray(object[key])) return object[key] as ReviewJob[]
  }
  return []
}
