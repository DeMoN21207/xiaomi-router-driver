# Mobile layout and latency repair

User request: investigate many 1 ms entries and repair mobile navigation, tables and responsive layouts. Continue the already authorized router update after verification.

## Findings
- Router ping for fazuno.net, fazzio.me and fexzoo.net resolves 0.0.0.0 and receives sub-millisecond replies from 127.0.0.1. Seven catalogue entries share these hosts. Their actual HTTPS tunnel checks fail. An explicit 1.1.1.1 DNS query from the router also returned 0.0.0.0; no claim about authoritative DNS or interference is made.
- Sidebar is hidden below 768 px with no mobile navigation control.
- Catalogue header and rows have a fixed minimum grid wider than phones; header is clipped while body scrolls separately.
- Traffic rows require horizontal scrolling, and hidden Settings hints enlarge document width.
- Candidate HTTPS checks still selected sing-box for Xray entries; centralize engine resolution with standby checks.

## Work and verification
- [x] Reproduce backend failures and mobile defects.
- [x] Reject local/non-routable resolved ping targets and pin the actual probed IP; add regression tests.
- [x] Correct per-entry engine for candidate checks; add regression test.
- [x] Add accessible mobile drawer and responsive server/traffic rows, contain settings hints.
- [x] Check pages at 320, 390, 768 and desktop widths, navigation closure/focus and long table data.
- [x] Run full tests/build, independent review, deploy with backup and verify live outcomes.

Preserve existing configuration and user-selected servers. Private router snapshots stay under ignored build/backups directories.

- All five routes checked at phone widths; connections/traffic/events/settings also checked at 768/1280. No document overflow. Catalogue switches by container width; traffic now switches by container width too, including tablet content beside sidebar. Server selection remains usable at320px.
- Native drawer: opening, route navigation, Escape returning focus to opener, keyboard wrap, desktop-resize dismissal and scroll unlock verified. Added explicit key wrap and guarded stale dialog close events after development hot reload.
- Fresh reviewer found one P2: hint buttons captured implicit field labels. Fixed explicit useId/htmlFor/id for all affected fields. Browser confirms all18 associations match; no consequential findings remain.
- On-router executable of updated latency code rejects all three 0.0.0.0 hosts; public control1.1.1.1 returns18ms. Provider JSON was inspected: no DNS hosts overrides explain these addresses.

- Final full Go suite,45 frontend tests, bundle build and diff whitespace check passed. Installed build2026-11-17T09:56:24Z through updater; backup `/mnt/usb-4d3e56cb/vpn-manager/backups/update-20261117-095715`.
- Live verification:83 entries, zero1ms measurements,7DNS errors for the previously false values; XHTTP HTTPS check succeeded453ms; XMC check ran its native engine but timed out during TLS. Both previously active VPNs are running and WAN is up. Configuration before/after identical.
- Installed UI verified in Chrome responsive viewport390px; menu and catalogue screenshots saved under build/mobile-validation. Verification used emulation, not a physical phone. Screenshots contain no subscription URL/key.
- Closed local preview/proxy; removed raw provider JSON and temporary helpers. Protected diagnostic snapshots retained under ignored backups/before-mobile-fix-2026-11-17.
