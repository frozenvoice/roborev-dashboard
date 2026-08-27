# Root Init Decomposition Audit

Audit date: 2026-08-27

## Scope and source of truth

This is a read-only decomposition audit of the root `roborev init` command. It does not add an initialization endpoint, aggregate CTA, or production subprocess call. The upstream source of truth is kenn-io/roborev v0.64.0 at commit `bc0af33c282733a9c39d72c08373b040e22c11d4`. Dashboard behavior is compared with the existing independently authorized B surfaces at the exact supported RoboRev v0.64.0 binding.

The taxonomy used here is the repository taxonomy: **B** means an independently implemented and tested Dashboard surface, **P** means a meaningful outcome is still planned or unavailable, and **N** means unsupported by design or superseded by a safer independent surface. A B component is not evidence that the whole root command is available, and a readiness card is not a single `Ready` boolean equivalent to init.

## Exact upstream flow

`cmd/roborev/init_cmd.go` performs the following sequential work from the selected checkout root returned by `gitrepo.Root(ctx, ".")`:

1. It resolves `config.DataDir()` and creates the global data directory with `MkdirAll(..., 0755)`.
2. It calls `os.Stat(config.GlobalConfigPath())`; only `os.IsNotExist(err)` causes it to write `DefaultConfig()` through a temporary file in the configuration directory, apply private `0600` permissions, and rename it into place. The generated file includes the first-time commented hooks example; the writer does not fsync the file or directory. `--agent <value>`, when present, seeds only `DefaultAgent` in this newly created global file. Any `os.Stat` result other than `IsNotExist`, including another error, is treated as an existing-file gate and no global write or parse occurs. A missing global file nevertheless has effective defaults through `LoadGlobal`; materializing the directory and bulk template is not required for effective configuration.
3. With `--agent`, it checks the literal selected checkout root for `.roborev.toml`. Only `os.IsNotExist` creates the file; an existing file or any other stat result is not edited. The config serializer writes the semantic agent value plus generated comments and many non-omitted zero-valued fields, filtering only its four known unintended zero-valued keys, with a new-file mode of `0644`; it is not a one-line writer. This is a literal `Root` path, not the linked-worktree `RepoConfigPath` inheritance lookup; in a linked worktree it can therefore shadow an inherited untracked main-worktree configuration. Cobra does not enforce the help-list as an argument allowlist: help advertises exactly `codex`, `claude-code`, `gemini`, `copilot`, `opencode`, `cursor`, `kiro`, `kilo`, `droid`, `pi`, and `grok`. Creation-time `agentname.ValidateReference` also accepts aliases such as `claude`, `agent`, and `grok-build`, the built-in `acp` and `test` identities, and syntactically valid `acp.<name>` references. Validation trims for checking but the raw supplied value is stored, so surrounding whitespace can survive; a whitespace-only value counts as specified and validates as empty. The value is not validated or applied when both target files already exist.
4. It resolves the configured snapshot directory, rejects unsafe/tracked content, checks the exact ignore probe, and appends the upstream root-anchored rule to the selected checkout's `.gitignore` when needed.
5. It normalizes a relative non-tilde effective `core.hooksPath` to an absolute path rooted at `MainRoot`, resolves the effective hooks directory, creates it, resolves the hook binary, and installs both `post-commit` and `post-rewrite`. Hook-path resolution always begins with Git `rev-parse --git-path hooks`; only a relative result is joined to `MainRoot`. An explicit `--binary` expands `~`; a bare name uses `LookPath`, whose returned string is not guaranteed to be absolute, while only separator-containing relative paths are made absolute. `os.Stat` follows symlinks and rejects directories, not every non-regular file; Unix additionally requires any execute bit, while Windows has no execute-bit requirement. With no override, resolution starts from the current executable, prefers a stable mise/Homebrew shim when available with a notice, and otherwise warns about a versioned manager/current path where applicable. Existing shell hook content is preserved around the managed block; non-shell hook errors are warnings, while other install errors are hard failures.
6. Without `--no-daemon`, it discovers/ensures the daemon, including the upstream start/restart/zombie-cleanup behavior, and then attempts registration. With `--no-daemon`, it skips daemon lifecycle work and attempts registration against an already-running daemon.
7. Daemon and registration failures print warnings and return a successful process exit with an incomplete setup message. Failures in earlier stages are hard errors, but earlier filesystem/config/Git mutations are not rolled back. A successful final message means only that the upstream sequence reported completion; it is not a Dashboard readiness proof.

