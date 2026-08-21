import { useEffect, useRef, useState } from 'react'
import { AlertTriangle, Check, Code2, Eye, Play, ShieldAlert, X } from 'lucide-react'
import { normalizeAnalysisPreview, normalizeAnalysisPrompt, normalizeAnalysisReceipt, normalizeAnalysisTypesResponse, request, type AnalysisPreview, type AnalysisPrompt, type AnalysisReceipt, type AnalysisScope, type AnalysisType, type ReviewJob } from './api'
import { LanguageSelector, useI18n } from './i18n'

type Toast = (kind: 'success' | 'error', message: string) => void

export type AnalyzeProps = {
  repoIdentity: string
  repoPath: string
  jobs: ReviewJob[]
  installed: boolean
  daemonRunning: boolean
  analyzeAvailable: boolean
  busy: boolean
  onOpenReview: (id: number) => void
  onToast?: Toast
}

function jobID(job: ReviewJob) {
  const value = job.id ?? job.job_id
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0 ? value : null
}

function jobStatus(job: ReviewJob | null) {
  return String(job?.verdict || job?.status || 'queued').toLowerCase()
}

function statusKey(job: ReviewJob | null): 'pass' | 'finding' | 'done' | 'running' | 'queued' | 'canceled' | 'error' | 'unknown' {
  const status = jobStatus(job)
  if (status === 'p' || status === 'pass' || status === 'passed') return 'pass'
  if (status === 'f' || status === 'fail' || status === 'finding' || status === 'failed_review') return 'finding'
  if (status === 'done') return 'done'
  if (status === 'running') return 'running'
  if (status === 'canceled' || status === 'cancelled') return 'canceled'
  if (status === 'failed' || status === 'error') return 'error'
  if (status === 'queued') return 'queued'
  return 'unknown'
}

function commandLabel(command: AnalysisPreview['command']) {
  return [command.executable, ...command.args].join(' ')
}

