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

Vite production output을 `internal/webui/dist`에 생성하고 Go `embed`로 포함한다. 최종 사용자는 Node나 Go 없이 `roborev-dashboard.exe` 하나만 실행한다. 개발 스크립트는 frontend build 후 Go build 순서를 보장한다.

### transport

stable JSON CLI를 우선한다. v0.64.0의 `review` 명령에는 JSON launch receipt가 없으므로 새 review는 감지된 daemon endpoint의 public `/api/enqueue` 계약을 version capability로 캡슐화한다. Dashboard가 요청 시점에 Git으로 `HEAD`를 full SHA로 고정해 daemon에 전송하고, enqueue receipt와 동일한 fresh repository metadata를 browser에 반환하므로 실제 target이 audit 가능하다. UI는 실행되지 않는 CLI command preview를 만들지 않는다.

`/api/job/rerun`은 v0.64.0 upstream에서 기존 frozen target과 resolved agent/panel config를 복제한다. 이 기능은 `Rerun same target/config`로만 노출하고 새 HEAD review와 request type, endpoint 및 UI 위치를 공유하지 않는다.

v0.64.0의 `repo list`에는 JSON이 없다. 초기 vertical slice는 Dashboard registry의 canonical Git repositories를 사용한다. human-readable `repo list` parsing은 core path에 넣지 않으며 향후 official JSON/API capability가 있을 때 추가한다.

v0.64.0에는 effective review panel을 열거하는 machine-readable CLI나 daemon API도 없다. Dashboard는 같은 버전의 upstream 계약에 맞춘 typed BurntSushi TOML decoder로 `ROBOREV_DATA_DIR/config.toml` 또는 `~/.roborev/config.toml`의 global `[review]`와 등록된 root의 `.roborev.toml`만 read-only로 읽는다. config entry는 플랫폼별 no-follow handle로 열고 handle의 regular-file metadata를 확인하며 1 MiB+1 byte까지만 읽어 oversized/special file을 fail closed한다. subagent/panel map은 global과 local의 합집합이며 같은 이름은 local entry 전체가 override한다. Browser에는 panel 이름, member 이름, synthesis/backup agent와 global/local origin만 보내며 instructions, model, provider, secret 및 나머지 config는 보내지 않는다. malformed 또는 구조적으로 resolve할 수 없는 config는 `none`으로 fallback하지 않고 Review HEAD를 막는다. Dashboard의 fallback은 registered root에 직접 존재하는 `.roborev.toml`까지만 지원하며, v0.64.0의 untracked main-checkout config를 linked worktree에 상속하는 특수 규칙은 구현하지 않는다. local config가 없는 경우에만 `.git` regular-file layout을 ambiguous로 분류하며, 이때 Git metadata가 실패하거나 누락되면 global named panel을 노출하지 않고 panel discovery/mutation을 fail closed한다. typed `git rev-parse --path-format=absolute --git-dir --git-common-dir` 결과가 다른 actual linked worktree만 ambiguous 상태에서 global named panel을 숨기고 명시적인 `Single agent · no panel`만 제공한다. submodule, separate-git-dir와 ordinary checkout은 metadata가 같아 global panel을 유지한다.

### process 실행

외부 process는 shell 없이 executable과 argument slice로 실행한다. stdout/stderr는 분리하고 각각 상한을 적용한다. request cancellation과 timeout을 전달하고 `WaitDelay`로 pipe 대기를 제한한다. Windows에서는 child를 kill-on-close Job Object에 넣어 timeout/cancellation 시 descendant까지 종료한다. daemon start만 background daemon 생존을 위해 이 tree ownership을 명시적으로 제외한다. audit metadata에는 executable basename, args, duration, exit code, truncation 여부만 기록하며 환경 변수 전체를 남기지 않는다.

### repository registry

server가 canonical absolute path만 등록한다. 등록 시 `.git`을 확인하고 `git -C <path> rev-parse --show-toplevel` 결과가 같은 root인지 검증한다. canonical path identity는 Windows에서 case-insensitive, case-sensitive platform에서 exact spelling으로 중앙화한다. 등록된 path의 fresh inspection 결과와 startup 시 persisted path를 다시 load한 결과도 expected identity와 일치하는지 검증해 junction/symlink retarget으로 다른 Git root가 authorization되거나 Review target이 되는 것을 거부한다. Browser에는 backend가 생성한 opaque repository identity를 correlation key로 제공하며, mutation authorization에는 계속 canonical path만 사용한다. path를 받는 mutation뿐 아니라 global job ID와 panel UUID를 받는 read/mutation도 daemon의 metadata-only lookup으로 owning repository를 확인하고 registry 밖이면 거부한다. recent repository, last selected repository와 platform-aware identity별 마지막 panel preference만 `os.UserConfigDir()/roborev-dashboard/config.json`에 저장하며 credential은 저장하지 않는다. Add/Select/panel preference는 prospective state 저장이 성공한 뒤에만 in-memory 상태를 갱신한다. 저장된 panel은 authorization이 아니며 current effective config에서 다시 발견될 때만 사용한다.

