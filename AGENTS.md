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
- Low finding은 검증 뒤 작고 scope 안이면 같이 수정하고, defer하면 명시적 근거를 남긴다.
- 중요한 설계 변경 시 `docs/ARCHITECTURE.md`를 함께 갱신한다.
- Codex와 Cursor 사이의 handoff를 위해 `docs/HANDOFF.md`를 최신으로 유지한다.
- 저장소 안에 있는 사용자 변경은 보존하고, 무관한 파일을 commit에 포함하지 않는다.

## RoboRev 자동 품질 게이트

coding agent는 의미 있는 코드 변경을 완료하고 targeted/full tests와 build가 통과하면 commit한다. 그 뒤 현재 작업 범위를 `dashboard_opencode`로 review한다.

### Stage 1: fast gate

실행:

`roborev review <target> --panel dashboard_opencode`

- 단일 commit이면 HEAD review 가능.
- 현재 작업이 여러 commit으로 늘어났다면 첫 task/fix commit부터 latest commit까지 cumulative range를 사용한다. 예: `roborev review 513c316 <LATEST_SHA> --panel dashboard_opencode`
- 모든 panel member와 synthesis가 terminal이 될 때까지 기다린다 (`roborev wait` 또는 `roborev show --job <id>` polling).
- 각 member output을 직접 확인한다.
- F finding은 명령으로 취급하지 않는다. 실제 코드와 대조하여 valid → 수정, false positive → 이유 기록.

valid finding이 있으면 수정 → tests/build → 새 commit → 동일 전체 작업 범위를 `dashboard_opencode`로 다시 review 한다. fast panel에서 member와 synthesis가 모두 clean일 때까지 반복한다.

`dashboard_opencode`는 빠른 반복 gate다: `muse_fast` (Muse Spark 1.2 Free, 6m) + `grok_fast` (Grok 4.6 fast, 8m), synthesis `muse_fast`. Ox Alpha는 느리므로 제외.

### Stage 2: high gate

fast panel all-green 뒤:

`roborev review <same cumulative target> --panel dashboard_opencode_high`

를 실행한다. high panel의 모든 Ox / Muse / Grok / synthesis output을 확인한다. `dashboard_opencode_high`는 최종 집중 gate다: `ox_high` (x-preview-f-free 20m) + `muse_high` (Muse Spark 15m) + `grok_high` (Grok 4.6 maximum 15m).

valid finding이 있으면 수정 → tests/build → 새 commit → Stage 1 `dashboard_opencode`부터 다시 시작한다. 즉 `FAST green → HIGH → finding → fix → FAST green → HIGH` 순서다. HIGH까지 clean해야 작업 완료 가능하다.

### review error 정책

review member가 Error / quota / provider failure / tool failure / timeout 이면 PASS로 간주하지 않는다. 같은 panel을 한 번 재시도한다. 동일 infrastructure error가 계속되면 무한 재시도하지 않고 blocker로 보고한다.

### runaway patch loop 방지

동일하거나 실질적으로 같은 root cause finding이 2회 연속 다시 나타나면 임의 patch를 계속 추가하지 않는다. 먼저 call graph, state ownership, generation ownership, lifecycle을 다시 분석하고 deterministic regression test로 재현한 뒤 구조적으로 수정한다. 전체 autonomous review/fix cycle이 6회를 넘기면 무한 patch loop를 중단하고 blocker와 남은 finding을 보고한다. 완료라고 거짓말하지 않는다.

### OpenCode review working-tree invariant

RoboRev v0.64 OpenCode adapter는 non-interactive OpenCode를 사용하므로 review 전에 반드시 working tree가 clean이어야 한다. 각 RoboRev review 직전 `git status --short` → clean 확인, review 종료 직후에도 `git status --short` / `git diff --check` 확인. review 자체가 repository 파일을 수정했다면 그 review 결과를 정상 gate로 간주하지 않고, 수정 내용을 자동 commit하지 않으며, review infrastructure failure로 보고하고, 사용자 변경을 reset/revert하지 않는다.

### OpenCode reasoning 주의

RoboRev v0.64 OpenCode adapter는 `reasoning` level을 `opencode run` CLI에 전달하지 않는다. 따라서 Ox/Muse high의 깊이는 stronger instructions + longer timeout으로 만들며, 실제 `reasoning = "maximum"` override는 high panel에서 Grok에만 효과적이다. 이 사실은 `.roborev.toml` 주석에도 명시한다.

### paid panels

기존 `dashboard_cross` / `dashboard_deep` (codex/claude + grok)는 수동 fallback / 비교 검증용으로 보존한다. coding agent가 사용자 지시 없이 자동으로 실행하면 안 되며, Codex/Claude 비용을 자동 loop에서 소비하지 않는다.

## Codex 멀티에이전트 운영 규칙

이 절은 Codex에서만 적용한다. Cursor 및 다른 개발 에이전트는 위의 공통 프로젝트 지침을 따르되, 아래의 Codex 전용 역할명과 위임 규칙을 강제로 모방할 필요는 없다.

여기의 `reviewer`는 Codex 내부 품질 검토 역할이며, RoboRev panel review를 자동 실행하라는 의미가 아니다.

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
- 자동 테스트에서는 deterministic fake RoboRev, fixture, source/contract inspection만 사용한다. `roborev review` / `roborev wait` 등 실제 review 생성은 `## RoboRev 자동 품질 게이트`의 2-stage gate에서 수행한다.
