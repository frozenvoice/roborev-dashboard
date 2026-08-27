import { AlertTriangle, Check, RefreshCw, Settings2, ShieldAlert, X } from 'lucide-react'
import { useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from 'react'
import { executeRepositoryAgentConfiguration, fetchRepositoryAgentConfigurationPreview, fetchRepositoryAgentConfigurationStatus, repositoryAgents, type RepositoryAgent, type RepositoryAgentConfigurationPreview, type RepositoryAgentConfigurationScope, type RepositoryAgentConfigurationStatus, type RepositoryAgentConfigurationStatusResponse } from './api'
import { useI18n } from './i18n'

type Props = { repositoryIdentity?: string }

const agentLabels: Record<RepositoryAgent, string> = {
  codex: 'Codex', 'claude-code': 'Claude Code', gemini: 'Gemini', copilot: 'GitHub Copilot', opencode: 'OpenCode', cursor: 'Cursor', kiro: 'Kiro', kilo: 'Kilo', droid: 'Factory Droid', pi: 'Pi', grok: 'Grok',
}

function statusLabel(status: RepositoryAgentConfigurationStatus, t: ReturnType<typeof useI18n>['t']) {
  if (status === 'canonical') return t('setup.agentConfigurationCanonical')
  if (status === 'custom') return t('setup.agentConfigurationCustom')
  return t('setup.agentConfigurationUnset')
}

function statusClass(status: RepositoryAgentConfigurationStatus) {
  return status === 'canonical' ? 'status-pass' : status === 'custom' ? 'status-queued' : 'status-failed'
}

const dialogFocusableSelector = 'button:not([disabled]), input:not([disabled]), select:not([disabled]), [href], [tabindex]:not([tabindex="-1"])'

export function RepositoryAgentConfiguration({ repositoryIdentity = '' }: Props) {
  const { t } = useI18n()
  const [status, setStatus] = useState<RepositoryAgentConfigurationStatusResponse | null>(null)
  const [statusLoading, setStatusLoading] = useState(false)
  const [statusError, setStatusError] = useState(false)
  const [selected, setSelected] = useState<Record<RepositoryAgentConfigurationScope, RepositoryAgent>>({ global: 'codex', repository: 'codex' })
  const [preview, setPreview] = useState<RepositoryAgentConfigurationPreview | null>(null)
  const [previewLoading, setPreviewLoading] = useState(false)
  const [previewError, setPreviewError] = useState(false)
  const [dialogOpen, setDialogOpen] = useState(false)
  const [confirmed, setConfirmed] = useState(false)
  const [executeLoading, setExecuteLoading] = useState(false)
  const [result, setResult] = useState<'verified-success' | 'failed-unverified' | null>(null)
  const statusControllerRef = useRef<AbortController | null>(null)
  const previewControllerRef = useRef<AbortController | null>(null)
  const actionLockRef = useRef(false)
  const identityRef = useRef(repositoryIdentity)
  const generationRef = useRef(0)
  const mountedRef = useRef(true)

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      generationRef.current += 1
      statusControllerRef.current?.abort()
      previewControllerRef.current?.abort()
    }
  }, [])

  // Repository switches discard all browser-held aliases, previews, and
  // receipts. An execute already sent to the server is intentionally not
  // aborted; its late receipt is ignored by the generation check.
  useEffect(() => {
    generationRef.current += 1
    identityRef.current = repositoryIdentity
    statusControllerRef.current?.abort()
    previewControllerRef.current?.abort()
    statusControllerRef.current = null
    previewControllerRef.current = null
    actionLockRef.current = false
    setStatus(null)
    setStatusLoading(false)
    setStatusError(false)
    setPreview(null)
    setPreviewLoading(false)
    setPreviewError(false)
    setDialogOpen(false)
    setConfirmed(false)
    setExecuteLoading(false)
    setResult(null)
  }, [repositoryIdentity])

  const checkStatus = async () => {
    if (!repositoryIdentity || statusLoading || actionLockRef.current) return
    const identity = repositoryIdentity
    const generation = ++generationRef.current
    statusControllerRef.current?.abort()
    previewControllerRef.current?.abort()
    const controller = new AbortController()
    statusControllerRef.current = controller
    actionLockRef.current = true
    setStatusLoading(true)
    setStatusError(false)
    setStatus(null)
    setPreview(null)
    setPreviewError(false)
    setDialogOpen(false)
    setConfirmed(false)
    setResult(null)
    try {
      const next = await fetchRepositoryAgentConfigurationStatus(identity, controller.signal)
      if (!mountedRef.current || controller.signal.aborted || generation !== generationRef.current || identityRef.current !== identity) return
      setStatus(next)
    } catch {
      if (mountedRef.current && !controller.signal.aborted && generation === generationRef.current && identityRef.current === identity) setStatusError(true)
    } finally {
      if (statusControllerRef.current === controller) statusControllerRef.current = null
      if (mountedRef.current && generation === generationRef.current && identityRef.current === identity) {
        setStatusLoading(false)
        actionLockRef.current = false
      }
    }
  }

  const preparePreview = async (scope: RepositoryAgentConfigurationScope) => {
    if (!status || !status.repositoryIdentity || statusLoading || previewLoading || executeLoading || actionLockRef.current) return
    const identity = repositoryIdentity
    const alias = status.repositoryIdentity
    const agent = selected[scope]
    const generation = generationRef.current
    previewControllerRef.current?.abort()
    const controller = new AbortController()
    previewControllerRef.current = controller
    actionLockRef.current = true
    setPreviewLoading(true)
    setPreviewError(false)
    setPreview(null)
    setDialogOpen(false)
    setConfirmed(false)
    setResult(null)
    try {
      const next = await fetchRepositoryAgentConfigurationPreview(alias, scope, agent, controller.signal)
      if (!mountedRef.current || controller.signal.aborted || generation !== generationRef.current || identityRef.current !== identity || !status || status.repositoryIdentity !== alias) return
      setPreview(next)
      if (next.action === 'set-agent') setDialogOpen(true)
    } catch {
      if (mountedRef.current && !controller.signal.aborted && generation === generationRef.current && identityRef.current === identity) setPreviewError(true)
    } finally {
      if (previewControllerRef.current === controller) previewControllerRef.current = null
      if (mountedRef.current && generation === generationRef.current) {
        setPreviewLoading(false)
        actionLockRef.current = false
      }
    }
  }

  const cancelPreview = () => {
    if (!executeLoading) {
      setDialogOpen(false)
      setConfirmed(false)
    }
  }

  const execute = async () => {
    if (!preview || preview.action !== 'set-agent' || !preview.token || !confirmed || executeLoading || actionLockRef.current || !mountedRef.current) return
    const token = preview.token
    const generation = generationRef.current
    actionLockRef.current = true
    setExecuteLoading(true)
    setDialogOpen(false)
    setConfirmed(false)
    // Remove browser-held token evidence before the request. A failed or
    // ambiguous execute is never retried from this component.
    setPreview(null)
    try {
      const next = await executeRepositoryAgentConfiguration(token)
      if (mountedRef.current && generation === generationRef.current) setResult(next.status)
    } catch {
      if (mountedRef.current && generation === generationRef.current) setResult('failed-unverified')
    } finally {
      if (mountedRef.current && generation === generationRef.current) setExecuteLoading(false)
      if (mountedRef.current && generation === generationRef.current) actionLockRef.current = false
    }
  }

  const row = (scope: RepositoryAgentConfigurationScope) => status?.[scope]
  const scopeLabel = (scope: RepositoryAgentConfigurationScope) => scope === 'global' ? t('setup.agentConfigurationGlobal') : t('setup.agentConfigurationRepository')

  return <section className="repository-agent-configuration" aria-labelledby="repository-agent-configuration-title">
    <header className="setup-subheading">
      <div><span className="eyebrow">{t('setup.agentConfigurationEyebrow')}</span><h2 id="repository-agent-configuration-title">{t('setup.agentConfigurationTitle')}</h2><p>{t('setup.agentConfigurationDescription')}</p></div>
      <button type="button" className="button ghost compact" disabled={!repositoryIdentity || statusLoading || executeLoading} onClick={() => void checkStatus()}><RefreshCw size={14} className={statusLoading ? 'spin' : ''} />{status ? t('setup.agentConfigurationRefresh') : t('setup.agentConfigurationCheck')}</button>
    </header>
    {!status && !statusLoading && !statusError && <div className="repository-agent-configuration-state"><Settings2 size={16} />{t('setup.agentConfigurationNotChecked')}</div>}
    {statusLoading && <div className="repository-agent-configuration-state" role="status" aria-busy="true"><RefreshCw size={16} className="spin" />{t('setup.agentConfigurationChecking')}</div>}
    {statusError && !statusLoading && <div className="repository-agent-configuration-error" role="alert"><ShieldAlert size={16} />{t('setup.agentConfigurationUnavailable')}</div>}
    {status && !statusLoading && <div className="repository-agent-configuration-rows">
      {(['global', 'repository'] as const).map(scope => {
        const current = row(scope)
        if (!current) return null
        return <article className="repository-agent-configuration-row" key={scope}>
          <div className="repository-agent-configuration-row-heading"><div><strong>{scopeLabel(scope)}</strong><p>{scope === 'global' ? t('setup.agentConfigurationGlobalDescription') : t('setup.agentConfigurationRepositoryDescription')}</p></div><span className={`status ${statusClass(current.status)}`}><span className="status-dot" />{statusLabel(current.status, t)}{current.status === 'canonical' && 'agent' in current ? ` · ${agentLabels[current.agent]}` : ''}</span></div>
          <div className="repository-agent-configuration-controls">
            <label>{t('setup.agentConfigurationChoose')}<select aria-label={`${scopeLabel(scope)} ${t('setup.agentConfigurationChoose')}`} value={selected[scope]} disabled={previewLoading || executeLoading} onChange={event => setSelected(previous => ({ ...previous, [scope]: event.target.value as RepositoryAgent }))}>{repositoryAgents.map(agent => <option value={agent} key={agent}>{agentLabels[agent]}</option>)}</select></label>
            <button type="button" className="button" disabled={previewLoading || executeLoading || actionLockRef.current} onClick={() => void preparePreview(scope)}>{t('setup.agentConfigurationPreview')}</button>
          </div>
        </article>
      })}
    </div>}
    {previewLoading && <p className="repository-agent-configuration-state" role="status"><RefreshCw size={16} className="spin" />{t('setup.agentConfigurationPreviewing')}</p>}
    {previewError && <p className="repository-agent-configuration-error" role="alert"><AlertTriangle size={16} />{t('setup.agentConfigurationPreviewFailed')}</p>}
    {preview?.action === 'already-configured' && <div className="repository-agent-configuration-result already-configured" role="status"><strong>{t('setup.agentConfigurationAlreadyConfigured')}</strong><span>{t('setup.agentConfigurationCheckAgain')}</span></div>}
    {executeLoading && <p className="repository-agent-configuration-state" role="status"><RefreshCw size={16} className="spin" />{t('setup.agentConfigurationApplying')}</p>}
    {result === 'verified-success' && <div className="repository-agent-configuration-result verified-success" role="status"><strong>{t('setup.agentConfigurationSuccess')}</strong><span>{t('setup.agentConfigurationCheckAgain')}</span></div>}
    {result === 'failed-unverified' && <div className="repository-agent-configuration-result failed-unverified" role="alert"><strong>{t('setup.agentConfigurationFailed')}</strong><span>{t('setup.agentConfigurationMakeNewPreview')}</span></div>}
    {dialogOpen && preview?.action === 'set-agent' && preview.token && <AgentConfigurationDialog preview={preview} confirmed={confirmed} onConfirmed={setConfirmed} onCancel={cancelPreview} onConfirm={() => void execute()} />}
  </section>
}

