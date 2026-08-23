import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ReviewDetail } from './App'
import { I18nProvider } from './i18n'

const baseJob = { id: 42, job_type: 'insights', status: 'done', closed: true, output: '# Recommendations' }
function renderDetail(job: Record<string, unknown>) {
  return render(<I18nProvider><ReviewDetail job={job as any} jobs={[job as any]} close={() => undefined} mutate={vi.fn(async () => undefined)} busy={false} canRerun canLoadMembers={false} repoPath="E:\\repo" repoIdentity="repo-a" toast={() => undefined} /></I18nProvider>)
}
afterEach(() => { cleanup(); vi.restoreAllMocks() })

describe('ReviewDetail Insights generic actions', () => {
  it('hides generic cancel while an Insights job is running', () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(JSON.stringify({ ...baseJob, status: 'running', closed: false }), { status: 200 }))))
    renderDetail({ ...baseJob, status: 'running', closed: false })
    expect(screen.queryByRole('button', { name: 'Cancel' })).not.toBeInTheDocument()
  })

  it('hides generic rerun for a terminal Insights job', () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response(JSON.stringify(baseJob), { status: 200 }))))
    renderDetail(baseJob)
    expect(screen.queryByRole('button', { name: 'Rerun same target/config' })).not.toBeInTheDocument()
  })

  it('keeps generic cancel for an ordinary running review', () => {
    renderDetail({ ...baseJob, job_type: 'review', status: 'running', closed: false })
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument()
  })

  it('keeps generic rerun for an ordinary terminal review', () => {
    renderDetail({ ...baseJob, job_type: 'review', status: 'done', closed: true })
    expect(screen.getByRole('button', { name: 'Rerun same target/config' })).toBeInTheDocument()
  })
})
