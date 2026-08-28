# Windows release readiness

이 문서는 RoboRev Dashboard의 첫 Windows 배포 후보를 검증하기 위한 운영 문서다. 현재 판정은 **RELEASE-READY-CANDIDATE / REMOTE-CI-PENDING**이며 **RELEASE-CLOSED** 또는 실제 release/tag 승인이 아니다. feature와 release를 분리한다.

## 지원 범위

- Dashboard의 Tier 1은 Windows 11 `windows/amd64` 단일 실행 파일이다.
- Dashboard가 지원하는 RoboRev compatibility boundary는 exact `v0.64.0`이다. `v0.65.0` 이상, 다른 버전, malformed 또는 확인할 수 없는 daemon은 capability와 downstream read를 fail closed한다.
- Git은 full Dashboard runtime의 외부 요구사항이다. 배포된 EXE의 `--help`, `--version`, `--version-json` 자체에는 Go, Node.js, npm 또는 Git이 필요하지 않다.
- Dashboard는 RoboRev CLI/daemon의 UI adapter이며 RoboRev 데이터의 source of truth를 재구현하지 않는다. RoboRev DB를 직접 읽거나 쓰지 않는다.

## Build provenance and artifact verification

Release provenance is compiled into the executable, not inferred from the machine at runtime. `roborev-dashboard.exe --version-json` emits the fixed product name, strict SemVer, 40-character Git commit, release flag, target and RoboRev compatibility (`v0.64.0`). Release builds are stamped with `-trimpath`, `-buildvcs=false`, explicit `windows/amd64` and fixed linker values; timestamps, source paths, usernames and host identity are not release inputs.

`scripts/build.ps1` is the developer build. It may stamp the current commit, permits a dirty checkout and uses development provenance. It is not a release publisher. `scripts/package.ps1 -Version <strict-SemVer>` is the release helper: it requires a clean checkout at the authoritative commit, builds the frontend and Windows executable, validates the executable's version JSON, and writes an atomic, no-overwrite artifact directory containing only:

- `roborev-dashboard.exe`
- a deterministic JSON manifest
- the executable's SHA-256 file

