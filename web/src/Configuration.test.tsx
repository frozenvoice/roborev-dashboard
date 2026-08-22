import { cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { Configuration, type ConfigurationProps } from './Configuration'
import { I18nProvider, LanguageSelector } from './i18n'

function response(value: unknown, status = 200) {
  return Promise.resolve(new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } }))
}

const snapshot = {
  repositoryIdentity: 'repo-1',
  settings: [
    { key: 'max_workers', valueType: 'integer', scopes: ['global'], min: 1, max: 32 },
    { key: 'review_context_count', valueType: 'integer', scopes: ['global', 'local'], min: 0, max: 20 },
    { key: 'auto_close_passing_reviews', valueType: 'boolean', scopes: ['global', 'local'] },
    { key: 'review_reasoning', valueType: 'enum', scopes: ['global', 'local'], enumValues: ['fast', 'standard', 'medium', 'thorough', 'maximum'] },
    { key: 'review_min_severity', valueType: 'enum', scopes: ['global', 'local'], enumValues: ['', 'critical', 'high', 'medium', 'low'] },
    { key: 'fix_min_severity', valueType: 'enum', scopes: ['global', 'local'], enumValues: ['', 'critical', 'high', 'medium', 'low'] },
    { key: 'refine_reasoning', valueType: 'enum', scopes: ['global', 'local'], enumValues: ['fast', 'standard', 'medium', 'thorough', 'maximum'] },
    { key: 'refine_min_severity', valueType: 'enum', scopes: ['global', 'local'], enumValues: ['', 'critical', 'high', 'medium', 'low'] },
  ],
  rows: [
    { origin: 'default', key: 'max_workers', value: '4' },
    { origin: 'local', key: 'review_reasoning', value: 'standard' },
    // Sensitive rows arrive already masked by RoboRev; the UI displays them verbatim.
    { origin: 'global', key: 'anthropic_api_key', value: '****abcd' },
    { origin: 'default', key: 'review_min_severity', value: '' },
  ],
}

const preview = {
  previewToken: 'token-1',
  key: 'review_reasoning',
  scope: 'local',
  currentValue: 'standard',
  currentOrigin: 'local',
  newValue: 'thorough',
  command: { executable: 'C:\\Tools\\roborev.exe', args: ['config', 'set', 'review_reasoning', 'thorough', '--local'] },
  warnings: ['local_toml_modified', 'tracked_dirty', 'normalize'],
}

function renderConfiguration(overrides?: Partial<ConfigurationProps>) {
  const onToast = vi.fn()
  const props: ConfigurationProps = { repositoryIdentity: 'repo-1', configEnabled: true, onToast, ...overrides }
  const view = render(<I18nProvider><Configuration {...props} /></I18nProvider>)
  return { view, onToast }
}

afterEach(() => { cleanup(); vi.restoreAllMocks() })


