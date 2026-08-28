import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { GeneralRefine } from './GeneralRefine'
import { I18nProvider, LANGUAGE_STORAGE_KEY, LanguageSelector } from './i18n'

function view() {
  return render(<I18nProvider><GeneralRefine repositoryIdentity="repo-1" onOpenReview={vi.fn()} onTerminal={vi.fn(async () => undefined)} /></I18nProvider>)
}

describe('General Refine lifecycle safety', () => {
  afterEach(() => { cleanup(); vi.unstubAllGlobals(); window.localStorage.setItem(LANGUAGE_STORAGE_KEY, 'en') })

  it('is a static disabled state with no preview, start, poll, or cancel request', () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    view()

    expect(screen.getByRole('button', { name: 'Execution disabled' })).toBeDisabled()
    expect(screen.getByRole('status')).toHaveTextContent("RoboRev v0.64.0's fix command can implicitly manage daemon lifecycle, so Dashboard execution is disabled.")
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('keeps the blocked explanation localized without exposing paths or adapter details', async () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    const user = userEvent.setup()
    render(<I18nProvider><LanguageSelector /><GeneralRefine repositoryIdentity="repo-1" /></I18nProvider>)

    await user.selectOptions(screen.getByLabelText('Select language'), 'ko')
    expect(screen.getByRole('button', { name: '실행 비활성화' })).toBeDisabled()
    expect(screen.getByRole('status')).toHaveTextContent('RoboRev v0.64.0의 fix 명령은 daemon 수명 주기를 암묵적으로 관리할 수 있으므로 Dashboard 실행을 비활성화했습니다.')
    expect(document.body.textContent).not.toMatch(/C:\\|\\Users\\|roborev\.exe|previewToken|POST \/api/)
    expect(fetchMock).not.toHaveBeenCalled()
  })
})
