# General Refine — blocked by the v0.64 fix lifecycle contract

General Refine was designed as a Dashboard-owned current-HEAD loop: fresh ordinary review, an exact fix for a finding, proof of a clean descendant HEAD, then another fresh ordinary review. The design remains useful, but RoboRev v0.64.0 does not expose a lifecycle-free current-checkout Fix transport. General Refine therefore remains **P / blocked-by-upstream-contract** and does not execute.

## Why the workflow is blocked

The proposed loop depended on `roborev fix --agent codex --quiet <owned-review-id>`. In exact v0.64.0 that command:

- runs best-effort `EnsureAbsoluteHooksPath` from Cobra `RunE`, which can write local Git configuration for a relative non-tilde `core.hooksPath`;
- calls `ensureDaemon` unconditionally from the explicit-ID `runFixWithSeen` path;
- calls the same `ensureDaemon` again from daemon refresh/recovery paths;
- can consequently start or restart the daemon and clean up legacy/zombie daemons.

Those effects are valid self-healing behavior for a person invoking the CLI directly. They are not acceptable hidden authority behind a browser-confirmed mutation whose preview promises only a repository Fix. An outer Dashboard preflight, final root guard, exact endpoint, or `--server` selection cannot remove behavior executed inside the child command.

The full source audit and unblock conditions are recorded in [FIX-LIFECYCLE-AUDIT.md](FIX-LIFECYCLE-AUDIT.md).

## Fail-closed HTTP and UI boundary

- `POST /api/general-refines/preview` is a strict authorized tombstone and returns HTTP 501 with `phase=general-refine-fix-lifecycle-blocked`.
- `POST /api/general-refines` returns the same blocked contract.
- Neither endpoint resolves a repository, probes Git or daemon state, enqueues a review, starts a Fix/refine process, or invokes daemon lifecycle.
- The UI presents General Refine as unavailable and has no executable confirmation or start control. It makes no General Refine preview/start/status/cancel request.
- Browser input never gains path, branch, HEAD, review ID, panel, agent, model, reasoning, prompt, executable, daemon endpoint, or iteration authority.

The safe design facts remain documentation, not a production execution manager. There is no automatic review, Fix, Refine, retry, recovery, history adoption, stash/reset/checkout/revert, rollback, commit, push, daemon action, or LLM call.

## Why `/api/job/fix` is not substituted

The v0.64 daemon endpoint creates a background agentic `JobTypeFix`, resolves daemon-side workflow configuration, agent, model and reasoning, runs in the job/worktree model, and stores a generated patch for later application semantics. That is not the CLI Fix contract that operates on the selected current checkout and owns its commit/comment/close/review behavior. Substitution would silently change the mutation, state ownership, readback and recovery model.

A future checkpoint may reconsider General Refine only after one of the explicit unblock contracts in the lifecycle audit exists. This checkpoint does not implement a daemon patch/apply/commit workflow.
