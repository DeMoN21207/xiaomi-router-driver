# Device exclusions

Approved by user: every device, including new devices, follows configured routes by default. One button excludes a device; another click restores routes. An excluded device uses direct WAN whether or not its own VPN is running. Apply immediately without a second save action; preserve exclusions across reconnects/restarts. Keep ordinary router firewall protection.

## Approach

Use a MAC-based exception list, separate from site routing configuration. Source IP alone is unsafe when DHCP reassigns addresses; automatic VPN detection is explicitly out of scope. Provide a dedicated Devices page, with name, MAC/IP, configured mode and a single action. Keep excluded offline devices visible so users can restore them.

Persist the exceptions independently in SQLite; ordinary configuration saves cannot erase them. Render a MAC list consumed by the existing serialized routing script. Match excluded LAN traffic before VPN marking, route it through the main routing table and bypass only the manager's VPN guards and DNS interception. Do not bypass the normal filter/FORWARD firewall. Retire only the changed device's conntrack entries so DNS/NAT state follows the new setting.

IPv6 is globally disabled on this router and has no WAN default route. Preserve that global setting; exclusion must not re-enable IPv6 or weaken protection for other clients. When IPv6 is allowed by existing global settings, this IPv4-only manager does not capture its traffic.

## Failure and UI behavior

Show confirmed mode only, disable conflicting operations while applying, and display actionable inline errors. Roll back persistence/runtime state on apply failure. Restore the persisted runtime list before background route recovery starts. Retain exclusions when subscriptions, VPN servers, or routing rules change. Russian and English, keyboard support and mobile 320px layout.

## Validation

Tests: default empty list, MAC validation/canonicalization, persistence, stale config save isolation, offline entries, IP reassignment, apply/rollback, routing guard/DNS priority and targeted connection cleanup. Build and full Go/frontend suites. Fresh independent review. Router backup, install, UI mobile/desktop verification, safe synthetic-device toggle and kernel rule verification, preserve all real device defaults.
