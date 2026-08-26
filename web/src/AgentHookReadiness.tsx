import { AlertTriangle, CheckCircle2, RefreshCw, ShieldAlert, X } from 'lucide-react'
import { useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from 'react'
import { normalizeAgentHookManagementPreview, normalizeAgentHookManagementResult, normalizeAgentHookReadiness, request, type AgentHookManagementPreview, type AgentHookManagementResult, type AgentHookReadiness, type AgentHookReadinessAgent, type AgentHookReadinessStatus } from './api'
import { useI18n } from './i18n'

type ReadinessResourceState =
  | { status: 'success'; value: AgentHookReadiness }
  | { status: 'failure' }

type ReadinessResource = {
  generation: number
  controller: AbortController
  promise: Promise<AgentHookReadiness>
}

type HookMutationPhase = 'idle' | 'previewing' | 'ready' | 'executing'

let resourceState: ReadinessResourceState | null = null
let resourceInFlight: ReadinessResource | null = null
let resourceGeneration = 0

function readinessError() {
  return new Error('Agent hook readiness response unavailable')
}

function readReadiness(force = false): Promise<AgentHookReadiness> {
  if (!force) {
    if (resourceState?.status === 'success') return Promise.resolve(resourceState.value)
    if (resourceState?.status === 'failure') return Promise.reject(readinessError())
    if (resourceInFlight) return resourceInFlight.promise
  }

  if (resourceInFlight) resourceInFlight.controller.abort()
  resourceState = null
  const generation = ++resourceGeneration
  const controller = new AbortController()
  const promise = request<unknown>('/api/agent-hooks/readiness', { signal: controller.signal })
    .then(value => {
      const normalized = normalizeAgentHookReadiness(value)
      if (!normalized) throw readinessError()
      if (generation === resourceGeneration) resourceState = { status: 'success', value: normalized }
      return normalized
    })
    .catch(error => {
      if (generation === resourceGeneration) resourceState = { status: 'failure' }
      throw error
    })
    .finally(() => {
      if (resourceInFlight?.generation === generation) resourceInFlight = null
    })
  resourceInFlight = { generation, controller, promise }
  return promise
}

/** Reset the module resource between isolated frontend tests. */
export function resetAgentHookReadinessForTests() {
  resourceInFlight?.controller.abort()
  resourceInFlight = null
  resourceState = null
  resourceGeneration += 1
}

const agentLabels: Record<AgentHookReadinessAgent, string> = {
  codex: 'Codex',
  claude: 'Claude',
  grok: 'Grok Build',
  droid: 'Factory Droid',
}

function statusLabel(status: AgentHookReadinessStatus, t: ReturnType<typeof useI18n>['t']) {
  if (status === 'current') return t('setup.agentHookReadinessCurrent')
  if (status === 'needs-update') return t('setup.agentHookReadinessNeedsUpdate')
  return t('setup.agentHookReadinessUnavailable')
}

function statusClass(status: AgentHookReadinessStatus) {
  if (status === 'current') return 'status-pass'
  if (status === 'needs-update') return 'status-queued'
  return 'status-failed'
}

function resultLabel(status: AgentHookManagementResult['status'], t: ReturnType<typeof useI18n>['t']) {
  return status === 'verified-success' ? t('setup.agentHookManagementVerified') : t('setup.agentHookManagementFailed')
}

const dialogFocusableSelector = 'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

type AgentHookManagementDialogProps = {
  preview: AgentHookManagementPreview
  busy: boolean
  onCancel: () => void
  onConfirm: () => void
}

function AgentHookManagementDialog({ preview, busy, onCancel, onConfirm }: AgentHookManagementDialogProps) {
  const { t } = useI18n()
  const dialogRef = useRef<HTMLElement | null>(null)
  const cancelRef = useRef<HTMLButtonElement | null>(null)

  useEffect(() => { cancelRef.current?.focus() }, [])

  const onDialogKeyDown = (event: ReactKeyboardEvent<HTMLElement>) => {
    if (event.key === 'Escape') {
      if (!busy) {
        event.preventDefault()
        onCancel()
      }
      return
    }
    if (event.key !== 'Tab') return
    const dialog = dialogRef.current
    if (!dialog) return
    const focusable = Array.from(dialog.querySelectorAll<HTMLElement>(dialogFocusableSelector)).filter(element => !element.hidden)
    if (focusable.length === 0) {
      event.preventDefault()
      dialog.focus()
      return
    }
    const active = document.activeElement
    const index = active instanceof HTMLElement ? focusable.indexOf(active) : -1
    if (event.shiftKey ? index <= 0 : index === focusable.length - 1 || index < 0) {
      event.preventDefault()
      focusable[event.shiftKey ? focusable.length - 1 : 0].focus()
    }
  }

  return <div className="prompt-overlay agent-hook-management-overlay" role="presentation">
    <section className="prompt-dialog agent-hook-management-dialog" role="dialog" aria-modal="true" aria-labelledby="agent-hook-management-title" aria-describedby="agent-hook-management-description" tabIndex={-1} ref={dialogRef} onKeyDown={onDialogKeyDown}>
      <header><div><h2 id="agent-hook-management-title">{t('setup.agentHookManagementTitle')}</h2><p id="agent-hook-management-description">{t('setup.agentHookManagementDescription', { agent: agentLabels[preview.agent] })}</p></div><button className="icon-button" type="button" aria-label={t('setup.agentHookManagementClose')} onClick={onCancel} disabled={busy}><X /></button></header>
      <dl className="agent-hook-management-facts"><div><dt>{t('setup.agentHookManagementAgent')}</dt><dd>{agentLabels[preview.agent]}</dd></div><div><dt>{t('setup.agentHookManagementAction')}</dt><dd>{t('setup.agentHookManagementActionValue')}</dd></div></dl>
      <div className="agent-hook-management-command"><strong>{t('setup.agentHookManagementCommand')}</strong><code>{JSON.stringify([preview.command.executable, ...preview.command.args])}</code><small>{t('setup.agentHookManagementCommandNote')}</small></div>
      <p className="agent-hook-management-warning"><AlertTriangle size={15} aria-hidden="true" /><span>{t('setup.agentHookManagementWarning')}</span></p>
      <footer><button className="button" type="button" ref={cancelRef} onClick={onCancel} disabled={busy}>{t('common.cancel')}</button><button className="button primary" type="button" onClick={onConfirm} disabled={busy}>{busy ? t('setup.agentHookManagementExecuting') : <><CheckCircle2 size={15} />{t('setup.agentHookManagementConfirm')}</>}</button></footer>
    </section>
  </div>
}

export function AgentHookReadiness() {
  const { t } = useI18n()
  const [view, setView] = useState<ReadinessResourceState | 'loading'>('loading')
  const [mutationPhase, setMutationPhase] = useState<HookMutationPhase>('idle')
  const [activeAgent, setActiveAgent] = useState<AgentHookReadinessAgent | null>(null)
  const [preview, setPreview] = useState<AgentHookManagementPreview | null>(null)
  const [mutationResult, setMutationResult] = useState<AgentHookManagementResult | null>(null)
  const [mutationError, setMutationError] = useState('')
  const mounted = useRef(false)
  const localGeneration = useRef(0)
  const mutationGeneration = useRef(0)
  const previewController = useRef<AbortController | null>(null)
  const preparing = useRef(false)
  const confirming = useRef(false)
  const invokingCta = useRef<HTMLButtonElement | null>(null)

  const load = (force = false) => {
    const generation = ++localGeneration.current
    setView('loading')
    void readReadiness(force)
      .then(value => {
        if (mounted.current && generation === localGeneration.current) setView({ status: 'success', value })
      })
      .catch(() => {
        if (mounted.current && generation === localGeneration.current) setView({ status: 'failure' })
      })
  }

  useEffect(() => {
    mounted.current = true
    load()
    return () => {
      mounted.current = false
      mutationGeneration.current += 1
      previewController.current?.abort()
      previewController.current = null
      preparing.current = false
      confirming.current = false
    }
  }, [])

  useEffect(() => {
    if (mutationPhase !== 'idle' || preview !== null) return
    const button = invokingCta.current
    if (button && document.contains(button)) button.focus()
    invokingCta.current = null
  }, [mutationPhase, preview])

  const closePreview = () => {
    if (mutationPhase === 'executing') return
    mutationGeneration.current += 1
    previewController.current?.abort()
    previewController.current = null
    setPreview(null)
    setActiveAgent(null)
    setMutationError('')
    setMutationPhase('idle')
  }

  const prepare = async (agent: AgentHookReadinessAgent, button: HTMLButtonElement) => {
    const currentView = view === 'loading' ? null : view.status === 'success' ? view.value : null
    const readiness = currentView?.agents.find(item => item.agent === agent)
    if (!readiness || readiness.status !== 'needs-update' || mutationPhase !== 'idle' || preparing.current || confirming.current || !mounted.current) return
    preparing.current = true
    invokingCta.current = button
    const generation = ++mutationGeneration.current
    previewController.current?.abort()
    const controller = new AbortController()
    previewController.current = controller
    setActiveAgent(agent)
    setPreview(null)
    setMutationResult(null)
    setMutationError('')
    setMutationPhase('previewing')
    try {
      const payload = await request<unknown>('/api/agent-hooks/preview', { method: 'POST', body: JSON.stringify({ agent }), signal: controller.signal })
      const normalized = normalizeAgentHookManagementPreview(payload, agent)
      if (!normalized) throw new Error('invalid-agent-hook-preview')
      if (!mounted.current || generation !== mutationGeneration.current) return
      setPreview(normalized)
      setMutationPhase('ready')
    } catch (error) {
      if (controller.signal.aborted || !mounted.current || generation !== mutationGeneration.current) return
      setMutationError(error instanceof Error && error.message === 'invalid-agent-hook-preview' ? t('setup.agentHookManagementMalformed') : t('setup.agentHookManagementPreviewFailed'))
      setActiveAgent(null)
      setMutationPhase('idle')
    } finally {
      if (previewController.current === controller) previewController.current = null
      preparing.current = false
    }
  }

  const refreshAfterTerminal = () => load(true)

  const confirm = async () => {
    if (!preview || mutationPhase !== 'ready' || confirming.current || !mounted.current) return
    confirming.current = true
    const generation = mutationGeneration.current
    const agent = preview.agent
    setMutationError('')
    setMutationPhase('executing')
    try {
      const payload = await request<unknown>('/api/agent-hooks/execute', { method: 'POST', body: JSON.stringify({ token: preview.token }) })
      const normalized = normalizeAgentHookManagementResult(payload, agent)
      if (!normalized) throw new Error('invalid-agent-hook-result')
      if (!mounted.current || generation !== mutationGeneration.current) return
      setMutationResult(normalized)
      setPreview(null)
      setActiveAgent(null)
      setMutationPhase('idle')
      refreshAfterTerminal()
    } catch (error) {
      if (!mounted.current || generation !== mutationGeneration.current) return
      setPreview(null)
      setActiveAgent(null)
      setMutationError(error instanceof Error && error.message === 'invalid-agent-hook-result' ? t('setup.agentHookManagementMalformed') : t('setup.agentHookManagementExecuteFailed'))
      setMutationPhase('idle')
      refreshAfterTerminal()
    } finally {
      confirming.current = false
    }
  }

  const loading = view === 'loading'
  const agents = view !== 'loading' && view.status === 'success' ? view.value.agents : []
  return <section className="agent-hook-readiness" aria-label={t('setup.agentHookReadinessTitle')} aria-busy={loading || mutationPhase === 'previewing' || mutationPhase === 'executing'}>
    <header className="setup-subheading">
      <div><h2>{t('setup.agentHookReadinessTitle')}</h2><p>{t('setup.agentHookReadinessDescription')}</p></div>
      <button className="button ghost compact" aria-label={t('setup.agentHookReadinessRefresh')} disabled={mutationPhase !== 'idle'} onClick={() => load(true)}><RefreshCw size={14} className={loading ? 'spin' : ''} />{t('common.refresh')}</button>
    </header>
    {loading && <div className="agent-hook-readiness-state" role="status" aria-live="polite"><RefreshCw size={17} className="spin" />{t('setup.agentHookReadinessLoading')}</div>}
    {view !== 'loading' && view.status === 'failure' && <div className="agent-hook-readiness-state agent-hook-readiness-error" role="alert"><ShieldAlert size={17} /><div><strong>{t('setup.agentHookReadinessUnavailable')}</strong><p>{t('setup.agentHookReadinessRetry')}</p></div></div>}
    {agents.length > 0 && <div className="agent-hook-readiness-list" role="list">{agents.map(agent => <article className="agent-hook-readiness-row" role="listitem" key={agent.agent}><div><h3>{agentLabels[agent.agent]}</h3><code>{agent.agent}</code></div><div className="agent-hook-readiness-row-action"><span className={`status ${statusClass(agent.status)}`} role="status"><span className="status-dot" />{statusLabel(agent.status, t)}</span>{agent.status === 'needs-update' && <button className="button compact" type="button" disabled={mutationPhase !== 'idle' || loading} onClick={event => void prepare(agent.agent, event.currentTarget)}>{t('setup.agentHookManagementCta')}</button>}</div></article>)}</div>}
    {mutationError && <p className="agent-hook-management-error agent-hook-management-result-error" role="alert">{mutationError}</p>}
    {mutationResult && <div className={`agent-hook-management-result ${mutationResult.status}`} role={mutationResult.status === 'failed-unverified' ? 'alert' : 'status'}><strong>{resultLabel(mutationResult.status, t)}</strong>{mutationResult.errorCode && <span>{t(mutationResult.status === 'verified-success' ? 'setup.agentHookManagementVerifiedWarning' : 'setup.agentHookManagementResultDetail')}</span>}</div>}
    {preview && (mutationPhase === 'ready' || mutationPhase === 'executing') && <AgentHookManagementDialog preview={preview} busy={mutationPhase === 'executing'} onCancel={closePreview} onConfirm={() => void confirm()} />}
    {activeAgent && mutationPhase === 'previewing' && <div className="agent-hook-management-progress" role="status"><RefreshCw size={15} className="spin" />{t('setup.agentHookManagementPreparing', { agent: agentLabels[activeAgent] })}</div>}
  </section>
}
