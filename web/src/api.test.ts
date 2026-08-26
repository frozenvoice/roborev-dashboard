import { afterEach, describe, expect, it, vi } from 'vitest'
import { fetchCIWorkflowPreview, normalizeCIWorkflowPreview, normalizeRepositoryInventoryResponse, normalizeRepositoryMergeEligibility, normalizeRepositoryMergePreview, normalizeRepositoryMergeResult, normalizeRepositoryMoveEligibility, normalizeRepositoryMovePreview, normalizeRepositoryMoveResult, normalizeRepositoryRenamePreview, normalizeRepositoryRenameResult, normalizeRuntimeDiagnostics, setCSRF, validateRepositoryRenameName } from './api'

const ciContent = 'name: RoboRev\non:\n  pull_request:\n    branches: [main]\n'
const ciPreview = { version: 'v0.64.0', repositoryIdentity: 'repo-1', target: '.github/workflows/roborev.yml', workflow: { content: ciContent, sha256: 'a'.repeat(64) } }

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })

describe('CI workflow preview API contract', () => {
  it('accepts only the fixed target/version, matching identity, safe UTF-8 content, and lowercase hash', () => {
    expect(normalizeCIWorkflowPreview(ciPreview, 'repo-1')).toEqual(ciPreview)
    expect(normalizeCIWorkflowPreview({ ...ciPreview, repositoryIdentity: 'other' }, 'repo-1')).toBeNull()
    expect(normalizeCIWorkflowPreview({ ...ciPreview, target: '.github/workflows/other.yml' }, 'repo-1')).toBeNull()
    expect(normalizeCIWorkflowPreview({ ...ciPreview, version: '0.64.0' }, 'repo-1')).toBeNull()
    expect(normalizeCIWorkflowPreview({ ...ciPreview, workflow: { ...ciPreview.workflow, sha256: 'A'.repeat(64) } }, 'repo-1')).toBeNull()
    expect(normalizeCIWorkflowPreview({ ...ciPreview, workflow: { ...ciPreview.workflow, sha256: 'a'.repeat(63) } }, 'repo-1')).toBeNull()
    expect(normalizeCIWorkflowPreview({ ...ciPreview, targetStatus: 'absent' }, 'repo-1')).toBeNull()
    expect(normalizeCIWorkflowPreview({ ...ciPreview, workflow: { ...ciPreview.workflow, content: 'bad\u0000content' } }, 'repo-1')).toBeNull()
    expect(normalizeCIWorkflowPreview({ ...ciPreview, workflow: { ...ciPreview.workflow, content: 'bad\u0001content' } }, 'repo-1')).toBeNull()
    expect(normalizeCIWorkflowPreview({ ...ciPreview, workflow: { ...ciPreview.workflow, content: 'bad\u007fcontent' } }, 'repo-1')).toBeNull()
    expect(normalizeCIWorkflowPreview({ ...ciPreview, workflow: { ...ciPreview.workflow, content: '\ud800' } }, 'repo-1')).toBeNull()
    expect(normalizeCIWorkflowPreview({ ...ciPreview, workflow: { ...ciPreview.workflow, content: '' } }, 'repo-1')).toBeNull()
  })

  it('rejects extra/private fields and workflows over the UTF-8 byte limit', () => {
    expect(normalizeCIWorkflowPreview({ ...ciPreview, privatePath: 'C:\\secret' }, 'repo-1')).toBeNull()
    expect(normalizeCIWorkflowPreview({ ...ciPreview, workflow: { ...ciPreview.workflow, stdout: 'secret' } }, 'repo-1')).toBeNull()
    expect(normalizeCIWorkflowPreview({ ...ciPreview, workflow: { content: ciContent, sha256: ciPreview.workflow.sha256, extra: true } }, 'repo-1')).toBeNull()
    expect(normalizeCIWorkflowPreview({ ...ciPreview, workflow: { ...ciPreview.workflow, content: 'x'.repeat(256 * 1024 + 1) } }, 'repo-1')).toBeNull()
  })

  it('POSTs exactly the browser identity and no browser-selected fields', async () => {
    setCSRF('test-csrf')
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify(ciPreview), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(fetchCIWorkflowPreview('repo-1')).resolves.toEqual(ciPreview)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [path, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(path).toBe('/api/ci-workflow/preview')
    expect(init.method).toBe('POST')
    expect(JSON.parse(String(init.body))).toEqual({ repositoryIdentity: 'repo-1' })
    expect(Object.keys(JSON.parse(String(init.body)))).toEqual(['repositoryIdentity'])
    expect(new Headers(init.headers).get('X-CSRF-Token')).toBe('test-csrf')
  })
})

const row = { name: 'repo', path: 'E:\\repo', jobCount: 2, dashboardRegistered: true }

const diagnostics = {
  daemon: { running: true, version: 'v0.64.0', uptime: '2h' },
  workers: { active: 1, max: 4 },
  queue: { paused: false, queued: 2, running: 1 },
  jobs: { completed: 100, failed: 3, skipped: 4 },
  health: { healthy: true, errorCount24h: 0, components: [{ name: 'database', healthy: true }] },
}

describe('runtime diagnostics API normalization', () => {
  it('accepts the exact Dashboard DTO', () => {
    expect(normalizeRuntimeDiagnostics(diagnostics)).toEqual(diagnostics)
  })

  it('rejects unknown fields, unsafe counters, invalid worker bounds, and duplicate components', () => {
    const invalid: unknown[] = [
      { ...diagnostics, extra: true },
      { ...diagnostics, daemon: { ...diagnostics.daemon, version: ' ' } },
      { ...diagnostics, workers: { active: 5, max: 4 } },
      { ...diagnostics, workers: { active: 0, max: 0 } },
      { ...diagnostics, jobs: { ...diagnostics.jobs, failed: -1 } },
      { ...diagnostics, health: { ...diagnostics.health, components: [{ name: 'database', healthy: true, extra: false }] } },
      { ...diagnostics, health: { ...diagnostics.health, components: [{ name: 'database', healthy: true }, { name: 'database', healthy: false }] } },
      { ...diagnostics, health: { ...diagnostics.health, components: [{ name: 'database', healthy: false }] } },
      { ...diagnostics, health: { ...diagnostics.health, errorCount24h: Number.MAX_SAFE_INTEGER + 1 } },
    ]
    for (const value of invalid) expect(normalizeRuntimeDiagnostics(value)).toBeNull()
    const inherited = Object.create({ extra: true }) as Record<string, unknown>
    Object.assign(inherited, diagnostics)
    expect(normalizeRuntimeDiagnostics(inherited)).toBeNull()
  })
})

describe('repository inventory API normalization', () => {
  it('accepts the typed empty and populated DTOs while preserving order and duplicate names', () => {
    expect(normalizeRepositoryInventoryResponse({ repositories: [], repositoryCount: 0, totalJobCount: 0 })).toEqual({ repositories: [], repositoryCount: 0, totalJobCount: 0 })
    const second = { ...row, path: 'E:\\other', jobCount: 0, dashboardRegistered: false }
    expect(normalizeRepositoryInventoryResponse({ repositories: [row, { ...second, name: row.name }], repositoryCount: 2, totalJobCount: 2 })).toEqual({ repositories: [row, { ...second, name: row.name }], repositoryCount: 2, totalJobCount: 2 })
  })

  it('rejects malformed, mismatched, unsafe, and unknown fields', () => {
    const valid = { repositories: [row], repositoryCount: 1, totalJobCount: 2 }
    const invalid = [
      null,
      [],
      { ...valid, repositoryCount: 2 },
      { ...valid, totalJobCount: 1 },
      { ...valid, extra: true },
      { ...valid, repositories: [{ ...row, extra: true }] },
      { ...valid, repositories: [{ ...row, path: '' }] },
      { ...valid, repositories: [{ ...row, jobCount: -1 }] },
      { ...valid, repositories: [{ ...row, jobCount: 1.5 }], totalJobCount: 1.5 },
      { repositories: [{ ...row, jobCount: Number.MAX_SAFE_INTEGER + 1 }], repositoryCount: 1, totalJobCount: Number.MAX_SAFE_INTEGER + 1 },
    ]
    for (const value of invalid) expect(normalizeRepositoryInventoryResponse(value)).toBeNull()
  })

  it('preserves upstream string values without browser path normalization', () => {
    const value = { repositories: [{ ...row, name: '', path: '  E:\\repo\\  ' }], repositoryCount: 1, totalJobCount: 2 }
    expect(normalizeRepositoryInventoryResponse(value)?.repositories[0]).toEqual(value.repositories[0])
  })
})

describe('repository rename API normalization and validation', () => {
  const preview = { path: 'E:\\tracked', currentName: 'Old', newName: '새 이름', jobCount: 4, dashboardRegistered: true, command: { executable: 'roborev.exe', args: ['repo', 'rename', 'E:\\tracked', '새 이름'] }, token: 'token', expiresAt: '2026-08-23T01:02:03Z' }

  it('accepts the strict preview and receipt DTOs while preserving raw path/argv', () => {
    expect(normalizeRepositoryRenamePreview(preview)).toEqual(preview)
    expect(normalizeRepositoryRenameResult({ status: 'renamed', path: 'E:\\tracked', previousName: 'Old', name: '새 이름' })).toEqual({ status: 'renamed', path: 'E:\\tracked', previousName: 'Old', name: '새 이름' })
    expect(normalizeRepositoryRenameResult({ status: 'unverified', path: 'E:\\tracked', message: 'refresh' })).toEqual({ status: 'unverified', path: 'E:\\tracked', message: 'refresh' })
  })

  it('rejects unknown fields, malformed command/expiry, and unsafe rename receipts', () => {
    expect(normalizeRepositoryRenamePreview({ ...preview, identity: 'secret' })).toBeNull()
    expect(normalizeRepositoryRenamePreview({ ...preview, command: { ...preview.command, args: [] } })).toBeNull()
    expect(normalizeRepositoryRenamePreview({ ...preview, expiresAt: 'not-a-date' })).toBeNull()
    expect(normalizeRepositoryRenameResult({ status: 'renamed', path: 'E:\\tracked', previousName: 'Old', name: '새 이름', jobCount: 4 })).toBeNull()
    expect(normalizeRepositoryRenameResult({ status: 'unverified', path: 'E:\\tracked', message: '' })).toBeNull()
  })

  it('enforces trim, Unicode code-point, control, leading-dash, and same-name policy', () => {
    expect(validateRepositoryRenameName('  새 이름  ', 'Old')).toEqual({ value: '새 이름' })
    expect(validateRepositoryRenameName('Old', ' Old ')).toEqual({ value: 'Old' })
    expect(validateRepositoryRenameName('', 'Old').error).toBe('empty')
    expect(validateRepositoryRenameName('😀'.repeat(129), 'Old').error).toBe('tooLong')
    expect(validateRepositoryRenameName('bad\nname', 'Old').error).toBe('control')
    expect(validateRepositoryRenameName('-new', 'Old').error).toBe('leadingDash')
    expect(validateRepositoryRenameName('Old', 'Old').error).toBe('same')
    expect(validateRepositoryRenameName('\ud800', 'Old').error).toBe('invalidUtf8')
  })
})

describe('repository move API normalization', () => {
  const preview = {
    sourcePath: 'E:/old/project', sourceName: 'old-name', jobCount: 4,
    destinationRepositoryIdentity: 'opaque-destination', destinationPath: 'E:\\new\\project', destinationName: 'new-name',
    command: { executable: 'roborev.exe', args: ['repo', 'move', 'E:/old/project', '.'], workingDirectory: 'E:\\new\\project' },
    token: 'move-token', expiresAt: '2026-08-23T01:02:03Z',
  }

  it('accepts the strict preview and moved/unverified receipts while preserving raw strings', () => {
    expect(normalizeRepositoryMovePreview(preview)).toEqual(preview)
    expect(normalizeRepositoryMoveResult({ status: 'moved', sourcePath: 'E:/old/project', destinationPath: 'E:\\new\\project' })).toEqual({ status: 'moved', sourcePath: 'E:/old/project', destinationPath: 'E:\\new\\project' })
    expect(normalizeRepositoryMoveResult({ status: 'unverified', sourcePath: 'E:/old/project', destinationPath: 'E:\\new\\project', message: 'refresh' })).toEqual({ status: 'unverified', sourcePath: 'E:/old/project', destinationPath: 'E:\\new\\project', message: 'refresh' })
  })

  it('accepts only the exact lazy eligibility union and preserves canonical source values', () => {
    expect(normalizeRepositoryMoveEligibility({ eligible: true, sourcePath: 'E:\\canonical', sourceName: 'canonical' })).toEqual({ eligible: true, sourcePath: 'E:\\canonical', sourceName: 'canonical' })
    expect(normalizeRepositoryMoveEligibility({ eligible: true, sourcePath: 'E:\\canonical', sourceName: '' })).toEqual({ eligible: true, sourcePath: 'E:\\canonical', sourceName: '' })
    expect(normalizeRepositoryMoveEligibility({ eligible: false, reason: 'source-exists' })).toEqual({ eligible: false, reason: 'source-exists' })
    expect(normalizeRepositoryMoveEligibility({ eligible: false, reason: 'dashboard-registered' })).toEqual({ eligible: false, reason: 'dashboard-registered' })
  })

  it('rejects unknown fields and unsafe or malformed eligibility DTOs', () => {
    const invalid: unknown[] = [
      null, [], {}, { eligible: 'true', sourcePath: 'E:\\canonical', sourceName: 'canonical' },
      { eligible: true, sourcePath: '', sourceName: 'canonical' },
      { eligible: true, sourcePath: 'E:\\canonical' },
      { eligible: true, sourcePath: 'E:\\canonical', sourceName: 'canonical', extra: true },
      { eligible: false }, { eligible: false, reason: 'unknown' },
      { eligible: false, reason: 'source-exists', sourcePath: 'E:\\canonical' },
    ]
    for (const value of invalid) expect(normalizeRepositoryMoveEligibility(value)).toBeNull()
    const inherited = Object.create({ eligible: false, reason: 'source-exists' }) as Record<string, unknown>
    expect(normalizeRepositoryMoveEligibility(inherited)).toBeNull()
  })

  it('rejects unknown fields, missing cwd, malformed identity and unsafe receipts', () => {
    expect(normalizeRepositoryMovePreview({ ...preview, extra: true })).toBeNull()
    expect(normalizeRepositoryMovePreview({ ...preview, destinationRepositoryIdentity: '' })).toBeNull()
    expect(normalizeRepositoryMovePreview({ ...preview, command: { ...preview.command, workingDirectory: '' } })).toBeNull()
    expect(normalizeRepositoryMovePreview({ ...preview, command: { ...preview.command, extra: true } })).toBeNull()
    expect(normalizeRepositoryMoveResult({ status: 'moved', sourcePath: 'E:/old/project', destinationPath: 'E:\\new\\project', message: 'unexpected' })).toBeNull()
    expect(normalizeRepositoryMoveResult({ status: 'unverified', sourcePath: 'E:/old/project', destinationPath: 'E:\\new\\project', message: '' })).toBeNull()
  })
})

describe('repository merge API normalization', () => {
  const source = { path: 'E:/old/project', name: 'old-name', jobCount: 7 }
  const target = { repositoryIdentity: 'dashboard-target', path: 'E:\\new\\project', name: 'new-name', jobCount: 3 }
  const preview = { source, target, command: { executable: 'roborev.exe', args: ['repo', 'merge', '--yes', source.path, target.path], workingDirectory: '' }, token: 'merge-token', expiresAt: '2026-08-23T01:02:03Z' }

  it('accepts server-provided source and opaque target candidates', () => {
    expect(normalizeRepositoryMergeEligibility({ eligible: true, source, targets: [target] })).toEqual({ eligible: true, source, targets: [target] })
    expect(normalizeRepositoryMergePreview(preview)).toEqual(preview)
    expect(normalizeRepositoryMergeResult({ status: 'merged' })).toEqual({ status: 'merged' })
    expect(normalizeRepositoryMergeResult({ status: 'unverified', message: 'refresh' })).toEqual({ status: 'unverified', message: 'refresh' })
  })

  it('accepts every ineligible reason and rejects unknown fields, identities, counts, and command drift', () => {
    for (const reason of ['source-exists', 'dashboard-registered', 'source-empty', 'active-source-jobs', 'no-targets'] as const) {
      expect(normalizeRepositoryMergeEligibility({ eligible: false, reason })).toEqual({ eligible: false, reason })
    }
    const invalid: unknown[] = [
      { eligible: true, source, targets: [] },
      { eligible: true, source, targets: [{ ...target, repositoryIdentity: '' }] },
      { eligible: true, source, targets: [target, { ...target, path: 'E:\\other\\project' }] },
      { eligible: true, source: { ...source, jobCount: Number.MAX_SAFE_INTEGER + 1 }, targets: [target] },
      { eligible: false, reason: 'unknown' },
      { ...preview, extra: true },
      { ...preview, target: { ...target, repositoryIdentity: '' } },
      { ...preview, command: { ...preview.command, workingDirectory: 'C:\\' } },
      { ...preview, command: { ...preview.command, args: ['repo', 'merge'] } },
      { status: 'unverified', message: '' },
      { status: 'merged', message: 'unexpected' },
    ]
    for (const value of invalid) {
      expect(normalizeRepositoryMergeEligibility(value)).toBeNull()
      expect(normalizeRepositoryMergePreview(value)).toBeNull()
      expect(normalizeRepositoryMergeResult(value)).toBeNull()
    }
  })
})
