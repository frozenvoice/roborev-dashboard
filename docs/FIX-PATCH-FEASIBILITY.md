# Lifecycle-Free Fix Patch Feasibility Audit — NO-GO

Date: 2026-08-28

Dashboard base: `8b86f75d2733f8a867ef510b1c0ad3c1c3910ad3` (`Sanitize blocked fix browser projection`)

Upstream authority: RoboRev v0.64.0, tag commit `bc0af33c282733a9c39d72c08373b040e22c11d4`

## Decision

**NO-GO.** Exact v0.64.0 does not provide a lifecycle-free, browser-authorizable
Fix patch workflow. `POST /api/job/fix` is a useful daemon-side background job
API for RoboRev's own clients, but it is not a safe transport for Dashboard Fix.
The generated patch can be stored and later retrieved, yet the complete
generation → inspect → apply → commit → state transition chain has hidden agent,
filesystem, queue, Git and recovery authorities that cannot be frozen and
verified at the browser confirmation boundary.

This audit is documentation-only. It adds no `/api/job/fix`, patch apply, commit,
or `/api/job/applied` production path. General Fix, Panel Refine and General
Refine execution remain blocked. Fix browser DTO privacy remains in force;
Tasks and Analyze execution remain disabled, Review Export remains **BLOCKED**,
and Repository Delete/unregister remains **BLOCKED**.

The three possible phases are deliberately not promoted to production safety:

* **Phase A — generation:** `POST /api/job/fix` is **not production GO**.
* **Phase B — inspection:** `GET /api/job/patch` plus a browser preview is **not
  an audit-only B claim**; the raw endpoint lacks the Dashboard's bounded,
  repo-bound, privacy-safe patch envelope.
* **Phase C — apply/commit:** TUI behavior is **not production GO**. It has
  observable partial-failure states and no transaction contract that lets the
  Dashboard claim mutation-zero or rollback.

## Authority and source map

The audit used current Dashboard source/tests first, then exact v0.64.0 source,
then `AGENTS.md`. Relevant upstream evidence is:

| Contract | Exact source evidence |
|---|---|
| Request and handler | `internal/daemon/types.go:506-517`; `internal/daemon/server.go:2527-2766` (`humaFixJob`) |
| Routes and response shapes | `internal/daemon/routes.go:311-363`; `internal/daemon/types.go:519-589` |
| Worker and patch creation | `internal/daemon/worker.go:807-899`; `internal/storage/jobs.go:608-681` |
| Worktree and Git adapter | `internal/daemon/worktree_create.go`; `go.kenn.io/kit@v0.1.8/git/worktree/worktree.go:24-245` |
| State transitions | `internal/daemon/server.go:2768-2812`; `internal/storage/jobs.go:812-852` |
| TUI apply/rebase flow | `cmd/roborev/tui/actions.go:273-625` |
| Configuration reload and safety globals | `internal/daemon/config_watcher.go:37-48,199-216`; `internal/agent/{agent,codex,claude,gemini,grok}.go` |
| Upstream regression coverage | `internal/daemon/server_ops_test.go:500-1323`; `internal/daemon/worker_test.go`; `internal/storage/db_job_test.go:833-910`; `internal/git/git_test.go:2977-3107` |

The paths above refer to the v0.64.0 checkout at the stated commit, not to a
future or modified upstream implementation.

## Exact background Fix API

### Request and returned job

`FixJobRequest` accepts exactly these JSON fields:

```json
{
  "parent_job_id": 123,
  "prompt": "optional user text",
  "git_ref": "optional ref",
  "stale_job_id": 456
}
```

`parent_job_id` is required. `prompt`, `git_ref` and `stale_job_id` are optional;
there is no request field for agent, model, provider, reasoning, sandbox,
approval mode, writable roots or failover policy. The successful response is
HTTP 201 containing the raw `storage.ReviewJob`, rather than a small receipt.
That DTO includes effective agent/model/provider/reasoning, prompt, repository
joined fields, `worktree_path`, `command_line`, `parent_job_id`, `patch`, retry
and failover metadata. It is not a browser-safe Dashboard DTO.
Immediately after enqueue, `patch` and `command_line` are normally empty because
the worker has not yet run or captured output; the response type is nevertheless
the same broad raw `ReviewJob`, and later completion/readback can populate those
fields.

