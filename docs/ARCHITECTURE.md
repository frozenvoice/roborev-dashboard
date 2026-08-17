# Architecture

## 목표와 경계

Dashboard는 React SPA와 Go localhost server로 구성된다. Go server가 repository registry, Git adapter, RoboRev capability layer, typed command builder와 bounded process runner를 소유한다. Browser는 RoboRev daemon이나 OS process를 직접 호출하지 않는다.

```text
Browser (React)
  -> same-origin JSON API + CSRF
Go localhost server
  -> Repository registry -> Git executable + args
  -> RoboRev service
       -> stable JSON CLI (version/status/list/show/summary/cost)
       -> version-gated daemon HTTP API (enqueue/rerun where CLI has no JSON receipt)
       -> typed CLI mutations (comment/close/cancel)
RoboRev daemon -> RoboRev-owned storage
```

## 주요 선택

### 단일 실행 파일

Vite production output을 `internal/webui/dist`에 생성하고 Go `embed`로 포함한다. 최종 사용자는 Node나 Go 없이 `roborev-dashboard.exe` 하나만 실행한다. 개발 스크립트는 frontend build 후 Go build 순서를 보장한다.

### transport

stable JSON CLI를 우선한다. v0.64.0의 `review` 명령에는 JSON launch receipt가 없으므로 새 review는 감지된 daemon endpoint의 public `/api/enqueue` 계약을 version capability로 캡슐화한다. Dashboard가 먼저 Git으로 `HEAD`를 full SHA로 고정하여 전송하므로 클릭 시점의 target이 audit 가능하다.

`/api/job/rerun`은 v0.64.0 upstream에서 기존 frozen target과 resolved agent/panel config를 복제한다. 이 기능은 `Rerun same target/config`로만 노출하고 새 HEAD review와 request type, endpoint 및 UI 위치를 공유하지 않는다.

v0.64.0의 `repo list`에는 JSON이 없다. 초기 vertical slice는 Dashboard registry의 canonical Git repositories를 사용한다. human-readable `repo list` parsing은 core path에 넣지 않으며 향후 official JSON/API capability가 있을 때 추가한다.

### process 실행

외부 process는 shell 없이 executable과 argument slice로 실행한다. stdout/stderr는 분리하고 각각 상한을 적용한다. request context cancellation과 timeout을 전달하며 Windows에서는 process group/job cleanup을 별도 구현 지점으로 둔다. audit metadata에는 executable basename, args, duration, exit code, truncation 여부만 기록하며 환경 변수 전체를 남기지 않는다.

### repository registry

server가 canonical absolute path만 등록한다. 등록 시 `.git`을 확인하고 `git -C <path> rev-parse --show-toplevel` 결과가 같은 root인지 검증한다. mutation API는 registry에 없는 path를 거부한다. recent repository와 last selection만 `os.UserConfigDir()/roborev-dashboard/config.json`에 저장하며 credential은 저장하지 않는다.

### HTTP 보안

기본 bind는 `127.0.0.1`이다. mutation request는 exact Host, allowed Origin과 per-process CSRF token을 검증한다. request body에 repository path가 있더라도 registry에서 다시 resolve한다. generic command API는 없다.

### 데이터와 UI 상태

review 목록은 가벼운 `roborev list --json` 결과를 polling한다. detail, prompt와 log는 별도 endpoint에서 사용자가 tab을 선택할 때만 읽는다. panel synthesis를 우선 표시하고 member jobs는 parent metadata로 묶는다. mutation 성공 후 목록/detail을 다시 fetch한다.

## 확장 원칙

각 RoboRev 기능은 service method, typed request, command builder, capability, HTTP route, confirmation 정책과 테스트를 함께 추가한다. CLI help에서 확인되지 않은 flag를 추측해 만들지 않는다. 중요한 변경은 이 문서와 coverage matrix를 함께 갱신한다.

