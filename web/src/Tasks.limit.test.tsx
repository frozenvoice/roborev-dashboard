import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider } from './i18n'
import { Tasks } from './Tasks'
import { App } from './App'
import { TASK_HISTORY_LIMIT } from './api'

const repository = { identity: 'repo-1', name: 'repo', path: 'E:\\repo', branch: 'main', head: 'abcdef1234567890', headShort: 'abcdef12', commitSubject: 'Initial', dirty: false, hasConfig: true }

afterEach(() => cleanup())

describe('Tasks bounded history wording', () => {
  it('describes history as bounded latest N and empty as bounded latest N', () => {
    const { rerender } = render(<I18nProvider><Tasks repository={repository} jobs={[]} onOpenReview={() => {}} /></I18nProvider>)
    expect(screen.getAllByText(new RegExp(`${TASK_HISTORY_LIMIT}`)).length).toBeGreaterThanOrEqual(1)
    expect(screen.getByText(/^Custom Tasks found in the latest 200 repository jobs\.$/)).toBeInTheDocument()
    expect(screen.getByText(/^No Custom Tasks found in the latest 200 repository jobs\.$/)).toBeInTheDocument()
    // Korean also contains limit when switched – verify placeholder interpolation works
    // Switch to Korean via localStorage would require reload; just ensure English already covers limit constant
    const historyJob = { id: 50, uuid: '550e8400-e29b-41d4-a716-446655440050', job_type: 'task', git_ref: 'run', status: 'done' }
    rerender(<I18nProvider><Tasks repository={repository} jobs={[historyJob as any]} onOpenReview={() => {}} /></I18nProvider>)
    expect(screen.getByText(/^Custom Tasks found in the latest 200 repository jobs\.$/)).toBeInTheDocument()
  })

  it('requests history with bounded limit parameter', async () => {
    const calls: string[] = []
    const bootstrap = {
      csrfToken: 'csrf',
      selectedRepository: 'E:\\repo',
      selectedRepositoryIdentity: 'repo-1',
      repositories: [{ identity: 'repo-1', name: 'repo', path: 'E:\\repo', branch: 'main', head: 'abcdef123456', headShort: 'abcdef12', commitSubject: 'Initial', dirty: false, hasConfig: true }],
      roborev: { installed: true, version: 'v0.64.0', status: { running: true }, capabilities: { reviewEnqueueApi: true, frozenRerunApi: true, jobListApi: true, jobOutputStatusApi: true, panelReview: true, fixCommand: true, panelMembersApi: true, promptLazyLoad: true, rawLog: true, streamEvents: false } },
    }
    const panelConfiguration = { repositoryIdentity: 'repo-1', panels: [{ name: 'reviewers', kind: 'panel', members: ['codex'], origin: 'global', valid: true }], selectedPanel: 'reviewers', selectionRequired: false }
    const fetchStub = vi.fn((input: RequestInfo | URL) => {
      const path = String(input)
      calls.push(path)
      if (path === '/api/bootstrap') return Promise.resolve(new Response(JSON.stringify(bootstrap), { headers: { 'Content-Type': 'application/json' } }))
      if (path.startsWith('/api/repos/panels?')) return Promise.resolve(new Response(JSON.stringify(panelConfiguration), { headers: { 'Content-Type': 'application/json' } }))
      if (path.startsWith('/api/reviews?')) return Promise.resolve(new Response('[]', { headers: { 'Content-Type': 'application/json' } }))
      return Promise.resolve(new Response(JSON.stringify({}), { headers: { 'Content-Type': 'application/json' } }))
    })
    vi.stubGlobal('fetch', fetchStub)
    const user = (await import('@testing-library/user-event')).default.setup()
    render(<I18nProvider><App /></I18nProvider>)
    const tasksButton = await screen.findByRole('button', { name: 'Tasks' })
    await user.click(tasksButton)
    await new Promise(resolve => setTimeout(resolve, 200))
    const historyCall = calls.find(c => c.includes('/api/reviews?') && c.includes('limit='))
    expect(historyCall).toBeDefined()
    expect(historyCall).toContain('limit=200')
    expect(historyCall).toContain(`limit=${TASK_HISTORY_LIMIT}`)
  })
})
