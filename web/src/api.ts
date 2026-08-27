export const TASK_HISTORY_LIMIT = 200

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

export type RuntimeDiagnosticsComponent = {
  name: string
  healthy: boolean
}

export type RuntimeDiagnostics = {
  daemon: {
    running: boolean
    version: string
    uptime: string
  }
  workers: {
    active: number
    max: number
  }
  queue: {
    paused: boolean
    queued: number
    running: number
  }
  jobs: {
    completed: number
    failed: number
    skipped: number
  }
  health: {
    healthy: boolean
    errorCount24h: number
    components: RuntimeDiagnosticsComponent[]
  }
}

export type AgentHookReadinessAgent = 'codex' | 'claude' | 'grok' | 'droid'
export type AgentHookReadinessStatus = 'current' | 'needs-update' | 'unavailable'
export type AgentHookReadinessAgentState = {
  agent: AgentHookReadinessAgent
  status: AgentHookReadinessStatus
}
export type AgentHookReadiness = {
  version: 'v0.64.0'
  agents: AgentHookReadinessAgentState[]
}

export type SkillsReadinessAgent = 'codex' | 'claude' | 'grok' | 'droid'
export type SkillsReadinessStatus = 'current' | 'needs-install-update' | 'config-unavailable'
export type SkillsReadinessAgentState = {
  agent: SkillsReadinessAgent
  status: SkillsReadinessStatus
  current: number
  outdated: number
  missing: number
  total: 10
}
export type SkillsReadiness = {
  version: 'v0.64.0'
  agents: SkillsReadinessAgentState[]
}

const skillsReadinessVersion = 'v0.64.0' as const
const skillsReadinessAgents: readonly SkillsReadinessAgent[] = ['codex', 'claude', 'grok', 'droid']
const skillsReadinessStatuses: readonly SkillsReadinessStatus[] = ['current', 'needs-install-update', 'config-unavailable']
const skillsReadinessTotal = 10 as const

/** Normalize the content-free, fixed-catalog Skills Readiness DTO. */
export function normalizeSkillsReadiness(value: unknown): SkillsReadiness | null {
  const root = exactObject(value, ['version', 'agents'])
  if (!root || root.version !== skillsReadinessVersion || !Array.isArray(root.agents) || root.agents.length !== skillsReadinessAgents.length) return null
  const agents: SkillsReadinessAgentState[] = []
  for (let index = 0; index < skillsReadinessAgents.length; index += 1) {
    const item = exactObject(root.agents[index], ['agent', 'status', 'current', 'outdated', 'missing', 'total'])
    const expectedAgent = skillsReadinessAgents[index]
    if (!item || item.agent !== expectedAgent || typeof item.status !== 'string' || !skillsReadinessStatuses.includes(item.status as SkillsReadinessStatus)) return null
    if (item.total !== skillsReadinessTotal || !nonNegativeSafeInteger(item.current) || !nonNegativeSafeInteger(item.outdated) || !nonNegativeSafeInteger(item.missing)) return null
    const status = item.status as SkillsReadinessStatus
    const current = item.current as number
    const outdated = item.outdated as number
    const missing = item.missing as number
    if (status === 'current' && (current !== skillsReadinessTotal || outdated !== 0 || missing !== 0)) return null
    if (status === 'needs-install-update' && (current + outdated + missing !== skillsReadinessTotal || outdated + missing === 0)) return null
    if (status === 'config-unavailable' && (current !== 0 || outdated !== 0 || missing !== 0)) return null
    agents.push({ agent: expectedAgent, status, current, outdated, missing, total: skillsReadinessTotal })
  }
  return { version: skillsReadinessVersion, agents }
}

export type SkillsManagementAction = 'install-update'
export type SkillsManagementCommand = {
  executable: 'roborev'
  args: string[]
}
export type SkillsManagementPreview = {
  token: string
  agent: SkillsReadinessAgent
  action: SkillsManagementAction
  summary: string
  command: SkillsManagementCommand
  expiresAt: string
}
export type SkillsManagementResultStatus = 'verified-success' | 'partial' | 'failed-unverified'
export type SkillsManagementErrorCode = 'cli-failed' | 'cli-not-started' | 'binding-drift' | 'readback-unavailable' | 'target-drift' | 'readback-not-current' | 'no-improvement'
export type SkillsManagementResult = {
  status: SkillsManagementResultStatus
  agent: SkillsReadinessAgent
  errorCode?: SkillsManagementErrorCode
}

const skillsManagementActions: readonly SkillsManagementAction[] = ['install-update']
const skillsManagementResultStatuses: readonly SkillsManagementResultStatus[] = ['verified-success', 'partial', 'failed-unverified']
const skillsManagementErrorCodes: readonly SkillsManagementErrorCode[] = ['cli-failed', 'cli-not-started', 'binding-drift', 'readback-unavailable', 'target-drift', 'readback-not-current', 'no-improvement']
const skillsManagementAgentLabels: Record<SkillsReadinessAgent, string> = { codex: 'Codex', claude: 'Claude Code', grok: 'Grok Build', droid: 'Factory Droid' }
const skillsManagementPathPlaceholder = '<agent-skills-directory>'

function strictRfc3339(value: unknown): value is string {
  if (typeof value !== 'string') return false
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?(Z|[+-](\d{2}):(\d{2}))$/.exec(value)
  if (!match || Number.isNaN(Date.parse(value))) return false
  const year = Number(match[1])
  const month = Number(match[2])
  const day = Number(match[3])
  const hour = Number(match[4])
  const minute = Number(match[5])
  const second = Number(match[6])
  const offsetHour = match[8] ? Number(match[8]) : 0
  const offsetMinute = match[9] ? Number(match[9]) : 0
  const leapYear = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0)
  const daysInMonth = [31, leapYear ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31][month - 1] ?? 0
  return month >= 1 && month <= 12 && day >= 1 && day <= daysInMonth && hour <= 23 && minute <= 59 && second <= 59 && offsetHour <= 23 && offsetMinute <= 59
}

/** Normalize the sanitized single-agent Skills Management preview. */
export function normalizeSkillsManagementPreview(value: unknown, expectedAgent?: SkillsReadinessAgent): SkillsManagementPreview | null {
  const root = exactObject(value, ['token', 'agent', 'action', 'summary', 'command', 'expiresAt'])
  if (!root || typeof root.token !== 'string' || !root.token.trim() || typeof root.agent !== 'string' || !skillsReadinessAgents.includes(root.agent as SkillsReadinessAgent) || (expectedAgent !== undefined && root.agent !== expectedAgent) || typeof root.action !== 'string' || !skillsManagementActions.includes(root.action as SkillsManagementAction) || typeof root.summary !== 'string' || !strictRfc3339(root.expiresAt)) return null
  const agent = root.agent as SkillsReadinessAgent
  if (root.summary !== `Install or update RoboRev skills for ${skillsManagementAgentLabels[agent]}`) return null
  const command = exactObject(root.command, ['executable', 'args'])
  const expectedArgs = ['skills', 'install', '--path', skillsManagementPathPlaceholder, '--agent', agent]
  if (!command || command.executable !== 'roborev' || !Array.isArray(command.args) || command.args.length !== expectedArgs.length || !command.args.every((item, index) => item === expectedArgs[index])) return null
  return { token: root.token, agent, action: root.action as SkillsManagementAction, summary: root.summary, command: { executable: 'roborev', args: [...expectedArgs] }, expiresAt: root.expiresAt }
}

/** Normalize the typed post-execute Skills Management receipt. */
export function normalizeSkillsManagementResult(value: unknown, expectedAgent?: SkillsReadinessAgent): SkillsManagementResult | null {
  const root = exactObject(value, ['status', 'agent']) ?? exactObject(value, ['status', 'agent', 'errorCode'])
  if (!root || typeof root.status !== 'string' || !skillsManagementResultStatuses.includes(root.status as SkillsManagementResultStatus) || typeof root.agent !== 'string' || !skillsReadinessAgents.includes(root.agent as SkillsReadinessAgent) || (expectedAgent !== undefined && root.agent !== expectedAgent)) return null
  const status = root.status as SkillsManagementResultStatus
  const errorCode = root.errorCode
  if (errorCode !== undefined && (typeof errorCode !== 'string' || !skillsManagementErrorCodes.includes(errorCode as SkillsManagementErrorCode))) return null
  if ((status === 'verified-success' || status === 'partial') && errorCode !== undefined && errorCode !== 'cli-failed') return null
  if (status === 'failed-unverified' && (typeof errorCode !== 'string' || !skillsManagementErrorCodes.includes(errorCode as SkillsManagementErrorCode))) return null
  return { status, agent: root.agent as SkillsReadinessAgent, ...(errorCode === undefined ? {} : { errorCode: errorCode as SkillsManagementErrorCode }) }
}

const agentHookReadinessVersion = 'v0.64.0' as const
const agentHookReadinessAgents: readonly AgentHookReadinessAgent[] = ['codex', 'claude', 'grok', 'droid']
const agentHookReadinessStatuses: readonly AgentHookReadinessStatus[] = ['current', 'needs-update', 'unavailable']

/** Normalize the content-free, fixed-order Agent Hook Readiness DTO. */
export function normalizeAgentHookReadiness(value: unknown): AgentHookReadiness | null {
  const root = exactObject(value, ['version', 'agents'])
  if (!root || root.version !== agentHookReadinessVersion || !Array.isArray(root.agents) || root.agents.length !== agentHookReadinessAgents.length) return null
  const agents: AgentHookReadinessAgentState[] = []
  for (let index = 0; index < agentHookReadinessAgents.length; index += 1) {
    const item = exactObject(root.agents[index], ['agent', 'status'])
    const expectedAgent = agentHookReadinessAgents[index]
    if (!item || item.agent !== expectedAgent || typeof item.status !== 'string' || !agentHookReadinessStatuses.includes(item.status as AgentHookReadinessStatus)) return null
    agents.push({ agent: expectedAgent, status: item.status as AgentHookReadinessStatus })
  }
  return { version: agentHookReadinessVersion, agents }
}

export type AgentHookManagementAction = 'install-update'
export type AgentHookManagementCommand = {
  executable: 'roborev'
  args: string[]
}
export type AgentHookManagementPreview = {
  token: string
  agent: AgentHookReadinessAgent
  action: AgentHookManagementAction
  summary: string
  command: AgentHookManagementCommand
  expiresAt: string
}
export type AgentHookManagementResultStatus = 'verified-success' | 'failed-unverified'
export type AgentHookManagementErrorCode = 'cli-failed' | 'cli-not-started' | 'binding-drift' | 'readback-unavailable' | 'readback-not-current'
export type AgentHookManagementResult = {
  status: AgentHookManagementResultStatus
  agent: AgentHookReadinessAgent
  errorCode?: AgentHookManagementErrorCode
}

const agentHookManagementActions: readonly AgentHookManagementAction[] = ['install-update']
const agentHookManagementResultStatuses: readonly AgentHookManagementResultStatus[] = ['verified-success', 'failed-unverified']
const agentHookManagementErrorCodes: readonly AgentHookManagementErrorCode[] = ['cli-failed', 'cli-not-started', 'binding-drift', 'readback-unavailable', 'readback-not-current']
const agentHookManagementBinaryPlaceholder = '<current-v0.64.0-binary>'
const agentHookManagementAgentLabels: Record<AgentHookReadinessAgent, string> = { codex: 'Codex', claude: 'Claude', grok: 'Grok Build', droid: 'Factory Droid' }