type DialogProps = { preview: RepositoryAgentConfigurationPreview; confirmed: boolean; onConfirmed: (value: boolean) => void; onCancel: () => void; onConfirm: () => void }

function AgentConfigurationDialog({ preview, confirmed, onConfirmed, onCancel, onConfirm }: DialogProps) {
  const { t } = useI18n()
  const dialogRef = useRef<HTMLElement | null>(null)
  const cancelRef = useRef<HTMLButtonElement | null>(null)
  useEffect(() => { cancelRef.current?.focus() }, [])
  const onKeyDown = (event: ReactKeyboardEvent<HTMLElement>) => {
    if (event.key === 'Escape') { event.preventDefault(); onCancel(); return }
    if (event.key !== 'Tab') return
    const dialog = dialogRef.current
    if (!dialog) return
    const focusable = Array.from(dialog.querySelectorAll<HTMLElement>(dialogFocusableSelector)).filter(element => !element.hidden)
    if (!focusable.length) { event.preventDefault(); dialog.focus(); return }
    const index = document.activeElement instanceof HTMLElement ? focusable.indexOf(document.activeElement) : -1
    if (event.shiftKey ? index <= 0 : index === focusable.length - 1 || index < 0) { event.preventDefault(); focusable[event.shiftKey ? focusable.length - 1 : 0].focus() }
  }
  const scope = preview.scope === 'global' ? t('setup.agentConfigurationGlobal') : t('setup.agentConfigurationRepository')
  const warning = preview.scope === 'global' ? t('setup.agentConfigurationGlobalWarning') : t('setup.agentConfigurationRepositoryWarning')
  return <div className="prompt-overlay repository-agent-configuration-overlay" role="presentation"><section className="prompt-dialog repository-agent-configuration-dialog" role="dialog" aria-modal="true" aria-labelledby="repository-agent-configuration-confirm-title" tabIndex={-1} ref={dialogRef} onKeyDown={onKeyDown}>
    <header><div><span className="eyebrow">{t('setup.agentConfigurationExplicitConfirmation')}</span><h2 id="repository-agent-configuration-confirm-title">{t('setup.agentConfigurationConfirmTitle')}</h2><p>{t('setup.agentConfigurationConfirmDescription', { scope, agent: agentLabels[preview.agent] })}</p></div><button className="icon-button" type="button" aria-label={t('setup.agentConfigurationClose')} onClick={onCancel}><X /></button></header>
    <dl className="repository-agent-configuration-facts"><div><dt>{t('setup.agentConfigurationScope')}</dt><dd>{scope}</dd></div><div><dt>{t('setup.agentConfigurationAgent')}</dt><dd><code>{agentLabels[preview.agent]}</code></dd></div><div><dt>{t('setup.agentConfigurationAction')}</dt><dd>{preview.summary}</dd></div></dl>
    {preview.command && <div className="repository-agent-configuration-command"><strong>{t('setup.agentConfigurationCommand')}</strong><code>{JSON.stringify([preview.command.executable, ...preview.command.args])}</code><small>{t('setup.backendCommand')}</small></div>}
    <p className="repository-agent-configuration-warning"><AlertTriangle size={15} aria-hidden="true" />{warning}</p>
    {preview.currentStatus !== 'unset' && <p className="repository-agent-configuration-replace-notice">{t('setup.agentConfigurationReplaceNotice')}</p>}
    <label className="repository-agent-configuration-confirm-check"><input type="checkbox" checked={confirmed} onChange={event => onConfirmed(event.target.checked)} />{t('setup.agentConfigurationConfirmCheck')}</label>
    <footer><button className="button" type="button" ref={cancelRef} onClick={onCancel}>{t('common.cancel')}</button><button className="button primary" type="button" disabled={!confirmed} onClick={onConfirm}><Check size={15} />{t('setup.agentConfigurationConfirm')}</button></footer>
  </section></div>
}
