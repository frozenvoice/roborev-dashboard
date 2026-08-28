# Final v0.64 Security & Regression Closure

마지막 갱신: 2026-08-29

## Scope / base / upstream

- Dashboard base: `70cb28911ff91eae4a8c4af5e18464b83bc9532b` (`Triage remaining v0.64 coverage`)
- authoritative upstream: kenn-io/roborev v0.64.0, commit `bc0af33c282733a9c39d72c08373b040e22c11d4`
- 우선순위: current source/tests → exact upstream source → `AGENTS.md` → 대화 설계 맥락
- 이 checkpoint는 기존 feature-complete candidate의 security/regression closure다. 새 major feature, compact/export/Fix/Refine/Task/Analyze execution 또는 release packaging을 추가하지 않는다.

자동 검증은 deterministic fake HTTP/process/filesystem fixture만 사용했다. 실제 RoboRev review/rerun/fix/refine/compact/task/analyze/Insights, daemon lifecycle, LLM, export 또는 사용자 repository mutation은 실행하지 않았다.

## Final verdict

- Final verdict: **PASS**
- Remaining MUST-SHIP: **0**
- Feature verdict: **v0.64 FEATURE-COMPLETE**
- Release verdict: **NOT YET RELEASE-CLOSED**
- Final fresh-context internal reviewer: **exactly 1, final PASS**
- Next checkpoint: **Windows Release Readiness & Non-Destructive E2E**

정확히 한 명의 fresh-context internal reviewer가 최초 **NOT-CLOSED**로 보고한 세 material finding을 교정했고, 같은 reviewer가 회귀 diff를 다시 읽어 모두 CLOSED, 새 material finding 0, 42개 질문의 remaining FAIL 0과 최종 **PASS**를 확인했다.

최종 corrective review에서는 generic `/api/reviews/{id}/rerun`을 trimmed empty legacy와 `review`/`range`/`dirty`/`synthesis`로만 allowlist하고 Fix/Compact/Insights/Analyze/Classify/unknown 및 task/run을 fail closed했다. Panel member list/detail은 strict browser read authorization을 query·registry·daemon 조회보다 먼저 수행한다. Git/daemon/process/config/setup/panel/analysis/agent 실패는 typed policy를 제외하고 app-layer fixed message로 축약하며, 내부 path·command·stderr는 browser에 직렬화하지 않는다. Compact와 CI Metrics는 여전히 별도 blocked/deferred surface이며 이 correction은 이를 활성화하지 않는다.

## Production route inventory

Static fallback을 포함한 semantic inventory는 94개다.

| 분류 | 수량 | 계약 |
| --- | ---: | --- |
| READ/PREVIEW | 49 (GET 33 + POST 16) | strict browser read 또는 mutation-free preview |
| MUTATION | 32 | same-origin/CSRF와 typed authority 뒤의 bounded mutation |
| BLOCKED TOMBSTONE | 11 (GET 3 + POST 8) | authorized fail-closed, service/process/lifecycle zero-call |
| STREAM | 1 | capability false, `OpenStream` unsupported, runner zero-call |
| STATIC/UI | 1 | embedded frontend fallback; unknown `/api`는 JSON 404 |

Mutation routes는 strict method/query/body, origin/CSRF, registered resource resolution, 필요한 Gate/lease와 preview-token 경계를 통과한다. Side effect가 시작된 surface는 자동 retry하지 않고, 계약에 필요한 곳은 detached authoritative readback으로 `verified`, `partial`, `unverified` 또는 `failed-unverified`를 구분한다.

## Browser authority boundaries

Browser가 executable, daemon endpoint, cwd, environment, agent executable, arbitrary config key/file, hook/skills/workflow target, raw Git ref, model/provider/reasoning 또는 mutation destination absolute path를 선택하는 production surface는 없다. Execution previews use only fixed semantic `roborev` shapes; actual binary/path/environment bindings remain server-side.

기존 architecture의 path-bearing 예외는 그대로 분리한다. Bootstrap/repository inventory와 setup install state는 사용자에게 로컬 path를 표시할 수 있고, Add/Select 및 legacy repository management requests는 등록 대상이나 fresh daemon inventory row를 찾는 selector path를 받을 수 있다. Rename/Move/Merge의 stale source path는 fresh exact inventory에서 unique하게 재해석되며 filesystem destination authority가 아니다. Destination, canonical cwd/root와 actual CLI argv는 Registry/Git/daemon evidence에서 server가 다시 계산한다.

## Repository isolation