/** Normalize the sanitized single-agent management preview; actual paths never cross this boundary. */
export function normalizeAgentHookManagementPreview(value: unknown, expectedAgent?: AgentHookReadinessAgent): AgentHookManagementPreview | null {
  const root = exactObject(value, ['token', 'agent', 'action', 'summary', 'command', 'expiresAt'])
  if (!root || typeof root.token !== 'string' || !root.token.trim() || typeof root.agent !== 'string' || !agentHookReadinessAgents.includes(root.agent as AgentHookReadinessAgent) || (expectedAgent !== undefined && root.agent !== expectedAgent) || typeof root.action !== 'string' || !agentHookManagementActions.includes(root.action as AgentHookManagementAction) || typeof root.summary !== 'string' || !rfc3339(root.expiresAt)) return null
  const agent = root.agent as AgentHookReadinessAgent
  if (root.summary !== `Install or update RoboRev hooks for ${agentHookManagementAgentLabels[agent]}`) return null
  const command = exactObject(root.command, ['executable', 'args'])
  const expectedArgs = ['agent-hook', 'install', '--agent', agent, '--binary', agentHookManagementBinaryPlaceholder]
  if (!command || command.executable !== 'roborev' || !Array.isArray(command.args) || command.args.length !== expectedArgs.length || !command.args.every((item, index) => item === expectedArgs[index])) return null
  return { token: root.token, agent, action: root.action as AgentHookManagementAction, summary: root.summary, command: { executable: 'roborev', args: [...expectedArgs] }, expiresAt: root.expiresAt }
}

/** Normalize the typed post-execute receipt without exposing CLI output. */
export function normalizeAgentHookManagementResult(value: unknown, expectedAgent?: AgentHookReadinessAgent): AgentHookManagementResult | null {
  const root = exactObject(value, ['status', 'agent']) ?? exactObject(value, ['status', 'agent', 'errorCode'])
  if (!root || typeof root.status !== 'string' || !agentHookManagementResultStatuses.includes(root.status as AgentHookManagementResultStatus) || typeof root.agent !== 'string' || !agentHookReadinessAgents.includes(root.agent as AgentHookReadinessAgent) || (expectedAgent !== undefined && root.agent !== expectedAgent)) return null
  const status = root.status as AgentHookManagementResultStatus
  const errorCode = root.errorCode
  if (errorCode !== undefined && (typeof errorCode !== 'string' || !agentHookManagementErrorCodes.includes(errorCode as AgentHookManagementErrorCode))) return null
  if ((status === 'verified-success' && errorCode !== undefined && errorCode !== 'cli-failed') || (status === 'failed-unverified' && errorCode === undefined)) return null
  return { status, agent: root.agent as AgentHookReadinessAgent, ...(errorCode === undefined ? {} : { errorCode: errorCode as AgentHookManagementErrorCode }) }
}

export type GitHookOverallStatus = 'ready' | 'needs-attention' | 'not-installed' | 'unknown'
export type GitHookPostCommitStatus = 'current' | 'outdated' | 'not-installed' | 'unknown'
export type GitHookPostRewriteStatus = 'current' | 'outdated' | 'missing' | 'not-installed' | 'unknown'

export type GitHookStatus = {
  repositoryIdentity: string
  status: GitHookOverallStatus
  postCommit: { status: GitHookPostCommitStatus }
  postRewrite: { status: GitHookPostRewriteStatus }
}

type CIWorkflowPreviewBase = {
  version: 'v0.64.0'
  repositoryIdentity: string
  target: '.github/workflows/roborev.yml'
  workflow: {
    content: string
    sha256: string
  }
}

export type RepositorySetupReadiness = {
  version: 'v0.64.0'
  repositoryIdentity: string
  overall: 'ready' | 'needs-attention' | 'unavailable'
  configuration: { status: 'ready' | 'unavailable' }
  snapshot: { status: 'ready' | 'not-ignored' | 'tracked-files' | 'unsafe' | 'unavailable' }
  gitHooks: { status: 'ready' | 'needs-attention' | 'not-installed' | 'unknown' }
  daemon: { status: 'online' | 'offline' | 'unavailable' }
  tracking: { status: 'tracked' | 'not-tracked' | 'unavailable' }
}

/** The browser-facing repository handle is a server-issued, opaque alias. */
export type RepositoryIdentityAlias = string
const repositoryIdentityAliasPattern = /^[A-Za-z0-9_-]{43}$/

function validRepositoryIdentityAlias(value: unknown): value is RepositoryIdentityAlias {
  return typeof value === 'string' && repositoryIdentityAliasPattern.test(value)
}

const setupReadinessVersion = 'v0.64.0' as const
const setupReadinessOverall = ['ready', 'needs-attention', 'unavailable'] as const
const setupReadinessConfiguration = ['ready', 'unavailable'] as const
const setupReadinessSnapshot = ['ready', 'not-ignored', 'tracked-files', 'unsafe', 'unavailable'] as const
const setupReadinessHooks = ['ready', 'needs-attention', 'not-installed', 'unknown'] as const
const setupReadinessDaemon = ['online', 'offline', 'unavailable'] as const
const setupReadinessTracking = ['tracked', 'not-tracked', 'unavailable'] as const

/** Normalize the exact status-only Repository Setup Readiness DTO. */
export function normalizeRepositorySetupReadiness(value: unknown, expectedRepositoryIdentity?: string): RepositorySetupReadiness | null {
  const root = exactObject(value, ['version', 'repositoryIdentity', 'overall', 'configuration', 'snapshot', 'gitHooks', 'daemon', 'tracking'])
  // `expectedRepositoryIdentity` is retained as a source-compatible argument
  // for callers that used the v1 normalizer.  It is deliberately not used for
  // correlation: the response identity is now a server-issued opaque alias,
  // while the request still carries the legacy selected-repository identity.
  void expectedRepositoryIdentity
  if (!root || root.version !== setupReadinessVersion || !validRepositoryIdentityAlias(root.repositoryIdentity)) return null
  const configuration = exactStatus(root.configuration, setupReadinessConfiguration)
  const snapshot = exactStatus(root.snapshot, setupReadinessSnapshot)
  const gitHooks = exactStatus(root.gitHooks, setupReadinessHooks)
  const daemon = exactStatus(root.daemon, setupReadinessDaemon)
  const tracking = exactStatus(root.tracking, setupReadinessTracking)
  if (!configuration || !snapshot || !gitHooks || !daemon || !tracking || !setupReadinessOverall.includes(root.overall as typeof setupReadinessOverall[number])) return null
  const overall = root.overall as RepositorySetupReadiness['overall']
  if (configuration.status === 'unavailable' && snapshot.status !== 'unavailable') return null
  if (daemon.status !== 'online' && tracking.status !== 'unavailable') return null
  const ready = configuration.status === 'ready' && snapshot.status === 'ready' && gitHooks.status === 'ready' && daemon.status === 'online' && tracking.status === 'tracked'
  const unavailable = snapshot.status === 'unsafe' || gitHooks.status === 'unknown' || daemon.status === 'unavailable' || configuration.status === 'unavailable' || snapshot.status === 'unavailable' || (daemon.status === 'online' && tracking.status === 'unavailable')
  const derived = ready ? 'ready' : unavailable ? 'unavailable' : 'needs-attention'
  if (overall !== derived) return null
  return { version: setupReadinessVersion, repositoryIdentity: root.repositoryIdentity, overall, configuration, snapshot, gitHooks, daemon, tracking } as RepositorySetupReadiness
}

export type SnapshotIgnorePreview = {
  version: 'v0.64.0'
  repositoryIdentity: RepositoryIdentityAlias
  action: 'append-snapshot-ignore'
  target: '.gitignore'
  summary: 'Add the RoboRev snapshot directory to Git ignore rules.'
  token: string
  expiresAt: string
}

export type SnapshotIgnoreExecuteResult = { status: 'verified-success' | 'failed-unverified' }

const snapshotIgnoreVersion = 'v0.64.0' as const
const snapshotIgnoreAction = 'append-snapshot-ignore' as const
const snapshotIgnoreTarget = '.gitignore' as const
const snapshotIgnoreSummary = 'Add the RoboRev snapshot directory to Git ignore rules.' as const
// RFC 3339 is intentionally stricter than Date.parse (which accepts many
// browser-specific date formats).  Date.parse then rejects impossible dates.
const rfc3339Pattern = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/
const snapshotIgnoreMaxTokenLength = 4096

/** Normalize the exact, content-free Snapshot Ignore preview DTO. */
export function normalizeSnapshotIgnorePreview(value: unknown, expectedRepositoryIdentity?: string): SnapshotIgnorePreview | null {
  const root = exactObject(value, ['version', 'repositoryIdentity', 'action', 'target', 'summary', 'token', 'expiresAt'])
  if (!root || root.version !== snapshotIgnoreVersion || !validRepositoryIdentityAlias(root.repositoryIdentity) || (expectedRepositoryIdentity !== undefined && root.repositoryIdentity !== expectedRepositoryIdentity) || root.action !== snapshotIgnoreAction || root.target !== snapshotIgnoreTarget || root.summary !== snapshotIgnoreSummary) return null
  if (typeof root.token !== 'string' || !root.token.trim() || root.token.length > snapshotIgnoreMaxTokenLength || /[\u0000-\u001F\u007F]/.test(root.token)) return null
  if (typeof root.expiresAt !== 'string' || !rfc3339Pattern.test(root.expiresAt) || !Number.isFinite(Date.parse(root.expiresAt))) return null
  return { version: snapshotIgnoreVersion, repositoryIdentity: root.repositoryIdentity, action: snapshotIgnoreAction, target: snapshotIgnoreTarget, summary: snapshotIgnoreSummary, token: root.token, expiresAt: root.expiresAt }
}

/** Normalize the exact, content-free Snapshot Ignore execute result DTO. */
export function normalizeSnapshotIgnoreExecuteResult(value: unknown): SnapshotIgnoreExecuteResult | null {
  const root = exactObject(value, ['status'])
  if (!root || (root.status !== 'verified-success' && root.status !== 'failed-unverified')) return null
  return { status: root.status }
}

function exactStatus<T extends readonly string[]>(value: unknown, allowed: T): { status: T[number] } | null {
  const object = exactObject(value, ['status'])
  if (!object || typeof object.status !== 'string' || !allowed.includes(object.status as T[number])) return null
  return { status: object.status as T[number] }
}

export type CIWorkflowExistingComparison = 'exact-match' | 'different' | 'unavailable'
export type CIWorkflowExisting = { comparison: CIWorkflowExistingComparison }

export type CIWorkflowPreview =
  | (CIWorkflowPreviewBase & { write: { status: 'exists' }; existing: CIWorkflowExisting })
  | (CIWorkflowPreviewBase & { write: Exclude<CIWorkflowWrite, { status: 'exists' }>; existing?: never })

export type CIWorkflowWrite =
  | { status: 'available'; token: string; expiresAt: string }
  | { status: 'exists' }
  | { status: 'unsafe' | 'ignored' | 'dirty' }

export type CIWorkflowExecuteResult = { status: 'verified-created' | 'failed-unverified' }

const ciWorkflowVersion = 'v0.64.0' as const
const ciWorkflowTarget = '.github/workflows/roborev.yml' as const
const ciWorkflowMaxBytes = 256 * 1024
const ciWorkflowExistingComparisons: readonly CIWorkflowExistingComparison[] = ['exact-match', 'different', 'unavailable']

