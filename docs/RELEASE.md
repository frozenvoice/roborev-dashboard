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

There is no first-release upgrade/migration path yet: the product has no prior published Dashboard release to upgrade from. Future release work must add an explicit migration and rollback contract before claiming upgrade support. RoboRev v0.65+ likewise requires a separate capability and contract checkpoint.

The produced Windows binary is unsigned. Windows SmartScreen, Defender or Application Control may warn or block it. Dashboard does not call `Unblock-File`, disable policy, change registry/security settings or generate a self-signed certificate to bypass those controls. A blocked binary remains blocked and must be handled by the operator's approved Windows policy.

## Human release checklist

Run from a clean `main` checkout and retain evidence for the exact candidate source, without copying dynamic evidence into frozen repository documentation:

1. Confirm `HEAD == origin/main` and no uncommitted files; confirm the intended release version is a strict SemVer.
2. Run targeted tests, `scripts/test.ps1`, `go vet ./...`, and the production frontend/Windows build.
3. Run the developer EXE `--help`, `--version` and `--version-json` with a toolchain-free child environment.
4. Run `scripts/package.ps1 -Version 0.1.0 -OutputDirectory .\dist\release\0.1.0 -VerifyReproducible`; inspect the exact three-file artifact set, manifest fields, absence of paths/timestamps and the SHA-256. If the destination already exists, stop rather than overwrite it. The output is a release candidate artifact until the release owner authorizes publication.
5. Run the packaged Windows non-destructive E2E against a copied artifact, including embedded frontend, clean temp config, fake v0.64 daemon, v0.65 mismatch, restart/crash and cleanup checks. Confirm mutation POST count is zero.
6. Run the gated online installer E2E only when network evidence is available, for example `$env:ROBOREV_DASHBOARD_ONLINE_INSTALL_E2E='1'; go test ./internal/roborevcompat -run TestOnlinePinnedInstallerE2E -count=1`. It must use the pinned official v0.64.0 asset in a temporary HOME and verify second-install no-replace/hash preservation. Network-unavailable is **ONLINE-INSTALL-EVIDENCE-MISSING**, never PASS.
7. Review the unsigned-binary and SmartScreen note, recovery instructions, blocked-feature list and support evidence requirements.
8. Observe the GitHub Actions Windows Tier 1 run for the exact candidate source; do not infer green from workflow syntax or a prior commit. Require a completed successful run including tests, vet, build, reproducible package, packaged E2E and artifact upload from a verified non-reparse Windows checkout.
9. Confirm branch protection/ruleset status. The current private-plan API boundary is unavailable/not configured; this remains a residual release risk until an administrator establishes it.
10. The release owner has selected `0.1.0` for this first candidate and may separately decide whether to publish it under future tag `v0.1.0`. This checkpoint creates no tag, release, signature, MSI/MSIX package or package-manager publication.

The local release gate, the gated online installer evidence, the exact-candidate Windows CI gate, the final 0.1.0 package checks and the release E2E must all pass before the semantic status is **RC-0.1.0-READY-TO-PUBLISH**. That status is not a publication record. The next action is **Await release-owner publication decision**. A later publication checkpoint must separately decide signing, tag, GitHub Release and asset upload.

## Support evidence

For a reproducible report, include the sanitized `--version-json` result, target OS/architecture, manifest and SHA-256, the failing bounded phase and whether the failure was local, daemon, policy or network. Never attach `%USERPROFILE%`, credentials, API keys, raw daemon output, prompts or private repository content without redaction.