The handler rejects a missing parent, a missing parent row, a parent that is
itself a Fix job, an invalid supplied `git_ref`, an invalid/stale parent
worktree, invalid workflow configuration, unresolved agent, and enqueue errors.
It does **not** require a parent review to be done, passing/failing, open,
non-skipped, current, or owned by a browser-selected repository before the
enqueue. For the non-stale path it eventually requires a review row and uses its
output plus comments to build the prompt. The selected repository, review
identity, branch, full HEAD and current clean state are not atomically bound to
the enqueue request.

There is no separate dirty/range/CI/synthesis/task exclusion at this boundary.
A literal `git_ref` value of `dirty` passes the generic ref validator, while an
omitted ref on a range parent skips that range and falls back to the parent
branch or `HEAD`. The `dirty` case can enqueue before a later checkout failure;
the range case silently changes the target from the range to a fallback ref.

For `stale_job_id`, the handler checks that the row is a Fix job in the same DB
repository, linked to the requested parent, terminal (`done`, `applied` or
`rebased`) and has a non-empty patch. It then constructs a rebase prompt from
the stored patch. This is a server-side rebase convention, not a Dashboard
confirmation or current-checkout apply contract.

`git_ref` is trimmed and validated. It can carry a full SHA, but the endpoint
does not require or bind an immutable full SHA; a movable ref is accepted. If
omitted, the handler falls back to the parent ref when it is not a range, then
the parent branch, then `HEAD`. That fallback can describe a different
checkout by the time a worker claims the job.

### Handler resolution and enqueue

For a normal Fix, `humaFixJob` reads the daemon's current config and:

1. resolves Fix minimum severity;
2. obtains a valid parent review and comments;
3. resolves Fix reasoning;
4. validates `.roborev.toml` for the effective repository/worktree;
5. resolves preferred and backup agent configuration;
6. chooses an available preferred-or-backup executable;
7. resolves the selected model; and
8. enqueues `JobTypeFix` with `Agentic: true`, the selected effective agent,
   model, reasoning, `ParentJobID`, `GitRef`, optional `WorktreePath` and prompt.

The only response receipt is the resulting job row. The enqueue has no frozen
Dashboard safety attestation and no reservation token that prevents later
configuration or executor changes.

## Configuration, hot reload and executor safety

Fix reasoning resolves `explicit > repository FixReasoning > global FixReasoning
> standard`; Fix minimum severity resolves `explicit > repository > global >
empty`. Workflow primary agent resolution is layer- and specificity-dependent:
CLI value, repository `{workflow}_agent_{level}`, repository
`{workflow}_agent`, repository `agent`, global `{workflow}_agent_{level}`,
global `{workflow}_agent`, global `default_agent`, then `codex`. Backup agent
resolution is repository workflow-specific, repository generic, global
workflow-specific, global default, then none. Backup model has its own
repository workflow/generic, global workflow, and global default precedence.
ACP backup model pairing adds another agent/model-layer rule.

The daemon watches global config. `default_agent`, `job_timeout`, quota
cooldown, `allow_unsafe_agents`, API key and context count take effect on reload;
server address, worker count and sync settings are restart-bound. Reload updates
global agent safety state through `agent.SetAllowUnsafeAgents` and
`agent.SetCodexSandboxDisabled`. A Dashboard preview of a file is not proof of
the daemon's effective config identity, reload generation, or environment.

The handler resolves reasoning and stores that string in the enqueue row. The
worker reads the stored `job.Reasoning` (normalizing legacy values) and passes it
to `WithReasoning`; a config hot reload does not rewrite that already-stored job
field. The stored receipt is still not bound to a Dashboard pre-enqueue
confirmation token or config generation, and it does not freeze the executor,
failover or safety profile. The worker resolves the agent again when it claims
the job, then calls `WithAgentic(true)` and `WithModel`. Agent errors can retry
and then switch to a configured backup agent/model, so the selected executor
can change after confirmation and an enqueue response cannot prove which agent
will mutate the worktree.

The safety consequence is material. Exact v0.64 Fix is always agentic:

* Fix enqueue sets `Agentic: true` and the worker uses `WithAgentic(true)`, so
  Codex Fix follows the always-agentic path and adds
  `--dangerously-bypass-approvals-and-sandbox`. The separate non-agentic
  `sandboxBroken + autoApprove` branch that can select `--full-auto` is not the
  Fix path. `allow_unsafe_agents` is already effectively true for this Fix and
  is not the distinguishing safety control.
