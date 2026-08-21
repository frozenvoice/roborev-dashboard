import { describe, expect, it } from 'vitest'
import type { ReviewJob } from './api'
import { jobStatus, jobStatusLabel } from './jobStatus'

const t = (key: string) => `label:${key}`

function job(overrides: Partial<ReviewJob>): ReviewJob {
  return { id: 1, job_type: 'review', git_ref: 'abcdef123456', status: 'done', ...overrides } as ReviewJob
}

describe('shared job status computation', () => {
  it('keeps the live lifecycle authoritative over stale verdicts', () => {
    expect(jobStatus(job({ status: 'running', verdict: 'F' }))).toBe('running')
    expect(jobStatus(job({ status: 'running', verdict: 'P' }))).toBe('running')
    expect(jobStatus(job({ status: 'queued', verdict: 'F' }))).toBe('queued')
    expect(jobStatus(job({ status: 'canceled', verdict: 'F' }))).toBe('canceled')
    expect(jobStatus(job({ status: 'failed', verdict: 'P' }))).toBe('error')
  })

  it('reads the verdict only on terminal-success statuses', () => {
    expect(jobStatus(job({ status: 'done', verdict: 'F' }))).toBe('finding')
    expect(jobStatus(job({ status: 'done', verdict: 'P' }))).toBe('pass')
    expect(jobStatus(job({ status: 'completed' }))).toBe('done')
    expect(jobStatus(job({ status: 'finding' }))).toBe('finding')
  })

  it('labels lifecycle tokens and raw verdict letters identically', () => {
    expect(jobStatusLabel('running', t)).toBe(t('status.running'))
    expect(jobStatusLabel('pass', t)).toBe(t('status.pass'))
    expect(jobStatusLabel('p', t)).toBe(t('status.pass'))
    expect(jobStatusLabel('f', t)).toBe(t('status.finding'))
    expect(jobStatusLabel('finding', t)).toBe(t('status.finding'))
    expect(jobStatusLabel('canceled', t)).toBe(t('status.canceled'))
    expect(jobStatusLabel('', t)).toBe(t('status.unknown'))
  })

  it('gives one job the same label on every screen', () => {
    // Reviews and Tasks both render through jobStatus/jobStatusLabel, so the
    // same running task with a stale F verdict can never be "Finding" in one
    // list and "Running" in the other.
    const task = job({ job_type: 'task', git_ref: 'run', status: 'running', verdict: 'F' })
    expect(jobStatusLabel(jobStatus(task), t)).toBe(t('status.running'))
  })
})
