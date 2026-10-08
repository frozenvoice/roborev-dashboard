# 로컬 품질 검증

`scripts/verify-local.ps1`은 Windows 11 Tier 1 개발 완료 전 검증의 단일 진입점이다. 기존 `scripts/test.ps1`, `scripts/build.ps1`, `scripts/package.ps1`, `scripts/release-e2e.ps1`가 소유한 검사를 다시 구현하지 않고 순서와 결과만 조정한다. GitHub Actions는 계속 `workflow_dispatch` 수동 실행으로 유지한다.

## 사용법

저장소 루트에서 Windows PowerShell 5.1 또는 PowerShell 7로 실행한다.

```powershell
.\scripts\verify-local.ps1                 # Full (기본값)
.\scripts\verify-local.ps1 -Mode Quick
.\scripts\verify-local.ps1 -Mode Full
.\scripts\verify-local.ps1 -Mode Release
.\scripts\verify-local.ps1 -Mode Release -Version 0.0.0-localverify.1
```

`-Version`의 기본값은 `0.0.0-localverify.1`이며 Release 패키지와 `release-e2e.ps1`의 expected version에 함께 사용된다. strict SemVer가 아닌 값은 실행 전에 거부한다.

각 행은 `PASS`, `FAIL`, `BLOCKED` 중 하나로 출력된다.

- `PASS`: 해당 검사를 실제로 실행해 계약을 통과했다.
- `FAIL`: 검사를 실행했지만 계약 또는 명령이 실패했다.
- `BLOCKED`: 지원 플랫폼이 아니거나 prerequisite 누락 또는 checkout 상태 때문에 검사를 실행할 수 없었다. Windows가 아닌 환경의 Windows 전용 검사는 PASS로 바꾸지 않는다. 실행 중 scanner/module/feed/network 오류는 `FAIL`로 보고한다.

프로세스 종료 코드는 `0`(필수 행 모두 PASS), `1`(실행된 행의 FAIL), `2`(필수 행 BLOCKED)다. 완료 보고에는 모드, 각 행의 상태, Windows 패키징 상태, 실제 실행 파일 실행 상태를 별도로 기록한다.

## 모드별 범위

| 모드 | 검사 |
| --- | --- |
| Quick | `go test -p 1 -count=1`의 `internal/process`, `internal/pathsafety`, `internal/gitx`, `internal/roborev` 회귀 묶음, `web` Vitest, 검증기와 기존 PowerShell 계약 self-test(Windows 전용) |
| Full | `scripts/test.ps1 -SerialGoPackages`를 한 번 실행한다. 이 명령이 소유한 PowerShell self-test와 전체 repository test aggregate는 Windows 전용이며, `go test ./...`, production frontend build와 Vitest를 다시 실행하지 않는다. 이어서 `go vet ./...`, `scripts/build.ps1 -SkipFrontendBuild`로 별도 소유 개발 EXE를 만들고 개발 EXE의 세 flag 계약을 별도 행으로 확인한다. |
| Release | Full 기준에 더해 안전한 현재 checkout/HEAD와 clean 상태, non-reparse 경로, Windows amd64 Go 취약점 검사(`govulncheck` v1.8.0), `scripts/package.ps1 -VerifyReproducible`, package EXE의 세 flag 계약을 별도 행으로 확인하고 `scripts/release-e2e.ps1` smoke를 실행한다. Windows 전용 Release 행은 non-Windows에서 BLOCKED다. |

Full의 `scripts/test.ps1 -SerialGoPackages`는 이 로컬 검증에서만 Go package compile/test 프로그램을 `-p 1`로 순차 실행한다. 이는 package 간 cold compile 자원 경쟁을 줄이는 scheduling 경계이며, 각 테스트의 goroutine·child/grandchild process concurrency와 timeout assertion을 직렬화하거나 완화하지 않는다. retry를 추가하지 않는다. `scripts/test.ps1`를 인자 없이 실행하는 standalone/CI 경로는 기존 기본 scheduling을 유지한다. 스크립트는 frontend production build를 Go 테스트보다 먼저 수행하므로 Full에서 개발 EXE는 `-SkipFrontendBuild`로 만들며 중복 build를 피한다. Go 회귀 실행은 `-count=1`로 캐시된 결과를 새 실행의 근거로 사용하지 않는다.

Quick의 PowerShell self-test와 Full의 전체 repository test aggregate는 기존 `cmd.exe` 및 Windows backslash helper 계약 때문에 Windows 전용이다. non-Windows에서는 이 행들을 `BLOCKED`로 보고하지만 portable Quick Go/Vitest와 Full `go vet`는 실행할 수 있다. 이 portable 결과는 Full Windows 검증을 의미하지 않는다.

