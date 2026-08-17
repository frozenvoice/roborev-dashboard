# Handoff

마지막 갱신: 2026-08-17

## 현재 상태

- 새 Git 저장소를 `main` branch로 초기화했다.
- 설치된 RoboRev v0.64.0의 command surface와 upstream v0.64.0 source를 조사했다.
- `AGENTS.md`, review 지침, architecture와 capability matrix를 작성했다.
- Windows에 Go 1.26.5를 설치했다. 현재 shell PATH 갱신 전에는 `C:\Program Files\Go\bin\go.exe`를 사용할 수 있다.
- application scaffold와 vertical slice 구현은 다음 단계다.

## 결정

- production은 React assets를 Go binary에 embed한 단일 exe다.
- 읽기는 stable JSON CLI 우선, review enqueue와 frozen rerun은 v0.64.0 daemon HTTP API를 version-gated로 사용한다.
- repository registry는 canonical Git root만 허용한다.
- 초기 live 상태는 4초 polling이며 JSONL stream relay는 후속 checkpoint다.
- `roborev init`은 post-commit hook과 daemon start를 함께 수행하므로 자동 호출하지 않는다. `.roborev.toml`을 직접 관리하고 manual panel review를 사용한다.

## 다음 작업

1. Go/React scaffold 및 fake RoboRev fixture
2. repository/bootstrap/version/status API
3. reviews/list/detail/prompt/log 및 mutation vertical slice
4. frontend unit + backend integration + smoke test
5. Checkpoint A/B commit 후 각각 새 HEAD `dashboard_cross` review

