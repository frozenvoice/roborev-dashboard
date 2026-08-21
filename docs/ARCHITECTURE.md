# Architecture

## 목표와 경계

Dashboard는 React SPA와 Go localhost server로 구성된다. Go server가 repository registry, Git adapter, RoboRev capability layer, typed command builder와 bounded process runner를 소유한다. Browser는 RoboRev daemon이나 OS process를 직접 호출하지 않는다.

```text
Browser (React)
  -> same-origin JSON API + CSRF
Go localhost server
  -> Repository registry -> Git executable + args
  -> RoboRev service
       -> stable JSON CLI (version/show/prompt/log and typed mutations)
       -> version-gated daemon HTTP API (metadata list/enqueue/rerun/panel members/health)
       -> typed CLI mutations (comment/close/cancel)
RoboRev daemon -> RoboRev-owned storage
```

## 주요 선택

### 단일 실행 파일

Vite production output을 `internal/webui/dist`에 생성하고 Go `embed`로 포함한다. 최종 사용자는 Node나 Go 없이 `roborev-dashboard.exe` 하나만 실행한다. 개발 스크립트는 frontend build 후 Go build 순서를 보장한다. `scripts/install.ps1` 소스 bootstrap은 이 runtime 경계와 분리되어 개발 도구만 준비하며 RoboRev를 설치하거나 실행하지 않는다. Dashboard의 RoboRev runtime onboarding은 Go backend가 소유하는 별도 capability다.

### RoboRev runtime 설치 신뢰 경계

`internal/roborevcompat`의 immutable manifest가 Dashboard가 완전히 지원하는 RoboRev release의 version, Windows/architecture, 공식 GitHub asset URL, exact compressed size, SHA-256, archive entry와 managed user target을 한 곳에서 정의한다. installer와 capability layer는 같은 정책을 사용한다. 이번 checkpoint의 권장 버전은 v0.64.0이며 newer/latest를 원격 metadata만 보고 자동 선택하지 않는다. 이미 발견된 supported binary는 존중하고 newer, unknown, broken 또는 policy-blocked binary를 자동 downgrade하거나 덮어쓰지 않는다. explicit `--roborev` override가 있으면 자동 설치를 제공하지 않는다.

Browser는 URL, version, digest, executable 또는 target을 설치 API에 전달하지 않는다. strict-empty same-origin/CSRF mutation이 설치를 시작하면 backend는 pinned HTTPS URL만 요청하고 redirect의 scheme, host, port와 횟수를 allowlist로 제한한다. response는 exact size보다 한 byte 큰 상한으로 streaming 검증하며 pinned SHA-256이 맞아야 다음 단계로 이동한다. 오류 응답 본문이나 signed redirect query는 상태와 log에 포함하지 않는다.

ZIP은 전체를 install directory에 풀지 않는다. root의 exact `roborev.exe` 하나만 허용하고 absolute/traversal/nested path, directory, symlink와 non-regular entry를 거부하며 compressed/extracted 크기를 제한한다. target directory component의 reparse point를 거부하고 같은 directory의 unique staging file을 flush한 뒤 no-replace publish한다. 기존 target은 성공·실패·경쟁 조건 모두에서 삭제하거나 교체하지 않으며 partial target과 알려진 temporary artifact만 정리한다.

설치된 후보는 bounded `version --json`의 exact product name과 canonical compatible version을 통과해야 한다. 그 뒤에만 `roborev.Client`의 mutex-protected generation binding을 atomic하게 교체하고 version/error cache를 새 generation으로 전환한다. 진행 중 command는 시작 시 얻은 immutable binary snapshot을 사용하며 이전 generation의 늦은 probe가 새 cache를 오염시키지 못한다. Dashboard 동작에는 absolute executable을 사용하므로 PATH를 변경하지 않는다.

setup은 process-scoped background state machine으로 checking, downloading, verifying, extracting, installing, binding, daemon start/health와 terminal ready/failed/blocked/unsupported 상태를 typed API로 제공한다. 전역 operation gate는 setup을 exclusive로, 일반 RoboRev mutation과 Panel Refine 전체 run을 shared lease로 취급해 binary rebind와 mutation이 겹치지 않게 하며 충돌은 기다리지 않고 409로 반환한다. 설치 뒤 health precheck와 bounded backoff로 daemon을 확인한다. daemon 시작 실패는 검증된 설치를 보존하고 재다운로드 없는 retry만 제공한다.

Windows가 exact policy error code로 실행을 거부하면 설치를 ready로 표시하지 않고 `blocked`로 분류한다. Dashboard는 target, requested version과 pinned digest를 계속 표시하지만 Smart App Control/WDAC, registry, Defender 설정을 변경하거나 `Unblock-File` 같은 우회를 실행하지 않는다. generic permission failure는 policy block으로 가장하지 않는다. runtime onboarding은 `roborev init`, Git hook, AI agent 설치·검사, credential 저장과 update를 수행하지 않는다.

### Agent detection과 quota 경계

