# Windows release readiness

이 문서는 RoboRev Dashboard 0.1.0 첫 Windows release candidate의 운영 정책과 검증 절차를 고정한다. 최종 local/upstream/package/E2E/exact-candidate Windows CI gate가 모두 통과한 경우에만 semantic status를 **RC-0.1.0-READY-TO-PUBLISH**로 판정한다. 실제 tag, GitHub Release, asset publication 또는 서명은 이 checkpoint에서 수행하지 않는다. Feature completion과 release readiness를 분리한다.

0.1.0은 첫 Dashboard release이자 pre-1.0 호환성 단계의 버전이다. 지원 계약은 exact RoboRev `v0.64.0` 경계에 묶여 있고, 의도적으로 좁은 안전 surface만 제공한다. 향후 tag convention은 `v0.1.0`이지만 tag는 release-owner의 별도 publication 결정 뒤에만 만들 수 있다.

## 지원 범위

- Dashboard의 Tier 1은 Windows 11 `windows/amd64` 단일 실행 파일이다.
- Dashboard가 지원하는 RoboRev compatibility boundary는 exact `v0.64.0`이다. `v0.65.0` 이상, 다른 버전, malformed 또는 확인할 수 없는 daemon은 capability와 downstream read를 fail closed한다.
- Git은 full Dashboard runtime의 외부 요구사항이다. 배포된 EXE의 `--help`, `--version`, `--version-json` 자체에는 Go, Node.js, npm 또는 Git이 필요하지 않다.
- Dashboard는 RoboRev CLI/daemon의 UI adapter이며 RoboRev 데이터의 source of truth를 재구현하지 않는다. RoboRev DB를 직접 읽거나 쓰지 않는다.

## Build provenance and artifact verification

Release provenance is compiled into the executable, not inferred from the machine at runtime. `roborev-dashboard.exe --version-json` emits the fixed product name, strict SemVer, 40-character Git commit, release flag, target and RoboRev compatibility (`v0.64.0`). Release builds are stamped with `-trimpath`, `-buildvcs=false`, explicit `windows/amd64` and fixed linker values; timestamps, source paths, usernames and host identity are not release inputs.

`scripts/build.ps1` is the developer build. It may stamp the current commit, permits a dirty checkout and uses development provenance. It is not a release publisher. `scripts/package.ps1 -Version <strict-SemVer>` is the release helper: it requires a clean checkout, builds the frontend and Windows executable, validates the executable's version JSON, and writes an atomic, no-overwrite artifact directory containing only:

- `roborev-dashboard.exe`
- a deterministic JSON manifest
- the executable's SHA-256 file

