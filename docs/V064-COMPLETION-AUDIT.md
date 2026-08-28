# Remaining P Triage & v0.64 Completion Audit

Date: 2026-08-28

Dashboard base: `664c8825d9ee850cfc7e5a76e3fb365ec8f411c6` (`Audit CI metrics export feasibility`)

Upstream authority: kenn-io/roborev v0.64.0, commit `bc0af33c282733a9c39d72c08373b040e22c11d4`

This is a source-and-documentation audit. It does not add a production endpoint,
invoke RoboRev, start or stop a daemon, run an agent, or call an LLM. The
repository source and tests are authoritative; the exact upstream v0.64.0
source is the external contract; the Dashboard design conversation is context
only.

## Decision

The remaining MUST-SHIP count is **0**. The Dashboard is a **v0.64 feature-
complete candidate**, not a release-ready product. “Complete” here means that
the safe supported user outcomes have an implemented, tested surface, while
unsafe, ambiguous, lifecycle-bearing and upstream-blocked outcomes remain
explicitly unavailable. It does not mean full CLI parity.

The exact stop condition is:

> `remaining MUST-SHIP == 0`

The one selected next checkpoint is **Final v0.64 Security & Regression
Closure**. It is a release-closure checkpoint, not permission to implement the
deferred CLI backlog.

## Before-audit inventory

The physical Coverage table contained 145 status-bearing row occurrences:

| Measure | Occurrences | Meaning |
|---|---:|---|
| B | 70 | Implemented Checkpoint B rows |
| P-like | 37 | `P`, `P/disabled`, `P/blocked...`, or `N/P` rows |
| N-like | 40 | `N` or `N/...` rows |
| Main matrix | 116 | Exclusive allocation is 53 B + 31 P-like + 32 N-only = 116 |
| Main-matrix raw P-like | 31 | Includes the two `N/P` rows |
| Main-matrix raw N-like | 34 | Includes the same two `N/P` rows |
| Main-matrix P/N overlap | 2 | The two `N/P` rows counted in both raw sets |
| Main-matrix exclusive N-only | 32 | Raw N-like 34 minus the two overlapping `N/P` rows |
| Repeated mirrors | 6 | checkpoint/correction mirrors of six top-matrix capabilities |

P-like and raw N-like are intentionally **not disjoint**: two `N/P` rows occur
in both sets. The main matrix removes six repeated mirrors. Its raw counts are
53 B, 31 P-like and 34 N-like; after removing the two overlaps from N-like,
the exclusive allocation is **53 B + 31 P-like + 32 N-only = 116**. The
full-document raw N-like count remains 40 because it includes all physical
rows, not only the main matrix. The full capability ledger below is normalized
after composite rows, CLI bundles and omitted exact commands are split,
producing 50 triage units.

## Taxonomy

| Code | Meaning |
|---|---|
| A — MUST-SHIP | Safe supported user outcome required for the v0.64 product contract. |
| B — OPTIONAL-ADVANCED | Useful operator/advanced work, but not required for the supported product outcome. This audit taxonomy is distinct from Coverage status `B`, which means an implemented Checkpoint B vertical slice. |
| C — SUPERSEDED | The exact CLI/API exists, but an implemented, safer Dashboard surface replaces it. |
| D — UNSUPPORTED-BY-DESIGN | Deliberately excluded because it is destructive, arbitrary, privacy-sensitive or outside Dashboard authority. |
| E — BLOCKED-BY-V0.64 | A useful outcome is known, but exact v0.64 lacks the safety/identity/lifecycle contract required to expose it. |
| F — DOCS/CLI-UX-ONLY | CLI entry, alias, completion or internal/operator behavior with no Dashboard product outcome. |
| G — NEEDS-SEPARATE-AUDIT | A distinct product/security question not decided by this ledger. None remain in this audit. |

## Full 50-unit ledger

“Completion blocker?” means a blocker for v0.64 feature completion, not a
release-readiness gap. Existing B substitutes are named without implying that
the upstream command itself is called.

