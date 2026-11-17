# Device Exclusions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** One-click persistent device exclusions from router VPN routing, deployed on the router.
**Architecture:** Independently persisted MAC exclusions; a shared runtime file and serialized routing script restore bypass rules; authenticated API and a dedicated responsive device page.
**Tech Stack:** Go, SQLite, POSIX shell/iptables/ipset, React/Tailwind.
**Spec:** docs/superpowers/specs/2026-11-17-device-exclusions.md

## Global Constraints
- Default and new devices follow configured site routes.
- Manual exclude/restore only; no VPN detection, timer, or second save button.
- Preserve the normal firewall, unrelated connections and global IPv6 setting.
- SQLite and runtime updates must recover from failures; credentials never enter source or logs.

## Review Focus
- MAC changes and IP reassignment must not transfer an exception to a different device.
- Concurrent configuration saves and route recovery must retain exclusions.
- Existing pinned routes and WAN guards must not block excluded devices.
- Failed applies must not display success or silently retain a new exception.
- Offline excluded devices and failed refreshes must remain manageable on mobile.

### Task 1: Persistence and inventory
Files: internal/config/device_exclusions.go, state.go; internal/status/devices.go; matching tests.
Interfaces: config.DeviceExclusion{MAC,Name}; Manager.DeviceExclusions/SetDeviceExclusion; status.NetworkDevices(lanIface, exclusions).
- [x] Add/run failing tests for default, validation, persistence, stale Save, offline device and IP reassignment.
- [x] Implement independent SQLite table and LAN DHCP/ARP inventory, merge exclusions by MAC.
- [x] Run config/status tests and record evidence.

### Task 2: Routing and API
Files: internal/routing/device_exclusions.go, update_routes.sh, apply.go; internal/api/devices.go, handler.go; cmd/vpn-manager/main.go; tests.
Interfaces: Runner.SetDeviceExclusionsPath, ApplyDeviceExclusions(ctx, settings, exclusions, changedIPs); GET/PUT /api/devices with {mac,excluded,name}.
- [x] Add/run failing tests for script bypass semantics, atomic file update and API rollback.
- [x] Implement source MAC set, main-table bypass mark, DNS exception and guard RETURN; restore at startup and after every route update; clear changed source connections only.
- [x] Implement serialized API transaction, rollback, config read-only exclusion snapshot and accurate traffic classification.
- [x] Run routing/API/config/status tests and record evidence.

### Task 3: Device page
Files: frontend/src/pages/DevicesPage.jsx, App.jsx, components/Layout.jsx, i18n.jsx.
- [x] Implement confirmed-only one-click actions, busy/error states, search, offline exclusions and responsive cards.
- [x] Run frontend tests/build; exercise exclude/restore and error state in browser at mobile and desktop widths.

### Task 4: Review and deployment
- [x] Run complete suites, shell syntax check and diff check; request fresh independent review and resolve important findings.
- [x] Build router package, preserve local/router backups, install through existing update API.
- [x] Verify installed assets, default empty exclusions, synthetic MAC toggle/kernel rules, restore defaults, VPN health and mobile interface.
- [x] Record verification and report the installed result.

## Execution ledger
- Base: db353ee; clean working tree before task. Branch codex/device-exclusions in canonical project directory, keeping existing tools/index and router deployment context.
- Baseline: full Go suite had one intermittent TestNativeXrayRollbackPreservesEngine failure; isolated rerun passed. Must verify full suite again before shipping.
- Router: backup saved in backups/before-device-exclusions-20261117; strict approved SSH key verified; conntrack available; IPv6 disabled globally and no IPv6 WAN default.

- Task 1 complete: config/status suites passed; independent persistence survives stale full config saves; inventory keeps offline exclusions and current ARP ownership.
- Task 2 complete: routing/API tests passed, including RED→GREEN for rejection of busy routing transactions and target connection cleanup on rollback (independent review P2 findings).
- Task 3 complete: 52 frontend tests; build passed; browser verified toggle/restore/error, mobile menu and 320px layout (no overflow, 46px buttons), desktop 1280px.
- Baseline test fix: native Xray mock now reports TUN readiness only after its process writes a PID marker; eliminates premature mock teardown. Full Go suite passed.
- Review: fresh reviewer device_exclusions_review; two P2 findings fixed with failing/passing regressions; kernel behavior will be validated on router. No deferred product issues.
- Ruling: existing global IPv6 disable remains; exclusion does not turn on IPv6 for other clients.

- Deployment complete: installed build 2026-11-17T13:35:11Z; router backup /mnt/usb-4d3e56cb/vpn-manager/backups/update-20261117-133717.
- Live verification: synthetic MAC persisted in SQLite and kernel set; real Mac temporarily excluded then restored; WAN route, Wi-Fi-bound TCP and DNS probes passed. Positive mangle/guard and DNS-bypass counters verified. All 11 devices follow configured routes, empty exception set, original providers/rules/policies/settings identical.
- The existing missing Sweden #4 subscription-target error is unchanged from the pre-install snapshot; this feature does not edit subscriptions.
- Installed UI verified at desktop and 390px; screenshots in build/device-exclusions-validation/.