The manifest contains the product/version/commit/target, exact RoboRev compatibility, artifact name and digest, and `signed: false`. It contains no timestamp, absolute path, user name or host name. `-VerifyReproducible` builds the frontend twice, compares the sorted frontend file manifests, builds the Windows executable twice with the same fixed inputs in validated staging, and compares the executable hashes before publishing. The validation version `0.0.0-releasecheck.1` (and CI's `0.0.0-ci.1`) is test-only; it is not a release version.

Before publication, the release owner must inspect the manifest and verify the detached SHA-256 with a trusted Windows tool. Do not call a CI artifact a release artifact. This checkpoint chooses the unsigned 0.1.0 policy but does not create a signing certificate, sign the binary, create a tag, publish a GitHub Release, upload assets or build MSI/MSIX packaging.

## Local data and installation

The Dashboard registry stores recent registered repositories, the last selection and per-repository panel selection in the OS user configuration location returned by Go's `os.UserConfigDir`, under `roborev-dashboard/config.json` on Windows (normally `%APPDATA%\roborev-dashboard\config.json`). `--config-dir` is an explicit test/deployment override. This file is Dashboard UI state, not RoboRev review data or an authorization grant. Malformed state is not silently rewritten by the registry loader.

The optional, user-confirmed RoboRev installer uses the immutable v0.64.0 Windows amd64 manifest and publishes the managed target `%USERPROFILE%\.roborev\bin\roborev.exe`. It verifies the official pinned URL, archive size, SHA-256 and safe ZIP shape, then performs a no-replace publish and a fresh `version --json` check. Existing supported binaries are respected; newer, unknown, broken or policy-blocked binaries are not downgraded or overwritten automatically. The installer does not run `roborev init`, install hooks or agents, change PATH, write credentials, or modify Windows security policy.

## Network and privacy

- The Dashboard HTTP server binds to loopback by default (`127.0.0.1`). The managed RoboRev daemon contract is also loopback-scoped.
- The only Dashboard-owned outbound installation request is an explicit user-confirmed HTTPS download of the pinned official RoboRev release asset, with bounded size and digest verification. The production installer does not consume mutable “latest” metadata.
- Other outbound behavior belongs to an explicitly requested RoboRev/agent operation and is not silently initiated by Dashboard startup or read-only readiness. Reads do not start, restart or repair a daemon. The Dashboard does not read or store provider credentials, API keys or passwords.
- A source audit of `cmd`, `internal` and `web` found no Dashboard telemetry or analytics transport. This is a code-scope statement, not a claim about network behavior of RoboRev or an agent subprocess.
- Browser projections remove private command, process, credential and raw upstream diagnostic fields where the endpoint contract requires it. Bootstrap, repository management and inventory have deliberately path-bearing display/selector contracts; the path-free claim applies to the dedicated readiness and management DTOs, not to every Dashboard response.

## Intentionally unavailable v0.64 surfaces

The following are intentionally disabled or unsupported by the exact v0.64 safety and authority contract, not unfinished hidden background work: General Fix execution, new Panel Refine execution, General Refine execution, Tasks execution, Analyze execution, Review Export, CI Metrics Export and compact. Root aggregate `roborev init`, repository delete/unregister and arbitrary daemon/command endpoints are not Dashboard features. The Dashboard never starts a daemon as a side effect of a read or substitutes an unreviewed background agent job for these surfaces.

## Recovery and operational limits

Dashboard mutations are explicit Preview → Confirmation → Execute operations. A process that never started cannot receive success credit from another writer; after a started external mutation, an ambiguous result is reported as unverified and is not retried or guessed back. Restart the Dashboard and use a fresh authoritative read when an operation is interrupted. Do not manually delete or edit RoboRev's database. Preserve the version JSON, manifest and sanitized phase/error when reporting a failure; remove credentials, tokens and private raw output first.

Windows Setup supports per-user same-version reinstall and forward-version replacement without migrating or deleting user settings. The official Setup packages the verified release executable and its unchanged official manifest/hash; `build-local.cmd` separately produces an unsigned development installer with a `-local` label and `version=dev, release=false` executable. Installer failures after replacement starts must be treated as potentially partial; automatic rollback is not a product guarantee. See [Windows local installer](WINDOWS-INSTALLER.md) for lifecycle and disposable verification. RoboRev v0.65+ still requires a separate capability and contract checkpoint.

On Windows, every server holds a current-user lease for its canonical configuration-file path before loading the registry. Installed and portable copies using the same settings cannot start separate servers and overwrite each other's registrations/preferences. Reopening the same installed executable activates its authenticated existing instance. An explicit separate `--config-dir` allows independent servers; a busy port selects the next available loopback port without adopting the existing listener. Ctrl+C stops a portable server; `--stop-installed` stops only the authenticated executable-scoped installed instance. Neither shutdown path stops the RoboRev daemon or cancels its background review jobs.

The produced Windows binary is unsigned. Windows SmartScreen, Defender or Application Control may warn or block it. Dashboard does not call `Unblock-File`, disable policy, change registry/security settings or generate a self-signed certificate to bypass those controls. A blocked binary remains blocked and must be handled by the operator's approved Windows policy.

## Human release checklist

Run from a clean `main` checkout and retain evidence for the exact candidate source, without copying dynamic evidence into frozen repository documentation:

1. Confirm `HEAD == origin/main` and no uncommitted files; confirm the intended release version is a strict SemVer.
2. Run targeted tests, `scripts/test.ps1`, `go vet ./...`, and the production frontend/Windows build.
3. Run the developer EXE `--help`, `--version` and `--version-json` with a toolchain-free child environment.
4. Run `pwsh -NoProfile -File scripts/prepare-distributions.ps1 -Version 0.1.0 -VerifyReproducible`. It invokes `package.ps1` for the official three-file source contract, then packages those same bytes into Setup and a deterministic portable ZIP. Inspect the exact candidate set, manifest and checksums. If the destination already exists, stop; preserve it and choose an explicit fresh `-OutputDirectory` when preparing another candidate. The output remains a candidate until publication approval.
5. Run the packaged Windows non-destructive E2E against a copied artifact, including embedded frontend, clean temp config, fake v0.64 daemon, v0.65 mismatch, restart/crash and cleanup checks. Confirm mutation POST count is zero.
6. Run the explicit distribution lifecycle gate described below, including installed payload/portable/manifest SHA comparison, upgrade, downgrade rejection, same-config exclusion and separate-config concurrency. Run the gated online RoboRev installer E2E only when network evidence is available, for example `$env:ROBOREV_DASHBOARD_ONLINE_INSTALL_E2E='1'; go test ./internal/roborevcompat -run TestOnlinePinnedInstallerE2E -count=1`. It must use the pinned official v0.64.0 asset in a temporary HOME and verify second-install no-replace/hash preservation. Network-unavailable is **ONLINE-INSTALL-EVIDENCE-MISSING**, never PASS.
7. Review the unsigned-binary and SmartScreen note, recovery instructions, blocked-feature list and support evidence requirements.
8. Observe the GitHub Actions Windows Tier 1 run for the exact candidate source; do not infer green from workflow syntax or a prior commit. Require a completed successful run including tests, vet, build, reproducible package, packaged E2E and artifact upload from a verified non-reparse Windows checkout.
9. Confirm branch protection/ruleset status. The current private-plan API boundary is unavailable/not configured; this remains a residual release risk until an administrator establishes it.
10. The release owner has selected `0.1.0` for this first candidate and may separately decide whether to publish it under future tag `v0.1.0`. This checkpoint creates no tag, release, signature, MSI/MSIX package or package-manager publication.

The local release gate, the gated online installer evidence, the exact-candidate Windows CI gate, the final 0.1.0 package checks and the release E2E must all pass before the semantic status is **RC-0.1.0-READY-TO-PUBLISH**. That status is not a publication record. The next action is **Await release-owner publication decision**. A later publication checkpoint must separately decide signing, tag, GitHub Release and asset upload.

## Support evidence

For a reproducible report, include the sanitized `--version-json` result, target OS/architecture, manifest and SHA-256, the failing bounded phase and whether the failure was local, daemon, policy or network. Never attach `%USERPROFILE%`, credentials, API keys, raw daemon output, prompts or private repository content without redaction.

## 설치형·포터블 동일 버전 릴리스 후보

위의 `scripts/package.ps1`은 검증 가능한 단독 EXE·manifest·SHA-256의 3종 공식 빌드 계약을 유지합니다. 사용자용 배포물 두 개를 한 번에 준비하려면 Windows x64 깨끗한 체크아웃에서 `pwsh -File scripts/prepare-distributions.ps1 -Version <SemVer> -VerifyReproducible`을 실행합니다.

이 스크립트는 공식 EXE의 동일한 바이트를 넣은 `RoboRev-Dashboard-Setup.exe`와 `RoboRev-Dashboard-<SemVer>-win-x64-portable.zip`을 생성합니다. 설치기의 정식 표시 버전은 EXE의 release provenance와 일치하며 `-local`을 붙이지 않습니다. ZIP에는 EXE와 공식 manifest·SHA-256 파일만 포함하며, 설정/계정/기존 RoboRev 바이너리는 포함하지 않습니다. ZIP 항목의 순서·timestamp·attributes를 고정하고 모든 항목을 원본과 대조합니다. `-VerifyReproducible`은 frontend/EXE와 ZIP 재현성을 검증합니다. Setup의 전체 파일 SHA는 별도로 기록하며, 미서명 Inno Setup 컨테이너의 바이트 재현성을 EXE 재현성과 동일한 것으로 주장하지 않습니다.

출력은 기본 `dist/release/<SemVer>/` 또는 명시한 `-OutputDirectory`에 원자적으로 반영합니다. 공식 3파일, Setup, portable ZIP, `SHA256SUMS.txt`의 정확히 6파일만 허용합니다. 같은 경로의 기존 후보는 덮어쓰지 않으며 실패한 staging은 정상 후보로 승격하지 않습니다. 최종 승격 직전에 clean checkout과 manifest의 commit을 다시 확인합니다. 같은 checkout의 배포 준비는 잠금으로 직렬화하며, 별도 build/test나 소스 편집을 동시에 실행하지 않습니다.

실제 설치·업데이트·제거 검증은 명시적으로 실행하는 `scripts/distribution-e2e.ps1 -DistributionDirectory <candidate> -Version <version> -AllowCurrentUserInstallerTest`를 사용합니다. 기본 모드는 정확한 후보 Setup을 새 임시 `/DIR`에 설치하며 기존 Dashboard 설치/등록이 있으면 보존하고 중단합니다. 시작 메뉴 항목을 만들지 않고 앱 프로세스는 toolchain/RoboRev 없는 PATH와 격리된 profile/config를 사용합니다. 같은 버전 재설치뿐 아니라 더 낮은 버전의 공식 3파일로 만든 `-OlderSetupPath`/`-OlderVersion`을 제공하여 upgrade와 downgrade 거부도 검증합니다. 임시 설치는 실제 uninstaller로 제거하며, 기존 사용자 설정과 managed RoboRev EXE는 hash sentinel로 보존 여부를 확인합니다.

기존 설치가 있는 호스트에서는 `-UseIsolatedInstallerIdentity`를 명시할 수 있습니다. 이 모드는 동일한 공식 payload·고정 Inno compiler·ISS로 AppId만 고정된 테스트 GUID로 바꿔 임시 설치기를 만듭니다. 제품 AppId/기본 설치 경로/시작 메뉴 수명주기의 검증과 구분하여 기록합니다. 정확한 원본 배포 Setup의 payload readback을 반드시 추가하기 위해 검증된 portable `innoextract` 경로와 SHA를 `-SetupExtractorPath`/`-SetupExtractorSha256`에 함께 전달해야 합니다. 원본 Setup을 실행하지 않고 추출한 EXE·manifest·SHA를 공식 3파일 및 ZIP과 대조합니다. 추출 도구는 이 gate가 다운로드하거나 설치하지 않으며, 호출자가 신뢰한 고정 소스·버전·archive digest와 실행 파일 digest를 확인합니다. 이번 검증 도구는 [innoextract Windows tag 670](https://github.com/UserUnknownFactor/innoextract_win/releases/tag/670), source commit `e561d8cb6004776eecb3184c0d56b3534a0c7e15`이며 archive SHA-256 `79b69b9b1fcd98f42ccd4b245efdf6a03bcfb674ba6af482f5a46891c9ed4d14`, EXE SHA-256 `0dab51583e403970065db49f87a6a2bf683ab6ebfdf27bede2bf633dd232a1a2`입니다. 소유하지 않은 임시 경로나 테스트 등록이 있으면 어느 모드도 재사용·삭제하지 않습니다. 브라우저 자동 열기와 Windows 정책 UI 경험은 별도 수동 smoke입니다.

**하나의 GitHub Release에 Setup EXE와 portable ZIP을 모두 첨부**합니다(`SHA256SUMS.txt`도 함께 권장). 게시 승인 후 동일한 후보 디렉터리의 두 파일을 함께 선택하고, 릴리스 버전·manifest commit·EXE provenance 및 각 배포물 checksum을 다시 확인합니다. 로컬 빌드는 태그·GitHub Release·게시·서명을 수행하지 않습니다. 위 Windows E2E/CI와 설치기 수명주기 검증이 완료되기 전까지 게시 가능한 것으로 주장하지 않습니다. 기존 `0.1.0-local` 개발 설치기 경로와 회귀 검사는 유지합니다.
