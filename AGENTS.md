# RoboRev Dashboard 개발 지침

이 문서는 Codex, Cursor 및 다른 개발 에이전트가 공통으로 따르는 agent-neutral 지침이다. 저장소 안의 모든 작업에 적용한다. 단, `Codex 멀티에이전트 운영 규칙` 절은 Codex에서만 적용한다.

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
- 과거 RoboRev job을 rerun해서 새 코드를 검증하지 않는다. 새로운 review job으로 검증한다.
- 단일 commit 변경이면 새로운 HEAD review로 검증할 수 있다.
- 하나의 checkpoint가 여러 code commit으로 구성되면 최종 quality gate를 마지막 HEAD commit 하나만 리뷰하지 않는다.
- 최종 gate는 첫 implementation commit부터 최신 code/fix commit까지의 누적 inclusive range를 새 review로 생성한다. `roborev review <FIRST_COMMIT> <LATEST_CODE_COMMIT> --panel <panel>`은 내부적으로 `FIRST^..LATEST` 누적 diff를 리뷰한다.
- material finding 수정 commit이 추가되면 최종 gate도 `FIRST_COMMIT` → 새로운 최신 code commit 전체 range를 다시 리뷰한다.
- 최종 보고에는 각 reviewer job의 review job ID, reviewer, target first SHA, target last SHA, verdict를 함께 기록한다.
- final code gate 결과를 기록하기 위한 docs-only commit이 생기더라도 그 docs commit의 HEAD review를 최종 code gate라고 부르지 않는다.
- review job ID를 기록하기 위한 docs-only commit을 만들기 위해 불필요하게 또 panel review를 돌리는 loop를 만들지 않는다. 최종 reviewer job ID는 가능한 repository 문서에 다시 commit하지 말고 작업 완료 보고에 기록한다.
- RoboRev quality gate를 실행했으면 synthesis만 보지 말고 모든 panel member 결과도 terminal까지 직접 확인한다.
- F verdict가 나오면 해당 output을 반드시 읽고 실제 코드와 upstream 계약으로 대조한다.
- latest quality gate에 unresolved valid High 또는 Medium finding이 하나라도 있으면 작업을 완료라고 선언하지 않는다.
- valid material finding은 수정 → tests → 새 commit → cumulative range re-review 순서로 닫는다.
- Low finding은 검증 뒤 작고 scope 안이면 같이 수정하고, defer하면 명시적 근거를 남긴다.
- latest cumulative gate가 clean일 때(해당 gate에서 제기된 valid High/Medium이 전부 해결되었을 때)만 완료할 수 있다.
- 이 completion 규칙은 Codex뿐 아니라 OpenCode, Cursor 및 모든 개발 agent에 동일하게 적용한다.
- 중요한 설계 변경 시 `docs/ARCHITECTURE.md`를 함께 갱신한다.
- Codex와 Cursor 사이의 handoff를 위해 `docs/HANDOFF.md`를 최신으로 유지한다.
- 저장소 안에 있는 사용자 변경은 보존하고, 무관한 파일을 commit에 포함하지 않는다.

## Codex 멀티에이전트 운영 규칙

이 절은 Codex에서만 적용한다. Cursor 및 다른 개발 에이전트는 위의 공통 프로젝트 지침을 따르되, 아래의 Codex 전용 역할명과 위임 규칙을 강제로 모방할 필요는 없다.

### 역할 분담

- 메인 에이전트는 coordinator 역할을 맡는다. 요구사항 해석, 분석, 설계, 작업 범위 결정, 모호성 해소, 복잡한 디버깅, cross-cutting 변경 판단 및 최종 의사결정을 담당한다.
- 복잡하거나 모호한 설계, 여러 계층에 걸친 변경, 중요한 기술 선택이 필요한 경우 `architect`에 분석과 설계를 위임한다.
- 요구사항, 설계 및 구현 방향이 충분히 확정된 작업은 실제 코드 수정을 `worker`에 우선 위임한다.
- `worker`는 명확히 정의된 기능 구현, 파일 수정, 단순 리팩터링, 이미 원인이 확인된 버그 수정, 테스트 작성 및 관련 검증 실행을 담당한다.
- 의미 있는 구현이 끝나면 비단순 변경은 `reviewer`를 최종 품질 게이트로 사용한다.
- `reviewer`가 material finding을 보고하면 해당 finding을 검증한 뒤 `worker`에 수정시키고 다시 `reviewer`로 검토한다. 중대한 문제가 남지 않을 때까지 이 루프를 반복한다.
- `worker`가 작업 중 새로운 아키텍처 결정, 모호한 제품 판단, 보안상 중요한 선택, 예상하지 못한 cross-cutting 영향 또는 복잡한 동시성/상태 문제를 발견하면 임의로 결정하지 말고 메인 에이전트에 반환한다.
- `architect`와 `reviewer`는 코드를 수정하지 않는다.

### 모델 및 비용 사용 원칙

- 실제 모델과 reasoning effort 설정의 source of truth는 `.codex/config.toml` 및 `.codex/agents/*.toml`이다. 이 문서에서 해당 값을 임의로 덮어쓰지 않는다.
- Sol 계열 메인 에이전트는 분석, 설계, 어려운 판단, 복잡한 디버깅 및 최종 검토처럼 높은 추론 품질이 필요한 작업에 집중한다.
- 이미 방향이 정해진 일상적인 구현 작업을 메인 에이전트가 직접 수행하지 말고 가능한 경우 `worker`에 위임한다.
- 단순한 한두 줄 수정이나 매우 작은 저위험 작업까지 무조건 subagent를 생성하지 않는다. 위임 오버헤드보다 이득이 있을 때 사용한다.
- 동일 파일 또는 강하게 겹치는 코드 영역을 여러 write-capable agent가 동시에 수정하지 않는다.
- 병렬화는 서로 독립적이고 충돌 가능성이 낮은 작업에만 사용한다.

### 위임 작업 작성 규칙

- 메인 에이전트는 `worker`에 넘기기 전에 작업을 작고 명확한 단위로 쪼갠다.
- 각 위임에는 목적, 허용된 범위, 관련 제약, 완료 조건 및 필요한 검증을 가능한 한 명시한다.
- 모든 subagent는 이 `AGENTS.md`의 프로젝트 목적, 아키텍처, 보안, UX, 개발 및 테스트 불변조건을 동일하게 준수해야 한다.
- `worker`는 완료 후 변경 파일, 수행한 검증, 실패하거나 생략한 검증, 남은 위험 또는 미해결 사항을 메인 에이전트에 보고한다.
- subagent는 요청된 범위를 자의적으로 넓히지 않는다.

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

