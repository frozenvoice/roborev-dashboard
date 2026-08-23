/// <reference types="vite/client" />

import stylesheet from './styles.css?raw'
import { describe, expect, it } from 'vitest'

function declaration(selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  return stylesheet.match(new RegExp(`\\.${escaped}[^\\{]*\\{[^}]*\\}`))?.[0] ?? ''
}

describe('setup theme stylesheet contract', () => {
  it('uses the semantic panel token for large setup surfaces', () => {
    for (const selector of ['setup-runtime', 'configured-agents', 'setup-facts', 'setup-safety']) {
      const rule = declaration(selector)
      expect(rule, `missing .${selector} declaration`).toContain('background:var(--panel)')
      expect(rule).not.toMatch(/background:(?:white|#fff(?:fff)?)(?:[;}])/i)
    }
  })

  it('uses dedicated light/dark tokens for preview and progress track', () => {
    expect(stylesheet).toContain('--setup-runtime-preview:#f8f9fb')
    expect(stylesheet).toContain('--setup-progress-track:#e8edf1')
    expect(stylesheet).toContain('--setup-runtime-preview:#202831')
    expect(stylesheet).toContain('--setup-progress-track:#303943')

    expect(declaration('runtime-command-preview')).toContain('background:var(--setup-runtime-preview)')
    expect(declaration('setup-progress-track')).toContain('background:var(--setup-progress-track)')
    expect(declaration('runtime-command-preview')).not.toMatch(/background:(?:white|#f8f9fb)(?:[;}])/i)
    expect(declaration('setup-progress-track')).not.toMatch(/background:(?:white|#e8edf1)(?:[;}])/i)
  })

  it('keeps the dark token overrides inside the dark media query', () => {
    const darkTheme = stylesheet.match(/@media\(prefers-color-scheme:dark\)\{([\s\S]*?)\}\s*\.setup-progress/)?.[1] ?? ''
    expect(darkTheme).toContain('--panel:#1a2129')
    expect(darkTheme).toContain('--setup-runtime-preview:#202831')
    expect(darkTheme).toContain('--setup-progress-track:#303943')
  })

  it('keeps the disabled Analyze execution state readable in light and dark themes', () => {
    const disabledState = declaration('analyze-execution-disabled')
    const disabledButton = declaration('analyze-execution-disabled .button[disabled]')
    expect(disabledState).toContain('background:var(--setup-runtime-preview)')
    expect(disabledButton).toContain('background:var(--panel)')
    expect(disabledButton).toContain('color:var(--muted)')
    expect(disabledButton).toContain('opacity:1')
    expect(stylesheet).toContain('--setup-runtime-preview:#202831')
    expect(stylesheet).not.toContain('.analyze-scope')
    expect(stylesheet).not.toContain('.analyze-command-preview')
    expect(stylesheet).not.toContain('.analyze-receipt')
    expect(stylesheet).not.toContain('.analyze-dialog')
  })

  it('keeps Analyze usable at a 390px viewport through its mobile media contract', () => {
    const mobileMatch = stylesheet.match(/@media\(max-width:(\d+)px\)\{([\s\S]*)\}\s*$/)
    expect(mobileMatch, 'missing Analyze mobile media query').not.toBeNull()
    expect(Number(mobileMatch?.[1])).toBeGreaterThanOrEqual(390)

    const mobileAnalyze = mobileMatch?.[2] ?? ''
    expect(mobileAnalyze).toContain('.analyze-page{padding:23px 12px}')
    expect(mobileAnalyze).toContain('.analyze-grid{grid-template-columns:1fr}')
    expect(mobileAnalyze).toContain('.analyze-inline-actions{justify-content:stretch}')
    expect(mobileAnalyze).toContain('.analyze-inline-actions .button{width:100%}')
    expect(mobileAnalyze).toContain('.analyze-execution-disabled .button[disabled]{min-height:40px}')
  })

  it('contains narrow Stats grid panels while preserving internal table scrolling', () => {
    expect(stylesheet).toContain('@media(max-width:390px){.stats-two-column{min-width:0}.stats-two-column>.stats-panel{min-width:0;max-width:100%}.stats-two-column>.stats-panel .stats-table-scroll{min-width:0;max-width:100%;overflow-x:auto}}')
  })

  it('keeps the tracked repository inventory readable on mobile', () => {
    expect(stylesheet).toContain('.tracked-table-wrap{overflow:auto')
    expect(stylesheet).toContain('.tracked-table td code{font:11px/1.45')
    expect(stylesheet).toContain('overflow-wrap:anywhere')
    expect(stylesheet).toContain('@media(max-width:720px){.tracked-repositories{padding:13px}')
  })
})