* Claude agentic mode uses write tools and may add
  `--dangerously-skip-permissions`.
* Gemini agentic mode uses approval mode `yolo` and/or
  `--dangerously-skip-permissions` rather than a read-only plan/sandbox.
* Grok agentic mode auto-approves tools; its read-only mode's sandbox/tool
  restrictions are not retained for agentic work.
* Other executor adapters similarly derive their writable/approval behavior
  from daemon config and agent implementation, not the request body.

A worktree is a Git checkout boundary, not an OS/filesystem sandbox. The agent
can still write within that checkout, invoke commands, follow links/reparse
points as permitted by the executor, access network/credentials available to
the process, and affect Git metadata. `--sandbox` or a temporary directory is
not equivalent to a verified writable-root policy. The upstream endpoint does
not attest to sandbox mode, allowed roots, inherited environment, or dangerous
flags before enqueue.

## Queue and lifecycle races

The daemon's queue pause API only prevents new queued jobs from starting;
already-running jobs continue. Pausing is global, affects unrelated users and
automation, and is not an enqueue reservation. A race remains between checking
queue state, pausing, configuration reload, another producer's enqueue, worker
claim, restart/recovery, and agent invocation. Existing jobs can hold worker
slots, fail over, or mutate while a browser confirmation is in progress.

The Dashboard must not pause a shared daemon as a substitute for an atomic Fix
plan. Queue pause would also create a new lifecycle/global side effect that the
browser confirmation did not describe. Exact v0.64 has no queue barrier that
freezes all producers, running workers, config state, failover, and executor
safety as one operation.

## Worker, worktree and patch behavior

For `JobTypeFix`, the worker creates a detached temporary worktree at the stored
Git ref with `InitSubmodules: true` and `PullLFS: true`, then runs the agent
there. The helper disables hooks during worktree creation, initializes
submodules with a constrained `file` protocol exception, optionally runs
`git lfs pull`, records a base SHA after creation, and removes the worktree on
completion. Cleanup errors are logged; they do not create a verified Dashboard
postcondition. A stale or invalid parent worktree can reject enqueue, but a
valid worktree does not contain the agent's command/network/credential powers.

The worker handles prompt construction, saves prompt data, resolves the agent,
creates the Fix worktree and marks `agent_invoked` only after pre-agent gates.
Agent failures, timeout, context-window errors and provider/quota classes feed
`RetryJob`; after retry exhaustion, `FailoverJob` can switch to the backup
agent/model. This can produce a patch from a different executor than the one
shown at preview time. Creation, submodule initialization, LFS pull, agent
execution, patch capture and cleanup can fail independently.

`CapturePatch` first runs `git add -A`, then prefers
`git write-tree` + `git diff-tree -p --binary <BaseSHA> <tree>`, falling back to
`git diff --cached --binary`. It therefore captures tracked and untracked
changes, mode changes, deletes and binary data that Git can represent, but its
contract does not bound the result, define a browser-safe envelope, or prove
that all relevant filesystem effects are representable by a patch. Submodule
gitlinks, LFS pointers/objects, symlinks, reparse points, ignored files and
external side effects need separate treatment. An empty capture fails the Fix
job; a capture failure can retry/fail.

`CompleteFixJob` transactionally stores the output, marks status `done`, and
persists the patch for a running job. This protects the daemon's DB row from a
patch-without-done row, but it does not make subsequent HTTP retrieval, apply,
commit or Dashboard state transition atomic with the repository filesystem.

## Patch retrieval, bounds and privacy

`GET /api/job/patch?job_id=<id>` loads a job and returns its stored patch as raw
`text/plain` when viewable and non-nil. It returns 404 for a missing job or no
patch. The route gates on `HasViewableOutput` and non-nil `Patch`, but does not
independently require `JobTypeFix`. It has no patch-specific maximum response
size, no `Content-Length` contract, no repository binding, no digest/manifest,
and no typed patch-only envelope. The stored patch itself is nullable text in
the job row. A browser
client cannot safely assume that the patch is small, UTF-8, path-safe or
associated with the currently selected checkout.

