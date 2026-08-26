import { AlertTriangle, CheckCircle2, CircleAlert, RefreshCw, X } from 'lucide-react'
import { useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from 'react'
import { normalizeSkillsManagementPreview, normalizeSkillsManagementResult, normalizeSkillsReadiness, request, type SkillsManagementPreview, type SkillsManagementResult, type SkillsReadiness, type SkillsReadinessAgent, type SkillsReadinessStatus } from './api'
import { useI18n } from './i18n'

type SkillsResourceState =
  | { status: 'success'; value: SkillsReadiness }
  | { status: 'failure' }

type SkillsResource = {
  generation: number
  controller: AbortController
  promise: Promise<SkillsReadiness>
}

type SkillsMutationPhase = 'idle' | 'previewing' | 'ready' | 'executing'

// Skills status is a global resource. Keep the settled value and unresolved
// request outside the component so StrictMode/remounts do not duplicate reads.
let resourceState: SkillsResourceState | null = null
let resourceInFlight: SkillsResource | null = null
let resourceGeneration = 0

function skillsReadinessError() {
  return new Error('Skills readiness response unavailable')
}

function readSkillsReadiness(force = false): Promise<SkillsReadiness> {
  if (!force) {
    if (resourceState?.status === 'success') return Promise.resolve(resourceState.value)
    if (resourceState?.status === 'failure') return Promise.reject(skillsReadinessError())
    if (resourceInFlight) return resourceInFlight.promise
  }

  if (resourceInFlight) resourceInFlight.controller.abort()
  resourceState = null
  const generation = ++resourceGeneration
  const controller = new AbortController()
  const promise = request<unknown>('/api/skills/readiness', { signal: controller.signal })
    .then(value => {
      const normalized = normalizeSkillsReadiness(value)
      if (!normalized) throw skillsReadinessError()
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

/** Reset the global resource between isolated frontend tests. */
export function resetSkillsReadinessForTests() {
  resourceInFlight?.controller.abort()
  resourceInFlight = null
  resourceState = null
  resourceGeneration += 1
}

const agentLabels: Record<SkillsReadinessAgent, string> = {
  codex: 'Codex',
  claude: 'Claude Code',
  grok: 'Grok Build',
  droid: 'Factory Droid',
}

function statusLabel(status: SkillsReadinessStatus, t: ReturnType<typeof useI18n>['t']) {
  if (status === 'current') return t('setup.skillsReadinessCurrent')
  if (status === 'needs-install-update') return t('setup.skillsReadinessNeedsUpdate')
  return t('setup.skillsReadinessConfigUnavailable')
}

function statusClass(status: SkillsReadinessStatus) {
  if (status === 'current') return 'status-pass'
  if (status === 'needs-install-update') return 'status-queued'
  return 'status-canceled'
}

function resultLabel(status: SkillsManagementResult['status'], t: ReturnType<typeof useI18n>['t']) {
  if (status === 'verified-success') return t('setup.skillsManagementVerified')
  if (status === 'partial') return t('setup.skillsManagementPartial')
  return t('setup.skillsManagementFailed')
}

function resultDetail(result: SkillsManagementResult, t: ReturnType<typeof useI18n>['t']) {
  if (!result.errorCode) return null
  if (result.errorCode === 'cli-failed' && result.status === 'verified-success') return t('setup.skillsManagementVerifiedCliWarning')
  if (result.errorCode === 'cli-failed' && result.status === 'partial') return t('setup.skillsManagementPartialCliWarning')
  const key = `setup.skillsManagementError.${result.errorCode}` as Parameters<typeof t>[0]
  return t(key)
}

const dialogFocusableSelector = 'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

type SkillsManagementDialogProps = {
  preview: SkillsManagementPreview
  busy: boolean
  onCancel: () => void
  onConfirm: () => void
}

function SkillsManagementDialog({ preview, busy, onCancel, onConfirm }: SkillsManagementDialogProps) {
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

  return <div className="prompt-overlay skills-management-overlay" role="presentation">
    <section className="prompt-dialog skills-management-dialog" role="dialog" aria-modal="true" aria-labelledby="skills-management-title" aria-describedby="skills-management-description" aria-busy={busy} tabIndex={-1} ref={dialogRef} onKeyDown={onDialogKeyDown}>
      <header><div><h2 id="skills-management-title">{t('setup.skillsManagementTitle')}</h2><p id="skills-management-description">{t('setup.skillsManagementDescription')}</p></div><button className="icon-button" type="button" aria-label={t('setup.skillsManagementClose')} onClick={onCancel} disabled={busy}><X aria-hidden="true" /></button></header>
      <dl className="skills-management-facts"><div><dt>{t('setup.skillsManagementAgent')}</dt><dd>{agentLabels[preview.agent]}</dd></div><div><dt>{t('setup.skillsManagementAction')}</dt><dd>{t('setup.skillsManagementActionValue')}</dd></div></dl>
      <div className="skills-management-command"><strong>{t('setup.skillsManagementCommand')}</strong><code>{preview.command.executable} {preview.command.args.join(' ')}</code><small>{t('setup.skillsManagementCommandNote')}</small></div>
      <ul className="skills-management-warnings">
        <li><AlertTriangle size={15} aria-hidden="true" /><span>{t('setup.skillsManagementWarningOverwrite')}</span></li>
        <li><AlertTriangle size={15} aria-hidden="true" /><span>{t('setup.skillsManagementWarningCodexPolicy')}</span></li>
        <li><AlertTriangle size={15} aria-hidden="true" /><span>{t('setup.skillsManagementWarningUnrelated')}</span></li>
        <li><AlertTriangle size={15} aria-hidden="true" /><span>{t('setup.skillsManagementWarningLegacy')}</span></li>
      </ul>
      <footer><button className="button" type="button" ref={cancelRef} onClick={onCancel} disabled={busy}>{t('common.cancel')}</button><button className="button primary" type="button" onClick={onConfirm} disabled={busy}>{busy ? t('setup.skillsManagementExecuting') : <><CheckCircle2 size={15} aria-hidden="true" />{t('setup.skillsManagementConfirm')}</>}</button></footer>
    </section>
  </div>
}

export function SkillsReadiness() {
  const { t, locale } = useI18n()
  const [view, setView] = useState<SkillsResourceState | 'loading'>('loading')
  const [mutationPhase, setMutationPhase] = useState<SkillsMutationPhase>('idle')
  const [activeAgent, setActiveAgent] = useState<SkillsReadinessAgent | null>(null)
  const [preview, setPreview] = useState<SkillsManagementPreview | null>(null)
  const [mutationResult, setMutationResult] = useState<SkillsManagementResult | null>(null)
  const [mutationError, setMutationError] = useState('')
  const mounted = useRef(false)
  const localGeneration = useRef(0)
  const mutationGeneration = useRef(0)
  const previewController = useRef<AbortController | null>(null)
  const preparing = useRef(false)
  const confirming = useRef(false)
  const invokingCta = useRef<HTMLButtonElement | null>(null)

  const load = (force = false) => {
    if (!mounted.current) return
    const generation = ++localGeneration.current
    setView('loading')
    void readSkillsReadiness(force)
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
      localGeneration.current += 1
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

  const prepare = async (agent: SkillsReadinessAgent, button: HTMLButtonElement) => {
    const currentView = view !== 'loading' && view.status === 'success' ? view.value : null
    const readiness = currentView?.agents.find(item => item.agent === agent)
    if (!readiness || readiness.status !== 'needs-install-update' || mutationPhase !== 'idle' || preparing.current || confirming.current || !mounted.current) return
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
      const payload = await request<unknown>('/api/skills/preview', { method: 'POST', body: JSON.stringify({ agent }), signal: controller.signal })
      const normalized = normalizeSkillsManagementPreview(payload, agent)
      if (!normalized) throw new Error('invalid-skills-preview')
      if (!mounted.current || generation !== mutationGeneration.current) return
      setPreview(normalized)
      setMutationPhase('ready')
    } catch (error) {
      if (controller.signal.aborted || !mounted.current || generation !== mutationGeneration.current) return
      setMutationError(error instanceof Error && error.message === 'invalid-skills-preview' ? t('setup.skillsManagementMalformed') : t('setup.skillsManagementPreviewFailed'))
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
      const payload = await request<unknown>('/api/skills/execute', { method: 'POST', body: JSON.stringify({ token: preview.token }) })
      const normalized = normalizeSkillsManagementResult(payload, agent)
      if (!normalized) throw new Error('invalid-skills-result')
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
      setMutationError(error instanceof Error && error.message === 'invalid-skills-result' ? t('setup.skillsManagementMalformed') : t('setup.skillsManagementExecuteFailed'))
      setMutationPhase('idle')
      refreshAfterTerminal()
    } finally {
      confirming.current = false
    }
  }

  const loading = view === 'loading'
  const mutationBusy = mutationPhase !== 'idle'
  const agents = view !== 'loading' && view.status === 'success' ? view.value.agents : []
  return <section className="skills-readiness" aria-label={t('setup.skillsReadinessTitle')} aria-busy={loading || mutationBusy}>
    <header className="setup-subheading">
      <div><h2>{t('setup.skillsReadinessTitle')}</h2><p>{t('setup.skillsReadinessDescription')}</p></div>
      <button className="button ghost compact" aria-label={t('setup.skillsReadinessRefresh')} disabled={mutationBusy} onClick={() => load(true)}><RefreshCw size={14} className={loading ? 'spin' : ''} aria-hidden="true" />{t('common.refresh')}</button>
    </header>
    {loading && <div className="skills-readiness-state" role="status" aria-live="polite"><RefreshCw size={17} className="spin" aria-hidden="true" />{t('setup.skillsReadinessLoading')}</div>}
    {view !== 'loading' && view.status === 'failure' && <div className="skills-readiness-state skills-readiness-error" role="alert"><CircleAlert size={17} aria-hidden="true" /><div><strong>{t('setup.skillsReadinessUnavailable')}</strong><p>{t('setup.skillsReadinessRetry')}</p></div></div>}
    {agents.length > 0 && <div className="skills-readiness-list" role="list">{agents.map(agent => <article className="skills-readiness-row" role="listitem" key={agent.agent}>
      <div className="skills-readiness-agent"><h3>{agentLabels[agent.agent]}</h3><code>{agent.agent}</code></div>
      <div className="skills-readiness-row-detail"><span className={`status ${statusClass(agent.status)}`} role="status"><span className="status-dot" />{statusLabel(agent.status, t)}</span>
        {agent.status === 'current' && <span className="skills-readiness-count">{t('setup.skillsReadinessCurrentCount', { current: count(agent.current, locale), total: count(agent.total, locale) })}</span>}
        {agent.status === 'needs-install-update' && <><span className="skills-readiness-count">{t('setup.skillsReadinessBreakdown', { current: count(agent.current, locale), outdated: count(agent.outdated, locale), missing: count(agent.missing, locale) })}</span><button className="button compact" type="button" disabled={mutationBusy || loading} onClick={event => void prepare(agent.agent, event.currentTarget)}>{t('setup.skillsManagementCta')}</button></>}
      </div>
    </article>)}</div>}
    {mutationError && <p className="skills-management-error" role="alert">{mutationError}</p>}
    {mutationResult && <div className={`skills-management-result ${mutationResult.status}`} role={mutationResult.status === 'failed-unverified' ? 'alert' : 'status'} aria-live="polite"><strong>{resultLabel(mutationResult.status, t)}</strong>{resultDetail(mutationResult, t) && <span>{resultDetail(mutationResult, t)}</span>}</div>}
    {preview && (mutationPhase === 'ready' || mutationPhase === 'executing') && <SkillsManagementDialog preview={preview} busy={mutationPhase === 'executing'} onCancel={closePreview} onConfirm={() => void confirm()} />}
    {activeAgent && mutationPhase === 'previewing' && <div className="skills-management-progress" role="status" aria-live="polite"><RefreshCw size={15} className="spin" aria-hidden="true" />{t('setup.skillsManagementPreparing', { agent: agentLabels[activeAgent] })}</div>}
  </section>
}

function count(value: number, locale: 'ko' | 'en') {
  return new Intl.NumberFormat(locale === 'ko' ? 'ko-KR' : 'en-US').format(value)
}

export default SkillsReadiness
