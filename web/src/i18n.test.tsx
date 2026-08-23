import { act, cleanup, render, screen } from '@testing-library/react'
import { useRef } from 'react'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { I18nProvider, LANGUAGE_STORAGE_KEY, resolveLocale, useI18n } from './i18n'

function Harness() {
  const { locale, setLocale, t, format } = useI18n()
  const firstT = useRef(t)
  const firstFormat = useRef(format)
  return <div>
    <output data-testid="locale">{locale}</output>
    <output data-testid="label">{t('nav.overview')}</output>
    <output data-testid="rename-title">{t('repo.renameTitle')}</output>
    <output data-testid="rename-unavailable">{t('repo.renameUnavailable')}</output>
    <output data-testid="rename-unsupported">{t('repo.renameUnsupported')}</output>
    <output data-testid="move-title">{t('repo.moveTitle')}</output>
    <output data-testid="move-warning">{t('repo.moveWarning')}</output>
    <output data-testid="formatted">{format('review.runningCount', { count: 2 })}</output>
    <output data-testid="stable-t">{String(firstT.current === t)}</output>
    <output data-testid="stable-format">{String(firstFormat.current === format)}</output>
    <button onClick={() => setLocale('ko')}>한국어</button>
    <button onClick={() => setLocale('en')}>English</button>
  </div>
}

function renderHarness() {
  return render(<I18nProvider><Harness /></I18nProvider>)
}

describe('frontend locale provider', () => {
  const originalLanguage = navigator.language
  let storageDescriptor: PropertyDescriptor | undefined

  beforeEach(() => {
    window.localStorage.clear()
    storageDescriptor = Object.getOwnPropertyDescriptor(window, 'localStorage')
    Object.defineProperty(window.navigator, 'language', { configurable: true, value: originalLanguage })
    document.documentElement.lang = ''
  })

  afterEach(() => {
    cleanup()
    if (storageDescriptor) Object.defineProperty(window, 'localStorage', storageDescriptor)
    Object.defineProperty(window.navigator, 'language', { configurable: true, value: originalLanguage })
  })

  it('resolves stored language before browser language and defaults to English', () => {
    Object.defineProperty(window.navigator, 'language', { configurable: true, value: 'ko-KR' })
    expect(resolveLocale()).toBe('ko')
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, 'en')
    expect(resolveLocale()).toBe('en')
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, 'invalid')
    Object.defineProperty(window.navigator, 'language', { configurable: true, value: 'fr-FR' })
    expect(resolveLocale()).toBe('en')
  })

  it('survives denied localStorage access without changing the locale contract', () => {
    Object.defineProperty(window, 'localStorage', {
      configurable: true,
      value: {
        getItem: () => { throw new Error('storage denied') },
        setItem: () => { throw new Error('storage denied') },
      },
    })
    Object.defineProperty(window.navigator, 'language', { configurable: true, value: 'ko-KR' })
    expect(resolveLocale()).toBe('ko')
    renderHarness()
    expect(screen.getByTestId('locale')).toHaveTextContent('ko')
    act(() => screen.getByRole('button', { name: 'English' }).click())
    expect(screen.getByTestId('locale')).toHaveTextContent('en')
  })

  it('switches presentation in place, updates html lang, and keeps formatter identities stable', () => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, 'en')
    renderHarness()
    expect(screen.getByTestId('label')).toHaveTextContent('Overview')
    expect(screen.getByTestId('rename-title')).toHaveTextContent('Rename RoboRev repository')
    expect(screen.getByTestId('rename-unavailable')).toHaveTextContent('RoboRev is currently unavailable')
    expect(screen.getByTestId('rename-unsupported')).toHaveTextContent('installed RoboRev does not support repository rename')
    expect(screen.getByTestId('move-title')).toHaveTextContent('Repair RoboRev repository path')
    expect(screen.getByTestId('move-warning')).toHaveTextContent('does not move a folder')
    expect(screen.getByTestId('formatted')).toHaveTextContent('2 running')
    expect(document.documentElement.lang).toBe('en')
    act(() => screen.getByRole('button', { name: '한국어' }).click())
    expect(screen.getByTestId('locale')).toHaveTextContent('ko')
    expect(screen.getByTestId('label')).toHaveTextContent('개요')
    expect(screen.getByTestId('rename-title')).toHaveTextContent('RoboRev 저장소 이름 변경')
    expect(screen.getByTestId('rename-unavailable')).toHaveTextContent('현재 RoboRev를 사용할 수 없습니다')
    expect(screen.getByTestId('rename-unsupported')).toHaveTextContent('설치된 RoboRev는 저장소 이름 변경을 지원하지 않습니다')
    expect(screen.getByTestId('move-title')).toHaveTextContent('RoboRev 저장소 경로 복구')
    expect(screen.getByTestId('move-warning')).toHaveTextContent('폴더를 이동하지 않습니다')
    expect(screen.getByTestId('formatted')).toHaveTextContent('2개 실행 중')
    expect(screen.getByTestId('stable-t')).toHaveTextContent('true')
    expect(screen.getByTestId('stable-format')).toHaveTextContent('true')
    expect(document.documentElement.lang).toBe('ko')
    expect(window.localStorage.getItem(LANGUAGE_STORAGE_KEY)).toBe('ko')
  })
})
