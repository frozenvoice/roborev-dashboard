import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Check, Download, ExternalLink, Info, LockKeyhole, Pause, Play, RefreshCw, RotateCcw, Server, ShieldAlert, Square, X } from 'lucide-react'
import { BACKEND_COMMAND_SENTINEL, type AgentsResponse, type AgentHealth, type ConfiguredReviewAgent, type DaemonCommandPreview, type RuntimeControlState, type SetupState } from './api'
import { LanguageSelector, useI18n, type Locale, type MessageKey } from './i18n'

export type SetupAction = () => Promise<void>
export type AgentCheckAction = (agent: string) => Promise<void>
export type DaemonAction = (action: 'start' | 'stop' | 'restart') => Promise<void>

const SOURCE_URL = 'https://github.com/kenn-io/roborev/releases/tag/v0.64.0'
const SOURCE_REPOSITORY = 'https://github.com/kenn-io/roborev'
const nonRetryableFailureCodes = new Set(['checksum_mismatch', 'archive_invalid', 'archive_corrupt', 'integrity_failure', 'unsafe_redirect', 'unsafe_archive', 'explicit_override', 'existing_binary'])

function isOfficialSource(value?: string) {
  if (!value) return false
  try {
    const url = new URL(value)
    return url.protocol === 'https:' && url.hostname.toLowerCase() === 'github.com' && url.pathname.toLowerCase().startsWith('/kenn-io/roborev/')
  } catch { return false }
}

function sourceLabel(state: SetupState, t: ReturnType<typeof useI18n>['t']) {
  return isOfficialSource(state.sourceURL) ? t('setup.officialRelease') : state.sourceLabel || t('setup.officialRelease')
}

export function setupPhaseLabel(phase: SetupState['phase'], translate?: (key: MessageKey) => string) {
  const labels: Record<SetupState['phase'], MessageKey> = {
    idle: 'setup.phase.idle', checking: 'setup.phase.checking', downloading: 'setup.phase.downloading', extracting: 'setup.phase.extracting', verifying: 'setup.phase.verifying', installing: 'setup.phase.installing', binding: 'setup.phase.binding', starting_daemon: 'setup.phase.starting_daemon', waiting_for_daemon: 'setup.phase.waiting_for_daemon', ready: 'setup.phase.ready', failed: 'setup.phase.failed', blocked: 'setup.phase.blocked', canceled: 'setup.phase.canceled', unsupported: 'setup.phase.unsupported',
  }
  const key = labels[phase]
  return key ? (translate ? translate(key) : key.replace('setup.phase.', '').split('_').map(part => part[0].toUpperCase() + part.slice(1)).join(' ')) : String(phase)
}
export function setupIsActive(state: SetupState | null | undefined) {
  return Boolean(state?.operationActive)
}

function formatNumber(value: number, locale: Locale, options?: Intl.NumberFormatOptions) {
  return new Intl.NumberFormat(locale === 'ko' ? 'ko-KR' : 'en-US', options).format(value)
}

function bytes(value: number | undefined, locale: Locale) {
  if (value === undefined) return '—'
  if (value < 1024) return `${formatNumber(value, locale)} B`
  if (value < 1024 * 1024) return `${formatNumber(value / 1024, locale, { minimumFractionDigits: 1, maximumFractionDigits: 1 })} KiB`
  return `${formatNumber(value / (1024 * 1024), locale, { minimumFractionDigits: 1, maximumFractionDigits: 1 })} MiB`
}

function Progress({ state }: { state: SetupState }) {
  const { t, locale } = useI18n()
  const determinate = state.totalBytes !== undefined && state.totalBytes > 0 && state.downloadedBytes !== undefined
  const percent = determinate ? Math.min(100, Math.round((state.downloadedBytes! / state.totalBytes!) * 100)) : undefined
  return <div className="setup-progress" aria-label={t('setup.progress', { phase: setupPhaseLabel(state.phase, t) })}>
    <div className="setup-progress-track"><div className={`setup-progress-value ${percent === undefined ? 'indeterminate' : ''}`} style={percent === undefined ? undefined : { width: `${percent}%` }} /></div>
    <span>{percent === undefined ? t('setup.downloaded', { bytes: bytes(state.downloadedBytes, locale) }) : t('setup.bytesProgress', { percent: formatNumber(percent, locale), downloaded: bytes(state.downloadedBytes, locale), total: bytes(state.totalBytes, locale) })}</span>
  </div>
}

