import { afterEach, describe, expect, it, vi } from 'vitest'
import { executeCIWorkflow, executeRepositoryTracking, executeSnapshotIgnore, fetchCIWorkflowPreview, fetchRepositorySetupReadiness, fetchRepositoryTrackingPreview, fetchSnapshotIgnorePreview, normalizeCIWorkflowExecuteResult, normalizeCIWorkflowPreview, normalizeRepositoryInventoryResponse, normalizeRepositoryMergeEligibility, normalizeRepositoryMergePreview, normalizeRepositoryMergeResult, normalizeRepositoryMoveEligibility, normalizeRepositoryMovePreview, normalizeRepositoryMoveResult, normalizeRepositoryRenamePreview, normalizeRepositoryRenameResult, normalizeRepositorySetupReadiness, normalizeRepositoryTrackingExecuteResult, normalizeRepositoryTrackingPreview, normalizeRuntimeDiagnostics, normalizeSnapshotIgnoreExecuteResult, normalizeSnapshotIgnorePreview, setCSRF, validateRepositoryRenameName } from './api'

const ciContent = 'name: RoboRev\non:\n  pull_request:\n    branches: [main]\n'
const ciPreview = { version: 'v0.64.0', repositoryIdentity: 'repo-1', target: '.github/workflows/roborev.yml', workflow: { content: ciContent, sha256: 'a'.repeat(64) }, write: { status: 'available' as const, token: 'opaque-create-token', expiresAt: '2099-01-01T00:00:00.000Z' } }

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

  it('requires exact write availability unions and rejects private fields', () => {
    for (const comparison of ['exact-match', 'different', 'unavailable'] as const) {
      const value = normalizeCIWorkflowPreview({ ...ciPreview, write: { status: 'exists' }, existing: { comparison } }, 'repo-1')
      expect(value?.write).toEqual({ status: 'exists' })
      expect(value?.existing).toEqual({ comparison })
    }
    expect(normalizeCIWorkflowPreview({ ...ciPreview, write: { status: 'exists' } }, 'repo-1')).toBeNull()
    expect(normalizeCIWorkflowPreview({ ...ciPreview, write: { status: 'exists' }, existing: { comparison: 'unknown' } }, 'repo-1')).toBeNull()
    expect(normalizeCIWorkflowPreview({ ...ciPreview, write: { status: 'exists' }, existing: { comparison: 'different', content: 'private' } }, 'repo-1')).toBeNull()
    expect(normalizeCIWorkflowPreview({ ...ciPreview, write: { status: 'exists' }, existing: { comparison: 'different', path: 'C:\\secret' } }, 'repo-1')).toBeNull()
    expect(normalizeCIWorkflowPreview({ ...ciPreview, write: { status: 'exists' }, existing: { comparison: 'different', sha256: 'a'.repeat(64) } }, 'repo-1')).toBeNull()
    expect(normalizeCIWorkflowPreview({ ...ciPreview, write: { status: 'exists' }, existing: { comparison: 'different', diff: 'private' } }, 'repo-1')).toBeNull()
    expect(normalizeCIWorkflowPreview({ ...ciPreview, write: { status: 'available' }, existing: { comparison: 'exact-match' } }, 'repo-1')).toBeNull()
    for (const status of ['dirty', 'ignored', 'unsafe'] as const) {
      expect(normalizeCIWorkflowPreview({ ...ciPreview, write: { status }, existing: { comparison: 'unavailable' } }, 'repo-1')).toBeNull()
    }
    for (const write of [
      { status: 'available', token: 'x', expiresAt: '2099-01-01T00:00:00.000Z', extra: true },
      { status: 'available', token: '', expiresAt: '2099-01-01T00:00:00.000Z' },
      { status: 'exists', token: 'private' },
      { status: 'unknown' },
    ]) expect(normalizeCIWorkflowPreview({ ...ciPreview, write }, 'repo-1')).toBeNull()
  })

  it('POSTs exactly the opaque token for execute and strictly normalizes the result', async () => {
    setCSRF('test-csrf')
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ status: 'verified-created' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(executeCIWorkflow('opaque-create-token')).resolves.toEqual({ status: 'verified-created' })
    const [path, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(path).toBe('/api/ci-workflow/execute')
    expect(JSON.parse(String(init.body))).toEqual({ token: 'opaque-create-token' })
    expect(Object.keys(JSON.parse(String(init.body)))).toEqual(['token'])
    expect(new Headers(init.headers).get('X-CSRF-Token')).toBe('test-csrf')
    expect(normalizeCIWorkflowExecuteResult({ status: 'failed-unverified' })).toEqual({ status: 'failed-unverified' })
    expect(normalizeCIWorkflowExecuteResult({ status: 'verified-created', token: 'secret' })).toBeNull()
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

describe('repository setup readiness API normalization', () => {
  const alias = 'A'.repeat(43)
  const otherAlias = 'B'.repeat(43)
  const valid = {
    version: 'v0.64.0', repositoryIdentity: alias, overall: 'ready' as const,
    configuration: { status: 'ready' as const }, snapshot: { status: 'ready' as const },
    gitHooks: { status: 'ready' as const }, daemon: { status: 'online' as const }, tracking: { status: 'tracked' as const },
  }

  it('accepts every enum through a consistent derived state', () => {
    expect(normalizeRepositorySetupReadiness(valid, valid.repositoryIdentity)).toEqual(valid)
    for (const status of ['not-ignored', 'tracked-files'] as const) {
      expect(normalizeRepositorySetupReadiness({ ...valid, overall: 'needs-attention', snapshot: { status } })).not.toBeNull()
    }
    expect(normalizeRepositorySetupReadiness({ ...valid, overall: 'unavailable', snapshot: { status: 'unsafe' } })).not.toBeNull()
    expect(normalizeRepositorySetupReadiness({ ...valid, overall: 'unavailable', snapshot: { status: 'unavailable' } })).not.toBeNull()
    for (const status of ['needs-attention', 'not-installed'] as const) {
      expect(normalizeRepositorySetupReadiness({ ...valid, overall: 'needs-attention', gitHooks: { status } })).not.toBeNull()
    }
    expect(normalizeRepositorySetupReadiness({ ...valid, overall: 'unavailable', gitHooks: { status: 'unknown' } })).not.toBeNull()
    expect(normalizeRepositorySetupReadiness({ ...valid, overall: 'needs-attention', daemon: { status: 'offline' }, tracking: { status: 'unavailable' } })).not.toBeNull()
    expect(normalizeRepositorySetupReadiness({ ...valid, overall: 'unavailable', daemon: { status: 'unavailable' }, tracking: { status: 'unavailable' } })).not.toBeNull()
    expect(normalizeRepositorySetupReadiness({ ...valid, overall: 'needs-attention', tracking: { status: 'not-tracked' } })).not.toBeNull()
    expect(normalizeRepositorySetupReadiness({ ...valid, overall: 'unavailable', tracking: { status: 'unavailable' } })).not.toBeNull()
    expect(normalizeRepositorySetupReadiness({ ...valid, overall: 'unavailable', configuration: { status: 'unavailable' }, snapshot: { status: 'unavailable' } })).not.toBeNull()
  })

  it('POSTs exactly the opaque repository identity', async () => {
    setCSRF('test-csrf')
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify(valid), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(fetchRepositorySetupReadiness('legacy-selected-repository')).resolves.toEqual(valid)
    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [path, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(path).toBe('/api/repositories/setup-readiness')
    expect(init.method).toBe('POST')
    expect(JSON.parse(String(init.body))).toEqual({ repositoryIdentity: 'legacy-selected-repository' })
    expect(Object.keys(JSON.parse(String(init.body)))).toEqual(['repositoryIdentity'])
    expect(new Headers(init.headers).get('X-CSRF-Token')).toBe('test-csrf')
  })

  it('rejects exact-object drift, private fields, identities and inconsistent cross-field states', () => {
    const invalid: unknown[] = [
      { ...valid, extra: true },
      { ...valid, version: '0.64.0' },
      { ...valid, repositoryIdentity: '' },
      { ...valid, repositoryIdentity: ' opaque-repository' },
      { ...valid, repositoryIdentity: 'bad\u0000identity' },
      { ...valid, configuration: { status: 'ready', path: 'C:\\private' } },
      { ...valid, snapshot: { status: 'ready', content: 'private' } },
      { ...valid, gitHooks: { status: 'ready', command: ['git'] } },
      { ...valid, daemon: { status: 'online', rawOutput: 'secret' } },
      { ...valid, tracking: { status: 'tracked', path: 'C:\\private' } },
      { ...valid, configuration: { status: 'unknown' } },
      { ...valid, snapshot: { status: 'not-ignored' }, overall: 'ready' },
      { ...valid, configuration: { status: 'unavailable' }, snapshot: { status: 'ready' }, overall: 'unavailable' },
      { ...valid, daemon: { status: 'offline' }, tracking: { status: 'tracked' }, overall: 'needs-attention' },
      { ...valid, daemon: { status: 'online' }, tracking: { status: 'unavailable' }, overall: 'needs-attention' },
      { ...valid, daemon: { status: 'unavailable' }, tracking: { status: 'unavailable' }, overall: 'needs-attention' },
      Object.assign(Object.create({ extra: true }), valid),
    ]
    for (const value of invalid) expect(normalizeRepositorySetupReadiness(value, valid.repositoryIdentity)).toBeNull()
    expect(normalizeRepositorySetupReadiness({ ...valid, repositoryIdentity: otherAlias }, 'legacy-selected-repository')).toEqual({ ...valid, repositoryIdentity: otherAlias })
  })
})

describe('snapshot ignore management API contract', () => {
  const alias = 'A'.repeat(43)
  const preview = {
    version: 'v0.64.0', repositoryIdentity: alias, action: 'append-snapshot-ignore', target: '.gitignore',
    summary: 'Add the RoboRev snapshot directory to Git ignore rules.', token: 'opaque-snapshot-token', expiresAt: '2099-01-01T00:00:00.000Z',
  } as const

  it('accepts only the exact content-free preview DTO and server-issued alias', () => {
    expect(normalizeSnapshotIgnorePreview(preview, alias)).toEqual(preview)
    for (const value of [
      { ...preview, repositoryIdentity: 'repo-a' },
      { ...preview, repositoryIdentity: 'A'.repeat(42) },
      { ...preview, action: 'replace-snapshot-ignore' },
      { ...preview, target: 'C:\\secret\\.gitignore' },
      { ...preview, summary: 'private' },
      { ...preview, token: '' },
      { ...preview, token: 'secret\u0000token' },
      { ...preview, expiresAt: 'not-a-date' },
      { ...preview, pattern: 'PRIVATE_SNAPSHOT_PATH' },
      { ...preview, content: 'PRIVATE_GITIGNORE_MARKER_DO_NOT_EXPOSE' },
      { ...preview, diff: 'private' },
      { ...preview, command: 'private' },
    ]) expect(normalizeSnapshotIgnorePreview(value, alias)).toBeNull()
    expect(normalizeSnapshotIgnorePreview({ ...preview, repositoryIdentity: 'B'.repeat(43) }, alias)).toBeNull()
  })

  it('POSTs exactly the opaque alias for preview and exactly the token for execute', async () => {
    setCSRF('test-csrf')
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify(preview), { status: 200, headers: { 'Content-Type': 'application/json' } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ status: 'verified-success' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(fetchSnapshotIgnorePreview(alias)).resolves.toEqual(preview)
    await expect(executeSnapshotIgnore(preview.token)).resolves.toEqual({ status: 'verified-success' })
    expect(fetchMock).toHaveBeenCalledTimes(2)
    const [previewPath, previewInit] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(previewPath).toBe('/api/snapshot-ignore/preview')
    expect(previewInit.method).toBe('POST')
    expect(JSON.parse(String(previewInit.body))).toEqual({ repositoryIdentity: alias })
    expect(Object.keys(JSON.parse(String(previewInit.body)))).toEqual(['repositoryIdentity'])
    const [executePath, executeInit] = fetchMock.mock.calls[1] as [string, RequestInit]
    expect(executePath).toBe('/api/snapshot-ignore/execute')
    expect(executeInit.method).toBe('POST')
    expect(JSON.parse(String(executeInit.body))).toEqual({ token: preview.token })
    expect(Object.keys(JSON.parse(String(executeInit.body)))).toEqual(['token'])
    expect(new Headers(executeInit.headers).get('X-CSRF-Token')).toBe('test-csrf')
  })

  it('strictly normalizes execute results and rejects private fields', () => {
    expect(normalizeSnapshotIgnoreExecuteResult({ status: 'verified-success' })).toEqual({ status: 'verified-success' })
    expect(normalizeSnapshotIgnoreExecuteResult({ status: 'failed-unverified' })).toEqual({ status: 'failed-unverified' })
    expect(normalizeSnapshotIgnoreExecuteResult({ status: 'verified-success', token: 'secret' })).toBeNull()
    expect(normalizeSnapshotIgnoreExecuteResult({ status: 'error' })).toBeNull()
  })
})

describe('repository tracking management API contract', () => {
  const alias = 'A'.repeat(43)
  const preview = {
    version: 'v0.64.0', repositoryIdentity: alias, action: 'register-repository', target: 'RoboRev tracking',
    summary: 'Register the selected repository with the running RoboRev daemon.', token: 'opaque-tracking-token', expiresAt: '2099-01-01T00:00:00.000Z',
  } as const

  it('accepts only the exact content-free preview DTO and matching alias', () => {
    expect(normalizeRepositoryTrackingPreview(preview, alias)).toEqual(preview)
    for (const value of [
      { ...preview, repositoryIdentity: 'repo-a' },
      { ...preview, repositoryIdentity: 'B'.repeat(43) },
      { ...preview, version: '0.64.0' },
      { ...preview, action: 'register' },
      { ...preview, target: 'C:\\private' },
      { ...preview, summary: 'private' },
      { ...preview, token: '' },
      { ...preview, token: 'secret\u0000token' },
      { ...preview, expiresAt: 'not-a-date' },
      { ...preview, repo_path: 'C:\\private' },
      { ...preview, daemonEndpoint: '127.0.0.1:7373' },
      { ...preview, binary: 'C:\\private\\roborev.exe' },
      { ...preview, repos: [{ root_path: 'C:\\private', name: 'private', identity: 'private', count: 1 }] },
      { ...preview, root_path: 'C:\\private' },
      { ...preview, identity: 'private' },
      { ...preview, error: 'PRIVATE_DAEMON_ERROR' },
    ]) expect(normalizeRepositoryTrackingPreview(value, alias)).toBeNull()
  })

  it('POSTs exactly the opaque alias for preview and exactly the token for execute', async () => {
    setCSRF('test-csrf')
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify(preview), { status: 200, headers: { 'Content-Type': 'application/json' } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ status: 'verified-success' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(fetchRepositoryTrackingPreview(alias)).resolves.toEqual(preview)
    await expect(executeRepositoryTracking(preview.token)).resolves.toEqual({ status: 'verified-success' })
    expect(fetchMock).toHaveBeenCalledTimes(2)
    const [previewPath, previewInit] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(previewPath).toBe('/api/repository-tracking/preview')
    expect(previewInit.method).toBe('POST')
    expect(JSON.parse(String(previewInit.body))).toEqual({ repositoryIdentity: alias })
    expect(Object.keys(JSON.parse(String(previewInit.body)))).toEqual(['repositoryIdentity'])
    const [executePath, executeInit] = fetchMock.mock.calls[1] as [string, RequestInit]
    expect(executePath).toBe('/api/repository-tracking/execute')
    expect(executeInit.method).toBe('POST')
    expect(JSON.parse(String(executeInit.body))).toEqual({ token: preview.token })
    expect(Object.keys(JSON.parse(String(executeInit.body)))).toEqual(['token'])
    expect(new Headers(executeInit.headers).get('X-CSRF-Token')).toBe('test-csrf')
  })

  it('strictly normalizes execute results and rejects private fields', () => {
    expect(normalizeRepositoryTrackingExecuteResult({ status: 'verified-success' })).toEqual({ status: 'verified-success' })
    expect(normalizeRepositoryTrackingExecuteResult({ status: 'failed-unverified' })).toEqual({ status: 'failed-unverified' })
    expect(normalizeRepositoryTrackingExecuteResult({ status: 'verified-success', repo_path: 'C:\\private' })).toBeNull()
    expect(normalizeRepositoryTrackingExecuteResult({ status: 'error' })).toBeNull()
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
