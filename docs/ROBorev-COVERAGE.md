# RoboRev v0.64.0 Coverage

조사일: 2026-08-20. 실제 Windows RoboRev v0.64.0 command/health response와 공식 `kenn-io/roborev` v0.64.0 source 및 GitHub release metadata를 교차 확인했다. 자동 테스트는 실제 RoboRev를 호출하지 않고 deterministic fixture를 사용하며, Dashboard는 런타임에 version/capability를 다시 감지한다.

상태: **B** = Checkpoint B vertical slice, **P** = planned, **N** = local UI action 부적합/문서화만.

| Command / API | Dashboard feature | Transport | 상태 | 자동 테스트 | Notes |
|---|---|---|---|---|---|
| pinned v0.64.0 GitHub release asset | missing RoboRev runtime 설치 | bounded HTTPS + SHA-256 + safe ZIP + no-replace publish | B | 예 | Windows amd64, explicit confirmation, immutable manifest; URL/version/hash/target을 browser가 선택하지 않음 |
| `version --json` | 설치/version 검증과 runtime rebind | JSON CLI | B | 예 | exact `name=roborev`, canonical v0.64.0 확인 뒤에만 generation binding 교체 |
| `/api/health` | daemon health | version-gated daemon API | B | 예 | bounded typed payload만 polling; optional `$schema`는 missing/임의 string을 허용하고 present null/non-string을 거부하며 known fields만 허용 |
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
| `/api/job/rerun` | Rerun same target/config | version-gated daemon API | B | 예 | frozen target; HEAD action과 분리; `job_type=task` 또는 `git_ref=run`은 adapter/UI에서 거부·숨김하고 `phase=task-rerun-unsupported` 반환; Tasks v1 rerun 미지원 |
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
| `fix --agent codex --quiet <job-id>` | General Fix: terminal F single review 또는 panel synthesis parent | typed CLI + Dashboard process-scoped manager | B | 예 | positive decimal ID만 positional target으로 사용; backend preview/confirmation과 fresh authorization을 거치며 exact argv는 `fix --agent codex --quiet <job-id>`; panel member·PASS·running·closed job은 대상에서 제외; 실제 fix 실행은 이 checkpoint에서 하지 않음 |
| `GET /api/fixes/preview`, `POST/GET /api/fixes`, `GET /api/fixes/{id}`, `POST /api/fixes/{id}/cancel` | General Fix preview/confirm, status, cancel | Dashboard async state machine + typed CLI | B | 예 | selected registered opaque identity, done/F/`closed=false`/`skipped=false`, current branch+HEAD equality, clean tree, shared global/repository gate, bounded status/error, post-run same identity/branch + advanced HEAD + clean tree, authoritative refresh; process restart resume 없음 |
| `refine` | iterative refine | typed CLI stream | P | 아니요 | unsafe 기본 OFF, confirmation 필수 |
| `analyze --list` | analysis type discovery | CLI text | B | 예 | exact v0.64.0 capability; bounded strict parser가 header, safe slug, description, duplicate와 size/count를 검증하며 `--json`을 사용하지 않음 |
| `analyze <type> --show-prompt` | selected analysis prompt template | CLI text | B | 예 | discovered type allowlist 기준 lazy read-only lookup; RoboRev prompt 원문을 번역/재작성하지 않음 |
| `analyze <type> --json` | confirmed analysis enqueue | JSON CLI | P | 아니요 | v0.64 upstream enqueue는 `Agentic: true`/worker `WithAgentic(true)`인 Edit·Write·Bash 위험 작업이므로 Dashboard execution disabled; future explicit safe capability/confirmation contract 필요 |
| `analyze --fix` | analysis 후 자동 수정 | JSON/agentic CLI | P | 아니요 | 이번 checkpoint에서 UI·request·argv 모두 미지원 |
| `analyze --per-file` | per-file analysis fan-out | JSON CLI | P | 아니요 | v0.64 agentic execution disabled; no fan-out request or argv |
| analysis arbitrary path/file/glob selection | 사용자 지정 분석 대상 | CLI argument surface | P | 아니요 | v0.64 agentic execution disabled; no user target/path/ref/base-branch input |
| Tasks history (`job_type=task`, `git_ref=run`) | 기존 custom task 기록/결과 보기 | `/api/reviews` + SSE/4s fallback + ReviewDetail | B | 예 | execution과 무관하게 유지; repository-scoped filter, status 표시, lazy 결과 열기 |
| `run --json` | Custom Task enqueue | JSON CLI | P/disabled | 아니요 | v0.64 daemon은 effective safety attestation을 제공하지 않으므로 Dashboard 실행 fail-closed; `POST /api/tasks/preview`와 CSRF 이후 `POST /api/tasks`는 service/process 0의 `501 phase=task-execution-disabled` tombstone |
| `POST /api/tasks/preview` | Tasks safety preview | Dashboard JSON API | P/disabled | 아니요(501 회귀 테스트) | execution disable로 preview도 tombstone; adapter/preflight/registry 호출 없음 |
| `POST /api/tasks` | Tasks confirmed enqueue | Dashboard JSON API + typed CLI | P/disabled | 예 | same-origin/CSRF rejection은 기존 계약대로 먼저 적용; 인가된 요청도 `task-execution-disabled`; RunTask/process 호출 0 |
| `/api/reviews/{id}/rerun` for task jobs | generic frozen rerun 우회 차단 | version-gated daemon API 거부 계층 | B | 예 | `job_type=task` 또는 `git_ref=run`은 `phase=task-rerun-unsupported` 반환; execution-disabled policy의 rerun bypass 차단 |
| `run --agentic` | agentic custom task mode | JSON CLI | P | 아니요 | Dashboard exposes no task execution at all |
| `run --yolo` | unsafe/yolo custom task mode | JSON CLI | P | 아니요 | Dashboard exposes no task execution at all |
| `run --wait` | wait-for-task completion mode | CLI stream | P | 아니요 | enqueue와 completion waiting은 분리된 future contract |
| custom task agent | arbitrary/custom agent selection | JSON CLI | P | 아니요 | Tasks v1은 `codex`만 허용; custom agent argv와 agent-specific execution contract는 미지원 |
| custom task model | custom model override | JSON CLI | P | 아니요 | model 입력과 argv는 Dashboard가 받거나 저장하지 않음 |
| custom task reasoning | custom reasoning override | JSON CLI | P | 아니요 | reasoning 입력과 argv는 Dashboard가 받거나 저장하지 않음 |
| `compact` | dry-run/background compaction | typed CLI stream | P | 아니요 | compact execution은 미지원; same-branch concurrency 경고는 별도 checkpoint |
| `config list --show-origin` | Configuration effective/origin table | version-gated bounded CLI text + fail-closed typed parser | B | 예 | exact v0.64.0 gate; origin allowlist(global/local/default), duplicate/unknown key reject, row/output bound, control char reject; sensitive rows는 upstream masked text 그대로 표시만 |
| `config get <key> --global/--local` | safe allowlisted post-write verification | version-gated bounded CLI text | B | 예 | editable allowlist 8개 key의 set 검증 범위에서만 사용; "is not set in"은 빈 값 처리; arbitrary key get endpoint 없음 |
| `config set <key> <value> --global/--local` | Configuration v1 editable allowlist | version-gated typed CLI + single-use preview token + exclusive operation gate | B | 예 | max_workers(global,1..32), review_context_count(0..20), auto_close_passing_reviews, review/refine_reasoning enum, review/fix/refine_min_severity enum; preview→fresh digest/HEAD/branch/dirty/binary 재검증→set→scoped get verification→fresh list refresh; 실패는 unverified 보고(단정 금지); local dirty fail closed |
| raw/unknown/sensitive config editing | `config set` outside allowlist (allow_unsafe_agents, disable_codex_sandbox, server_addr, *_cmd, credentials, panels/subagents/hooks 등) | 없음 — Dashboard가 key/type/scope/validation을 소유하는 backend allowlist 밖 | N | 아니요 | effective table에는 masked 표시될 수 있으나 mutation surface에서 영구 제외 |
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
- 실제 v0.64.0 health payload는 `healthy:true`, `uptime`, `version:"v0.64.0"`, `database`/`workers` components, `recent_errors:null`, `error_count_24h:0` shape로 확인했다. deterministic fixture가 bounded/malformed/trailing/oversized/unhealthy/unknown-field를 고정하며 `$schema`는 missing/임의 string을 허용하고 null/non-string을 거부한다. Stream Relay daemon-version mismatch도 계속 fail closed한다.
- `review`는 `--json`을 제공하지 않는다. upstream `/api/enqueue` response는 single job 또는 panel synthesis와 `member_job_ids`를 반환한다.
- 실제 v0.64.0 `/api/jobs` payload에서 `commit_subject`, `enqueued_at`, `started_at`, `finished_at`, `verdict: "P"|"F"`, `panel_member_name`, `panel_member_index`를 확인했고 HTTP fixture/UI test로 고정했다. Panel member 목록은 `panel_run` metadata query로만 polling하되 Dashboard endpoint가 opaque repository identity와 synthesis parent ID에 run/role/job identity를 bind한다. 선택한 member의 실제 저장 output은 panel-scoped detail이 같은 tuple과 Show response identity를 검증한 뒤 `show --job <id> --json`으로 lazy-load한다.
- `list --json`은 prompt와 diff를 포함한다. v0.64 daemon도 `omit_prompt=true`에서 queued/running prompt를 유지하므로 Dashboard adapter가 status와 무관하게 heavy fields를 제거한다.
- panel parent rerun은 `/api/job/rerun`에서 원본 member의 frozen target/config와 synthesis를 새 `panel_run_uuid`로 복제한다.
- Panel Refine은 Rerun same target/config가 아니라 매 iteration fresh HEAD의 새 panel review를 enqueue한다. iteration panel PASS 뒤 명시된 final panel을 검증하며, final panel F도 같은 fix budget으로 수정한 뒤 iteration panel부터 다시 시작한다.
- Panel Refine receipt와 `/api/jobs` polling은 job ID, panel run UUID, repo path, full git ref, branch, panel, synthesis role 및 panel job type을 일치시켜야 한다. 알려진 running 상태만 계속 polling하고, unknown/malformed status 또는 P/F 없는 terminal은 fail closed한다.
- Panel Refine run은 Dashboard process-scoped다. browser reload/reopen은 active run에 재접속할 수 있지만 process restart 뒤 자동 resume하지 않는다. shutdown은 active run context와 child process를 취소한다.
- v0.64.0 `fix --agent codex --quiet <synthesis-id>`는 새 commit에 panel 없는 default review를 자동 enqueue할 수 있다. Dashboard의 선택 panel enqueue와 별도로 unrelated extra review가 생길 수 있으며, Dashboard는 이를 추측으로 억제하지 않는다.
- v0.64.0은 panel enumeration JSON/API를 제공하지 않는다. Dashboard는 typed TOML로 global/local panel map을 병합하지만 linked worktree의 untracked main-checkout `.roborev.toml` 상속은 지원하지 않는다. local config가 없고 `.git`이 regular file인 ambiguous layout에서만 `--git-dir`과 `--git-common-dir` metadata를 요구하며, metadata resolution 실패는 global named panel을 숨기고 discovery/mutation을 fail closed한다. metadata가 성공해 actual linked worktree가 아니면 global catalog를 유지하고, `.git` directory/absent도 metadata 없이 global catalog를 유지한다.
- Fix Prompt는 요청한 job의 `show --job --json` top-level finding과 job metadata만 사용하고 original prompt, diff, log, 다른 member aggregate 및 config를 포함하지 않는다.
- General Fix의 v0.64.0 upstream contract는 [fix source](https://github.com/kenn-io/roborev/blob/v0.64.0/cmd/roborev/fix.go)와 [assisted-refactoring guide](https://github.com/kenn-io/roborev/blob/v0.64.0/docs/guides/assisted-refactoring.md)에서 확인했다. 명시적 positive decimal job ID만 positional target으로 전달하며 Dashboard의 Codex checkpoint exact argv는 `fix --agent codex --quiet <job-id>`다. upstream은 현재 checkout에서 실행하고 시작 dirty/stale를 자체 차단하지 않으며 stdout/stderr를 machine-readable 결과로 보장하지 않는다. exit 0이어도 no-change/dirty 상태 또는 job close가 가능하고, commit 뒤 panel 없는 default review enqueue를 시도할 수 있다.
- Dashboard General Fix는 이 upstream 전제를 보강한다. selected registered opaque identity의 `done` + `F` + `closed=false` + `skipped=false`인 single 또는 synthesis parent만 허용하고 member는 항상 거부한다. preview token과 backend-generated argv를 확인에 표시한 뒤 confirmation POST에서 fresh ownership, current branch+HEAD equality와 clean tree를 재검증한다. process-scoped async manager/cancel/shutdown, shared global/repository gate, bounded status/error와 post-run same identity/branch + advanced HEAD + clean tree 검증을 유지하며 성공·실패·취소 뒤 authoritative reviews/fix state를 다시 조회한다. process restart resume, arbitrary command, 일반 `roborev refine`은 지원하지 않는다.
- `repo list --json`과 독립 `reopen` command는 없다. reopen은 `close --reopen`이다.
- `stream --repo` checkpoint는 exact v0.64.0 capability로 구현했다. 시작 전에 installation/version/capability와 daemon already-online/healthy 상태를 확인하며 offline에서 daemon이나 stream process를 자동 시작하지 않는다. source는 repository identity별 하나만 유지하고 0..N SSE subscriber가 공유한다. stdout JSONL line은 1 MiB, stderr sink는 64 KiB로 bounded하며 malformed/unknown/oversized line, process exit, cancellation과 Dashboard shutdown을 안전하게 처리한다. recognized event는 raw payload를 버리고 `jobs-changed`와 monotonic sequence만 SSE로 전달한다.
- `stream`은 **lossy refresh/invalidation hint**이며 UI source of truth가 아니다. frontend는 event burst를 coalesce/debounce한 뒤 `/api/reviews` **authoritative API = actual state**를 다시 요청하고, repository epoch로 stale event/late response를 거부한다. EventSource reconnect를 지원하지만 **4초 polling = fallback/safety net**을 유지한다. 자동 테스트는 `internal/process/stream_test.go`, `internal/roborev/stream_test.go`, `internal/streamrelay/manager_test.go`, `internal/app/server_events_test.go`에서 deterministic fake process/service만 사용하며 실제 RoboRev/daemon/LLM은 실행하지 않는다.
- `/api/events`의 browser authorization은 mutation과 분리된다. explicit Origin은 정확히 한 개의 same-origin 값만 허용하고 Origin이 없으면 세 Fetch Metadata 값을 exact하게 모두 요구한다. mutation은 기존 absent-Origin 허용 및 CSRF 검증을 유지한다. authorization 실패는 query parsing, Registry.InspectIdentity, `Subscribe`, stream process start 전에 종료된다.
- Analyze introspection checkpoint는 exact v0.64.0의 `analyze --list`와 `analyze <type> --show-prompt`만 지원한다(B). upstream `analyze <type> --json`은 `Agentic: true`와 worker `WithAgentic(true)`로 Edit·Write·Bash 위험 작업을 수행하므로 Dashboard execution은 P/disabled이며, preview/confirmation/receipt/process 경로도 없다. `/api/analyses/preview`와 CSRF/Origin 이후 POST는 service/process 0의 501 tombstone이다. future upstream safe transport/capability/explicit confirmation이 확인될 때만 JSON execution을 재검토한다. 실제 RoboRev/LLM analysis smoke는 실행하지 않았다.
- Tasks는 **execution-disabled, history-only**다. v0.64 `run` job의 기본은 non-agentic이지만 Dashboard가 daemon의 effective `allow_unsafe_agents` / `disable_codex_sandbox` 상태나 global config identity를 검증할 machine-readable 계약(`/api/health`, `/api/status`, OpenAPI 어디에도 없음)을 제공하지 않으므로 실행은 fail-closed로 비활성화되어 있다. enqueue command builder, receipt parser, preflight/safety adapter, preview token과 task manager는 저장소에서 제거되었고 production reachable한 custom task enqueue 경로는 0이다. Task history(`job_type=task` + `git_ref=run`)와 결과 보기, 그리고 generic rerun 차단(`task-rerun-unsupported`)은 유지된다. 자동 검증은 fake-only이며 실제 task/Codex/LLM enqueue를 실행하지 않았다. Analyze disable(upstream이 `Agentic:true`)과 원인이 다르므로 두 사례를 하나의 manager로 합치지 않는다.
