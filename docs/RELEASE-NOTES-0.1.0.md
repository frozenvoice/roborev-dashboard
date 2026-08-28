# RoboRev Dashboard 0.1.0

RoboRev Dashboard 0.1.0 is the first pre-1.0 Windows release candidate. Its semantic status is **RC-0.1.0-READY-TO-PUBLISH** only after the release gates in [`docs/RELEASE.md`](RELEASE.md) are green. This checkpoint does not create the future `v0.1.0` tag, a GitHub Release, release asset publication or a signature.

## Why 0.1.0

This is the first Dashboard release, in a pre-1.0 compatibility phase. The supported RoboRev contract is intentionally fixed at exact `v0.64.0`, and the supported Dashboard surface is intentionally narrow where a broader upstream operation would carry hidden lifecycle or agent authority.

## Highlights

- A local Windows Dashboard for exact RoboRev `v0.64.0`, with Review HEAD, review history and detail, panel review, member results, comments, close/reopen/cancel, and lazy prompt or log views.
- Repository setup readiness, runtime diagnostics and explicit daemon lifecycle controls. Read-only pages do not silently start, restart or repair a daemon.
- Repository inventory and readiness views, plus safe repository display-name rename, stale metadata move repair and metadata/job merge. A metadata/job merge consolidates RoboRev records; it is not a filesystem move or a Git merge.
- Typed Configuration and a separate Repository Agent Configuration flow for supported global and selected-repository choices.
- Bounded Git Hooks, Agent Hooks and Skills readiness/management surfaces with explicit confirmation and readback where a write is supported.
- Snapshot Ignore append management with a preview, confirmation and bounded readback flow.
- Repository Tracking registration against an already-running, healthy, exact-v0.64.0 RoboRev daemon. It does not start or repair the daemon.
- CI Workflow preview and create-only management for the fixed `.github/workflows/roborev.yml` target, including target drift/readiness reporting. It does not offer arbitrary output, overwrite or automatic Git commit/push.
- Read-only Stats and Cost views, and explicit agentic Insights preview/confirmation/status/result flows. Insights can consume the configured agent quota, so the confirmation surface includes a quota warning.
- Analyze is limited to available introspection/read-only information; Analyze execution is not enabled. Existing Tasks history may be viewed where exposed, but Tasks execution is not enabled.

These surfaces are Dashboard adapters around RoboRev's source of truth. They do not replace RoboRev storage or claim a single universal mutation workflow.

## Intentionally unavailable

The following are intentionally disabled or unsupported by the exact RoboRev `v0.64.0` safety and authority contract. They are not hidden background work waiting to run:

- General Fix execution
- starting a new Panel Refine
- General Refine execution
- Tasks execution
- Analyze execution
- Review Export
- CI Metrics Export
- compact
- repository delete or unregister
- aggregate/root `roborev init`

## Requirements and compatibility

- Windows 11 `windows/amd64` is the Tier 1 release target.
- Git is required for repository operations and runtime repository inspection.
- RoboRev exact `v0.64.0` is supported. Other, newer, malformed or unverifiable versions fail closed rather than being silently substituted.
- If RoboRev is absent, the Dashboard's explicit Setup flow can offer the pinned official Windows asset for confirmation and installation. It does not run `roborev init`, install hooks or agents, change `PATH` or write credentials.
- The packaged Dashboard executable embeds its frontend and does not require Go, Node.js or npm at runtime.
- The browser UI is served by a local loopback Dashboard server.

## Security, privacy and network behavior

- The Dashboard server binds to loopback, and the managed RoboRev daemon is local and loopback-scoped.
- Installation uses an explicit, user-confirmed HTTPS request for the pinned official RoboRev asset: [RoboRev v0.64.0 Windows amd64 ZIP](https://github.com/kenn-io/roborev/releases/download/v0.64.0/roborev_0.64.0_windows_amd64.zip). Other network activity may occur when the user explicitly starts a RoboRev or agent operation; this note does not claim that RoboRev or agents never use the network.
- A source audit found no Dashboard telemetry or analytics transport. The Dashboard does not read or store provider credentials, API keys or passwords.
- Mutations use typed, explicit confirmation and server-side repository authorization. Read paths do not perform hidden daemon lifecycle work.
- Privacy is endpoint-scoped: readiness and management DTOs keep repository paths and raw upstream details server-side where their contracts require it, while existing bootstrap, repository selection and inventory displays may intentionally contain repository paths. Not every Dashboard response is path-free.

## Unsigned Windows binary

The 0.1.0 release candidate is **UNSIGNED**. No signing certificate is explored, created, purchased or used in this checkpoint, and no self-signed certificate or security-policy bypass is provided. Windows SmartScreen, Defender or Application Control may warn or block the executable. Operators must follow their approved Windows policy; the Dashboard does not call `Unblock-File`, disable security controls or change registry policy.

## Publication status

The future tag convention is `v0.1.0`. This release-candidate checkpoint has not created a Git tag, GitHub Release or release asset, and has not published to MSI/MSIX, winget, Chocolatey or Scoop. The next action is **Await release-owner publication decision**. Publication, signing and any distribution packaging require a separate, explicit release-owner decision.
