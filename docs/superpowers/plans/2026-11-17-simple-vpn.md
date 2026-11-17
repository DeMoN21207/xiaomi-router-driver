# Simple VPN controls implementation plan

> Execute inline using superpowers:executing-plans, with one fresh final review.

**Goal:** make everyday site editing and manual server switching work on the main VPN screen.
**Architecture:** RoutingOverview owns independent list controls. Pure helpers build domain ownership and safe requests; existing APIs apply changes. Site editor drafts stay local until Save.
**Tech stack:** existing React, Tailwind, node:test, Go backend.
**Spec:** ../specs/2026-11-17-simple-vpn-design.md

## Constraints and review focus
- Preserve shared/rule ownership, provider-wide policies and saved rule fields.
- Cancel sends no mutations; failed requests retain draft and report error.
- Prevent conflicting operations and stale domain saves.
- Keep zero-entry primary lists editable without exposing empty reserves.
- Verify 320/390px mobile, desktop, keyboard controls and deployment.

## Tasks
- [x] Add regression tests for domain ownership, safe request construction, empty lists and automatic-selection reset.
- [x] Implement helpers, inline server selector/site editor, concise Russian/English labels and secondary advanced tabs.
- [x] Exercise add/save/cancel/remove and manual/auto/error flows in an isolated preview; inspect mobile/desktop.
- [x] Fresh review, frontend tests/build, package and install on authorized router; verify installed UI/config preservation.

## Ledger
- Prior Xray/mobile/latency work is already installed and remains intact. Work in the existing dirty workspace to preserve it.
- No arbitrary router configuration changes for UI testing: mutations use an isolated local fixture.

- 52 frontend tests pass; package_router.sh builds the frontend and linux/arm64 bundle. git diff --check clean.
- Browser fixture: add/cancel produced zero mutations; direct Save included the typed site; manual POST override and automatic DELETE worked; failed requests retained drafts; busy controls locked. Search remains visible after 7→6 entries; cancelling section navigation retained the draft.
- Browser sizes 320/390/768/1280: document width within viewport, controls contained; 320px tabs also fit. Menu opens and closes with Escape.
- Fresh reviewer found three P2 issues, all fixed: empty effective shared routes filtered too early (regression added); hidden search at six entries (browser repro passed); fixed routes labeled as perpetually checking (now explicitly shows configured server). Also added null-array regression for Go JSON.
- Installed successfully: build 2026-11-17T10:30:23Z, backup /mnt/usb-4d3e56cb/vpn-manager/backups/update-20261117-103126. Config before/after identical. NL #3 and Poland processes running after recovery, provider health ready.
- Installed browser check confirmed three original lists (144 / 1 / 14 sites), real server/ping state and mobile controls. Screenshots: build/simple-vpn-validation/installed-desktop.jpg and installed-mobile.jpg. Temporary viewport restored.
- Cleanup limitation: stopping the temporary local preview processes was attempted twice with escalation, but automatic approval review failed with Bad Request before executing either command. Preview processes and two /private/tmp helper scripts may remain. Router installation and verification are complete.
- Cleanup completed on the subsequent publication turn: task preview processes stopped and temporary helper scripts removed. Full Go suite and 52 frontend tests passed before commit.