The snapshot helper uses `ResolveSnapshotDir`, local-path/no-symlink and tracked-file checks, `IgnorePatternForDir`, `check-ignore --quiet --no-index`, and `AppendIgnorePatternFile`. Registration posts exactly `{"repo_path": "<root>"}` to `/api/repos/register`; the daemon canonicalizes the input through `MainRoot`, resolves identity, and persists the tracked repository. Registration itself does not check dirty state, branch, or HEAD.

## Flag and variant matrix

The three flags are syntactically orthogonal. The table enumerates all eight combinations. “P (agent only)” means that the broad command remains unavailable only because the agent-selection outcome is not covered; the other component outcomes retain their N classifications below.

| Flags | Dashboard classification | Exact implication |
|---|---|---|
| none | N/superseded | Meaningful snapshot, hook, daemon-availability, and tracking outcomes are covered by independent B surfaces. The automatic multi-mutation chain, implicit daemon lifecycle, dirty/detached snapshot-init behavior, upstream relative-`core.hooksPath` mutation, and management of arbitrary external hook locations are not reproduced. |
| `--no-daemon` | N/superseded | Independent B readiness, snapshot-ignore, hook, and tracking surfaces cover the safe outcomes; the broad register-against-an-existing-daemon chain, dirty/detached snapshot-init behavior, upstream relative-`core.hooksPath` mutation, management of arbitrary external hook locations, and aggregate incomplete/Ready result are not exposed. |
| `--agent <agent>` | P (agent only) | Global `default_agent` seeding and selected-repository `.roborev.toml` create-if-absent behavior have no Dashboard v1 management surface. |
| `--binary <path>` | N/superseded / N/unsupported | The Dashboard Git Hook surface binds the current installed v0.64.0 executable server-side. A browser-selected arbitrary binary is unsupported. |
| `--agent <agent> --no-daemon` | P (agent only) | Same agent gap as above; no-daemon lifecycle behavior remains superseded by explicit readiness/tracking and no implicit registration chain. |
| `--agent <agent> --binary <path>` | P (agent only) | Same agent gap; current Dashboard-bound binary is the only supported hook binary, and arbitrary browser binary selection remains unsupported. |
| `--no-daemon --binary <path>` | N/superseded / N/unsupported | Safe independently managed outcomes remain available; arbitrary binary selection and the broad no-daemon init chain are not. |
| `--agent <agent> --no-daemon --binary <path>` | P (agent only) | Agent selection remains the only meaningful P gap; binary and no-daemon behavior retain the classifications above. |

## Mapping to Dashboard surfaces

| Init component | Existing Dashboard surface | Coverage and boundary |
|---|---|---|
| Global data directory and full default config materialization | Configuration v1 effective config read/write | N/unsupported-by-design for physical bulk bootstrap. Missing global config already yields effective defaults through `LoadGlobal`; an allowlisted `config set` may materialize a file only as its own documented consequence. Dashboard never writes the full init template or infers successful bootstrap from a readiness row. |
| `--agent` global default and selected-repo config creation | None | P. Existing global/local files remain untouched; no root-init fallback or broad config editor exists. See the next checkpoint below. |
| Snapshot directory ignore | Snapshot Ignore Management v2 plus Setup Readiness | B as an explicit, previewed, confirmed, create/append-only `.gitignore` action with fresh safety checks, no arbitrary path/pattern input, bounded readback, and no rollback. Dirty or detached state is a separate init behavior and is not silently accepted by this CTA. |
| `core.hooksPath` normalization, hooks directory, and two managed hooks | Git Hook Status/Management v2 | B only for a canonical safe Git/common hooks location and the exact Dashboard-bound v0.64.0 binary. Upstream mutation of a relative `core.hooksPath`, management of an arbitrary external hook location, and arbitrary `--binary` are N/unsupported-by-design; status can observe unsupported locations without mutating them. |
| Daemon availability | Runtime lifecycle and Setup Readiness | B as explicit Start/Stop/Restart and already-running health observation. Root init’s implicit ensure/start/restart/zombie cleanup is not a Dashboard action and has production occurrence 0. |
| Repository registration | Repository Tracking Management v2 | B as explicit preview/confirm registration against an already-running healthy exact-version daemon, with canonical server-side root, no redirect, bounded request, one attempt, and detached inventory readback. It does not start or repair the daemon. |
| Aggregate init result and warnings | Setup Readiness observer | B only as independent component statuses. Configuration, snapshot, hooks, daemon, and tracking failures do not collapse into one claim that root init ran or can be rerun safely. |