Agent 목록은 exact v0.64.0 compatibility descriptor와 bounded config reader가 제공하는 safe subset으로 구성한다. global command override와 global/local `[review.subagents]` whole-entry merge를 적용하되 alias, canonical agent, model, reasoning과 resolved command만 반환하고 instructions, provider credential, API key, raw TOML과 dynamic ACP command/args는 읽어 내보내지 않는다. 자동 detection은 regular executable lookup만 사용하며 Cursor의 ambiguous `agent` command에 한해 upstream과 같은 `--version`/`-v` bounded identity probe를 수행한다. probe는 임시 working directory, nil stdin, 짧은 timeout/WaitDelay, bounded combined output과 forge credential/preload hook이 제거된 환경을 사용한다. timeout, empty/crashed/oversized output 또는 Grok identity는 Cursor로 인정하지 않는다.

`Detected`는 executable/config presence이고 `Checked`는 실제 smoke request 성공이다. `GET /api/agents`와 background refresh는 `check-agents` 또는 Agent.Review를 호출하지 않는다. 사용자가 canonical allowlist의 agent 하나를 confirmation한 `POST /api/agents/check`만 fixed `check-agents --agent <canonical> --timeout 60` argv를 request context에서 실행한다. CLI 내부 timeout보다 긴 bounded outer timeout을 두고 v0.64.0의 bounded single-agent output만 최소 parser로 분류하며 결과는 process memory에만 둔다. browser disconnect cancellation은 failure가 아니라 canceled/not-completed다. 설치되지 않았거나 exact v0.64.0이 아닌 runtime에서는 discovery/check를 fail closed해 다른 agent contract를 추측하지 않는다.

Agent health check는 요청이 확인한 selected repository에 전용 read lease를 잡고 single authoritative discovery와 quota-bearing dispatch가 끝날 때까지 유지한다. repository selection commit만 같은 lease의 writer로 직렬화하며 Add, panel preference와 일반 registry read는 막지 않는다. Frontend는 repository identity 변경 epoch와 same-repository `GET /api/agents` request generation을 분리해 단순 refresh가 승인된 check completion을 무효화하지 않게 하고, 실제 repository 변경 또는 다른 scope 응답은 stale health reload와 toast를 거부한다.

### Runtime control lifecycle

Queue state는 prompt/log를 포함하지 않는 official v0.64.0 daemon `GET /api/status`의 typed bounded `queue_paused` 필드에서 읽는다. Pause/Resume은 이미 healthy한 daemon에 `POST /api/queue/pause` 또는 `/api/queue/unpause`를 보내고 exact boolean response를 검증한다. CLI `pause`가 daemon을 암묵적으로 시작하는 경로와 RoboRev DB 직접 읽기는 사용하지 않는다. Pause 중에도 running job은 계속되고 새 queued job만 시작하지 않는다.

Daemon Start/Stop/Restart는 typed `daemon start|stop|restart` argv만 사용하며 확인 UI에는 backend가 제공한 executable과 exact argv만 표시한다. Stop/Restart는 acceptance 뒤 request context와 분리된 process-owned manager가 bounded health/offline verification까지 소유하며, Dashboard shutdown은 active context를 cancel하고 worker와 gate release를 기다린다. restart는 upstream command 하나를 사용해 daemon이 이미 stopped인 경우 start하는 semantics를 보존한다. Stop verification은 transport-level 연결 불가만 offline 증거로 인정하고, reachable HTTP 오류·malformed/oversized/unhealthy health 응답은 실패로 유지한다. 실제 v0.64.0 runtime health envelope의 `$schema`는 optional이며, missing 또는 임의 JSON string을 허용하고 present null/non-string을 거부한다. `DisallowUnknownFields`를 유지해 다른 contract drift도 거부한다. CLI 성공 뒤 health가 기대 상태와 다르면 성공으로 표시하지 않는다. exact v0.64.0 이외에서는 status discovery를 포함한 runtime control 계약을 fail closed한다.

전역 operation gate의 runtime-control lease는 setup, Panel Refine, ordinary mutation과 다른 runtime control 모두와 fail-fast 409로 충돌한다. setup도 runtime control을, mutation/refine도 active runtime control을 거부한다. Queue Pause/Resume은 짧은 runtime-control lease를, daemon lifecycle은 terminal success/failure/cancel까지 유지되는 lease를 사용한다. 따라서 active Panel Refine 중 queue pause나 daemon restart가 workflow를 중간에 stall시키지 않는다.

### transport

stable JSON CLI를 우선한다. v0.64.0의 `review` 명령에는 JSON launch receipt가 없으므로 새 review는 감지된 daemon endpoint의 public `/api/enqueue` 계약을 version capability로 캡슐화한다. Dashboard가 요청 시점에 Git으로 `HEAD`를 full SHA로 고정해 daemon에 전송하고, enqueue receipt와 동일한 fresh repository metadata를 browser에 반환하므로 실제 target이 audit 가능하다. UI는 실행되지 않는 CLI command preview를 만들지 않는다.

`/api/job/rerun`은 v0.64.0 upstream에서 기존 frozen target과 resolved agent/panel config를 복제한다. 이 기능은 `Rerun same target/config`로만 노출하고 새 HEAD review와 request type, endpoint 및 UI 위치를 공유하지 않는다. Dashboard의 generic frozen-rerun adapter와 UI는 `job_type=task` 또는 `git_ref=run`인 job을 거부하거나 숨기고 `phase=task-rerun-unsupported`를 반환한다. 따라서 Tasks execution-disabled policy를 우회하는 rerun은 없으며 Tasks 자체에는 rerun을 지원하지 않는다.