Release의 `govulncheck`는 CI와 같은 `golang.org/x/vuln/cmd/govulncheck@v1.8.0`을 Windows amd64 대상으로 사용한다. prerequisite 누락은 BLOCKED로 보고하지만, scanner compilation·Go module·취약점 feed·network 오류로 검사를 완료하지 못한 경우는 FAIL로 표면화한다. 어떤 경우에도 취약점 결과나 실행 오류를 숨기거나 예외 처리해 PASS로 만들지 않는다.

## Release 경계

Release는 현재 checkout의 canonical 경로와 모든 기존 parent를 검사하고 reparse point를 통과하지 않는다. `git rev-parse HEAD`와 clean status를 확인한 뒤에만 패키징을 시작한다. dirty checkout은 release 패키징을 BLOCKED로 보고한다.

검증기가 소유하는 임시 루트는 고정된 `.tmp\verify-local`이다. 이미 존재하면 재사용하거나 지우지 않고 즉시 BLOCKED로 종료한다. 새로 만든 루트만 cleanup하며, 기존 루트 `dist` 산출물, 다른 `.tmp` 항목, 사용자 작업물과 Git checkout은 삭제·복제·덮어쓰지 않는다. `TEMP`, `TMP`와 검증기 artifact용 `LOCAL_VERIFY_*` 환경 상태는 잠시 지정한 뒤 모든 종료 경로에서 이전 값을 clear/restore한다. 기존 `dist` 산출물을 보존하기 위해 Release package output은 검증기 소유 임시 경로 아래에 둔다.

`scripts/package.ps1`는 frontend assets와 Windows amd64 EXE를 두 번 빌드해 재현성을 확인한다. 최종 release manifest와 SHA-256 파일은 별도로 artifact와 대조한다. 기존 패키징 helper의 no-overwrite, parent safety와 reparse 검사를 그대로 사용한다.

개발 EXE와 package EXE의 세 flag 계약은 서로 다른 결과 행으로 확인한다. Windows 행은 기존 `internal/releasee2e`의 `TestArtifactCommandContract`에 위임하며 PowerShell process runner를 새로 만들지 않는다. wrapper는 정확한 `TestArtifactCommandContract`의 machine-readable Go JSON `pass` event를 실제로 받아야 하며, 무출력·skip·package-only 결과는 PASS가 아니라 FAIL이다. `scripts/package.ps1`도 최종 manifest publication 전에 같은 helper를 재사용한다. PASS는 copied executable을 allowlist한 private-profile environment와 toolchain-free PATH에서 실제 실행했다는 증거를 포함해야 한다. 이 harness는 `process.ExecRunner`의 suspended Job/tree cleanup을 재사용한다. fake fixture는 stdout/stderr 128 KiB 및 5초 경계를 포함해 timeout, overflow와 descendant cleanup을 검증한다.

1. `roborev-dashboard.exe --help`
2. `roborev-dashboard.exe --version`
3. `roborev-dashboard.exe --version-json`의 strict provenance/version/target JSON
4. `scripts/release-e2e.ps1 -Version <Version>`의 fake RoboRev CLI·fake daemon·fixture Git repository·임시 HOME/config smoke

마지막 smoke는 실제 사용자 RoboRev DB, 설정, 인증정보 또는 운영 daemon을 읽거나 쓰지 않는다. fake fixture가 아닌 실제 RoboRev review job, LLM 또는 외부 RoboRev panel review를 시작하지 않는다.

## 보안 회귀 범위

기존 backend/frontend 테스트가 소유한 다음 회귀를 유지한다. 변경이 필요한 경우 해당 소유 테스트를 강화하고 검증기에서 같은 suite를 중복 호출하지 않는다.

- typed executable/argument array와 shell injection 방지
- canonical repository path, Windows 공백·한글 경로와 reparse 경계
- same-origin/Origin 및 CSRF 검증
- cancellation, timeout, bounded stdout/stderr와 descendant process cleanup
- partial failure, stale response와 source-of-truth 재조회
- deterministic fake RoboRev/daemon과 fixture 기반 CLI 계약

검증기는 `~/.roborev/reviews.db`, RoboRev credential/API key/password와 전체 환경 변수에 접근하거나 로그에 기록하지 않는다. `release-e2e.ps1`가 만드는 fixture와 임시 profile은 테스트 종료 시 해당 실행이 새로 소유한 경로만 정리한다.

## 완료 workflow와 CI 정책

개발 완료 순서는 `targeted tests → verify-local Full → Codex internal reviewer → valid finding 수정 → commit → push → clean working tree`다. Release에 민감한 변경은 clean commit 뒤 `verify-local -Mode Release`를 별도로 수행한다.

여기서 `reviewer`는 Codex 내부 reviewer다. 사용자가 명시하지 않는 한 `roborev review`, panel review, `roborev wait` 또는 finding 수정 후 자동 re-review loop를 실행하지 않는다. `dashboard_cross`와 `dashboard_deep` 외부 gate는 사용자 또는 외부 gatekeeper가 수동으로 실행한다. 이 로컬 검증 진입점은 기존 정책을 변경하거나 GitHub Actions를 자동화하지 않는다.
