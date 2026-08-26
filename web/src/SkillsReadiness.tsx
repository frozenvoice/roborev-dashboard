import { CircleAlert, RefreshCw } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { normalizeSkillsReadiness, request, type SkillsReadiness, type SkillsReadinessAgent, type SkillsReadinessStatus } from './api'
import { useI18n } from './i18n'

type SkillsResourceState =
  | { status: 'success'; value: SkillsReadiness }
  | { status: 'failure' }

type SkillsResource = {
  generation: number
  controller: AbortController
  promise: Promise<SkillsReadiness>
}

// Skills status is a global read-only resource. Keep the settled value and an
// unresolved request outside the component so StrictMode and repository-key
// remounts do not turn a global read into duplicate process calls.
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

function count(value: number, locale: 'ko' | 'en') {
  return new Intl.NumberFormat(locale === 'ko' ? 'ko-KR' : 'en-US').format(value)
}

export function SkillsReadiness() {
  const { t, locale } = useI18n()
  const [view, setView] = useState<SkillsResourceState | 'loading'>('loading')
  const mounted = useRef(false)
  const localGeneration = useRef(0)

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
    }
  }, [])

  const loading = view === 'loading'
  const agents = view !== 'loading' && view.status === 'success' ? view.value.agents : []
  return <section className="skills-readiness" aria-label={t('setup.skillsReadinessTitle')} aria-busy={loading}>
    <header className="setup-subheading">
      <div><h2>{t('setup.skillsReadinessTitle')}</h2><p>{t('setup.skillsReadinessDescription')}</p></div>
      <button className="button ghost compact" aria-label={t('setup.skillsReadinessRefresh')} onClick={() => load(true)}><RefreshCw size={14} className={loading ? 'spin' : ''} />{t('common.refresh')}</button>
    </header>
    {loading && <div className="skills-readiness-state" role="status" aria-live="polite"><RefreshCw size={17} className="spin" aria-hidden="true" />{t('setup.skillsReadinessLoading')}</div>}
    {view !== 'loading' && view.status === 'failure' && <div className="skills-readiness-state skills-readiness-error" role="alert"><CircleAlert size={17} aria-hidden="true" /><div><strong>{t('setup.skillsReadinessUnavailable')}</strong><p>{t('setup.skillsReadinessRetry')}</p></div></div>}
    {agents.length > 0 && <div className="skills-readiness-list" role="list">{agents.map(agent => <article className="skills-readiness-row" role="listitem" key={agent.agent}>
      <div className="skills-readiness-agent"><h3>{agentLabels[agent.agent]}</h3><code>{agent.agent}</code></div>
      <div className="skills-readiness-row-detail"><span className={`status ${statusClass(agent.status)}`} role="status"><span className="status-dot" />{statusLabel(agent.status, t)}</span>
        {agent.status === 'current' && <span className="skills-readiness-count">{t('setup.skillsReadinessCurrentCount', { current: count(agent.current, locale), total: count(agent.total, locale) })}</span>}
        {agent.status === 'needs-install-update' && <span className="skills-readiness-count">{t('setup.skillsReadinessBreakdown', { current: count(agent.current, locale), outdated: count(agent.outdated, locale), missing: count(agent.missing, locale) })}</span>}
      </div>
    </article>)}</div>}
  </section>
}

export default SkillsReadiness