| Command/API | Current status | Existing Dashboard substitute | User value | Risk/mutation class | Exact v0.64 issue | Final taxonomy | Completion blocker? | Next action |
|---|---|---|---|---|---|---|---|---|
| `agent-hook reset` | P (bundled) | Single-agent Agent Hook Management | Reset stale managed state | Writes agent-hook state | Operator reset semantics are broader than the readiness contract | B — OPTIONAL-ADVANCED | No | Keep out of v0.64; design a narrow reset/readback adapter later |
| `sync status` | P (bundled) | No Dashboard surface | Inspect PostgreSQL sync state | Reads DB/machine/schema state | May initialize local DB/machine ID and PostgreSQL schema | B — OPTIONAL-ADVANCED | No | Require a bounded operator-only adapter |
| `sync now` | P (bundled) | No Dashboard surface | Push/pull sync now | Network + persistent sync mutation | Hidden `ensureDaemon`; global push/pull can run 5–6 minutes | B — OPTIONAL-ADVANCED | No | Require explicit sync scope, cancellation and readback |
| `log clean` | P (bundled) | No Dashboard surface | Reclaim old log storage | Destructive local deletion | Deletes DataDir job logs older than seven days; no rollback | B — OPTIONAL-ADVANCED | No | Add an explicit bounded cleanup contract only if prioritized |
| `update --check` | P (bundled) | Runtime version/install status | Check for an update | Network/cache-writing operator read | The check can write cache and is not a stable v0.64 product read | B — OPTIONAL-ADVANCED | No | Revisit with bounded cache and privacy policy |
| `backfill-tokens` | P (bundled) | Stats/Cost read-only views | Repair historical token usage | DB/log maintenance, partial writes | Scans DB/logs and may call agentsview; best-effort writes can be partial | B — OPTIONAL-ADVANCED | No | Keep CLI-only until transactional, bounded maintenance is specified |
| `snooze` | N (bundled) | Agent Hook readiness/management status | Silence agent-hook reminder | Small persistent state mutation | Changes reminder state only; it is not review-queue snoozing | B — OPTIONAL-ADVANCED | No | Document as operator-only; do not add a review CTA |
| `repo list` | P (bundled) | `GET /api/repos` inventory | See tracked repositories | Read-only inventory | Human CLI output has no need to be parsed; daemon API is the typed source | C — SUPERSEDED | No | Keep the API-backed inventory |
| `repo show` | P (bundled) | Repository inventory + repository-scoped history | Inspect one tracked repository | Read-only inspection | No independent safe Dashboard value beyond existing scoped cards | C — SUPERSEDED | No | Keep existing typed views |
| `agent-hook install --agent all` | P (bundled) | Single-agent install/update | Install all agent hooks | Multi-agent filesystem mutation | Broad path visits several agent trees and has no one-resource readback | C — SUPERSEDED | No | Keep the single-agent surface |
| broad `skills install` | P (bundled) | Single-agent Skills Management | Install skills | Multi-agent filesystem mutation | No-path branch may write several trees and remove legacy content | C — SUPERSEDED | No | Keep server-derived single-agent install |
| broad `skills update` | P (bundled) | Single-agent Skills Management | Update skills | Multi-agent/legacy cleanup mutation | Can update several agents and remove `roborev-address` | C — SUPERSEDED | No | Keep the bounded single-agent surface |
| upstream direct `WriteWorkflow` create intent | P (bundled) | CI Workflow Preview + create-only Management | Create CI workflow | Filesystem mutation | Upstream writer uses `Stat`/`MkdirAll`/`WriteFile`; not the Dashboard safety contract | C — SUPERSEDED | No | Keep the fixed-target create-only adapter |
| `quickstart` | N (bundled) | Setup and runtime cards | Follow onboarding instructions | CLI UX | It is a human onboarding entry point, not an independent Dashboard authority | C — SUPERSEDED | No | Maintain README/setup guidance |
| `tui` | N (bundled) | History, detail and explicit action cards | Use terminal UI | Interactive CLI UX | TUI is a separate interactive product surface with no browser equivalence | C — SUPERSEDED | No | Keep Dashboard UI boundaries explicit |
| root `wait` | unlisted | Review polling/SSE and lazy detail | Wait for a job | CLI wait UX | Dashboard owns bounded refresh and authoritative reads; root wait is not a mutation authority | C — SUPERSEDED | No | Keep polling/SSE safety contract |
| Analyze arbitrary path/file/glob | P | Analyze type list and prompt read | Choose an analysis target | Arbitrary target/agentic execution | v0.64 analysis execution is agentic and Dashboard accepts no browser path/ref | D — UNSUPPORTED-BY-DESIGN | No | Preserve server-selected repository scope |
| custom task agent | P | Task history only | Choose a task agent | Arbitrary agent execution | Dashboard does not accept/store custom agent selection | D — UNSUPPORTED-BY-DESIGN | No | Require a separately frozen allowlist contract |
| custom task model | P | Task history only | Choose a task model | Arbitrary model execution | Dashboard does not accept/store model overrides | D — UNSUPPORTED-BY-DESIGN | No | Keep model authority out of browser |
| `agent-hook dump` / raw config | P (bundled) | Sanitized Agent Hook status | Inspect merged configuration | Sensitive read | Dump can expose merged config, paths and raw values | D — UNSUPPORTED-BY-DESIGN | No | Keep raw config server-private |
| `skills install --path <browser path>` | P | Server-derived agent skills target | Choose custom install path | Arbitrary filesystem mutation | Browser path is not an authorized target; safe target is server-derived | D — UNSUPPORTED-BY-DESIGN | No | Keep custom destinations rejected |
| workflow `--force` | P/unsupported | Create-only workflow Management | Force overwrite workflow | Destructive overwrite | Exact v0.64 supports force, but Dashboard must not overwrite existing workflow | D — UNSUPPORTED-BY-DESIGN | No | Preserve existing-target no-CTA policy |
| workflow overwrite/replace/backup | P/unsupported | Drift comparison only | Replace an existing workflow | Destructive/ambiguous filesystem mutation | No atomic backup/replace/readback contract exists | D — UNSUPPORTED-BY-DESIGN | No | Require a separate immutable replacement design |
| upstream self-update `update`/install | P (bundled) | Pinned runtime installer | Upgrade RoboRev | Binary replacement + lifecycle + hook/skill repair | Replaces binary, may restart daemon and repair hooks/skills, and violates exact v0.64 pin | D — UNSUPPORTED-BY-DESIGN | No | Keep upgrades a separate v0.65+ milestone |
| new Panel Refine | P/disabled/BLOCKED | Panel history/detail and ordinary Review HEAD | Iterate review and fix | Agentic Fix + commit + repeated review | Explicit v0.64 Fix enters hook normalization/`ensureDaemon`; no lifecycle-free contract | E — BLOCKED-BY-V0.64 | No | Keep 501 tombstone; require safe upstream Fix mode |
| General Fix execute | P/disabled/BLOCKED | Read-only Fix eligibility/history and Fix Prompt | Apply a finding fix | Agentic filesystem/Git mutation | `fix` can normalize hooks and start/restart/recover daemon | E — BLOCKED-BY-V0.64 | No | Keep execution blocked; see Fix lifecycle audit |
| General Refine | P/blocked-by-upstream-contract | General Refine tombstone + history | Iterative refine | Agentic review/Fix mutation | Its v0.64 Fix path has the same hidden lifecycle authority | E — BLOCKED-BY-V0.64 | No | Keep blocked until upstream contract changes |
| `analyze <type> --json` enqueue | P | `analyze --list` and `--show-prompt` | Run analysis | Agentic Edit/Write/Bash | Upstream enqueue sets `Agentic:true` and worker `WithAgentic(true)` | E — BLOCKED-BY-V0.64 | No | Require an explicit safe capability/receipt |
| `analyze --fix` | P | Analysis introspection | Apply analysis result | Agentic Fix mutation | Depends on blocked agentic analysis and Fix semantics | E — BLOCKED-BY-V0.64 | No | Reassess with safe analysis and Fix contracts |
| `analyze --per-file` | P | Analysis introspection | Fan out analysis by file | Agentic fan-out | Same unsafe enqueue; no bounded fan-out contract | E — BLOCKED-BY-V0.64 | No | Reassess only with a bounded safe transport |
| `run --json` task enqueue | P/disabled | Task history/detail | Run a custom task | Agent/process mutation | Effective safety (`allow_unsafe_agents`, sandbox settings) is not attested by v0.64 | E — BLOCKED-BY-V0.64 | No | Require machine-readable effective safety identity |
| task preview tombstone | P/disabled | Typed 501 safety tombstone | Preview a task | Pre-execution authority | A preview cannot safely describe an executable whose effective safety is unknown | E — BLOCKED-BY-V0.64 | No | Keep no-call tombstone |
| task execute tombstone | P/disabled | Typed 501 safety tombstone | Confirm a task | Agent/process mutation | Same missing safety attestation; no execution path is authorized | E — BLOCKED-BY-V0.64 | No | Keep execution disabled |
| `run --agentic` | P | Task history only | Force agentic mode | Explicit unsafe agentic execution | Dashboard exposes no task execution authority | E — BLOCKED-BY-V0.64 | No | Revisit only after a safe task capability |
| `run --yolo` | P | Task history only | Force agentic mode | Explicit agentic execution | Alias of `--agentic`; the same missing v0.64 safety attestation applies | E — BLOCKED-BY-V0.64 | No | Revisit only after a safe task capability |
| `run --wait` | P | Review/task status refresh | Wait for task completion | CLI process/wait UX | Enqueue and completion waiting are not one safe Dashboard contract | E — BLOCKED-BY-V0.64 | No | Use authoritative status reads if task execution ever unblocks |
| custom task reasoning | P | Task history only | Choose reasoning effort | Agent execution override | Browser does not receive a frozen effective reasoning/config identity | E — BLOCKED-BY-V0.64 | No | Require a server-owned fixed policy |
| `compact` | P | No Dashboard surface | Consolidate findings | Agentic + non-atomic metadata mutation | Even `--dry-run` calls `ensureDaemon`; operation is agentic, non-atomic and not a frozen safe plan | E — BLOCKED-BY-V0.64 | No | Keep blocked; require a no-lifecycle atomic plan |
| `daemon run` | N/P | Runtime Diagnostics and already-running lifecycle cards | Run foreground daemon | Foreground lifecycle | Local UI does not own terminal foreground lifecycle | F — DOCS/CLI-UX-ONLY | No | Document as CLI-only |
| `agent-hook status` | P (bundled) | Agent Hook Readiness | Inspect status | CLI status/lifecycle-adjacent read | Exact command can enter lifecycle-bearing client setup | F — DOCS/CLI-UX-ONLY | No | Keep typed Dashboard readiness adapter |
| `agent-hook daemon` family | P (bundled) | Runtime health/status | Inspect agent-hook daemon | Lifecycle/diagnostic CLI | Runtime record cleanup and lifecycle semantics are not a pure read | F — DOCS/CLI-UX-ONLY | No | Keep out of Dashboard |
| `agent-hook run` | P (bundled) | Agent Hook status/readiness | Run hook worker | Process/lifecycle execution | No independent browser contract or safe bounded receipt | F — DOCS/CLI-UX-ONLY | No | Keep CLI-only |
| `ci review` | unlisted | CI Workflow Preview/Management/Drift | Run CI-side review | Pipeline-side multi-agent/forge action | v0.64 `ci` has review-oriented pipeline behavior, not a local Dashboard product API | F — DOCS/CLI-UX-ONLY | No | Document integration boundaries |
| shell completion | N (bundled) | README/CLI help | Complete CLI commands | CLI UX | v0.64 Cobra completion callbacks exist; there is no standalone completion product command | F — DOCS/CLI-UX-ONLY | No | Keep CLI help responsibility |
| `post-commit` | N (bundled) | Git Hook Status/Management | Hook callback | Internal hook entry | It is invoked by Git/RoboRev hook flow, not an independent browser action | F — DOCS/CLI-UX-ONLY | No | Keep hook management boundaries |
| `remap` | unlisted | Repository path repair/merge | Repair internal mapping | Internal maintenance | No independent typed Dashboard contract is established | F — DOCS/CLI-UX-ONLY | No | Keep CLI/operator-only |
| `backfill-verdicts` | unlisted | Review history/status | Repair verdict data | DB maintenance | Internal best-effort repair has no Dashboard transaction/readback contract | F — DOCS/CLI-UX-ONLY | No | Keep CLI/operator-only |
| `enqueue` alias | unlisted | Review HEAD | Enqueue review | CLI alias | Dashboard uses the typed daemon enqueue contract and fixed UI terminology | F — DOCS/CLI-UX-ONLY | No | Keep alias undocumented as a product surface |
| `respond` alias | unlisted | Comment & Close | Respond to finding | CLI alias | Dashboard owns explicit comment/close ordering and authorization | F — DOCS/CLI-UX-ONLY | No | Keep explicit UI action |
| `prompt` alias | unlisted | Lazy Prompt / Fix Prompt | Read prompt | CLI alias/content read | Dashboard uses bounded authorized lazy reads and local safe templates | F — DOCS/CLI-UX-ONLY | No | Keep privacy-safe lazy content boundary |

