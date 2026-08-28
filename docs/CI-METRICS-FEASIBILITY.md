# CI Metrics Export Feasibility Audit — NO-GO

Audit date: 2026-08-28
Dashboard base: `dc9736f5fe25f19f10f394d0d947a201e8db5702`
Upstream authority: RoboRev v0.64.0 checkout
`C:\Users\froze\AppData\Local\Temp\roborev-v0640-general-refine-audit-01a042eb`,
commit `bc0af33c282733a9c39d72c08373b040e22c11d4`.

## Scope/exact version

This is a read-only feasibility audit for a future Dashboard export of one
selected registered repository. It covers both the panel-era selected-
repository complete export and the frozen legacy selected-repository export.
The conclusion is **NO-GO** for both. The audit does not add an endpoint, UI,
adapter, download, clipboard or CSV path. Production occurrence of the direct
CLI and daemon CI-metrics endpoint remains zero.

The conclusion is not based on the absence of a selector, nor on a rule that
global backend reads are inherently forbidden. A global read can be useful for
an observational product. The current v0.64.0 contract cannot prove that a
global, mutable, identity-ambiguous result is a complete and private result
for the selected repository.

## Upstream HTTP contract

Exact v0.64.0 exposes `GET /api/export/ci-metrics` (the route is registered in
`internal/daemon/routes.go` and handled by `humaExportCIMetrics`). Its complete
input is `format`, `since`, `until`, `limit`, `cursor` and `legacy`. `format`
accepts empty/default JSON or `json`; `since` is inclusive and `until` is
exclusive. Timestamps accept RFC3339Nano or `YYYY-MM-DD`, with a date-only
`until` expanded to the next UTC day. `cursor` cannot be combined with
`since`, but `until` can change across cursor requests, and there is no
`since < until` validation. `legacy=true` selects the pre-panel path. A page
defaults to 500 rows and clamps the top-level page limit to 5000. The panel
response is ordered by global `posted_at, panel_id`; the legacy response is a
pseudopanel export.

Panel-era rows are metadata-only in the narrow sense that they contain no
prompt, review body, diff, filesystem path or credential. They still include
`github_repo`, PR number, head SHA, panel timestamps, outcome, synthesis
agent/model, and a nested `jobs` array with job UUID, role, agent, model,
provider, status and times. Legacy mode is more sensitive: its `github_repo`
can pass through a RoboRev identity such as `local://<absolute-path>`, so the
global response cannot be characterized as path-free. There is no explicit
bound on nested jobs, strings, or response body. The 5000 bound applies to
top-level panels only.

Cursor database mismatch is HTTP 409. Invalid, not-found, malformed or
mode-mismatched cursors are HTTP 400. The route normally performs a read after
daemon/database startup, but `GetDatabaseID` is get-or-create if metadata is
missing. Startup backfills are migrations, not lazy export writes.

## CLI lifecycle difference

`roborev export ci-metrics` calls `ensureDaemon()` before fetching. That helper
can discover, restart, clean up stale runtime state and start a daemon. The
CLI follows `next_cursor` pages and accumulates them until `truncated=false`
(or a user top-level limit is reached); this is unbounded for a full export.
Therefore the direct CLI is **N/superseded** for Dashboard use. A candidate
HTTP adapter could talk only to an already-running daemon, but Dashboard does
not currently use this endpoint and the HTTP contract is not repository-safe.

## Storage/query model

Panel mode queries `ci_pr_panels` globally with posted-time bounds and a global
`(posted_at, panel_id)` cursor. It does not filter by a local repository before
ordering and `LIMIT`. Legacy mode groups terminal `review_jobs` by
`(repo_id, git_ref)` and derives one pseudopanel per group. `jobs` are
reconstructed from currently retained rows, while finalized panel snapshots
can survive cascade deletion with an empty nested job list. Reconstructing
jobs or using another panel/jobs endpoint is not semantically equivalent: it
loses `posted_at`, attempt/outcome snapshots and deletion-surviving fields.