The manifest contains the product/version/commit/target, exact RoboRev compatibility, artifact name and digest, and `signed: false`. It contains no timestamp, absolute path, user name or host name. `-VerifyReproducible` builds the frontend twice, compares the sorted frontend file manifests, builds the Windows executable twice with the same fixed inputs in validated staging, and compares the executable hashes before publishing. The validation version `0.0.0-releasecheck.1` (and CI's `0.0.0-ci.1`) is test-only; it is not a release version.

Before publishing a future release, inspect the manifest and verify the detached SHA-256 with a trusted Windows tool. Do not call a CI artifact a release artifact. Code signing, tag creation, GitHub release publication and MSI/MSIX packaging are intentionally outside this checkpoint.

## Local data and installation

The Dashboard registry stores recent registered repositories, the last selection and per-repository panel selection in the OS user configuration location returned by Go's `os.UserConfigDir`, under `roborev-dashboard/config.json` on Windows (normally `%APPDATA%\roborev-dashboard\config.json`). `--config-dir` is an explicit test/deployment override. This file is Dashboard UI state, not RoboRev review data or an authorization grant. Malformed state is not silently rewritten by the registry loader.

The optional, user-confirmed RoboRev installer uses the immutable v0.64.0 Windows amd64 manifest and publishes the managed target `%USERPROFILE%\.roborev\bin\roborev.exe`. It verifies the official pinned URL, archive size, SHA-256 and safe ZIP shape, then performs a no-replace publish and a fresh `version --json` check. Existing supported binaries are respected; newer, unknown, broken or policy-blocked binaries are not downgraded or overwritten automatically. The installer does not run `roborev init`, install hooks or agents, change PATH, write credentials, or modify Windows security policy.

## Network and privacy

- The Dashboard HTTP server binds to loopback by default (`127.0.0.1`). The managed RoboRev daemon contract is also loopback-scoped.
- The only Dashboard-owned outbound installation request is an explicit user-confirmed HTTPS download of the pinned official RoboRev release asset, with bounded size and digest verification. The production installer does not consume mutable “latest” metadata.
- Other outbound behavior belongs to an explicitly requested RoboRev/agent operation and is not silently initiated by Dashboard startup or read-only readiness. The Dashboard does not read or store provider credentials, API keys or passwords.
- A source audit of `cmd`, `internal` and `web` found no Dashboard telemetry or analytics transport. This is a code-scope statement, not a claim about network behavior of RoboRev or an agent subprocess.
- Browser projections remove private command, process, credential and raw upstream diagnostic fields where the endpoint contract requires it. Bootstrap, repository management and inventory have deliberately path-bearing display/selector contracts; the path-free claim applies to the dedicated readiness and management DTOs, not to every Dashboard response.

## Intentionally unavailable v0.64 surfaces

The following are disabled or blocked by the exact v0.64 safety boundary, not unfinished hidden background work: General Fix execution, new Panel Refine execution, General Refine execution, Tasks execution, Analyze execution, Review Export, CI Metrics Export and compact. Root aggregate `roborev init`, repository delete/unregister and arbitrary daemon/command endpoints are not Dashboard features. The Dashboard never starts a daemon as a side effect of a read or substitutes an unreviewed background agent job for these surfaces.

## Recovery and operational limits

Dashboard mutations are explicit Preview → Confirmation → Execute operations. A process that never started cannot receive success credit from another writer; after a started external mutation, an ambiguous result is reported as unverified and is not retried or guessed back. Restart the Dashboard and use a fresh authoritative read when an operation is interrupted. Do not manually delete or edit RoboRev's database. Preserve the version JSON, manifest and sanitized phase/error when reporting a failure; remove credentials, tokens and private raw output first.

There is no first-release upgrade/migration path yet: the product has no prior published Dashboard release to upgrade from. Future release work must add an explicit migration and rollback contract before claiming upgrade support. RoboRev v0.65+ likewise requires a separate capability and contract checkpoint.

The produced Windows binary is unsigned. Windows SmartScreen, Defender or Application Control may warn or block it. Dashboard does not call `Unblock-File`, disable policy, change registry/security settings or generate a self-signed certificate to bypass those controls. A blocked binary remains blocked and must be handled by the operator's approved Windows policy.

## Human release checklist

Run from a clean `main` checkout and record evidence for the exact commit:

1. Confirm `HEAD == origin/main` and no uncommitted files; confirm the intended release version is a strict SemVer.
2. Run targeted tests, `scripts/test.ps1`, `go vet ./...`, and the production frontend/Windows build.
3. Run the developer EXE `--help`, `--version` and `--version-json` with a toolchain-free child environment.
4. Run `scripts/package.ps1 -Version 0.0.0-releasecheck.1 -VerifyReproducible`; inspect manifest fields, absence of paths/timestamps and the SHA-256. This validation version is not a release version.
5. Run the packaged Windows non-destructive E2E against a copied artifact, including embedded frontend, clean temp config, fake v0.64 daemon, v0.65 mismatch, restart/crash and cleanup checks. Confirm mutation POST count is zero.
6. Run the gated online installer E2E only when network evidence is available, for example `$env:ROBOREV_DASHBOARD_ONLINE_INSTALL_E2E='1'; go test ./internal/roborevcompat -run TestOnlinePinnedInstallerE2E -count=1`. It must use the pinned official v0.64.0 asset in a temporary HOME and verify second-install no-replace/hash preservation. Network-unavailable is **ONLINE-INSTALL-EVIDENCE-MISSING**, never PASS.
7. Review the unsigned-binary and SmartScreen note, recovery instructions, blocked-feature list and support evidence requirements.
8. Observe the GitHub Actions Windows Tier 1 run for the pushed commit. Until observed, record **REMOTE-CI-PENDING**; do not infer green from workflow syntax.
9. Confirm branch protection/ruleset status. The current private-plan API boundary is unavailable/not configured; this remains a residual release risk until an administrator establishes it.
10. Only a release owner may choose the real version/tag and publish a future release. This checkpoint creates no tag, release, signature or installer package.

The local release gate and the real gated online installer evidence passed for the current source. The current verdict is **RELEASE-READY-CANDIDATE / REMOTE-CI-PENDING**. The next checkpoint is **Final Release Candidate Closure**, which observes the pushed Windows CI run, chooses the real release version, freezes release notes, regenerates the artifact for that version and decides signing/tag/publication. It must not infer a published release from this candidate evidence.

## Support evidence

For a reproducible report, include the sanitized `--version-json` result, target OS/architecture, manifest and SHA-256, the failing bounded phase and whether the failure was local, daemon, policy or network. Never attach `%USERPROFILE%`, credentials, API keys, raw daemon output, prompts or private repository content without redaction.
