# RoboRev v0.64.0 Coverage

조사일: 2026-08-17. 기준 binary: `C:\Users\froze\.roborev\bin\roborev.exe`, `roborev version --json` 결과 `v0.64.0`. 아래 표는 설치된 binary의 root/subcommand help와 해당 tag의 upstream source를 기준으로 한다. Dashboard는 런타임 version/capability를 다시 감지한다.

상태: **B** = Checkpoint B vertical slice, **P** = planned, **N** = local UI action 부적합/문서화만.

| Command / API | Dashboard feature | Transport | 상태 | 자동 테스트 | Notes |
|---|---|---|---|---|---|
| `version --json` | 설치/version 감지 | JSON CLI | B | 예정 | binary override → PATH 순서 |
| `status --json` | daemon health/queue | JSON CLI | B | 예정 | running false도 정상 상태 |
| `list --json` | review 목록/filter | JSON CLI | B | 예정 | prompt/log 미포함 |
| `show --job --json` | review detail/metadata | JSON CLI | B | 예정 | 선택 시 lazy-load |
| `show --job --prompt` | prompt detail | CLI text | B | 예정 | 전용 endpoint, lazy-load |
| `log --raw` | raw log | CLI bytes | B | 예정 | bounded response, lazy-load |
| `review` + `/api/enqueue` | Review HEAD/advanced enqueue | version-gated daemon API | B | 예정 | CLI에 JSON receipt가 없어 API 사용 |
| `/api/job/rerun` | Rerun same target/config | version-gated daemon API | B/P | 예정 | frozen target; HEAD action과 분리 |
| `cancel <id>` | queued/running cancel | typed CLI | B | 예정 | terminal job은 UI에서 숨김 |
| `comment --job <id> -m` | comment | typed CLI | B | 예정 | argument array, multiline 지원 |
| `close <id>` | close | typed CLI | B | 예정 | Comment & Close 순서 보장 |
| `close <id> --reopen` | reopen | typed CLI | B | 예정 | 독립 `reopen` command 없음 |
| `stream --repo` | live JSONL events | JSONL CLI/SSE relay | P | 아니요 | 초기에는 4초 polling |
| `pause`, `unpause` | queue control | typed CLI | P | 아니요 | running job 정지로 표현 금지 |
| `daemon start/stop/restart/run` | lifecycle | typed CLI | P | 아니요 | run은 local UI에서 제외 가능 |
| `check-agents` | agent health | CLI text | P | 아니요 | 현재 JSON flag 없음 |
| `fix` | preview/full fix | typed CLI stream | P | 아니요 | confirmation 필수 |
| `refine` | iterative refine | typed CLI stream | P | 아니요 | unsafe 기본 OFF, confirmation 필수 |
| `compact` | dry-run/background/wait | typed CLI stream | P | 아니요 | same-branch concurrency 경고 |
| `analyze --list` | analysis type discovery | CLI text | P | 아니요 | v0.64.0은 `--json`과 함께여도 text list |
| `analyze <type> --json` | analysis enqueue | JSON CLI | P | 아니요 | `--fix`는 confirmation 필수 |
| `run --json` | custom task receipt | JSON CLI | P | 아니요 | agentic/yolo 기본 OFF |
| `config list --show-origin` | effective/origin | CLI tabular text | P | 아니요 | version-gated parser 필요 |
| `config get/set` | common settings | typed CLI | P | 아니요 | unknown key 보존 |
| `repo list/show/rename/move/delete/merge` | RoboRev repo management | CLI text/API 검토 | P | 아니요 | `repo list --json` 미지원 |
| `summary --json` | stats | JSON CLI | P | 아니요 | repo/branch/since |
| `cost --json` | approximate cost | JSON CLI | P | 아니요 | 실제 청구액이 아닌 lower bound |
| `insights --json` | guideline insights | JSON CLI | P | 아니요 | LLM-powered, 명시 실행 |
| `agent-hook install/status/reset/daemon/dump/run` | agent hooks | typed CLI/JSON 일부 | P | 아니요 | 실제 지원 agent만 표시 |
| `skills install/update` | skill 관리 | typed CLI | P | 아니요 | 명시 실행 |
| `install-hook`, `uninstall-hook` | Git hook 관리 | typed CLI | P | 아니요 | post-commit 중복 review 방지 |
| `init`, `init gh-action` | 초기화/CI 생성 | typed CLI | P | 아니요 | 자동 hook 설치 주의 |
| `export reviews`, `export ci-metrics` | export | JSON/file stream | P | 아니요 | download endpoint 예정 |
| `sync status/now` | PostgreSQL sync | typed CLI | P | 아니요 | local feature만 |
| `wait` | existing jobs wait | typed CLI | P | 아니요 | enqueue와 분리 |
| `log clean` | old log cleanup | typed CLI | P | 아니요 | destructive 확인 |
| `update --check` / `update` | update | typed CLI stream | P | 아니요 | preview + confirmation |
| official PowerShell installer | missing setup | fixed installer command | P | 아니요 | source/command 확인 후만 실행 |
| `backfill-tokens` | maintenance | typed CLI | P | 아니요 | Advanced only |
| `ci` | CI operations | docs/settings | N/P | 아니요 | 로컬 실행 의미를 subcommand별 판단 |
| `completion`, `post-commit`, `quickstart`, `snooze`, `tui` | CLI UX/internal entry | docs only | N | 아니요 | Dashboard 버튼으로 복제하지 않음 |

## v0.64.0 capability notes

- `review`는 `--json`을 제공하지 않는다. upstream `/api/enqueue` response는 single job 또는 panel synthesis와 `member_job_ids`를 반환한다.
- panel parent rerun은 `/api/job/rerun`에서 원본 member의 frozen target/config와 synthesis를 새 `panel_run_uuid`로 복제한다.
- `repo list --json`과 독립 `reopen` command는 없다. reopen은 `close --reopen`이다.
- `stream`은 JSONL이며 UI source of truth가 충분한지 검증 전까지 polling 보조 신호로만 계획한다.