v0.64.0의 `repo list`에는 JSON이 없다. 초기 vertical slice는 Dashboard registry의 canonical Git repositories를 사용한다. human-readable `repo list` parsing은 core path에 넣지 않으며 향후 official JSON/API capability가 있을 때 추가한다.

v0.64.0에는 effective review panel을 열거하는 machine-readable CLI나 daemon API도 없다. Dashboard는 같은 버전의 upstream 계약에 맞춘 typed BurntSushi TOML decoder로 `ROBOREV_DATA_DIR/config.toml` 또는 `~/.roborev/config.toml`의 global `[review]`와 등록된 root의 `.roborev.toml`만 read-only로 읽는다. config entry는 플랫폼별 no-follow handle로 열고 handle의 regular-file metadata를 확인하며 1 MiB+1 byte까지만 읽어 oversized/special file을 fail closed한다. subagent/panel map은 global과 local의 합집합이며 같은 이름은 local entry 전체가 override한다. Browser에는 panel 이름, member 이름, synthesis/backup agent와 global/local origin만 보내며 instructions, model, provider, secret 및 나머지 config는 보내지 않는다. malformed 또는 구조적으로 resolve할 수 없는 config는 `none`으로 fallback하지 않고 Review HEAD를 막는다. Dashboard의 fallback은 registered root에 직접 존재하는 `.roborev.toml`까지만 지원하며, v0.64.0의 untracked main-checkout config를 linked worktree에 상속하는 특수 규칙은 구현하지 않는다. local config가 없는 경우에만 `.git` regular-file layout을 ambiguous로 분류하며, 이때 Git metadata가 실패하거나 누락되면 global named panel을 노출하지 않고 panel discovery/mutation을 fail closed한다. typed `git rev-parse --path-format=absolute --git-dir --git-common-dir` 결과가 다른 actual linked worktree만 ambiguous 상태에서 global named panel을 숨기고 명시적인 `Single agent · no panel`만 제공한다. submodule, separate-git-dir와 ordinary checkout은 metadata가 같아 global panel을 유지한다.

### process 실행

외부 process는 shell 없이 executable과 argument slice로 실행한다. stdout/stderr는 분리하고 각각 상한을 적용한다. request cancellation과 timeout을 전달하고 `WaitDelay`로 pipe 대기를 제한한다. Windows에서는 child를 kill-on-close Job Object에 넣어 timeout/cancellation 시 descendant까지 종료한다. daemon start만 background daemon 생존을 위해 이 tree ownership을 명시적으로 제외한다. audit metadata에는 executable basename, args, duration, exit code, truncation 여부만 기록하며 환경 변수 전체를 남기지 않는다.

### Live event relay

`roborev stream --repo <registered canonical path>`는 exact v0.64.0에서만 capability를 열며, stream 시작 전 CLI `Installation` version/capability와 daemon health의 typed `Version`이 모두 supported이고 서로 일치하며 daemon이 already-online/healthy인지 확인해야 한다. offline daemon을 자동 시작하지 않으며 unsupported/failed stream은 기존 API와 polling을 방해하지 않는다. typed argv와 immutable binary snapshot을 사용하고 shell, stdin, generic command endpoint는 없다.

Backend는 repository identity별 RoboRev source 하나와 `0..N` browser subscribers를 소유한다. JSONL stdout은 bounded line reader(1 MiB), stderr는 bounded sink(64 KiB)로 처리하고 malformed/unknown/oversized line, process exit, cancellation과 shutdown을 안전하게 처리한다. recognized `job.enqueued`, `review.started`, `review.completed`, `review.failed`, `review.canceled`, `review.remapped`만 sequence를 증가시키며 raw payload를 버리고 `jobs-changed` 하나의 invalidation signal로 축약한다. subscriber channel은 한 칸이며 burst를 nonblocking coalesce하고 source 재시작은 subscriber가 남아 있고 daemon gate가 다시 healthy할 때만 bounded backoff로 시도한다. 마지막 subscriber 뒤에는 짧은 idle grace를 두되 reconnect가 source를 중복 생성하지 않게 하며, `Server.BeginShutdown`이 HTTP shutdown 전에 source와 child를 취소한다.

`GET /api/events?repositoryIdentity=<opaque>`는 source startup side effect를 가지므로 browser request의 same-origin을 fail-closed하게 증명한 뒤에만 query/registry/subscribe를 수행하는 SSE다. explicit `Origin`은 정확히 하나의 fully formed origin이어야 하며 request의 scheme+host와 exact match해야 한다. `Origin`이 없을 때는 native same-origin EventSource 계약인 `Sec-Fetch-Site: same-origin`, `Sec-Fetch-Mode: cors`, `Sec-Fetch-Dest: empty`가 각각 정확히 한 번씩 있어야 허용한다. foreign, `null`, malformed, blank, duplicate Origin과 누락·중복·다른 Fetch Metadata는 403이며, SSE에는 CSRF token을 요구하지 않는다. mutation은 non-browser/local client 호환성을 위해 기존 absent-Origin 허용과 CSRF semantics를 유지한다. query shape도 strict하게 검사하고 응답에는 `{repositoryIdentity,sequence,kind}`만 보낸다. prompt, diff, log, agent output, credential 가능 필드와 raw stderr/error는 브라우저로 전달하지 않는다. stream은 **lossy refresh/invalidation hint**이며 상태를 직접 표현하지 않는다. frontend는 `jobs-changed`를 받으면 burst를 짧게 coalesce/debounce하고 existing `/api/reviews` **authoritative API = actual state**를 다시 읽는다. repository epoch가 다른 event/late response는 무시하고 EventSource reconnect를 유지한다. 기존 4초 review polling은 제거하지 않으며 **4초 polling = fallback/safety net**이다.

