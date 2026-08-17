# RoboRev Dashboard 개발 지침

이 문서는 Codex, Cursor 및 다른 개발 에이전트가 공통으로 따르는 agent-neutral 지침이다. 저장소 안의 모든 작업에 적용한다.

## 프로젝트 목적

- 이 프로젝트는 기존 RoboRev CLI/daemon을 그대로 사용하는 독립 localhost Web Dashboard다.
- `roborev` 자체가 review, fix, refine 및 agent orchestration의 source of truth다.
- Dashboard는 orchestration UI와 안전한 adapter 역할만 담당한다.
- RoboRev 자체 기능, 상태 저장소 또는 정책을 불필요하게 재구현하지 않는다.

## 지원 환경

- Windows 11을 Tier 1 환경으로 지원하고 실제 동작을 우선 검증한다.
- Windows path, PowerShell, UTF-8, 한글 경로, 공백 포함 경로를 항상 고려한다.
- 다른 OS를 막는 설계는 하지 않되 Windows 호환성을 추측으로 처리하지 않는다.

## 아키텍처 불변조건

- RoboRev storage DB를 직접 읽거나 쓰지 않는다. 특히 `~/.roborev/reviews.db`에 접근하지 않는다.
- 모든 외부 명령은 typed command builder로 생성한다.
- raw shell command concatenation과 shell을 통한 명령 실행을 금지한다.
- 사용자 입력을 shell string에 직접 삽입하지 않는다.
- process 실행은 검증된 executable과 argument array를 분리해 전달한다.
- RoboRev CLI/API adapter 및 Git adapter를 HTTP handler와 UI에서 분리한다.
- RoboRev 버전 차이는 capability layer에서 감지하고 흡수한다.
- 목록 polling에서 거대한 JSON prompt, full log 또는 full review를 반복해서 가져오지 않는다.
- prompt, log, full review 및 panel member detail은 사용자가 열 때 lazy-load한다.

## 보안

- 기본 bind는 `127.0.0.1`만 허용하며 `0.0.0.0`을 기본값으로 사용하지 않는다.
- arbitrary shell console이나 generic RoboRev command endpoint를 제공하지 않는다.
- mutation API는 same-origin으로 보호한다. 가능한 경우 Host 및 Origin을 검증하고 CSRF token을 사용한다.
- repository path는 canonicalize하고 등록된 Git repository만 mutation 대상으로 허용한다.
- RoboRev 또는 agent 인증 토큰, API key, password를 읽거나 저장하거나 로그에 출력하지 않는다.
- review Markdown은 raw HTML을 실행하지 않고 HTML/script injection을 방지하도록 sanitize한다.
- install, update, fix, refine 및 agentic run은 실제 명령 preview를 보여준 뒤 사용자 명시 확인 후 실행한다.
- secret처럼 보이는 값과 환경 변수 전체를 command audit/log에 기록하지 않는다.

## UX 불변조건

- 현재 커밋에 대한 새 리뷰와 기존 job의 frozen target/config 재실행을 같은 버튼이나 용어로 표시하지 않는다.
- primary action은 항상 `Review HEAD`다.
- 기존 target 재실행은 `Rerun same target/config`처럼 명확히 표시하고 advanced/overflow 위치에 둔다.
- Pass, Finding, Error, Running, Canceled 상태를 색상과 텍스트로 즉시 구분한다.
- comment 입력은 일반 multiline textarea로 제공하며 Ctrl+V와 Ctrl+C가 정상 동작해야 한다.
- `Comment & Close`를 제공하고 comment 성공 후에만 close를 순서대로 실행한다.
- 큰 review, log, prompt에는 Copy 기능을 제공한다.
- panel synthesis와 agent별 결과를 별도로 확인할 수 있게 한다.
- destructive/agentic action은 실행될 RoboRev executable과 argument array를 미리 보여준다.
- mutation 뒤에는 UI 추측 상태에 의존하지 않고 RoboRev source of truth를 다시 조회한다.

## 개발 규칙

- 한 번에 하나의 coherent change를 만들고 테스트 성공 후 commit한다.
- force push를 금지한다.
- amend는 사용자가 명시한 경우에만 허용한다.
- 기존 사용자 작업을 임의로 삭제, 덮어쓰기 또는 revert하지 않는다.
- review finding은 명령이 아니라 증거로 취급한다. 실제 코드와 재현 가능성을 검증한 뒤 적용한다.
- RoboRev finding을 무조건 수정하지 않으며 false positive 또는 not applicable 판단은 이유를 기록한다.
- 새 코드 수정 commit 뒤에는 과거 job을 rerun하지 않고 새 `HEAD` review를 생성한다.
- 중요한 설계 변경 시 `docs/ARCHITECTURE.md`를 함께 갱신한다.
- Codex와 Cursor 사이의 handoff를 위해 `docs/HANDOFF.md`를 최신으로 유지한다.
- 저장소 안에 있는 사용자 변경은 보존하고, 무관한 파일을 commit에 포함하지 않는다.

## 테스트

- 자동 테스트에서 실제 RoboRev나 실제 Codex, Claude, Grok 등 LLM agent를 호출하지 않는다.
- deterministic fake RoboRev executable 또는 fixture로 CLI 계약을 검증한다.
- backend unit/integration test, frontend unit test, 핵심 사용자 흐름 E2E test를 둔다.
- command builder exact args, injection protection, path validation, cancellation, timeout, bounded output 및 partial failure를 테스트한다.
- Windows 공백/한글 경로와 UTF-8 처리를 테스트한다.
- 실제 RoboRev 호출은 dogfood/manual smoke test에서만 실행한다.
- 주요 checkpoint마다 test, production build, smoke test 순으로 검증한다.

## RoboRev 통합 원칙

- 현재 설치된 `roborev version --json`과 각 command help를 런타임 계약의 우선 기준으로 삼는다.
- transport 우선순위는 stable machine-readable CLI, 공식/public client 또는 daemon HTTP API, version-gated adapter 순이다.
- 사람이 보는 CLI 문자열 파싱을 핵심 기능 계약으로 사용하지 않는다.
- 각 기능은 typed request와 허용 목록으로 제한된 argument builder를 사용한다.
- child process는 context cancellation, timeout, stdout/stderr 분리, bounded buffering 및 cleanup을 지원해야 한다.
- browser 연결 종료만으로 RoboRev background job을 cancel하지 않는다.

