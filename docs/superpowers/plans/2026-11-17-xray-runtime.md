# Native Xray Runtime Implementation Plan

> Execute with superpowers:executing-plans in this session; use a fresh reviewer before deployment.

**Goal:** Import all supported subscription endpoints and run XHTTP/VLESS Encryption without losing settings.
**Architecture:** Preserve a native Xray outbound on Entry when sing-box conversion cannot represent it. Select one engine per TUN; retain existing policy routing and lifecycle.
**Tech Stack:** Go, sing-box 1.13.3, official Xray Linux arm64 release, OpenWrt.
**Spec:** ../specs/2026-11-17-xray-runtime-design.md

## Global constraints
- No subscription secrets in logs, tests or versioned fixtures.
- No automatic default-route or system DNS changes by Xray.
- Old runtime records/bundles and sing-box endpoints remain compatible.
- User has authorized provider diagnostics, router installation and the new SSH key.

## Review focus
- Process identification after manager restart and rollback must use the saved engine.
- Missing Xray must fail before disrupting any active tunnel.
- XHTTP nested download settings and VLESS encryption/finalmask must survive unchanged.
- Standby probes must actually use Xray for native entries and clean up on timeout.
- New bundles validate declared Xray files; old bundles still install/rollback.

## Tasks
- [x] 1. Add failing parser/config tests, then preserve native VLESS outbounds and implement Xray TUN/SOCKS config builders.
- [x] 2. Add failing lifecycle tests, select engine by `.xray.json` path for startup/reuse/health/kill/rollback, and validate binary availability before active changes.
- [x] 3. Add standby probe coverage and choose Xray binary/config for native entries.
- [x] 4. Package pinned Xray, add declared binary validation and backwards-compatible archive handling; test.
- [x] 5. Run full Go/frontend tests, obtain independent code review, resolve consequential findings, build router bundle.
- [x] 6. Validate live JSON with both engines, test native TUN and SOCKS connections in isolation, deploy via built-in updater, verify catalogue and health.

## Execution ledger
- Initial state: previous JSON fix is installed and remains uncommitted locally. Retain those edits in this continuation. Native TUN avoids adding a second process or a database migration.

- Tasks 1–4: parser/startup and standby tests first reproduced missing support; bundle test reproduced acceptance of a missing declared Xray. Implemented native config preservation, lifecycle selection, binary preflight, probe selection, and pinned packaging. Added active-tunnel preservation and native rollback tests.
- Task 5: all Go packages passed; all 45 frontend tests passed; shell syntax and bundle build passed. Fresh independent reviewer found no consequential issues.
- Review exclusions accepted: live router results are verified separately; generated binaries/private snapshots stay outside source review; stale user server selections and protocols outside the stated subset remain separate work.
- Task 6 in progress: fresh provider response contains 83 configs; import yields 70 sing-box + 13 Xray. All 83 generated TUN configs passed their actual engine validators on the Linux arm64 router.
- Live native TUN test passed: interface/address created, existing main routes and policy rules unchanged (apart from the expected connected interface subnet); HTTPS succeeded through a temporary source-specific test table; process/interface/test rules removed. Native SOCKS initial pass: 9/13 reachable, four connection timeouts being retried independently.
- Deployment complete: installed 2026-11-17T09:21:45Z build via built-in updater; router backup `/mnt/usb-4d3e56cb/vpn-manager/backups/update-20261117-092534`. Installed Xray reports v26.11.9; catalogue returns rawCount=83 and 83 locations. User configuration before/after is identical.
- After automatic recovery, both pre-existing NL #3 and Poland tunnels are running and WAN is up. Existing lastError about the old Sweden #4 selection remains; no user-selected locations were reassigned.
- Native connectivity: 10/13 successful across two HTTPS probe targets. Moldova XHTTP and both USA XHTTP endpoints timed out on both attempts; their configs validate, but connectivity is unconfirmed. This is a reachability limitation, not a claim that all 83 servers are live.
- Sensitive diagnostic subscription/config files removed locally and from router `/tmp`; protected before/after operational snapshots retained in ignored backups/before-xray-update-2026-11-17.
