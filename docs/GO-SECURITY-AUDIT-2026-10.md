# Go security and Windows validation audit (2026-10-05)

## Scope and version decision

The initial checkout was clean `main` at
`ff5db8517ae18d730436e43498956a8c556f5e15`. Its `go.mod` specified Go
`1.24.0`, `golang.org/x/sys v0.41.0`, `github.com/BurntSushi/toml v1.6.0`
and `github.com/Microsoft/go-winio v0.6.2`. The actual local compiler was
`go1.26.5 windows/amd64`; the module directive alone did not describe the
compiler used locally. The historical successful Windows CI compiler was
`go1.24.0`, as recorded below.

The [official Go release history](https://go.dev/doc/devel/release), checked
on 2026-10-05, lists Go 1.26 and 1.27 as supported branches and Go 1.26.8 as
the latest 1.26 patch. This change retains the 1.26 branch and sets the
module's minimum compiler to `1.26.8`. The Windows dependency changes only
from `golang.org/x/sys v0.41.0` to `v0.44.0`, the minimum fixed version for
[GO-2026-5024](https://pkg.go.dev/vuln/GO-2026-5024).
The TOML and go-winio dependencies retain their original versions.

CI already selects Go using `go-version-file` pointing at the safe
checkout's `go.mod`; changing that directive updates CI's requested version.
Developer prerequisite and packaging checks are aligned with the same module
minimum and must inspect the selected compiler rather than infer it from an
installed launcher version. The patched source does not by itself establish
that the local verification or a remote CI run has passed.

## Windows vulnerability baseline

The source scan used versioned `govulncheck v1.8.0`, target
`windows/amd64`, 37 root packages (`./...`), and the
`https://vuln.go.dev` database last modified `2026-10-01T20:24:15Z`.
The JSON config and SBOM record the actual compiler as `go1.26.5` and
`golang.org/x/sys v0.41.0`. Raw baseline evidence is retained locally under
`$env:TEMP/roborev-dashboard-security-20261005/` as
`before-go1.26.5-windows.json` and `before-go1.26.5-windows.txt`.

Both scans set `GOTOOLCHAIN=local`, `GOOS=windows`, `GOARCH=amd64` and use an
isolated temporary `GOCACHE`. The baseline PATH selected
`C:\Program Files\Go\bin`; the patched PATH selected the temporary official
Go 1.26.8 toolchain. Both invoke the same
`versioned-bin/govulncheck.exe -json ./...` and convert the resulting JSON
using `govulncheck.exe -mode=convert -show=verbose`. JSON generation exited
0 even with baseline findings; conversion exited 3 for the vulnerable
baseline and 0 after patching. The converted result, rather than the JSON
generation exit status, determines the reported finding outcome. This
comparison scans the observed local Go 1.26.5 baseline, not the historical
CI Go 1.24.0 compiler.

The scanner reported the following six symbol-level findings. Each row
records one representative static call trace; the baseline JSON contains
the full traces and embedded official vulnerability records.

| ID | Affected component / fixed version | Representative scanner trace |
| --- | --- | --- |
| [GO-2026-6218](https://pkg.go.dev/vuln/GO-2026-6218) | `net/url`, Go 1.26.6 | `internal/roborev/client.go:674`: `Client.JobOutputStatus` → `http.Client.Do` → `url.URL.Parse` |
| [GO-2026-6090](https://pkg.go.dev/vuln/GO-2026-6090) | `crypto/tls`, Go 1.26.6 | `cmd/dashboard/main.go:323`: `main` → `http.Server.Serve` → `tls.Conn.HandshakeContext` |
| [GO-2026-6089](https://pkg.go.dev/vuln/GO-2026-6089) | `net/http`, Go 1.26.6 | `cmd/dashboard/main.go:323`: `main` → `http.Server.Serve` |
| [GO-2026-5972](https://pkg.go.dev/vuln/GO-2026-5972) | `encoding/asn1`, Go 1.26.6 | `internal/roborev/repository_merge_jobs.go:99`: `mergeJobsLimitReader.Read` → `bufio.Reader.Read` → `asn1.Unmarshal` |
| [GO-2026-5026](https://pkg.go.dev/vuln/GO-2026-5026) | standard-library `net/http` IDNA code, Go 1.26.6 | `internal/roborev/client.go:674`: `Client.JobOutputStatus` → `http.Client.Do` |
| [GO-2026-5024](https://pkg.go.dev/vuln/GO-2026-5024) | `golang.org/x/sys/windows`, x/sys v0.44.0 | `internal/snapshotignoremanagement/file_windows.go:136`: `windowsRootCreateAuthority.CreateGitignoreExclusive` → `windows.NewNTUnicodeString` |

The scan additionally reported
[GO-2026-5942](https://pkg.go.dev/vuln/GO-2026-5942) at package level (`net`),
and [GO-2026-6091](https://pkg.go.dev/vuln/GO-2026-6091) (`html/template`) and
[GO-2026-6088](https://pkg.go.dev/vuln/GO-2026-6088) (`encoding/xml`) at module
level. These three were present in the baseline standard library, with no
vulnerable symbol call demonstrated by the scanner. Their listed fixes are
also Go 1.26.6.

Symbol reachability is evidence for upgrading the compiler/dependency, not
proof of an exploitable Dashboard input. In particular, the x/sys call uses
the fixed `.gitignore` child name, so the static trace does not demonstrate
an attacker-controlled oversized Unicode-string input. The unencrypted
HTTP/2 advisory has protocol and timeout conditions; the `Server.Serve`
trace alone does not prove they occur in this application. No exploit was
attempted. Existing loopback binding, Host/Origin and CSRF authorization,
operation conflict gates, and canonical path/reparse-point controls remain
required independently of the dependency updates.

## Patched verification checkpoint

The patched Windows source scan completed with `No vulnerabilities found.`
Its config and SBOM record actual `go1.26.8`, x/sys `v0.44.0`, the same
scanner `v1.8.0` and database revision, and the same 37 root packages / four
modules. Evidence is retained beside the baseline as
`after-go1.26.8-windows.json` and `after-go1.26.8-windows.txt`.

| Scan | Compiler | x/sys | Symbol findings | Package-only findings | Module-only findings |
| --- | --- | --- | --- | --- | --- |
| Before | Go 1.26.5 | v0.41.0 | 6 | 1 | 2 |
| After | Go 1.26.8 | v0.44.0 | 0 | 0 | 0 |

The existing `scripts/test.ps1` passed under Windows PowerShell 5.1 with
actual Go 1.26.8: all Go packages, all four PowerShell contract scripts,
frontend 31 files / 594 tests, and the production frontend build. The first
sandboxed invocation stopped at esbuild process creation with `spawn EPERM`;
the approved identical retry passed. No tests or assertions were changed or
relaxed to obtain that pass.

The default installed launcher in `C:\Program Files\Go` remains Go 1.26.5.
With the pre-existing `GOTOOLCHAIN=auto`, ordinary `go version` from this
repository now selects actual Go 1.26.8 from Go's verified standard toolchain
cache. The repository minimum therefore governs the selected compiler;
no global Go setting or system Go installation was changed.

`go vet ./...` passed. `scripts/build.ps1 -OutputPath
dist/security-20261005-2f0c39b9/roborev-dashboard.exe -SkipFrontendBuild`
passed using the frontend already built by `scripts/test.ps1`. `go version
-m` on the resulting binary records actual Go 1.26.8, `windows/amd64`,
`CGO_ENABLED=0`, x/sys v0.44.0 and the unchanged TOML/go-winio versions.

`scripts/package-local.ps1` with a custom isolated `OutputDirectory`
compiled the `0.1.0-local` installer using pinned Inno Setup 6.7.3. Setup
was not launched, and the existing `dist/installer` output was not
overwritten. A copied executable passed the existing ignored
`.tmp/smoke-built-installed.ps1` harness using temporary configuration/data:
version/help with no development toolchain on PATH, absent-instance
status/stop, occupied port 7410 isolation, embedded frontend, duplicate
`--no-open` preserving the same PID/instance, explicit-port conflict,
graceful stop of the exact instance, preserved configuration hash and no
`.roborev` data creation. Results are retained at
`.tmp/installed-smoke-48d3e799298a46958dbb0b6f408d2488/result.json`.

This is installed-mode executable validation and installer compilation;
actual Setup installation/uninstallation remains unverified because
`WindowsSandbox.exe` is absent. Fresh disposable Sandbox inputs were
successfully prepared at
`dist/installer-sandbox/0ebe595b03c544e485f1bd0011e47558/installer-test.wsb`
but were not launched. Formal clean-source `scripts/package.ps1`
reproducibility and packaged `release-e2e.ps1` checks are still pending after
the code commit. No operational release, account billing change or existing
user installation/configuration overwrite was performed. A local pass
cannot replace successful remote CI on the candidate commit, and
account/provisioning failures cannot be fixed by relaxing tests.

## Remote CI evidence

Read-only GitHub connector queries on 2026-10-05 independently confirmed
`main` at `ff5db8517ae18d730436e43498956a8c556f5e15`. The repository returned
five main-branch workflow runs. Its latest run is still
[Windows CI 35519038770](https://github.com/frozenvoice/roborev-dashboard/actions/runs/35519038770),
attempt 1, triggered by push on 2026-09-20 at 15:16:05 UTC
(2026-09-21 00:16:05 KST), completed with failure at 15:16:09 UTC.

The [raw jobs response](https://api.github.com/repos/frozenvoice/roborev-dashboard/actions/runs/35519038770/jobs)
contains one job, `Windows Tier 1` / `106099797465`, completed with failure
at 15:16:08 UTC. Its exact relevant fields are `steps: []`,
`labels: ["windows-latest"]`, `runner_id: 0`, `runner_name: ""`,
`runner_group_id: 0` and `runner_group_name: ""`. No runner or code/test
step execution is recorded. The original annotation, retrieved below,
confirms an account payment/spending-limit admission failure before
runner/step execution.

The [commit check-run response](https://api.github.com/repos/frozenvoice/roborev-dashboard/commits/ff5db8517ae18d730436e43498956a8c556f5e15/check-runs)
reports exactly one annotation, but `output.title`, `output.summary` and
`output.text` are null. A later approved `gh api` retry retrieved the
[original check-run annotation](https://api.github.com/repos/frozenvoice/roborev-dashboard/check-runs/106099797465/annotations)
with failure severity at `.github`, line 1. Its exact message is:

> The job was not started because recent account payments have failed or your spending limit needs to be increased. Please check the 'Billing & plans' section in your settings

This confirms account payment/spending-limit admission failure; it does not
distinguish failed recent payments from an insufficient spending limit.
That distinction requires an authorized account check. The failed run
provides no evidence of a code/test failure or an allocated runner failure.
The owner must check Billing & plans and obtain successful CI on the patched
candidate after resolving the account condition. No account settings or CI
runs were changed during this investigation.

Initial CLI queries failed locally with `proxyconnect tcp: dial tcp
127.0.0.1:9 ... actively refused it`, then `net/http: TLS handshake timeout`
and `error connecting to api.github.com` under approved direct access. The
connector initially rejected the annotations endpoint/HTML job URL, and the
job-log API returned `404 BlobNotFound`. These are investigation-access
failures, not the workflow's original failure; the subsequent successful
annotation retrieval supersedes the earlier unverified billing hypothesis.

The latest successful main run is
[33220089303](https://github.com/frozenvoice/roborev-dashboard/actions/runs/33220089303)
for `56f03027a05d52dfcb913539d5f0bedad11db9e2`, started 2026-08-28
23:20:15 UTC and completed 23:29:50 UTC. Its recorded tests, vet, development
build, reproducible package, packaged smoke and artifact upload all passed.
The retained job log explicitly says `Setup go version spec 1.24.0` and
`go version go1.24.0 windows/amd64`. This confirms the historical compiler;
it is not evidence that the current patched candidate has passed remote CI.

The workflow reads `SAFE_CHECKOUT\go.mod` through `go-version-file`; it does
not pin Go 1.26.5. The [main branch metadata](https://api.github.com/repos/frozenvoice/roborev-dashboard/branches/main)
returned `protected: false` and no required status-check contexts. Existing
AGENTS commit/push/internal-review requirements and the separate release-owner
publication approval boundary still apply.
