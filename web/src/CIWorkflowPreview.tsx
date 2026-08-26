import { AlertTriangle, Check, Clipboard, RefreshCw } from 'lucide-react'
import { fetchCIWorkflowPreview, type CIWorkflowPreview as CIWorkflowPreviewData } from './api'
import { useI18n } from './i18n'
import { useEffect, useRef, useState } from 'react'

type CIWorkflowPreviewProps = { repositoryIdentity?: string }
type PreviewPhase = 'idle' | 'loading' | 'success' | 'error'
type CopyPhase = 'idle' | 'success' | 'error'

/**
 * A repository-scoped, read-only workflow preview. The component deliberately
 * has no effect-driven fetch: the only request is made by the Generate button.
 */
export function CIWorkflowPreview({ repositoryIdentity = '' }: CIWorkflowPreviewProps) {
  const { t } = useI18n()
  const [phase, setPhase] = useState<PreviewPhase>('idle')
  const [preview, setPreview] = useState<CIWorkflowPreviewData | null>(null)
  const [copyPhase, setCopyPhase] = useState<CopyPhase>('idle')
  const mountedRef = useRef(true)
  const generationRef = useRef(0)
  const inFlightRef = useRef(false)
  const controllerRef = useRef<AbortController | null>(null)

  useEffect(() => {
    const generation = ++generationRef.current
    controllerRef.current?.abort()
    controllerRef.current = null
    inFlightRef.current = false
    setPhase('idle')
    setPreview(null)
    setCopyPhase('idle')
    // The generation is intentionally captured only to make the lifecycle
    // contract explicit; request callbacks compare the ref before updating.
    void generation
  }, [repositoryIdentity])

  useEffect(() => {
    // React StrictMode replays effect setup/cleanup once on initial mount.
    // Restore the mounted guard during the replay so an otherwise harmless
    // development-only cycle cannot permanently disable Generate.
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      generationRef.current += 1
      controllerRef.current?.abort()
      controllerRef.current = null
      inFlightRef.current = false
    }
  }, [])

  const generate = async () => {
    const identity = repositoryIdentity
    if (!identity || inFlightRef.current || !mountedRef.current) return
    const generation = ++generationRef.current
    const controller = new AbortController()
    controllerRef.current = controller
    inFlightRef.current = true
    setPhase('loading')
    setPreview(null)
    setCopyPhase('idle')
    try {
      const result = await fetchCIWorkflowPreview(identity, controller.signal)
      if (!mountedRef.current || generation !== generationRef.current || controller.signal.aborted || result.repositoryIdentity !== repositoryIdentity) return
      setPreview(result)
      setPhase('success')
    } catch {
      if (!mountedRef.current || generation !== generationRef.current || controller.signal.aborted) return
      setPreview(null)
      setPhase('error')
    } finally {
      if (generation === generationRef.current) {
        inFlightRef.current = false
        if (controllerRef.current === controller) controllerRef.current = null
      }
    }
  }

  const copy = async () => {
    if (!preview || copyPhase === 'success') return
    const generation = generationRef.current
    const content = preview.workflow.content
    try {
      if (typeof navigator.clipboard?.writeText !== 'function') throw new Error('clipboard-unavailable')
      await navigator.clipboard.writeText(content)
      if (mountedRef.current && generation === generationRef.current) setCopyPhase('success')
    } catch {
      if (mountedRef.current && generation === generationRef.current) setCopyPhase('error')
    }
  }

  const visiblePreview = preview?.repositoryIdentity === repositoryIdentity ? preview : null
  const currentPhase = visiblePreview ? 'success' : phase
  return <section className="ci-workflow-preview" aria-labelledby="ci-workflow-preview-title">
    <header className="setup-subheading ci-workflow-preview-heading">
      <div><span className="eyebrow">{t('setup.ciWorkflowPreviewEyebrow')}</span><h2 id="ci-workflow-preview-title">{t('setup.ciWorkflowPreviewTitle')}</h2><p>{t('setup.ciWorkflowPreviewDescription')}</p></div>
      <button type="button" className="button ghost compact" disabled={!repositoryIdentity || currentPhase === 'loading'} onClick={() => void generate()}><RefreshCw size={14} className={currentPhase === 'loading' ? 'spin' : ''} />{currentPhase === 'loading' ? t('setup.ciWorkflowPreviewGenerating') : t('setup.ciWorkflowPreviewGenerate')}</button>
    </header>
    {currentPhase === 'idle' && repositoryIdentity && <p className="ci-workflow-preview-state">{t('setup.ciWorkflowPreviewIdle')}</p>}
    {currentPhase === 'loading' && <p className="ci-workflow-preview-state" role="status"><RefreshCw size={15} className="spin" />{t('setup.ciWorkflowPreviewGenerating')}</p>}
    {currentPhase === 'error' && <p className="ci-workflow-preview-error" role="alert"><AlertTriangle size={15} aria-hidden="true" />{t('setup.ciWorkflowPreviewError')}</p>}
    {visiblePreview && <div className="ci-workflow-preview-content">
      <dl className="ci-workflow-preview-facts"><div><dt>{t('setup.ciWorkflowPreviewTarget')}</dt><dd><code>{visiblePreview.target}</code></dd></div><div><dt>{t('setup.ciWorkflowPreviewVersion')}</dt><dd><code>{visiblePreview.version}</code></dd></div></dl>
      <p className="ci-workflow-preview-warning" role="note"><AlertTriangle size={15} aria-hidden="true" /><span>{t('setup.ciWorkflowPreviewSupplyChainWarning')}</span></p>
      <div className="ci-workflow-preview-toolbar"><strong>{t('setup.ciWorkflowPreviewYaml')}</strong><button type="button" className="button ghost compact" disabled={copyPhase === 'success'} onClick={() => void copy()}><Clipboard size={14} />{copyPhase === 'success' ? <><Check size={14} aria-hidden="true" />{t('setup.ciWorkflowPreviewCopied')}</> : t('setup.ciWorkflowPreviewCopy')}</button></div>
      {copyPhase === 'error' && <p className="ci-workflow-preview-copy-error" role="status">{t('setup.ciWorkflowPreviewCopyFailed')}</p>}
      <pre className="ci-workflow-preview-yaml"><code>{visiblePreview.workflow.content}</code></pre>
    </div>}
    {!repositoryIdentity && <p className="ci-workflow-preview-state">{t('setup.ciWorkflowPreviewNoRepository')}</p>}
  </section>
}

export type { CIWorkflowPreviewProps }