function validUtf8WorkflowContent(value: unknown): value is string {
  if (typeof value !== 'string' || value.length === 0) return false
  // TextEncoder replaces unpaired surrogates, so reject those explicitly
  // before applying the byte limit. This keeps the accepted value valid UTF-8
  // rather than silently changing the generated workflow in the browser.
  for (let index = 0; index < value.length; index += 1) {
    const code = value.charCodeAt(index)
    if (code >= 0xd800 && code <= 0xdbff) {
      const next = value.charCodeAt(index + 1)
      if (Number.isNaN(next) || next < 0xdc00 || next > 0xdfff) return false
      index += 1
      continue
    }
    if (code >= 0xdc00 && code <= 0xdfff) return false
    if ((code <= 0x1f && code !== 0x09 && code !== 0x0a && code !== 0x0d) || (code >= 0x7f && code <= 0x9f)) return false
  }
  return new TextEncoder().encode(value).byteLength <= ciWorkflowMaxBytes
}

/** Normalize the strict, read-only CI workflow preview DTO. */
export function normalizeCIWorkflowPreview(value: unknown, expectedRepositoryIdentity?: string): CIWorkflowPreview | null {
  const root = objectValue(value)
  if (!root || (Object.getPrototypeOf(root) !== Object.prototype && Object.getPrototypeOf(root) !== null) || Object.keys(root).some(key => !['version', 'repositoryIdentity', 'target', 'workflow', 'write', 'existing'].includes(key)) || !['version', 'repositoryIdentity', 'target', 'workflow', 'write'].every(key => Object.prototype.hasOwnProperty.call(root, key))) return null
  if (!root || root.version !== ciWorkflowVersion || root.target !== ciWorkflowTarget || typeof root.repositoryIdentity !== 'string' || !root.repositoryIdentity.trim() || (expectedRepositoryIdentity !== undefined && root.repositoryIdentity !== expectedRepositoryIdentity)) return null
  const workflow = exactObject(root.workflow, ['content', 'sha256'])
  if (!workflow || !validUtf8WorkflowContent(workflow.content) || typeof workflow.sha256 !== 'string' || !/^[0-9a-f]{64}$/.test(workflow.sha256)) return null
  const write = normalizeCIWorkflowWrite(root.write)
  if (!write) return null
  if (write.status === 'exists') {
    const existing = exactObject(root.existing, ['comparison'])
    if (!existing || typeof existing.comparison !== 'string' || !ciWorkflowExistingComparisons.includes(existing.comparison as CIWorkflowExistingComparison)) return null
    return { version: ciWorkflowVersion, repositoryIdentity: root.repositoryIdentity, target: ciWorkflowTarget, workflow: { content: workflow.content, sha256: workflow.sha256 }, write, existing: { comparison: existing.comparison as CIWorkflowExistingComparison } }
  }
  if (Object.prototype.hasOwnProperty.call(root, 'existing')) return null
  return { version: ciWorkflowVersion, repositoryIdentity: root.repositoryIdentity, target: ciWorkflowTarget, workflow: { content: workflow.content, sha256: workflow.sha256 }, write }
}

function normalizeCIWorkflowWrite(value: unknown): CIWorkflowWrite | null {
  const object = objectValue(value)
  if (!object || typeof object.status !== 'string') return null
  if (object.status === 'available') {
    const available = exactObject(object, ['status', 'token', 'expiresAt'])
    if (!available || typeof available.token !== 'string' || !available.token.trim() || available.token.length > 4096 || typeof available.expiresAt !== 'string' || !available.expiresAt.trim() || !Number.isFinite(Date.parse(available.expiresAt))) return null
    return { status: 'available', token: available.token, expiresAt: available.expiresAt }
  }
  if (object.status === 'exists' || object.status === 'unsafe' || object.status === 'ignored' || object.status === 'dirty') {
    const unavailable = exactObject(object, ['status'])
    return unavailable ? { status: object.status } : null
  }
  return null
}

export type GitHookClassification = 'absent' | 'unmanaged' | 'managed-current' | 'managed-outdated' | 'managed-stale-binary' | 'ambiguous' | 'unreadable'
export type GitHookManagementStatus = 'not-installed' | 'current' | 'outdated' | 'unknown'
export type GitHookManagementHook = {
  name: 'post-commit' | 'post-rewrite'
  status: GitHookManagementStatus
  classification: GitHookClassification
  managed: boolean
  shell: boolean
  binaryCurrent: boolean
}
export type GitHookAvailability = { available: boolean; recommended: boolean; reason: string }
export type GitHookSafety = {
  hooksDirectorySafe: boolean
  configurationMutationRequired: boolean
  unmanagedHookPresent: boolean
  nonShellHookPresent: boolean
  ambiguousRoborevContent: boolean
  unreadableHookPresent: boolean
}
export type GitHookManagement = {
  repositoryIdentity: string
  postCommit: GitHookManagementHook
  postRewrite: GitHookManagementHook
  install: GitHookAvailability
  repair: GitHookAvailability
  remove: GitHookAvailability
  safety: GitHookSafety
  summaryCode: string
  warningCodes: string[]
}
export type GitHookManagementCommand = { executable: string; args: string[] }
export type GitHookManagementPreview = {
  token: string
  action: GitHookAction
  repository: string
  preflight: GitHookManagement
  summary: string
  warningCodes: string[]
  command: GitHookManagementCommand
  expiresAt: string
}
export type GitHookAction = 'install' | 'repair' | 'remove'
export type GitHookManagementOutcome = 'verified-success' | 'partial' | 'failed-unverified'
export type GitHookManagementResult = {
  status: GitHookManagementOutcome
  action: GitHookAction
  preflight: GitHookManagement
  errorCode?: string
}

const gitHookOverallStatuses: GitHookOverallStatus[] = ['ready', 'needs-attention', 'not-installed', 'unknown']
const gitHookPostCommitStatuses: GitHookPostCommitStatus[] = ['current', 'outdated', 'not-installed', 'unknown']
const gitHookPostRewriteStatuses: GitHookPostRewriteStatus[] = ['current', 'outdated', 'missing', 'not-installed', 'unknown']

function validGitHookCombination(status: GitHookOverallStatus, postCommit: GitHookPostCommitStatus, postRewrite: GitHookPostRewriteStatus) {
  // When post-commit itself is unreadable, post-rewrite can still report a
  // factual marker state, but absence cannot be called not-installed because
  // the unreadable primary hook prevents that installation conclusion.
  if (postCommit === 'unknown') return status === 'unknown' && (postRewrite === 'current' || postRewrite === 'outdated' || postRewrite === 'unknown')
  // An unreadable post-rewrite hook makes the aggregate unavailable, including
  // when post-commit is known not-installed.
  if (postRewrite === 'unknown') return status === 'unknown'
  // A missing post-commit hook makes the aggregate not-installed, while a
  // RoboRev marker in post-rewrite remains factual (current/outdated).
  if (postCommit === 'not-installed') return status === 'not-installed' && (postRewrite === 'current' || postRewrite === 'outdated' || postRewrite === 'not-installed')
  // post-rewrite not-installed is only meaningful as part of the same absent
  // hook set; an installed post-commit hook with no rewrite hook is missing.
  if (postRewrite === 'not-installed') return false
  if (postCommit === 'current' && postRewrite === 'current') return status === 'ready'
  return status === 'needs-attention'
}

/** Normalize the strict, content-free Git hook status DTO. */
export function normalizeGitHookStatus(value: unknown, expectedRepositoryIdentity?: string): GitHookStatus | null {
  const object = objectValue(value)
  if (!object || !hasExactKeys(object, ['repositoryIdentity', 'status', 'postCommit', 'postRewrite'])) return null
  if (typeof object.repositoryIdentity !== 'string' || !object.repositoryIdentity.trim() || (expectedRepositoryIdentity !== undefined && object.repositoryIdentity !== expectedRepositoryIdentity)) return null
  if (typeof object.status !== 'string' || !gitHookOverallStatuses.includes(object.status as GitHookOverallStatus)) return null
  const postCommit = objectValue(object.postCommit)
  const postRewrite = objectValue(object.postRewrite)
  if (!postCommit || !postRewrite || !hasExactKeys(postCommit, ['status']) || !hasExactKeys(postRewrite, ['status'])) return null
  if (typeof postCommit.status !== 'string' || !gitHookPostCommitStatuses.includes(postCommit.status as GitHookPostCommitStatus)) return null
  if (typeof postRewrite.status !== 'string' || !gitHookPostRewriteStatuses.includes(postRewrite.status as GitHookPostRewriteStatus)) return null
  const normalizedPostCommit = postCommit.status as GitHookPostCommitStatus
  const normalizedPostRewrite = postRewrite.status as GitHookPostRewriteStatus
  const normalizedOverall = object.status as GitHookOverallStatus
  if (!validGitHookCombination(normalizedOverall, normalizedPostCommit, normalizedPostRewrite)) return null
  return { repositoryIdentity: object.repositoryIdentity, status: normalizedOverall, postCommit: { status: normalizedPostCommit }, postRewrite: { status: normalizedPostRewrite } }
}

const gitHookManagementClassifications: GitHookClassification[] = ['absent', 'unmanaged', 'managed-current', 'managed-outdated', 'managed-stale-binary', 'ambiguous', 'unreadable']
const gitHookManagementStatuses: GitHookManagementStatus[] = ['not-installed', 'current', 'outdated', 'unknown']
const gitHookManagementActions: GitHookAction[] = ['install', 'repair', 'remove']
const gitHookManagementPreviewExecutable = 'roborev'
const gitHookManagementPreviewBinary = '<current-v0.64.0-binary>'
const gitHookWarningCodes = ['relative-hooks-path-normalization', 'non-shell-hook', 'ambiguous-roborev-content', 'unsafe-hooks-directory']
const gitHookSummaryCodes = ['blocked', 'repair-recommended', 'managed-current', 'not-installed', 'attention']

function normalizeGitHookManagementHook(value: unknown): GitHookManagementHook | null {
  const object = objectValue(value)
  if (!object || !hasExactKeys(object, ['name', 'status', 'classification', 'managed', 'shell', 'binaryCurrent'])) return null
  if (object.name !== 'post-commit' && object.name !== 'post-rewrite') return null
  if (typeof object.status !== 'string' || !gitHookManagementStatuses.includes(object.status as GitHookManagementStatus)) return null
  if (typeof object.classification !== 'string' || !gitHookManagementClassifications.includes(object.classification as GitHookClassification)) return null
  if (typeof object.managed !== 'boolean' || typeof object.shell !== 'boolean' || typeof object.binaryCurrent !== 'boolean') return null
  const classification = object.classification as GitHookClassification
  const status = object.status as GitHookManagementStatus
  const managed = object.managed
  const binaryCurrent = object.binaryCurrent
  if (classification === 'absent' && (status !== 'not-installed' || managed || binaryCurrent)) return null
  if (classification === 'unmanaged' && (status !== 'not-installed' || managed || binaryCurrent)) return null
  if (classification === 'managed-current' && (status !== 'current' || !managed || !binaryCurrent)) return null
  if (classification === 'managed-outdated' && (status !== 'outdated' || !managed)) return null
  if (classification === 'managed-stale-binary' && (status !== 'outdated' || !managed || binaryCurrent)) return null
  if (classification === 'ambiguous' && (status !== 'outdated' || managed || binaryCurrent)) return null
  if (classification === 'unreadable' && (status !== 'unknown' || binaryCurrent)) return null
  return { name: object.name, status: object.status as GitHookManagementStatus, classification: object.classification as GitHookClassification, managed: object.managed, shell: object.shell, binaryCurrent: object.binaryCurrent }
}

