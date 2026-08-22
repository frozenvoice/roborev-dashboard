import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { I18nProvider } from './i18n'
import { Tasks } from './Tasks'
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

  it('TASK_HISTORY_LIMIT constant matches backend bounded limit contract', () => {
    expect(TASK_HISTORY_LIMIT).toBe(200)
  })
})