This is a real global storage read, not an inherent prohibition. The problem is
that a selected repository predicate is absent from both query plans, and the
identity fields used in the output are not an atomic Dashboard authorization
witness.

## Panel-era identity

Panel `github_repo` is a hostless CI-poller configured/base-repository key
snapshot. It is not a local root, Dashboard repository ID, or daemon inventory
key. A fork PR is attributed to its base repository. GHE host information is
discarded; case and canonicalization can drift; a repository rename does not
rewrite historical rows. Panels survive local repository deletion and can
retain an empty `jobs` array.

Consequently, a path selected in Dashboard cannot be safely translated into a
panel `github_repo` value. The value can be private foreign activity even when
the row contains no prompt or review body.

## Legacy identity

Legacy mode selects `review_jobs` groups by `(repo_id, git_ref)` and uses
`COALESCE(NULLIF(TRIM(r.identity), ''), r.name)`. `legacyGithubRepo` normalizes only a few
case-sensitive public-GitHub URL forms. GHE, custom remotes, `local://` values,
other identities and names can pass through unchanged, collide, or leak a
foreign repository label. Legacy has no recoverable PR linkage (`pr_number=0`)
and is independently **NO-GO**; it is not made safe by the panel-era rules.
Its panel-era cutoff is recomputed from current storage on each export, so
cleanup of marker rows can also change the historical boundary.

## Selected repository mapping

Dashboard has deliberately different identity surfaces. Bootstrap and several
existing APIs use the registered path-derived repository key. Repository Setup
Readiness, Snapshot Ignore and Repository Tracking specifically use a path-free
per-process HMAC alias; that narrow rule does not extend to CI Workflow or every
mutation DTO. Server authorization ultimately resolves the supported selector
to a registered canonical Git root. Bootstrap and repository-inventory DTOs
remain path-bearing. `gitx.Repository` has no remote identity.
The daemon inventory identity is not Dashboard authentication. Upstream
registration may use `.roborev-id`, a remote URL, a custom identity, a local
fallback, or stale data. A root-to-inventory match therefore does not establish
root-to-`github_repo` or root-to-legacy-identity equality.

There is no atomic selected-root → daemon-key → export-filter operation in the
v0.64.0 API. A before/after inventory read around the global export would still
leave an external producer, sync, rename, deletion or identity update race.

| Identity | Source | Example | Stable? | Selected-repo scoped? | Can map to github_repo? | Ambiguity | Decision |
|---|---|---|---|---|---|---|---|
| Bootstrap repository key | Dashboard Registry normalized-path key | `<normalized-local-root-key>` | Path-dependent | Yes for Dashboard auth | No direct mapping | path-bearing and changes with relocation | Existing bootstrap selector only |
| Readiness/Snapshot Ignore/Tracking alias | per-process HMAC of registered path | `<path-free-HMAC-alias>` | Process-local, not a CI key | Yes for those Dashboard APIs | No direct mapping | intentionally unlinkable to remotes | Auth only |
| canonical root | Dashboard Git adapter/Registry | `C:\src\repo` | Stable only for the observed checkout; worktree/root can change | Yes, server-side | No; `gitx.Repository` has no remote | linked worktree, rename, reparse and case semantics | Server-side selection witness |
| RoboRev inventory root | `/api/repos.root_path` | `C:/src/repo` | Can become stale after a move | No; daemon DB-wide | No | case/filesystem aliases and stale rows | Exact-root correlation only |
| RoboRev `repos.identity` | Upstream registration/config resolver | `.roborev-id`, remote URL, `local://...` | Optional, mutable or stale | No; daemon DB-wide | Not reliably | custom/local fallback and stale values | Never Dashboard auth |
| Git origin | Git config (not currently exposed as a Dashboard export key) | `https://github.com/acme/app.git` | Mutable or absent | Repository-local, but no atomic export binding | Public GitHub URL sometimes | GHE host, case, credentials and renames | Evidence only |
| other remotes | Git config | `upstream`, GHE or custom URL | Mutable/optional | No canonical selected contract | No general mapping | multiple remotes and provider-specific forms | Not an authority |
| panel `github_repo` | CI poller configured/base-repo snapshot | `acme/app` | Historical snapshot; rename does not rewrite it | No; global panel row | It is the output label, not a local key | fork attribution, host discarded, case drift | Projection only |
| legacy identity | `review_jobs` join to `repos.identity` | remote, custom or `local://...` | Optional/stale | No; grouped globally by repo/job | Only narrow public-GitHub forms | GHE/custom/local values, including absolute paths, pass through | Legacy NO-GO |
| legacy name fallback | `COALESCE(NULLIF(TRIM(identity), ''), name)` | `app` | Mutable display name | No | No | blank/whitespace identity falls back; names collide across repos and can leak | Never an auth key |
| Future immutable repository key | Not present in v0.64.0 | `<host-qualified-repository-uuid>` | Must be durable and historical | Must be bound before SQL `ORDER/LIMIT` | Yes, by an authoritative server relation | requires defined clone/fork/rename semantics | Required to unblock |

