import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { I18nProvider, LanguageSelector } from './i18n'
import { ReviewExport } from './ReviewExport'

function renderExport(withLanguageSelector = false) {
  return render(<I18nProvider>{withLanguageSelector && <LanguageSelector />}<ReviewExport /></I18nProvider>)
}

describe('ReviewExport', () => {
  let fetchMock: ReturnType<typeof vi.fn>
  let createObjectURL: ReturnType<typeof vi.fn>
  let revokeObjectURL: ReturnType<typeof vi.fn>
  let clickMock: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    createObjectURL = vi.fn()
    revokeObjectURL = vi.fn()
    Object.defineProperty(window.URL, 'createObjectURL', { configurable: true, writable: true, value: createObjectURL })
    Object.defineProperty(window.URL, 'revokeObjectURL', { configurable: true, writable: true, value: revokeObjectURL })
    clickMock = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined)
    window.localStorage.clear()
  })

  afterEach(() => {
    vi.restoreAllMocks()
    cleanup()
    vi.unstubAllGlobals()
  })

  it('keeps the export CTA disabled and explains the v0.64 safety block in English', async () => {
    renderExport(true)
    const exportButton = screen.getByRole('button', { name: 'Export reviews' })

    expect(exportButton).toBeDisabled()
    expect(screen.getByRole('note')).toHaveTextContent('RoboRev v0.64.0 cannot safely provide repository-scoped review export.')

    await userEvent.click(exportButton)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
    expect(createObjectURL).not.toHaveBeenCalled()
    expect(revokeObjectURL).not.toHaveBeenCalled()
    expect(clickMock).not.toHaveBeenCalled()
  })

  it('keeps the same non-actionable state and explanation localized in Korean', async () => {
    renderExport(true)
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Select language' }), 'ko')

    const exportButton = screen.getByRole('button', { name: '리뷰 내보내기' })
    expect(exportButton).toBeDisabled()
    expect(screen.getByRole('note')).toHaveTextContent('RoboRev v0.64.0에서는 저장소 범위 리뷰 내보내기를 안전하게 제공할 수 없습니다.')

    await userEvent.click(exportButton)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
    expect(createObjectURL).not.toHaveBeenCalled()
    expect(revokeObjectURL).not.toHaveBeenCalled()
    expect(clickMock).not.toHaveBeenCalled()
  })

  it('does not perform export work when the locale changes', async () => {
    renderExport(true)
    expect(fetchMock).not.toHaveBeenCalled()
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Select language' }), 'ko')
    await userEvent.selectOptions(screen.getByRole('combobox', { name: '언어 선택' }), 'en')

    expect(fetchMock).not.toHaveBeenCalled()
    expect(createObjectURL).not.toHaveBeenCalled()
    expect(revokeObjectURL).not.toHaveBeenCalled()
    expect(clickMock).not.toHaveBeenCalled()
  })
})