const gitHookAvailabilityReasons = ['eligible', 'already-current', 'repair-recommended', 'managed-hook-needs-repair', 'no-managed-hook-needs-repair', 'managed-hook-present', 'no-managed-hook', 'unsafe-hooks-directory', 'relative-hooks-path-normalization', 'unreadable-hook', 'non-shell-hook', 'ambiguous-roborev-content']
function normalizeGitHookAvailability(value: unknown): GitHookAvailability | null {
  const object = objectValue(value)
  if (!object || Object.keys(object).some(key => !['available', 'recommended', 'reason'].includes(key)) || typeof object.available !== 'boolean') return null
  if (object.recommended !== undefined && typeof object.recommended !== 'boolean') return null
  if (object.reason !== undefined && (typeof object.reason !== 'string' || !gitHookAvailabilityReasons.includes(object.reason))) return null
  return { available: object.available, recommended: object.recommended === true, reason: typeof object.reason === 'string' ? object.reason : '' }
}

function normalizeGitHookSafety(value: unknown): GitHookSafety | null {
  const object = objectValue(value)
  if (!object || !hasExactKeys(object, ['hooksDirectorySafe', 'configurationMutationRequired', 'unmanagedHookPresent', 'nonShellHookPresent', 'ambiguousRoborevContent', 'unreadableHookPresent'])) return null
  for (const key of ['hooksDirectorySafe', 'configurationMutationRequired', 'unmanagedHookPresent', 'nonShellHookPresent', 'ambiguousRoborevContent', 'unreadableHookPresent'] as const) if (typeof object[key] !== 'boolean') return null
  return { hooksDirectorySafe: object.hooksDirectorySafe as boolean, configurationMutationRequired: object.configurationMutationRequired as boolean, unmanagedHookPresent: object.unmanagedHookPresent as boolean, nonShellHookPresent: object.nonShellHookPresent as boolean, ambiguousRoborevContent: object.ambiguousRoborevContent as boolean, unreadableHookPresent: object.unreadableHookPresent as boolean }
}

function normalizeGitHookCodes(value: unknown): string[] | null {
  if (!Array.isArray(value) || !value.every(item => typeof item === 'string' && gitHookWarningCodes.includes(item))) return null
  const result = value as string[]
  return new Set(result).size === result.length ? result : null
}

function sameGitHookCodes(left: readonly string[], right: readonly string[]) {
  return left.length === right.length && left.every((code, index) => code === right[index])
}

function expectedGitHookWarningCodes(safety: GitHookSafety) {
  const result: string[] = []
  if (safety.configurationMutationRequired) result.push('relative-hooks-path-normalization')
  if (safety.nonShellHookPresent) result.push('non-shell-hook')
  if (safety.ambiguousRoborevContent) result.push('ambiguous-roborev-content')
  if (!safety.hooksDirectorySafe) result.push('unsafe-hooks-directory')
  return result
}

function expectedGitHookSummaryCode(preflight: Pick<GitHookManagement, 'postCommit' | 'postRewrite' | 'repair' | 'safety'>): string {
  if (!preflight.safety.hooksDirectorySafe || preflight.safety.nonShellHookPresent || preflight.safety.ambiguousRoborevContent || preflight.safety.unreadableHookPresent) return 'blocked'
  if (preflight.repair.available) return 'repair-recommended'
  if (preflight.postCommit.classification === 'managed-current' && preflight.postRewrite.classification === 'managed-current') return 'managed-current'
  if (preflight.postCommit.classification === 'absent' && preflight.postRewrite.classification === 'absent') return 'not-installed'
  return 'attention'
}

/** Normalize the content-free, typed Git hook management preflight. */
export function normalizeGitHookManagement(value: unknown, expectedRepositoryIdentity?: string): GitHookManagement | null {
  const object = objectValue(value)
  if (!object || Object.keys(object).some(key => !['repositoryIdentity', 'postCommit', 'postRewrite', 'install', 'repair', 'remove', 'safety', 'summaryCode', 'warningCodes'].includes(key)) || !['repositoryIdentity', 'postCommit', 'postRewrite', 'install', 'repair', 'remove', 'safety', 'summaryCode'].every(key => Object.prototype.hasOwnProperty.call(object, key))) return null
  if (typeof object.repositoryIdentity !== 'string' || !object.repositoryIdentity.trim() || (expectedRepositoryIdentity !== undefined && object.repositoryIdentity !== expectedRepositoryIdentity)) return null
  const postCommit = normalizeGitHookManagementHook(object.postCommit)
  const postRewrite = normalizeGitHookManagementHook(object.postRewrite)
  const install = normalizeGitHookAvailability(object.install)
  const repair = normalizeGitHookAvailability(object.repair)
  const remove = normalizeGitHookAvailability(object.remove)
  const safety = normalizeGitHookSafety(object.safety)
  const warningCodes = object.warningCodes === undefined ? [] : normalizeGitHookCodes(object.warningCodes)
  if (!postCommit || !postRewrite || postCommit.name !== 'post-commit' || postRewrite.name !== 'post-rewrite' || !install || !repair || !remove || !safety || typeof object.summaryCode !== 'string' || !gitHookSummaryCodes.includes(object.summaryCode) || !warningCodes) return null
  const hooks = [postCommit, postRewrite]
  const unmanagedHookPresent = hooks.some(hook => hook.classification === 'unmanaged')
  const ambiguousRoborevContent = hooks.some(hook => hook.classification === 'ambiguous')
  const unreadableHookPresent = hooks.some(hook => hook.classification === 'unreadable')
  // The backend's Present bit is intentionally not exposed. Absent and IO
  // unreadable hooks cannot prove a present non-shell file. An unreadable
  // managed hook, however, comes from a present parser-error block and keeps
  // Managed=true, so it remains a known present hook for this safety flag.
  const nonShellHookPresent = hooks.some(hook => {
    const knownPresent = hook.classification !== 'absent' && (hook.classification !== 'unreadable' || hook.managed)
    return knownPresent && !hook.shell
  })
  if (safety.unmanagedHookPresent !== unmanagedHookPresent || safety.ambiguousRoborevContent !== ambiguousRoborevContent || safety.unreadableHookPresent !== unreadableHookPresent || safety.nonShellHookPresent !== nonShellHookPresent) return null
  if (!sameGitHookCodes(warningCodes, expectedGitHookWarningCodes(safety))) return null
  const normalizedSummaryCode = object.summaryCode as string
  if (normalizedSummaryCode !== expectedGitHookSummaryCode({ postCommit, postRewrite, repair, safety })) return null
  return { repositoryIdentity: object.repositoryIdentity, postCommit, postRewrite, install, repair, remove, safety, summaryCode: normalizedSummaryCode, warningCodes: warningCodes as string[] }
}

function normalizeGitHookManagementCommand(value: unknown, action: GitHookAction): GitHookManagementCommand | null {
  const object = objectValue(value)
  if (!object || !hasExactKeys(object, ['executable', 'args']) || object.executable !== gitHookManagementPreviewExecutable || !Array.isArray(object.args) || !object.args.every(item => typeof item === 'string')) return null
  const args = object.args as string[]
  const expected = action === 'install' ? ['install-hook', '--binary', gitHookManagementPreviewBinary] : action === 'repair' ? ['install-hook', 'repair', '--binary', gitHookManagementPreviewBinary] : ['uninstall-hook']
  if (args.length !== expected.length || args.some((arg, index) => arg !== expected[index]) || args.some(arg => ['--force', '--registered', 'agent-hook', 'init', 'status'].includes(arg))) return null
  return { executable: gitHookManagementPreviewExecutable, args }
}

/** Normalize a preview while enforcing the narrow Dashboard-owned CLI allowlist. */
export function normalizeGitHookManagementPreview(value: unknown, expectedRepositoryIdentity?: string, expectedAction?: GitHookAction): GitHookManagementPreview | null {
  const object = objectValue(value)
  if (!object || Object.keys(object).some(key => !['token', 'action', 'repository', 'preflight', 'summary', 'warningCodes', 'command', 'expiresAt'].includes(key)) || !['token', 'action', 'repository', 'preflight', 'summary', 'command', 'expiresAt'].every(key => Object.prototype.hasOwnProperty.call(object, key)) || typeof object.token !== 'string' || !object.token.trim() || typeof object.action !== 'string' || !gitHookManagementActions.includes(object.action as GitHookAction) || (expectedAction !== undefined && object.action !== expectedAction) || typeof object.repository !== 'string' || !object.repository.trim() || typeof object.summary !== 'string' || !object.summary.trim() || !rfc3339(object.expiresAt)) return null
  const action = object.action as GitHookAction
  const preflight = normalizeGitHookManagement(object.preflight, expectedRepositoryIdentity)
  const command = normalizeGitHookManagementCommand(object.command, action)
  const warningCodes = object.warningCodes === undefined ? [] : normalizeGitHookCodes(object.warningCodes)
  if (!preflight || !command || !warningCodes || warningCodes.length !== preflight.warningCodes.length || warningCodes.some((code, index) => code !== preflight.warningCodes[index]) || (expectedRepositoryIdentity !== undefined && preflight.repositoryIdentity !== expectedRepositoryIdentity)) return null
  return { token: object.token, action, repository: object.repository, preflight, summary: object.summary, warningCodes: preflight.warningCodes, command, expiresAt: object.expiresAt }
}

/** Normalize the typed, post-read Git hook mutation receipt. */
const gitHookResultErrorCodes = ['cli-failed', 'cli-not-started', 'readback-unavailable']
export function normalizeGitHookManagementResult(value: unknown, expectedRepositoryIdentity?: string, expectedAction?: GitHookAction): GitHookManagementResult | null {
  const object = objectValue(value)
  if (!object || (Object.keys(object).some(key => !['status', 'action', 'preflight', 'errorCode'].includes(key))) || typeof object.status !== 'string' || !['verified-success', 'partial', 'failed-unverified'].includes(object.status) || typeof object.action !== 'string' || !gitHookManagementActions.includes(object.action as GitHookAction) || (expectedAction !== undefined && object.action !== expectedAction) || object.errorCode !== undefined && (typeof object.errorCode !== 'string' || !gitHookResultErrorCodes.includes(object.errorCode))) return null
  const preflight = normalizeGitHookManagement(object.preflight, expectedRepositoryIdentity)
  if (!preflight) return null
  return { status: object.status as GitHookManagementOutcome, action: object.action as GitHookAction, preflight, errorCode: typeof object.errorCode === 'string' ? object.errorCode : undefined }
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
    /** Optional because older bootstrap responses predate the stream relay. */
    streamEvents?: boolean
    /** Optional because older bootstrap responses predate the config surface. */
    configIntrospection?: boolean
    configMutation?: boolean
    guidelineInsights?: boolean
    /** Optional because older bootstrap responses predate repository inventory. */
    repositoryInventory?: boolean
    /** Optional because older bootstrap responses predate repository rename. */
    repositoryRename?: boolean
    /** Optional because older bootstrap responses predate repository move. */
    repositoryMove?: boolean
    /** Optional because older bootstrap responses predate repository merge. */
    repositoryMerge?: boolean
  }
}