type BannerProps = {
  state: SetupState | null | undefined
  busy?: boolean
  onInstall?: SetupAction
  onRetry?: SetupAction
  onStartDaemon?: SetupAction
}

export function SetupBanner({ state, busy = false, onInstall, onRetry, onStartDaemon }: BannerProps) {
  const { t } = useI18n()
  if (!state) return null
  const active = setupIsActive(state)
  const blocked = state.blocked || state.phase === 'blocked'
  const unsupported = state.phase === 'unsupported' || (state.installed && state.compatible === false)
  const checksumFailure = state.failureCode === 'checksum_mismatch' || state.failureCode === 'archive_invalid' || state.failureCode === 'archive_corrupt'
  const retryAllowed = state.canRetry && !nonRetryableFailureCodes.has(state.failureCode || '')
  let title = ''
  let message = ''
  let icon = <Server />
  let action: ReactNode = null
  if (active) {
    title = t('setup.setupTitle', { phase: setupPhaseLabel(state.phase, t) })
    message = t('setup.setupWaiting')
    icon = <Download />
  } else if (blocked) {
    title = t('setup.blockedTitle')
    message = t('setup.blockedBody')
    icon = <LockKeyhole />
    if (state.canRetry && onRetry) action = <button className="button" disabled={busy} onClick={() => void onRetry()}>{t('setup.retryVerification')}</button>
  } else if (unsupported) {
    title = t('setup.unsupportedTitle', { version: state.version || t('setup.version') })
    message = t('setup.unsupportedBody', { version: state.recommendedVersion })
    icon = <Info />
  } else if (state.phase === 'failed') {
    title = checksumFailure ? t('setup.installRejected') : t('setup.setupFailed')
    message = state.failureCode ? t('setup.failureCode', { code: state.failureCode }) : t('setup.operationIncomplete')
    icon = <ShieldAlert />
    if (retryAllowed && onRetry) action = <button className="button" disabled={busy} onClick={() => void onRetry()}>{t('setup.retryInstallation')}</button>
    // A failed install has already published the verified binary. Retry via
    // the setup manager so it can start/health-check that binding without a
    // second download; the direct daemon endpoint is for an ordinary stopped
    // daemon only.
    if (state.canStartDaemon && onRetry) action = <button className="button" disabled={busy} onClick={() => void onRetry()}>{t('setup.retryStart')}</button>
  } else if (state.phase === 'canceled') {
    title = t('setup.setupCanceled')
    message = t('setup.canceledBody')
    icon = <Info />
    if (state.canRetry && onRetry) action = <button className="button" disabled={busy} onClick={() => void onRetry()}>{t('setup.retrySetup')}</button>
  } else if (!state.installed) {
    title = t('setup.notInstalled')
    message = t('setup.installRelease', { version: state.recommendedVersion })
    icon = <Download />
    if ((state.installAvailable || state.canInstall) && onInstall) action = <button className="button primary" disabled={busy} onClick={() => void onInstall()}>{t('setup.install')}</button>
  } else if (state.installed && !state.daemonRunning && state.canStartDaemon && onStartDaemon) {
    title = t('setup.daemonStopped')
    message = t('setup.daemonInstalledStopped')
    action = <button className="button" disabled={busy} onClick={() => void onStartDaemon()}>{t('setup.daemonStart')}</button>
  } else return null
  return <section className={`setup-banner ${blocked ? 'setup-banner-blocked' : ''}`} role={blocked || state.phase === 'failed' ? 'alert' : 'status'}>
    {icon}<div><strong>{title}</strong><p>{message}</p>{active && <Progress state={state} />}</div>{action}
  </section>
}

type SetupProps = {
  state: SetupState | null | undefined
  busy?: boolean
  onInstall?: SetupAction
  onRetry?: SetupAction
  onStartDaemon?: SetupAction
  agents?: AgentsResponse | null
  agentsLoading?: boolean
  agentsError?: string
  onRefreshAgents?: SetupAction
  onCheckAgent?: AgentCheckAction
  runtimeControl?: RuntimeControlState | null
  runtimeLoading?: boolean
  runtimeError?: string
  onRefreshRuntime?: SetupAction
  onDaemonAction?: DaemonAction
  onQueueAction?: (action: 'pause' | 'resume') => Promise<void>
  panelRefineActive?: boolean
}

