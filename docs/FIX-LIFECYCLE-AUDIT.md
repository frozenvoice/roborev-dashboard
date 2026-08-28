# Fix Lifecycle Safety Audit — RoboRev v0.64.0

This audit records why browser-authorized Dashboard Fix execution is fail-closed on exact RoboRev v0.64.0 (`bc0af33c282733a9c39d72c08373b040e22c11d4`). The finding affects standalone General Fix, the Fix phase of a newly started Panel Refine, and the proposed General Refine workflow. The separate [`FIX-PATCH-FEASIBILITY.md`](FIX-PATCH-FEASIBILITY.md) audit now concludes **NO-GO** for substituting daemon background Fix with a browser patch/apply workflow.

## Exact upstream call paths

| Surface | v0.64.0 source evidence | Hidden authority |
|---|---|---|
| Fix Cobra entry | `cmd/roborev/fix.go` `RunE` resolves `gitrepo.Root` and best-effort calls `gitrepo.EnsureAbsoluteHooksPath` before option dispatch | A relative non-tilde `core.hooksPath` can be normalized through local Git config |
| Explicit job-ID Fix | `runFixWithSeen` calls `ensureDaemon` before resolving repository roots or the supplied IDs | Daemon discovery and lifecycle are entered even for `fix --agent codex --quiet <id>` |
| Fix reconnect/refresh | `fixDaemonEnsure = ensureDaemon`; both `refreshFixDaemonAddr` and `waitForFixDaemonRecovery` invoke it | A command that began against an existing daemon can later re-enter lifecycle recovery |
| Daemon ensure | `cmd/roborev/daemon_lifecycle.go` `ensureDaemon` restarts on failed probe, missing version or version mismatch; otherwise it probes the default endpoint, cleans legacy/zombie daemons, then starts one | Start, restart, process cleanup and related daemon/storage effects are possible |

`--server` selects an endpoint but does not disable these paths. `ROBOREV_SKIP_VERSION_CHECK=1` does not disable cold start or recovery and would weaken the Dashboard's exact-version guarantee. A last-moment outer guard cannot prove that a subsequently launched child will not execute its own ensure/recovery or hooks-path normalization.

Upstream self-healing is appropriate for direct CLI users. The defect is not that RoboRev performs recovery; it is that the Dashboard cannot truthfully place that extra authority behind a browser confirmation for only “Fix this review.”

## `/api/job/fix` is a different operation

The daemon route `POST /api/job/fix` is documented as “Create a background fix job.” Its handler validates a parent job, constructs a fix prompt, resolves daemon-side fix severity, workflow configuration, preferred/backup agent, model and reasoning, and enqueues an agentic `JobTypeFix`. The storage model describes that type as a background Fix using a worktree and persists its generated patch.

It does not provide the current-checkout CLI Fix transaction that applies changes and owns the expected commit/comment/close/follow-up-review behavior. Using it would require a separately designed patch inspection, apply, commit, review-state and ambiguous-recovery contract. It is therefore not a drop-in transport and is not called by this checkpoint.

## Dashboard correction

| Dashboard surface | Classification | Production behavior |
|---|---|---|
| General Fix execution | P / disabled / BLOCKED | Authorized `POST /api/fixes` returns HTTP 501 `phase=fix-hidden-lifecycle-blocked`; Fix/process/lifecycle calls are zero |
| New Panel Refine start | P / disabled / BLOCKED | Authorized `POST /api/panel-refines` returns HTTP 501 `phase=panel-refine-fix-lifecycle-blocked` before panel review or Fix; historical reads and existing in-memory cancel/shutdown semantics remain |
| General Refine | P / blocked-by-upstream-contract | Preview/start tombstones return HTTP 501 `phase=general-refine-fix-lifecycle-blocked`; review/Fix/refine/lifecycle calls are zero |

Read-only General Fix eligibility/preview/history, Panel Refine history/detail/timeline, and the independently safe Review HEAD, review history/detail, ordinary rerun, comment/close and repository management surfaces are not reclassified. `FixCommand` capability may still report that the upstream command exists; command existence is not Dashboard execution safety.

## Browser privacy boundary

The Fix HTTP handlers do not serialize `fixoperation.Preview` or `fixoperation.Operation` directly. The app layer projects separate browser DTOs for preview, list, detail, and cancel responses. The internal manager may retain the canonical repository path, the resolved RoboRev executable, and raw process diagnostics for authority and postcondition bookkeeping, but those values stop at the app boundary.

Browser-visible Fix history contains typed semantic fields and `errorCode`; it does not contain `repositoryPath`, the resolved executable, raw `Error`, stderr, or process output. When a command preview is present, its executable is the fixed display value `roborev` and its arguments are rebuilt from the safe semantic job ID (`fix --agent codex --quiet <job-id>`), never copied from manager command state. Privacy fixtures cover both raw and JSON-escaped private path, executable, stderr, and output markers across preview/list/detail/cancel responses, including a completed operation whose internal error contains those markers and a separate active operation exercising cancel compatibility.

## Future unblock conditions

One of these contracts must be designed and audited before Dashboard Fix execution can return:

1. Upstream exposes an explicit-ID current-checkout Fix mode that guarantees no daemon autostart/restart/recovery and no hooks-path normalization.
2. Upstream exposes a lifecycle-free daemon/API current-checkout mutation, commit and authoritative readback contract equivalent to the intended Fix semantics.
3. Dashboard deliberately designs `/api/job/fix` as a different patch-generation workflow, including safe patch inspection, apply, commit, review-state transitions and ambiguity handling. The feasibility audit of that option is concluded **NO-GO** in [`FIX-PATCH-FEASIBILITY.md`](FIX-PATCH-FEASIBILITY.md); no audit-only Phase B or production Phase A/C claim is made.

This checkpoint implements none of those future paths. It only removes unsafe browser reachability and proves the blocked endpoints make zero mutation/lifecycle calls.
