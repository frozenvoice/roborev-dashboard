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

export type SetupPhase = 'idle' | 'checking' | 'downloading' | 'verifying' | 'extracting' | 'installing' | 'binding' | 'starting_daemon' | 'waiting_for_daemon' | 'ready' | 'failed' | 'blocked' | 'unsupported' | 'canceled'

export type SetupState = {
  status: SetupPhase
  phase: SetupPhase
  operationActive: boolean
  installed: boolean
  binaryPath?: string
  version?: string
  recommendedVersion: string
  compatible?: boolean
  panelRefineCompatible?: boolean
  daemonRunning: boolean
  blocked: boolean
  error?: string
  failureCode?: string
  retryable: boolean
  installAvailable: boolean
  canInstall: boolean
  canRetry: boolean
  canStartDaemon: boolean
  architecture?: string
  sourceURL?: string
  sourceLabel?: string
  assetName?: string
  sha256?: string
  targetPath?: string
  downloadedBytes?: number
  totalBytes?: number
  startedAt?: string
  updatedAt?: string
  completedAt?: string
  capabilities?: Record<string, unknown>
}

/** The health result is deliberately separate from executable/config detection. */
export type AgentHealth = 'not_checked' | 'passed' | 'failed' | 'skipped' | 'canceled'

export type AgentCommand = string | { executable: string; args: string[] }

export type ConfiguredReviewAgent = {
  name: string
  agent: string
  model?: string
  reasoning?: string
  command: AgentCommand
  commandSource?: string
  detected: boolean
  detectionReason?: string
  resolvedPath?: string
  health: AgentHealth
  lastCheckedAt?: string
}

export type AgentsResponse = {
  roborevVersion?: string
  configured: ConfiguredReviewAgent[]
}

export type QueueState = 'processing' | 'paused' | 'unknown'

export type DaemonAction = 'start' | 'stop' | 'restart'
export type DaemonCommandPreview = { executable: string; args: string[] }
export type DaemonCommands = Record<DaemonAction, DaemonCommandPreview>

export type RuntimeControlState = {
  daemonRunning: boolean
  daemonOperation?: string
  operationActive: boolean
  queueState: QueueState
  canPause: boolean
  canResume: boolean
  canStart: boolean
  canStop: boolean
  canRestart: boolean
  daemonCommands?: DaemonCommands
  disabledReason?: string
  error?: string
}

const agentHealthValues: AgentHealth[] = ['not_checked', 'passed', 'failed', 'skipped', 'canceled']
const queueStateValues: QueueState[] = ['processing', 'paused', 'unknown']

function objectValue(value: unknown) {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null
}

function optionalString(value: unknown) { return typeof value === 'string' ? value : undefined }
function readAlias(object: Record<string, unknown>, camel: string, snake: string) { return object[camel] ?? object[snake] }

function normalizeDaemonCommands(value: unknown): DaemonCommands | undefined {
  const object = objectValue(value)
  if (!object) return undefined
  const actions: DaemonAction[] = ['start', 'stop', 'restart']
  const result = {} as DaemonCommands
  for (const action of actions) {
    const command = objectValue(object[action])
    if (!command || typeof command.executable !== 'string' || !command.executable.trim() || !Array.isArray(command.args) || !command.args.every(item => typeof item === 'string')) return undefined
    result[action] = { executable: command.executable, args: command.args as string[] }
  }
  return result
}

function normalizeAgentHealth(value: unknown): AgentHealth {
  if (typeof value !== 'string') return 'not_checked'
  const normalized = value.toLowerCase().replace(/[ -]/g, '_')
  return agentHealthValues.includes(normalized as AgentHealth) ? normalized as AgentHealth : 'not_checked'
}

function normalizeAgentCommand(value: unknown): AgentCommand {
  if (typeof value === 'string') return value
  const object = objectValue(value)
  if (object && typeof object.executable === 'string' && Array.isArray(object.args) && object.args.every(item => typeof item === 'string')) return { executable: object.executable, args: object.args as string[] }
  return 'Command supplied by RoboRev'
}

/** Parse the machine-readable configured-agent response. Unknown fields are ignored. */
export function normalizeAgentsResponse(value: unknown): AgentsResponse | null {
  const object = objectValue(value)
  if (!object || !Array.isArray(object.configured)) return null
  const configured: ConfiguredReviewAgent[] = []
  for (const item of object.configured) {
    const agent = objectValue(item)
    if (!agent || typeof agent.name !== 'string' || typeof agent.agent !== 'string' || typeof agent.detected !== 'boolean') return null
    configured.push({
      name: agent.name,
      agent: agent.agent,
      model: optionalString(agent.model),
      reasoning: optionalString(agent.reasoning),
      command: normalizeAgentCommand(agent.command),
      commandSource: optionalString(readAlias(agent, 'commandSource', 'command_source')),
      detected: agent.detected,
      detectionReason: optionalString(readAlias(agent, 'detectionReason', 'detection_reason')),
      resolvedPath: optionalString(readAlias(agent, 'resolvedPath', 'resolved_path')),
      health: normalizeAgentHealth(agent.health),
      lastCheckedAt: optionalString(readAlias(agent, 'lastCheckedAt', 'last_checked_at')),
    })
  }
  return { roborevVersion: optionalString(readAlias(object, 'roborevVersion', 'roborev_version')), configured }
}

