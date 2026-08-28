import { AlertTriangle, ShieldCheck } from 'lucide-react'
import { useI18n } from './i18n'

type Toast = (kind: 'success' | 'error', message: string) => void

/**
 * General Refine is intentionally an inspection-only tombstone for v0.64.
 *
 * The upstream fix command can start/restart a daemon and normalize hooks as
 * part of its own execution path. The Dashboard cannot make that hidden
 * lifecycle explicit, so this component must not fetch state or expose a
 * preview/confirmation/start/cancel workflow. Keep the props shape so the
 * surrounding Fix & Refine wiring remains source-compatible with a future
 * lifecycle-safe implementation.
 */
export type GeneralRefineProps = {
  repositoryIdentity: string
  onOpenReview?: (id: number) => void
  onTerminal?: () => Promise<void>
  onToast?: Toast
}

export function GeneralRefine(_props: GeneralRefineProps) {
  const { t } = useI18n()
  return <section className="general-refine" aria-label={t('generalRefine.aria')}>
    <div className="section-heading refine-heading">
      <div>
        <span className="eyebrow">{t('generalRefine.eyebrow')}</span>
        <h2>General Refine</h2>
        <p>{t('generalRefine.description')}</p>
      </div>
      <button className="button primary" type="button" disabled aria-describedby="general-refine-disabled-reason">
        <ShieldCheck size={16} />{t('generalRefine.executionDisabledCta')}
      </button>
    </div>
    <div className="general-refine-disabled" id="general-refine-disabled-reason" role="status">
      <AlertTriangle size={16} />
      <div>
        <strong>{t('generalRefine.executionDisabledTitle')}</strong>
        <p>{t('generalRefine.executionDisabled')}</p>
      </div>
    </div>
  </section>
}