Manager는 single identity ownership/state machine으로 identity별 source 하나, shared startup result, subscriber cancellation, idle-generation cleanup과 bounded restart를 관리한다. `Subscribe`는 opaque identity만 받고 initial start와 모든 restart 직전에 registry authorizer로 identity를 fresh resolve하므로 stale/fallback path를 재사용하지 않으며, 마지막 subscriber가 사라지거나 Dashboard shutdown이 시작되면 source context와 child를 정리한다.

### repository registry

server가 canonical absolute path만 등록한다. 등록 시 `.git`을 확인하고 `git -C <path> rev-parse --show-toplevel` 결과가 같은 root인지 검증한다. canonical path identity는 Windows에서 case-insensitive, case-sensitive platform에서 exact spelling으로 중앙화한다. 등록된 path의 fresh inspection 결과와 startup 시 persisted path를 다시 load한 결과도 expected identity와 일치하는지 검증해 junction/symlink retarget으로 다른 Git root가 authorization되거나 Review target이 되는 것을 거부한다. Browser에는 backend가 생성한 opaque repository identity를 correlation key로 제공하며, mutation authorization에는 계속 canonical path만 사용한다. path를 받는 mutation뿐 아니라 global job ID와 panel UUID를 받는 read/mutation도 daemon의 metadata-only lookup으로 owning repository를 확인하고 registry 밖이면 거부한다. recent repository, last selected repository와 platform-aware identity별 마지막 panel preference만 `os.UserConfigDir()/roborev-dashboard/config.json`에 저장하며 credential은 저장하지 않는다. Add/Select/panel preference는 prospective state 저장이 성공한 뒤에만 in-memory 상태를 갱신한다. 저장된 panel은 authorization이 아니며 current effective config에서 다시 발견될 때만 사용한다.

### HTTP 보안

기본 bind는 `127.0.0.1`이다. mutation request는 exact Host, allowed Origin과 per-process CSRF token을 검증한다. request body에 repository path가 있더라도 registry에서 다시 resolve한다. generic command API는 없다.

### 데이터와 UI 상태

review 목록은 v0.64.0의 `/api/jobs?repo=...&omit_prompt=true`를 사용하며, 각 요청 완료 4초 후 다음 요청을 예약해 겹치는 poll을 만들지 않는다. polling이 가능한 selected repository는 첫 current-generation 목록 요청이 성공 또는 실패로 settle되기 전까지 empty state와 Review HEAD를 gate한다. repository 전환과 unmount는 generation을 무효화해 이전 응답과 오류가 새 상태를 덮거나 새 repository의 initial gate를 해제하지 못하게 한다. background refresh 오류는 반복 toast가 아니라 deduplicated inline 상태로 표시한다. 목록 polling은 known heavy fields를 adapter에서 제거하며 Panel Refine은 별도 `/api/job/output` status-prefix 계약을 사용해 active prompt/lines를 수신하지 않는다. `list --json`은 full prompt와 diff를 포함하므로 주기 경로에서 사용하지 않는다. detail, prompt와 log는 별도 endpoint에서 사용자가 tab을 선택할 때만 읽는다. panel synthesis를 actionable parent로 우선 표시하고 member metadata는 같은 `panel_run_uuid`의 v0.64.0 full-run `/api/jobs?panel_run=...&limit=0&omit_prompt=true` 결과에서 synthesis 1개와 member rows를 함께 받아 lazy-load/polling한다. `GET /api/panels/{run}/members`는 opaque `repositoryIdentity`와 synthesis `parentJobId`를 필수로 받아 response 자체의 정확히 하나인 synthesis row를 parent로 bind하고 parent ID, repository, run, panel, `panel_role=synthesis`와 `job_type=synthesis` 및 모든 row의 positive unique ID를 fail closed하게 검증한다. 별도 parent `JobMetadata` lookup은 반복 poll 경로에서 사용하지 않는다. member tree는 upstream status, verdict, member name, agent, model과 duration을 그대로 표시하고 metadata만으로 terminal/total 진행률을 계산한다. member 원문은 사용자가 선택한 job 하나에만 panel-scoped `GET /api/panels/{run}/members/{memberId}`를 사용한다. backend가 같은 parent/run/repository의 실제 member임을 다시 확인한 뒤 typed `show --job <id> --json`을 호출하고 Show 자체의 `job_id`, nested job ID, repository, run과 role도 검증한다. synthesis가 재가공한 내용이나 Dashboard 번역 결과로 대체하지 않는다. 선택 전 member output을 선조회하지 않고, parent/member list/member detail/prompt·log loading을 분리하며 repository/panel/member scope token과 generation으로 새 scope의 첫 skeleton, 동일 scope background refresh, 늦은 detail 응답의 stale 반영을 구분한다. terminal reconciliation이 initial detail 요청을 supersede하면 current generation이 success/final failure 뒤 `detailLoading` 종료를 소유하고 stale finally는 state를 변경하지 않는다. terminal log refresh의 실패는 generation-aware lazy error와 retry UI로 표시한다. cancel/rerun/close 같은 panel action과 전체 verdict는 계속 synthesis parent 기준이다. mutation 성공 후 목록을 RoboRev source of truth에서 다시 fetch한다. Live event relay는 이 목록을 직접 수정하지 않고 **lossy refresh/invalidation hint**만 제공하며 `/api/reviews` **authoritative API = actual state**, **4초 polling = fallback/safety net** 관계를 유지한다. 이벤트 burst는 frontend에서 coalescing/debounce하고 repository switch 뒤 stale event와 late response를 거부하며, SSE unavailable/reconnect 중에도 polling을 계속한다.

