import { describe, expect, it } from 'vitest'
import { normalizeRepositoryInventoryResponse } from './api'

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