- Registered operations resolve identity through `Registry`, then perform fresh canonical Git inspection where required.
- Job/review reads validate positive ID, repository ownership, panel run/role/type and expected job identity before projecting content.
- Linked-worktree `Root`/`MainRoot` semantics remain feature-specific: selected checkout filesystem mutations use the authorized checkout root; tracking readback uses the effective upstream registration root.
- Foreign repository adoption, duplicate/equivalent roots, unknown identity and stale selected-repository switches fail closed.
- Frontend repository/request generations discard A→B late responses. A→B→A applies only evidence that still belongs to the current A generation.

## Mutation confirmation / readback

Configuration, Agent Configuration, Snapshot Ignore, Tracking, Git Hooks, Agent Hooks, Skills, CI Workflow and repository rename/move/merge retain explicit Preview → Confirmation → Execute boundaries. Token stores use cryptographic random opaque values, finite TTLs, bounded capacity and single-use claim semantics. Private root/file/binary/digest witnesses are token-side server state, never encoded into the browser token.

General Configuration now claims the token before the global/repository gates, never restores it after a busy/stale attempt, revalidates exact executable/file/root/config witnesses, performs a final root guard, records process `Started`, and uses detached scoped readback. A never-started process cannot receive success credit from an external writer.

## Operation gates and leases

The shared global Gate serializes setup/runtime lifecycle, review/enqueue-sensitive work and agentic Insights enqueue where those resources conflict. Repository keys are platform-aware and are acquired atomically for two-resource Move/Merge operations; selected leases prevent repository switching during accepted work. Preview-store mutexes are not held while acquiring the shared Gate, and opposite-order multi-key tests remain nonblocking/fail-fast rather than deadlocking.

Some older repository-management contracts intentionally preserve tokens when the shared Gate is busy; Configuration, Snapshot Ignore, Tracking, Agent Configuration, Hooks, Skills and CI creation consume them according to their documented claim timing. In every case, an already claimed token is not replayable and mutation launch remains at most once.

## Process and daemon lifecycle

Review, Rerun, Show, Comment, Close, Reopen, Cancel, JobMetadata, Summary, Cost and Insights use exact v0.64 direct daemon HTTP instead of their lifecycle-bearing CLI commands. The adapter uses a pinned/current binding, fresh health/version match, no redirects, `Accept-Encoding: identity`, exact expected status, bounded `limit+1` reads, duplicate/trailing/depth-safe JSON validation where authority or receipts require it, sanitized errors and no mutation retry.

Remaining process surfaces use typed executable + argument arrays, bounded stdout/stderr, timeouts/cancellation and no dynamic shell. Stream capability is false and production starts neither `roborev stream` nor an SSE relay source. Review/task refresh uses authoritative 4-second polling.

## Filesystem / Git mutation boundaries

- Snapshot Ignore and CI Workflow writes keep canonical containment, reparse/symlink/ancestor identity guards, create-new/no-replace rules and exact-byte readback.
- Configuration/hook/skills operations retain final safety guards and Started-aware detached verification.
- No production code performs Git reset/checkout/restore/stash/rebase, `git add`, `git commit` or `git push` on a user repository.
- Repository rename/move/merge mutate RoboRev metadata through typed exact-v0.64 contracts only; they do not move filesystem/Git roots or mutate Dashboard Registry selection.
- No automatic rollback guesses at partial external mutation.

## Privacy boundaries

Review list/detail/panel member and enqueue receipt responses are app-layer allowlist projections. Internal authorization may use `repo_path`, but that path is stripped before browser serialization. Projections remove prompt/diff/patch, raw error, dirty/worktree/config paths, raw command/process fields, token usage, session/worker metadata and unexpected nested fields while preserving UI-required IDs/status/verdict/panel metadata and intentional review Markdown/output.

Installation/runtime, Agents, Panel Refine/Fix history, repository command previews and Stats use dedicated browser DTOs. Actual binary paths, raw process/daemon errors, summary `repo_path`, per-repository Stats paths, HOME/config paths, stdout/stderr and private markers do not cross those DTOs. Intentional path-bearing bootstrap/inventory/install-display contracts remain explicitly out of scope of the path-free mutation projection claim.

## Blocked-surface zero-call proof

- General Fix execute: `501 fix-hidden-lifecycle-blocked`; Fix/service/process/lifecycle zero.
- Panel Refine start: `501 panel-refine-fix-lifecycle-blocked`; ReviewPanel/Fix/LLM/lifecycle zero; historical read/cancel compatibility remains.
- General Refine preview/start: `501 general-refine-fix-lifecycle-blocked`; review/Fix/refine/lifecycle zero.
- Tasks preview/execute: `501 task-execution-disabled`; generic rerun keeps the task/run rejection and exact review-like allowlist.
- Analyze preview/execute: `501 analysis-execution-disabled`; only list/show-prompt introspection remains.
- Review Export: repository-isolation tombstone; upstream export and browser download zero.
- CI Metrics and compact: no production route/client/process occurrence.
- Root aggregate init and repository delete/cascade: no production invocation/route.