The raw job response and patch route are also broader than the Dashboard
privacy boundary. Existing Dashboard Fix projections do not serialize
`repositoryPath`, resolved executables, raw errors, stderr or process output;
they expose only safe semantic fields and rebuild a fixed display command. Any
future patch feature must retain that rule: no private path, executable,
command line, environment, prompt, token, stderr, or raw error may cross the
browser boundary. Patch text itself must be treated as sensitive repository
content and be returned only after server-side repository/job ownership checks.

## Exact TUI apply/commit/rebase sequence

The upstream TUI is evidence of behavior, not a Dashboard authorization model.
`cmd/roborev/tui/actions.go` does the following:

1. `applyFixPatch` loads patch and job detail through two independent daemon
   reads (the raw patch route and a separate job-detail/list read), then calls
   `gitrepo.WorktreePathForBranch`. It applies to the branch's existing
   worktree if checked out; otherwise it asks for a temporary worktree. The two
   reads have no shared patch digest, repository binding or transaction.
2. `applyFixPatchInWorktree` creates a temp directory, runs
   `git worktree add <dir> <branch>`, and removes it after success. If patch
   application succeeds but commit fails, it keeps the worktree for recovery;
   failed cleanup falls back to directory removal and `worktree prune`.
3. `checkApplyCommitPatch` parses file names from the unified diff, checks only
   patch-touched paths for dirty state, runs `gitworktree.CheckPatch`, loads
   Fix commit metadata, then runs `gitworktree.ApplyPatch`. Its exact dirty
   command, `git diff --name-only HEAD --`, sees tracked staged/unstaged changes
   but omits untracked files and does not use NUL-delimited path output.
4. It stages only paths returned by `patchFiles` using `git add -- <files>` and
   commits with `git commit --only -m <message> -- <files>`, optional author and
   Co-authored-by trailers. It does not claim repository-wide clean state.
5. After local commit, it calls `POST /api/job/applied` with `{ "job_id": id }`.
   A successful local commit plus failed mark leaves local mutation committed
   while daemon state remains `done`.
6. On a dry-run conflict it offers rebase. `triggerRebase` loads the stale job,
   posts `/api/job/fix` with `parent_job_id` and `stale_job_id`, then posts
   `/api/job/rebased` for the old job. The new job can be enqueued even if the
   old-row mark fails.
7. After a successful apply/commit/mark, the TUI separately closes the parent
   review with `closeReview`/`markParentClosed`. Close is therefore not part of
   the daemon's apply transaction.

The helper dependency is exact and narrow: `CheckPatch` runs `git apply
--check --binary -`; `ApplyPatch` runs `git apply --binary -` with the patch on
stdin, without `--reject` or `--3way`. For an ordinary hunk conflict under
Git's default non-`--reject` behavior, the whole patch is rejected and the
target is normally left unchanged; that is useful positive mutation-zero
evidence for that specific conflict when the target is read back. It does not
make the complete check → apply → stage → commit sequence transactional. Generic
I/O/concurrency errors, later index/commit steps and missing readback remain
ambiguous and require authoritative inspection. There is no reject-file policy,
staged/index transaction, allowlisted path envelope, post-apply digest, or
rollback operation in this contract. A mutation-started outcome that cannot be
verified is `failed-unverified`.

The kit `gitcmd.New()` runner used by `CheckPatch` and `ApplyPatch` strips
inherited `GIT_*` routing/configuration variables, disables system/global config
through its defensive environment, and injects its own Git safety settings. In
contrast, the TUI's raw `exec.Command` for `git add` and `git commit` starts from
`os.Environ()` and adds only `GIT_LITERAL_PATHSPECS=1`; inherited `GIT_*` values
can therefore affect staging/commit differently from check/apply. A future
contract must use one explicitly bound Git environment across every step.

## Path, Windows and selected-checkout concerns

The TUI's `patchFiles` uses `sourcegraph/go-diff`, strips `a/` and `b/`, ignores
`/dev/null`, and unions old/new names. It does not establish the stricter
Dashboard envelope required for absolute paths, `..` traversal, duplicate or
case-equivalent names, control characters, invalid UTF-8, quoted tabs/spaces,
backslashes, Windows drive paths, UNC paths, symlinks/reparse points,
submodules or empty path records. A patch parser that only reproduces this
filename list cannot prove that `git apply` will touch exactly those files.

On Windows, drive/case/separator normalization and reparse-point traversal
must be handled by server-owned path identity checks, not guessed from patch
text. Git's path interpretation, LFS filters, submodule gitlinks and mode bits
can differ from a simple OS path comparison. A future contract must reject
ambiguous/unsupported patch forms instead of broadening path authority.

