import { Download } from 'lucide-react'
import { useI18n } from './i18n'

export function ReviewExport() {
  const { t } = useI18n()

  return <div className="review-export">
    <button type="button" className="button ghost compact" disabled aria-describedby="review-export-unavailable">
      <Download size={14} aria-hidden="true" />{t('review.export.open')}
    </button>
    <p id="review-export-unavailable" className="review-export-status review-export-blocked" role="note">
      {t('review.export.unavailable')}
    </p>
  </div>
}

export default ReviewExport
