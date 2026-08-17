# Handoff

마지막 갱신: 2026-08-17

## 현재 상태

- 새 Git 저장소를 `main` branch로 초기화했다.
- 설치된 RoboRev v0.64.0의 command surface와 upstream v0.64.0 source를 조사했다.
- `AGENTS.md`, review 지침, architecture와 capability matrix를 작성했다.
- Windows에 Go 1.26.5를 설치했다. 현재 shell PATH 갱신 전에는 `C:\Program Files\Go\bin\go.exe`를 사용할 수 있다.
- Checkpoint A/B vertical slice와 후속 hardening을 `fb88aa9`, `cff6d45`, `d3ed6b2`로 commit했다.
- RoboRev panel jobs 13, 17, 21을 완료하고 finding을 재현·분류했다. 실제 수정과 false-positive 근거는 `docs/REVIEW-FINDINGS.md`에 있다.
- 현재 작업 트리는 job authorization, Windows Job Object cleanup, clean-checkout embed placeholder, registry persistence, lightweight runtime polling, panel-member refresh/race/error, real payload fixture와 capability gating을 반영한다.
- Go 전체 테스트와 React/Vitest 8 tests, Vite production build, embedded EXE HTTP/browser smoke가 통과했다. 실제 job 21 detail에서 commit title/Finding/20s와 Codex·Claude·Grok member 상태를 확인했다.
- 최종 hardening commit 뒤 clean-checkout compile 검증과 새 HEAD panel review를 수행한다.
- `666086c`의 새 panel job 25를 완료했다. active-job prompt stripping, verified `id=` lookup, suspended Job Object attach, selected polling reconciliation, version TTL, poll gating, panel handler coverage와 deterministic registry fallback을 후속 작업 트리에 반영했다.
- 후속 React/Vitest 9 tests와 Vite production build가 통과했다. Go packages/test binaries는 모두 컴파일됐고 app/process suites는 explicit test EXE로 통과했다. 실행 도중 Windows Application Control이 새 unsigned Go binary hash를 차단하기 시작해 roborev suite와 최신 EXE 재실행은 정책상 차단됐다. 차단 전 `666086c` build의 전체 Go tests/EXE browser smoke는 통과했다.

## 결정

- production은 React assets를 Go binary에 embed한 단일 exe다.
- detail/prompt/log는 stable CLI를 사용한다. prompt/diff 없는 목록과 job ownership lookup, review enqueue, frozen rerun, panel members, health는 v0.64.0 daemon HTTP API를 version-gated로 사용한다.
- repository registry는 canonical Git root만 허용한다.
- review list는 4초, 작은 `/api/runtime` health 상태는 10초 polling이다. `/api/runtime`은 repository inspect/list를 수행하지 않는다. `status --json`과 `list --json`은 full prompt/diff를 포함할 수 있어 polling에 사용하지 않는다.
- `roborev init`은 post-commit hook과 daemon start를 함께 수행하므로 자동 호출하지 않는다. `.roborev.toml`을 직접 관리하고 manual panel review를 사용한다.

## 다음 작업

1. 현재 hardening 변경을 commit하고 새 HEAD `dashboard_cross` review 결과를 평가
2. Checkpoint C: installer/update/daemon lifecycle/check-agents/hooks/config
3. Checkpoint D 이후: fix/refine/analyze/run 등 agentic workflow와 stream relay
