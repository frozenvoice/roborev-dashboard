# RoboRev v0.64.0 Coverage

조사일: 2026-08-20. 실제 Windows RoboRev v0.64.0 command/health response와 공식 `kenn-io/roborev` v0.64.0 source 및 GitHub release metadata를 교차 확인했다. 자동 테스트는 실제 RoboRev를 호출하지 않고 deterministic fixture를 사용하며, Dashboard는 런타임에 version/capability를 다시 감지한다.

상태: **B** = Checkpoint B vertical slice, **P** = planned, **N** = local UI action 부적합/문서화만.

| Command / API | Dashboard feature | Transport | 상태 | 자동 테스트 | Notes |
|---|---|---|---|---|---|
| pinned v0.64.0 GitHub release asset | missing RoboRev runtime 설치 | bounded HTTPS + SHA-256 + safe ZIP + no-replace publish | B | 예 | Windows amd64, explicit confirmation, immutable manifest; URL/version/hash/target을 browser가 선택하지 않음 |
| `version --json` | 설치/version 검증과 runtime rebind | JSON CLI | B | 예 | exact `name=roborev`, canonical v0.64.0 확인 뒤에만 generation binding 교체 |
| `/api/health` | daemon health | version-gated daemon API | B | 예 | bounded typed payload만 polling; required non-null string `$schema`와 known fields만 허용 |
| `status --json` | full daemon/queue diagnostics | JSON CLI | P | 아니요 | v0.64.0은 recent job prompt를 포함하므로 polling 금지 |
| `/api/jobs?repo=...&omit_prompt=true` | review 목록/filter | version-gated daemon API | B | 예 | active prompt도 adapter가 strip하는 4초 polling |
| `/api/jobs?id=...&omit_prompt=true` | job owning-repo authorization | version-gated daemon API | B | 예 | upstream v0.64 source와 query-sensitive oversized fixture로 검증 |
| `list --json` | full CLI job payload | JSON CLI | N | 아니요 | prompt/diff를 포함하므로 polling 금지 |
| `show --job --json` | review detail/metadata | JSON CLI | B | 예 | `job_id` identity 보존, 선택 시 lazy-load |
| `show --job --prompt` | prompt detail | CLI text | B | 예정 | 전용 endpoint, lazy-load |
| `log --raw` | raw log | CLI bytes | B | 예정 | bounded response, lazy-load |
| `review` + `/api/enqueue` | repository panel-aware Review HEAD | version-gated daemon API | B | 예 | current effective panel을 재검증하고 full SHA를 pin; CLI에 JSON receipt가 없어 API 사용 |
| `POST /api/panel-refines` | backend-authoritative panel review/fix loop 시작 | Dashboard process state machine + `/api/enqueue` | B | 예 | fresh pinned HEAD, clean-tree/identity/branch 검증, repository identity lock; browser lifecycle과 독립 |
| `GET /api/panel-refines`, `GET /api/panel-refines/{id}` | Panel Refine 상태/timeline 재접속 | Dashboard process state machine | B | 예 | serialized active-run polling; process restart resume 없음 |
| `POST /api/panel-refines/{id}/cancel` | Panel Refine 취소 | Dashboard state machine + best-effort daemon cancel/context cancellation | B | 예 | cancel 후 fresh inspection의 availability/HEAD/branch/dirty/error를 `cancelState`로 표시 |
| `fix --agent codex --quiet <synthesis-id>` | Panel Refine synthesis fix | typed CLI, v0.64.0 | B | 예 | Codex allowlist checkpoint; shell/finding text argv 없음; 새 commit/clean tree/HEAD 전진 재검증 |
| global/local `[review.panels]` | repository panel discovery/selector | bounded typed read-only TOML fallback | B | 예 | platform no-follow handle + regular file/1 MiB limit; map union + local whole-entry override, malformed fail closed |
| Dashboard `panelSelections` | repository별 panel preference | Dashboard local JSON config | B | 예 | platform-aware repository identity key; preference는 authorization이 아님 |
| `/api/jobs?panel_run=...&omit_prompt=true` | panel member 상태 | version-gated daemon API | B | 예 | panel tab에서만 lazy-load |
| `show --job --json` subset | Copy/Open Fix Prompt | bounded JSON CLI + deterministic local template | B | 예 | terminal F single/synthesis/individual member; LLM/수정 실행 없음 |
| `/api/job/rerun` | Rerun same target/config | version-gated daemon API | B | 예 | frozen target; HEAD action과 분리 |
| `cancel <id>` | queued/running cancel | typed CLI | B | 예 | terminal job은 UI에서 숨김 |
| `comment --job <id> --message ... --commenter ...` | comment | typed CLI | B | 예 | 설치된 help로 확인; argument array, multiline 지원 |
| `close <id>` | close | typed CLI | B | 예 | Comment & Close 순서/부분 실패 보장 |
| `close <id> --reopen` | reopen | typed CLI | B | 예 | 독립 `reopen` command 없음 |
| `stream --repo` | live JSONL events | bounded JSONL CLI + sanitized SSE relay | B | 예 | exact v0.64.0 only; daemon already-online/healthy preflight; `/api/events` explicit Origin은 exact scheme+host match를, Origin 없음은 정확히 한 번의 `Sec-Fetch-Site: same-origin`, `Sec-Fetch-Mode: cors`, `Sec-Fetch-Dest: empty`를 요구하며 누락·중복·wrong metadata와 foreign/null/malformed/blank/duplicate Origin은 403; repository identity별 source 1개와 0..N subscribers; bounded JSONL/SSE sanitization, fresh identity authorization과 shutdown cleanup; `Subscribe`는 opaque identity만 받고 stale/fallback path를 저장하지 않음; stream은 lossy refresh/invalidation hint이고 `/api/reviews` authoritative API가 actual state이며 4초 polling은 fallback/safety net |
| `pause`, `unpause` | queue control | daemon `POST /api/queue/pause`, `/api/queue/unpause` | B | 예 | daemon online에서만 실행; running job은 계속되고 새 queued job만 시작하지 않음 |
| `daemon start` | 설치 직후/기존 설치 daemon 시작 | typed CLI + bounded health retry | B | 예 | health precheck; 시작 실패 시 설치 보존, 재다운로드 없는 retry |
| `daemon stop/restart` | global lifecycle | typed CLI + process-scoped bounded health verification | B | 예 | backend exact argv preview; request disconnect와 분리; shutdown cancellation; transport 연결 불가만 offline으로 인정; restart는 stopped daemon도 start하는 upstream semantics 유지 |
| `daemon run` | foreground lifecycle | typed CLI | N/P | 아니요 | local UI에서 제외 |
| `check-agents --agent <canonical> --timeout 60` | 선택한 agent health | bounded v0.64 CLI text adapter | B | 예 | explicit confirmation 뒤에만 실행하는 quota-consuming manual check; 자동/page-load 실행 금지 |
| v0.64 command agent discovery | configured agent detection | bounded typed read-only TOML + isolated executable/identity probe | B | 예 | exact v0.64.0 gate; zero-quota; Detected와 Checked 분리; Cursor/Grok collision fail closed; secret/raw config 미노출 |
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
| `update --check` / `update` | update | typed CLI stream | P | 아니요 | v0.65+ adapter 검증 전 자동 update 금지; preview + confirmation 필요 |
| official remote PowerShell installer | missing setup | remote script | N | 예 | Dashboard는 `irm | iex`, execution-policy bypass와 latest install을 사용하지 않음 |
| `backfill-tokens` | maintenance | typed CLI | P | 아니요 | Advanced only |
| `ci` | CI operations | docs/settings | N/P | 아니요 | 로컬 실행 의미를 subcommand별 판단 |
| `completion`, `post-commit`, `quickstart`, `snooze`, `tui` | CLI UX/internal entry | docs only | N | 아니요 | Dashboard 버튼으로 복제하지 않음 |

