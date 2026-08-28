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

export type RepositoryAgent = 'codex' | 'claude-code' | 'gemini' | 'copilot' | 'opencode' | 'cursor' | 'kiro' | 'kilo' | 'droid' | 'pi' | 'grok'
export type RepositoryAgentConfigurationScope = 'global' | 'repository'
export type RepositoryAgentConfigurationStatus = 'unset' | 'canonical' | 'custom'
export type RepositoryAgentConfigurationRow =
  | { status: 'unset' | 'custom' }
  | { status: 'canonical'; agent: RepositoryAgent }
export type RepositoryAgentConfigurationStatusResponse = {
  version: 'v0.64.0'
  repositoryIdentity: RepositoryIdentityAlias
  global: RepositoryAgentConfigurationRow
  repository: RepositoryAgentConfigurationRow
}
export type RepositoryAgentConfigurationCommand = { executable: 'roborev'; args: string[] }
export type RepositoryAgentConfigurationPreview = {
  version: 'v0.64.0'
  repositoryIdentity: RepositoryIdentityAlias
  scope: RepositoryAgentConfigurationScope
  agent: RepositoryAgent
  currentStatus: RepositoryAgentConfigurationStatus
  currentAgent?: RepositoryAgent
  action: 'set-agent' | 'already-configured'
  target: string
  summary: string
  token?: string
  expiresAt?: string
  command?: RepositoryAgentConfigurationCommand
}

export type RepositoryAgentConfigurationExecuteResult = { status: 'verified-success' | 'failed-unverified' }

export const repositoryAgents: readonly RepositoryAgent[] = ['codex', 'claude-code', 'gemini', 'copilot', 'opencode', 'cursor', 'kiro', 'kilo', 'droid', 'pi', 'grok']
const repositoryAgentScopes: readonly RepositoryAgentConfigurationScope[] = ['global', 'repository']
const repositoryAgentStatuses: readonly RepositoryAgentConfigurationStatus[] = ['unset', 'canonical', 'custom']
const repositoryAgentVersion = 'v0.64.0' as const
const repositoryAgentPreviewActions = ['set-agent', 'already-configured'] as const
const repositoryAgentMaxTokenLength = 64
const repositoryAgentTargets: Record<RepositoryAgentConfigurationScope, string> = { global: 'Global default agent', repository: 'Repository agent' }
const repositoryAgentSummaries: Record<RepositoryAgentConfigurationScope, string> = { global: 'Set the global default RoboRev agent.', repository: 'Set the selected repository RoboRev agent.' }

function normalizeRepositoryAgentRow(value: unknown): RepositoryAgentConfigurationRow | null {
  const object = exactObject(value, ['status']) ?? exactObject(value, ['status', 'agent'])
  if (!object || typeof object.status !== 'string' || !repositoryAgentStatuses.includes(object.status as RepositoryAgentConfigurationStatus)) return null
  const status = object.status as RepositoryAgentConfigurationStatus
  if (status === 'canonical') {
    if (typeof object.agent !== 'string' || !repositoryAgents.includes(object.agent as RepositoryAgent)) return null
    return { status, agent: object.agent as RepositoryAgent }
  }
  if (object.agent !== undefined) return null
  return { status: status as 'unset' | 'custom' }
}

/** Normalize the exact, privacy-preserving agent configuration status DTO. */
export function normalizeRepositoryAgentConfigurationStatus(value: unknown): RepositoryAgentConfigurationStatusResponse | null {
  const root = exactObject(value, ['version', 'repositoryIdentity', 'global', 'repository'])
  if (!root || root.version !== repositoryAgentVersion || !validRepositoryIdentityAlias(root.repositoryIdentity)) return null
  const global = normalizeRepositoryAgentRow(root.global)
  const repository = normalizeRepositoryAgentRow(root.repository)
  if (!global || !repository) return null
  return { version: repositoryAgentVersion, repositoryIdentity: root.repositoryIdentity, global, repository }
}

/** Normalize the exact, content-free agent configuration preview DTO. */
export function normalizeRepositoryAgentConfigurationPreview(value: unknown, expectedRepositoryIdentity?: string, expectedScope?: RepositoryAgentConfigurationScope, expectedAgent?: RepositoryAgent): RepositoryAgentConfigurationPreview | null {
  const setKeys = ['version', 'repositoryIdentity', 'scope', 'agent', 'currentStatus', 'action', 'target', 'summary', 'token', 'expiresAt', 'command'] as const
  const setCanonicalKeys = [...setKeys.slice(0, 5), 'currentAgent', ...setKeys.slice(5)] as const
  const noOpKeys = ['version', 'repositoryIdentity', 'scope', 'agent', 'currentStatus', 'currentAgent', 'action', 'target', 'summary'] as const
  const setRoot = exactObject(value, setKeys) ?? exactObject(value, setCanonicalKeys)
  const noOpRoot = exactObject(value, noOpKeys)
  const root = setRoot ?? noOpRoot
  if (!root || root.version !== repositoryAgentVersion || !validRepositoryIdentityAlias(root.repositoryIdentity) || (expectedRepositoryIdentity !== undefined && root.repositoryIdentity !== expectedRepositoryIdentity) || typeof root.scope !== 'string' || !repositoryAgentScopes.includes(root.scope as RepositoryAgentConfigurationScope) || (expectedScope !== undefined && root.scope !== expectedScope) || typeof root.agent !== 'string' || !repositoryAgents.includes(root.agent as RepositoryAgent) || (expectedAgent !== undefined && root.agent !== expectedAgent) || typeof root.currentStatus !== 'string' || !repositoryAgentStatuses.includes(root.currentStatus as RepositoryAgentConfigurationStatus) || typeof root.target !== 'string' || root.target !== repositoryAgentTargets[root.scope as RepositoryAgentConfigurationScope] || typeof root.summary !== 'string' || root.summary !== repositoryAgentSummaries[root.scope as RepositoryAgentConfigurationScope] || typeof root.action !== 'string' || !repositoryAgentPreviewActions.includes(root.action as typeof repositoryAgentPreviewActions[number])) return null
  const scope = root.scope as RepositoryAgentConfigurationScope
  const agent = root.agent as RepositoryAgent
  const currentStatus = root.currentStatus as RepositoryAgentConfigurationStatus
  if (currentStatus === 'canonical') {
    if (typeof root.currentAgent !== 'string' || !repositoryAgents.includes(root.currentAgent as RepositoryAgent)) return null
  } else if (root.currentAgent !== undefined) return null
  if (root.action === 'already-configured') {
    if (!noOpRoot || root.currentStatus !== 'canonical' || root.currentAgent !== agent) return null
    return { version: repositoryAgentVersion, repositoryIdentity: root.repositoryIdentity, scope, agent, currentStatus, currentAgent: agent, action: 'already-configured', target: root.target, summary: root.summary }
  }
  if (!setRoot || typeof root.token !== 'string' || !/^[A-Za-z0-9_-]+$/.test(root.token) || root.token.length > repositoryAgentMaxTokenLength || !strictRfc3339(root.expiresAt)) return null
  const command = exactObject(root.command, ['executable', 'args'])
  const expectedArgs = scope === 'global' ? ['config', 'set', 'default_agent', agent, '--global'] : ['config', 'set', 'agent', agent, '--local']
  if (!command || command.executable !== 'roborev' || !Array.isArray(command.args) || command.args.length !== expectedArgs.length || !command.args.every((item, index) => item === expectedArgs[index])) return null
  return { version: repositoryAgentVersion, repositoryIdentity: root.repositoryIdentity, scope, agent, currentStatus, ...(currentStatus === 'canonical' ? { currentAgent: root.currentAgent as RepositoryAgent } : {}), action: 'set-agent', target: root.target, summary: root.summary, token: root.token, expiresAt: root.expiresAt as string, command: { executable: 'roborev', args: [...expectedArgs] } }
}

