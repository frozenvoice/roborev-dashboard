import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Check, Download, ExternalLink, Info, LockKeyhole, RefreshCw, Server, ShieldAlert, X } from 'lucide-react'
import type { SetupState } from './api'

export type SetupAction = () => Promise<void>

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
}

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

export function Setup({ state, busy = false, onInstall, onRetry, onStartDaemon }: SetupProps) {
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
    {state && <SetupBanner state={state} busy={busy || actionBusy} onInstall={() => { setConfirming(true); return Promise.resolve() }} onRetry={() => runAction(onRetry)} onStartDaemon={() => runAction(onStartDaemon)} />}
    {!state && <section className="setup-card"><Server /><div><h3>Setup state unavailable</h3><p>The Dashboard could not validate the typed RoboRev setup response.</p></div></section>}
    {state && <article className="setup-card setup-detail-card"><Server /><div><h3>RoboRev {state.version || 'not detected'}</h3><code>{state.binaryPath || 'PATH lookup unavailable'}</code><p>{state.daemonRunning ? 'Daemon is running.' : state.installed ? 'Binary is installed; daemon is stopped.' : 'RoboRev is not installed.'}</p></div><span className={`status ${state.daemonRunning ? 'status-pass' : state.installed ? 'status-queued' : 'status-failed'}`}><span className="status-dot" />{state.daemonRunning ? 'Ready' : state.installed ? 'Stopped' : 'Setup required'}</span></article>}
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