Ledger totals: **A 0, B 7, C 9, D 8, E 14, F 12, G 0**.

## Remaining MUST-SHIP

| MUST-SHIP unit | Result |
|---|---|
| None | The count is zero. All safe v0.64 user outcomes are covered by the existing B product inventory below; every remaining P-like unit is optional, superseded, unsupported, blocked by the exact upstream contract, or CLI/docs-only. |

## Optional advanced backlog (7)

| Feature | Why optional | Who needs it | Future condition |
|---|---|---|---|
| Agent Hook reset | Repairs operator state but is not setup readiness | Operators maintaining hook installations | Narrow target binding, confirmation and readback |
| Sync status | PostgreSQL sync is not required for local review workflow | Operators using sync | Read-only adapter that cannot initialize or mutate state |
| Sync now | Long-running global push/pull is not a core review action | Sync operators | Explicit scope, cancellation, timeout and authoritative readback |
| Log clean | Storage cleanup is maintenance, not review functionality | Disk-constrained operators | Bounded age/target selection, confirmation, partial-failure reporting |
| `update --check` | Update availability is outside the pinned runtime contract | Release/operator users | Bounded cache/network policy and privacy review |
| `backfill-tokens` | Historical repair does not affect current review outcomes | Maintainers repairing old metrics | Transactional/bounded maintenance and partial-write readback |
| Agent Hook snooze | It only changes a reminder, not review state | Operators suppressing reminders | Explicit reminder-only semantics and isolated state readback |