function healthLabel(value: AgentHealth, t?: ReturnType<typeof useI18n>['t']) {
  if (value === 'not_checked') return t ? t('setup.healthNotChecked') : 'Not checked'
  if (value === 'passed') return t ? t('setup.healthPassed') : 'Passed'
  if (value === 'failed') return t ? t('setup.healthFailed') : 'Failed'
  if (value === 'canceled') return t ? t('setup.healthCanceled') : 'Canceled'
  if (value === 'skipped') return t ? t('setup.healthSkipped') : 'Skipped'
  return value
}

function commandLabel(command: ConfiguredReviewAgent['command'], t: ReturnType<typeof useI18n>['t']) {
  if (command === BACKEND_COMMAND_SENTINEL) return t('setup.backendCommandSupplied')
  if (typeof command === 'string') return command
  return JSON.stringify([command.executable, ...command.args])
}

function daemonCommandMatches(command: DaemonCommandPreview | undefined, action: 'start' | 'stop' | 'restart') {
  return Boolean(command && command.executable.trim() && command.args.length === 2 && command.args[0] === 'daemon' && command.args[1] === action)
}

function agentStatusClass(value: AgentHealth) {
  if (value === 'passed') return 'status-pass'
  if (value === 'failed') return 'status-fail'
  if (value === 'canceled') return 'status-canceled'
  if (value === 'skipped') return 'status-queued'
  return ''
}

type RuntimeConfirmationProps = {
  action: 'stop' | 'restart'
  command: DaemonCommandPreview
  busy?: boolean
  onCancel: () => void
  onConfirm: () => void
}

function RuntimeConfirmation({ action, command, busy = false, onCancel, onConfirm }: RuntimeConfirmationProps) {
  const { t } = useI18n()
  const restart = action === 'restart'
  return <div className="prompt-overlay" role="presentation"><section className="prompt-dialog setup-confirm-dialog" role="dialog" aria-modal="true" aria-labelledby="runtime-confirm-title"><header><div><span className="eyebrow">{t('setup.explicitConfirmation')}</span><h2 id="runtime-confirm-title">{restart ? t('setup.restartDaemon') : t('setup.stopDaemon')}</h2><p>{restart ? t('setup.restartDescription') : t('setup.stopDescription')}</p></div><div className="modal-header-actions"><LanguageSelector compact /><button className="icon-button" aria-label={t('setup.closeDaemon')} onClick={onCancel}><X /></button></div></header><div className="runtime-command-preview"><strong>{t('setup.executionPreview')}</strong><code>{JSON.stringify([command.executable, ...command.args])}</code><small>{t('setup.backendCommand')}</small></div><div className="setup-confirm-scope"><div><strong>{t('setup.willDo')}</strong><span>{restart ? t('setup.requestRestart') : t('setup.requestStop')}</span></div><div><strong>{t('setup.willNotDo')}</strong><span>{t('setup.noDashboardStop')}</span></div></div><footer><button className="button" disabled={busy} onClick={onCancel}>{t('common.cancel')}</button><button className="button danger" disabled={busy} onClick={onConfirm}>{restart ? <RotateCcw size={16} /> : <Square size={16} />}{restart ? t('setup.restartDaemonAction') : t('setup.stopDaemonAction')}</button></footer></section></div>
}

type AgentCheckConfirmationProps = {
  agent: ConfiguredReviewAgent
  busy?: boolean
  onCancel: () => void
  onConfirm: () => void
}