repository 전환 시 panel catalog도 별도 current-generation request로 읽는다. valid saved preference가 있으면 복원하고, named panel이 하나면 자동 선택하며, 여러 개면 사용자 선택 전까지 Review HEAD를 막고, named panel이 없을 때만 reserved `none`을 자동 선택한다. 사용자가 선택하거나 Review HEAD를 누를 때 backend는 fresh repository identity와 current TOML을 다시 resolve해 exact panel 또는 built-in `none`만 daemon JSON enqueue에 전달한다.

### 화면과 작업 진입점

Sidebar의 `Fix & Refine`는 더 이상 placeholder가 아니다. 기존 Panel Refine 화면은 이 화면의 full workflow surface로 이동하고, General Fix의 terminal F single review와 panel synthesis parent도 같은 화면에서 선택한다. `Reviews`는 review 목록과 상세를 계속 소유하되 Panel Refine의 전체 조작 화면을 중복 렌더링하지 않는다. active 또는 `needs_attention`인 Panel Refine이 있으면 `Reviews`에는 compact summary만 표시하고, 사용자가 `Fix & Refine`로 이동해 해당 operation을 재개한다. Summary의 링크와 operation의 repository identity/generation은 현재 선택 repository와 일치할 때만 동작한다. `Review HEAD`와 `Rerun same target/config`는 계속 별개의 primary/advanced action이며 General Fix 진입점으로 재사용하지 않는다.

### Frontend localization

`web/src/i18n.tsx`는 English source catalog와 exact-key Korean catalog, `Locale = 'ko' | 'en'`, `roborev-dashboard-language` storage key, guarded storage access와 browser fallback을 한 곳에서 소유한다. `I18nProvider`의 `t`/`format` callback은 locale 전환에도 stable identity를 유지하고 `<html lang>`만 presentation 상태로 갱신한다. App은 stable navigation/repository/panel/review IDs와 번역된 표시 label을 분리하며 locale 변경을 fetch/effect dependency나 React key/remount 조건으로 사용하지 않는다.

Language selector는 sidebar footer와 overlay가 가리는 다섯 개의 confirmation surface(install, daemon, agent check, Fix Prompt, Panel Refine) 헤더에 배치한다. 전환은 현재 repo, panel, detail/tab, modal, setup/progress/refine/agent/runtime/form/search/comment 상태를 유지하고 새 network request나 mutation을 만들지 않는다. Dashboard가 만든 chrome/empty/loading/error/action 문구만 번역하며 RoboRev, Git/SHA/API/CLI/CSRF/URL/JSON, paths, versions, names, branches, IDs, executable argv와 backend/RoboRev raw error는 source of truth로서 보존한다. English fallback은 누락 key를 안전하게 표시하고 typed catalog가 build 단계에서 key drift를 검출한다.

`GET /api/reviews/{id}/fix-prompt`는 job ownership과 current registered repository를 확인하고 `show --job --json`을 lazy-load한 뒤 요청한 single review, panel synthesis 또는 individual panel member의 top-level finding output과 최소 metadata만 typed decode한다. terminal F 결과에 한해 deterministic Korean template를 생성하며 LLM, `roborev fix`, mutation, close 또는 rerun을 실행하지 않는다. prompt, diff, log, comments, 다른 member의 aggregate와 config는 포함하지 않고 finding은 변형하지 않는다. target과 current HEAD가 다르면 stale warning을 넣으며 input/output 크기는 bounded하고 초과 시 truncate하지 않고 거부한다.

Dashboard의 `/api/runtime`은 repository inspection이나 review listing 없이 TTL 30초의 version detection과 v0.64.0의 작은 `/api/health`만 수행하며 UI가 10초마다 호출한다. 실제 `status --json`은 최근 job의 full prompt까지 포함할 수 있어 주기적 polling transport로 사용하지 않는다. panel member도 목록에 합치지 않고 panel tab을 열 때 `/api/jobs?panel_run=...&limit=0&omit_prompt=true`의 v0.64.0 full-run 응답(멤버 + 정확히 하나의 synthesis)을 받아 검증한다. backend member list/detail은 요청의 opaque repository identity를 registry에서 resolve하고 `gitx.SameRepositoryPath`로 권한 경계를 재검증한다. member list는 매 poll에 PanelMembers 1회만 호출하며 parent JobMetadata를 호출하지 않는다. nonterminal member가 있는 동안 4초마다 갱신하고, `limit=0`은 unlimited로 문서화되어 있다.

