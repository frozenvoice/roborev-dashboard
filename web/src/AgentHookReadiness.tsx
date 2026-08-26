import { RefreshCw, ShieldAlert } from 'lucide-react'
import { normalizeAgentHookReadiness, request, type AgentHookReadiness, type AgentHookReadinessAgent, type AgentHookReadinessStatus } from './api'
import { useI18n } from './i18n'
import { useEffect, useRef, useState } from 'react'

type ReadinessResourceState =
  | { status: 'success'; value: AgentHookReadiness }
  | { status: 'failure' }

type ReadinessResource = {
  generation: number
  controller: AbortController
  promise: Promise<AgentHookReadiness>
}

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

export function AgentHookReadiness() {
  const { t } = useI18n()
  const [view, setView] = useState<ReadinessResourceState | 'loading'>('loading')
  const mounted = useRef(false)
  const localGeneration = useRef(0)

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
    return () => { mounted.current = false }
  }, [])

  const loading = view === 'loading'
  return <section className="agent-hook-readiness" aria-label={t('setup.agentHookReadinessTitle')}>
    <header className="setup-subheading">
      <div><h2>{t('setup.agentHookReadinessTitle')}</h2><p>{t('setup.agentHookReadinessDescription')}</p></div>
      <button className="button ghost compact" aria-label={t('setup.agentHookReadinessRefresh')} onClick={() => load(true)}><RefreshCw size={14} className={loading ? 'spin' : ''} />{t('common.refresh')}</button>
    </header>
    {loading && <div className="agent-hook-readiness-state" role="status" aria-live="polite"><RefreshCw size={17} className="spin" />{t('setup.agentHookReadinessLoading')}</div>}
    {view !== 'loading' && view.status === 'failure' && <div className="agent-hook-readiness-state agent-hook-readiness-error" role="alert"><ShieldAlert size={17} /><div><strong>{t('setup.agentHookReadinessUnavailable')}</strong><p>{t('setup.agentHookReadinessRetry')}</p></div></div>}
    {view !== 'loading' && view.status === 'success' && <div className="agent-hook-readiness-list" role="list">{view.value.agents.map(agent => <article className="agent-hook-readiness-row" role="listitem" key={agent.agent}><div><h3>{agentLabels[agent.agent]}</h3><code>{agent.agent}</code></div><span className={`status ${statusClass(agent.status)}`} role="status"><span className="status-dot" />{statusLabel(agent.status, t)}</span></article>)}</div>}
  </section>
}