## Frontend stale-state / network boundaries

Review detail/member, Stats, Configuration, repository management and setup resources use generation/AbortController ownership. Repository switches clear old dialog/token/result state and reject stale finally blocks. Blocked CTAs are disabled or history/read-only, locale changes do not create requests, cancel does not execute, and guarded double-confirm emits one request. Strict normalizers reject private fields and job/review identity drift before state adoption.

## Forbidden production occurrence report

Static production-source audit found:

- root `roborev init`: 0 (only the separately authorized `init gh-action` builder exists)
- reachable direct `fix` / direct `refine`: 0 (historical managers/builders remain behind start tombstones)
- `compact`: 0
- `export reviews` and `export ci-metrics`: 0
- `/api/job/fix` and `/api/export/ci-metrics`: 0
- repository delete/cascade: 0
- user-repository `git push`: 0
- dynamic shell interpolation: 0
- browser-selected executable/cwd/env/daemon endpoint mutation: 0

The sole production `--yes` is the server-owned, preview-bound `repo merge --yes` contract. The sole `"init"` argv is fixed `init gh-action` into a server-owned private temporary output.

## Regression suite

- Targeted Go: `internal/app`, `internal/roborev`, operation/process/git/setup/configuration/agent-configuration, repository, hook, skills, CI, Stats and Insights packages PASS.
- Focused frontend integration: 7 files / 288 tests PASS.
- Concurrency repeats: `internal/operation` ×20, selected app token/Gate/lease tests ×10, stream manager shutdown/idle/concurrent tests ×10 PASS.
- `scripts/test.ps1`: all Go packages PASS; Vitest 31 files / 594 tests PASS; TypeScript/Vite production build PASS.
- `scripts/build.ps1`: PASS; Windows executable built.
- `go vet ./...`: PASS.
- `git diff --check`: PASS (Windows line-ending conversion warnings only).
- `dist/roborev-dashboard.exe --help`: PASS; default bind remains `127.0.0.1`.
- Go race was not forced on the full Windows suite because it is optional and materially more expensive; focused concurrency repeats provide deterministic coverage for the audited lock/token paths.

## Findings and corrections

Material findings corrected in this checkpoint include lifecycle-bearing CLI use on otherwise direct daemon-capable surfaces; redirect/error/body ambiguity in local daemon transports; stream/SSE capability drift; browser projection of binary/path/raw error/private upstream fields; missing global serialization for Insights; configuration token restoration/binding/readback weaknesses; unbounded repository preview stores; and frontend detail identity/projection regressions. The final reviewer additionally found and closed three integrated bypasses: generic rerun accepted stored Fix/Compact jobs, panel member reads lacked strict browser authorization, and several app handlers serialized raw Git/daemon/process errors. Exact review-like rerun allowlisting, authorization-before-service, fixed app-layer error projection and private-marker/zero-call regressions now cover those boundaries.

Unresolved material findings: **0**. The same internal reviewer returned final **PASS** after the corrections.

## Residual risks

- Same-user external writers can race after an unavoidable final filesystem/daemon guard.
- Dashboard/process crash after a started external mutation may leave only an unverified outcome.
- Exact Windows clean-install/upgrade, real-world non-destructive smoke, crash recovery and migration evidence remain release work.
- Remote CI/status checks are absent. Branch protection/ruleset metadata is unavailable under the current private-plan API response (HTTP 403).
- v0.65+ is unsupported until a separate capability/contract checkpoint.

## Feature closure / release-readiness handoff

The source/test/reviewer evidence supports `MUST-SHIP 0` and **v0.64 FEATURE-COMPLETE**. It does not claim full CLI parity or release readiness. Optional, superseded, unsupported and upstream-blocked taxonomy remains unchanged from `V064-COMPLETION-AUDIT.md`.

The exact next checkpoint is **Windows Release Readiness & Non-Destructive E2E**. It is not implemented in this commit.

## Remote repository / Git

- Remote CI evidence: **NONE** (no GitHub Actions workflow/check/status evidence).
- Branch protection/ruleset: API unavailable under the current private-plan boundary; no remote setting was changed.
- Commit/fetch/push and final clean `HEAD == origin/main` verification are completion gates performed after the internal reviewer’s final PASS and recorded in the completion report.

## External / 3-review

External RoboRev `dashboard_cross`, `dashboard_deep`, 3-review, real review job, daemon lifecycle and LLM execution: **NOT RUN**.