None is authorized by this audit for implementation.

## Upstream-blocked backlog (14)

| Feature | Unblock trigger |
|---|---|
| Panel Refine | Lifecycle-free Fix/refine transport with frozen repository, HEAD, executable, config and sandbox authority |
| General Fix execute | Same lifecycle-free Fix contract and detached authoritative result |
| General Refine | Lifecycle-free refine orchestration and Fix contract |
| Analyze enqueue | Explicit safe capability, bounded receipt and confirmation contract; no agentic Edit/Write/Bash ambiguity |
| Analyze `--fix` | Safe Analyze plus lifecycle-free Fix |
| Analyze `--per-file` | Safe Analyze plus bounded fan-out and aggregate readback |
| Task enqueue | Machine-readable effective safety/config identity and frozen server-owned policy |
| Task preview | Same safety identity must be available before preview authority is described |
| Task execute | Same safety identity plus bounded execution/readback |
| `run --agentic` | Safe task capability that explicitly attests agentic authority |
| `run --yolo` | Safe task capability; it is the exact v0.64 `--agentic` alias, not a separate guardrail mode |
| `run --wait` | Authorized task enqueue plus bounded authoritative completion read |
| Custom task reasoning | Server-owned frozen reasoning/config identity and allowlist |
| `compact` | Lifecycle-free, atomic/readback-verifiable plan that is safe even for `--dry-run` |