The only tracking CTA gate is fresh `tracking=not-tracked` with daemon online/healthy. Dirty state and the configuration, snapshot, and hook rows are independent; they are not hidden prerequisites for registration. Conversely, snapshot-ignore and hook actions have their own eligibility and safety gates. This preserves the distinct upstream side effects instead of inventing an unsafe aggregate “Initialize” action.

## Linked-worktree and Git layout matrix

Init always starts from the active checkout `Root`. Registration is then canonicalized by the daemon’s `MainRoot` rules, and hooks resolve through the effective Git/common hooks directory.

| Layout | Config/snapshot init scope | HooksPath result (Git `rev-parse --git-path hooks`; relative result joined to `MainRoot`) | Registration scope |
|---|---|---|---|
| Ordinary checkout (`gitDir == commonDir`) | Active checkout root | Git-resolved hooks path; a relative result is joined to `MainRoot` (the checkout) | Active checkout root |
| Ordinary linked worktree (`.git/worktrees/...`, common dir is main `.git`) | Linked checkout root | Git-resolved hooks path; a relative result is joined to `MainRoot` (normally the main shared hooks path) | Main checkout root via `MainRoot` |
| Bare-backed linked worktree (common config `core.bare=true`) | Linked checkout root | Git-resolved hooks path; a relative result is joined to `MainRoot` under the Git layout’s own resolution | Linked checkout root via `MainRoot` |
| Separate external Git dir, normally `gitDir == commonDir` | Active checkout root | Git-resolved hooks path; a relative result is joined to `MainRoot` | Active checkout root |
| Non-standard common dir/submodule-like layout with configured `core.worktree` | Active checkout root | Git-resolved hooks path; a relative result is joined to `MainRoot` under the Git layout’s own resolution | Configured `core.worktree` target via `MainRoot` |
| Bare repository without a checkout | Root init fails at its initial `Root` lookup before side effects | Not reached by root init | Not reached by root init; standalone registration also fails when `MainRoot` falls through to `Root` without a checkout |

The linked-worktree distinction matters: a literal local `.roborev.toml` created by `--agent` can shadow an untracked inherited main-worktree config, while snapshot and other selected-root writes remain in the active checkout. Dashboard registry authorization and repository leases continue to identify the selected repository; no browser path can select a different row.

## Failure and partial-mutation semantics

The upstream command is sequential and non-transactional. Data/config creation can precede a snapshot or hook failure, and `.gitignore`, `core.hooksPath`, or hook files can be changed before a later daemon warning; all prior changes remain. The daemon stage is special: ensure/start/restart failure prints a warning and manual-start guidance, skips registration, marks setup incomplete, and still returns nil. Under `--no-daemon`, registration transport or non-200 failure prints a warning and setup-incomplete guidance. Registration storage may issue an insert or identity update and then fail its re-read or HTTP response, so the client can see failure after persistence; even an exact HTTP 200 has no authoritative inventory readback in the CLI path.

Dashboard actions expose narrower, honest outcomes. Snapshot Ignore Management uses a single-use preview token, explicit confirmation, bounded file mutation and exact post-write Git/readback classification. Repository Tracking uses a single-use token and, once `HTTP.Do` is attempted, detached bounded inventory verification that survives browser disconnect; ambiguous external outcomes remain `failed-unverified` unless the audited status and exact readback justify success. No action offers automatic retry, rollback, unregister, arbitrary shell, or a browser-provided path/binary. These are evidence-based boundaries from the existing adapters, not a claim that the upstream init command is transactional.

## Why Dashboard never runs root init