export type Bootstrap = {
  csrfToken: string
  repositories: Repository[]
  selectedRepository: string
  selectedRepositoryIdentity: string
  roborev: Installation
}

export type RepositoryInventoryRow = {
  name: string
  path: string
  jobCount: number
  dashboardRegistered: boolean
}

export type RepositoryInventoryResponse = {
  repositories: RepositoryInventoryRow[]
  repositoryCount: number
  totalJobCount: number
}

export type RepositoryRenameCommand = { executable: string; args: string[] }
export type RepositoryRenamePreview = {
  path: string
  currentName: string
  newName: string
  jobCount: number
  dashboardRegistered: boolean
  command: RepositoryRenameCommand
  token: string
  expiresAt: string
}
export type RepositoryRenameResult =
  | { status: 'renamed'; path: string; previousName: string; name: string }
  | { status: 'unverified'; path: string; message: string }

export type RepositoryMoveCommand = { executable: string; args: string[]; workingDirectory: string }
export type RepositoryMovePreview = {
  sourcePath: string
  sourceName: string
  jobCount: number
  destinationRepositoryIdentity: string
  destinationPath: string
  destinationName: string
  command: RepositoryMoveCommand
  token: string
  expiresAt: string
}
export type RepositoryMoveResult =
  | { status: 'moved'; sourcePath: string; destinationPath: string }
  | { status: 'unverified'; sourcePath: string; destinationPath: string; message: string }

export type RepositoryMoveEligibility =
  | { eligible: true; sourcePath: string; sourceName: string }
  | { eligible: false; reason: 'source-exists' | 'dashboard-registered' }

export type RepositoryMergeSnapshot = { path: string; name: string; jobCount: number }
export type RepositoryMergeCandidate = RepositoryMergeSnapshot & { repositoryIdentity: string }
export type RepositoryMergeEligibility =
  | { eligible: true; source: RepositoryMergeSnapshot; targets: RepositoryMergeCandidate[] }
  | { eligible: false; reason: 'source-exists' | 'dashboard-registered' | 'source-empty' | 'active-source-jobs' | 'no-targets' }
export type RepositoryMergeEligibilityReason = Exclude<RepositoryMergeEligibility, { eligible: true }>['reason']
export type RepositoryMergeCommand = { executable: string; args: string[]; workingDirectory: string }
export type RepositoryMergePreview = {
  source: RepositoryMergeSnapshot
  target: RepositoryMergeCandidate
  command: RepositoryMergeCommand
  token: string
  expiresAt: string
}
export type RepositoryMergeResult =
  | { status: 'merged' }
  | { status: 'unverified'; message: string }

function renameObject(value: unknown): Record<string, unknown> | null {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null
}

function hasExactKeys(object: Record<string, unknown>, expected: readonly string[]): boolean {
  const keys = Object.keys(object)
  return keys.length === expected.length && expected.every(key => Object.prototype.hasOwnProperty.call(object, key))
}

function renameCommand(value: unknown): RepositoryRenameCommand | null {
  const command = renameObject(value)
  if (!command || typeof command.executable !== 'string' || !command.executable.trim() || !Array.isArray(command.args) || command.args.length === 0 || !command.args.every(item => typeof item === 'string')) return null
  if (Object.keys(command).some(key => !['executable', 'args'].includes(key))) return null
  return { executable: command.executable, args: command.args as string[] }
}

function moveCommand(value: unknown): RepositoryMoveCommand | null {
  const command = renameObject(value)
  if (!command || typeof command.executable !== 'string' || !command.executable.trim() || !Array.isArray(command.args) || command.args.length === 0 || !command.args.every(item => typeof item === 'string') || typeof command.workingDirectory !== 'string' || !command.workingDirectory) return null
  if (Object.keys(command).some(key => !['executable', 'args', 'workingDirectory'].includes(key))) return null
  return { executable: command.executable, args: command.args as string[], workingDirectory: command.workingDirectory }
}

function mergeCommand(value: unknown): RepositoryMergeCommand | null {
  const command = renameObject(value)
  if (!command || typeof command.executable !== 'string' || !command.executable.trim() || !Array.isArray(command.args) || command.args.length === 0 || !command.args.every(item => typeof item === 'string') || command.workingDirectory !== '') return null
  if (Object.keys(command).some(key => !['executable', 'args', 'workingDirectory'].includes(key))) return null
  return { executable: command.executable, args: command.args as string[], workingDirectory: command.workingDirectory }
}

function mergeSnapshot(value: unknown): RepositoryMergeSnapshot | null {
  const object = renameObject(value)
  if (!object || !hasExactKeys(object, ['path', 'name', 'jobCount']) || typeof object.path !== 'string' || object.path.length === 0 || typeof object.name !== 'string' || !nonNegativeSafeInteger(object.jobCount)) return null
  return { path: object.path, name: object.name, jobCount: object.jobCount }
}

function mergeCandidate(value: unknown): RepositoryMergeCandidate | null {
  const object = renameObject(value)
  if (!object || !hasExactKeys(object, ['repositoryIdentity', 'path', 'name', 'jobCount']) || typeof object.repositoryIdentity !== 'string' || !object.repositoryIdentity.trim() || typeof object.path !== 'string' || object.path.length === 0 || typeof object.name !== 'string' || !nonNegativeSafeInteger(object.jobCount)) return null
  return { path: object.path, name: object.name, jobCount: object.jobCount, repositoryIdentity: object.repositoryIdentity }
}

/** Normalize the strict, server-owned repository merge eligibility result. */
export function normalizeRepositoryMergeEligibility(value: unknown): RepositoryMergeEligibility | null {
  const object = renameObject(value)
  if (!object || typeof object.eligible !== 'boolean') return null
  if (object.eligible) {
    if (!hasExactKeys(object, ['eligible', 'source', 'targets']) || !Array.isArray(object.targets)) return null
    const source = mergeSnapshot(object.source)
    if (!source) return null
    const targets: RepositoryMergeCandidate[] = []
    const identities = new Set<string>()
    for (const item of object.targets) {
      const target = mergeCandidate(item)
      if (!target) return null
      if (identities.has(target.repositoryIdentity)) return null
      identities.add(target.repositoryIdentity)
      targets.push(target)
    }
    if (targets.length === 0) return null
    return { eligible: true, source, targets }
  }
  if (!hasExactKeys(object, ['eligible', 'reason']) || !['source-exists', 'dashboard-registered', 'source-empty', 'active-source-jobs', 'no-targets'].includes(object.reason as string)) return null
  return { eligible: false, reason: object.reason as RepositoryMergeEligibilityReason }
}

/** Normalize the strict, server-owned repository merge preview. */
export function normalizeRepositoryMergePreview(value: unknown): RepositoryMergePreview | null {
  const object = renameObject(value)
  if (!object || !hasExactKeys(object, ['source', 'target', 'command', 'token', 'expiresAt'])) return null
  const source = mergeSnapshot(object.source)
  const target = mergeCandidate(object.target)
  const command = mergeCommand(object.command)
  if (!source || !target || !command || command.args.length !== 5 || command.args[0] !== 'repo' || command.args[1] !== 'merge' || command.args[2] !== '--yes' || command.args[3] !== source.path || command.args[4] !== target.path || typeof object.token !== 'string' || !object.token.trim() || !rfc3339(object.expiresAt)) return null
  return { source, target, command, token: object.token, expiresAt: object.expiresAt }
}

/** Normalize the post-confirm repository merge receipt. */
export function normalizeRepositoryMergeResult(value: unknown): RepositoryMergeResult | null {
  const object = renameObject(value)
  if (!object || typeof object.status !== 'string') return null
  if (object.status === 'merged' && hasExactKeys(object, ['status'])) return { status: 'merged' }
  if (object.status === 'unverified' && hasExactKeys(object, ['status', 'message']) && typeof object.message === 'string' && object.message.trim()) return { status: 'unverified', message: object.message }
  return null
}

/** Normalize the strict, server-owned repository move preview. */
export function normalizeRepositoryMovePreview(value: unknown): RepositoryMovePreview | null {
  const object = renameObject(value)
  if (!object || Object.keys(object).some(key => !['sourcePath', 'sourceName', 'jobCount', 'destinationRepositoryIdentity', 'destinationPath', 'destinationName', 'command', 'token', 'expiresAt'].includes(key))) return null
  const command = moveCommand(object.command)
  if (typeof object.sourcePath !== 'string' || object.sourcePath.length === 0 || typeof object.sourceName !== 'string' || typeof object.destinationRepositoryIdentity !== 'string' || !object.destinationRepositoryIdentity.trim() || typeof object.destinationPath !== 'string' || object.destinationPath.length === 0 || typeof object.destinationName !== 'string' || !nonNegativeSafeInteger(object.jobCount) || !command || typeof object.token !== 'string' || !object.token.trim() || !rfc3339(object.expiresAt)) return null
  return { sourcePath: object.sourcePath, sourceName: object.sourceName, jobCount: object.jobCount, destinationRepositoryIdentity: object.destinationRepositoryIdentity, destinationPath: object.destinationPath, destinationName: object.destinationName, command, token: object.token, expiresAt: object.expiresAt }
}

/** Normalize the post-confirm move receipt. Both outcomes carry the server's path evidence. */
export function normalizeRepositoryMoveResult(value: unknown): RepositoryMoveResult | null {
  const object = renameObject(value)
  if (!object || typeof object.status !== 'string') return null
  if (object.status === 'moved') {
    if (Object.keys(object).some(key => !['status', 'sourcePath', 'destinationPath'].includes(key)) || typeof object.sourcePath !== 'string' || object.sourcePath.length === 0 || typeof object.destinationPath !== 'string' || object.destinationPath.length === 0) return null
    return { status: 'moved', sourcePath: object.sourcePath, destinationPath: object.destinationPath }
  }
  if (object.status === 'unverified') {
    if (Object.keys(object).some(key => !['status', 'sourcePath', 'destinationPath', 'message'].includes(key)) || typeof object.sourcePath !== 'string' || object.sourcePath.length === 0 || typeof object.destinationPath !== 'string' || object.destinationPath.length === 0 || typeof object.message !== 'string' || !object.message.trim()) return null
    return { status: 'unverified', sourcePath: object.sourcePath, destinationPath: object.destinationPath, message: object.message }
  }
  return null
}

/** Normalize the strict server-owned preflight result for lazy path repair. */
export function normalizeRepositoryMoveEligibility(value: unknown): RepositoryMoveEligibility | null {
  const object = renameObject(value)
  if (!object || typeof object.eligible !== 'boolean') return null
  if (object.eligible === true) {
    if (!hasExactKeys(object, ['eligible', 'sourcePath', 'sourceName']) || typeof object.sourcePath !== 'string' || object.sourcePath.length === 0 || typeof object.sourceName !== 'string') return null
    return { eligible: true, sourcePath: object.sourcePath, sourceName: object.sourceName }
  }
  if (!hasExactKeys(object, ['eligible', 'reason']) || (object.reason !== 'source-exists' && object.reason !== 'dashboard-registered')) return null
  return { eligible: false, reason: object.reason }
}