### Panel Refine

Panel Refine은 React effect가 아니라 Dashboard backend process가 소유하는 typed state machine이다. API는 `POST /api/panel-refines`, `GET /api/panel-refines`, `GET /api/panel-refines/{id}`, `POST /api/panel-refines/{id}/cancel`로 구성되며, Browser는 시작·상태 조회·취소만 요청한다. browser reload나 tab close는 active run을 중단하지 않는다. 이 checkpoint의 run은 process-scoped이므로 Dashboard process가 종료되면 active run context와 child process가 취소되고, 재시작 뒤 자동 resume하지 않는다. active run은 `reviewing`, `evaluating`, `fixing`, `final_reviewing`, `passed`, `needs_attention`, `failed`, `cancelled` 같은 명시적 phase와 iteration/fix/review receipt를 보존한다.

시작 시 registered repository의 canonical identity를 fresh inspection으로 재검증하고, clean working tree·branch·HEAD를 확보한다. iteration panel과 선택한 optional final panel은 현재 Panel Resolver에서 다시 resolve하며, fix agent는 typed allowlist로 제한한다. 기본 max fix attempts는 3이고 허용 범위는 1~10이다. 같은 repository identity에는 process-scoped lock을 두어 동시에 두 Panel Refine을 시작하지 않으며, active run 동안 해당 repository의 `Review HEAD`, `Rerun same target/config`, 다른 Panel Refine start도 backend에서 거부한다. read-only 조회는 계속 허용한다.

각 iteration은 fresh `Inspect` 결과의 full SHA와 branch를 사용해 새 panel review를 enqueue한다. 기존 frozen target을 재실행하는 Rerun은 사용하지 않는다. enqueue receipt에는 synthesis job ID, panel run UUID, repository path, full `git_ref`, branch, panel, synthesis role과 panel job type이 모두 있어야 하며 요청한 identity와 일치하지 않으면 중단한다. v0.64.0 Panel Refine status는 `/api/job/output?job_id=...`의 ordered `job_id,status,lines,has_more` 응답에서 8 KiB 이하 prefix만 읽고 `lines` 값 직전에 response를 닫는다. `Content-Type`, HTTP status, key order, job ID와 malformed prefix를 검증하며 v0.64.x 밖의 capability와 4xx contract 오류는 fail closed한다. `queued`/`running` 중에는 metadata를 요청하지 않고, 첫 `done`에서만 `omit_prompt=true` terminal metadata를 최대 256 KiB로 bounded fetch해 receipt의 repository/ref/branch/panel/run UUID/role/type, skipped, status와 P/F verdict를 모두 재검증한다. transport/5xx 오류만 제한된 backoff retry하며 unknown/canceled/failed/skipped/malformed/identity mismatch는 PASS로 취급하지 않고 `failed` 또는 `needs_attention`으로 중단한다. iteration panel이 PASS하고 final panel이 명시되면 final panel도 같은 HEAD에서 별도로 검증한다. 두 panel이 같으면 중복 review를 하지 않는다. final panel의 F도 같은 fix budget을 사용하며, fix 뒤에는 항상 iteration panel부터 새 HEAD를 검증한다.

이번 checkpoint에서는 Codex만 허용하는 fix adapter가 v0.64.0의 확인된 typed command인 `roborev fix --agent codex --quiet <synthesis-id>`를 configured executable과 argument array로 실행한다. synthesis/parent job ID만 전달하고 finding text, prompt, diff 또는 arbitrary executable을 argv에 넣지 않는다. fix 성공 뒤에도 fresh inspection으로 repository identity와 branch가 동일하고 working tree가 clean하며 HEAD가 실제로 전진했는지 확인한다. HEAD 불변, dirty tree, branch 변경, identity drift, authorization 상실 또는 fix/review 오류는 자동으로 다음 iteration으로 넘기지 않고 `needs_attention`/`failed`로 중단한다.

process-scoped manager(refine, fixoperation, setup, runtimecontrol)의 Start acceptance와 Shutdown은 같은 manager mutex 아래에서 선형화된다. `closing` 플래그가 등록(`wg.Add` equivalent)과 함께 원자적으로 검사되므로, validate/preflight 중인 Start가 shutdown 시작 뒤에 추적되지 않은 채 등록되어 Shutdown 반환 후 work를 남기는 race는 구조적으로 존재하지 않는다. Shutdown은 새 acceptance를 닫은 뒤 active cancel과 wait를 수행한다.

Review 완료 직후의 재검증과 repository identity lock은 사용자가 외부에서 commit, edit, checkout 또는 path retarget을 한 stale finding의 자동 적용을 막는다. 각 enqueue, polling, fix에는 짧은 bounded timeout을 두며 전체 run에도 명시적인 safety ceiling을 둔다. 취소 중 review는 RoboRev cancel을 best-effort로 호출하고, 취소 중 fix는 context cancellation으로 child process를 중단한다. 취소 후 fresh inspection 결과는 `cancelState`의 availability, HEAD, branch, dirty 및 inspection error로 run에 기록하며 clean을 추측하지 않는다.