export function AgentCheckConfirmation({ agent, busy = false, onCancel, onConfirm }: AgentCheckConfirmationProps) {
  const { t } = useI18n()
  return <div className="prompt-overlay" role="presentation"><section className="prompt-dialog setup-confirm-dialog" role="dialog" aria-modal="true" aria-labelledby="agent-check-confirm-title"><header><div><span className="eyebrow">{t('setup.explicitConfirmation')}</span><h2 id="agent-check-confirm-title">{t('setup.confirmAgentCheck')}</h2><p>{t('setup.agentCheckDescription')}</p></div><div className="modal-header-actions"><LanguageSelector compact /><button className="icon-button" aria-label={t('setup.closeAgentCheck')} onClick={onCancel}><X /></button></div></header><div className="setup-confirm-facts"><p><strong>{t('setup.configuredAgent')}</strong><code>{agent.name}</code></p><p><strong>{t('setup.canonicalAgent')}</strong><code>{agent.agent}</code></p><p><strong>{t('setup.model')}</strong><span>{agent.model || t('setup.defaultModel')}</span></p></div><div className="setup-confirm-scope"><div><strong>{t('setup.willDo')}</strong><span>{t('setup.sendCheck')}</span></div><div><strong>{t('setup.willNotDo')}</strong><span>{t('setup.noConfigChange')}</span></div></div><footer><button className="button" disabled={busy} onClick={onCancel}>{t('common.cancel')}</button><button className="button primary" disabled={busy} onClick={onConfirm}><Play size={16} />{t('setup.runCheck')}</button></footer></section></div>
}

function daemonOperationLabel(value: string, daemonRunning: boolean, t: ReturnType<typeof useI18n>['t']) {
  if (value === 'idle') return daemonRunning ? t('setup.daemonReady') : t('setup.daemonNotAccepting')
  if (value === 'start') return t('setup.daemonStart')
  if (value === 'stop') return t('setup.stopDaemonAction')
  if (value === 'restart') return t('setup.restartDaemonAction')
  if (value === 'pause') return t('setup.pauseQueue')
  if (value === 'resume') return t('setup.resumeQueue')
  return value
}

function lastCheckedLabel(value: string, locale: Locale) {
  const timestamp = Date.parse(value)
  if (!Number.isFinite(timestamp)) return value
  return new Intl.DateTimeFormat(locale === 'ko' ? 'ko-KR' : 'en-US', { dateStyle: 'medium', timeStyle: 'short' }).format(timestamp)
}