The CLI command is appropriate for an operator who intentionally invokes it from a working directory, but its broad, sequential authority is not a safe browser mutation contract. Dashboard keeps each authority independently selected, previewed, confirmed, and verified.

| Boundary | Upstream root init behavior | Dashboard safety decision |
|---|---|---|
| Repository authority | Uses process cwd and begins with `Root(ctx, ".")`; registration later changes scope through `MainRoot`. | Accepts only an opaque registered identity, acquires the selected repository lease, and revalidates the canonical root/effective registration root server-side. No browser path or cwd chooses a repository. |
| Snapshot target and races | Validates a repository-local path and no symlinks before a later `.gitignore` append, but does not bind every path component/target identity through a browser transaction. | Snapshot Ignore binds the selected root and target with platform-specific identity/no-reparse checks, fixed `.gitignore` authority, bounded create/append, final guards, and exact readback. |
| Hook ambiguity | May rewrite relative `core.hooksPath`, create the resolved directory, warn and continue for foreign non-shell hooks, and manage hooks outside Dashboard's canonical safe location. | Hook Management supports only the canonical Git/common hooks location and the exact server-bound v0.64.0 binary; ambiguous, non-shell, relative-normalization, and arbitrary external-location cases are observed or rejected rather than silently folded into init. |
| User intent | One command can cross config, snapshot, Git config/hooks, daemon lifecycle, and registration boundaries before one final Ready/incomplete message. | Each B surface has its own named preview/confirmation or explicit lifecycle action. There is no aggregate Initialize endpoint, CTA, or automatic continuation. |
| Daemon lifecycle | Default init may discover, restart, clean up zombies, or start a daemon before registration; `--no-daemon` still attempts registration. | Runtime Start/Stop/Restart is a separate explicit capability. Repository Tracking requires an already-running, healthy, exact-version daemon and never starts or repairs it. |
| Path and executable input | CLI callers may supply `--binary`, and config may direct hooks to relative or external locations. | Browser requests cannot supply repository paths, hook locations, executable paths, argv, or shell text; the hook action binds the installed exact-version executable server-side. |
| Partial mutation truth | Earlier changes are retained on later hard errors or incomplete warnings, and registration can persist before a failed response. | Each action reports only its own verified result: Snapshot Ignore uses bounded post-write evidence, Tracking uses `Attempted` plus detached inventory readback, and neither claims rollback or aggregate readiness. |

## Remaining gap and next checkpoint

The meaningful remaining init gap is agent selection. Dashboard Configuration v1 intentionally allowlists eight safe keys and does not include `agent` or `default_agent`; it therefore cannot reproduce `--agent`’s two create-if-absent outcomes. Init’s validation is gate-dependent: if the global file is missing, an invalid value can fail the global write after the data directory step; if only the local file is missing, it can fail at the local write; if both files exist, the supplied value is ignored and is not validated at those gates. Existing malformed global config is not parsed by the create gate, while an existing malformed local config can fail later snapshot resolution. It also does not promise physical data-directory or full-template creation, because missing global configuration already has effective defaults and bulk bootstrap is unsupported by design.

The next checkpoint is **Repository Agent Configuration Management v1**. It should separately define global `default_agent` and selected-repository `agent` scopes, the exact v0.64.0 agent catalog and validation, preview/confirm/readback and readiness gates, create-only authority for existing files, and linked-worktree selected-root behavior. It must not reintroduce root `roborev init` or an aggregate initialization CTA. Until that checkpoint is independently authorized, every variant containing `--agent` remains P for that agent outcome only.

## Evidence and validation

The audit uses the upstream `init_cmd.go`, config, snapshot, hook, daemon-lifecycle, registration, storage, and linked-worktree tests at the exact source commit above. Existing Dashboard proof includes `internal/configuration`, `internal/roborev`, `internal/setupreadiness`, `internal/snapshotignoremanagement`, `internal/repositorytracking`, `internal/hookstatus`, `internal/hookmanagement`, `internal/gitx`, and `internal/app` tests. The production `roborev init` occurrence is a manual source audit and remains 0; no brittle source-scanning test is added. No actual `roborev init`, daemon lifecycle, registration, review/LLM job, or external RoboRev review is run for this audit.