The selected-checkout policy for a future Dashboard surface must be stricter
than the TUI helper: one server-authorized registered repository, one selected
checkout, attached expected branch, exact full HEAD equal to the patch base,
whole-tree clean baseline, stable root identity, no temporary/worktree target,
and no rebase in the apply operation. `WorktreePathForBranch` can locate an
existing linked worktree or report that a branch is not checked out; that is
useful discovery, not an authorization lease. Bare repositories, multiple
equivalent worktrees, detached HEAD, branch movement, root replacement and
main-vs-linked-worktree ambiguity must fail closed.

## Partial failure semantics

There are several irreversible or ambiguous boundaries:

* enqueue can succeed while the HTTP response is lost;
* a worker can run, fail over and leave a stored patch after the browser closes;
* patch retrieval can be truncated, redirected or read from a different job
  unless it is bounded and repository-bound;
* `CheckPatch` can pass while the target changes before `ApplyPatch`;
* an ordinary non-`--reject` hunk conflict normally rejects the whole patch with
  mutation-zero, but generic I/O/concurrency errors are not covered by that
  useful default-conflict guarantee and require readback;
* `git add` can stage only a subset or include unrelated index state through
  ordinary repository interactions;
* commit can fail after apply, leaving dirty files and possibly a kept temp
  worktree;
* commit can succeed while `/api/job/applied` fails or times out;
* `/api/job/applied` and `/api/job/rebased` are conditional DB state updates,
  not idempotent mutation receipts; and
* parent close is a separate request and can fail after the patch commit.

The default hunk-conflict behavior is not a transaction for the full flow, and
there is no automatic rollback. The only honest Dashboard result after a
mutation-started operation without complete authoritative readback is
`failed-unverified` / needs-attention. Automatic retry is unsafe because it can
duplicate a commit, apply to a changed tree, or enqueue a second Fix.

## Required phase table