/** Normalize the strict, server-owned repository rename preview. */
export function normalizeRepositoryRenamePreview(value: unknown): RepositoryRenamePreview | null {
  const object = renameObject(value)
  if (!object || Object.keys(object).some(key => !['path', 'currentName', 'newName', 'jobCount', 'dashboardRegistered', 'command', 'token', 'expiresAt'].includes(key))) return null
  const command = renameCommand(object.command)
  if (typeof object.path !== 'string' || object.path.length === 0 || typeof object.currentName !== 'string' || typeof object.newName !== 'string' || object.newName.trim() === '' || !nonNegativeSafeInteger(object.jobCount) || typeof object.dashboardRegistered !== 'boolean' || !command || typeof object.token !== 'string' || !object.token.trim() || !rfc3339(object.expiresAt)) return null
  return { path: object.path, currentName: object.currentName, newName: object.newName, jobCount: object.jobCount, dashboardRegistered: object.dashboardRegistered, command, token: object.token, expiresAt: object.expiresAt }
}

/** Normalize the post-confirm receipt. Unverified results intentionally carry no name claim. */
export function normalizeRepositoryRenameResult(value: unknown): RepositoryRenameResult | null {
  const object = renameObject(value)
  if (!object || typeof object.status !== 'string') return null
  if (object.status === 'renamed') {
    if (Object.keys(object).some(key => !['status', 'path', 'previousName', 'name'].includes(key)) || typeof object.path !== 'string' || object.path.length === 0 || typeof object.previousName !== 'string' || typeof object.name !== 'string' || object.name.length === 0) return null
    return { status: 'renamed', path: object.path, previousName: object.previousName, name: object.name }
  }
  if (object.status === 'unverified') {
    if (Object.keys(object).some(key => !['status', 'path', 'message'].includes(key)) || typeof object.path !== 'string' || object.path.length === 0 || typeof object.message !== 'string' || !object.message.trim()) return null
    return { status: 'unverified', path: object.path, message: object.message }
  }
  return null
}

export type RepositoryRenameNameError = 'empty' | 'tooLong' | 'invalidUtf8' | 'control' | 'leadingDash' | 'same'

/** Apply the backend rename policy locally so invalid input never creates a preview request. */
export function validateRepositoryRenameName(input: string, currentName: string): { value: string | null; error?: RepositoryRenameNameError } {
  const value = input.trim()
  if (!value) return { value: null, error: 'empty' }
  let codePoints = 0
  for (let index = 0; index < value.length; index += 1) {
    const code = value.charCodeAt(index)
    if (code >= 0xd800 && code <= 0xdbff) {
      const next = value.charCodeAt(index + 1)
      if (Number.isNaN(next) || next < 0xdc00 || next > 0xdfff) return { value: null, error: 'invalidUtf8' }
      index += 1
    } else if (code >= 0xdc00 && code <= 0xdfff) return { value: null, error: 'invalidUtf8' }
    const point = value.codePointAt(index) as number
    if (point <= 0x1f || (point >= 0x7f && point <= 0x9f)) return { value: null, error: 'control' }
    codePoints += 1
  }
  if (codePoints > 128) return { value: null, error: 'tooLong' }
  if (value.startsWith('-')) return { value: null, error: 'leadingDash' }
  if (value === currentName) return { value: null, error: 'same' }
  return { value }
}

function nonNegativeSafeInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
}

function exactObject(value: unknown, keys: readonly string[]): Record<string, unknown> | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null
  const prototype = Object.getPrototypeOf(value)
  if (prototype !== Object.prototype && prototype !== null) return null
  const object = value as Record<string, unknown>
  return hasExactKeys(object, keys) ? object : null
}

/** Normalize the Dashboard-owned, read-only runtime diagnostics DTO. */
export function normalizeRuntimeDiagnostics(value: unknown): RuntimeDiagnostics | null {
  const root = exactObject(value, ['daemon', 'workers', 'queue', 'jobs', 'health'])
  if (!root) return null

  const daemon = exactObject(root.daemon, ['running', 'version', 'uptime'])
  const workers = exactObject(root.workers, ['active', 'max'])
  const queue = exactObject(root.queue, ['paused', 'queued', 'running'])
  const jobs = exactObject(root.jobs, ['completed', 'failed', 'skipped'])
  const health = exactObject(root.health, ['healthy', 'errorCount24h', 'components'])
  if (!daemon || !workers || !queue || !jobs || !health) return null
  if (typeof daemon.running !== 'boolean' || typeof daemon.version !== 'string' || !daemon.version.trim() || typeof daemon.uptime !== 'string' || !daemon.uptime.trim()) return null
  if (!nonNegativeSafeInteger(workers.active) || !nonNegativeSafeInteger(workers.max) || workers.max === 0 || workers.active > workers.max) return null
  if (typeof queue.paused !== 'boolean' || !nonNegativeSafeInteger(queue.queued) || !nonNegativeSafeInteger(queue.running)) return null
  if (!nonNegativeSafeInteger(jobs.completed) || !nonNegativeSafeInteger(jobs.failed) || !nonNegativeSafeInteger(jobs.skipped)) return null
  if (typeof health.healthy !== 'boolean' || !nonNegativeSafeInteger(health.errorCount24h) || !Array.isArray(health.components)) return null

  const components: RuntimeDiagnosticsComponent[] = []
  const names = new Set<string>()
  for (const value of health.components) {
    const component = exactObject(value, ['name', 'healthy'])
    if (!component || typeof component.name !== 'string' || !component.name.trim() || typeof component.healthy !== 'boolean' || names.has(component.name)) return null
    names.add(component.name)
    components.push({ name: component.name, healthy: component.healthy })
  }
  if (health.healthy && components.some(component => !component.healthy)) return null
  return {
    daemon: { running: daemon.running, version: daemon.version, uptime: daemon.uptime },
    workers: { active: workers.active, max: workers.max },
    queue: { paused: queue.paused, queued: queue.queued, running: queue.running },
    jobs: { completed: jobs.completed, failed: jobs.failed, skipped: jobs.skipped },
    health: { healthy: health.healthy, errorCount24h: health.errorCount24h, components },
  }
}

/**
 * Normalize the backend-owned repository inventory DTO.  The inventory is
 * read-only UI data, so malformed rows are rejected as a whole rather than
 * being partially rendered or matched in the browser.
 */
export function normalizeRepositoryInventoryResponse(value: unknown): RepositoryInventoryResponse | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null
  const object = value as Record<string, unknown>
  if (Object.keys(object).some(key => !['repositories', 'repositoryCount', 'totalJobCount'].includes(key))) return null
  if (!Array.isArray(object.repositories) || !nonNegativeSafeInteger(object.repositoryCount) || !nonNegativeSafeInteger(object.totalJobCount)) return null
  if (object.repositoryCount !== object.repositories.length) return null
  const repositories: RepositoryInventoryRow[] = []
  let total = 0
  for (const item of object.repositories) {
    if (!item || typeof item !== 'object' || Array.isArray(item)) return null
    const row = item as Record<string, unknown>
    if (Object.keys(row).some(key => !['name', 'path', 'jobCount', 'dashboardRegistered'].includes(key))) return null
    if (typeof row.name !== 'string' || typeof row.path !== 'string' || row.path.length === 0 || !nonNegativeSafeInteger(row.jobCount) || typeof row.dashboardRegistered !== 'boolean') return null
    total += row.jobCount
    if (!Number.isSafeInteger(total)) return null
    repositories.push({ name: row.name, path: row.path, jobCount: row.jobCount, dashboardRegistered: row.dashboardRegistered })
  }
  if (total !== object.totalJobCount) return null
  return { repositories, repositoryCount: object.repositoryCount, totalJobCount: object.totalJobCount }
}

export type StatsWindow = '24h' | '7d' | '30d' | '90d'
export type StatsBranchScope = 'all' | 'current'
export type StatsSummaryResponse = {
  repositoryIdentity: string
  window: StatsWindow
  branchScope: StatsBranchScope
  summary: {
    $schema?: string
    since: string
    repo_path?: string
    branch?: string
    repos?: Array<{ name: string; path: string; total: number; passed: number; failed: number; addressed: number }>
    overview: { total: number; queued: number; running: number; done: number; failed: number; canceled: number; applied: number; rebased: number }
    verdicts: { total: number; passed: number; failed: number; addressed: number; pass_rate: number; resolution_rate: number }
    duration: { review_p50_secs: number; review_p90_secs: number; review_p99_secs: number; queue_p50_secs: number; queue_p90_secs: number; queue_p99_secs: number }
    agents: Array<{ agent: string; total: number; passed: number; failed: number; errors: number; pass_rate: number; median_duration_secs: number }>
    job_types: Array<{ type: string; count: number; applied?: number; rebased?: number }>
    failures: { total: number; retries: number; errors: Record<string, number> }
    cost: { total_usd: number; jobs_with_cost: number; jobs_total: number; complete: boolean }
  }
}
export type StatsCostResponse = {
  repositoryIdentity: string
  branchScope: StatsBranchScope
  cost: {
    $schema?: string
    total_usd: number
    jobs_with_cost: number
    jobs_total: number
    complete: boolean
  }
}

export type InsightsWindow = '7d' | '30d' | '90d'
export type InsightsBranchScope = 'all' | 'current'
export type InsightsCommand = { executable: string; args: string[] }
export type GuidelineInsightsPreview = {
  repositoryIdentity: string
  repositoryName: string
  window: InsightsWindow
  branchScope: InsightsBranchScope
  branch?: string
  command: InsightsCommand
  warnings: string[]
  token: string
  expiresAt: string
}
export type GuidelineInsightsLaunch = {
  status: 'queued' | 'skipped' | 'unverified'
  jobId?: number
  agent?: string
  since?: string
  skipped?: boolean
  reason?: string
  message?: string
}
export type GuidelineInsightsStatus = { jobId: number; status: 'queued' | 'running' | 'done' | 'failed' | 'canceled' }
export type GuidelineInsightsResult = { jobId: number; status: 'done'; markdown: string }

const maxGuidelineInsightsMarkdown = 4 * 1024 * 1024
function rfc3339(value: unknown): value is string {
  return typeof value === 'string' && value.length > 0 && !Number.isNaN(Date.parse(value))
}

