import { CircleAlert, RefreshCw } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { normalizeRuntimeDiagnostics, request, type RuntimeDiagnostics, type RuntimeDiagnosticsComponent } from './api'
import { useI18n } from './i18n'

type DiagnosticsPhase = 'unloaded' | 'loading' | 'loaded' | 'error'

function StatusPill({ healthy, good, tone, label }: { healthy: boolean; good?: boolean; tone?: 'queued'; label: string }) {
  const positive = good ?? healthy
  return <span className={`status ${tone === 'queued' ? 'status-queued' : positive ? 'status-pass' : 'status-failed'}`}><span className="status-dot" />{label}</span>
}

function CountRow({ label, value }: { label: string; value: number }) {
  return <div className="runtime-diagnostics-row"><dt>{label}</dt><dd>{value.toLocaleString()}</dd></div>
}

function ComponentRow({ component, healthyLabel, unhealthyLabel }: { component: RuntimeDiagnosticsComponent; healthyLabel: string; unhealthyLabel: string }) {
  return <li className="runtime-diagnostics-component"><span>{component.name}</span><StatusPill healthy={component.healthy} label={component.healthy ? healthyLabel : unhealthyLabel} /></li>
}

export function RuntimeDiagnostics() {
  const { t } = useI18n()
  const [expanded, setExpanded] = useState(false)
  const [phase, setPhase] = useState<DiagnosticsPhase>('unloaded')
  const [data, setData] = useState<RuntimeDiagnostics | null>(null)
  const generationRef = useRef(0)
  const controllerRef = useRef<AbortController | null>(null)
  const mountedRef = useRef(true)

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      generationRef.current += 1
      controllerRef.current?.abort()
      controllerRef.current = null
    }
  }, [])

  const load = () => {
    if (!mountedRef.current) return
    controllerRef.current?.abort()
    const controller = new AbortController()
    controllerRef.current = controller
    const generation = ++generationRef.current
    setPhase('loading')
    void request<unknown>('/api/runtime/diagnostics', { signal: controller.signal })
      .then(value => {
        const normalized = normalizeRuntimeDiagnostics(value)
        if (!normalized) throw new Error('invalid-runtime-diagnostics')
        if (!mountedRef.current || generationRef.current !== generation) return
        setData(normalized)
        setPhase('loaded')
      })
      .catch(() => {
        if (!mountedRef.current || generationRef.current !== generation) return
        setPhase('error')
      })
      .finally(() => {
        if (mountedRef.current && generationRef.current === generation && controllerRef.current === controller) controllerRef.current = null
      })
  }

  const toggle = () => {
    if (expanded) {
      setExpanded(false)
      return
    }
    setExpanded(true)
    if (phase === 'unloaded') load()
  }

  const diagnosticsId = 'runtime-diagnostics-content'
  return <section className="runtime-diagnostics" aria-labelledby="runtime-diagnostics-title">
    <header className="runtime-diagnostics-heading">
      <div>
        <h2 id="runtime-diagnostics-title">{t('diagnostics.title')}</h2>
        <p>{t('diagnostics.description')}</p>
      </div>
      <div className="runtime-diagnostics-actions">
        <button className="button ghost compact" aria-expanded={expanded} aria-controls={diagnosticsId} onClick={toggle}>{expanded ? t('diagnostics.collapse') : t('diagnostics.open')}</button>
        {expanded && phase !== 'unloaded' && <button className="button ghost compact" onClick={load} aria-label={t('common.refresh')}><RefreshCw size={14} className={phase === 'loading' ? 'spin' : ''} />{t('common.refresh')}</button>}
      </div>
    </header>
    {expanded && <div id={diagnosticsId} className="runtime-diagnostics-content" aria-busy={phase === 'loading'} aria-live="polite">
      {phase === 'loading' && <div className="runtime-diagnostics-state"><RefreshCw size={18} className="spin" aria-hidden="true" /><span>{t('diagnostics.loading')}</span></div>}
      {phase === 'error' && <div className="runtime-diagnostics-state runtime-diagnostics-error" role="alert"><CircleAlert size={18} aria-hidden="true" /><div><strong>{t('diagnostics.unavailable')}</strong><p>{t('diagnostics.unavailableBody')}</p><button className="button compact" onClick={load}>{t('diagnostics.retry')}</button></div></div>}
      {phase === 'loaded' && data && <div className="runtime-diagnostics-grid">
        <section className="runtime-diagnostics-group" aria-labelledby="runtime-diagnostics-daemon">
          <h3 id="runtime-diagnostics-daemon">{t('diagnostics.daemon')}</h3>
          <div className="runtime-diagnostics-status"><StatusPill healthy={data.daemon.running} label={data.daemon.running ? t('diagnostics.running') : t('diagnostics.offline')} /></div>
          <dl className="runtime-diagnostics-list"><div className="runtime-diagnostics-row"><dt>{t('diagnostics.version')}</dt><dd>{data.daemon.version}</dd></div><div className="runtime-diagnostics-row"><dt>{t('diagnostics.uptime')}</dt><dd>{data.daemon.uptime}</dd></div></dl>
        </section>
        <section className="runtime-diagnostics-group" aria-labelledby="runtime-diagnostics-workers">
          <h3 id="runtime-diagnostics-workers">{t('diagnostics.workers')}</h3>
          <dl className="runtime-diagnostics-list"><div className="runtime-diagnostics-row"><dt>{t('diagnostics.activeMax')}</dt><dd>{data.workers.active} / {data.workers.max}</dd></div></dl>
        </section>
        <section className="runtime-diagnostics-group" aria-labelledby="runtime-diagnostics-queue">
          <h3 id="runtime-diagnostics-queue">{t('diagnostics.queue')}</h3>
          <div className="runtime-diagnostics-status"><StatusPill healthy={!data.queue.paused} good={!data.queue.paused} tone={data.queue.paused ? 'queued' : undefined} label={data.queue.paused ? t('diagnostics.paused') : t('diagnostics.processing')} /></div>
          <dl className="runtime-diagnostics-list"><CountRow label={t('diagnostics.queued')} value={data.queue.queued} /><CountRow label={t('diagnostics.runningJobs')} value={data.queue.running} /></dl>
        </section>
        <section className="runtime-diagnostics-group" aria-labelledby="runtime-diagnostics-jobs">
          <h3 id="runtime-diagnostics-jobs">{t('diagnostics.jobs')}</h3>
          <dl className="runtime-diagnostics-list"><CountRow label={t('diagnostics.completed')} value={data.jobs.completed} /><CountRow label={t('diagnostics.failed')} value={data.jobs.failed} /><CountRow label={t('diagnostics.skipped')} value={data.jobs.skipped} /></dl>
        </section>
        <section className="runtime-diagnostics-group runtime-diagnostics-health" aria-labelledby="runtime-diagnostics-health">
          <h3 id="runtime-diagnostics-health">{t('diagnostics.health')}</h3>
          <div className="runtime-diagnostics-status"><StatusPill healthy={data.health.healthy} label={data.health.healthy ? t('diagnostics.healthy') : t('diagnostics.degraded')} /></div>
          <dl className="runtime-diagnostics-list"><CountRow label={t('diagnostics.errors24h')} value={data.health.errorCount24h} /></dl>
          <div className="runtime-diagnostics-components"><strong>{t('diagnostics.components')}</strong><ul>{data.health.components.map(component => <ComponentRow key={component.name} component={component} healthyLabel={t('diagnostics.componentHealthy')} unhealthyLabel={t('diagnostics.componentUnhealthy')} />)}</ul></div>
        </section>
      </div>}
    </div>}
  </section>
}

export default RuntimeDiagnostics