v0.64.0의 `fix`는 fix agent가 새 commit을 만든 뒤 panel 없는 default review를 자동 enqueue할 수 있다. Dashboard는 선택한 panel의 새 HEAD review를 별도로 enqueue하므로, 이 upstream 동작으로 unrelated extra review가 생길 수 있다. Dashboard는 응답이나 이름만으로 이를 추측해 억제하지 않으며, 해당 중복 제거는 upstream capability가 제공될 때 별도 version-gated 계약으로 다룬다.

### General Fix

General Fix는 Panel Refine의 반복 workflow와 별개인 명시적 단일 fix operation이다. Dashboard가 지원하는 exact RoboRev v0.64.0의 upstream contract는 positive decimal job ID를 positional target으로 받는 `fix --agent codex --quiet <job-id>`다. v0.64.0의 `fix`는 현재 checkout에서 agent를 실행하며 시작 시 dirty tree나 review target의 stale HEAD를 차단하지 않는다. stdout/stderr는 machine-readable 결과 계약이 아니고, exit 0도 no-change 또는 dirty worktree인 채 job이 닫힌 경우를 배제하지 않는다. commit이 생성되면 RoboRev가 panel 없는 default review enqueue를 시도할 수 있으므로 Dashboard는 그 부수 job을 성공 근거로 추측하거나 억제하지 않는다. 이 동작은 [v0.64.0 fix source](https://github.com/kenn-io/roborev/blob/v0.64.0/cmd/roborev/fix.go)와 [assisted-refactoring guide](https://github.com/kenn-io/roborev/blob/v0.64.0/docs/guides/assisted-refactoring.md)를 기준으로 version-gated 한다.

Dashboard는 upstream의 느슨한 실행 전제를 안전 경계로 보강한다. 요청은 선택된 registered repository의 opaque `repositoryIdentity`와 positive `targetJobId`만 받으며, backend가 fresh registry/ownership lookup과 job metadata를 다시 확인한다. 대상은 명시적으로 `status=done`, `verdict=F`, `closed=false`, `skipped=false`인 terminal review여야 하며 single review 또는 panel synthesis parent만 허용한다. panel member ID, synthesis가 아닌 다른 panel row, PASS/running/canceled/closed job은 General Fix 대상이 아니다. 시작 시 current branch와 target job의 full `git_ref`/HEAD가 같고 working tree가 clean이어야 한다. stale, dirty, branch/identity drift는 자동 보정하지 않고 conflict로 거부한다.

Preview/prepare 단계는 backend가 fresh authorization으로 만든 opaque preview token과 exact executable/argument array를 반환한다. Browser는 executable, job ID 이외의 argv, finding text, prompt, diff 또는 arbitrary command를 제출하지 않으며, preview에 표시된 command가 confirmation 대상이다. 확인 POST는 token을 재사용하더라도 repository, target, branch, HEAD, clean state와 authorization을 다시 검사하고 같은 typed command builder로 argv를 다시 생성한다. Browser request의 lifetime과 분리된 process-scoped manager가 operation을 `running`, `succeeded`, `failed`, `cancelled` 같은 명시 상태로 소유한다. API는 `GET /api/fixes/preview`, `POST /api/fixes`(확인된 실행), `GET /api/fixes`, `GET /api/fixes/{id}`, `POST /api/fixes/{id}/cancel`을 제공하며, cancel과 Dashboard shutdown은 context와 child process를 정리한다. browser disconnect나 reload는 실행을 암묵적으로 취소하지 않고, process restart 뒤 active operation을 resume하지 않는다.

General Fix는 shared global operation gate와 repository-scoped lease를 사용한다. global gate는 Setup/runtime control/Panel Refine/Review·Rerun을 포함한 다른 mutation과 active Fix를 상호 배제하고, repository lease는 Panel Refine 및 Review/Rerun과 같은 repository의 worktree 경쟁을 fail-fast conflict으로 거부한다. read-only 조회는 허용한다. process runner는 shell 없이 validated executable과 argument slice를 사용하며 stdout/stderr와 오류는 bounded status/error summary만 보존한다. 실행 완료 후 backend는 fresh Git inspection으로 같은 registered identity와 branch, HEAD advancement, clean tree를 모두 확인한다. exit 0이더라도 commit/HEAD가 전진하지 않았거나 dirty, branch 변경, identity drift, process/cancel/timeout 오류가 있으면 성공으로 표시하지 않고 `failed` 또는 `cancelled`로 남긴다. 성공·실패·취소 뒤에는 UI 추측 상태를 갱신하지 않고 Reviews와 Fix & Refine의 authoritative API를 다시 조회한다.

### Tasks

Tasks는 **history-only** 화면이다. v0.64 Custom Task 실행은 fail-closed로 비활성화되어 있다. 이유는 다음과 같다.

- RoboRev `run`의 job-level 기본은 non-agentic이지만, upstream Codex agent는 `agenticMode := a.Agentic || AllowUnsafeAgents()`로 daemon effective global config를 함께 본다. `allow_unsafe_agents=true` 또는 `disable_codex_sandbox=true`인 daemon은 job-level `Agentic:false` 뒤에서도 더 위험한 mode로 task를 실행할 수 있다.
- daemon이 실제로 사용하는 config identity와 effective safety state를 증명하는 machine-readable contract가 v0.64에는 없다. `/api/health`, `/api/status`, OpenAPI 어디에도 config path/identity나 effective `allow_unsafe_agents`/`disable_codex_sandbox` 값이 노출되지 않으며, `roborev status --json`도 version/queue metadata만 제공한다.
- Dashboard process가 읽는 global config file은 각 process 자신의 environment(`ROBOREV_DATA_DIR` 포함)에 의존하므로, 그것을 daemon의 effective state로 가정하는 것은 추측이다. read-only safety authorization은 추측해서는 안 된다.

따라서 Dashboard에는 `run` enqueue command builder, receipt parser, task preflight/safety adapter, preview token, task manager와 task execution lease가 존재하지 않는다. `POST /api/tasks/preview`와 mutation authorization(same-origin/CSRF) 뒤의 `POST /api/tasks`는 service/registry/process를 호출하지 않고 `501 phase=task-execution-disabled` tombstone을 반환한다. Frontend composer는 inspection-only(disabled textarea)이며 실행 CTA가 없고 `/api/tasks*`를 호출하지 않는다. UI와 backend 양쪽 모두에서 production reachable한 custom task enqueue 경로는 0이다. Analyze가 upstream 자체가 `Agentic:true`라서 막힌 것과 달리 Tasks는 "effective safety를 증명 불가"라서 막혀 있으므로, 두 disable 사례를 하나의 generic unsafe-agent manager로 합치지 않는다.

기존 기능은 유지된다. Task history는 `/api/reviews`의 `job_type=task` + `git_ref=run` rows와 SSE `jobs-changed` + 4초 polling fallback으로 조회하고, 결과는 기존 ReviewDetail lazy detail path로 연다. Generic frozen rerun은 `job_type=task` 또는 `git_ref=run`을 거부·숨기고 `phase=task-rerun-unsupported`를 반환한다. 이 차단이 없으면 generic rerun이 execution-disabled policy를 우회하는 통로가 되므로 execution disable 이후에도 유지한다. 향후 재검토 조건은 upstream이 daemon effective safety attestation을 공식 machine-readable 계약으로 제공하고 capability로 확인될 때뿐이다.

### Analyze

Analyze는 exact v0.64.0 `analyzeIntrospection=true` capability로만 열리는 introspection-only catalog/prompt 경로다. execution capability는 노출하지 않는다. type discovery는 bounded human-readable `roborev analyze --list`를 strict typed parser로 읽고, `Available analysis types:` header, safe-slug 이름, nonempty description, 중복 금지와 reasonable count/output bound를 검증한다. prompt는 사용자가 선택한 type에 대해 lazy하게 `roborev analyze <type> --show-prompt`를 호출한다. RoboRev가 반환한 type name, description과 prompt template은 source of truth이며 Dashboard는 locale에 따라 번역하거나 재작성하지 않는다.

backend가 사용하는 introspection exact argv는 다음 두 가지다.

```text
list:       roborev analyze --list
prompt:     roborev analyze <type> --show-prompt
```

v0.64 Analyze enqueue는 upstream의 verified contract상 `Agentic: true`이며 worker도 `WithAgentic(true)`로 구성된다. Edit/Write/Bash 권한을 포함한 agentic 작업은 Dashboard가 임의로 실행하면 안 되므로, Dashboard에는 enqueue command builder, receipt DTO/parser, execution capability 또는 실행 service method가 없다. 따라서 `GET /api/analyses/preview`와 CSRF/Origin 보호 뒤의 `POST /api/analyses`는 모두 process/service를 호출하지 않고 `501 phase=analysis-execution-disabled` tombstone을 반환한다. JSON execution coverage는 P/disabled다. 향후 upstream이 agentic 안전 정책, 명시적 preview/confirmation 계약과 non-agentic 또는 별도 승인 transport를 제공하고 capability로 확인될 때만 재검토한다.

Analyze manager는 operation history, preview token, confirmation, registry lease, execution gate 또는 별도 job lifecycle을 저장하지 않는 stateless catalog/prompt adapter다. GET types/prompt와 SSE는 strict browser read authorization 뒤에만 service/stream을 호출한다. explicit Origin은 정확히 하나의 same scheme+host이거나, Origin이 없을 때 `Sec-Fetch-Site: same-origin`, `Sec-Fetch-Mode: cors`, `Sec-Fetch-Dest: empty`가 각각 정확히 하나여야 한다. foreign/null/blank/malformed/duplicate Origin과 missing/duplicate/wrong metadata는 query, registry, service 전에 403이다.

health adapter는 다른 runtime/stream 계약과 동일하게 bounded body, `DisallowUnknownFields`, exactly one JSON value, `healthy`와 version 검증을 유지한다. `$schema`는 missing 또는 임의 JSON string이면 허용하고, present null/non-string은 거부한다. 실제 Analyze enqueue, daemon mutation 또는 LLM smoke는 자동 검증에서 실행하지 않는다.

## 확장 원칙

각 RoboRev 기능은 service method, typed request, command builder, capability, HTTP route, confirmation 정책과 테스트를 함께 추가한다. CLI help에서 확인되지 않은 flag를 추측해 만들지 않는다. 중요한 변경은 이 문서와 coverage matrix를 함께 갱신한다.
