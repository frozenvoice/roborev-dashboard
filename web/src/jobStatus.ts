import type { ReviewJob } from './api'
import type { useI18n } from './i18n'

type Translate = ReturnType<typeof useI18n>['t']

// RoboRev's `status` field is the authoritative job lifecycle. Non-terminal
// and failure states win over any recorded verdict; only a terminal-success
// status is interpreted through the verdict (done+F → finding, done+P → pass).
// Reviews and Tasks must render the same job with the same label, so both
// screens share this computation instead of keeping private copies.
export function jobStatus(job: ReviewJob): string {
  const value = String(job.status || '').toLowerCase()
  if (value === 'queued') return 'queued'
  if (['running', 'processing', 'started'].includes(value)) return 'running'
  if (value === 'finding' || value === 'failed_review') return 'finding'
  if (value === 'failed' || value === 'error') return 'error'
  if (value === 'canceled' || value === 'cancelled') return 'canceled'
  if (['done', 'completed', 'succeeded', 'passed', 'pass'].includes(value)) {
    const verdict = String(job.verdict || (job.review && typeof job.review === 'object' && job.review.verdict) || '').toLowerCase()
    if (verdict === 'f' || verdict === 'fail' || verdict === 'finding' || verdict === 'failed_review') return 'finding'
    if (verdict === 'p' || verdict === 'pass' || verdict === 'passed') return 'pass'
    return 'done'
  }
  return value || 'queued'
}

// Labels accept lifecycle tokens from jobStatus plus raw verdict letters
// ('p'/'f') that callers read straight off member rows.
export function jobStatusLabel(value: string, t: Translate): string {
  if (value === 'p' || value === 'pass' || value === 'passed') return t('status.pass')
  if (value === 'f' || value === 'fail' || value === 'finding' || value === 'failed_review') return t('status.finding')
  if (value === 'done') return t('status.done')
  if (value === 'running') return t('status.running')
  if (value === 'queued') return t('status.queued')
  if (value === 'canceled' || value === 'cancelled') return t('status.canceled')
  if (value === 'failed' || value === 'error') return t('status.error')
  return value || t('status.unknown')
}
