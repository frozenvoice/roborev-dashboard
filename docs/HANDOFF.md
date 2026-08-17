# Handoff

마지막 갱신: 2026-08-17

## 현재 상태

- 새 Git 저장소를 `main` branch로 초기화했다.
- 설치된 RoboRev v0.64.0의 command surface와 upstream v0.64.0 source를 조사했다.
- `AGENTS.md`, review 지침, architecture와 capability matrix를 작성했다.
- Windows에 Go 1.26.5를 설치했다. 현재 shell PATH 갱신 전에는 `C:\Program Files\Go\bin\go.exe`를 사용할 수 있다.
- Checkpoint A/B vertical slice와 후속 hardening을 `fb88aa9`, `cff6d45`, `d3ed6b2`, `666086c`, `ea1174b`로 commit했다.
- RoboRev panel jobs 13, 17, 21, 25, 29, 33, 37, 41의 finding을 재현·분류했고, Jobs 58, 59, 60은 Codex/Claude/Grok 모두 PASS했다. 실제 수정과 근거는 `docs/REVIEW-FINDINGS.md`에 있다.
- `39ae550`은 Jobs 58/59/60 quality gate를 통과한 기준 commit이다.
- `1f9d018`은 repository별 effective RoboRev panel discovery/selection/persistence와 deterministic Copy/Open Fix Prompt checkpoint다. 후속 작업 트리는 Jobs 62/63/64의 bounded regular-file TOML loading, actual linked-worktree 판별과 repository switch panel single-fetch를 반영한다.
- Jobs 62/63/64 후속 checkpoint에서 `scripts/test.ps1`의 전체 Go suite, React/Vitest 46 tests와 Vite production build가 통과했고, `scripts/build.ps1`로 Windows EXE를 생성했다. 이전 feature smoke에서는 실제 v0.64 daemon의 repository-aware `dashboard_cross` 선택과 기존 Finding의 stale Fix Prompt 편집 모달을 확인했으며 review/rerun mutation은 실행하지 않았다.
- `666086c` clean archive에서 `go build ./...`가 통과해 embed placeholder를 검증했다.
- `666086c`의 새 panel job 25를 완료했다. active-job prompt stripping, verified `id=` lookup, suspended Job Object attach, selected polling reconciliation, version TTL, poll gating, panel handler coverage와 deterministic registry fallback을 후속 작업 트리에 반영했다.
- 후속 React/Vitest 9 tests, Vite production build와 roborev/app/process backend suites가 통과했다. Windows Application Control의 일시 차단 해제 후 최신 9.9MB EXE를 다시 실행했고 root/runtime/reviews smoke와 browser job 29 목록을 확인했다. 실제 list response는 7.3KB이며 `prompt`, `diff_content`, `output`, `review`가 없다.
- Panel Refine checkpoint는 `POST/GET /api/panel-refines`와 `POST /api/panel-refines/{id}/cancel`을 사용하는 backend process-scoped authoritative state machine으로 fresh pinned panel review → synthesis 평가 → Codex fix → 새 clean HEAD 재검토를 반복한다. 시작 dialog는 실제 request body와 typed fix argv template preview를 표시한다. `dashboard_cross`를 fast iteration panel로 사용하고, 사용자가 명시한 경우에만 distinct `dashboard_deep` final gate를 실행한다. browser reload/reopen은 active run에 재접속할 수 있지만 Dashboard process restart resume은 지원하지 않으며 shutdown/cancel은 child process와 context를 정리한다. cancel 후 fresh inspection 결과는 `cancelState`로 보존한다.
- Panel Refine은 repository identity lock으로 같은 repository의 동시 refine과 Review HEAD/Rerun 경쟁 mutation을 거부한다. 시작·iteration·fix 후 fresh inspection으로 clean tree, branch, canonical identity, HEAD 전진을 확인하고 stale/dirty/unknown 상태는 자동 fix하지 않는다. v0.64.0 `fix --agent codex --quiet <synthesis-id>`가 새 commit에 panel 없는 default review를 자동 enqueue할 수 있어 Dashboard의 선택 panel review와 unrelated extra review가 생길 수 있으며, 이를 이름이나 응답 추측으로 억제하지 않는다.
- enqueue receipt와 `/api/jobs` polling은 synthesis job ID, panel run UUID, repo path, full git ref, branch, panel, synthesis role와 panel job type을 모두 일치시킨다. running 상태만 bounded 4초 polling하고 malformed/unknown/P-F 없는 결과는 fail closed한다.

## 결정

- production은 React assets를 Go binary에 embed한 단일 exe다.
- detail/prompt/log는 stable CLI를 사용한다. prompt/diff 없는 목록과 job ownership lookup, review enqueue, frozen rerun, panel members, health는 v0.64.0 daemon HTTP API를 version-gated로 사용한다.
- repository registry는 canonical Git root만 허용하고 runtime fresh inspection과 startup persisted-path inspection 결과가 expected identity와 일치하는지 같은 platform-aware 규칙으로 재검증한다. Frontend는 backend 제공 opaque identity만 비교한다.
- panel discovery는 v0.64.0에 enumeration API가 없어 typed TOML fallback으로 global/local map merge를 재현한다. registered root direct `.roborev.toml`만 지원하며, config는 플랫폼별 no-follow handle로 bounded read한다. direct config 없는 `.git` regular-file layout은 metadata를 확인하고, metadata 실패 시 global named panel discovery/mutation을 fail closed하며 actual linked worktree는 main-config 상속을 추측하지 않고 built-in `none`만 허용한다.
- panel preference는 Dashboard config에 repository identity별로 저장하되 current catalog를 매번 다시 authorization한다. malformed config는 fail closed한다.
- Panel Refine은 기존 Fix Prompt를 대체하지 않는 별도 자동 workflow다. backend state machine만 run을 진행하고, v0.64.0 fix adapter는 확인된 `roborev fix --agent codex --quiet <synthesis-id>` argv 계약을 사용한다. 전체 run과 각 enqueue/poll/fix에는 bounded timeout이 있다.
- Fix Prompt는 terminal F single/synthesis/individual member의 bounded `show --json` subset으로 로컬에서 결정적으로 생성하며 LLM이나 mutation을 호출하지 않는다.
- review list는 각 요청 완료 4초 뒤, 작은 `/api/runtime` health 상태는 각 요청 완료 10초 뒤 다음 poll을 예약한다. `/api/runtime`은 repository inspect/list를 수행하지 않는다. `status --json`과 `list --json`은 full prompt/diff를 포함할 수 있어 polling에 사용하지 않는다.
- `roborev init`은 post-commit hook과 daemon start를 함께 수행하므로 자동 호출하지 않는다. `.roborev.toml`을 직접 관리하고 manual panel review를 사용한다.

## 다음 작업

1. Panel Refine checkpoint의 backend/frontend 회귀 테스트와 전체 test/build를 완료하고 새 HEAD에 `dashboard_cross` review 실행
2. Panel Refine 통과 뒤 필요하면 사용자가 별도로 `dashboard_deep` final review 실행
3. Checkpoint C: installer/update/daemon lifecycle/check-agents/hooks/config
4. Checkpoint D 이후: analyze/run 등 agentic workflow와 stream relay; RoboRev upstream이 panel-aware fix suppression capability를 제공할 때만 v0.64 extra default review 재검토