Until then, 501/no-call tombstones and history-only views are the correct
architecture, not unfinished implementation.

The following focused families are outside the 14-unit ledger count and remain
blocked or unsupported:

| Focused family | Unblock trigger |
|---|---|
| Review Export | Atomic repository/root-bound selector and response identity that proves selected-root isolation |
| CI Metrics | Repository/root/host-bound complete export with bounded nested data and a stable snapshot/cursor contract |
| Repository Delete | Safe non-cascade delete contract with complete dependency/readback proof; cascade remains intentionally unsupported |
| Fix patch generation/apply | Frozen safe Fix plan, bounded repository-bound patch envelope, and authoritative apply/commit/readback contract |

## Superseded and unsupported boundaries

The nine superseded units are covered by existing typed Dashboard surfaces or
documentation. The eight unsupported units are intentionally absent because
they would weaken path, config, overwrite, agent or lifecycle authority. The
focused decisions/families outside this 50-unit ledger remain unchanged:

| Superseded unit | Replaced by |
|---|---|
| `repo list` | Typed `GET /api/repos` inventory |
| `repo show` | Inventory plus repository-scoped history/detail |
| `agent-hook install --agent all` | Single-agent Agent Hook Management |
| Broad `skills install` | Server-derived single-agent Skills Management |
| Broad `skills update` | Server-derived single-agent Skills Management |
| Direct `WriteWorkflow` create intent | CI Workflow Preview plus create-only Management |
| `quickstart` | Setup cards and README guidance |
| `tui` | Dashboard history/detail/action cards |
| Root `wait` | Bounded review polling/SSE and authoritative reads |

