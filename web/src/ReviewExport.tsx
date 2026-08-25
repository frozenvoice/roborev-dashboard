import { AlertTriangle, Download, X } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { LanguageSelector, useI18n } from './i18n'

type ExportPhase = 'idle' | 'open' | 'busy' | 'error' | 'success'

const FALLBACK_FILENAME = 'roborev-reviews.json'
const SAFE_FILENAME = /^[A-Za-z0-9][A-Za-z0-9._-]{0,126}\.json$/i

function exportFilename(contentDisposition: string | null): string {
  if (!contentDisposition) return FALLBACK_FILENAME
  const match = /(?:^|;)\s*filename\s*=\s*(?:"([^"]*)"|([^;\s]*))/i.exec(contentDisposition)
  const candidate = match?.[1] ?? match?.[2] ?? ''
  return SAFE_FILENAME.test(candidate) ? candidate : FALLBACK_FILENAME
}

function exportUrl(repositoryIdentity: string): string {
  return `/api/exports/reviews?repositoryIdentity=${encodeURIComponent(repositoryIdentity)}`
}

export type ReviewExportProps = {
  repositoryIdentity: string
  repositoryName: string
}

export function ReviewExport({ repositoryIdentity, repositoryName }: ReviewExportProps) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const [phase, setPhase] = useState<ExportPhase>('idle')
  const [truncated, setTruncated] = useState(false)
  const [error, setError] = useState(false)
  const mounted = useRef(true)
  const generation = useRef(0)
  const controller = useRef<AbortController | null>(null)
  const busy = useRef(false)
  const identityRef = useRef(repositoryIdentity)
  identityRef.current = repositoryIdentity

  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
      generation.current += 1
      controller.current?.abort()
      controller.current = null
      busy.current = false
    }
  }, [])

  useEffect(() => {
    generation.current += 1
    controller.current?.abort()
    controller.current = null
    busy.current = false
    setOpen(false)
    setPhase('idle')
    setTruncated(false)
    setError(false)
  }, [repositoryIdentity])

  const close = () => {
    generation.current += 1
    controller.current?.abort()
    controller.current = null
    busy.current = false
    setOpen(false)
    setPhase('idle')
    setError(false)
  }

  const confirm = async () => {
    if (!repositoryIdentity || busy.current || (phase !== 'open' && phase !== 'error')) return
    busy.current = true
    const requestGeneration = ++generation.current
    const requestController = new AbortController()
    controller.current = requestController
    setPhase('busy')
    setError(false)
    try {
      const response = await fetch(exportUrl(repositoryIdentity), { method: 'GET', headers: { Accept: 'application/json' }, signal: requestController.signal })
      if (!response.ok) throw new Error('export-response')
      const contentType = response.headers.get('Content-Type')?.split(';', 1)[0].trim().toLowerCase()
      if (contentType !== 'application/json') throw new Error('export-content-type')
      const truncatedHeader = response.headers.get('X-RoboRev-Export-Truncated')
      if (truncatedHeader !== 'true' && truncatedHeader !== 'false') throw new Error('export-truncated-header')
      const blob = await response.blob()
      if (!mounted.current || identityRef.current !== repositoryIdentity || generation.current !== requestGeneration || requestController.signal.aborted) return

      const objectUrl = window.URL.createObjectURL(blob)
      try {
        const anchor = document.createElement('a')
        anchor.href = objectUrl
        anchor.download = exportFilename(response.headers.get('Content-Disposition'))
        anchor.style.display = 'none'
        document.body.appendChild(anchor)
        anchor.click()
        anchor.remove()
      } finally {
        window.URL.revokeObjectURL(objectUrl)
      }

      if (!mounted.current || identityRef.current !== repositoryIdentity || generation.current !== requestGeneration) return
      setTruncated(truncatedHeader === 'true')
      setOpen(false)
      setPhase('success')
    } catch {
      if (requestController.signal.aborted || !mounted.current || generation.current !== requestGeneration) return
      setError(true)
      setPhase('error')
    } finally {
      if (controller.current === requestController) controller.current = null
      busy.current = false
    }
  }

  const openDialog = () => {
    if (!repositoryIdentity || busy.current) return
    setError(false)
    setPhase('open')
    setOpen(true)
  }

  return <div className="review-export">
    <button type="button" className="button ghost compact" onClick={openDialog} disabled={!repositoryIdentity || busy.current}>
      <Download size={14} aria-hidden="true" />{t('review.export.open')}
    </button>
    {phase === 'success' && <p className={truncated ? 'review-export-status review-export-warning' : 'review-export-status'} role="status">
      {truncated && <AlertTriangle size={14} aria-hidden="true" />}
      {truncated ? t('review.export.truncated') : t('review.export.success')}
    </p>}
    {open && <div className="prompt-overlay">
      <section className="prompt-dialog review-export-dialog" role="dialog" aria-modal="true" aria-labelledby="review-export-title" aria-describedby="review-export-description">
        <header>
          <div>
            <h2 id="review-export-title">{t('review.export.title')}</h2>
            <p id="review-export-description">{t('review.export.description')}</p>
          </div>
          <div className="modal-header-actions"><LanguageSelector compact /><button type="button" className="icon-button" aria-label={t('review.export.close')} onClick={close}><X /></button></div>
        </header>
        <dl className="review-export-facts">
          <div><dt>{t('review.export.repository')}</dt><dd className="review-export-repository">{repositoryName || t('common.none')}</dd></div>
          <div><dt>{t('review.export.profile')}</dt><dd>{t('review.export.metadata')}</dd></div>
          <div><dt>{t('review.export.limit')}</dt><dd>{t('review.export.limitValue')}</dd></div>
          <div><dt>{t('review.export.format')}</dt><dd>{t('review.export.json')}</dd></div>
        </dl>
        {error && <p className="review-export-error" role="alert">{t('review.export.error')}</p>}
        <footer>
          <button type="button" className="button" onClick={close}>{t('common.cancel')}</button>
          <button type="button" className="button primary" onClick={() => { void confirm() }} disabled={phase === 'busy'}>{phase === 'busy' ? t('review.export.exporting') : t('review.export.confirm')}</button>
        </footer>
      </section>
    </div>}
  </div>
}

export default ReviewExport
