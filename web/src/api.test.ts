import { describe, expect, it } from 'vitest'
import { normalizeRepositoryInventoryResponse, normalizeRepositoryMovePreview, normalizeRepositoryMoveResult, normalizeRepositoryRenamePreview, normalizeRepositoryRenameResult, validateRepositoryRenameName } from './api'

const row = { name: 'repo', path: 'E:\\repo', jobCount: 2, dashboardRegistered: true }

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

  it('rejects unknown fields, missing cwd, malformed identity and unsafe receipts', () => {
    expect(normalizeRepositoryMovePreview({ ...preview, extra: true })).toBeNull()
    expect(normalizeRepositoryMovePreview({ ...preview, destinationRepositoryIdentity: '' })).toBeNull()
    expect(normalizeRepositoryMovePreview({ ...preview, command: { ...preview.command, workingDirectory: '' } })).toBeNull()
    expect(normalizeRepositoryMovePreview({ ...preview, command: { ...preview.command, extra: true } })).toBeNull()
    expect(normalizeRepositoryMoveResult({ status: 'moved', sourcePath: 'E:/old/project', destinationPath: 'E:\\new\\project', message: 'unexpected' })).toBeNull()
    expect(normalizeRepositoryMoveResult({ status: 'unverified', sourcePath: 'E:/old/project', destinationPath: 'E:\\new\\project', message: '' })).toBeNull()
  })
})