## v0.64.0 capability notes

- Dashboard의 recommended policy는 전체 Review/Panel/Panel Refine 계약이 검증된 exact v0.64.0이다. Windows amd64 ZIP의 official asset name, 16,325,029-byte compressed size, SHA-256 `157b008086a2ad1c107fe4aa70f172275400dd508c4c9761db2d604f9e56e056`, root `roborev.exe`와 user-level target을 source manifest에 pin한다. remote metadata가 다른 digest를 제시해도 자동 신뢰하지 않는다.
- 이미 발견된 supported binary는 재설치하지 않는다. explicit override와 newer/unknown/broken/blocked binary는 보존하며 자동 downgrade/replace하지 않는다. 설치는 PATH, `roborev init`, hook, agent, credential과 Windows 보안 설정을 변경하지 않는다.
- setup은 same-origin/CSRF/Host 보호를 받는 typed strict-empty API다. download/hash/archive/publish/probe/rebind/daemon-health를 bounded 단계로 수행하며 integrity와 unsafe archive 오류는 fail closed한다. Application Control은 exact Windows policy error만 `blocked`로 분류하고 우회하지 않는다.
- 실제 v0.64.0 health payload는 `"$schema":"http://127.0.0.1:7373/HealthStatus.json"`, `healthy:true`, `uptime`, `version:"v0.64.0"`, `database`/`workers` components, `recent_errors:null`, `error_count_24h:0` shape로 확인했다. deterministic fixture가 이 shape를 고정하며 malformed/trailing/oversized/unhealthy/unknown-field와 Stream Relay daemon-version mismatch를 계속 fail closed한다.
- `review`는 `--json`을 제공하지 않는다. upstream `/api/enqueue` response는 single job 또는 panel synthesis와 `member_job_ids`를 반환한다.
- 실제 v0.64.0 `/api/jobs` payload에서 `commit_subject`, `enqueued_at`, `started_at`, `finished_at`, `verdict: "P"|"F"`, `panel_member_name`, `panel_member_index`를 확인했고 HTTP fixture/UI test로 고정했다. Panel member 목록은 `panel_run` metadata query로만 polling하며, 선택한 member의 실제 저장 output만 기존 repository-authorized `show --job-id <id> --json` detail 경로로 lazy-load한다.
- `list --json`은 prompt와 diff를 포함한다. v0.64 daemon도 `omit_prompt=true`에서 queued/running prompt를 유지하므로 Dashboard adapter가 status와 무관하게 heavy fields를 제거한다.
- panel parent rerun은 `/api/job/rerun`에서 원본 member의 frozen target/config와 synthesis를 새 `panel_run_uuid`로 복제한다.
- Panel Refine은 Rerun same target/config가 아니라 매 iteration fresh HEAD의 새 panel review를 enqueue한다. iteration panel PASS 뒤 명시된 final panel을 검증하며, final panel F도 같은 fix budget으로 수정한 뒤 iteration panel부터 다시 시작한다.
- Panel Refine receipt와 `/api/jobs` polling은 job ID, panel run UUID, repo path, full git ref, branch, panel, synthesis role 및 panel job type을 일치시켜야 한다. 알려진 running 상태만 계속 polling하고, unknown/malformed status 또는 P/F 없는 terminal은 fail closed한다.
- Panel Refine run은 Dashboard process-scoped다. browser reload/reopen은 active run에 재접속할 수 있지만 process restart 뒤 자동 resume하지 않는다. shutdown은 active run context와 child process를 취소한다.
- v0.64.0 `fix --agent codex --quiet <synthesis-id>`는 새 commit에 panel 없는 default review를 자동 enqueue할 수 있다. Dashboard의 선택 panel enqueue와 별도로 unrelated extra review가 생길 수 있으며, Dashboard는 이를 추측으로 억제하지 않는다.
- v0.64.0은 panel enumeration JSON/API를 제공하지 않는다. Dashboard는 typed TOML로 global/local panel map을 병합하지만 linked worktree의 untracked main-checkout `.roborev.toml` 상속은 지원하지 않는다. local config가 없고 `.git`이 regular file인 ambiguous layout에서만 `--git-dir`과 `--git-common-dir` metadata를 요구하며, metadata resolution 실패는 global named panel을 숨기고 discovery/mutation을 fail closed한다. metadata가 성공해 actual linked worktree가 아니면 global catalog를 유지하고, `.git` directory/absent도 metadata 없이 global catalog를 유지한다.
- Fix Prompt는 요청한 job의 `show --job --json` top-level finding과 job metadata만 사용하고 original prompt, diff, log, 다른 member aggregate 및 config를 포함하지 않는다.
- `repo list --json`과 독립 `reopen` command는 없다. reopen은 `close --reopen`이다.
- `stream --repo` checkpoint는 exact v0.64.0 capability로 구현했다. 시작 전에 installation/version/capability와 daemon already-online/healthy 상태를 확인하며 offline에서 daemon이나 stream process를 자동 시작하지 않는다. source는 repository identity별 하나만 유지하고 0..N SSE subscriber가 공유한다. stdout JSONL line은 1 MiB, stderr sink는 64 KiB로 bounded하며 malformed/unknown/oversized line, process exit, cancellation과 Dashboard shutdown을 안전하게 처리한다. recognized event는 raw payload를 버리고 `jobs-changed`와 monotonic sequence만 SSE로 전달한다.
- `stream`은 **lossy refresh/invalidation hint**이며 UI source of truth가 아니다. frontend는 event burst를 coalesce/debounce한 뒤 `/api/reviews` **authoritative API = actual state**를 다시 요청하고, repository epoch로 stale event/late response를 거부한다. EventSource reconnect를 지원하지만 **4초 polling = fallback/safety net**을 유지한다. 자동 테스트는 `internal/process/stream_test.go`, `internal/roborev/stream_test.go`, `internal/streamrelay/manager_test.go`, `internal/app/server_events_test.go`에서 deterministic fake process/service만 사용하며 실제 RoboRev/daemon/LLM은 실행하지 않는다.
- `/api/events`의 browser authorization은 mutation과 분리된다. explicit Origin은 정확히 한 개의 same-origin 값만 허용하고 Origin이 없으면 세 Fetch Metadata 값을 exact하게 모두 요구한다. mutation은 기존 absent-Origin 허용 및 CSRF 검증을 유지한다. authorization 실패는 query parsing, Registry.InspectIdentity, `Subscribe`, stream process start 전에 종료된다.
