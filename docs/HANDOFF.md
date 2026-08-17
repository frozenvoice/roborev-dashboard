# Handoff

마지막 갱신: 2026-08-17

## 현재 상태

- 새 Git 저장소를 `main` branch로 초기화했다.
- 설치된 RoboRev v0.64.0의 command surface와 upstream v0.64.0 source를 조사했다.
- `AGENTS.md`, review 지침, architecture와 capability matrix를 작성했다.
- Windows에 Go 1.26.5를 설치했다. 현재 shell PATH 갱신 전에는 `C:\Program Files\Go\bin\go.exe`를 사용할 수 있다.
- Checkpoint A/B vertical slice를 `fb88aa9`로 commit했다.
- Go backend tests, React/Vitest 4 tests, Vite build와 embedded exe HTTP/browser smoke가 통과했다.
- 실제 `dashboard_cross` panel review job 13을 `fb88aa9`에 enqueue했고 현재 진행 중이다.

## 결정

- production은 React assets를 Go binary에 embed한 단일 exe다.
- 읽기는 stable JSON CLI 우선, review enqueue/frozen rerun/panel members/health는 v0.64.0 daemon HTTP API를 version-gated로 사용한다.
- repository registry는 canonical Git root만 허용한다.
- review list는 4초, 작은 daemon health/bootstrap은 10초 polling이며 JSONL stream relay는 후속 checkpoint다. `status --json`은 full prompt를 포함할 수 있어 polling에 사용하지 않는다.
- `roborev init`은 post-commit hook과 daemon start를 함께 수행하므로 자동 호출하지 않는다. `.roborev.toml`을 직접 관리하고 manual panel review를 사용한다.

## 다음 작업

1. job 13 synthesis 결과와 actionable finding 평가
2. 현재 SPA root/status refresh/panel-member 보강 변경 commit
3. 새 commit에 새 HEAD `dashboard_cross` review
4. installer/update/daemon/check-agents/hooks/config Checkpoint C