function RuntimeControls({ state, runtimeControl, runtimeLoading = false, runtimeError = '', busy = false, panelRefineActive = false, onRefreshRuntime, onDaemonAction, onStartDaemon, onQueueAction }: Pick<SetupProps, 'state' | 'runtimeControl' | 'runtimeLoading' | 'runtimeError' | 'busy' | 'panelRefineActive' | 'onRefreshRuntime' | 'onDaemonAction' | 'onStartDaemon' | 'onQueueAction'>) {
  const { t } = useI18n()
  // Direct Setup consumers from the earlier setup flow have no runtime-control
  // response yet; preserve their start affordance while the App fails closed.
  const runtime = runtimeControl === undefined && state ? {
    daemonRunning: state.daemonRunning,
    operationActive: state.operationActive,
    queueState: 'unknown' as const,
    canPause: false,
    canResume: false,
    canStart: state.canStartDaemon,
    canStop: false,
    canRestart: false,
    disabledReason: undefined,
  } : runtimeControl
  const unavailable = runtimeControl === null || (!runtime && !runtimeLoading)
  const gateReason = panelRefineActive ? t('setup.panelActive') : runtime?.operationActive ? runtime.disabledReason || t('setup.operationActive') : ''
  const actionDisabled = busy || runtimeLoading || Boolean(gateReason)
  const queueDisabled = actionDisabled || !runtime?.daemonRunning || runtime.queueState === 'unknown' || (!runtime.canPause && !runtime.canResume)
  const daemonCommandReady = (action: 'start' | 'stop' | 'restart') => daemonCommandMatches(runtime?.daemonCommands?.[action], action)
  const [confirmation, setConfirmation] = useState<'stop' | 'restart' | null>(null)
  const [actionBusy, setActionBusy] = useState(false)
  const actionLock = useRef(false)
  const runDaemon = async (action: 'start' | 'stop' | 'restart') => {
    const actionHandler = onDaemonAction || (action === 'start' && onStartDaemon ? async () => onStartDaemon() : undefined)
    if (!actionHandler || actionBusy || actionLock.current || actionDisabled || !daemonCommandReady(action)) return
    if (action !== 'stop' && action !== 'restart') {
      actionLock.current = true
      setActionBusy(true)
      try { await actionHandler(action) } catch { /* the action callback owns user-facing error reporting */ } finally { actionLock.current = false; setActionBusy(false) }
      return
    }
    setConfirmation(action)
  }
  const confirmDaemon = async () => {
    if (!confirmation || !onDaemonAction || actionLock.current) return
    const action = confirmation
    actionLock.current = true
    setActionBusy(true)
    try { await onDaemonAction(action); setConfirmation(null) } catch { /* the action callback owns user-facing error reporting */ } finally { actionLock.current = false; setActionBusy(false) }
  }
  const runQueue = async (action: 'pause' | 'resume') => {
    if (!onQueueAction || actionBusy || actionLock.current || queueDisabled) return
    actionLock.current = true
    setActionBusy(true)
    try { await onQueueAction(action) } catch { /* the action callback owns user-facing error reporting */ } finally { actionLock.current = false; setActionBusy(false) }
  }
  return <>
    <section className="setup-runtime" aria-label={t('setup.runtime')}>
      <header className="setup-subheading"><div><span className="eyebrow">{t('setup.runtime')}</span><h2>{t('setup.daemonControls')}</h2><p>{t('setup.runtimeDescription')}</p></div>{onRefreshRuntime && <button className="button ghost compact" disabled={runtimeLoading} onClick={() => void onRefreshRuntime()}><RefreshCw size={14} className={runtimeLoading ? 'spin' : ''} />{t('common.refresh')}</button>}</header>
      {runtimeError && <p className="setup-action-error" role="alert">{runtimeError}</p>}
      {unavailable ? <div className="runtime-unavailable"><Server size={18} /><div><strong>{t('setup.runtimeUnavailable')}</strong><p>{t('setup.runtimeRefresh')}</p></div></div> : <div className="runtime-control-grid">
        <article className="runtime-control-card"><div><span className="eyebrow">{t('setup.daemon')}</span><h3>{runtime?.daemonRunning ? t('setup.daemonRunning') : t('setup.daemonOffline')}</h3><p>{runtime?.daemonOperation ? daemonOperationLabel(runtime.daemonOperation, runtime.daemonRunning, t) : runtime?.daemonRunning ? t('setup.daemonReady') : t('setup.daemonNotAccepting')}</p></div><span className={`status ${runtime?.daemonRunning ? 'status-pass' : 'status-failed'}`}><span className="status-dot" />{runtime?.daemonRunning ? t('status.running') : t('status.offline')}</span><div className="runtime-buttons"><button className="button" disabled={actionDisabled || !runtime?.canStart || !daemonCommandReady('start')} onClick={() => void runDaemon('start')}><Play size={15} />{t('setup.daemonStart')}</button><button className="button" disabled={actionDisabled || !runtime?.canStop || !daemonCommandReady('stop')} onClick={() => void runDaemon('stop')}><Square size={15} />{t('setup.stopDaemonAction')}</button><button className="button" disabled={actionDisabled || !runtime?.canRestart || !daemonCommandReady('restart')} onClick={() => void runDaemon('restart')}><RotateCcw size={15} />{t('setup.restartDaemonAction')}</button></div></article>
        <article className="runtime-control-card"><div><span className="eyebrow">{t('setup.reviewQueue')}</span><h3>{runtime?.queueState === 'paused' ? t('setup.queuePaused') : runtime?.queueState === 'processing' ? t('setup.queueProcessing') : t('setup.queueUnknown')}</h3><p>{runtime?.queueState === 'paused' ? t('setup.queuePausedBody') : runtime?.queueState === 'processing' ? t('setup.queueProcessingBody') : t('setup.queueUnknownBody')}</p></div><span className={`status ${runtime?.queueState === 'paused' ? 'status-queued' : runtime?.queueState === 'processing' ? 'status-pass' : 'status-failed'}`}><span className="status-dot" />{runtime?.queueState === 'paused' ? t('status.paused') : runtime?.queueState === 'processing' ? t('status.processing') : t('status.unknown')}</span><div className="runtime-buttons"><button className="button" disabled={queueDisabled || !runtime?.canPause} onClick={() => void runQueue('pause')}><Pause size={15} />{t('setup.pauseQueue')}</button><button className="button" disabled={queueDisabled || !runtime?.canResume} onClick={() => void runQueue('resume')}><Play size={15} />{t('setup.resumeQueue')}</button></div>{queueDisabled && <small className="runtime-disabled-reason">{gateReason || runtime?.disabledReason || (!runtime?.daemonRunning ? t('setup.daemonOfflineReason') : runtime?.queueState === 'unknown' ? t('setup.queueUnknownReason') : '')}</small>}</article>
      </div>}
      {gateReason && <p className="runtime-disabled-reason">{t('setup.controlsDisabled', { reason: gateReason })}</p>}
    </section>
    {confirmation && runtime?.daemonCommands?.[confirmation] && daemonCommandReady(confirmation) && <RuntimeConfirmation action={confirmation} command={runtime.daemonCommands[confirmation]} busy={actionBusy} onCancel={() => { if (!actionBusy) setConfirmation(null) }} onConfirm={() => void confirmDaemon()} />}
  </>
}