### HTTP 보안

기본 bind는 `127.0.0.1`이다. mutation request는 exact Host, allowed Origin과 per-process CSRF token을 검증한다. request body에 repository path가 있더라도 registry에서 다시 resolve한다. generic command API는 없다.

### 데이터와 UI 상태

review 목록은 v0.64.0의 `/api/jobs?repo=...&omit_prompt=true`를 사용하며, 각 요청 완료 4초 후 다음 요청을 예약해 겹치는 poll을 만들지 않는다. polling이 가능한 selected repository는 첫 current-generation 목록 요청이 성공 또는 실패로 settle되기 전까지 empty state와 Review HEAD를 gate한다. repository 전환과 unmount는 generation을 무효화해 이전 응답과 오류가 새 상태를 덮거나 새 repository의 initial gate를 해제하지 못하게 한다. background refresh 오류는 반복 toast가 아니라 deduplicated inline 상태로 표시한다. v0.64.0은 이 flag에서도 queued/running prompt를 유지하므로 adapter가 상태와 무관하게 알려진 heavy content key를 제거한 뒤 browser로 전달한다. upstream projection이 없어 daemon→Dashboard 구간에서는 active prompt를 수신·검사해야 하는 비용이 남으며, 이는 v0.64 capability limitation이다. `list --json`은 full prompt와 diff를 포함하므로 주기 경로에서 사용하지 않는다. detail, prompt와 log는 별도 endpoint에서 사용자가 tab을 선택할 때만 읽는다. panel synthesis를 우선 표시하고 member jobs는 panel UUID로 lazy-load한다. mutation 성공 후 목록을 RoboRev source of truth에서 다시 fetch한다.

repository 전환 시 panel catalog도 별도 current-generation request로 읽는다. valid saved preference가 있으면 복원하고, named panel이 하나면 자동 선택하며, 여러 개면 사용자 선택 전까지 Review HEAD를 막고, named panel이 없을 때만 reserved `none`을 자동 선택한다. 사용자가 선택하거나 Review HEAD를 누를 때 backend는 fresh repository identity와 current TOML을 다시 resolve해 exact panel 또는 built-in `none`만 daemon JSON enqueue에 전달한다.

`GET /api/reviews/{id}/fix-prompt`는 job ownership과 current registered repository를 확인하고 `show --job --json`을 lazy-load한 뒤 요청한 single review, panel synthesis 또는 individual panel member의 top-level finding output과 최소 metadata만 typed decode한다. terminal F 결과에 한해 deterministic Korean template를 생성하며 LLM, `roborev fix`, mutation, close 또는 rerun을 실행하지 않는다. prompt, diff, log, comments, 다른 member의 aggregate와 config는 포함하지 않고 finding은 변형하지 않는다. target과 current HEAD가 다르면 stale warning을 넣으며 input/output 크기는 bounded하고 초과 시 truncate하지 않고 거부한다.

Dashboard의 `/api/runtime`은 repository inspection이나 review listing 없이 TTL 30초의 version detection과 v0.64.0의 작은 `/api/health`만 수행하며 UI가 10초마다 호출한다. 실제 `status --json`은 최근 job의 full prompt까지 포함할 수 있어 주기적 polling transport로 사용하지 않는다. panel member도 목록에 합치지 않고 panel tab을 열 때 `/api/jobs?panel_run=...&limit=0&omit_prompt=true`로 읽고 adapter에서 heavy field를 제거하며, nonterminal member가 있는 동안 4초마다 갱신한다. upstream v0.64.0에서 `limit=0`은 unlimited로 문서화되어 있다.

## 확장 원칙

각 RoboRev 기능은 service method, typed request, command builder, capability, HTTP route, confirmation 정책과 테스트를 함께 추가한다. CLI help에서 확인되지 않은 flag를 추측해 만들지 않는다. 중요한 변경은 이 문서와 coverage matrix를 함께 갱신한다.
