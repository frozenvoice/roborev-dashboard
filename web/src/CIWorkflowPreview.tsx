import { AlertTriangle, Check, Clipboard, RefreshCw, X } from 'lucide-react'
import { executeCIWorkflow, fetchCIWorkflowPreview, type CIWorkflowExecuteResult, type CIWorkflowPreview as CIWorkflowPreviewData } from './api'
import { useI18n } from './i18n'
import { useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from 'react'

type CIWorkflowPreviewProps = { repositoryIdentity?: string }
type PreviewPhase = 'idle' | 'loading' | 'success' | 'error'
type CopyPhase = 'idle' | 'success' | 'error'
type ExecutePhase = 'idle' | 'executing'

/** A repository-scoped workflow preview and create-only management surface. */
export function CIWorkflowPreview({ repositoryIdentity = '' }: CIWorkflowPreviewProps) {
  const { t } = useI18n()
  const [phase, setPhase] = useState<PreviewPhase>('idle')
  const [preview, setPreview] = useState<CIWorkflowPreviewData | null>(null)
  const [copyPhase, setCopyPhase] = useState<CopyPhase>('idle')
  const [dialogOpen, setDialogOpen] = useState(false)
  const [executePhase, setExecutePhase] = useState<ExecutePhase>('idle')
  const [executeResult, setExecuteResult] = useState<CIWorkflowExecuteResult | null>(null)
  const [writeConsumed, setWriteConsumed] = useState(false)
  const mountedRef = useRef(true)
  const generationRef = useRef(0)
  const inFlightRef = useRef(false)
  const confirmingRef = useRef(false)
  const controllerRef = useRef<AbortController | null>(null)

  useEffect(() => {
    const generation = ++generationRef.current
    controllerRef.current?.abort()
    controllerRef.current = null
    inFlightRef.current = false
    confirmingRef.current = false
    setPhase('idle')
    setPreview(null)
    setCopyPhase('idle')
    setDialogOpen(false)
    setExecutePhase('idle')
    setExecuteResult(null)
    setWriteConsumed(false)
    void generation
  }, [repositoryIdentity])

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      generationRef.current += 1
      controllerRef.current?.abort()
      controllerRef.current = null
      inFlightRef.current = false
      confirmingRef.current = false
    }
  }, [])

  const generate = async () => {
    const identity = repositoryIdentity
    if (!identity || inFlightRef.current || executePhase === 'executing' || !mountedRef.current) return
    const generation = ++generationRef.current
    const controller = new AbortController()
    controllerRef.current = controller
    inFlightRef.current = true
    setPhase('loading')
    setPreview(null)
    setCopyPhase('idle')
    setDialogOpen(false)
    setExecuteResult(null)
    setWriteConsumed(false)
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
    try {
      if (typeof navigator.clipboard?.writeText !== 'function') throw new Error('clipboard-unavailable')
      await navigator.clipboard.writeText(preview.workflow.content)
      if (mountedRef.current && generation === generationRef.current) setCopyPhase('success')
    } catch {
      if (mountedRef.current && generation === generationRef.current) setCopyPhase('error')
    }
  }

  const openCreateDialog = () => {
    if (!preview || preview.write.status !== 'available' || writeConsumed || executePhase !== 'idle' || confirmingRef.current) return
    setDialogOpen(true)
  }

  const cancelCreate = () => {
    if (!confirmingRef.current) setDialogOpen(false)
  }

  const confirmCreate = async () => {
    if (!preview || preview.write.status !== 'available' || writeConsumed || confirmingRef.current || executePhase !== 'idle' || !mountedRef.current) return
    const token = preview.write.token
    const generation = generationRef.current
    confirmingRef.current = true
    // Consume the browser-held authority before the request starts. There is
    // no retry CTA for an ambiguous or failed execute result. Drop the token
    // from the retained display model as well; the server remains responsible
    // for single-use enforcement.
    setWriteConsumed(true)
    setPreview(current => current && current.write.status === 'available' ? { ...current, write: { status: 'unsafe' } } : current)
    setDialogOpen(false)
    setExecuteResult(null)
    setExecutePhase('executing')
    try {
      const result = await executeCIWorkflow(token)
      if (!mountedRef.current || generation !== generationRef.current) return
      setExecuteResult(result)
    } catch {
      if (!mountedRef.current || generation !== generationRef.current) return
      setExecuteResult({ status: 'failed-unverified' })
    } finally {
      confirmingRef.current = false
      if (mountedRef.current && generation === generationRef.current) setExecutePhase('idle')
    }
  }

  const visiblePreview = preview?.repositoryIdentity === repositoryIdentity ? preview : null
  const currentPhase = visiblePreview ? 'success' : phase
  const writeStatus = visiblePreview && !writeConsumed ? visiblePreview.write.status : null
  const titleId = 'ci-workflow-preview-title'
  return <section className="ci-workflow-preview" aria-labelledby={titleId} aria-busy={currentPhase === 'loading' || executePhase === 'executing'}>
    <header className="setup-subheading ci-workflow-preview-heading">
      <div><span className="eyebrow">{t('setup.ciWorkflowPreviewEyebrow')}</span><h2 id={titleId}>{t('setup.ciWorkflowPreviewTitle')}</h2><p>{t('setup.ciWorkflowPreviewDescription')}</p></div>
      <button type="button" className="button ghost compact" disabled={!repositoryIdentity || currentPhase === 'loading' || executePhase === 'executing'} onClick={() => void generate()}><RefreshCw size={14} className={currentPhase === 'loading' ? 'spin' : ''} />{currentPhase === 'loading' ? t('setup.ciWorkflowPreviewGenerating') : t('setup.ciWorkflowPreviewGenerate')}</button>
    </header>
    {currentPhase === 'idle' && repositoryIdentity && <p className="ci-workflow-preview-state">{t('setup.ciWorkflowPreviewIdle')}</p>}
    {currentPhase === 'loading' && <p className="ci-workflow-preview-state" role="status"><RefreshCw size={15} className="spin" />{t('setup.ciWorkflowPreviewGenerating')}</p>}
    {currentPhase === 'error' && <p className="ci-workflow-preview-error" role="alert"><AlertTriangle size={15} aria-hidden="true" />{t('setup.ciWorkflowPreviewError')}</p>}
    {visiblePreview && <div className="ci-workflow-preview-content">
      <dl className="ci-workflow-preview-facts"><div><dt>{t('setup.ciWorkflowPreviewTarget')}</dt><dd><code>{visiblePreview.target}</code></dd></div><div><dt>{t('setup.ciWorkflowPreviewVersion')}</dt><dd><code>{visiblePreview.version}</code></dd></div></dl>
      <p className="ci-workflow-preview-warning" role="note"><AlertTriangle size={15} aria-hidden="true" /><span>{t('setup.ciWorkflowPreviewSupplyChainWarning')}</span></p>
      {writeStatus === 'available' && <div className="ci-workflow-preview-write available"><p>{t('setup.ciWorkflowCreateAvailable')}</p><button type="button" className="button primary" onClick={openCreateDialog}><Check size={14} />{t('setup.ciWorkflowCreate')}</button></div>}
      {writeStatus === 'exists' && <p className="ci-workflow-preview-write blocked" role="status">{t('setup.ciWorkflowAlreadyExists')}</p>}
      {writeStatus === 'ignored' && <p className="ci-workflow-preview-write blocked" role="status">{t('setup.ciWorkflowIgnored')}</p>}
      {writeStatus === 'dirty' && <p className="ci-workflow-preview-write blocked" role="status">{t('setup.ciWorkflowDirty')}</p>}
      {writeStatus === 'unsafe' && <p className="ci-workflow-preview-write blocked" role="status">{t('setup.ciWorkflowUnsafe')}</p>}
      {executePhase === 'executing' && <p className="ci-workflow-preview-state" role="status"><RefreshCw size={15} className="spin" />{t('setup.ciWorkflowCreating')}</p>}
      {executeResult?.status === 'verified-created' && <div className="ci-workflow-preview-result verified-success" role="status"><strong>{t('setup.ciWorkflowCreated')}</strong><span>{t('setup.ciWorkflowPreviewTarget')}: <code>{visiblePreview.target}</code></span><span>{t('setup.ciWorkflowCreatedNext')}</span></div>}
      {executeResult?.status === 'failed-unverified' && <p className="ci-workflow-preview-result failed-unverified" role="alert">{t('setup.ciWorkflowFailed')}</p>}
      <div className="ci-workflow-preview-toolbar"><strong>{t('setup.ciWorkflowPreviewYaml')}</strong><button type="button" className="button ghost compact" disabled={copyPhase === 'success'} onClick={() => void copy()}><Clipboard size={14} />{copyPhase === 'success' ? <><Check size={14} aria-hidden="true" />{t('setup.ciWorkflowPreviewCopied')}</> : t('setup.ciWorkflowPreviewCopy')}</button></div>
      {copyPhase === 'error' && <p className="ci-workflow-preview-copy-error" role="status">{t('setup.ciWorkflowPreviewCopyFailed')}</p>}
      <pre className="ci-workflow-preview-yaml"><code>{visiblePreview.workflow.content}</code></pre>
    </div>}
    {!repositoryIdentity && <p className="ci-workflow-preview-state">{t('setup.ciWorkflowPreviewNoRepository')}</p>}
    {dialogOpen && visiblePreview?.write.status === 'available' && !writeConsumed && <WorkflowCreateDialog preview={visiblePreview} onCancel={cancelCreate} onConfirm={() => void confirmCreate()} />}
  </section>
}