function ConfiguredReviewAgents({ agents, loading = false, error = '', busy = false, onRefresh, onCheck }: { agents?: AgentsResponse | null; loading?: boolean; error?: string; busy?: boolean; onRefresh?: SetupAction; onCheck?: AgentCheckAction }) {
  const { t, locale } = useI18n()
  const [selected, setSelected] = useState<ConfiguredReviewAgent | null>(null)
  const [actionBusy, setActionBusy] = useState(false)
  const actionLock = useRef(false)
  const check = async () => {
    if (!selected || !onCheck || actionBusy || actionLock.current) return
    actionLock.current = true
    setActionBusy(true)
    try { await onCheck(selected.agent); setSelected(null) } catch { /* the action callback owns user-facing error reporting */ } finally { actionLock.current = false; setActionBusy(false) }
  }
  return <>
    <section className="configured-agents" aria-label={t('setup.configuredAgents')}>
      <header className="setup-subheading"><div><span className="eyebrow">{t('setup.configuredAgents')}</span><h2>{t('setup.configuredAgents')}</h2><p>{t('setup.agentDescription')}</p></div>{onRefresh && <button className="button ghost compact" disabled={loading || busy || actionBusy} onClick={() => void onRefresh()}><RefreshCw size={14} className={loading || busy || actionBusy ? 'spin' : ''} />{t('common.refresh')}</button>}</header>
      {error && <p className="setup-action-error" role="alert">{error}</p>}
      {loading && !agents ? <div className="agents-loading">{t('setup.agentsLoading')}</div> : !agents ? <div className="runtime-unavailable"><Server size={18} /><div><strong>{t('setup.agentsUnavailable')}</strong><p>{t('setup.agentsRefresh')}</p></div></div> : agents.configured.length === 0 ? <div className="agents-loading">{t('setup.noConfiguredAgents')}</div> : <div className="agent-cards">{agents.configured.map(agent => <article className="agent-card" key={`${agent.name}:${agent.agent}`}><div className="agent-card-main"><div className="agent-heading"><BotGlyph /><div><h3>{agent.name}</h3><code>{agent.agent}</code></div></div><div className="agent-facts"><span><strong>{t('setup.detected')}</strong><em className={agent.detected ? 'detected' : 'not-detected'}>{agent.detected ? t('common.yes') : t('common.no')}</em></span><span><strong>{t('setup.health')}</strong><em className={`status ${agentStatusClass(agent.health)}`}><span className="status-dot" />{healthLabel(agent.health, t)}</em></span>{agent.model && <span><strong>{t('setup.model')}</strong><em>{agent.model}</em></span>}{agent.reasoning && <span><strong>{t('setup.reasoning')}</strong><em>{agent.reasoning}</em></span>}</div><p className="agent-detail">{agent.detected ? agent.detectionReason || agent.resolvedPath || t('setup.executableDetected') : agent.detectionReason || t('setup.executableNotDetected')}</p><div className="agent-command"><strong>{t('setup.command')}</strong><code>{commandLabel(agent.command, t)}</code>{agent.commandSource && <small>{t('setup.sourceLabel', { source: agent.commandSource })}</small>}</div>{agent.lastCheckedAt && <small className="agent-checked">{t('setup.lastChecked', { at: lastCheckedLabel(agent.lastCheckedAt, locale) })}</small>}</div><button className="button compact" disabled={busy || actionBusy || !onCheck || !agent.detected} title={!agent.detected ? t('setup.agentMustDetected') : undefined} onClick={() => setSelected(agent)}>{t('setup.runCheck')}</button></article>)}</div>}
      {agents?.roborevVersion && <small className="agent-version">RoboRev {agents.roborevVersion}</small>}
    </section>
    {selected && <AgentCheckConfirmation agent={selected} busy={actionBusy || busy} onCancel={() => { if (!actionBusy) setSelected(null) }} onConfirm={() => void check()} />}
  </>
}

