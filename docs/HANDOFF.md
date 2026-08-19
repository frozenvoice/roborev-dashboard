# Handoff

마지막 갱신: 2026-08-19

## 현재 상태

- 새 Git 저장소를 `main` branch로 초기화했다.
- 설치된 RoboRev v0.64.0의 command surface와 upstream v0.64.0 source를 조사했다.
- `AGENTS.md`, review 지침, architecture와 capability matrix를 작성했다.
- 소스 개발 bootstrap을 `scripts/prerequisites.ps1`, `scripts/install.ps1`로 분리했다. 전자는 정의·검사만 수행하고, 후자는 allowlist된 Git/Node.js LTS/Go만 winget으로 설치한 뒤 현재 프로세스 PATH를 갱신하고 `npm ci`를 실행한다. `scripts/build.ps1`, `scripts/test.ps1`, `scripts/dev.ps1`는 공통 prerequisite assertion이 반환한 절대 npm/go 경로만 사용하며 RoboRev를 검사하거나 실행하지 않는다.
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
- enqueue receipt와 v0.64 `/api/job/output?job_id=...` status-prefix polling은 synthesis job ID를 일치시키고 active response의 `lines`를 읽지 않는다. `queued`/`running` 동안 metadata를 요청하지 않으며 첫 terminal `done`에만 `omit_prompt=true` metadata를 최대 256 KiB로 한 번 bounded fetch해 repo path, full git ref, branch, panel, panel run UUID, synthesis role/type, skipped, status와 P/F verdict를 모두 검증한다. 8 KiB prefix, content type/status, ordered keys와 contract/version gate를 검사하고 malformed/unknown/canceled/failed/skipped/identity mismatch는 fail closed한다. transport/5xx만 제한된 retry 대상이다.
- clean-machine developer bootstrap은 Windows PowerShell 5.1에서 실제 prerequisite 검사, dependency install plan, test와 production build를 통과했다. `scripts/install.ps1`은 계속 Git/Node.js/Go와 npm dependency만 다루며 RoboRev runtime installer가 아니다.
- RoboRev가 없는 Windows에서 Dashboard first launch와 Setup-required 상태까지 실행되는 것을 확인했다. 새 Setup & Install checkpoint는 Dashboard가 지원하는 RoboRev v0.64.0 Windows amd64 release의 exact asset size와 SHA-256을 immutable manifest에 pin하고, 명시적 UI 확인 뒤에만 bounded HTTPS download, safe ZIP extraction, user-level no-replace publish, strict version probe, current-process rebind와 bounded daemon health를 수행한다.
- runtime installer는 existing supported binary를 존중하며 explicit override와 newer/unknown/broken/blocked binary를 자동 downgrade 또는 overwrite하지 않는다. PATH, `roborev init`, Git hook, agent/credential과 Windows security policy를 변경하지 않는다. exact Windows policy error는 typed `blocked` 상태로 표시하되 우회하지 않는다.
- 설치 상태는 process-scoped manager가 소유하고 global operation gate가 setup과 Review/Rerun/comment/close/cancel/Panel Refine mutation의 동시 실행을 fail-fast 409로 막는다. 설치 뒤 daemon 시작이 실패해도 verified binary를 보존하고 재다운로드 없는 Start daemon retry를 제공한다.
- Setup acceptance lifecycle hardening은 request 종료 뒤 install/retry가 process context에서 계속되고 Dashboard shutdown/ceiling에서 취소·wait·gate release되는 regression suite를 통과했다. Jobs 91/92/93과 후속 GPT review는 모두 PASS했다.
- Agent Status + Runtime Control checkpoint는 exact v0.64.0 command descriptor와 safe global/local config merge로 zero-quota detection을 제공하고, 격리된 bounded Cursor identity probe로 Grok collision을 fail closed하며, 명시적으로 확인한 canonical agent 하나에만 quota-consuming `check-agents` smoke check를 허용한다. Queue는 official daemon status/pause/unpause API를 사용한다. Stop/Restart는 backend exact argv preview, transport-aware health verification, process-scoped lifecycle과 global runtime-control gate로 Setup/Panel Refine/mutation과 충돌을 막는다. 다른 RoboRev 버전은 agent/runtime contract를 실행하지 않는다.
- Agent health check는 selected-repository 전용 read lease로 repository selection commit과 quota dispatch를 선형화하고, service의 single discovery에서 configured/detected eligibility를 재검증한다. Frontend의 repository epoch는 실제 identity 변경만 추적하고 GET generation은 same-repository latest-wins만 담당하므로 concurrent refresh와 failed repository switch 뒤에도 올바른 scope의 Agent cards와 completion 상태를 복원한다.
- 자동 backend/frontend test와 production build에서는 fake HTTP/filesystem/process만 사용했으며 실제 RoboRev, GitHub release download, daemon 또는 LLM을 실행하지 않았다. 완성된 **Install & Start**의 실제 clean-machine E2E는 이 노트북의 missing-RoboRev 상태를 보존한 채 사용자 수동 검증을 기다린다.
- Frontend ko/en localization checkpoint를 구현했다. `web/src/i18n.tsx`가 typed English/Korean catalog, safe `roborev-dashboard-language` storage/browser fallback, stable `t`/`format` callbacks와 `<html lang>`을 제공하며 App/Setup/Panel Refine chrome, empty/loading/error/action 안내와 sidebar/modal language selector를 번역한다. locale 전환은 repo/panel/review/detail/tab/modal/setup/progress/refine/agent/runtime/form 상태를 보존하고 fetch/mutation을 시작하지 않는다. RoboRev, Git/SHA/API/CLI/CSRF/URL/JSON, names/branches/IDs/paths/versions, argv/preview와 backend raw error는 technical source-of-truth로 유지한다. 단위·통합 frontend tests와 Vite build는 통과했으며 RoboRev는 아직 설치하지 않았다.
- RoboRev v0.64.0 `stream --repo` live relay checkpoint를 구현했다. exact version/capability와 daemon already-online/healthy preflight 뒤에만 typed argv로 시작하며, repository identity별 source 하나를 0..N SSE subscribers가 공유한다. bounded JSONL/stderr, malformed·oversized line, process exit/restart, subscriber disconnect, identity isolation, sanitized payload와 shutdown cleanup을 구현했다. SSE는 `jobs-changed` invalidation만 전달하고, frontend는 burst coalescing/reconnect 뒤 `/api/reviews` authoritative fetch를 수행한다. 기존 4초 polling은 fallback/safety net으로 유지된다. deterministic fake 기반 backend/frontend tests에서 실제 RoboRev/daemon/LLM을 호출하지 않았다.

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
- 릴리스 exe는 embed된 frontend assets만 사용하므로 Go/Node.js/npm 없이 실행할 수 있다. RoboRev가 없는 Windows amd64 runtime은 Agents & Setup의 explicit confirmation으로 pinned v0.64.0을 설치하고 현재 process에 즉시 rebind할 수 있다. browser는 URL/version/hash/path를 선택하지 못하며 자동 update는 지원하지 않는다.

