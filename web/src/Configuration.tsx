import { useCallback, useEffect, useRef, useState } from 'react'
import { RefreshCw, ShieldAlert } from 'lucide-react'
import { normalizeConfigurationPreview, normalizeConfigurationSetResult, normalizeConfigurationSnapshot, request, type ConfigurationPreview, type ConfigurationRow, type ConfigurationSettingSpec, type ConfigurationSnapshot, type ConfigScope } from './api'
import { useI18n, type MessageKey } from './i18n'

type Draft = { scope: ConfigScope; value: boolean | number | string }

const configKey = (suffix: string) => `config.${suffix}` as MessageKey

export type ConfigurationProps = {
  repositoryIdentity: string
  configEnabled: boolean
  busy?: boolean
  onToast: (kind: 'success' | 'error' | 'warning', message: string) => void
}

function severityOptions(values: string[] | undefined) {
  return values ?? []
}

/**
 * Configuration shows the RoboRev CLI effective config (source of truth) and
 * edits only the backend allowlisted settings. Raw values — including
 * upstream-masked sensitive rows — are display-only. The daemon is never
 * started or required: config list/set are filesystem/CLI operations.
 */
export function Configuration({ repositoryIdentity, configEnabled, onToast }: ConfigurationProps) {
  const { t } = useI18n()
  const [snapshot, setSnapshot] = useState<ConfigurationSnapshot | null>(null)
  const [loadError, setLoadError] = useState('')
  const [loading, setLoading] = useState(false)
  const [drafts, setDrafts] = useState<Record<string, Draft>>({})
  const [preview, setPreview] = useState<ConfigurationPreview | null>(null)
  const [preparingKey, setPreparingKey] = useState<string | null>(null)
  const [confirming, setConfirming] = useState(false)
  // A generation counter discards stale snapshot responses after a repository
  // switch so repo A data can never land in repo B's UI.
  const generation = useRef(0)
  const refreshEpoch = useRef(0)

  useEffect(() => {
    if (!configEnabled || !repositoryIdentity) return
    const current = ++generation.current
    setLoading(true)
    request<unknown>(`/api/configuration?repositoryIdentity=${encodeURIComponent(repositoryIdentity)}`)
      .then(value => {
        if (current !== generation.current) return
        const next = normalizeConfigurationSnapshot(value)
        if (!next) throw new Error(t('config.malformed'))
        setSnapshot(next)
        setLoadError('')
      })
      .catch(error => {
        if (current !== generation.current) return
        setSnapshot(null)
        setLoadError((error as Error).message)
      })
      .finally(() => {
        if (current !== generation.current) return
        setLoading(false)
      })
    return () => { generation.current++ }
  }, [configEnabled, repositoryIdentity, refreshEpoch.current, t])

  const reload = useCallback(() => { refreshEpoch.current++ }, [])

  const rowFor = useCallback((key: string): ConfigurationRow | undefined => snapshot?.rows.find(row => row.key === key), [snapshot])
  const defaultDraft = useCallback((spec: ConfigurationSettingSpec): Draft => {
    const raw = rowFor(spec.key)?.value ?? ''
    let value: boolean | number | string
    if (spec.valueType === 'boolean') value = raw === 'true'
    else if (spec.valueType === 'integer') value = raw === '' ? '' : Number(raw)
    else value = raw
    return { scope: spec.scopes[spec.scopes.length - 1], value }
  }, [rowFor])
  const draftFor = useCallback((spec: ConfigurationSettingSpec): Draft => drafts[spec.key] ?? defaultDraft(spec), [drafts, defaultDraft])

  const updateDraft = (spec: ConfigurationSettingSpec, patch: Partial<Draft>) => {
    // Editing any field invalidates an open preview: it no longer matches.
    setPreview(null)
    // A partial edit must never drop the scope default.
    setDrafts(old => ({ ...old, [spec.key]: { ...defaultDraft(spec), ...old[spec.key], ...patch } }))
  }

  const draftValid = (spec: ConfigurationSettingSpec, draft: Draft): boolean => {
    if (typeof draft.value !== spec.valueType && !(spec.valueType === 'integer')) {
      if (spec.valueType === 'boolean' && typeof draft.value !== 'boolean') return false
      if (spec.valueType === 'enum' && typeof draft.value !== 'string') return false
    }
    if (spec.valueType === 'integer') {
      if (typeof draft.value !== 'number' || !Number.isSafeInteger(draft.value)) return false
      if (spec.min !== undefined && draft.value < spec.min) return false
      if (spec.max !== undefined && draft.value > spec.max) return false
    }
    if (spec.valueType === 'enum' && spec.enumValues && !severityOptions(spec.enumValues).includes(String(draft.value))) return false
    return true
  }

  const prepareChange = async (spec: ConfigurationSettingSpec) => {
    if (preparingKey) return
    const draft = draftFor(spec)
    if (!draftValid(spec, draft)) return
    setPreparingKey(spec.key)
    try {
      const value = await request<unknown>('/api/configuration/preview', { method: 'POST', body: JSON.stringify({ repositoryIdentity, key: spec.key, scope: draft.scope, value: draft.value }) })
      const next = normalizeConfigurationPreview(value)
      if (!next) throw new Error(t('config.malformed'))
      setPreview(next)
    } catch (error) {
      onToast('error', (error as Error).message)
    } finally {
      setPreparingKey(null)
    }
  }

  const confirmChange = async () => {
    if (!preview || confirming) return
    // Single-flight: a double click must produce exactly one POST.
    setConfirming(true)
    try {
      const result = normalizeConfigurationSetResult(await request<unknown>('/api/configuration/set', { method: 'POST', body: JSON.stringify({ previewToken: preview.previewToken }) }))
      if (!result) throw new Error(t('config.malformed'))
      setPreview(null)
      if (result.status === 'unverified') {
        onToast('warning', t('config.unverified'))
      } else {
        onToast('success', result.repositoryDirty ? t('config.dirtyNote') : t('config.changed'))
      }
      reload()
    } catch (error) {
      onToast('error', (error as Error).message)
      setPreview(null)
      reload()
    } finally {
      setConfirming(false)
    }
  }

  const originBadge = (origin: string) => <span className={`badge origin-${origin}`}>{t(configKey(`origin.${origin}`))}</span>

  return <section className="configuration-page">
    <header className="section-heading"><div><span className="eyebrow">{t('nav.configuration')}</span><h1>{t('nav.configuration')}</h1><p>{t('config.description')}</p></div><button className="button ghost compact" disabled={loading} onClick={reload}><RefreshCw size={15} />{t('config.refresh')}</button></header>
    {!configEnabled && <p role="note" className="hint"><ShieldAlert size={14} />{t('config.unavailable')}</p>}
    {loadError && <p role="alert" className="hint">{t('config.loadFailed', { error: loadError })}</p>}
    {loading && <div className="skeleton tall" />}
    {snapshot && <>
      <section aria-labelledby="config-effective-title" className="card">
        <h2 id="config-effective-title">{t('config.effectiveTitle')}</h2>
        <p className="hint">{t('config.effectiveHint')}</p>
        <div className="table-wrap"><table data-testid="effective-config"><thead><tr><th>{t('config.columnKey')}</th><th>{t('config.columnValue')}</th><th>{t('config.origin')}</th></tr></thead><tbody>
          {snapshot.rows.map(row => <tr key={row.key}><td><code>{row.key}</code></td><td data-testid={`value-${row.key}`}>{row.value === '' ? '—' : row.value}</td><td>{originBadge(row.origin)}</td></tr>)}
          {snapshot.rows.length === 0 && <tr><td colSpan={3}>{t('config.noRows')}</td></tr>}
        </tbody></table></div>
      </section>
      <section aria-labelledby="config-editable-title" className="card">
        <h2 id="config-editable-title">{t('config.editable')}</h2>
        <div className="settings-grid">
          {snapshot.settings.map(spec => {
            const draft = draftFor(spec)
            const row = rowFor(spec.key)
            const valid = draftValid(spec, draft)
            return <article className="setting-card" key={spec.key} data-testid={`setting-${spec.key}`}>
              <header><strong>{t(configKey(`setting.${spec.key}`))}</strong>{row && originBadge(row.origin)}{!row && <span className="badge origin-default">{t('config.origin.default')}</span>}</header>
              <dl><dt>{t('config.currentValue')}</dt><dd>{row ? `${row.value === '' ? t('config.severityDisabled') : row.value}` : t('config.unset')}</dd></dl>
              <fieldset className="scope-fieldset">
                <legend>{t('config.scope')}</legend>
                {(['global', 'local'] as ConfigScope[]).map(scope => <label key={scope}>
                  <input type="radio" name={`scope-${spec.key}`} checked={draft.scope === scope} disabled={!spec.scopes.includes(scope)} onChange={() => updateDraft(spec, { scope })} />
                  {t(configKey(`scope.${scope}`))}
                </label>)}
              </fieldset>
              {spec.valueType === 'integer' && <input aria-label={t('config.newValue')} type="number" min={spec.min} max={spec.max} step={1} value={String(draft.value)} onChange={event => updateDraft(spec, { value: event.target.value === '' ? '' : Number(event.target.value) })} />}
              {spec.valueType === 'boolean' && <select aria-label={t('config.newValue')} value={String(draft.value)} onChange={event => updateDraft(spec, { value: event.target.value === 'true' })}>
                <option value="true">true</option>
                <option value="false">false</option>
              </select>}
              {spec.valueType === 'enum' && <select aria-label={t('config.newValue')} value={String(draft.value)} onChange={event => updateDraft(spec, { value: event.target.value })}>
                {(spec.enumValues ?? []).map(option => <option key={option} value={option}>{option === '' ? t('config.severityDisabled') : option}</option>)}
              </select>}
              {!valid && <p role="note" className="hint">{t('config.invalidValue')}</p>}
              <button className="button" disabled={!valid || preparingKey !== null || preparingKey === spec.key} onClick={() => void prepareChange(spec)}>{t('config.prepare')}</button>
            </article>
          })}
        </div>
      </section>
    </>}
    {preview && <div className="prompt-overlay"><section className="prompt-dialog" role="dialog" aria-modal="true" aria-labelledby="config-confirm-title">
      <header><div><span className="eyebrow">{t('config.confirmEyebrow')}</span><h2 id="config-confirm-title">{t('config.confirmTitle')}</h2></div></header>
      <dl className="confirm-facts">
        <dt>{t('config.settingLabel')}</dt><dd><code>{preview.key}</code></dd>
        <dt>{t('config.scope')}</dt><dd>{t(configKey(`scope.${preview.scope}`))}</dd>
        <dt>{t('config.currentValue')}</dt><dd>{preview.currentValue === '' ? t('config.severityDisabled') : preview.currentValue}{preview.currentOrigin ? ` · ${t(configKey(`origin.${preview.currentOrigin}`))}` : ''}</dd>
        <dt>{t('config.newValue')}</dt><dd>{preview.newValue === '' ? t('config.severityDisabled') : preview.newValue}</dd>
        <dt>{t('config.command')}</dt>
        <dd><div className="text-toolbar"><code data-testid="preview-command" className="long-text">{JSON.stringify([preview.command.executable, ...preview.command.args])}</code></div></dd>
      </dl>
      {preview.warnings.length > 0 && <ul className="warnings" role="note">{preview.warnings.map(warning => <li key={warning}>{t(configKey(`warning.${warning}`))}</li>)}</ul>}
      <footer>
        <button className="button" onClick={() => setPreview(null)}>{t('common.cancel')}</button>
        <button className="button primary" disabled={confirming} onClick={() => void confirmChange()}>{t('config.confirmAction')}</button>
      </footer>
    </section></div>}
  </section>
}