function BotGlyph() { return <span className="agent-card-icon" aria-hidden="true">●</span> }

type InstallConfirmationProps = {
  state: SetupState
  busy?: boolean
  onCancel: () => void
  onInstall: () => void
}

export function InstallConfirmation({ state, busy = false, onCancel, onInstall }: InstallConfirmationProps) {
  const { t, locale } = useI18n()
  const sourceURL = state.sourceURL || SOURCE_URL
  const exactBytes = state.totalBytes === undefined ? t('setup.archiveSizeUnavailable') : `${formatNumber(state.totalBytes, locale)} bytes`
  return <div className="prompt-overlay" role="presentation"><section className="prompt-dialog setup-confirm-dialog" role="dialog" aria-modal="true" aria-labelledby="setup-confirm-title"><header><div><span className="eyebrow">{t('setup.explicitConfirmation')}</span><h2 id="setup-confirm-title">{t('setup.installConfirm')}</h2><p>{t('setup.installOnlyStep')}</p></div><div className="modal-header-actions"><LanguageSelector compact /><button className="icon-button" aria-label={t('setup.closeInstall')} onClick={onCancel}><X /></button></div></header><div className="setup-confirm-facts"><p><strong>{t('setup.repository')}</strong><a href={SOURCE_REPOSITORY} target="_blank" rel="noreferrer">github.com/kenn-io/roborev</a></p><p><strong>{t('setup.releaseSource')}</strong><a href={sourceURL} target="_blank" rel="noreferrer">{sourceLabel(state, t)}</a></p><p><strong>{t('setup.version')}</strong><code>{state.recommendedVersion}</code></p><p><strong>{t('setup.assetBytes')}</strong><code>{exactBytes}</code></p><p><strong>{t('setup.target')}</strong><code>{state.targetPath || t('setup.verifiedTarget')}</code></p><p><strong>{t('setup.sha256')}</strong><code>{state.sha256 || t('setup.checksumBackend')}</code></p><p><strong>{t('setup.compatibility')}</strong><span>{state.compatible === true ? t('setup.compatibleWindows') : t('setup.verifyCompatibility')}</span></p></div><div className="setup-confirm-scope"><div><strong>{t('setup.willDo')}</strong><span>{t('setup.installWillDo', { version: state.recommendedVersion })}</span></div><div><strong>{t('setup.willNotDo')}</strong><span>{t('setup.installWillNotDo')}</span></div></div><footer><button className="button" disabled={busy} onClick={onCancel}>{t('common.cancel')}</button><button className="button primary" disabled={busy} onClick={onInstall}><Download size={16} />{t('setup.installStart')}</button></footer></section></div>
}

