import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Check, Download, ExternalLink, Info, LockKeyhole, Pause, Play, RefreshCw, RotateCcw, Server, ShieldAlert, Square, X } from 'lucide-react'
import type { AgentsResponse, AgentHealth, ConfiguredReviewAgent, DaemonCommandPreview, RuntimeControlState, SetupState } from './api'

export type SetupAction = () => Promise<void>
export type AgentCheckAction = (agent: string) => Promise<void>
export type DaemonAction = (action: 'start' | 'stop' | 'restart') => Promise<void>

const SOURCE_URL = 'https://github.com/kenn-io/roborev/releases/tag/v0.64.0'
const SOURCE_REPOSITORY = 'https://github.com/kenn-io/roborev'
const nonRetryableFailureCodes = new Set(['checksum_mismatch', 'archive_invalid', 'archive_corrupt', 'integrity_failure', 'unsafe_redirect', 'unsafe_archive', 'explicit_override', 'existing_binary'])

export function setupPhaseLabel(phase: SetupState['phase']) {
  return phase.split('_').map(part => part[0].toUpperCase() + part.slice(1)).join(' ')
}

export function setupIsActive(state: SetupState | null | undefined) {
  return Boolean(state?.operationActive)
}

function bytes(value?: number) {
  if (value === undefined) return '—'
  if (value < 1024) return `${value.toLocaleString()} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KiB`
  return `${(value / (1024 * 1024)).toFixed(1)} MiB`
}

