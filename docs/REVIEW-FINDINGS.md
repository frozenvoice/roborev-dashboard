# RoboRev Dogfood Review Findings

검토일: 2026-08-17. 모든 검토는 과거 job rerun이 아니라 해당 시점의 새 `HEAD`를 `dashboard_cross` panel로 enqueue했다.

| Job | Commit | Verdict | 처리 |
|---|---|---|---|
| 13 | `fb88aa9` | Finding | repository job-ID authorization, Windows descendant cleanup, embed placeholder, registry load, repo-switch race, capability gating, show identity, metadata-only listing, config detection, detached mutation을 수정 |
| 17 | `cff6d45` | Finding | panel UUID repository authorization, member refresh/race/error, lightweight runtime endpoint, unknown health state와 diagnostic, daemon HTTP fixture를 수정 |
| 21 | `d3ed6b2` | Finding | visible commit subject/normalized verdict search, real v0.64 lifecycle fixture와 elapsed assertion, status palette duplication을 수정 |
| 25 | `666086c` | Finding | active-job heavy field strip, verified `id=` authorization fixture, suspended-before-Job-Object resume, selected state sync, version TTL, poll gating, panel handler tests, deterministic registry fallback을 수정 |
| 29 | `ea1174b` | Finding | defensive heavy-key expansion, offline stale state clear/online immediate refresh, detail identity-only key로 tab/comment draft 보존, coalesced version refresh를 수정; upstream 수신 비용은 v0.64 limitation으로 기록 |
| 33 | `4ecc039` | Finding | terminal detail/log refresh, in-flight list invalidation, transient offline draft 보존과 mutation disable, cold-load queue gating, failed version detection coalescing을 수정 |
| 37 | `12a1cd9` | Finding | detail/log generation guard와 empty-log requested state, cancellation-isolated version failure cache, malformed version failure cache를 수정 |
| 41 | `497abbf` | Finding | replacement request loading ownership, lazy error/retry UI, bounded terminal refresh retry, review 전환 시 전체 lazy generation invalidation을 수정 |
| 49 | `3341e11` | Finding | platform-aware path identity, serialized polling, registry persistence rollback, PowerShell native failure propagation, startup timeout 분리, 실제 daemon review receipt/fresh HEAD metadata, background error dedupe와 focused contract tests를 수정 |

## 적용한 핵심 finding

- Global job ID와 panel UUID는 RoboRev metadata에서 `repo_path`를 확인한 뒤 등록 registry와 대조한다.
- Windows child process를 kill-on-close Job Object에 할당하고 실제 descendant marker test로 timeout cleanup을 검증한다.
- 목록 polling은 full prompt/diff가 포함되는 `list --json` 대신 `/api/jobs?omit_prompt=true`를 사용한다.
- `show --json`의 review-row `id`가 list job identity를 덮지 않도록 URL의 job ID를 보존한다.
- panel member 요청은 panel UUID 단위 cleanup을 수행하고 nonterminal 상태 동안 갱신하며 오류를 inline 표시한다.
- 10초 runtime poll은 repository Git inspection과 review list를 반복하지 않는다.
- 실제 v0.64 응답에서 관찰한 `commit_subject`, `enqueued_at`, `started_at`, `finished_at`, `P`/`F`를 fixture와 UI test에 반영했다.
- v0.64 `omit_prompt=true`가 queued/running prompt를 유지하는 upstream 계약을 재확인하고 adapter가 `prompt`, `diff_content`, `patch`를 상태와 무관하게 제거하도록 했다. 1 MiB를 넘는 active prompt fixture로 list와 job authorization을 검증한다.
- Windows process는 suspended 상태로 만들고 Job Object에 할당한 뒤 resume하며, delay 없이 descendant를 spawn하는 timeout test로 race window를 검증한다.

## 적용하지 않은 지적과 근거

- `comment --message` / `--commenter`가 미지원이라는 지적: 설치된 v0.64.0 `roborev comment --help`가 두 flag를 모두 명시하므로 false positive다. coverage 문구를 실제 builder와 일치시켰다.
- strict TypeScript null 오류: `tsc -b && vite build`를 반복 실행했으나 재현되지 않았다. production build를 인수 조건으로 유지한다.
- `limit=0`이 zero rows일 수 있다는 지적: upstream v0.64.0 `/api/jobs` 계약과 TUI 호출부에서 0이 unlimited임을 확인했다.
- `--bind localhost` Host 불일치: 출력된 실제 listener 주소와 browser Host가 일치하는 기본/검증 경로에서 재현되지 않았다. 현재 Tier 1 기본은 `127.0.0.1`이다.
- Review HEAD에 destructive confirmation이 없다는 지적: review는 source tree를 변경하지 않는 핵심 read-only enqueue action이다. 대신 현재 HEAD SHA를 server에서 고정하고, frozen rerun은 별도 이름과 실제 transport confirmation을 사용한다. fix/refine/install/update/agentic run에는 계속 명시 확인을 요구한다.