function insightsWindow(value: unknown): InsightsWindow | null {
  return value === '7d' || value === '30d' || value === '90d' ? value : null
}
function insightsBranchScope(value: unknown): InsightsBranchScope | null {
  return value === 'all' || value === 'current' ? value : null
}
export function normalizeGuidelineInsightsPreview(value: unknown): GuidelineInsightsPreview | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null
  const object = value as Record<string, unknown>
  const window = insightsWindow(object.window)
  const branchScope = insightsBranchScope(object.branchScope)
  const command = object.command && typeof object.command === 'object' && !Array.isArray(object.command) ? object.command as Record<string, unknown> : null
  if (typeof object.repositoryIdentity !== 'string' || !object.repositoryIdentity.trim() || typeof object.repositoryName !== 'string' || !object.repositoryName.trim() || !window || !branchScope || !command || typeof command.executable !== 'string' || !command.executable.trim() || !Array.isArray(command.args) || command.args.length === 0 || !command.args.every(item => typeof item === 'string') || !Array.isArray(object.warnings) || !object.warnings.every(item => typeof item === 'string') || typeof object.token !== 'string' || !object.token.trim() || !rfc3339(object.expiresAt)) return null
  if (branchScope === 'current' && (typeof object.branch !== 'string' || !object.branch.trim())) return null
  return { repositoryIdentity: object.repositoryIdentity, repositoryName: object.repositoryName, window, branchScope, branch: typeof object.branch === 'string' ? object.branch : undefined, command: { executable: command.executable, args: command.args as string[] }, warnings: object.warnings as string[], token: object.token, expiresAt: object.expiresAt }
}
export function normalizeGuidelineInsightsLaunch(value: unknown): GuidelineInsightsLaunch | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null
  const object = value as Record<string, unknown>
  if (object.status !== 'queued' && object.status !== 'skipped' && object.status !== 'unverified') return null
  if (object.jobId !== undefined && (typeof object.jobId !== 'number' || !Number.isSafeInteger(object.jobId) || object.jobId <= 0)) return null
  if (object.status === 'queued' && object.jobId === undefined) return null
  if (object.status === 'skipped' && (object.skipped !== true || typeof object.reason !== 'string' || !object.reason.trim() || !rfc3339(object.since))) return null
  if (object.status === 'unverified' && (typeof object.message !== 'string' || !object.message.trim())) return null
  if (object.status === 'queued' && (typeof object.agent !== 'string' || !object.agent.trim() || !rfc3339(object.since))) return null
  return { status: object.status, jobId: object.jobId as number | undefined, agent: typeof object.agent === 'string' ? object.agent : undefined, since: typeof object.since === 'string' ? object.since : undefined, skipped: object.skipped === true ? true : undefined, reason: typeof object.reason === 'string' ? object.reason : undefined, message: typeof object.message === 'string' ? object.message : undefined }
}
export function normalizeGuidelineInsightsStatus(value: unknown): GuidelineInsightsStatus | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null
  const object = value as Record<string, unknown>
  if (typeof object.jobId !== 'number' || !Number.isSafeInteger(object.jobId) || object.jobId <= 0 || !['queued', 'running', 'done', 'failed', 'canceled'].includes(String(object.status))) return null
  return { jobId: object.jobId, status: object.status as GuidelineInsightsStatus['status'] }
}
export function normalizeGuidelineInsightsResult(value: unknown): GuidelineInsightsResult | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null
  const object = value as Record<string, unknown>
  if (typeof object.jobId !== 'number' || !Number.isSafeInteger(object.jobId) || object.jobId <= 0 || object.status !== 'done' || typeof object.markdown !== 'string' || object.markdown.length > maxGuidelineInsightsMarkdown) return null
  return { jobId: object.jobId, status: 'done', markdown: object.markdown }
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
// API normalization must not embed a display-language string. The Setup UI
// maps this stable sentinel to the active catalog language.
export const BACKEND_COMMAND_SENTINEL = '__roborev_backend_command__'

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
  repositoryIdentity: string
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
  return BACKEND_COMMAND_SENTINEL
}

/** Parse the machine-readable configured-agent response. Unknown fields are ignored. */
export function normalizeAgentsResponse(value: unknown): AgentsResponse | null {
  const object = objectValue(value)
  if (!object || typeof object.repositoryIdentity !== 'string' || !object.repositoryIdentity.trim() || !Array.isArray(object.configured)) return null
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
  return { repositoryIdentity: object.repositoryIdentity, roborevVersion: optionalString(readAlias(object, 'roborevVersion', 'roborev_version')), configured }
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

export type FixTarget = {
  jobId: number
  gitRef: string
  branch: string
  jobType: string
  panelRole: string
  panelName?: string
}

export type FixCommand = { executable: string; args: string[] }
export type FixPreview = {
  repositoryIdentity: string
  repositoryPath: string
  target: FixTarget
  agent: string
  command: FixCommand
  startingHead: string
  startingBranch: string
  previewToken: string
}
export type FixPhase = 'starting' | 'running' | 'succeeded' | 'failed' | 'cancelled'
export type FixOperation = {
  id: number | string
  repositoryIdentity: string
  repositoryPath: string
  target: FixTarget
  agent: string
  command: FixCommand
  startingHead?: string
  startingBranch?: string
  endingHead?: string
  endingBranch?: string
  endingDirty?: boolean
  phase: FixPhase
  startedAt?: string
  updatedAt?: string
  completedAt?: string
  error?: string
  errorCode?: string
  cancelRequestedAt?: string
}
export type FixOperationsResponse = { operations: FixOperation[]; operation: FixOperation | null }

function readFixAlias(object: Record<string, unknown>, camel: string, snake: string) { return object[camel] ?? object[snake] }
function fixString(object: Record<string, unknown>, camel: string, snake = camel) { const value = readFixAlias(object, camel, snake); return typeof value === 'string' ? value : undefined }
function fixNumber(object: Record<string, unknown>, camel: string, snake = camel) {
  const value = readFixAlias(object, camel, snake)
  if (typeof value === 'number') return Number.isSafeInteger(value) ? value : undefined
  if (typeof value !== 'string' || !/^\d+$/.test(value)) return undefined
  const parsed = Number(value)
  return Number.isSafeInteger(parsed) ? parsed : undefined
}
function parseFixTarget(value: unknown): FixTarget | null {
  const object = objectValue(value)
  if (!object) return null
  const jobId = fixNumber(object, 'jobId', 'job_id')
  const gitRef = fixString(object, 'gitRef', 'git_ref')
  const branch = fixString(object, 'branch')
  const jobType = fixString(object, 'jobType', 'job_type')
  const panelRole = fixString(object, 'panelRole', 'panel_role')
  if (jobId === undefined || jobId <= 0 || !gitRef || !branch || !jobType || typeof panelRole !== 'string') return null
  return { jobId, gitRef, branch, jobType, panelRole, panelName: fixString(object, 'panelName', 'panel_name') }
}
function parseFixCommand(value: unknown): FixCommand | null {
  const object = objectValue(value)
  if (!object || typeof object.executable !== 'string' || !object.executable.trim() || !Array.isArray(object.args) || !object.args.every(item => typeof item === 'string')) return null
  return { executable: object.executable, args: object.args as string[] }
}
function parseFixPreview(value: unknown): FixPreview | null {
  const object = objectValue(value)
  if (!object || typeof readFixAlias(object, 'repositoryIdentity', 'repository_identity') !== 'string' || typeof readFixAlias(object, 'repositoryPath', 'repository_path') !== 'string' || typeof object.agent !== 'string' || typeof readFixAlias(object, 'startingHead', 'starting_head') !== 'string' || typeof readFixAlias(object, 'startingBranch', 'starting_branch') !== 'string' || typeof readFixAlias(object, 'previewToken', 'preview_token') !== 'string') return null
  const target = parseFixTarget(object.target)
  const command = parseFixCommand(object.command)
  if (!target || !command) return null
  return { repositoryIdentity: readFixAlias(object, 'repositoryIdentity', 'repository_identity') as string, repositoryPath: readFixAlias(object, 'repositoryPath', 'repository_path') as string, target, agent: object.agent, command, startingHead: readFixAlias(object, 'startingHead', 'starting_head') as string, startingBranch: readFixAlias(object, 'startingBranch', 'starting_branch') as string, previewToken: readFixAlias(object, 'previewToken', 'preview_token') as string }
}
function parseFixOperation(value: unknown): FixOperation | null {
  const object = objectValue(value)
  if (!object || (typeof object.id !== 'string' && typeof object.id !== 'number')) return null
  const phase = fixString(object, 'phase')
  const phases: FixPhase[] = ['starting', 'running', 'succeeded', 'failed', 'cancelled']
  const target = parseFixTarget(object.target)
  const command = parseFixCommand(object.command)
  const repositoryIdentity = fixString(object, 'repositoryIdentity', 'repository_identity')
  const repositoryPath = fixString(object, 'repositoryPath', 'repository_path')
  if (!phase || !phases.includes(phase as FixPhase) || !target || !command || !repositoryIdentity || !repositoryPath || typeof object.agent !== 'string') return null
  const optionalBool = (camel: string, snake: string) => { const value = readFixAlias(object, camel, snake); return typeof value === 'boolean' ? value : undefined }
  return { id: object.id as number | string, repositoryIdentity, repositoryPath, target, agent: object.agent, command, phase: phase as FixPhase, startingHead: fixString(object, 'startingHead', 'starting_head'), startingBranch: fixString(object, 'startingBranch', 'starting_branch'), endingHead: fixString(object, 'endingHead', 'ending_head'), endingBranch: fixString(object, 'endingBranch', 'ending_branch'), endingDirty: optionalBool('endingDirty', 'ending_dirty'), startedAt: fixString(object, 'startedAt', 'started_at'), updatedAt: fixString(object, 'updatedAt', 'updated_at'), completedAt: fixString(object, 'completedAt', 'completed_at'), error: fixString(object, 'error'), errorCode: fixString(object, 'errorCode', 'error_code'), cancelRequestedAt: fixString(object, 'cancelRequestedAt', 'cancel_requested_at') }
}
export function normalizeFixPreview(value: unknown): FixPreview | null { return parseFixPreview(value) }
export function normalizeFixOperation(value: unknown): FixOperation | null { return parseFixOperation(value) }
export function normalizeFixOperationsResponse(value: unknown): FixOperationsResponse {
  const object = objectValue(value)
  if (!object) return { operations: [], operation: null }
  const values = Array.isArray(object.operations) ? object.operations : []
  const operations = values.map(parseFixOperation).filter((item): item is FixOperation => Boolean(item))
  const direct = parseFixOperation(object.operation)
  const sorted = [...operations].sort((left, right) => String(right.updatedAt ?? right.startedAt ?? '').localeCompare(String(left.updatedAt ?? left.startedAt ?? '')))
  return { operations, operation: direct ?? sorted[0] ?? null }
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
  job_uuid?: string
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
  panel_member_name?: string
  panel_run_uuid?: string
  panel_role?: string
  parent_job_id?: number
  panel_member_index?: number
  member_job_ids?: number[]
  duration_ms?: number
  [key: string]: unknown
}

export type AnalysisType = {
  name: string
  description: string
}

export type AnalysisPrompt = {
  analysisType: string
  prompt: string
}

const analysisTypePattern = /^[a-z0-9]+(?:-[a-z0-9]+)*$/
const maxAnalysisTypes = 64
const maxAnalysisDescriptionLength = 4_096
const maxAnalysisPromptLength = 2_097_152

function analysisString(value: unknown, maxLength = 8_192) {
  return typeof value === 'string' && value.trim() && value.length <= maxLength && !/[\u0000-\u0008\u000B\u000C\u000E-\u001F\u007F]/.test(value) ? value : null
}

function analysisTypeName(value: unknown) {
  const name = analysisString(value, 80)
  return name && analysisTypePattern.test(name) ? name : null
}

function analysisObject(value: unknown) {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null
}

function analysisAlias(object: Record<string, unknown>, camel: string, snake: string) {
  return object[camel] ?? object[snake]
}

/**
 * Normalize the validated JSON envelope returned by GET /api/analyses/types.
 * The server owns discovery; the browser never manufactures a type catalog.
 */
export function normalizeAnalysisTypesResponse(value: unknown): AnalysisType[] | null {
  const object = analysisObject(value)
  const values = Array.isArray(value) ? value : object && Array.isArray(object.types) ? object.types : null
  if (!values || values.length === 0 || values.length > maxAnalysisTypes) return null
  const seen = new Set<string>()
  const types: AnalysisType[] = []
  for (const item of values) {
    const entry = analysisObject(item)
    const name = analysisTypeName(entry && analysisAlias(entry, 'name', 'type'))
    const description = analysisString(entry && analysisAlias(entry, 'description', 'description'), maxAnalysisDescriptionLength)
    if (!name || !description || seen.has(name)) return null
    seen.add(name)
    types.push({ name, description })
  }
  return types
}

/** Normalize the lazy prompt response without translating RoboRev text. */
export function normalizeAnalysisPrompt(value: unknown, expectedType?: string): AnalysisPrompt | null {
  const object = analysisObject(value)
  if (!object) return null
  const analysisType = analysisTypeName(analysisAlias(object, 'analysisType', 'analysis_type') ?? object.type)
  const prompt = analysisString(object.prompt, maxAnalysisPromptLength)
  if (!analysisType || !prompt || (expectedType && analysisType !== expectedType)) return null
  return { analysisType, prompt }
}

/**
 * The stream is deliberately a tiny invalidation signal.  It is not a
 * review/job state contract: callers must fetch the authoritative review
 * list after accepting one of these messages.
 */
export type ReviewInvalidationEvent = {
  repositoryIdentity: string
  sequence: number
  kind: 'jobs-changed'
}

export function reviewEventsURL(repositoryIdentity: string): string | null {
  if (!repositoryIdentity.trim()) return null
  return `/api/events?repositoryIdentity=${encodeURIComponent(repositoryIdentity)}`
}

export function normalizeReviewInvalidationEvent(value: unknown): ReviewInvalidationEvent | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null
  const object = value as Record<string, unknown>
  if (typeof object.repositoryIdentity !== 'string' || !object.repositoryIdentity.trim()) return null
  if (typeof object.sequence !== 'number' || !Number.isSafeInteger(object.sequence) || object.sequence < 0) return null
  if (object.kind !== 'jobs-changed') return null
  return { repositoryIdentity: object.repositoryIdentity, sequence: object.sequence, kind: 'jobs-changed' }
}

