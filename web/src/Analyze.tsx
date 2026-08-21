import { useEffect, useRef, useState } from 'react'
import { Check, Clipboard, Eye, ShieldAlert } from 'lucide-react'
import { normalizeAnalysisPrompt, normalizeAnalysisTypesResponse, request, type AnalysisPrompt, type AnalysisType } from './api'
import { useI18n } from './i18n'

export type AnalyzeProps = {
  /** Used only to invalidate an in-flight prompt when the selected repository changes. */
  repoIdentity: string
}

type CopyState = 'idle' | 'copying' | 'success' | 'error'

export function Analyze({ repoIdentity }: AnalyzeProps) {
  const { t } = useI18n()
  const translateRef = useRef(t)
  translateRef.current = t
  const [types, setTypes] = useState<AnalysisType[]>([])
  const [selectedType, setSelectedType] = useState('')
  const [catalogLoading, setCatalogLoading] = useState(false)
  const [catalogError, setCatalogError] = useState('')
  const [prompt, setPrompt] = useState<AnalysisPrompt | null>(null)
  const [promptLoading, setPromptLoading] = useState(false)
  const [promptError, setPromptError] = useState('')
  const [copyState, setCopyState] = useState<CopyState>('idle')
  const catalogGeneration = useRef(0)
  const promptGeneration = useRef(0)
  const copyGeneration = useRef(0)

  // The catalog is global to the installed RoboRev version, not a repository
  // resource. It is intentionally fetched once per Analyze mount.
  useEffect(() => {
    const generation = ++catalogGeneration.current
    setCatalogLoading(true)
    setCatalogError('')
    void request<unknown>('/api/analyses/types').then(value => {
      if (generation !== catalogGeneration.current) return
      const next = normalizeAnalysisTypesResponse(value)
      if (!next) throw new Error(translateRef.current('error.analyzeCatalogMalformed'))
      setTypes(next)
      setSelectedType(current => current && next.some(type => type.name === current) ? current : next[0]?.name ?? '')
    }).catch(error => {
      if (generation === catalogGeneration.current) setCatalogError((error as Error).message)
    }).finally(() => {
      if (generation === catalogGeneration.current) setCatalogLoading(false)
    })
    return () => { catalogGeneration.current++ }
  }, [])

  // Prompt text is tied to the repository the user was inspecting. A switch
  // must clear it and invalidate the old response without reloading catalog.
  useEffect(() => {
    promptGeneration.current++
    copyGeneration.current++
    setPrompt(null)
    setPromptError('')
    setPromptLoading(false)
    setCopyState('idle')
    return () => { promptGeneration.current++ }
  }, [repoIdentity])

  const selected = types.find(type => type.name === selectedType) ?? null

  const selectType = (value: string) => {
    promptGeneration.current++
    copyGeneration.current++
    setSelectedType(value)
    setPrompt(null)
    setPromptError('')
    setPromptLoading(false)
    setCopyState('idle')
  }

  const loadPrompt = async () => {
    if (!selectedType || promptLoading) return
    const generation = ++promptGeneration.current
    const expectedType = selectedType
    setPrompt(null)
    setPromptError('')
    setPromptLoading(true)
    copyGeneration.current++
    setCopyState('idle')
    try {
      const next = normalizeAnalysisPrompt(await request<unknown>(`/api/analyses/prompt?type=${encodeURIComponent(expectedType)}`), expectedType)
      if (!next) throw new Error(translateRef.current('error.analyzePromptMalformed'))
      if (generation === promptGeneration.current) setPrompt(next)
    } catch (error) {
      if (generation === promptGeneration.current) setPromptError((error as Error).message)
    } finally {
      if (generation === promptGeneration.current) setPromptLoading(false)
    }
  }

  const copyPrompt = async () => {
    if (!prompt || copyState === 'copying') return
    const generation = ++copyGeneration.current
    setCopyState('copying')
    try {
      if (typeof navigator.clipboard?.writeText !== 'function') throw new Error('Clipboard unavailable')
      await navigator.clipboard.writeText(prompt.prompt)
      if (generation === copyGeneration.current) setCopyState('success')
    } catch {
      if (generation === copyGeneration.current) setCopyState('error')
    }
  }

  const closePrompt = () => {
    copyGeneration.current++
    setCopyState('idle')
    setPrompt(null)
  }

  return <section className="analyze-page" aria-label={t('analyze.aria')}>
    <header className="section-heading analyze-heading"><div><h1>{t('analyze.title')}</h1><p>{t('analyze.description')}</p></div></header>
    <div className="analyze-grid">
      <section className="analyze-workflow" aria-labelledby="analyze-type-heading">
        <div className="analyze-section-heading"><div><h2 id="analyze-type-heading">{t('analyze.type')}</h2><p>{t('analyze.typeDescription')}</p></div></div>
        {catalogLoading && <div className="skeleton" aria-label={t('common.loading')} />}
        {catalogError && <p role="alert" className="analyze-error">{catalogError}</p>}
        {!catalogLoading && !catalogError && <label className="analyze-select-label" htmlFor="analysis-type"><span>{t('analyze.type')}</span><select id="analysis-type" aria-label={t('analyze.type')} value={selectedType} onChange={event => selectType(event.target.value)} disabled={!types.length}>{types.map(type => <option value={type.name} key={type.name}>{type.name}</option>)}</select></label>}
        {selected && <p className="analyze-source-text">{selected.description}</p>}
        <div className="analyze-inline-actions"><button type="button" className="button" onClick={() => void loadPrompt()} disabled={!selectedType || promptLoading || catalogLoading}><Eye size={15} />{promptLoading ? t('analyze.loadingPrompt') : t('analyze.showPrompt')}</button></div>
        {promptError && <p role="alert" className="analyze-error">{t('analyze.promptFailed', { error: promptError })}</p>}
        {prompt && prompt.analysisType === selectedType && <section className="analyze-prompt" aria-live="polite"><header><div><span>{prompt.analysisType}</span><h2>{t('analyze.promptTitle')}</h2></div><div className="analyze-prompt-actions"><button type="button" className="button ghost compact" onClick={() => void copyPrompt()} disabled={copyState === 'copying'}><Clipboard size={14} />{copyState === 'copying' ? t('analyze.copyingPrompt') : t('analyze.copyPrompt')}</button><button type="button" className="button ghost compact" onClick={closePrompt}>{t('common.close')}</button></div></header>{copyState !== 'idle' && <p className={`analyze-copy-feedback ${copyState === 'success' ? 'success' : copyState === 'error' ? 'error' : ''}`} role="status" aria-live="polite">{copyState === 'copying' ? t('analyze.copyingPrompt') : copyState === 'success' ? <><Check size={14} aria-hidden="true" />{t('analyze.promptCopied')}</> : t('analyze.promptCopyFailed')}</p>}<pre>{prompt.prompt}</pre></section>}
      </section>
      <aside className="analyze-guidance" aria-label={t('analyze.safety')}>
        <div className="analyze-guidance-icon"><ShieldAlert size={19} /></div>
        <h2>{t('analyze.safetyTitle')}</h2>
        <p>{t('analyze.agenticWarning')}</p>
        <p>{t('analyze.executionDisabled')}</p>
        <div className="analyze-execution-disabled">
          <strong>{t('analyze.executionDisabledTitle')}</strong>
          <button type="button" className="button primary" disabled aria-describedby="analyze-execution-disabled-reason">{t('analyze.executionDisabledCta')}</button>
          <p id="analyze-execution-disabled-reason">{t('analyze.executionDisabledReason')}</p>
        </div>
      </aside>
    </div>
  </section>
}