/** Parse runtime/queue capabilities and fail closed to unknown queue controls. */
export function normalizeRuntimeControl(value: unknown): RuntimeControlState | null {
  const object = objectValue(value)
  if (!object || typeof object.daemonRunning !== 'boolean') return null
  const operation = readAlias(object, 'operationActive', 'operation_active')
  const queue = readAlias(object, 'queueState', 'queue_state')
  const operationActive = operation === undefined ? false : operation === true
  const queueState = typeof queue === 'string' && queueStateValues.includes(queue as QueueState) ? queue as QueueState : 'unknown'
  const readCapability = (camel: string, snake: string) => object[camel] === true || object[snake] === true
  const daemonCommands = normalizeDaemonCommands(readAlias(object, 'daemonCommands', 'daemon_commands'))
  return {
    daemonRunning: object.daemonRunning,
    daemonOperation: optionalString(readAlias(object, 'daemonOperation', 'daemon_operation')),
    operationActive,
    queueState,
    canPause: readCapability('canPause', 'can_pause'),
    canResume: readCapability('canResume', 'can_resume'),
    canStart: readCapability('canStart', 'can_start'),
    canStop: readCapability('canStop', 'can_stop'),
    canRestart: readCapability('canRestart', 'can_restart'),
    daemonCommands,
    disabledReason: optionalString(readAlias(object, 'disabledReason', 'disabled_reason')),
    error: optionalString(object.error),
  }
}

const setupPhases: SetupPhase[] = ['idle', 'checking', 'downloading', 'verifying', 'extracting', 'installing', 'binding', 'starting_daemon', 'waiting_for_daemon', 'ready', 'failed', 'blocked', 'unsupported', 'canceled']
const setupActivePhases: SetupPhase[] = ['checking', 'downloading', 'verifying', 'extracting', 'installing', 'binding', 'starting_daemon', 'waiting_for_daemon']
function setupField(object: Record<string, unknown>, ...names: string[]) {
  for (const name of names) if (object[name] !== undefined) return object[name]
  return undefined
}
const setupSnakeNames: Record<string, string> = { operationActive: 'operation_active', panelRefineCompatible: 'panel_refine_compatible', recommendedVersion: 'recommended_version', binaryPath: 'binary_path', daemonRunning: 'daemon_running', installAvailable: 'install_available', canInstall: 'can_install', canRetry: 'can_retry', canStartDaemon: 'can_start_daemon', sourceURL: 'source_url', sourceLabel: 'source_label', assetName: 'asset_name', downloadedBytes: 'downloaded_bytes', totalBytes: 'total_bytes', startedAt: 'started_at', updatedAt: 'updated_at', completedAt: 'completed_at', failureCode: 'failure_code', targetPath: 'target_path' }
function setupSnakeName(field: string) { return setupSnakeNames[field] ?? field.replace(/[A-Z]/g, letter => `_${letter.toLowerCase()}`) }
function finiteNumber(value: unknown) { return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : undefined }