/** Parse EventSource.data without ever treating raw stream fields as state. */
export function parseReviewInvalidationEvent(data: unknown): ReviewInvalidationEvent | null {
  if (typeof data !== 'string') return null
  try { return normalizeReviewInvalidationEvent(JSON.parse(data)) } catch { return null }
}

let csrfToken = ''
export function setCSRF(value: string) { csrfToken = value }

export class APIError extends Error {
  phase?: string
  code?: string
  constructor(message: string, phase?: string, code?: string) { super(message); this.phase = phase; this.code = code }
}

export async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const headers = new Headers(options.headers)
  if (options.body) headers.set('Content-Type', 'application/json')
  if ((options.method ?? 'GET') !== 'GET') headers.set('X-CSRF-Token', csrfToken)
  const response = await fetch(path, { ...options, headers })
  // Clone before parsing so deterministic test doubles may safely service
  // coalesced endpoint reads with the same Response object. Real fetch
  // responses retain the same wire semantics.
  const text = await response.clone().text()
  let payload: unknown = undefined
  if (text) {
    try { payload = JSON.parse(text) } catch { payload = text }
  }
  if (!response.ok) {
    const problem = payload as { error?: string; phase?: string; code?: string; error_code?: string; errorCode?: string } | undefined
    throw new APIError(problem?.error ?? `Request failed (${response.status})`, problem?.phase, problem?.code ?? problem?.error_code ?? problem?.errorCode)
  }
  return payload as T
}

/** Request one explicitly-triggered repository-scoped CI workflow preview. */
export async function fetchCIWorkflowPreview(repositoryIdentity: string, signal?: AbortSignal): Promise<CIWorkflowPreview> {
  const value = await request<unknown>('/api/ci-workflow/preview', {
    method: 'POST',
    body: JSON.stringify({ repositoryIdentity }),
    signal,
  })
  const normalized = normalizeCIWorkflowPreview(value, repositoryIdentity)
  if (!normalized) throw new APIError('invalid-ci-workflow-preview')
  return normalized
}

export async function fetchRepositorySetupReadiness(repositoryIdentity: string, signal?: AbortSignal): Promise<RepositorySetupReadiness> {
  const value = await request<unknown>('/api/repositories/setup-readiness', {
    method: 'POST',
    body: JSON.stringify({ repositoryIdentity }),
    signal,
  })
  const normalized = normalizeRepositorySetupReadiness(value)
  if (!normalized) throw new APIError('invalid-repository-setup-readiness')
  return normalized
}

/** Request one explicitly-triggered repository-scoped Snapshot Ignore preview. */
export async function fetchSnapshotIgnorePreview(repositoryIdentity: RepositoryIdentityAlias, signal?: AbortSignal): Promise<SnapshotIgnorePreview> {
  const value = await request<unknown>('/api/snapshot-ignore/preview', {
    method: 'POST',
    body: JSON.stringify({ repositoryIdentity }),
    signal,
  })
  const normalized = normalizeSnapshotIgnorePreview(value, repositoryIdentity)
  if (!normalized) throw new APIError('invalid-snapshot-ignore-preview')
  return normalized
}

/** Execute one server-bound, single-use Snapshot Ignore token. */
export async function executeSnapshotIgnore(token: string): Promise<SnapshotIgnoreExecuteResult> {
  const value = await request<unknown>('/api/snapshot-ignore/execute', {
    method: 'POST',
    body: JSON.stringify({ token }),
  })
  const normalized = normalizeSnapshotIgnoreExecuteResult(value)
  if (!normalized) throw new APIError('invalid-snapshot-ignore-execute-result')
  return normalized
}

/** Execute one server-bound, single-use CI workflow create token. */
export async function executeCIWorkflow(token: string): Promise<CIWorkflowExecuteResult> {
  const value = await request<unknown>('/api/ci-workflow/execute', {
    method: 'POST',
    body: JSON.stringify({ token }),
  })
  const normalized = normalizeCIWorkflowExecuteResult(value)
  if (!normalized) throw new APIError('invalid-ci-workflow-execute-result')
  return normalized
}

export function normalizeCIWorkflowExecuteResult(value: unknown): CIWorkflowExecuteResult | null {
  const object = exactObject(value, ['status'])
  if (!object || (object.status !== 'verified-created' && object.status !== 'failed-unverified')) return null
  return { status: object.status }
}

export function jobsFromPayload(value: unknown): ReviewJob[] {
  if (Array.isArray(value)) return value as ReviewJob[]
  if (value && typeof value === 'object') {
    const object = value as Record<string, unknown>
    for (const key of ['jobs', 'reviews', 'items']) if (Array.isArray(object[key])) return object[key] as ReviewJob[]
  }
  return []
}

export type ConfigValueType = 'integer' | 'boolean' | 'enum'
export type ConfigScope = 'global' | 'local'
export type ConfigRowOrigin = 'global' | 'local' | 'default'

/** Backend-owned editable allowlist schema; the browser never invents keys, types, scopes, or bounds. */
export type ConfigurationSettingSpec = {
  key: string
  valueType: ConfigValueType
  scopes: ConfigScope[]
  enumValues?: string[]
  min?: number
  max?: number
}

export type ConfigurationRow = { origin: ConfigRowOrigin; key: string; value: string }

export type ConfigurationSnapshot = {
  repositoryIdentity: string
  settings: ConfigurationSettingSpec[]
  rows: ConfigurationRow[]
}

export type ConfigurationPreview = {
  previewToken: string
  key: string
  scope: string
  currentValue: string
  currentOrigin: string
  newValue: string
  command: { executable: string; args: string[] }
  warnings: string[]
}

export type ConfigurationSetResult = {
  status: 'succeeded' | 'unverified'
  message?: string
  key?: string
  scope?: string
  value?: string
  verifiedValue?: string
  repositoryDirty?: boolean
}

const configScopeValues: ConfigScope[] = ['global', 'local']
const configValueTypeValues: ConfigValueType[] = ['integer', 'boolean', 'enum']

function normalizeConfigSpec(value: unknown): ConfigurationSettingSpec | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null
  const object = value as Record<string, unknown>
  if (typeof object.key !== 'string' || !/^[a-z0-9_]+$/.test(object.key)) return null
  if (typeof object.valueType !== 'string' || !configValueTypeValues.includes(object.valueType as ConfigValueType)) return null
  if (!Array.isArray(object.scopes) || object.scopes.length === 0 || !object.scopes.every(scope => configScopeValues.includes(scope as ConfigScope))) return null
  const spec: ConfigurationSettingSpec = { key: object.key, valueType: object.valueType as ConfigValueType, scopes: object.scopes as ConfigScope[] }
  if (object.enumValues !== undefined) {
    if (!Array.isArray(object.enumValues) || !object.enumValues.every(item => typeof item === 'string')) return null
    spec.enumValues = object.enumValues as string[]
  }
  for (const bound of ['min', 'max'] as const) {
    if (object[bound] !== undefined) {
      if (typeof object[bound] !== 'number' || !Number.isSafeInteger(object[bound])) return null
      spec[bound] = object[bound] as number
    }
  }
  return spec
}

function normalizeConfigurationRows(value: unknown): ConfigurationRow[] | null {
  if (!Array.isArray(value)) return null
  const rows: ConfigurationRow[] = []
  const seen = new Set<string>()
  for (const item of value) {
    if (!item || typeof item !== 'object') return null
    const object = item as Record<string, unknown>
    if (typeof object.origin !== 'string' || !['global', 'local', 'default'].includes(object.origin)) return null
    if (typeof object.key !== 'string' || !object.key) return null
    if (typeof object.value !== 'string') return null
    if (seen.has(object.key)) return null
    seen.add(object.key)
    rows.push({ origin: object.origin as ConfigRowOrigin, key: object.key, value: object.value })
  }
  return rows
}

/** Parse the typed configuration snapshot. Malformed envelopes fail closed. */
export function normalizeConfigurationSnapshot(value: unknown): ConfigurationSnapshot | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null
  const object = value as Record<string, unknown>
  if (typeof object.repositoryIdentity !== 'string' || !object.repositoryIdentity) return null
  if (!Array.isArray(object.settings)) return null
  const settings: ConfigurationSettingSpec[] = []
  for (const item of object.settings) {
    const spec = normalizeConfigSpec(item)
    if (!spec) return null
    settings.push(spec)
  }
  const rows = normalizeConfigurationRows(object.rows)
  if (rows === null) return null
  return { repositoryIdentity: object.repositoryIdentity, settings, rows }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value && typeof value === 'object' && !Array.isArray(value))
}

export function normalizeConfigurationPreview(value: unknown): ConfigurationPreview | null {
  if (!isRecord(value)) return null
  if (typeof value.previewToken !== 'string' || !value.previewToken) return null
  if (typeof value.key !== 'string' || typeof value.scope !== 'string' || typeof value.newValue !== 'string') return null
  if (typeof value.currentValue !== 'string' || typeof value.currentOrigin !== 'string') return null
  const command = value.command && typeof value.command === 'object' && !Array.isArray(value.command) ? value.command as Record<string, unknown> : null
  if (!command || typeof command.executable !== 'string' || !Array.isArray(command.args) || !command.args.every(item => typeof item === 'string')) return null
  if (!Array.isArray(value.warnings) || !value.warnings.every(item => typeof item === 'string')) return null
  return { previewToken: value.previewToken, key: value.key, scope: value.scope, currentValue: value.currentValue, currentOrigin: value.currentOrigin, newValue: value.newValue, command: { executable: command.executable, args: command.args as string[] }, warnings: value.warnings as string[] }
}

export function normalizeConfigurationSetResult(value: unknown): ConfigurationSetResult | null {
  if (!isRecord(value)) return null
  if (value.status !== 'succeeded' && value.status !== 'unverified') return null
  return { status: value.status, message: optionalString(value.message), key: optionalString(value.key), scope: optionalString(value.scope), value: optionalString(value.value), verifiedValue: optionalString(value.verifiedValue), repositoryDirty: value.repositoryDirty === true }
}