type WorkflowCreateDialogProps = { preview: CIWorkflowPreviewData; onCancel: () => void; onConfirm: () => void }
const dialogFocusableSelector = 'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

function WorkflowCreateDialog({ preview, onCancel, onConfirm }: WorkflowCreateDialogProps) {
  const { t } = useI18n()
  const dialogRef = useRef<HTMLElement | null>(null)
  const cancelRef = useRef<HTMLButtonElement | null>(null)
  useEffect(() => { cancelRef.current?.focus() }, [])
  const onDialogKeyDown = (event: ReactKeyboardEvent<HTMLElement>) => {
    if (event.key === 'Escape') { event.preventDefault(); onCancel(); return }
    if (event.key !== 'Tab') return
    const dialog = dialogRef.current
    if (!dialog) return
    const focusable = Array.from(dialog.querySelectorAll<HTMLElement>(dialogFocusableSelector)).filter(element => !element.hidden)
    if (focusable.length === 0) { event.preventDefault(); dialog.focus(); return }
    const active = document.activeElement
    const index = active instanceof HTMLElement ? focusable.indexOf(active) : -1
    if (event.shiftKey ? index <= 0 : index === focusable.length - 1 || index < 0) { event.preventDefault(); focusable[event.shiftKey ? focusable.length - 1 : 0].focus() }
  }
  return <div className="prompt-overlay ci-workflow-preview-overlay" role="presentation">
    <section className="prompt-dialog ci-workflow-preview-dialog" role="dialog" aria-modal="true" aria-labelledby="ci-workflow-confirm-title" aria-describedby="ci-workflow-confirm-description" tabIndex={-1} ref={dialogRef} onKeyDown={onDialogKeyDown}>
      <header><div><span className="eyebrow">{t('setup.ciWorkflowConfirmEyebrow')}</span><h2 id="ci-workflow-confirm-title">{t('setup.ciWorkflowConfirmTitle')}</h2><p id="ci-workflow-confirm-description">{t('setup.ciWorkflowConfirmDescription')}</p></div><button className="icon-button" type="button" aria-label={t('setup.ciWorkflowCloseConfirmation')} onClick={onCancel}><X /></button></header>
      <dl className="ci-workflow-preview-confirm-facts"><div><dt>{t('setup.ciWorkflowPreviewTarget')}</dt><dd><code>{preview.target}</code></dd></div><div><dt>{t('setup.ciWorkflowPreviewVersion')}</dt><dd><code>{preview.version}</code></dd></div></dl>
      <ul className="ci-workflow-preview-confirm-rules"><li>{t('setup.ciWorkflowExactPreview')}</li><li>{t('setup.ciWorkflowCreateOnly')}</li><li>{t('setup.ciWorkflowNoOverwrite')}</li><li>{t('setup.ciWorkflowNoAutomation')}</li></ul>
      <p className="ci-workflow-preview-warning" role="note"><AlertTriangle size={15} aria-hidden="true" /><span>{t('setup.ciWorkflowPreviewSupplyChainWarning')}</span></p>
      <footer><button className="button" type="button" ref={cancelRef} onClick={onCancel}>{t('common.cancel')}</button><button className="button primary" type="button" onClick={onConfirm}><Check size={15} />{t('setup.ciWorkflowCreate')}</button></footer>
    </section>
  </div>
}

export type { CIWorkflowPreviewProps }