export function Analyze({ repoIdentity, repoPath, jobs, installed, daemonRunning, analyzeAvailable, busy, onOpenReview, onToast }: AnalyzeProps) {
  const { t } = useI18n()
  const translateRef = { current: t }
  translateRef.current = t
  const [types, setTypes] = useState<AnalysisType[]>([])
  const [selectedType, setSelectedType] = useState('')
  const [catalogLoading, setCatalogLoading] = useState(false)
  const [catalogError, setCatalogError] = useState('')
  const [scope, setScope] = useState<AnalysisScope>('branch')
  const [prompt, setPrompt] = useState<AnalysisPrompt | null>(null)
  const [promptLoading, setPromptLoading] = useState(false)
  const [promptError, setPromptError] = useState('')
  const [preview, setPreview] = useState<AnalysisPreview | null>(null)
  const [previewLoading, setPreviewLoading] = useState(false)
  const [previewError, setPreviewError] = useState('')
  const [dialogOpen, setDialogOpen] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [receipt, setReceipt] = useState<AnalysisReceipt | null>(null)
  const [receiptScope, setReceiptScope] = useState<AnalysisScope | null>(null)
  const catalogGeneration = useRef(0)
  const promptGeneration = useRef(0)
  const previewGeneration = useRef(0)
  const submitRef = useRef(false)

  useEffect(() => {
    const generation = ++catalogGeneration.current
    promptGeneration.current++
    previewGeneration.current++
    setTypes([])
    setSelectedType('')
    setCatalogLoading(true)
    setCatalogError('')
    setPrompt(null)
    setPreview(null)
    setReceipt(null)
    setReceiptScope(null)
    void request<unknown>('/api/analyses/types').then(value => {
      if (generation !== catalogGeneration.current) return
      const next = normalizeAnalysisTypesResponse(value)
      if (!next) throw new Error(translateRef.current('error.analyzeCatalogMalformed'))
      setTypes(next)
      setSelectedType(next[0]?.name ?? '')
    }).catch(error => {
      if (generation === catalogGeneration.current) setCatalogError((error as Error).message)
    }).finally(() => {
      if (generation === catalogGeneration.current) setCatalogLoading(false)
    })
    return () => { catalogGeneration.current++; promptGeneration.current++; previewGeneration.current++ }
  }, [repoIdentity])

  const selected = types.find(type => type.name === selectedType) ?? null
  const matchingJob = receipt?.jobs.length ? jobs.find(job => jobID(job) === receipt.jobs[0].id) ?? null : null
  const receiptAgent = receipt?.jobs[0]?.agent ?? matchingJob?.agent ?? ''
  const receiptStatus = statusKey(matchingJob)
  const canPrepare = Boolean(repoIdentity && repoPath && selectedType && installed && daemonRunning && analyzeAvailable && !busy && !catalogLoading && !previewLoading && !submitting)
  const availabilityReason = !installed ? t('analyze.unavailableNotInstalled') : !daemonRunning ? t('analyze.unavailableDaemonOffline') : !analyzeAvailable ? t('analyze.unavailableCapability') : ''

  const selectType = (value: string) => {
    promptGeneration.current++
    previewGeneration.current++
    setSelectedType(value)
    setPrompt(null)
    setPromptError('')
    setPromptLoading(false)
    setPreview(null)
    setPreviewError('')
    setDialogOpen(false)
  }

  const loadPrompt = async () => {
    if (!selectedType || promptLoading) return
    const generation = ++promptGeneration.current
    const expectedType = selectedType
    setPrompt(null)
    setPromptError('')
    setPromptLoading(true)
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

  const prepare = async () => {
    if (!canPrepare || !selectedType) return
    const generation = ++previewGeneration.current
    const expectedType = selectedType
    const expectedScope = scope
    setPreview(null)
    setPreviewError('')
    setPreviewLoading(true)
    try {
      const url = `/api/analyses/preview?repositoryIdentity=${encodeURIComponent(repoIdentity)}&type=${encodeURIComponent(expectedType)}&scope=${encodeURIComponent(expectedScope)}`
      const next = normalizeAnalysisPreview(await request<unknown>(url), { repositoryIdentity: repoIdentity, analysisType: expectedType, scope: expectedScope })
      if (!next) throw new Error(translateRef.current('error.analyzePreviewMalformed'))
      if (generation === previewGeneration.current) {
        setPreview(next)
        setDialogOpen(true)
      }
    } catch (error) {
      if (generation === previewGeneration.current) setPreviewError((error as Error).message)
    } finally {
      if (generation === previewGeneration.current) setPreviewLoading(false)
    }
  }

  const submit = async () => {
    if (!preview || submitting || submitRef.current || busy) return
    submitRef.current = true
    setSubmitting(true)
    try {
      const next = normalizeAnalysisReceipt(await request<unknown>('/api/analyses', {
        method: 'POST',
        body: JSON.stringify({ repositoryIdentity: preview.repositoryIdentity, analysisType: preview.analysisType, scope: preview.scope, previewToken: preview.previewToken }),
      }), preview.analysisType)
      if (!next) throw new Error(t('error.analyzeReceiptMalformed'))
      setReceipt(next)
      setReceiptScope(preview.scope)
      setDialogOpen(false)
      onToast?.('success', t('analyze.enqueued'))
    } catch (error) {
      onToast?.('error', (error as Error).message)
    } finally {
      submitRef.current = false
      setSubmitting(false)
    }
  }

  return <section className="analyze-page" aria-label={t('analyze.aria')}>
    <header className="section-heading analyze-heading"><div><h1>{t('analyze.title')}</h1><p>{t('analyze.description')}</p></div></header>
    <div className="analyze-grid">
      <section className="analyze-workflow" aria-labelledby="analyze-type-heading">
        <div className="analyze-section-heading"><div><h2 id="analyze-type-heading">{t('analyze.type')}</h2><p>{t('analyze.typeDescription')}</p></div></div>
        {catalogLoading && <div className="skeleton" aria-label={t('common.loading')} />}
        {catalogError && <p role="alert" className="analyze-error">{catalogError}</p>}
        {!catalogLoading && !catalogError && <label className="analyze-select-label" htmlFor="analysis-type"><span>{t('analyze.type')}</span><select id="analysis-type" aria-label={t('analyze.type')} value={selectedType} onChange={event => selectType(event.target.value)} disabled={!types.length || previewLoading || submitting}>{types.map(type => <option value={type.name} key={type.name}>{type.name}</option>)}</select></label>}
        {selected && <p className="analyze-source-text">{selected.description}</p>}
        <fieldset className="analyze-scope"><legend>{t('analyze.scope')}</legend>
          <label className={scope === 'branch' ? 'selected' : ''}><input type="radio" name="analysis-scope" value="branch" checked={scope === 'branch'} onChange={() => { setScope('branch'); setPreview(null); setPreviewError(''); previewGeneration.current++ }} /><span><strong>{t('analyze.scopeBranch')}</strong><small>{t('analyze.scopeBranchDescription')}</small></span></label>
          <label className={scope === 'repository' ? 'selected' : ''}><input type="radio" name="analysis-scope" value="repository" checked={scope === 'repository'} onChange={() => { setScope('repository'); setPreview(null); setPreviewError(''); previewGeneration.current++ }} /><span><strong>{t('analyze.scopeRepository')}</strong><small>{t('analyze.scopeRepositoryDescription')}</small></span></label>
        </fieldset>
        <div className="analyze-inline-actions"><button type="button" className="button" onClick={() => void loadPrompt()} disabled={!selectedType || promptLoading || catalogLoading}><Eye size={15} />{promptLoading ? t('analyze.loadingPrompt') : t('analyze.showPrompt')}</button><button type="button" className="button primary" onClick={() => void prepare()} disabled={!canPrepare}><Play size={15} />{previewLoading ? t('analyze.preparing') : t('analyze.prepare')}</button></div>
        {availabilityReason && <p className="analyze-disabled-reason" role="status">{availabilityReason}</p>}
        {previewError && <p role="alert" className="analyze-error">{t('analyze.previewFailed', { error: previewError })}</p>}
        {promptError && <p role="alert" className="analyze-error">{t('analyze.promptFailed', { error: promptError })}</p>}
        {prompt && prompt.analysisType === selectedType && <section className="analyze-prompt" aria-live="polite"><header><div><span>{prompt.analysisType}</span><h2>{t('analyze.promptTitle')}</h2></div><button type="button" className="button ghost compact" onClick={() => setPrompt(null)}>{t('common.close')}</button></header><pre>{prompt.prompt}</pre></section>}
      </section>
      <aside className="analyze-guidance" aria-label={t('analyze.safety')}>
        <div className="analyze-guidance-icon"><ShieldAlert size={19} /></div><h2>{t('analyze.safetyTitle')}</h2><p>{t('analyze.agenticWarning')}</p><p>{scope === 'branch' ? t('analyze.branchTruth') : t('analyze.repositoryTruth')}</p><p>{t('analyze.noAutoFix')}</p>
      </aside>
    </div>
    {receipt && <section className="analyze-receipt" aria-live="polite"><header><div><span className="eyebrow">{t('analyze.recent')}</span><h2>#{receipt.jobs[0].id} · {receipt.analysisType}</h2></div><span className={`status status-${receiptStatus}`}><span className="status-dot" />{t(`status.${receiptStatus}` as 'status.pass')}</span></header><dl><div><dt>{t('analyze.agent')}</dt><dd>{receiptAgent}</dd></div><div><dt>{t('analyze.scope')}</dt><dd>{receiptScope === 'repository' ? t('analyze.scopeRepository') : t('analyze.scopeBranch')}</dd></div><div><dt>{t('analyze.files')}</dt><dd>{receipt.files.length}</dd></div></dl>{!matchingJob && <p className="analyze-receipt-note">{t('analyze.waitingForJobs')}</p>}{matchingJob && <button type="button" className="button" onClick={() => { const id = jobID(matchingJob); if (id !== null) onOpenReview(id) }}><Code2 size={15} />{t('analyze.openResult')}</button>}</section>}
    {dialogOpen && preview && <div className="prompt-overlay" role="presentation"><section className="prompt-dialog analyze-dialog" role="dialog" aria-modal="true" aria-labelledby="analyze-confirm-title"><header><div><h2 id="analyze-confirm-title">{t('analyze.confirmTitle')}</h2><p>{t('analyze.confirmDescription')}</p></div><div className="modal-header-actions"><LanguageSelector compact /><button type="button" className="icon-button" aria-label={t('analyze.closePreview')} onClick={() => setDialogOpen(false)} disabled={submitting}><X /></button></div></header><dl className="analyze-preview-facts"><div><dt>{t('analyze.repository')}</dt><dd><code>{preview.repositoryPath}</code></dd></div><div><dt>{t('analyze.head')}</dt><dd><code>{preview.head}</code> · {preview.branch}</dd></div><div><dt>{t('analyze.type')}</dt><dd>{preview.analysisType} · {preview.scope === 'branch' ? t('analyze.scopeBranch') : t('analyze.scopeRepository')}</dd></div></dl><div className="analyze-command-preview"><strong>{t('analyze.command')}</strong><code>{preview.command.executable}</code><ol>{preview.command.args.map((arg, index) => <li key={`${index}-${arg}`}><span>argv[{index}]</span><code>{arg}</code></li>)}</ol><small>{commandLabel(preview.command)}</small></div><p className="analyze-confirm-warning"><AlertTriangle size={16} />{t('analyze.confirmWarning')}</p><footer><button type="button" className="button" onClick={() => setDialogOpen(false)} disabled={submitting}>{t('common.cancel')}</button><button type="button" className="button primary" onClick={() => void submit()} disabled={submitting}>{submitting ? t('analyze.confirming') : <><Check size={15} />{t('analyze.confirm')}</>}</button></footer></section></div>}
  </section>
}