## Cross-repository privacy

The response omits full review content but still exposes foreign repository
slug, PR/SHA, agent/model/provider and timing metadata. Legacy `github_repo`
can additionally expose `local://<absolute-path>` or another raw effective
identity. A safe future CI-metrics browser response must reject such rows and
keep database ID, raw cursor/errors, canonical roots, RoboRev identity and
remote URL server-side; none may become a client-side filter or authorization
token. The table defines that proposed surface and does not rewrite existing
path-bearing bootstrap/inventory DTOs.

| Data | Daemon response | Dashboard backend | Browser allowed? |
|---|---|---|---|
| Selected/foreign repository slug | `github_repo` | Must prove selected key before projection | Selected only after proof; foreign **no** |
| PR/SHA/agent/model/provider | Panel/legacy metadata and nested jobs | Bounded, selected-only DTO after proof | Selected only; foreign **no** |
| Database ID | `database_id` | Cursor validation/reset witness | **No** |
| Cursor | `next_cursor` | Opaque server-side continuation | **No raw cursor** |
| Path | Panel: absent; legacy `github_repo` may carry `local://<absolute-path>` | Treat as sensitive and reject, never project | **No** |
| RoboRev identity | Legacy `github_repo` may derive/pass it through | Server-side mapping only | **No** |
| Remote | May have contributed to identity | Server-side Git/config evidence only | **No** |
| Raw error | HTTP problem text/status | Internal taxonomy/log only | **No** |

## Pagination/cursor/database reset

The cursor payload is `{version, database_id, posted_at, panel_id, legacy}`.
It is bound to the database, global ordering anchor and panel/legacy mode, but
not to a repository or a time window. Database mismatch returns 409; invalid,
not-found or mode mismatch returns 400. A nonempty page emits `next_cursor`
even when `truncated=false`; `generated_at` varies, and `until` can vary
between cursor requests. Thus a cursor is a useful global continuation token,
not proof of a complete selected-repository snapshot.

## Bounds/resource model

The daemon bounds only top-level rows (default 500, maximum 5000). Nested jobs,
strings and the complete response body have no corresponding contract bound.
The CLI follows and accumulates pages without a full-export ceiling. A
Dashboard adapter would need independent byte, row, nested-item, string and
wall-clock limits, plus a typed parser that fails closed on truncation.

## Dashboard transport seam (not implemented)

If upstream repository isolation is ever added, the Dashboard can reuse its
no-redirect HTTP-client clone and strict daemon-read patterns: exact endpoint
and HTTP 200, `Accept-Encoding: identity`, a `limit+1` body bound, media-type
and UTF-8 checks, exact JSON fields, duplicate/unknown/trailing-value rejection,
and sanitized status/error taxonomy. The generic daemon JSON helpers and the
upstream generated client/CLI are not sufficient because they can follow
redirects, lack the required exact response bound, or consume raw error-body
detail. No CI transport is added by this audit.

