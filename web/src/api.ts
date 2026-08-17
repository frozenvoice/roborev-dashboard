export type Repository = {
  name: string
  path: string
  branch: string
  head: string
  headShort: string
  commitSubject: string
  dirty: boolean
  hasConfig: boolean
}

export type Installation = {
  installed: boolean
  binary?: string
  version?: string
  status?: { running?: boolean; queued?: number; active?: number; [key: string]: unknown }
  error?: string
  capabilities: {
    reviewEnqueueApi: boolean
    frozenRerunApi: boolean
    panelReview: boolean
    panelMembersApi: boolean
    promptLazyLoad: boolean
    rawLog: boolean
  }
}

export type CommandPreview = { executable: string; args: string[] }

export type Bootstrap = {
  csrfToken: string
  repositories: Repository[]
  selectedRepository: string
  roborev: Installation
  reviewHeadCommand?: CommandPreview
}

export type ReviewJob = {
  id: number
  job_id?: number
  uuid?: string
  repo_path?: string
  repo_name?: string
  git_ref?: string
  commit_sha?: string
  branch?: string
  subject?: string
  commit_subject?: string
  job_type?: string
  agent?: string
  model?: string
  reasoning?: string
  status?: string
  verdict?: string
  review?: { output?: string; verdict?: string; [key: string]: unknown } | string
  output?: string
  addressed?: boolean
  closed?: boolean
  created_at?: string
  completed_at?: string
  enqueued_at?: string
  started_at?: string
  finished_at?: string
  panel_name?: string
  panel_run_uuid?: string
  panel_role?: string
  parent_job_id?: number
  panel_member_index?: number
  member_job_ids?: number[]
  duration_ms?: number
  [key: string]: unknown
}

let csrfToken = ''
export function setCSRF(value: string) { csrfToken = value }

export class APIError extends Error {
  phase?: string
  constructor(message: string, phase?: string) { super(message); this.phase = phase }
}

export async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const headers = new Headers(options.headers)
  if (options.body) headers.set('Content-Type', 'application/json')
  if ((options.method ?? 'GET') !== 'GET') headers.set('X-CSRF-Token', csrfToken)
  const response = await fetch(path, { ...options, headers })
  const text = await response.text()
  let payload: unknown = undefined
  if (text) {
    try { payload = JSON.parse(text) } catch { payload = text }
  }
  if (!response.ok) {
    const problem = payload as { error?: string; phase?: string } | undefined
    throw new APIError(problem?.error ?? `Request failed (${response.status})`, problem?.phase)
  }
  return payload as T
}

export function jobsFromPayload(value: unknown): ReviewJob[] {
  if (Array.isArray(value)) return value as ReviewJob[]
  if (value && typeof value === 'object') {
    const object = value as Record<string, unknown>
    for (const key of ['jobs', 'reviews', 'items']) if (Array.isArray(object[key])) return object[key] as ReviewJob[]
  }
  return []
}