| Phase | Mutation happened | Authority | Residue | Rollback | Dashboard result |
|---|---|---|---|---|---|
| A1 plan/eligibility | No | Dashboard can inspect selected review, repository and current Git state | None | Not needed | Read-only facts only; no production Fix token |
| A2 background generation enqueue | DB enqueue may happen | Daemon `humaFixJob`; response is raw `ReviewJob` | Queued Fix row and stored composed prompt | Best-effort cancel is available, but it is not a transactional rollback of enqueue | **NO-GO**; do not call `/api/job/fix` |
| A2a enqueue transport error | Unknown: handler may have enqueued before response failure | HTTP transport plus daemon/storage state | Queued or running Fix can remain despite client error | Best-effort `/api/job/cancel` exists, but is not transactional rollback or mutation-zero proof; no idempotent receipt | **failed-unverified**; never blindly retry |
| A3 worker claim/config resolution | Agent execution may not yet have started | Worker claim plus live config watcher | Running row, selected/failover metadata | Cancel is best effort; config can reload | **NO-GO** without frozen reservation |
| A3a job failed/canceled | Agent/worktree mutation may already have happened | Worker terminal status, logs and cleanup | Failed/canceled row, worktree/artifacts/logs may remain | Cleanup is best effort; no source rollback | Terminal failure is not mutation-zero evidence; **NO-GO** |
| A4 agentic generation/worktree | Yes: worktree files, Git metadata, commands/network may change | Agent executor and daemon worker | Worktree, submodule/LFS artifacts, logs, process effects | Worktree cleanup only; not filesystem rollback | **NO-GO**; worktree is not containment |
| A5 patch capture/store | Yes: `git add -A` in worker worktree and DB transaction | `CapturePatch` + `CompleteFixJob` | Stored raw patch and job output | DB transaction only covers row; no source rollback | **NO-GO**; bounded patch envelope absent |
| B1 patch retrieval/inspection | No target mutation | `GET /api/job/patch` and TUI parser | Browser may hold sensitive patch; raw response unbounded | Drop browser copy only | Not an audit-only B claim |
| B1a patch missing/malformed | No target mutation by inspection | Patch route/storage plus strict future parser | No usable candidate; raw/sensitive response may still be exposed | Drop candidate; no apply rollback needed | Fail closed; never apply or credit inspection |
| B2 target resolution/dirty check | No intended mutation | TUI `WorktreePathForBranch`, `patchFiles`, touched-file `git diff` | TOCTOU between check and apply | None | Future preflight only; whole-tree/exact-HEAD required |
| B2a HEAD drift | No | Fresh Git HEAD/base witness | Stale token/candidate; target unchanged | None; issue a new preview | Stale conflict; process 0 |
| B2b dirty tree | No | Fresh whole-tree status and selected-checkout identity | Pre-existing edits/index state remain | None; user owns existing changes | Block before apply; process 0 |
| C1 dry-run check | Usually no mutation, but not a transaction reservation | `git apply --check --binary -` | Target may change immediately after check | None | Future advisory check only |
| C1a check conflict | No mutation for ordinary non-`--reject` hunk conflict, subject to readback | Git default `git apply --check --binary -` behavior | Conflict/no candidate; target is normally unchanged | None | Verified conflict only with target readback; no apply |
| C2 apply | Intended patch mutation may happen; ordinary hunk conflict normally rejects whole patch | `git apply --binary -` without `--reject` | Dirty target if applied; generic I/O/concurrency outcome may be unknown | No full-flow transaction or automatic rollback | Conflict can be mutation-zero after readback; otherwise `failed-unverified` |
| C2a apply failure | Ordinary hunk conflict is normally mutation-zero; generic I/O/concurrency failure is ambiguous | Git apply result plus fresh target/index readback | Conflict may leave none; generic failure may leave unknown dirty/index state | No automatic rollback | Readback-proven conflict is safe failure; otherwise `failed-unverified` |
| C3 stage/commit | Yes | TUI `git add --` then `git commit --only` | Staged files, commit, hooks/post-commit effects | No automatic reset/restore permitted | `needs-attention` / `failed-unverified` on ambiguity |
| C3a stage failure | Apply already happened; staging may be partial or absent | Git index and worktree readback | Dirty applied files and possible staged subset | No automatic index/worktree restore | `failed-unverified` / needs attention |
| C3b commit failure | Apply and staging happened; commit may or may not have been created | Git commit/HEAD/index/worktree readback | Dirty/staged files, possible commit and hooks effects | No automatic reset/restore | `failed-unverified` until authoritative state is clear |
| C3c commit success | Yes: local Git commit | Git HEAD/log and tree state | New commit; daemon job still `done` until mark | No automatic commit rollback | Continue exactly once to mark-applied/readback |
| C4 mark applied | DB mutation only | `/api/job/applied` → `MarkJobApplied` (`done` → `applied`) | Local commit and daemon status can diverge | No reverse state operation | Only after local + authoritative readback; otherwise unverified |
| C4a applied | Yes: daemon DB state transition after local commit | `MarkJobApplied` plus Git commit/readback | Local commit + job status `applied` | No reverse state operation | Verified only when both Git and daemon readbacks match |
| C4b mark-applied failure | Local commit already happened; DB transition unknown/unchanged | `/api/job/applied` response plus fresh job/Git readback | Local commit with job still `done` or unknown | No safe reverse commit/state rollback | `failed-unverified`; do not retry blindly |
| C5 parent close | DB/review state mutation only | separate close endpoint | Parent may remain open or close after commit | Reopen is separate user action | Separate postcondition, never implicit success |
| C5a parent-close failure | Local commit and possibly mark-applied happened | Close response plus fresh parent/job/Git readback | Committed fix with parent still open/unknown | No automatic reopen/rollback | `failed-unverified` / needs attention |
| C5b post-commit review ambiguity | Commit succeeded; mark/close/readback may be unknown | Git plus daemon review/job source of truth | Commit exists while review state is uncertain | No automatic commit rollback | `failed-unverified`; require manual reconciliation |
| C6 stale/rebase | DB enqueue plus old-row state mutation | `/api/job/fix` with `stale_job_id`, then `/api/job/rebased` | New queued job and stale row may diverge | No atomic pair | Unsupported until frozen stale/base contract |

## Required concern table