- Review Export remains blocked for v0.64 repository-isolation/TOCTOU reasons.
- Selected panel-era and legacy CI Metrics remain unsupported by Dashboard
  safety; direct CLI is superseded and the global daemon export has no product
  surface.
- Repository Delete remains blocked for non-cascade and unsupported for
  cascade. Root-init safe outcomes are superseded by independent B surfaces;
  unsafe aggregate residue remains unsupported. Fix patch generation/apply is
  still NO-GO under the focused Fix Patch Feasibility audit.

## Current B product inventory

The implemented v0.64 product is the following deliberately narrow set:

| Area | Supported outcomes |
|---|---|
| Runtime/setup | Pinned install, version/capability binding, health, diagnostics, queue pause/resume, explicit daemon lifecycle |
| Repository | Selection, inventory, setup readiness, Snapshot Ignore, tracking registration, display-name rename, stale path repair, safe duplicate merge |
| Configuration | Effective config read, allowlisted config mutation, global and selected repository agent configuration |
| Hooks/skills | Git Hook status/install/repair/remove, Agent Hook readiness/management, single-agent Skills readiness/management |
| Review | Review HEAD, panel selection, panel member results/history, history/detail, lazy prompt/log, rerun, cancel, comment/close/reopen, safe Fix Prompt |
| Insights | Stats, Cost, Guideline Insights |
| CI workflow | Preview, fixed-target create-only management, byte-level drift comparison |
| Safety boundaries | Same-origin/CSRF, opaque identities/tokens, server-owned canonical roots, bounded reads, no raw upstream detail, detached post-mutation readback |

## Release readiness is separate

Feature completion does not imply release readiness. Read-only remote checks
found no local `.github` workflows and GitHub Actions reported zero workflows.
Branch-protection and ruleset API checks returned 403 because the current
private-repository plan does not support that feature; therefore no required
checks or protection can currently be verified or enforced there. This is a
release-process gap, not a MUST-SHIP feature gap.

The release-readiness gaps are:

- CI/status checks and branch-protection/ruleset enforcement;
- reproducible release packaging, signing, checksums/archive publication and
  Dashboard version/commit stamping (the current stripped executable build has
  no apparent stamp);
- clean-install and upgrade-compatibility E2E, including the exact v0.64 pin
  and a separately planned v0.65 transition;
- crash recovery, interrupted-operation behavior and packaging/upgrade
  migration evidence;
- explicit privacy/telemetry disclosure and a final README accuracy pass;
- non-mutating Windows smoke evidence on a clean machine.

These gaps must not be counted as remaining v0.64 product capabilities.

## Windows non-destructive smoke shortlist

The release-closure smoke should use a deterministic or offline setup and begin
with `--no-open` on loopback. It may cover selected repository load/inventory,
already-running health/status, review history/detail, setup readiness, Stats,
configuration read and clean shutdown. It must not run Review, Fix, Refine,
Task, Analyze, CI Export, daemon lifecycle mutation or any LLM/agent action.

## v0.65 upstream watchlist

Watch for a lifecycle-free Fix/refine API, effective safety/config attestation
for tasks, bounded safe Analyze execution, atomic compact plans, repository-
bound patch envelopes/apply readback, root-bound CI metrics export, and stable
versioned migration/install contracts. A v0.65 capability is not implicitly a
v0.64 implementation target.

## Audit close

No G — Needs Separate Audit unit remains. The v0.64 feature-completion stop
condition is exactly `remaining MUST-SHIP == 0`, which is satisfied. The one
next checkpoint is **Final v0.64 Security & Regression Closure**; it should
close release evidence and regression risk without reopening the rejected
CLI-parity backlog.
