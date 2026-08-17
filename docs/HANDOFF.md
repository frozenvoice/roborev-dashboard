# Handoff

마지막 갱신: 2026-08-17

## 현재 상태

- 새 Git 저장소를 `main` branch로 초기화했다.
- 설치된 RoboRev v0.64.0의 command surface와 upstream v0.64.0 source를 조사했다.
- `AGENTS.md`, review 지침, architecture와 capability matrix를 작성했다.
- Windows에 Go 1.26.5를 설치했다. 현재 shell PATH 갱신 전에는 `C:\Program Files\Go\bin\go.exe`를 사용할 수 있다.
- Checkpoint A/B vertical slice와 후속 hardening을 `fb88aa9`, `cff6d45`, `d3ed6b2`, `666086c`, `ea1174b`로 commit했다.
- RoboRev panel jobs 13, 17, 21, 25, 29, 33, 37, 41을 완료하고 finding을 재현·분류했다. 실제 수정과 false-positive 근거는 `docs/REVIEW-FINDINGS.md`에 있다.
- 현재 작업 트리는 Job 49의 platform-aware repository identity, transactional registry persistence, serialized polling, fresh Review HEAD receipt metadata, background error dedupe, daemon mutation 계약 테스트와 PowerShell native failure propagation을 반영한다.
- Go 전체 테스트와 React/Vitest 14 tests, Vite production build 및 Windows EXE build가 통과했다.
- `666086c` clean archive에서 `go build ./...`가 통과해 embed placeholder를 검증했다.
- `666086c`의 새 panel job 25를 완료했다. active-job prompt stripping, verified `id=` lookup, suspended Job Object attach, selected polling reconciliation, version TTL, poll gating, panel handler coverage와 deterministic registry fallback을 후속 작업 트리에 반영했다.
- 후속 React/Vitest 9 tests, Vite production build와 roborev/app/process backend suites가 통과했다. Windows Application Control의 일시 차단 해제 후 최신 9.9MB EXE를 다시 실행했고 root/runtime/reviews smoke와 browser job 29 목록을 확인했다. 실제 list response는 7.3KB이며 `prompt`, `diff_content`, `output`, `review`가 없다.

## 결정

- production은 React assets를 Go binary에 embed한 단일 exe다.
- detail/prompt/log는 stable CLI를 사용한다. prompt/diff 없는 목록과 job ownership lookup, review enqueue, frozen rerun, panel members, health는 v0.64.0 daemon HTTP API를 version-gated로 사용한다.
- repository registry는 canonical Git root만 허용한다.
- review list는 각 요청 완료 4초 뒤, 작은 `/api/runtime` health 상태는 각 요청 완료 10초 뒤 다음 poll을 예약한다. `/api/runtime`은 repository inspect/list를 수행하지 않는다. `status --json`과 `list --json`은 full prompt/diff를 포함할 수 있어 polling에 사용하지 않는다.
- `roborev init`은 post-commit hook과 daemon start를 함께 수행하므로 자동 호출하지 않는다. `.roborev.toml`을 직접 관리하고 manual panel review를 사용한다.

## 다음 작업

1. Job 49 finding 수정 commit을 새 HEAD `dashboard_cross` review 대상으로 사용
2. Checkpoint C: installer/update/daemon lifecycle/check-agents/hooks/config
3. Checkpoint D 이후: fix/refine/analyze/run 등 agentic workflow와 stream relay