| Concern | Upstream authority | Verifiable before mutation | Token binding | Residual race | Decision |
|---|---|---|---|---|---|
| Exact lifecycle-free generation | `fix.go`, `daemon_lifecycle.go`, worker | No; `/api/job/fix` is daemon background work, while CLI Fix has hidden lifecycle | None | Daemon recovery/config/worker can change after preview | NO-GO |
| Repository binding | `humaFixJob`, daemon repository lookup, Dashboard Registry | Canonical root and registered opaque identity can be inspected, but are not one upstream enqueue proof | No repository identity/root-generation binding | Registration, root identity or selected repository can drift | NO-GO |
| Parent review eligibility | `humaFixJob` only checks existence/not-Fix and later review presence | Not done/P/open/current/selected ownership as one proof | No parent state/version binding | Review close/output/current HEAD may change | NO-GO |
| Branch/ref binding | `FixJobRequest.GitRef` validation and fallback to parent ref/branch/`HEAD` | A full SHA can be supplied, but the endpoint does not require it; movable refs are accepted | No immutable branch/ref witness | Branch/ref can move before worker claim | NO-GO |
| HEAD/base SHA | Worker worktree `BaseSHA`, parent commit and Git ref | Worker records its own base only after checkout | No exact full SHA requirement or confirmation binding | HEAD/ref can drift between preview, enqueue and claim | NO-GO |
| Config precedence and hot reload | `ResolveFixReasoning`, `ResolveWorkflowConfig`, `ConfigWatcher` | File snapshot is not daemon effective state | No config digest/reload generation | Reload between preview, claim and agent start | NO-GO |
| Primary agent | `ResolveAgentForWorkflow*`, `GetPreferredOrBackup*` | The selected primary is stored and observable in the post-enqueue receipt, which is too late for pre-mutation authorization | No pre-enqueue primary executor attestation | Live command/ACP config is read again, and later failover can select another agent | NO-GO |
| Backup agent/failover | `failOrRetryAgent`, `FailoverJob` | Backup can be listed but not frozen | No backup executor/model reservation | Retry/quota/session error selects backup later | NO-GO |
| Model/provider | Workflow model resolution and agent adapter | The primary model is stored after enqueue; provider is not a request field and may remain implicit/empty rather than an attested choice | No pre-enqueue model/provider attestation | Live backup resolution or ACP pairing can change the failover model/provider | NO-GO |
| Reasoning | Handler `ResolveFixReasoning`, storage `job.Reasoning`, worker `WithReasoning` | Stored reasoning is observable in the receipt and is what the worker uses | No pre-enqueue confirmation-token/config-generation binding | Executor, failover and safety profile can change even though stored reasoning does not | NO-GO |
| Agentic/approval mode | `Agentic:true`, `WithAgentic(true)`, agent adapters | Effective dangerous/approval flags are not exposed as attestation | No safety-profile binding | Adapter config, inherited environment and failover executor behavior remain live | NO-GO |
| Sandbox/writable roots | Codex/Claude/Gemini/Grok adapters and process environment | No bounded root/approval proof | No writable-root binding | Agent command/network/credential authority remains | NO-GO |
| Daemon version | Runtime daemon/version and handler | Version can be probed, but enqueue does not bind it to worker execution | No exact daemon generation/version token | Daemon restart/upgrade/recovery can change behavior | NO-GO |
| Queue pause barrier | `/api/queue/pause`, worker claim loop | Only a point-in-time paused bit | No global producer/worker lease | Running/unrelated jobs and new producers race | NO-GO |
| Worktree ownership | `gitworktree.Create`, worker `createWorkerWorktree` | Path and Git relationship can be checked | No OS containment token | Links, Git metadata, process and cleanup remain | NO-GO |
| Submodule/LFS/binary/mode capture | `InitSubmodules`, `PullLFS`, `CapturePatch` | Representation is not a complete side-effect proof | No patch manifest | Git filters/artifacts and untracked effects differ | NO-GO |
| Patch digest/size | `CapturePatch`, `GET /api/job/patch` | Current endpoint has no response bound or digest | No bounded repo-bound envelope | Large/sensitive/raw patch can escape or be confused with another job | NO-GO |
| Patch files/manifest | `CapturePatch`, TUI `patchFiles` | Touched names can be listed, but no signed/complete file manifest exists | No canonical allowed-file set | Patch may include forms not represented by the filename list | NO-GO |
| Patch parser/path safety | `sourcegraph/go-diff`, `git apply --binary -` | TUI filename list is insufficient | No canonical path policy | Windows/case/reparse/traversal ambiguity | NO-GO |
| Selected checkout | `WorktreePathForBranch` and TUI branch lookup | Not whole-tree clean/exact HEAD/unique target | No checkout identity + lease | branch/worktree/root can change after check | NO-GO |
| Working tree | TUI touched-file check plus Git status | Whole-tree clean baseline and exact HEAD are not one upstream precondition | No clean-tree/root identity binding | Untracked/ignored files, unrelated edits, or checkout change after check | NO-GO |
| Index/staging | TUI `git add --`, `git commit --only` | Touched files only, not an isolated index | No index snapshot/staging reservation | Existing index state or stage failure can persist | NO-GO |
| Hooks | Worker hook suppression and target `git commit --only` | Worker temp hooks policy can be observed; target hook state is not frozen | No hooks-path/config policy binding | Commit hooks/post-commit can mutate or fail | NO-GO |
| Git command environment | kit `gitcmd.New()` versus TUI raw `exec.Command` | kit check/apply strips inherited `GIT_*` and pins Git config; TUI add/commit inherit `os.Environ()` | No one-environment binding across check/apply/stage/commit | Inherited `GIT_*` can select different repository/config behavior | NO-GO |
| Check-to-apply TOCTOU | TUI `CheckPatch` then `ApplyPatch` | Dry-run is only a point-in-time check | No atomic Git reservation | Files/index/HEAD can change between commands | NO-GO |
| Apply behavior and full-flow transaction | kit `ApplyPatch` invokes `git apply --binary -` without `--reject` | Ordinary hunk conflict normally rejects the whole patch; generic I/O/concurrency needs readback | No transaction token or rollback contract for later steps | Apply, stage, commit and state calls remain separate | NO-GO; ambiguous mutation is failed-unverified |
| Mark applied idempotency | `MarkJobApplied`: `done` + `fix` only | Local commit does not prove DB state | No commit digest/idempotency key | Local commit succeeds, API fails/retries | NO-GO |
| Parent close | Separate TUI close request | Can verify only after commit | No coupled parent transition | Close failure leaves open review | Separate postcondition |
| Stale/rebase | `stale_job_id` validation and `/api/job/rebased` | Old patch/status can be checked | No atomic old/new reservation | New enqueue and old mark can split | NO-GO |
| Browser privacy | Raw upstream job/patch DTOs vs Dashboard Fix projection | Dashboard can redact but endpoint does not | No privacy-safe patch token | Logs/errors/raw fields can leak | Preserve existing boundary |