function Progress({ state }: { state: SetupState }) {
  const determinate = state.totalBytes !== undefined && state.totalBytes > 0 && state.downloadedBytes !== undefined
  const percent = determinate ? Math.min(100, Math.round((state.downloadedBytes! / state.totalBytes!) * 100)) : undefined
  return <div className="setup-progress" aria-label={`${setupPhaseLabel(state.phase)} progress`}>
    <div className="setup-progress-track"><div className={`setup-progress-value ${percent === undefined ? 'indeterminate' : ''}`} style={percent === undefined ? undefined : { width: `${percent}%` }} /></div>
    <span>{percent === undefined ? `${bytes(state.downloadedBytes)} downloaded` : `${percent}% · ${bytes(state.downloadedBytes)} / ${bytes(state.totalBytes)}`}</span>
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
    title = `RoboRev setup · ${setupPhaseLabel(state.phase)}`
    message = 'The Dashboard is waiting for the verified RoboRev installation to finish.'
    icon = <Download />
  } else if (blocked) {
    title = 'RoboRev was blocked by Windows Application Control'
    message = 'The verified file was not started. Check Windows Security or your organization’s application-control policy; the Dashboard cannot disable or bypass it.'
    icon = <LockKeyhole />
    if (state.canRetry && onRetry) action = <button className="button" disabled={busy} onClick={() => void onRetry()}>Retry verification</button>
  } else if (unsupported) {
    title = `RoboRev ${state.version || 'version'} is not supported`
    message = `The Dashboard will not downgrade it automatically. Recommended version: ${state.recommendedVersion}.`
    icon = <Info />
  } else if (state.phase === 'failed') {
    title = checksumFailure ? 'RoboRev installation was rejected' : 'RoboRev setup failed'
    message = state.failureCode ? `Failure code: ${state.failureCode}.` : 'The typed setup operation did not complete.'
    icon = <ShieldAlert />
    if (retryAllowed && onRetry) action = <button className="button" disabled={busy} onClick={() => void onRetry()}>Retry installation</button>
    // A failed install has already published the verified binary. Retry via
    // the setup manager so it can start/health-check that binding without a
    // second download; the direct daemon endpoint is for an ordinary stopped
    // daemon only.
    if (state.canStartDaemon && onRetry) action = <button className="button" disabled={busy} onClick={() => void onRetry()}>Retry Start</button>
  } else if (state.phase === 'canceled') {
    title = 'RoboRev setup was canceled'
    message = 'No partial installation is treated as ready. Start a new verified setup operation when appropriate.'
    icon = <Info />
    if (state.canRetry && onRetry) action = <button className="button" disabled={busy} onClick={() => void onRetry()}>Retry setup</button>
  } else if (!state.installed) {
    title = 'RoboRev is not installed'
    message = `Install the verified ${state.recommendedVersion} release to enable reviews and Panel Refine.`
    icon = <Download />
    if ((state.installAvailable || state.canInstall) && onInstall) action = <button className="button primary" disabled={busy} onClick={() => void onInstall()}>Install RoboRev</button>
  } else if (state.installed && !state.daemonRunning && state.canStartDaemon && onStartDaemon) {
    title = 'RoboRev daemon is stopped'
    message = 'The verified RoboRev binary is installed. Start its daemon to enqueue reviews.'
    action = <button className="button" disabled={busy} onClick={() => void onStartDaemon()}>Start daemon</button>
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

function healthLabel(value: AgentHealth) {
  return value === 'not_checked' ? 'Not checked' : value[0].toUpperCase() + value.slice(1)
}

function commandLabel(command: ConfiguredReviewAgent['command']) {
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
  const restart = action === 'restart'
  return <div className="prompt-overlay" role="presentation"><section className="prompt-dialog setup-confirm-dialog" role="dialog" aria-modal="true" aria-labelledby="runtime-confirm-title"><header><div><span className="eyebrow">Explicit confirmation</span><h2 id="runtime-confirm-title">{restart ? 'Restart RoboRev daemon?' : 'Stop RoboRev daemon?'}</h2><p>{restart ? 'This stops and starts the daemon. Active jobs may be interrupted; queued jobs remain queued.' : 'This stops the daemon. Active jobs may be interrupted; queued jobs remain queued.'}</p></div><button className="icon-button" aria-label="Close daemon confirmation" onClick={onCancel}><X /></button></header><div className="runtime-command-preview"><strong>Execution preview</strong><code>{JSON.stringify([command.executable, ...command.args])}</code><small>Backend-bound executable and exact typed arguments.</small></div><div className="setup-confirm-scope"><div><strong>Will do</strong><span>{restart ? 'Request one typed daemon restart through RoboRev.' : 'Request one typed daemon stop through RoboRev.'}</span></div><div><strong>Will not do</strong><span>Delete reviews, change repository files, or stop jobs directly from the Dashboard.</span></div></div><footer><button className="button" disabled={busy} onClick={onCancel}>Cancel</button><button className="button danger" disabled={busy} onClick={onConfirm}>{restart ? <RotateCcw size={16} /> : <Square size={16} />}{restart ? 'Restart daemon' : 'Stop daemon'}</button></footer></section></div>
}

type AgentCheckConfirmationProps = {
  agent: ConfiguredReviewAgent
  busy?: boolean
  onCancel: () => void
  onConfirm: () => void
}

export function AgentCheckConfirmation({ agent, busy = false, onCancel, onConfirm }: AgentCheckConfirmationProps) {
  return <div className="prompt-overlay" role="presentation"><section className="prompt-dialog setup-confirm-dialog" role="dialog" aria-modal="true" aria-labelledby="agent-check-confirm-title"><header><div><span className="eyebrow">Explicit confirmation</span><h2 id="agent-check-confirm-title">Run agent check?</h2><p>This sends a small real request through RoboRev and may use subscription/API quota.</p></div><button className="icon-button" aria-label="Close agent check confirmation" onClick={onCancel}><X /></button></header><div className="setup-confirm-facts"><p><strong>Configured agent</strong><code>{agent.name}</code></p><p><strong>Canonical agent</strong><code>{agent.agent}</code></p><p><strong>Model</strong><span>{agent.model || 'Default model'}</span></p></div><div className="setup-confirm-scope"><div><strong>Will do</strong><span>Send the check only through this selected, configured agent.</span></div><div><strong>Will not do</strong><span>Change configuration or store credentials in the Dashboard.</span></div></div><footer><button className="button" disabled={busy} onClick={onCancel}>Cancel</button><button className="button primary" disabled={busy} onClick={onConfirm}><Play size={16} />Run check</button></footer></section></div>
}

function RuntimeControls({ state, runtimeControl, runtimeLoading = false, runtimeError = '', busy = false, panelRefineActive = false, onRefreshRuntime, onDaemonAction, onStartDaemon, onQueueAction }: Pick<SetupProps, 'state' | 'runtimeControl' | 'runtimeLoading' | 'runtimeError' | 'busy' | 'panelRefineActive' | 'onRefreshRuntime' | 'onDaemonAction' | 'onStartDaemon' | 'onQueueAction'>) {
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
  const gateReason = panelRefineActive ? 'Panel Refine is active.' : runtime?.operationActive ? runtime.disabledReason || 'A daemon operation is in progress.' : ''
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
    <section className="setup-runtime" aria-label="RoboRev Runtime">
      <header className="setup-subheading"><div><span className="eyebrow">RoboRev Runtime</span><h2>Daemon and queue controls</h2><p>Typed controls reflect the daemon source of truth. Actions are serialized while an operation is active.</p></div>{onRefreshRuntime && <button className="button ghost compact" disabled={runtimeLoading} onClick={() => void onRefreshRuntime()}><RefreshCw size={14} className={runtimeLoading ? 'spin' : ''} />Refresh</button>}</header>
      {runtimeError && <p className="setup-action-error" role="alert">{runtimeError}</p>}
      {unavailable ? <div className="runtime-unavailable"><Server size={18} /><div><strong>Runtime controls unavailable</strong><p>Refresh to retrieve the typed daemon and queue state.</p></div></div> : <div className="runtime-control-grid">
        <article className="runtime-control-card"><div><span className="eyebrow">Daemon</span><h3>{runtime?.daemonRunning ? 'Daemon Running' : 'Daemon Offline'}</h3><p>{runtime?.daemonOperation || (runtime?.daemonRunning ? 'Ready to accept typed review operations.' : 'The daemon is not accepting new operations.')}</p></div><span className={`status ${runtime?.daemonRunning ? 'status-pass' : 'status-failed'}`}><span className="status-dot" />{runtime?.daemonRunning ? 'Running' : 'Offline'}</span><div className="runtime-buttons"><button className="button" disabled={actionDisabled || !runtime?.canStart || !daemonCommandReady('start')} onClick={() => void runDaemon('start')}><Play size={15} />Start daemon</button><button className="button" disabled={actionDisabled || !runtime?.canStop || !daemonCommandReady('stop')} onClick={() => void runDaemon('stop')}><Square size={15} />Stop daemon</button><button className="button" disabled={actionDisabled || !runtime?.canRestart || !daemonCommandReady('restart')} onClick={() => void runDaemon('restart')}><RotateCcw size={15} />Restart daemon</button></div></article>
        <article className="runtime-control-card"><div><span className="eyebrow">Review queue</span><h3>{runtime?.queueState === 'paused' ? 'Queue Paused' : runtime?.queueState === 'processing' ? 'Queue Processing' : 'Queue Unknown'}</h3><p>{runtime?.queueState === 'paused' ? 'Running jobs continue; new queued jobs will not start.' : runtime?.queueState === 'processing' ? 'Queued jobs can start when capacity is available.' : 'Queue state is unavailable while the daemon is offline or unknown.'}</p></div><span className={`status ${runtime?.queueState === 'paused' ? 'status-queued' : runtime?.queueState === 'processing' ? 'status-pass' : 'status-failed'}`}><span className="status-dot" />{runtime?.queueState === 'paused' ? 'Paused' : runtime?.queueState === 'processing' ? 'Processing' : 'Unknown'}</span><div className="runtime-buttons"><button className="button" disabled={queueDisabled || !runtime?.canPause} onClick={() => void runQueue('pause')}><Pause size={15} />Pause queue</button><button className="button" disabled={queueDisabled || !runtime?.canResume} onClick={() => void runQueue('resume')}><Play size={15} />Resume queue</button></div>{queueDisabled && <small className="runtime-disabled-reason">{gateReason || runtime?.disabledReason || (!runtime?.daemonRunning ? 'Daemon is offline.' : runtime?.queueState === 'unknown' ? 'Queue state is unknown. Refresh to retry.' : '')}</small>}</article>
      </div>}
      {gateReason && <p className="runtime-disabled-reason">Controls disabled: {gateReason}</p>}
    </section>
    {confirmation && runtime?.daemonCommands?.[confirmation] && daemonCommandReady(confirmation) && <RuntimeConfirmation action={confirmation} command={runtime.daemonCommands[confirmation]} busy={actionBusy} onCancel={() => { if (!actionBusy) setConfirmation(null) }} onConfirm={() => void confirmDaemon()} />}
  </>
}

function ConfiguredReviewAgents({ agents, loading = false, error = '', busy = false, onRefresh, onCheck }: { agents?: AgentsResponse | null; loading?: boolean; error?: string; busy?: boolean; onRefresh?: SetupAction; onCheck?: AgentCheckAction }) {
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
    <section className="configured-agents" aria-label="Configured Review Agents">
      <header className="setup-subheading"><div><span className="eyebrow">Configured Review Agents</span><h2>Configured Review Agents</h2><p>Detection confirms executable/config presence. Health is separate and changes only after an explicit check.</p></div>{onRefresh && <button className="button ghost compact" disabled={loading || busy || actionBusy} onClick={() => void onRefresh()}><RefreshCw size={14} className={loading || busy || actionBusy ? 'spin' : ''} />Refresh</button>}</header>
      {error && <p className="setup-action-error" role="alert">{error}</p>}
      {loading && !agents ? <div className="agents-loading">Loading configured agents…</div> : !agents ? <div className="runtime-unavailable"><Server size={18} /><div><strong>Configured agents unavailable</strong><p>Refresh to retrieve the typed agent configuration.</p></div></div> : agents.configured.length === 0 ? <div className="agents-loading">No configured review agents were reported by RoboRev.</div> : <div className="agent-cards">{agents.configured.map(agent => <article className="agent-card" key={`${agent.name}:${agent.agent}`}><div className="agent-card-main"><div className="agent-heading"><BotGlyph /><div><h3>{agent.name}</h3><code>{agent.agent}</code></div></div><div className="agent-facts"><span><strong>Detected</strong><em className={agent.detected ? 'detected' : 'not-detected'}>{agent.detected ? 'Yes' : 'No'}</em></span><span><strong>Health</strong><em className={`status ${agentStatusClass(agent.health)}`}><span className="status-dot" />{healthLabel(agent.health)}</em></span>{agent.model && <span><strong>Model</strong><em>{agent.model}</em></span>}{agent.reasoning && <span><strong>Reasoning</strong><em>{agent.reasoning}</em></span>}</div><p className="agent-detail">{agent.detected ? agent.detectionReason || agent.resolvedPath || 'Executable/configuration detected.' : agent.detectionReason || 'Executable or configuration was not detected.'}</p><div className="agent-command"><strong>Command</strong><code>{commandLabel(agent.command)}</code>{agent.commandSource && <small>Source: {agent.commandSource}</small>}</div>{agent.lastCheckedAt && <small className="agent-checked">Last checked {agent.lastCheckedAt}</small>}</div><button className="button compact" disabled={busy || actionBusy || !onCheck || !agent.detected} title={!agent.detected ? 'Agent must be detected before a check can run.' : undefined} onClick={() => setSelected(agent)}>Run check</button></article>)}</div>}
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
  const sourceURL = state.sourceURL || SOURCE_URL
  const exactBytes = state.totalBytes === undefined ? 'Backend-supplied exact archive size' : `${state.totalBytes.toLocaleString()} bytes`
  return <div className="prompt-overlay" role="presentation"><section className="prompt-dialog setup-confirm-dialog" role="dialog" aria-modal="true" aria-labelledby="setup-confirm-title"><header><div><span className="eyebrow">Explicit confirmation</span><h2 id="setup-confirm-title">Install &amp; Start RoboRev?</h2><p>This is the only step that downloads and installs software.</p></div><button className="icon-button" aria-label="Close install confirmation" onClick={onCancel}><X /></button></header><div className="setup-confirm-facts"><p><strong>Repository</strong><a href={SOURCE_REPOSITORY} target="_blank" rel="noreferrer">github.com/kenn-io/roborev</a></p><p><strong>Release source</strong><a href={sourceURL} target="_blank" rel="noreferrer">{state.sourceLabel || 'Official GitHub release'}</a></p><p><strong>Version</strong><code>{state.recommendedVersion}</code></p><p><strong>Asset bytes</strong><code>{exactBytes}</code></p><p><strong>Target</strong><code>{state.targetPath || 'Verified Dashboard target'}</code></p><p><strong>SHA-256</strong><code>{state.sha256 || 'Checksum supplied by backend'}</code></p><p><strong>Compatibility</strong><span>{state.compatible === true ? 'Compatible with this Windows architecture.' : 'Backend will verify compatibility before installation.'}</span></p></div><div className="setup-confirm-scope"><div><strong>Will do</strong><span>Download, hash-check, install, probe, and start the daemon.</span></div><div><strong>Will not do</strong><span>Change security policy or PATH, run <code>roborev init</code>, install agents/hooks, or store keys.</span></div></div><footer><button className="button" disabled={busy} onClick={onCancel}>Cancel</button><button className="button primary" disabled={busy} onClick={onInstall}><Download size={16} />Install &amp; Start</button></footer></section></div>
}

export function Setup({ state, busy = false, onInstall, onRetry, onStartDaemon, agents, agentsLoading, agentsError, onRefreshAgents, onCheckAgent, runtimeControl, runtimeLoading, runtimeError, onRefreshRuntime, onDaemonAction, onQueueAction, panelRefineActive }: SetupProps) {
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
    try { await action() } catch (error) { setActionError((error as Error).message || 'The setup action failed.') } finally { actionLock.current = false; setActionBusy(false) }
  }
  const install = () => {
    setConfirming(false)
    void runAction(onInstall)
  }
  const sourceURL = state?.sourceURL || SOURCE_URL
  const recommended = state?.recommendedVersion || 'v0.64.0'
  const installable = Boolean(state && !state.installed && !state.operationActive && (state.installAvailable || state.canInstall) && onInstall)
  return <section className="cards-page setup-page">
    <header className="section-heading"><div><span className="eyebrow">Runtime health</span><h1>Agents &amp; Setup</h1><p>Dashboard uses existing agent CLI authentication and never stores tokens.</p></div></header>
    {state && <SetupBanner state={state} busy={busy || actionBusy} onInstall={() => { setConfirming(true); return Promise.resolve() }} onRetry={() => runAction(onRetry)} />}
    {!state && <section className="setup-card"><Server /><div><h3>Setup state unavailable</h3><p>The Dashboard could not validate the typed RoboRev setup response.</p></div></section>}
    {state && <article className="setup-card setup-detail-card"><Server /><div><h3>RoboRev {state.version || 'not detected'}</h3><code>{state.binaryPath || 'PATH lookup unavailable'}</code><p>{state.daemonRunning ? 'Daemon is running.' : state.installed ? 'Binary is installed; daemon is stopped.' : 'RoboRev is not installed.'}</p></div><span className={`status ${state.daemonRunning ? 'status-pass' : state.installed ? 'status-queued' : 'status-failed'}`}><span className="status-dot" />{state.daemonRunning ? 'Ready' : state.installed ? 'Stopped' : 'Setup required'}</span></article>}
    <RuntimeControls state={state} runtimeControl={runtimeControl} runtimeLoading={runtimeLoading} runtimeError={runtimeError} busy={busy || actionBusy} panelRefineActive={panelRefineActive} onRefreshRuntime={onRefreshRuntime} onDaemonAction={onDaemonAction} onStartDaemon={onStartDaemon} onQueueAction={onQueueAction} />
    <ConfiguredReviewAgents agents={agents} loading={agentsLoading} error={agentsError} busy={busy || actionBusy} onRefresh={onRefreshAgents} onCheck={onCheckAgent} />
    {state && <section className="setup-facts" aria-label="RoboRev setup details"><h2>Verified installation</h2><dl>
      <div><dt>Requested version</dt><dd>{recommended}</dd></div>
      <div><dt>Architecture</dt><dd>{state.architecture || '—'}</dd></div>
      <div><dt>Source</dt><dd>{state.sourceLabel || 'Official GitHub release'} <a href={sourceURL} target="_blank" rel="noreferrer" aria-label="Open official release source"><ExternalLink size={13} /></a></dd></div>
      <div><dt>Official repository</dt><dd><a href={SOURCE_REPOSITORY} target="_blank" rel="noreferrer">github.com/kenn-io/roborev</a></dd></div>
      <div><dt>Asset</dt><dd>{state.assetName || '—'}</dd></div>
      <div><dt>Target</dt><dd>{state.targetPath || '—'}</dd></div>
      <div><dt>SHA-256</dt><dd className="setup-hash">{state.sha256 || '—'}</dd></div>
      <div><dt>Compatibility</dt><dd>{!state.installed ? 'Pending installation' : state.compatible === true ? 'Compatible with this Dashboard' : state.compatible === false ? 'Not compatible' : 'Pending verification'}</dd></div>
    </dl></section>}
    {state?.operationActive && <section className="setup-card setup-active-card"><RefreshCw className="spin" /><div><h3>{setupPhaseLabel(state.phase)}</h3><p>Setup operations are serialized. Review and mutation controls remain disabled until the backend reports ready.</p><Progress state={state} /></div></section>}
    {state && <section className="setup-safety"><h2>Explicit install scope</h2><div className="setup-safety-grid"><div><h3><Check size={15} />Will do</h3><ul><li>Download the exact {recommended} release.</li><li>Verify the full SHA-256 checksum.</li><li>Install to the verified target and probe the binary.</li><li>Start and verify the RoboRev daemon.</li></ul></div><div><h3><X size={15} />Will not do</h3><ul><li>Change Windows security policy or PATH.</li><li>Run <code>roborev init</code>.</li><li>Install agents, hooks, or extensions.</li><li>Read or store agent keys and tokens.</li></ul></div></div></section>}
    {actionError && <p role="alert" className="setup-action-error">{actionError}</p>}
    {installable && confirming && state && <InstallConfirmation state={state} busy={actionBusy} onCancel={() => setConfirming(false)} onInstall={install} />}
  </section>
}