describe('Configuration workspace', () => {
  it('renders effective rows with origin badges and masked values verbatim', async () => {
    vi.stubGlobal('fetch', vi.fn(() => response(snapshot)))
    renderConfiguration()
    expect(await screen.findByTestId('value-max_workers')).toHaveTextContent('4')
    expect(screen.getByTestId('value-anthropic_api_key')).toHaveTextContent('****abcd')
    const table = screen.getByTestId('effective-config')
    expect(table).toHaveTextContent('Repository')
    expect(table).toHaveTextContent('Default')
    expect(screen.getByText('review_reasoning')).toBeInTheDocument()
  })

  it('edits only the allowlisted settings and keeps raw rows display-only', async () => {
    vi.stubGlobal('fetch', vi.fn(() => response(snapshot)))
    renderConfiguration()
    expect(await screen.findByTestId('setting-review_reasoning')).toBeInTheDocument()
    expect(screen.queryByTestId('setting-anthropic_api_key')).not.toBeInTheDocument()
    expect(screen.queryByTestId('setting-allow_unsafe_agents')).not.toBeInTheDocument()
    // The sensitive row is rendered only inside the read-only table.
    const editors = document.querySelectorAll('.setting-card input, .setting-card select')
    for (const editor of editors) {
      expect(editor.closest('.setting-card')?.textContent).not.toContain('anthropic_api_key')
    }
  })

  it('restricts max_workers to the global scope', async () => {
    vi.stubGlobal('fetch', vi.fn(() => response(snapshot)))
    renderConfiguration()
    const card = await screen.findByTestId('setting-max_workers')
    const localRadio = withinCard(card, 'Repository')
    expect(localRadio).toBeDisabled()
    expect(withinCard(card, 'Global configuration')).toBeEnabled()
  })

  function withinCard(card: HTMLElement, label: string) {
    const radios = Array.from(card.querySelectorAll('input[type="radio"]'))
    const match = radios.find(radio => radio.closest('label')?.textContent === label)
    if (!match) throw new Error(`radio ${label} not found`)
    return match as HTMLInputElement
  }

  it('validates integer bounds before offering a preview', async () => {
    vi.stubGlobal('fetch', vi.fn(() => response(snapshot)))
    const user = userEvent.setup()
    renderConfiguration()
    const card = await screen.findByTestId('setting-max_workers')
    const input = card.querySelector('input[type="number"]') as HTMLInputElement
    await user.clear(input)
    await user.type(input, '99')
    expect(screen.getAllByRole('button', { name: 'Prepare change' }).find(button => (button as HTMLButtonElement).closest('.setting-card') === card)).toBeDisabled()
  })

  it('previews an exact argv, requires explicit confirmation, and posts once', async () => {
    const calls: Array<{ path: string; init?: RequestInit }> = []
    let setPosts = 0
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      calls.push({ path, init })
      if (path === '/api/configuration/preview') return response(preview)
      if (path === '/api/configuration/set') { setPosts++; return response({ status: 'succeeded', key: 'review_reasoning', scope: 'local', value: 'thorough', verifiedValue: 'thorough', rows: snapshot.rows }) }
      return response(snapshot)
    }))
    const user = userEvent.setup()
    renderConfiguration()
    const card = await screen.findByTestId('setting-review_reasoning')
    const select = card.querySelector('select') as HTMLSelectElement
    await user.selectOptions(select, 'thorough')
    const prepare = screen.getAllByRole('button', { name: 'Prepare change' }).find(button => (button as HTMLButtonElement).closest('.setting-card') === card) as HTMLButtonElement
    await user.click(prepare)

    const previewCall = calls.find(call => call.path === '/api/configuration/preview')
    expect(JSON.parse(String(previewCall?.init?.body))).toEqual({ repositoryIdentity: 'repo-1', key: 'review_reasoning', scope: 'local', value: 'thorough' })
    const dialog = screen.getByRole('dialog')
    expect(dialog).toHaveTextContent('roborev.exe')
    expect(JSON.parse(screen.getByTestId('preview-command').textContent!)).toEqual(['C:\\Tools\\roborev.exe', 'config', 'set', 'review_reasoning', 'thorough', '--local'])
    expect(dialog).toHaveTextContent('.roborev.toml will be modified.')

    // Cancel never mutates.
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    expect(setPosts).toBe(0)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()

    // Re-prepare then confirm twice quickly: exactly one POST.
    await user.click(prepare)
    const confirm = await within(screen.getByRole('dialog')).findByRole('button', { name: 'Apply setting' })
    await user.click(confirm)
    await user.click(confirm)
    await waitFor(() => expect(setPosts).toBe(1))
    expect(calls.some(call => call.path === '/api/configuration/set')).toBe(true)
  })

  function within(element: HTMLElement) {
    // Local helper avoids importing @testing-library/react's `within` twice in this file.
    return {
      getByRole: (role: string, options?: { name?: RegExp | string }) => {
        const candidates = Array.from(element.querySelectorAll<HTMLElement>('button, [role="button"]'))
        const match = candidates.find(node =>
          node.getAttribute('role') === role || node.tagName === 'BUTTON'
            ? (!options?.name || node.textContent?.match(options.name instanceof RegExp ? options.name : new RegExp(options.name)))
            : false,
        )
        if (!match) throw new Error(`role ${role} not found in dialog`)
        return match
      },
      findByRole: async (role: string, options?: { name?: RegExp | string }) => {
        await waitFor(() => expect(within(element).getByRole(role, options)).toBeInTheDocument())
        return within(element).getByRole(role, options)
      },
    }
  }

  it('surfaces the ambiguous result without claiming the value did not change', async () => {
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      if (path === '/api/configuration/preview') return response(preview)
      if (path === '/api/configuration/set') return response({ status: 'unverified', message: 'could not verify', key: 'review_reasoning', scope: 'local', value: 'thorough', rows: snapshot.rows })
      return response(snapshot)
    }))
    const user = userEvent.setup()
    const { onToast } = renderConfiguration()
    const card = await screen.findByTestId('setting-review_reasoning')
    await user.selectOptions(card.querySelector('select') as HTMLSelectElement, 'thorough')
    await user.click(screen.getAllByRole('button', { name: 'Prepare change' }).find(button => (button as HTMLButtonElement).closest('.setting-card') === card) as HTMLButtonElement)
    await user.click(screen.getByRole('button', { name: 'Apply setting' }))
    await waitFor(() => expect(onToast).toHaveBeenCalledWith('warning', expect.stringContaining('could not be fully verified')))
  })

  it('discards a stale snapshot after a repository switch', async () => {
    const stale = deferred<typeof snapshot>()
    let currentIdentity = 'repo-1'
    vi.stubGlobal('fetch', vi.fn((input: RequestInfo | URL) => {
      if (String(input).startsWith('/api/configuration?')) {
        if (currentIdentity === 'repo-b') return response({ repositoryIdentity: 'repo-b', settings: snapshot.settings, rows: [{ origin: 'global', key: 'max_workers', value: '8' }] })
        if (String(input).includes('repo-stale')) return stale.promise
        return response(snapshot)
      }
      return response({})
    }))
    const { rerender } = render(<I18nProvider><Configuration repositoryIdentity={currentIdentity} configEnabled onToast={vi.fn()} /></I18nProvider>)
    expect(await screen.findByTestId('value-max_workers')).toHaveTextContent('4')

    currentIdentity = 'repo-b'
    rerender(<I18nProvider><Configuration repositoryIdentity={currentIdentity} configEnabled onToast={vi.fn()} /></I18nProvider>)
    expect(await screen.findByTestId('value-max_workers')).toHaveTextContent('8')

    // The stale repo-1 response must never replace repo-b's rows.
    stale.resolve(snapshot)
    await new Promise(resolve => setTimeout(resolve, 0))
    expect(screen.getByTestId('value-max_workers')).toHaveTextContent('8')
    expect(screen.queryByText('****abcd')).not.toBeInTheDocument()
  })

  it('does not re-run configuration requests when the language changes', async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => String(input).startsWith('/api/configuration?') ? response(snapshot) : response({}))
    vi.stubGlobal('fetch', fetchMock)
    const user = userEvent.setup()
    render(<I18nProvider><LanguageSelector /><Configuration repositoryIdentity='repo-1' configEnabled onToast={vi.fn()} /></I18nProvider>)
    await screen.findByTestId('value-max_workers')
    const before = fetchMock.mock.calls.length
    await user.selectOptions(screen.getByLabelText('Select language'), 'ko')
    await screen.findByText('적용된 구성')
    await new Promise(resolve => setTimeout(resolve, 50))
    expect(fetchMock.mock.calls.length).toBe(before)
  })
})

type Deferred<T> = { promise: Promise<T>; resolve: (value: T) => void }
function deferred<T>(): Deferred<T> {
  let resolve!: (value: T) => void
  const promise = new Promise<T>(res => { resolve = res })
  return { promise, resolve }
}
