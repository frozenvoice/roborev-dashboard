import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { PanelRefine, type PanelRefineProps } from './PanelRefine'
import type { PanelConfiguration, PanelRefineRun } from './api'

const configuration: PanelConfiguration = {
  repositoryIdentity: 'repo-1',
  panels: [
    { name: 'dashboard_cross', kind: 'panel', members: ['gpt', 'claude', 'grok'], origin: 'global', valid: true },
    { name: 'dashboard_deep', kind: 'panel', members: ['gpt', 'claude', 'grok'], origin: 'global', valid: true },
    { name: 'none', kind: 'single', members: ['codex'], origin: 'builtin', valid: true },
  ],
  selectedPanel: 'dashboard_cross',
  selectionRequired: false,
}
const options = { executable: 'C:\\Tools\\roborev.exe', args: ['fix', '--agent', 'codex', '--quiet', '<synthesisJobID>'], allowedFixAgents: ['codex'], defaultFixAgent: 'codex', defaultMaxFixAttempts: 3, minFixAttempts: 1, maxFixAttempts: 10 }
const baseRun: PanelRefineRun = {
  id: 8,
  repositoryIdentity: 'repo-1',
  repositoryPath: 'E:\\repo',
  iterationPanel: 'dashboard_cross',
  finalPanel: 'dashboard_deep',
  fixAgent: 'codex',
  maxFixAttempts: 3,
  fixAttempts: 1,
  phase: 'reviewing',
  startingHead: 'abcdef1234567890',
  currentHead: 'abcdef1234567890',
  currentReviewJobID: 42,
  startedAt: '2026-08-18T00:00:00Z',
  completedAt: '2026-08-18T00:01:05Z',
  command: options,
  iterations: [{ number: 1, targetHead: 'abcdef1234567890', panel: 'dashboard_cross', reviewJobID: 42 }],
}

function renderPanel(overrides: Partial<PanelRefineProps> = {}) {
  const props: PanelRefineProps = { repoPath: 'E:\\repo', panelConfiguration: configuration, options, run: null, onStart: vi.fn(async () => undefined), onCancel: vi.fn(async () => undefined), ...overrides }
  return { ...render(<PanelRefine {...props} />), props }
}

afterEach(() => { cleanup(); vi.restoreAllMocks() })

