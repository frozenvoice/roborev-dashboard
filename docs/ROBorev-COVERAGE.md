# RoboRev v0.64.0 Coverage

조사일: 2026-08-17. 기준 binary: `C:\Users\froze\.roborev\bin\roborev.exe`, `roborev version --json` 결과 `v0.64.0`. 아래 표는 설치된 binary의 root/subcommand help와 해당 tag의 upstream source를 기준으로 한다. Dashboard는 런타임 version/capability를 다시 감지한다.

상태: **B** = Checkpoint B vertical slice, **P** = planned, **N** = local UI action 부적합/문서화만.

| Command / API | Dashboard feature | Transport | 상태 | 자동 테스트 | Notes |
|---|---|---|---|---|---|
| `version --json` | 설치/version 감지 | JSON CLI | B | 예 | binary override → PATH 순서 |
| `/api/health` | daemon health | version-gated daemon API | B | 예 | 작은 health payload만 polling |
| `status --json` | full daemon/queue diagnostics | JSON CLI | P | 아니요 | v0.64.0은 recent job prompt를 포함하므로 polling 금지 |
| `/api/jobs?repo=...&omit_prompt=true` | review 목록/filter | version-gated daemon API | B | 예 | active prompt도 adapter가 strip하는 4초 polling |
| `/api/jobs?id=...&omit_prompt=true` | job owning-repo authorization | version-gated daemon API | B | 예 | upstream v0.64 source와 query-sensitive oversized fixture로 검증 |
| `list --json` | full CLI job payload | JSON CLI | N | 아니요 | prompt/diff를 포함하므로 polling 금지 |
| `show --job --json` | review detail/metadata | JSON CLI | B | 예 | `job_id` identity 보존, 선택 시 lazy-load |
| `show --job --prompt` | prompt detail | CLI text | B | 예정 | 전용 endpoint, lazy-load |
| `log --raw` | raw log | CLI bytes | B | 예정 | bounded response, lazy-load |
| `review` + `/api/enqueue` | repository panel-aware Review HEAD | version-gated daemon API | B | 예 | current effective panel을 재검증하고 full SHA를 pin; CLI에 JSON receipt가 없어 API 사용 |
| global/local `[review.panels]` | repository panel discovery/selector | bounded typed read-only TOML fallback | B | 예 | platform no-follow handle + regular file/1 MiB limit; map union + local whole-entry override, malformed fail closed |
| Dashboard `panelSelections` | repository별 panel preference | Dashboard local JSON config | B | 예 | platform-aware repository identity key; preference는 authorization이 아님 |
| `/api/jobs?panel_run=...&omit_prompt=true` | panel member 상태 | version-gated daemon API | B | 예 | panel tab에서만 lazy-load |
| `show --job --json` subset | Copy/Open Fix Prompt | bounded JSON CLI + deterministic local template | B | 예 | terminal F single/synthesis/individual member; LLM/수정 실행 없음 |
| `/api/job/rerun` | Rerun same target/config | version-gated daemon API | B | 예 | frozen target; HEAD action과 분리 |
| `cancel <id>` | queued/running cancel | typed CLI | B | 예 | terminal job은 UI에서 숨김 |
| `comment --job <id> --message ... --commenter ...` | comment | typed CLI | B | 예 | 설치된 help로 확인; argument array, multiline 지원 |
| `close <id>` | close | typed CLI | B | 예 | Comment & Close 순서/부분 실패 보장 |
| `close <id> --reopen` | reopen | typed CLI | B | 예 | 독립 `reopen` command 없음 |
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
- 실제 v0.64.0 `/api/jobs` payload에서 `commit_subject`, `enqueued_at`, `started_at`, `finished_at`, `verdict: "P"|"F"`를 확인했고 HTTP fixture/UI test로 고정했다.
- `list --json`은 prompt와 diff를 포함한다. v0.64 daemon도 `omit_prompt=true`에서 queued/running prompt를 유지하므로 Dashboard adapter가 status와 무관하게 heavy fields를 제거한다.
- panel parent rerun은 `/api/job/rerun`에서 원본 member의 frozen target/config와 synthesis를 새 `panel_run_uuid`로 복제한다.
- v0.64.0은 panel enumeration JSON/API를 제공하지 않는다. Dashboard는 typed TOML로 global/local panel map을 병합하지만 linked worktree의 untracked main-checkout `.roborev.toml` 상속은 지원하지 않는다. local config가 없고 `.git`이 regular file인 ambiguous layout에서만 `--git-dir`과 `--git-common-dir` metadata를 요구하며, metadata resolution 실패는 global named panel을 숨기고 discovery/mutation을 fail closed한다. metadata가 성공해 actual linked worktree가 아니면 global catalog를 유지하고, `.git` directory/absent도 metadata 없이 global catalog를 유지한다.
- Fix Prompt는 요청한 job의 `show --job --json` top-level finding과 job metadata만 사용하고 original prompt, diff, log, 다른 member aggregate 및 config를 포함하지 않는다.
- `repo list --json`과 독립 `reopen` command는 없다. reopen은 `close --reopen`이다.
- `stream`은 JSONL이며 UI source of truth가 충분한지 검증 전까지 polling 보조 신호로만 계획한다.