## 다음 작업

1. clean-machine Windows에서 최신 Dashboard exe를 실행하고 Setup confirmation의 repository/version/target/size/full SHA와 no-init/no-agent/no-security-change 범위를 검토한 뒤 사용자가 직접 **Install & Start** 실행
2. 설치, runtime rebind, daemon online, repository panel 복원, Review HEAD와 Panel Refine 활성화를 같은 session에서 확인
3. 같은 clean-machine session에서 sidebar와 열린 install/daemon/agent-check/Fix Prompt/Panel Refine modal의 한국어↔English 전환, technical preview 보존, network/mutation 부재와 mobile wrapping을 수동 확인
4. 별도 checkpoint로 v0.65+ compatibility adapter를 검증한 뒤에만 update policy 검토; 자동 downgrade 금지 유지
5. Agent Status + Runtime Control 실제 E2E는 사용자 검증 대기: agent detection 확인, 선택한 agent 하나만 Run check, Pause/Resume, Restart, 마지막 Stop/Start 순서
6. clean-machine Windows에서 RoboRev v0.64.0 daemon online 상태로 stream relay를 수동 smoke test: Review enqueue/complete event, browser reconnect/sleep/network flap, repository switch isolation, SSE unavailable 시 4초 polling fallback과 Dashboard shutdown/child cleanup 확인
7. 이후 hooks/config와 analyze/run 같은 agentic workflow 진행