describe('PanelRefine', () => {
  it('synchronizes a late panel discovery without overwriting an open dialog selection', async () => {
    const user = userEvent.setup()
    const view = renderPanel({ panelConfiguration: null })
    expect(screen.getByRole('button', { name: 'Panel Refine' })).toBeDisabled()
    view.rerender(<PanelRefine {...view.props} panelConfiguration={configuration} />)
    expect(screen.getByRole('button', { name: 'Panel Refine' })).toBeEnabled()
    await user.click(screen.getByRole('button', { name: 'Panel Refine' }))
    const iteration = screen.getByLabelText('Iteration panel')
    expect(iteration).toHaveValue('dashboard_cross')
    await user.selectOptions(iteration, 'dashboard_deep')
    const changedConfiguration = { ...configuration, selectedPanel: 'dashboard_deep' }
    view.rerender(<PanelRefine {...view.props} panelConfiguration={changedConfiguration} />)
    expect(screen.getByLabelText('Iteration panel')).toHaveValue('dashboard_deep')
    view.rerender(<PanelRefine {...view.props} panelConfiguration={{ ...configuration, selectedPanel: 'dashboard_cross' }} />)
    expect(screen.getByLabelText('Iteration panel')).toHaveValue('dashboard_deep')
  })

  it('opens a dialog with valid named panels, codex default, attempts and exact start body', async () => {
    const user = userEvent.setup()
    const onStart = vi.fn(async () => undefined)
    renderPanel({ onStart })
    await user.click(screen.getByRole('button', { name: 'Panel Refine' }))
    expect(screen.getByRole('dialog')).toHaveTextContent('dashboard_cross')
    expect(screen.getByLabelText('Fix agent')).toHaveValue('codex')
    expect(screen.getByLabelText('Max fix attempts')).toHaveValue('3')
    expect(screen.getByText(/POST \/api\/panel-refines/)).toBeInTheDocument()
    expect(screen.getByText(text => text.includes('roborev.exe'))).toBeInTheDocument()
    await user.selectOptions(screen.getByLabelText('Final deep panel'), 'dashboard_deep')
    await user.click(screen.getByRole('button', { name: 'Start Panel Refine' }))
    expect(onStart).toHaveBeenCalledWith({ repoPath: 'E:\\repo', panel: 'dashboard_cross', fixAgent: 'codex', maxFixAttempts: 3, finalPanel: 'dashboard_deep' })
  })

  it('renders phase, progress, SHAs, timeline and allows opening the review', async () => {
    const user = userEvent.setup()
    const onOpenReview = vi.fn()
    renderPanel({ run: baseRun, onOpenReview })
    expect(screen.getAllByRole('heading', { name: 'Panel Refine' }).length).toBeGreaterThanOrEqual(2)
    expect(screen.getByText('Reviewing')).toBeInTheDocument()
    expect(screen.getByText('1 / 3')).toBeInTheDocument()
    expect(screen.getAllByText('abcdef123456').length).toBeGreaterThanOrEqual(2)
    await user.click(screen.getByRole('button', { name: 'Open Review' }))
    expect(onOpenReview).toHaveBeenCalledWith(42)
  })

  it('numbers fixes by fix-attempt ordinal rather than review sequence', () => {
    renderPanel({ run: { ...baseRun, iterations: [
      { number: 1, targetHead: 'abcdef123456', panel: 'dashboard_cross', reviewResult: 'P' },
      { number: 2, targetHead: 'abcdef123456', panel: 'dashboard_deep', reviewResult: 'F', fixAttempted: true, fixAgent: 'codex' },
    ] } })
    expect(screen.getByText(/Fix #1/)).toBeInTheDocument()
    expect(screen.queryByText(/Fix #2/)).not.toBeInTheDocument()
  })

  it('renders iteration errors and cancelled context consistently instead of Pending', () => {
    renderPanel({ run: { ...baseRun, iterations: [
      { number: 1, targetHead: 'aaaaaaaaaaaa', panel: 'dashboard_cross', error: 'review timed out' },
      { number: 2, targetHead: 'bbbbbbbbbbbb', panel: 'dashboard_deep', error: 'context canceled' },
    ] } })
    const errorLabel = screen.getByText(/· Error/)
    const cancelledLabel = screen.getByText(/· Cancelled/)
    expect(errorLabel.closest('li')?.querySelector('.refine-timeline-mark')).toHaveClass('error')
    expect(cancelledLabel.closest('li')?.querySelector('.refine-timeline-mark')).toHaveClass('cancelled')
    expect(screen.queryByText(/· Pending/)).not.toBeInTheDocument()
  })

  it('renders terminal review totals, elapsed time, final result and final review action', () => {
    renderPanel({ run: { ...baseRun, phase: 'passed', iterations: [...baseRun.iterations, { number: 2, targetHead: 'fedcba654321', panel: 'dashboard_deep', reviewResult: 'P', reviewJobID: 43 }] } })
    expect(screen.getByText('Total reviews').parentElement).toHaveTextContent('2')
    expect(screen.getByText('1m 5s')).toBeInTheDocument()
    expect(screen.getByText('Final panel result').parentElement).toHaveTextContent('Pass')
    expect(screen.getByRole('button', { name: 'Open Final Review' })).toBeInTheDocument()
  })

  it.each([
    ['passed', 'Panel Refine passed'],
    ['needs_attention', 'Panel Refine needs attention'],
    ['failed', 'Panel Refine failed'],
    ['cancelled', 'Panel Refine cancelled'],
  ] as const)('renders terminal %s state', (phase, title) => {
    renderPanel({ run: { ...baseRun, phase, error: phase === 'failed' ? 'review failed' : undefined } })
    expect(screen.getByRole('heading', { name: title })).toBeInTheDocument()
    if (phase === 'failed') expect(screen.getByRole('alert')).toHaveTextContent('review failed')
  })

  it('cancels an active run', async () => {
    const user = userEvent.setup()
    const onCancel = vi.fn(async () => undefined)
    renderPanel({ run: baseRun, onCancel })
    await user.click(screen.getByRole('button', { name: 'Cancel Panel Refine' }))
    expect(onCancel).toHaveBeenCalledWith(8)
  })

  it('shows repository state and partial mutation warning after cancellation', () => {
    renderPanel({ run: { ...baseRun, phase: 'cancelled', cancelState: { available: true, head: 'deadbeef1234', branch: 'feature/refine', dirty: true, error: 'inspection incomplete' } } })
    expect(screen.getByText(/feature\/refine/)).toBeInTheDocument()
    expect(screen.getByText(/partial repository changes/i)).toBeInTheDocument()
    expect(screen.getByText(/inspection incomplete/)).toBeInTheDocument()
  })

  it('does not present unavailable cancellation state as clean', () => {
    renderPanel({ run: { ...baseRun, phase: 'cancelled', cancelState: { available: false } } })
    expect(screen.getByText(/state unavailable/i)).toBeInTheDocument()
    expect(screen.getByText(/could not be verified/i)).toBeInTheDocument()
    expect(screen.queryByText(/clean working tree/i)).not.toBeInTheDocument()
  })

  it('does not offer start when no valid named panel is loaded', () => {
    renderPanel({ panelConfiguration: { ...configuration, panels: configuration.panels.map(panel => ({ ...panel, valid: false })) } })
    expect(screen.getByRole('button', { name: 'Panel Refine' })).toBeDisabled()
  })

  it('does not offer start until the current repository command options are loaded', () => {
    renderPanel({ options: null })
    expect(screen.getByRole('button', { name: 'Panel Refine' })).toBeDisabled()
  })

  it('does not offer start when backend capabilities make Panel Refine unavailable', () => {
    renderPanel({ available: false, disabledReason: 'typed fix command is unavailable' })
    expect(screen.getByRole('button', { name: 'Panel Refine' })).toBeDisabled()
    expect(screen.getByRole('status')).toHaveTextContent('typed fix command is unavailable')
  })
})