## Completeness semantics

Filtering a global page after receipt cannot provide selected completeness. If
foreign rows dominate the ordered stream, a bounded scan may miss the selected
row or incorrectly call the result empty. Scanning until a match or exhaustion
is unbounded and has no cross-request snapshot. A global-cursor view can be
bounded, but it is neither a complete selected export nor private unless every
page is wrapped by a safe server-side selection contract.

| Case | Global rows | Selected rows | Requests needed | Can prove complete? | Budget outcome |
|---|---:|---:|---:|---|---|
| First-page match | mixed | present in first page | 1 | No, later selected rows may exist | Bounded page, incomplete |
| After many foreign pages | many foreign first | later | Unbounded until match/exhaustion | No under fixed budget | Reject as incomplete |
| No rows | none | none | 1 | Only for the global set, not selected identity | Empty is unproven for selected |
| 5000+ foreign | >5000 | unknown | Unbounded | No | Reject; top-level cap exhausted |
| Database reset | reset between pages | unknown | 1+ | No; cursor returns 409 | Discard continuation, fail closed |
| Mode mismatch | panel/legacy disjoint | unknown | 1 | No | 400; never mix modes |
| Legacy | global `(repo_id, git_ref)` groups | identity ambiguous | Unbounded | No | Independent NO-GO |
| Malformed foreign page | includes invalid foreign data | unknown | 1 | No | Fail closed; do not skip malformed rows |

## Alternative transports

| Candidate | Evidence | Dashboard decision |
|---|---|---|
| Direct `roborev export ci-metrics` | Calls `ensureDaemon`, follows/accumulates pages | **N/superseded** |
| Already-running daemon `GET /api/export/ci-metrics` | Global panel/legacy query; no selected predicate or identity proof | **N/no Dashboard global product surface** |
| Reconstruct from jobs/panel endpoints | Loses posted-at, attempt/outcome and deletion-surviving snapshots | Not equivalent; no claim |
| New adapter/download/CSV | Would require a new safety contract | Not implemented by this audit |

## Final decision — NO-GO

**NO-GO** is the single final verdict. GO is not selected, CONDITIONAL GO is
not selected, and this audit makes no B claim. Neither selected-repository mode
has an authoritative, bounded, private and complete contract:

- panel-era selected-repository complete export: **NO-GO**;
- legacy selected-repository export: **NO-GO**.

## Future implementation contract

The smallest coherent design would require a server-owned, host-qualified
immutable repository key/UUID on every panel row that remains valid after local
repository deletion, with a selected predicate applied before `ORDER/LIMIT` in
both panel and legacy modes. The mapping from selected canonical root to that
daemon key must be atomic and retained as an authorization witness. Cursors
must bind repository, mode, window and database identity. Nested jobs, strings,
body bytes, page count and total work need explicit bounds. The response must
carry enough server-verifiable proof to establish selected completeness without
exposing raw identity, path, cursor or database metadata.

## Unblock conditions

Do not implement this as a client-side filter. Unblock requires all of:

1. A host-qualified immutable repo key/UUID in panel and legacy-compatible
   rows, with historical retention after local deletion.
2. A server-side selected-repository predicate before ordering and limiting for
   both modes, plus an atomic selected-root-to-daemon-key binding.
3. A cursor bound to repository, mode, window and database generation, with a
   stable snapshot/completeness definition.
4. Explicit byte, nested-row, string, page and time budgets with bounded
   parser/response proof.
5. A legacy backfill only where the identity mapping is provable; otherwise
   legacy remains unsupported.
6. A browser DTO that exposes only selected, bounded metadata and keeps path,
   remote, RoboRev identity, database ID, raw cursor and raw errors server-side.

Until these conditions exist, keep direct CLI and daemon endpoint occurrence at
zero and keep the product surfaces disabled. No implementation checkpoint is
selected by this audit.