export function Setup({ state, busy = false, onInstall, onRetry, onStartDaemon, agents, agentsLoading, agentsError, onRefreshAgents, onCheckAgent, runtimeControl, runtimeLoading, runtimeError, onRefreshRuntime, onDaemonAction, onQueueAction, panelRefineActive }: SetupProps) {
  const { t } = useI18n()
  const [confirming, setConfirming] = useState(false)
  const [actionBusy, setActionBusy] = useState(false)
  const [actionError, setActionError] = useState('')
  const actionLock = useRef(false)
  useEffect(() => { if (state?.operationActive) setConfirming(false) }, [state?.operationActive])
  const runAction = async (action: SetupAction | undefined) => {
    if (!action || actionBusy || actionLock.current || busy) return
    actionLock.current = true
    setActionBusy(true)
    setActionError('')
    try { await action() } catch (error) { setActionError((error as Error).message || t('setup.actionFailed')) } finally { actionLock.current = false; setActionBusy(false) }
  }
  const install = () => {
    setConfirming(false)
    void runAction(onInstall)
  }
  const sourceURL = state?.sourceURL || SOURCE_URL
  const recommended = state?.recommendedVersion || 'v0.64.0'
  const installable = Boolean(state && !state.installed && !state.operationActive && (state.installAvailable || state.canInstall) && onInstall)
  return <section className="cards-page setup-page">
    <header className="section-heading"><div><span className="eyebrow">{t('setup.runtimeHealth')}</span><h1>{t('setup.heading')}</h1><p>{t('setup.authNotice')}</p></div></header>
    {state && <SetupBanner state={state} busy={busy || actionBusy} onInstall={() => { setConfirming(true); return Promise.resolve() }} onRetry={() => runAction(onRetry)} />}
    {!state && <section className="setup-card"><Server /><div><h3>{t('setup.setupUnavailable')}</h3><p>{t('setup.setupResponseInvalid')}</p></div></section>}
    {state && <article className="setup-card setup-detail-card"><Server /><div><h3>RoboRev {state.version || t('setup.versionNotDetected')}</h3><code>{state.binaryPath || t('setup.pathUnavailable')}</code><p>{state.daemonRunning ? t('setup.daemonRunningDetail') : state.installed ? t('setup.daemonStoppedDetail') : t('setup.daemonNotInstalledDetail')}</p></div><span className={`status ${state.daemonRunning ? 'status-pass' : state.installed ? 'status-queued' : 'status-failed'}`}><span className="status-dot" />{state.daemonRunning ? t('status.ready') : state.installed ? t('status.stopped') : t('status.setupRequired')}</span></article>}
    <RuntimeControls state={state} runtimeControl={runtimeControl} runtimeLoading={runtimeLoading} runtimeError={runtimeError} busy={busy || actionBusy} panelRefineActive={panelRefineActive} onRefreshRuntime={onRefreshRuntime} onDaemonAction={onDaemonAction} onStartDaemon={onStartDaemon} onQueueAction={onQueueAction} />
    <ConfiguredReviewAgents agents={agents} loading={agentsLoading} error={agentsError} busy={busy || actionBusy} onRefresh={onRefreshAgents} onCheck={onCheckAgent} />
    {state && <section className="setup-facts" aria-label={t('setup.verifiedInstallation')}><h2>{t('setup.verifiedInstallation')}</h2><dl>
      <div><dt>{t('setup.requestedVersion')}</dt><dd>{recommended}</dd></div>
      <div><dt>{t('setup.architecture')}</dt><dd>{state.architecture || '—'}</dd></div>
      <div><dt>{t('setup.source')}</dt><dd>{sourceLabel(state, t)} <a href={sourceURL} target="_blank" rel="noreferrer" aria-label={t('setup.openRelease')}><ExternalLink size={13} /></a></dd></div>
      <div><dt>{t('setup.officialRepository')}</dt><dd><a href={SOURCE_REPOSITORY} target="_blank" rel="noreferrer">github.com/kenn-io/roborev</a></dd></div>
      <div><dt>{t('setup.asset')}</dt><dd>{state.assetName || '—'}</dd></div>
      <div><dt>{t('setup.target')}</dt><dd>{state.targetPath || '—'}</dd></div>
      <div><dt>{t('setup.sha256')}</dt><dd className="setup-hash">{state.sha256 || '—'}</dd></div>
      <div><dt>{t('setup.compatibility')}</dt><dd>{!state.installed ? t('setup.pendingInstallation') : state.compatible === true ? t('setup.compatibleDashboard') : state.compatible === false ? t('setup.notCompatible') : t('setup.pendingVerification')}</dd></div>
    </dl></section>}
    {state?.operationActive && <section className="setup-card setup-active-card"><RefreshCw className="spin" /><div><h3>{setupPhaseLabel(state.phase, t)}</h3><p>{t('setup.operationSerialized')}</p><Progress state={state} /></div></section>}
    {state && <section className="setup-safety"><h2>{t('setup.installScope')}</h2><div className="setup-safety-grid"><div><h3><Check size={15} />{t('setup.willDo')}</h3><ul><li>{t('setup.downloadExact', { version: recommended })}</li><li>{t('setup.verifyChecksum')}</li><li>{t('setup.installProbe')}</li><li>{t('setup.startDaemonVerify')}</li></ul></div><div><h3><X size={15} />{t('setup.willNotDo')}</h3><ul><li>{t('setup.noSecurityChanges')}</li><li>{t('setup.noInit')}</li><li>{t('setup.noAgents')}</li><li>{t('setup.noKeys')}</li></ul></div></div></section>}
    {actionError && <p role="alert" className="setup-action-error">{actionError}</p>}
    {installable && confirming && state && <InstallConfirmation state={state} busy={actionBusy} onCancel={() => setConfirming(false)} onInstall={install} />}
  </section>
}