/** Parse the typed setup state and fail closed when the server sends an unusable envelope. */
export function normalizeSetupState(value: unknown): SetupState | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null
  const object = value as Record<string, unknown>
  const phaseValue = object.phase
  const statusValue = object.status
  if (typeof phaseValue !== 'string' || typeof statusValue !== 'string' || phaseValue !== statusValue || !setupPhases.includes(phaseValue as SetupPhase)) return null
  const phase = phaseValue as SetupPhase
  const operationValue = setupField(object, 'operationActive', 'operation_active')
  if (typeof operationValue !== 'boolean') return null
  const installedValue = setupField(object, 'installed')
  if (typeof installedValue !== 'boolean') return null
  const recommended = setupField(object, 'recommendedVersion', 'recommended_version')
  if (typeof recommended !== 'string' || recommended !== 'v0.64.0') return null
  const booleanFields = ['compatible', 'panelRefineCompatible', 'daemonRunning', 'blocked', 'retryable', 'installAvailable', 'canInstall', 'canRetry', 'canStartDaemon', 'operationActive'] as const
  for (const field of booleanFields) {
    const valueForField = setupField(object, field, setupSnakeName(field))
    if (valueForField !== undefined && typeof valueForField !== 'boolean') return null
  }
  const numberFields = ['downloadedBytes', 'totalBytes'] as const
  for (const field of numberFields) {
    const valueForField = setupField(object, field, setupSnakeName(field))
    if (valueForField !== undefined && finiteNumber(valueForField) === undefined) return null
  }
  const stringFields = ['binaryPath', 'version', 'error', 'failureCode', 'architecture', 'sourceURL', 'sourceLabel', 'assetName', 'sha256', 'targetPath', 'startedAt', 'updatedAt', 'completedAt'] as const
  for (const field of stringFields) {
    const valueForField = setupField(object, field, setupSnakeName(field))
    if (valueForField !== undefined && typeof valueForField !== 'string') return null
  }
  const capabilities = setupField(object, 'capabilities')
  if (capabilities !== undefined && (!capabilities || typeof capabilities !== 'object' || Array.isArray(capabilities))) return null
  const readBoolean = (field: typeof booleanFields[number], fallback: boolean) => {
    const valueForField = setupField(object, field, setupSnakeName(field))
    return typeof valueForField === 'boolean' ? valueForField : fallback
  }
  const readOptionalBoolean = (field: typeof booleanFields[number]) => {
    const valueForField = setupField(object, field, setupSnakeName(field))
    return typeof valueForField === 'boolean' ? valueForField : undefined
  }
  const readString = (field: typeof stringFields[number]) => {
    const valueForField = setupField(object, field, setupSnakeName(field))
    return typeof valueForField === 'string' ? valueForField : undefined
  }
  const readNumber = (field: typeof numberFields[number]) => {
    const valueForField = setupField(object, field, setupSnakeName(field))
    return finiteNumber(valueForField)
  }
  const active = readBoolean('operationActive', false)
  if (active !== setupActivePhases.includes(phase)) return null
  if (phase === 'ready' && (!installedValue || readOptionalBoolean('compatible') !== true || readOptionalBoolean('panelRefineCompatible') === undefined)) return null
  if (phase === 'unsupported' && (!installedValue || readOptionalBoolean('compatible') !== false)) return null
  if (phase === 'blocked' && !readBoolean('blocked', false)) return null
  if ((phase === 'idle' || phase === 'canceled') && installedValue) return null
  if (['failed', 'blocked', 'unsupported', 'canceled', 'ready'].includes(phase) && readBoolean('canInstall', false)) return null
  if (installedValue && readBoolean('canInstall', false)) return null
  const installFields = ['architecture', 'sourceURL', 'sourceLabel', 'assetName', 'sha256', 'targetPath'] as const
  for (const field of installFields) {
    const fieldValue = readString(field)
    if (!fieldValue?.trim()) return null
  }
  const digest = readString('sha256')
  if (!digest || !/^[a-f0-9]{64}$/i.test(digest)) return null
  const totalBytes = readNumber('totalBytes')
  const downloadedBytes = readNumber('downloadedBytes')
  if (totalBytes === undefined || totalBytes <= 0 || (downloadedBytes !== undefined && downloadedBytes > totalBytes)) return null
  const source = readString('sourceURL')
  try {
    const sourceURL = new URL(source || '')
    if (sourceURL.protocol !== 'https:' || sourceURL.hostname.toLowerCase() !== 'github.com' || !sourceURL.pathname.toLowerCase().startsWith('/kenn-io/roborev/')) return null
  } catch { return null }
  return {
    status: phase,
    phase,
    operationActive: active,
    installed: typeof installedValue === 'boolean' ? installedValue : false,
    binaryPath: readString('binaryPath'),
    version: readString('version'),
    recommendedVersion: recommended,
    compatible: readOptionalBoolean('compatible'),
    panelRefineCompatible: readOptionalBoolean('panelRefineCompatible'),
    daemonRunning: readBoolean('daemonRunning', false),
    blocked: readBoolean('blocked', phase === 'blocked'),
    error: readString('error'),
    failureCode: readString('failureCode'),
    retryable: readBoolean('retryable', false),
    installAvailable: readBoolean('installAvailable', false),
    canInstall: readBoolean('canInstall', false),
    canRetry: readBoolean('canRetry', false),
    canStartDaemon: readBoolean('canStartDaemon', false),
    architecture: readString('architecture'),
    sourceURL: readString('sourceURL'),
    sourceLabel: readString('sourceLabel'),
    assetName: readString('assetName'),
    sha256: readString('sha256'),
    targetPath: readString('targetPath'),
    downloadedBytes,
    totalBytes,
    startedAt: readString('startedAt'),
    updatedAt: readString('updatedAt'),
    completedAt: readString('completedAt'),
    capabilities: capabilities as Record<string, unknown> | undefined,
  }
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