/** Normalize the content-free post-execute receipt. */
export function normalizeRepositoryAgentConfigurationExecuteResult(value: unknown): RepositoryAgentConfigurationExecuteResult | null {
  const root = exactObject(value, ['status'])
  if (!root || (root.status !== 'verified-success' && root.status !== 'failed-unverified')) return null
  return { status: root.status }
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

export type RepositoryTrackingPreview = {
  version: 'v0.64.0'
  repositoryIdentity: RepositoryIdentityAlias
  action: 'register-repository'
  target: 'RoboRev tracking'
  summary: 'Register the selected repository with the running RoboRev daemon.'
  token: string
  expiresAt: string
}

export type RepositoryTrackingExecuteResult = { status: 'verified-success' | 'failed-unverified' }

const snapshotIgnoreVersion = 'v0.64.0' as const
const snapshotIgnoreAction = 'append-snapshot-ignore' as const
const snapshotIgnoreTarget = '.gitignore' as const
const snapshotIgnoreSummary = 'Add the RoboRev snapshot directory to Git ignore rules.' as const
// RFC 3339 is intentionally stricter than Date.parse (which accepts many
// browser-specific date formats).  Date.parse then rejects impossible dates.
const rfc3339Pattern = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/
const snapshotIgnoreMaxTokenLength = 4096
const repositoryTrackingVersion = 'v0.64.0' as const
const repositoryTrackingAction = 'register-repository' as const
const repositoryTrackingTarget = 'RoboRev tracking' as const
const repositoryTrackingSummary = 'Register the selected repository with the running RoboRev daemon.' as const
const repositoryTrackingMaxTokenLength = 4096

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

/** Normalize the exact, content-free Repository Tracking preview DTO. */
export function normalizeRepositoryTrackingPreview(value: unknown, expectedRepositoryIdentity?: string): RepositoryTrackingPreview | null {
  const root = exactObject(value, ['version', 'repositoryIdentity', 'action', 'target', 'summary', 'token', 'expiresAt'])
  if (!root || root.version !== repositoryTrackingVersion || !validRepositoryIdentityAlias(root.repositoryIdentity) || (expectedRepositoryIdentity !== undefined && root.repositoryIdentity !== expectedRepositoryIdentity) || root.action !== repositoryTrackingAction || root.target !== repositoryTrackingTarget || root.summary !== repositoryTrackingSummary) return null
  if (typeof root.token !== 'string' || !root.token.trim() || root.token.length > repositoryTrackingMaxTokenLength || /[\u0000-\u001F\u007F]/.test(root.token)) return null
  if (typeof root.expiresAt !== 'string' || !rfc3339Pattern.test(root.expiresAt) || !Number.isFinite(Date.parse(root.expiresAt))) return null
  return { version: repositoryTrackingVersion, repositoryIdentity: root.repositoryIdentity, action: repositoryTrackingAction, target: repositoryTrackingTarget, summary: repositoryTrackingSummary, token: root.token, expiresAt: root.expiresAt }
}

/** Normalize the exact, content-free Repository Tracking execute result DTO. */
export function normalizeRepositoryTrackingExecuteResult(value: unknown): RepositoryTrackingExecuteResult | null {
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

export type RepositoryRenameCommand = { executable: 'roborev'; args: string[] }
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

export type RepositoryMoveCommand = { executable: 'roborev'; args: string[]; workingDirectory: string }
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
export type RepositoryMergeCommand = { executable: 'roborev'; args: string[]; workingDirectory: string }
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
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null
  const seen = new Set<object>()
  const forbidden = new Set(['binary', 'env', 'stdout', 'stderr', 'rawerror'])
  const containsForbidden = (candidate: unknown): boolean => {
    if (!candidate || typeof candidate !== 'object') return false
    if (seen.has(candidate as object)) return false
    seen.add(candidate as object)
    if (Array.isArray(candidate)) return candidate.some(containsForbidden)
    for (const [key, child] of Object.entries(candidate as Record<string, unknown>)) {
      if (forbidden.has(key.replace(/[_-]/g, '').toLowerCase()) || containsForbidden(child)) return true
    }
    return false
  }
  return containsForbidden(value) ? null : value as Record<string, unknown>
}

function hasExactKeys(object: Record<string, unknown>, expected: readonly string[]): boolean {
  const keys = Object.keys(object)
  return keys.length === expected.length && expected.every(key => Object.prototype.hasOwnProperty.call(object, key))
}

function renameCommand(value: unknown): RepositoryRenameCommand | null {
  const command = renameObject(value)
  if (!command || !hasExactKeys(command, ['executable', 'args']) || command.executable !== 'roborev' || !Array.isArray(command.args) || command.args.length !== 4 || !command.args.every(item => typeof item === 'string')) return null
  return { executable: 'roborev', args: command.args as string[] }
}

function moveCommand(value: unknown): RepositoryMoveCommand | null {
  const command = renameObject(value)
  if (!command || !hasExactKeys(command, ['executable', 'args', 'workingDirectory']) || command.executable !== 'roborev' || !Array.isArray(command.args) || command.args.length !== 4 || !command.args.every(item => typeof item === 'string') || typeof command.workingDirectory !== 'string' || !command.workingDirectory) return null
  return { executable: 'roborev', args: command.args as string[], workingDirectory: command.workingDirectory }
}

function mergeCommand(value: unknown): RepositoryMergeCommand | null {
  const command = renameObject(value)
  if (!command || !hasExactKeys(command, ['executable', 'args', 'workingDirectory']) || command.executable !== 'roborev' || !Array.isArray(command.args) || command.args.length !== 5 || !command.args.every(item => typeof item === 'string') || command.workingDirectory !== '') return null
  return { executable: 'roborev', args: command.args as string[], workingDirectory: command.workingDirectory }
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
  if (!object || !hasExactKeys(object, ['sourcePath', 'sourceName', 'jobCount', 'destinationRepositoryIdentity', 'destinationPath', 'destinationName', 'command', 'token', 'expiresAt'])) return null
  const command = moveCommand(object.command)
  if (typeof object.sourcePath !== 'string' || object.sourcePath.length === 0 || typeof object.sourceName !== 'string' || typeof object.destinationRepositoryIdentity !== 'string' || !object.destinationRepositoryIdentity.trim() || typeof object.destinationPath !== 'string' || object.destinationPath.length === 0 || typeof object.destinationName !== 'string' || !nonNegativeSafeInteger(object.jobCount) || !command || command.args[0] !== 'repo' || command.args[1] !== 'move' || command.args[2] !== object.sourcePath || command.args[3] !== '.' || command.workingDirectory !== object.destinationPath || typeof object.token !== 'string' || !object.token.trim() || !rfc3339(object.expiresAt)) return null
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
  if (!object || !hasExactKeys(object, ['path', 'currentName', 'newName', 'jobCount', 'dashboardRegistered', 'command', 'token', 'expiresAt'])) return null
  const command = renameCommand(object.command)
  if (typeof object.path !== 'string' || object.path.length === 0 || typeof object.currentName !== 'string' || typeof object.newName !== 'string' || object.newName.trim() === '' || !nonNegativeSafeInteger(object.jobCount) || typeof object.dashboardRegistered !== 'boolean' || !command || command.args[0] !== 'repo' || command.args[1] !== 'rename' || command.args[2] !== object.path || command.args[3] !== object.newName || typeof object.token !== 'string' || !object.token.trim() || !rfc3339(object.expiresAt)) return null
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
export type StatsRepositorySummary = { name: string; total: number; passed: number; failed: number; addressed: number }
export type StatsSummaryResponse = {
  repositoryIdentity: string
  window: StatsWindow
  branchScope: StatsBranchScope
  summary: {
    $schema?: string
    since: string
    branch?: string
    repos?: StatsRepositorySummary[]
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

export type AgentCommand = typeof BACKEND_COMMAND_SENTINEL | typeof BACKEND_UNRESOLVED_COMMAND_SENTINEL
// API normalization must not embed a display-language string. The Setup UI
// maps this stable sentinel to the active catalog language.
export const BACKEND_COMMAND_SENTINEL = '__roborev_backend_command__'
// The daemon can report an inherited/default agent whose executable cannot be
// resolved. Keep that fact as a stable, content-free value; never forward the
// source command or path to the browser.
export const BACKEND_UNRESOLVED_COMMAND_SENTINEL = 'Detection unavailable'

export type ConfiguredReviewAgent = {
  name: string
  agent: string
  model?: string
  reasoning?: string
  command: AgentCommand
  commandSource?: string
  detected: boolean
  detectionReason?: string
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
export type DaemonCommandPreview = { executable: 'roborev'; args: ['daemon', DaemonAction] }
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

const projectionForbiddenKeys = new Set(['repositorypath', 'path', 'binary', 'binarypath', 'cwd', 'workingdirectory', 'env', 'stdout', 'stderr', 'rawerror', 'resolvedpath', 'commandline', 'worktreepath', 'prompt', 'diff', 'patch'])
function containsForbiddenProjectionKey(value: unknown, seen = new Set<object>()): boolean {
  if (!value || typeof value !== 'object') return false
  const object = value as object
  if (seen.has(object)) return false
  seen.add(object)
  if (Array.isArray(value)) return value.some(item => containsForbiddenProjectionKey(item, seen))
  return Object.entries(value as Record<string, unknown>).some(([key, child]) => projectionForbiddenKeys.has(key.replace(/[_-]/g, '').toLowerCase()) || containsForbiddenProjectionKey(child, seen))
}

function strictProjectionText(value: unknown, maxLength = 256): value is string {
  return typeof value === 'string' && value.length <= maxLength && !/[\u0000-\u001f\u007f]/.test(value)
}

function objectWithKeys(value: unknown, required: readonly string[], optional: readonly string[] = []): Record<string, unknown> | null {
  const object = objectValue(value)
  if (!object || containsForbiddenProjectionKey(object)) return null
  const allowed = new Set([...required, ...optional])
  if (Object.keys(object).some(key => !allowed.has(key)) || required.some(key => !Object.prototype.hasOwnProperty.call(object, key))) return null
  return object
}

function normalizeDaemonCommands(value: unknown): DaemonCommands | undefined {
  const object = objectWithKeys(value, ['start', 'stop', 'restart'])
  if (!object) return undefined
  const actions: DaemonAction[] = ['start', 'stop', 'restart']
  const result = {} as DaemonCommands
  for (const action of actions) {
    const command = objectWithKeys(object[action], ['executable', 'args'])
    if (!command || command.executable !== 'roborev' || !Array.isArray(command.args) || command.args.length !== 2 || command.args[0] !== 'daemon' || command.args[1] !== action) return undefined
    result[action] = { executable: 'roborev', args: ['daemon', action] }
  }
  return result
}

function normalizeAgentHealth(value: unknown): AgentHealth {
  if (typeof value !== 'string') return 'not_checked'
  const normalized = value.toLowerCase().replace(/[ -]/g, '_')
  return agentHealthValues.includes(normalized as AgentHealth) ? normalized as AgentHealth : 'not_checked'
}

function normalizeAgentCommand(value: unknown): AgentCommand {
  if (value === BACKEND_COMMAND_SENTINEL || value === BACKEND_UNRESOLVED_COMMAND_SENTINEL) return value
  return BACKEND_COMMAND_SENTINEL
}

/** Parse the machine-readable configured-agent response. Unknown/private fields fail closed. */
export function normalizeAgentsResponse(value: unknown): AgentsResponse | null {
  const object = objectWithKeys(value, ['repositoryIdentity', 'configured'], ['roborevVersion'])
  if (!object || !strictProjectionText(object.repositoryIdentity) || !object.repositoryIdentity.trim() || !Array.isArray(object.configured) || (object.roborevVersion !== undefined && object.roborevVersion !== 'v0.64.0')) return null
  const configured: ConfiguredReviewAgent[] = []
  for (const item of object.configured) {
    const agent = objectWithKeys(item, ['name', 'agent', 'command', 'detected', 'health'], ['model', 'reasoning', 'commandSource', 'detectionReason', 'lastCheckedAt'])
    if (!agent || !strictProjectionText(agent.name) || !agent.name.trim() || !strictProjectionText(agent.agent) || !agent.agent.trim() || typeof agent.detected !== 'boolean' || !agentHealthValues.includes(agent.health as AgentHealth) || (agent.model !== undefined && !strictProjectionText(agent.model)) || (agent.reasoning !== undefined && !strictProjectionText(agent.reasoning)) || (agent.commandSource !== undefined && !strictProjectionText(agent.commandSource)) || (agent.detectionReason !== undefined && !strictProjectionText(agent.detectionReason)) || (agent.lastCheckedAt !== undefined && !strictRfc3339(agent.lastCheckedAt))) return null
    const command = normalizeAgentCommand(agent.command)
    if (agent.command !== command) return null
    configured.push({
      name: agent.name,
      agent: agent.agent,
      model: optionalString(agent.model),
      reasoning: optionalString(agent.reasoning),
      command,
      commandSource: optionalString(readAlias(agent, 'commandSource', 'command_source')),
      detected: agent.detected,
      detectionReason: optionalString(readAlias(agent, 'detectionReason', 'detection_reason')),
      health: agent.health as AgentHealth,
      lastCheckedAt: optionalString(readAlias(agent, 'lastCheckedAt', 'last_checked_at')),
    })
  }
  return { repositoryIdentity: object.repositoryIdentity, roborevVersion: optionalString(object.roborevVersion), configured }
}

/** Parse runtime/queue capabilities and fail closed to unknown queue controls. */
export function normalizeRuntimeControl(value: unknown): RuntimeControlState | null {
  const object = objectWithKeys(value, ['daemonRunning', 'operationActive', 'queueState', 'canPause', 'canResume', 'canStart', 'canStop', 'canRestart', 'startCommand', 'stopCommand', 'restartCommand', 'daemonCommands'], ['daemonOperation', 'disabledReason', 'error'])
  if (!object || typeof object.daemonRunning !== 'boolean' || typeof object.operationActive !== 'boolean' || typeof object.queueState !== 'string' || !queueStateValues.includes(object.queueState as QueueState) || ![object.canPause, object.canResume, object.canStart, object.canStop, object.canRestart].every(item => typeof item === 'boolean')) return null
  const operationActive = object.operationActive
  const queueState = object.queueState as QueueState
  const daemonCommands = normalizeDaemonCommands(object.daemonCommands)
  const commandFor = (action: DaemonAction) => {
    const command = objectWithKeys(object[`${action}Command`], ['executable', 'args'])
    if (!command || command.executable !== 'roborev' || !Array.isArray(command.args) || command.args.length !== 2 || command.args[0] !== 'daemon' || command.args[1] !== action) return null
    return { executable: 'roborev' as const, args: ['daemon', action] as ['daemon', DaemonAction] }
  }
  if (!daemonCommands || !commandFor('start') || !commandFor('stop') || !commandFor('restart')) return null
  return {
    daemonRunning: object.daemonRunning,
    daemonOperation: object.daemonOperation === undefined ? undefined : (typeof object.daemonOperation === 'string' && ['start', 'stop', 'restart', 'pause', 'resume'].includes(object.daemonOperation) ? object.daemonOperation : undefined),
    operationActive,
    queueState,
    canPause: object.canPause as boolean,
    canResume: object.canResume as boolean,
    canStart: object.canStart as boolean,
    canStop: object.canStop as boolean,
    canRestart: object.canRestart as boolean,
    daemonCommands,
    disabledReason: object.disabledReason === undefined ? undefined : (typeof object.disabledReason === 'string' && object.disabledReason.length <= 256 ? object.disabledReason : undefined),
    error: object.error === undefined ? undefined : (typeof object.error === 'string' && object.error.length <= 256 ? object.error : undefined),
  }
}

function statsNumber(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0
}

function statsCount(value: unknown): value is number {
  return nonNegativeSafeInteger(value)
}

function statsText(value: unknown): value is string {
  return strictProjectionText(value) && value.trim().length > 0
}

function statsRecord(value: unknown): Record<string, unknown> | null {
  return objectValue(value)
}

function normalizeStatsOverview(value: unknown): StatsSummaryResponse['summary']['overview'] | null {
  const object = exactObject(value, ['total', 'queued', 'running', 'done', 'failed', 'canceled', 'applied', 'rebased'])
  if (!object || !Object.values(object).every(statsCount)) return null
  return object as StatsSummaryResponse['summary']['overview']
}

function normalizeStatsVerdicts(value: unknown): StatsSummaryResponse['summary']['verdicts'] | null {
  const object = exactObject(value, ['total', 'passed', 'failed', 'addressed', 'pass_rate', 'resolution_rate'])
  if (!object || !statsCount(object.total) || !statsCount(object.passed) || !statsCount(object.failed) || !statsCount(object.addressed) || !statsNumber(object.pass_rate) || object.pass_rate > 1 || !statsNumber(object.resolution_rate) || object.resolution_rate > 1) return null
  return object as StatsSummaryResponse['summary']['verdicts']
}

function normalizeStatsDuration(value: unknown): StatsSummaryResponse['summary']['duration'] | null {
  const object = exactObject(value, ['review_p50_secs', 'review_p90_secs', 'review_p99_secs', 'queue_p50_secs', 'queue_p90_secs', 'queue_p99_secs'])
  if (!object || !Object.values(object).every(statsNumber)) return null
  return object as StatsSummaryResponse['summary']['duration']
}

function normalizeStatsAgents(value: unknown): StatsSummaryResponse['summary']['agents'] | null {
  if (!Array.isArray(value)) return null
  const agents: StatsSummaryResponse['summary']['agents'] = []
  for (const item of value) {
    const object = exactObject(item, ['agent', 'total', 'passed', 'failed', 'errors', 'pass_rate', 'median_duration_secs'])
    if (!object || !statsText(object.agent) || !statsCount(object.total) || !statsCount(object.passed) || !statsCount(object.failed) || !statsCount(object.errors) || !statsNumber(object.pass_rate) || object.pass_rate > 1 || !statsNumber(object.median_duration_secs)) return null
    agents.push({ agent: object.agent, total: object.total, passed: object.passed, failed: object.failed, errors: object.errors, pass_rate: object.pass_rate, median_duration_secs: object.median_duration_secs })
  }
  return agents
}

function normalizeStatsJobTypes(value: unknown): StatsSummaryResponse['summary']['job_types'] | null {
  if (!Array.isArray(value)) return null
  const jobTypes: StatsSummaryResponse['summary']['job_types'] = []
  for (const item of value) {
    const object = objectWithKeys(item, ['type', 'count'], ['applied', 'rebased'])
    if (!object || !statsText(object.type) || !statsCount(object.count) || (object.applied !== undefined && !statsCount(object.applied)) || (object.rebased !== undefined && !statsCount(object.rebased))) return null
    jobTypes.push({ type: object.type, count: object.count, ...(object.applied === undefined ? {} : { applied: object.applied }), ...(object.rebased === undefined ? {} : { rebased: object.rebased }) })
  }
  return jobTypes
}

function normalizeStatsFailures(value: unknown): StatsSummaryResponse['summary']['failures'] | null {
  const object = exactObject(value, ['total', 'retries', 'errors'])
  const errors = statsRecord(object?.errors)
  if (!object || !statsCount(object.total) || !statsCount(object.retries) || !errors) return null
  const safeErrors: Record<string, number> = {}
  for (const [name, count] of Object.entries(errors)) {
    if (!strictProjectionText(name, 128) || !statsCount(count)) return null
    safeErrors[name] = count
  }
  return { total: object.total, retries: object.retries, errors: safeErrors }
}

function normalizeStatsCost(value: unknown): StatsSummaryResponse['summary']['cost'] | null {
  const object = objectWithKeys(value, ['total_usd', 'jobs_with_cost', 'jobs_total', 'complete'], ['$schema'])
  if (!object || (object.$schema !== undefined && !strictProjectionText(object.$schema)) || !statsNumber(object.total_usd) || !statsCount(object.jobs_with_cost) || !statsCount(object.jobs_total) || typeof object.complete !== 'boolean') return null
  return { ...(object.$schema === undefined ? {} : { $schema: object.$schema }), total_usd: object.total_usd, jobs_with_cost: object.jobs_with_cost, jobs_total: object.jobs_total, complete: object.complete }
}

/** Normalize the path-free, server-owned Stats summary projection. */
export function normalizeStatsSummaryResponse(value: unknown, expectedRepositoryIdentity?: string): StatsSummaryResponse | null {
  const root = exactObject(value, ['repositoryIdentity', 'window', 'branchScope', 'summary'])
  const summary = root ? objectWithKeys(root.summary, ['since', 'overview', 'verdicts', 'duration', 'agents', 'job_types', 'failures', 'cost'], ['$schema', 'branch', 'repos']) : null
  if (!root || !statsText(root.repositoryIdentity) || (expectedRepositoryIdentity !== undefined && root.repositoryIdentity !== expectedRepositoryIdentity) || !['24h', '7d', '30d', '90d'].includes(String(root.window)) || !['all', 'current'].includes(String(root.branchScope)) || !summary || !strictRfc3339(summary.since) || (summary.$schema !== undefined && !strictProjectionText(summary.$schema)) || (summary.branch !== undefined && !strictProjectionText(summary.branch))) return null
  const overview = normalizeStatsOverview(summary.overview)
  const verdicts = normalizeStatsVerdicts(summary.verdicts)
  const duration = normalizeStatsDuration(summary.duration)
  const agents = normalizeStatsAgents(summary.agents)
  const jobTypes = normalizeStatsJobTypes(summary.job_types)
  const failures = normalizeStatsFailures(summary.failures)
  const cost = normalizeStatsCost(summary.cost)
  if (!overview || !verdicts || !duration || !agents || !jobTypes || !failures || !cost) return null
  let repos: StatsRepositorySummary[] | undefined
  if (summary.repos !== undefined) {
    if (!Array.isArray(summary.repos)) return null
    repos = []
    for (const item of summary.repos) {
      const object = exactObject(item, ['name', 'total', 'passed', 'failed', 'addressed'])
      if (!object || !statsText(object.name) || !statsCount(object.total) || !statsCount(object.passed) || !statsCount(object.failed) || !statsCount(object.addressed)) return null
      repos.push({ name: object.name, total: object.total, passed: object.passed, failed: object.failed, addressed: object.addressed })
    }
  }
  return {
    repositoryIdentity: root.repositoryIdentity,
    window: root.window as StatsWindow,
    branchScope: root.branchScope as StatsBranchScope,
    summary: {
      ...(summary.$schema === undefined ? {} : { $schema: summary.$schema }),
      since: summary.since,
      ...(summary.branch === undefined ? {} : { branch: summary.branch }),
      overview, verdicts, duration, agents, job_types: jobTypes, failures,
      ...(repos === undefined ? {} : { repos }), cost,
    },
  }
}

/** Normalize the path-free, server-owned Stats cost projection. */
export function normalizeStatsCostResponse(value: unknown, expectedRepositoryIdentity?: string): StatsCostResponse | null {
  const root = exactObject(value, ['repositoryIdentity', 'branchScope', 'cost'])
  const cost = root ? objectWithKeys(root.cost, ['total_usd', 'jobs_with_cost', 'jobs_total', 'complete'], ['$schema']) : null
  if (!root || !statsText(root.repositoryIdentity) || (expectedRepositoryIdentity !== undefined && root.repositoryIdentity !== expectedRepositoryIdentity) || !['all', 'current'].includes(String(root.branchScope)) || !cost || (cost.$schema !== undefined && !strictProjectionText(cost.$schema)) || !statsNumber(cost.total_usd) || !statsCount(cost.jobs_with_cost) || !statsCount(cost.jobs_total) || typeof cost.complete !== 'boolean') return null
  return { repositoryIdentity: root.repositoryIdentity, branchScope: root.branchScope as StatsBranchScope, cost: { ...(cost.$schema === undefined ? {} : { $schema: cost.$schema }), total_usd: cost.total_usd, jobs_with_cost: cost.jobs_with_cost, jobs_total: cost.jobs_total, complete: cost.complete } }
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
export type PanelRefineErrorCode = 'panel_refine_error' | 'repository_state_unavailable'
export type PanelRefineStopReason = 'maximum_fix_attempts' | 'repository_changed' | 'fix_did_not_commit' | 'fix_left_dirty' | 'review_failed' | 'fix_failed' | 'malformed_review_receipt' | 'malformed_review_status' | 'cancelled'
export type PanelRefineCommand = { executable: 'roborev'; args: ['fix', '--agent', 'codex', '--quiet', '<synthesisJobID>'] }

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
  errorCode?: PanelRefineErrorCode
}

export type PanelRefineRun = {
  id: number | string
  repositoryIdentity: string
  branch: string
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
  startedAt: string
  updatedAt: string
  completedAt?: string
  errorCode?: PanelRefineErrorCode
  stopReason?: PanelRefineStopReason
  command: PanelRefineCommand
  cancelState?: { available: boolean; head?: string; branch?: string; dirty?: boolean; errorCode?: 'repository_state_unavailable' }
  iterations: PanelRefineIteration[]
}

export type PanelRefineOptions = {
  executable: 'roborev'
  args: ['fix', '--agent', 'codex', '--quiet', '<synthesisJobID>']
  allowedFixAgents: ['codex']
  defaultFixAgent: 'codex'
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

/**
 * Browser-safe fix command projection.  The server may execute a configured
 * binary, but that private path is not part of the browser contract.
 */
export type FixCommand = { executable: 'roborev'; args: string[] }
export type FixPreview = {
  repositoryIdentity: string
  target: FixTarget
  agent: string
  command: FixCommand
  startingHead: string
  startingBranch: string
  previewToken: string
}
export type FixPhase = 'starting' | 'running' | 'succeeded' | 'failed' | 'cancelled'
export type FixErrorCode = 'fix_command_failed' | 'fix_did_not_commit' | 'fix_left_dirty' | 'repository_changed' | 'repository_unavailable' | 'operation_timed_out' | 'cancelled'
export type FixOperation = {
  id: number | string
  repositoryIdentity: string
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
  errorCode?: FixErrorCode
}
export type FixOperationsResponse = { operations: FixOperation[]; operation: FixOperation | null }

function readFixAlias(object: Record<string, unknown>, camel: string, snake: string) { return object[camel] ?? object[snake] }
function fixString(object: Record<string, unknown>, camel: string, snake = camel) {
  const value = readFixAlias(object, camel, snake)
  return typeof value === 'string' && value.length <= 512 && !/[\u0000-\u001f\u007f\\]/.test(value) ? value : undefined
}
function fixNumber(object: Record<string, unknown>, camel: string, snake = camel) {
  const value = readFixAlias(object, camel, snake)
  if (typeof value === 'number') return Number.isSafeInteger(value) ? value : undefined
  if (typeof value !== 'string' || !/^\d+$/.test(value)) return undefined
  const parsed = Number(value)
  return Number.isSafeInteger(parsed) ? parsed : undefined
}
function parseFixTarget(value: unknown): FixTarget | null {
  const object = objectValue(value)
  if (!object || hasForbiddenFixKey(object) || !hasOnlyFixKeys(object, fixTargetKeys)) return null
  const jobId = fixNumber(object, 'jobId')
  const gitRef = fixString(object, 'gitRef')
  const branch = fixString(object, 'branch')
  const jobType = fixString(object, 'jobType')
  const panelRole = fixString(object, 'panelRole')
  if (jobId === undefined || jobId <= 0 || !gitRef || !branch || !jobType || typeof panelRole !== 'string') return null
  return { jobId, gitRef, branch, jobType, panelRole, panelName: fixString(object, 'panelName') }
}
const forbiddenFixKeys = new Set(['repositorypath', 'path', 'binary', 'binarypath', 'cwd', 'env', 'stdout', 'stderr', 'rawerror'])
const fixPreviewKeys = new Set(['repositoryIdentity', 'target', 'agent', 'command', 'startingHead', 'startingBranch', 'previewToken'])
const fixOperationKeys = new Set(['id', 'repositoryIdentity', 'target', 'agent', 'phase', 'status', 'command', 'startingHead', 'startingBranch', 'newHead', 'endingHead', 'endingBranch', 'endingDirty', 'startedAt', 'updatedAt', 'completedAt', 'errorCode'])
const fixErrorCodes = new Set<FixErrorCode>(['fix_command_failed', 'fix_did_not_commit', 'fix_left_dirty', 'repository_changed', 'repository_unavailable', 'operation_timed_out', 'cancelled'])
const fixOperationsResponseKeys = new Set(['operations', 'operation'])
const fixTargetKeys = new Set(['jobId', 'gitRef', 'branch', 'jobType', 'panelRole', 'panelName'])
const fixCommandKeys = new Set(['executable', 'args'])
function hasOnlyFixKeys(object: Record<string, unknown>, allowed: Set<string>) { return Object.keys(object).every(key => allowed.has(key)) }
function hasForbiddenFixKey(value: unknown, seen = new Set<object>()): boolean {
  if (!value || typeof value !== 'object') return false
  if (seen.has(value)) return false
  seen.add(value)
  for (const [key, child] of Object.entries(value as Record<string, unknown>)) {
    const normalized = key.replace(/[_-]/g, '').toLowerCase()
    if (forbiddenFixKeys.has(normalized) || hasForbiddenFixKey(child, seen)) return true
  }
  return false
}
function parseFixErrorCode(object: Record<string, unknown>): FixErrorCode | null | undefined {
  const value = object.errorCode
  if (value === undefined) return undefined
  return typeof value === 'string' && fixErrorCodes.has(value as FixErrorCode) ? value as FixErrorCode : null
}
function parseFixCommand(value: unknown, targetJobId: number, agent: string): FixCommand | null {
  const object = objectValue(value)
  if (!object || hasForbiddenFixKey(object) || !hasOnlyFixKeys(object, fixCommandKeys) || object.executable !== 'roborev' || !Array.isArray(object.args) || !object.args.every(item => typeof item === 'string')) return null
  const args = object.args as string[]
  if (agent !== 'codex' || args.length !== 5 || args[0] !== 'fix' || args[1] !== '--agent' || args[2] !== agent || args[3] !== '--quiet' || args[4] !== String(targetJobId)) return null
  return { executable: 'roborev', args }
}
function parseFixPreview(value: unknown): FixPreview | null {
  const object = objectValue(value)
  if (!object || hasForbiddenFixKey(object) || !hasOnlyFixKeys(object, fixPreviewKeys) || typeof object.repositoryIdentity !== 'string' || typeof object.agent !== 'string' || typeof object.startingHead !== 'string' || typeof object.startingBranch !== 'string' || typeof object.previewToken !== 'string') return null
  const target = parseFixTarget(object.target)
  const command = target && parseFixCommand(object.command, target.jobId, object.agent)
  if (!target || !command) return null
  return { repositoryIdentity: object.repositoryIdentity, target, agent: object.agent, command, startingHead: object.startingHead, startingBranch: object.startingBranch, previewToken: object.previewToken }
}
function parseFixOperation(value: unknown): FixOperation | null {
  const object = objectValue(value)
  if (!object || hasForbiddenFixKey(object) || !hasOnlyFixKeys(object, fixOperationKeys) || (typeof object.id !== 'string' && typeof object.id !== 'number')) return null
  const phase = fixString(object, 'phase')
  const phases: FixPhase[] = ['starting', 'running', 'succeeded', 'failed', 'cancelled']
  const target = parseFixTarget(object.target)
  const agent = fixString(object, 'agent')
  const command = target && agent && parseFixCommand(object.command, target.jobId, agent)
  const repositoryIdentity = fixString(object, 'repositoryIdentity')
  const errorCode = parseFixErrorCode(object)
  if (!phase || !phases.includes(phase as FixPhase) || !target || !command || !repositoryIdentity || !agent || errorCode === null) return null
  const optionalBool = (key: string) => { const value = object[key]; return typeof value === 'boolean' ? value : undefined }
  return { id: object.id as number | string, repositoryIdentity, target, agent, command, phase: phase as FixPhase, startingHead: fixString(object, 'startingHead'), startingBranch: fixString(object, 'startingBranch'), endingHead: fixString(object, 'endingHead') ?? fixString(object, 'newHead'), endingBranch: fixString(object, 'endingBranch'), endingDirty: optionalBool('endingDirty'), startedAt: fixString(object, 'startedAt'), updatedAt: fixString(object, 'updatedAt'), completedAt: fixString(object, 'completedAt'), ...(errorCode === undefined ? {} : { errorCode: errorCode as FixErrorCode }) }
}
export function normalizeFixPreview(value: unknown): FixPreview | null { return parseFixPreview(value) }
export function normalizeFixOperation(value: unknown): FixOperation | null { return parseFixOperation(value) }
export function normalizeFixOperationsResponse(value: unknown): FixOperationsResponse {
  const object = objectValue(value)
  if (!object || hasForbiddenFixKey(object) || !hasOnlyFixKeys(object, fixOperationsResponseKeys)) return { operations: [], operation: null }
  const values = Array.isArray(object.operations) ? object.operations : []
  const operations = values.map(parseFixOperation).filter((item): item is FixOperation => Boolean(item))
  const direct = parseFixOperation(object.operation)
  const sorted = [...operations].sort((left, right) => String(right.updatedAt ?? right.startedAt ?? '').localeCompare(String(left.updatedAt ?? left.startedAt ?? '')))
  return { operations, operation: direct ?? sorted[0] ?? null }
}

function numberValue(value: unknown) {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : undefined
}
const panelRefinePhases: readonly PanelRefinePhase[] = ['idle', 'starting', 'reviewing', 'evaluating', 'fixing', 'final_reviewing', 'passed', 'needs_attention', 'failed', 'cancelled']
const panelRefineStopReasons: readonly PanelRefineStopReason[] = ['maximum_fix_attempts', 'repository_changed', 'fix_did_not_commit', 'fix_left_dirty', 'review_failed', 'fix_failed', 'malformed_review_receipt', 'malformed_review_status', 'cancelled']
const panelRefineErrorCodes: readonly PanelRefineErrorCode[] = ['panel_refine_error', 'repository_state_unavailable']
const panelRefineReviewResults = new Set(['', 'P', 'F', 'pass', 'fail', 'passed', 'failed'])
const panelRefineCommandKeys = ['executable', 'args'] as const
const panelRefineCommandArgs: PanelRefineCommand['args'] = ['fix', '--agent', 'codex', '--quiet', '<synthesisJobID>']
const panelRefineRunRequiredKeys = ['id', 'repositoryIdentity', 'branch', 'iterationPanel', 'fixAgent', 'maxFixAttempts', 'fixAttempts', 'phase', 'startingHead', 'currentHead', 'startedAt', 'updatedAt', 'iterations', 'command'] as const
const panelRefineRunOptionalKeys = ['finalPanel', 'currentReviewJobId', 'currentPanelRunUUID', 'completedAt', 'errorCode', 'stopReason', 'cancelState'] as const
const panelRefineIterationRequiredKeys = ['number', 'targetHead', 'panel', 'reviewJobId', 'panelRunUUID', 'reviewResult', 'fixAttempted'] as const
const panelRefineIterationOptionalKeys = ['fixAgent', 'headAfterFix', 'errorCode'] as const
const panelRefineOptionsRequiredKeys = ['executable', 'args', 'allowedFixAgents', 'defaultFixAgent', 'defaultMaxFixAttempts', 'minFixAttempts', 'maxFixAttempts'] as const
function panelText(value: unknown, maxLength = 512): value is string { return strictProjectionText(value, maxLength) }
function panelCommand(value: unknown): PanelRefineCommand | null {
  const object = exactObject(value, panelRefineCommandKeys)
  if (!object || object.executable !== 'roborev' || !Array.isArray(object.args) || object.args.length !== panelRefineCommandArgs.length || object.args.some((item, index) => item !== panelRefineCommandArgs[index])) return null
  return { executable: 'roborev', args: [...panelRefineCommandArgs] }
}
function parsePanelRefineOptions(value: unknown): PanelRefineOptions | null {
  const object = exactObject(value, panelRefineOptionsRequiredKeys)
  if (!object || object.executable !== 'roborev' || !Array.isArray(object.args) || object.args.length !== panelRefineCommandArgs.length || object.args.some((item, index) => item !== panelRefineCommandArgs[index]) || !Array.isArray(object.allowedFixAgents) || object.allowedFixAgents.length !== 1 || object.allowedFixAgents[0] !== 'codex' || object.defaultFixAgent !== 'codex') return null
  const defaultMax = numberValue(object.defaultMaxFixAttempts)
  const min = numberValue(object.minFixAttempts)
  const max = numberValue(object.maxFixAttempts)
  if (defaultMax === undefined || min === undefined || max === undefined || min !== 1 || max !== 10 || defaultMax !== 3) return null
  return { executable: 'roborev', args: [...panelRefineCommandArgs], allowedFixAgents: ['codex'], defaultFixAgent: 'codex', defaultMaxFixAttempts: defaultMax, minFixAttempts: min, maxFixAttempts: max }
}

function parsePanelRefineRun(value: unknown): PanelRefineRun | null {
  const object = objectWithKeys(value, panelRefineRunRequiredKeys, panelRefineRunOptionalKeys)
  if (!object || typeof object.id !== 'string' || !object.id || !panelText(object.repositoryIdentity) || !object.repositoryIdentity || !panelText(object.branch) || !panelText(object.iterationPanel) || !object.iterationPanel || !panelText(object.fixAgent) || object.fixAgent !== 'codex' || !panelText(object.startingHead) || !panelText(object.currentHead) || !panelRefinePhases.includes(object.phase as PanelRefinePhase) || !strictRfc3339(object.startedAt) || !strictRfc3339(object.updatedAt) || (object.completedAt !== undefined && !strictRfc3339(object.completedAt)) || !Array.isArray(object.iterations) || !panelCommand(object.command)) return null
  const maxFixAttempts = numberValue(object.maxFixAttempts)
  const fixAttempts = numberValue(object.fixAttempts)
  if (maxFixAttempts === undefined || fixAttempts === undefined || maxFixAttempts < 1 || maxFixAttempts > 10 || fixAttempts > maxFixAttempts) return null
  const command = panelCommand(object.command)
  if (!command) return null
  const currentReviewJobID = object.currentReviewJobId === undefined ? undefined : numberValue(object.currentReviewJobId)
  if (object.finalPanel !== undefined && (!panelText(object.finalPanel) || !object.finalPanel) || object.currentReviewJobId !== undefined && (currentReviewJobID === undefined || currentReviewJobID <= 0) || object.currentPanelRunUUID !== undefined && (!panelText(object.currentPanelRunUUID) || !object.currentPanelRunUUID)) return null
  if (object.errorCode !== undefined && !panelRefineErrorCodes.includes(object.errorCode as PanelRefineErrorCode)) return null
  if (object.stopReason !== undefined && !panelRefineStopReasons.includes(object.stopReason as PanelRefineStopReason)) return null
  const iterations: PanelRefineIteration[] = []
  for (const item of object.iterations) {
    const entry = objectWithKeys(item, panelRefineIterationRequiredKeys, panelRefineIterationOptionalKeys)
    const number = entry ? numberValue(entry.number) : undefined
    const reviewJobID = entry ? numberValue(entry.reviewJobId) : undefined
    if (!entry || number === undefined || number <= 0 || !panelText(entry.targetHead) || !panelText(entry.panel) || !entry.panel || reviewJobID === undefined || reviewJobID <= 0 || !panelText(entry.panelRunUUID) || !panelRefineReviewResults.has(entry.reviewResult as string) || typeof entry.fixAttempted !== 'boolean') return null
    if (entry.fixAgent !== undefined && (entry.fixAgent !== 'codex' || !panelText(entry.fixAgent)) || entry.headAfterFix !== undefined && !panelText(entry.headAfterFix) || entry.errorCode !== undefined && !panelRefineErrorCodes.includes(entry.errorCode as PanelRefineErrorCode)) return null
    iterations.push({ number, targetHead: entry.targetHead, panel: entry.panel, reviewJobID, panelRunUUID: entry.panelRunUUID as string, reviewResult: entry.reviewResult as string, fixAttempted: entry.fixAttempted, fixAgent: entry.fixAgent as string | undefined, headAfterFix: entry.headAfterFix as string | undefined, errorCode: entry.errorCode as PanelRefineErrorCode | undefined })
  }
  const cancel = object.cancelState
  let cancelState: PanelRefineRun['cancelState']
  if (cancel !== undefined) {
    const cancelObject = objectWithKeys(cancel, ['available'], ['head', 'branch', 'dirty', 'errorCode'])
    if (!cancelObject || typeof cancelObject.available !== 'boolean' || cancelObject.head !== undefined && !panelText(cancelObject.head) || cancelObject.branch !== undefined && !panelText(cancelObject.branch) || cancelObject.dirty !== undefined && typeof cancelObject.dirty !== 'boolean' || cancelObject.errorCode !== undefined && cancelObject.errorCode !== 'repository_state_unavailable') return null
    cancelState = { available: cancelObject.available, head: cancelObject.head as string | undefined, branch: cancelObject.branch as string | undefined, dirty: cancelObject.dirty as boolean | undefined, errorCode: cancelObject.errorCode as 'repository_state_unavailable' | undefined }
  }
  return { id: object.id, repositoryIdentity: object.repositoryIdentity, branch: object.branch, iterationPanel: object.iterationPanel, finalPanel: object.finalPanel as string | undefined, fixAgent: object.fixAgent, maxFixAttempts, fixAttempts, phase: object.phase as PanelRefinePhase, startingHead: object.startingHead as string, currentHead: object.currentHead as string, currentReviewJobID, currentPanelRunUUID: object.currentPanelRunUUID as string | undefined, startedAt: object.startedAt as string, updatedAt: object.updatedAt as string, completedAt: object.completedAt as string | undefined, errorCode: object.errorCode as PanelRefineErrorCode | undefined, stopReason: object.stopReason as PanelRefineStopReason | undefined, command, cancelState, iterations }
}

export function normalizePanelRefineResponse(value: unknown): PanelRefineResponse {
  const object = objectWithKeys(value, ['runs', 'options'], ['run'])
  if (!object || !Array.isArray(object.runs)) return { runs: [], run: null, options: null }
  const runs = object.runs.map(parsePanelRefineRun).filter((run): run is PanelRefineRun => Boolean(run))
  const direct = object.run === undefined ? null : parsePanelRefineRun(object.run)
  const options = parsePanelRefineOptions(object.options)
  if (object.run !== undefined && !direct || !options) return { runs: [], run: null, options: null }
  const active = (phase: PanelRefinePhase) => ['starting', 'reviewing', 'evaluating', 'fixing', 'final_reviewing'].includes(phase)
  runs.sort((left, right) => {
    if (active(left.phase) !== active(right.phase)) return active(left.phase) ? -1 : 1
    return String(right.updatedAt ?? right.startedAt ?? '').localeCompare(String(left.updatedAt ?? left.startedAt ?? ''))
  })
  return { runs, run: direct ?? runs[0] ?? null, options }
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
  // These fields are safe daemon state, retained only when the typed backend
  // projection explicitly supplies them. Repository/process authority is
  // intentionally absent from this browser model.
  review_id?: number
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
  state?: string
  verdict?: string
  verdict_bool?: boolean
  skipped?: boolean
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

const reviewJobKeys = new Set(['id', 'job_id', 'review_id', 'uuid', 'job_uuid', 'git_ref', 'commit_sha', 'branch', 'subject', 'commit_subject', 'job_type', 'agent', 'model', 'reasoning', 'status', 'state', 'verdict', 'verdict_bool', 'skipped', 'review', 'output', 'addressed', 'closed', 'created_at', 'completed_at', 'enqueued_at', 'started_at', 'finished_at', 'panel_name', 'panel_member_name', 'panel_run_uuid', 'panel_role', 'parent_job_id', 'panel_member_index', 'member_job_ids', 'duration_ms'])
const reviewForbiddenKeys = new Set(['repo_path', 'repo_name', 'repository_path', 'path', 'binary', 'binary_path', 'cwd', 'working_directory', 'env', 'stdout', 'stderr', 'raw_error', 'error', 'prompt', 'diff', 'patch', 'log', 'command_line', 'session', 'worker', 'machine'])
const reviewTextKeys = new Set(['uuid', 'job_uuid', 'git_ref', 'commit_sha', 'branch', 'subject', 'commit_subject', 'job_type', 'agent', 'model', 'reasoning', 'status', 'state', 'verdict', 'created_at', 'completed_at', 'enqueued_at', 'started_at', 'finished_at', 'panel_name', 'panel_member_name', 'panel_run_uuid', 'panel_role'])
const reviewMaxOutput = 4 * 1024 * 1024

function reviewJobNumber(value: unknown): number | undefined {
  if (typeof value === 'number') return Number.isSafeInteger(value) && value > 0 ? value : undefined
  if (typeof value === 'string' && /^\d+$/.test(value)) {
    const parsed = Number(value)
    return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : undefined
  }
  return undefined
}
function reviewSafeText(value: unknown, maxLength = 1024): value is string {
  return typeof value === 'string' && value.length <= maxLength && !/[\u0000-\u0008\u000B\u000C\u000E-\u001F\u007F]/.test(value)
}
function normalizeReviewPayload(value: unknown): ReviewJob['review'] | undefined | null {
  if (value === undefined) return undefined
  if (typeof value === 'string') return reviewSafeText(value, reviewMaxOutput) ? value : null
  const object = objectWithKeys(value, [], ['output', 'verdict'])
  if (!object) return null
  if (object.output !== undefined && !reviewSafeText(object.output, reviewMaxOutput)) return null
  if (object.verdict !== undefined && !reviewSafeText(object.verdict, 128)) return null
  return { ...(object.output === undefined ? {} : { output: object.output }), ...(object.verdict === undefined ? {} : { verdict: object.verdict }) }
}

/** Normalize a review/job projection before it enters browser state. Paths,
 * process details, prompts, logs, and raw errors are deliberately dropped. */
export function normalizeReviewJob(value: unknown): ReviewJob | null {
  const object = objectValue(value)
  if (!object) return null
  const id = reviewJobNumber(object.id ?? object.job_id)
  if (id === undefined) return null
  const explicitID = object.id === undefined ? id : reviewJobNumber(object.id)
  const explicitJobID = object.job_id === undefined ? id : reviewJobNumber(object.job_id)
  if (explicitID === undefined || explicitJobID === undefined || explicitID !== explicitJobID) return null
  if ([...Object.keys(object)].some(key => (reviewForbiddenKeys.has(key.toLowerCase()) && key !== 'repo_path' && key !== 'repo_name') || (!reviewJobKeys.has(key) && key !== 'repo_path' && key !== 'repo_name'))) return null
  const normalized: ReviewJob = { id }
  if (object.job_id !== undefined) normalized.job_id = id
  for (const key of reviewTextKeys) {
    if (object[key] !== undefined) {
      if (!reviewSafeText(object[key])) return null
      normalized[key as keyof ReviewJob] = object[key] as never
    }
  }
  const review = normalizeReviewPayload(object.review)
  if (review === null) return null
  if (review !== undefined) normalized.review = review
  if (object.output !== undefined) {
    if (!reviewSafeText(object.output, reviewMaxOutput)) return null
    normalized.output = object.output
  }
  for (const key of ['addressed', 'closed'] as const) {
    if (object[key] !== undefined) {
      if (typeof object[key] !== 'boolean') return null
      normalized[key] = object[key]
    }
  }
  for (const key of ['verdict_bool', 'skipped'] as const) {
    if (object[key] !== undefined) {
      if (typeof object[key] !== 'boolean') return null
      normalized[key] = object[key]
    }
  }
  if (object.review_id !== undefined) {
    const reviewID = reviewJobNumber(object.review_id)
    if (reviewID === undefined) return null
    normalized.review_id = reviewID
  }
  for (const key of ['parent_job_id', 'panel_member_index'] as const) {
    if (object[key] !== undefined) {
      const number = key === 'panel_member_index'
        ? (typeof object[key] === 'number' && Number.isSafeInteger(object[key]) && object[key] >= 0 ? object[key] : typeof object[key] === 'string' && /^\d+$/.test(object[key]) && Number.isSafeInteger(Number(object[key])) ? Number(object[key]) : undefined)
        : reviewJobNumber(object[key])
      if (number === undefined) return null
      normalized[key] = number
    }
  }
  if (object.member_job_ids !== undefined) {
    if (!Array.isArray(object.member_job_ids) || object.member_job_ids.length > 256) return null
    const ids = object.member_job_ids.map(reviewJobNumber)
    if (ids.some(number => number === undefined)) return null
    normalized.member_job_ids = ids as number[]
  }
  if (object.duration_ms !== undefined) {
    if (typeof object.duration_ms !== 'number' || !Number.isSafeInteger(object.duration_ms) || object.duration_ms < 0) return null
    normalized.duration_ms = object.duration_ms
  }
  return normalized
}

// `/api/review?job_id=` is not a job-shaped response. RoboRev's detail
// envelope uses the storage review-row `id` at the top level and carries the
// requested job identity in `job_id` plus the nested `job.id`. Keep that row
// identity out of the browser job model and require all job identities to
// agree before projecting any display data.
const reviewDetailKeys = new Set([...reviewJobKeys, 'job', 'text', 'markdown', 'result'])
const reviewDetailOverlayKeys = [...reviewJobKeys].filter(key => key !== 'id' && key !== 'job_id')

function reviewDetailObject(value: unknown): Record<string, unknown> | null {
  const object = objectValue(value)
  if (!object) return null
  // Do not run the recursive forbidden-key scanner here: the nested `job`
  // object may still contain the server-side repo_path used for authorization.
  // normalizeReviewJob below strips that field while rejecting all other
  // unknown/private nested keys.
  if (Object.keys(object).some(key => !reviewDetailKeys.has(key) || projectionForbiddenKeys.has(key.replace(/[_-]/g, '').toLowerCase()))) return null
  return object
}

function reviewDetailOutput(value: unknown): string | undefined | null {
  if (value === undefined) return undefined
  return reviewSafeText(value, reviewMaxOutput) ? value : null
}

/** Normalize the review-row/job detail envelope without exposing row IDs or
 * repository/process authority. Plain job-shaped fixtures remain supported
 * for the list-compatible callers and tests. */
export function normalizeReviewDetail(value: unknown, expectedJobID?: number): ReviewJob | null {
  const object = reviewDetailObject(value)
  if (!object) return null
  const nestedValue = object.job
  if (nestedValue !== undefined) {
    const nested = normalizeReviewJob(nestedValue)
    if (!nested) return null
    const nestedID = reviewJobNumber(nested.id)
    const wireJobID = object.job_id === undefined ? nestedID : reviewJobNumber(object.job_id)
    if (nestedID === undefined || wireJobID === undefined || wireJobID !== nestedID) return null
    const targetID = expectedJobID ?? wireJobID
    if (!Number.isSafeInteger(targetID) || targetID <= 0 || targetID !== wireJobID) return null
    if (object.id !== undefined && reviewJobNumber(object.id) === undefined) return null

    const overlay: Record<string, unknown> = { id: targetID, job_id: targetID }
    for (const key of reviewDetailOverlayKeys) if (object[key] !== undefined) overlay[key] = object[key]
    const text = reviewDetailOutput(object.text)
    const markdown = reviewDetailOutput(object.markdown)
    const result = reviewDetailOutput(object.result)
    if (text === null || markdown === null || result === null) return null
    if (overlay.output === undefined && overlay.review === undefined) {
      const displayOutput = text ?? markdown ?? result
      if (displayOutput !== undefined) overlay.output = displayOutput
    }
    const normalizedOverlay = normalizeReviewJob(overlay)
    if (!normalizedOverlay) return null
    return { ...nested, ...normalizedOverlay, id: targetID, job_id: targetID }
  }

  const direct = normalizeReviewJob(object)
  if (!direct || (expectedJobID !== undefined && direct.id !== expectedJobID)) return null
  return direct
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

/** Request agent configuration status only after an explicit user action. */
export async function fetchRepositoryAgentConfigurationStatus(repositoryIdentity: string, signal?: AbortSignal): Promise<RepositoryAgentConfigurationStatusResponse> {
  const value = await request<unknown>('/api/repository-agent-configuration/status', {
    method: 'POST',
    body: JSON.stringify({ repositoryIdentity }),
    signal,
  })
  const normalized = normalizeRepositoryAgentConfigurationStatus(value)
  if (!normalized) throw new APIError('invalid-repository-agent-configuration-status')
  return normalized
}

/** Request one explicit agent configuration preview. */
export async function fetchRepositoryAgentConfigurationPreview(repositoryIdentity: RepositoryIdentityAlias, scope: RepositoryAgentConfigurationScope, agent: RepositoryAgent, signal?: AbortSignal): Promise<RepositoryAgentConfigurationPreview> {
  const value = await request<unknown>('/api/repository-agent-configuration/preview', {
    method: 'POST',
    body: JSON.stringify({ repositoryIdentity, scope, agent }),
    signal,
  })
  const normalized = normalizeRepositoryAgentConfigurationPreview(value, repositoryIdentity, scope, agent)
  if (!normalized) throw new APIError('invalid-repository-agent-configuration-preview')
  return normalized
}

/** Execute one server-bound, single-use agent configuration token. */
export async function executeRepositoryAgentConfiguration(token: string): Promise<RepositoryAgentConfigurationExecuteResult> {
  const value = await request<unknown>('/api/repository-agent-configuration/execute', {
    method: 'POST',
    body: JSON.stringify({ token }),
  })
  const normalized = normalizeRepositoryAgentConfigurationExecuteResult(value)
  if (!normalized) throw new APIError('invalid-repository-agent-configuration-execute-result')
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

/** Request one explicitly-triggered repository-scoped tracking preview. */
export async function fetchRepositoryTrackingPreview(repositoryIdentity: RepositoryIdentityAlias, signal?: AbortSignal): Promise<RepositoryTrackingPreview> {
  const value = await request<unknown>('/api/repository-tracking/preview', {
    method: 'POST',
    body: JSON.stringify({ repositoryIdentity }),
    signal,
  })
  const normalized = normalizeRepositoryTrackingPreview(value, repositoryIdentity)
  if (!normalized) throw new APIError('invalid-repository-tracking-preview')
  return normalized
}

/** Execute one server-bound, single-use repository tracking token. */
export async function executeRepositoryTracking(token: string): Promise<RepositoryTrackingExecuteResult> {
  const value = await request<unknown>('/api/repository-tracking/execute', {
    method: 'POST',
    body: JSON.stringify({ token }),
  })
  const normalized = normalizeRepositoryTrackingExecuteResult(value)
  if (!normalized) throw new APIError('invalid-repository-tracking-execute-result')
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
  if (Array.isArray(value)) return value.map(normalizeReviewJob).filter((job): job is ReviewJob => Boolean(job))
  if (value && typeof value === 'object') {
    const object = value as Record<string, unknown>
    for (const key of ['jobs', 'reviews', 'items']) if (Array.isArray(object[key])) return object[key].map(normalizeReviewJob).filter((job): job is ReviewJob => Boolean(job))
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