## Smallest credible unblock

The smallest future design is not “call `/api/job/fix` after confirmation.” It
requires an atomic, server-owned frozen **Fix plan/reservation token** created
before any agent or Git mutation. At minimum the token must bind:

* opaque selected repository identity, canonical root identity and parent review
  ID/ownership;
* full parent review target, branch and exact base SHA (not a movable ref);
* whole-tree clean baseline, selected checkout/worktree identity and allowed
  relative target root;
* exact primary and backup agent, model, provider, reasoning and failover
  policy, with a daemon-effective config digest/reload generation;
* exact executor command identity, environment allowlist, sandbox mode,
  dangerous/approval settings and bounded writable-root set; and
* reservation state that the daemon worker must consume atomically and cannot
  silently re-resolve, fail over, or broaden after claim.

That token still needs a safe, contained agent mode: agentic file edits must be
restricted to a verified checkout/root, with no dangerous bypass, no ambient
credentials/network unless separately authorized, no arbitrary sub-process
writes outside the root, and explicit cleanup/readback semantics. A Git
worktree alone does not satisfy this requirement.

Separately, Phase B/C need a bounded repo-bound patch envelope and safer apply
contract. The envelope must include a hard byte limit, exact base/repository
identity, digest, allowed file manifest, UTF-8/path policy, explicit handling of
binary/mode/symlink/submodule/LFS forms, and no private upstream fields. The
apply contract must require one selected checkout, whole-tree clean state, exact
HEAD, stable path identities and a Git operation that either provides a proven
atomic transaction or honestly reports `failed-unverified`; it must also define
isolated index/commit, hooks policy, commit metadata, mark-applied idempotency,
parent-close ordering and postconditions. Until both contracts exist, no phase
is a production GO.

## Next checkpoint

The next independent checkpoint is **CI Metrics Export Feasibility Audit**. It
must be a separate source audit and must not be implemented as part of this
checkpoint. This document does not add or enable any Fix, patch apply, commit,
rebase, Tasks, Analyze, Review Export or Repository Delete surface.
