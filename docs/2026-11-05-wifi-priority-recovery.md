# Wi-Fi VPN and Priority recovery — 2026-11-05

## Findings

- The deployed November shared-domain/rule-scoped policy changes existed only in a separate working copy. They are restored here so the repository can reproduce the router binary.
- Shared-domain routing replaced the saved primary policy ID with `shared-domain-list`. The Connections page keyed status and manual selection by the saved ID, leaving the primary card without an active target.
- Retired DNS snippets referenced the persistent protection set. Cleanup incorrectly counted that set as a live VPN route. Conflicting DNS selectors then learned new destination addresses only into the protection set, without a routing mark, and firewall protection blocked their Wi-Fi traffic.

## Fix

- An explicit primary policy keeps its ID, name, targets, schedule and enabled state. Enabled policies take precedence over disabled cards for the same rule. Membership-derived failover remains the default when no explicit policy exists.
- Both the shared-list header and policy cards resolve the same runtime status. Shared-list rules and independent rules (including DeepSeek) retain separate routing scopes.
- DNS cleanup checks real routing sets. It prunes retired selectors covered by a live replacement, including subdomains and mixed selectors. Unmatched stopped-domain selectors and historical IP protection remain intact.
- Cleanup runs after rendering a replacement and while retiring a tunnel, so an old selector cannot reappear after failover.

## Verification

- Regression tests exercise saved-policy identity/controls and execute the real shell DNS cleanup/deletion functions against temporary fragments.
- `go test -race ./...`, `go vet ./...`, frontend tests/build and shell syntax validation passed. Frontend suite: 16 tests.
- Before repair, HTTPS over the physical Wi-Fi interface timed out. After retiring stale DNS fragments, tests using the router's DNS and `curl -4 --interface en0 --noproxy '*' --resolve ...` returned: ordinary internet 302, YouTube 301, ChatGPT 200, DeepSeek models 401 without an API key.
- The one-time retired-fragment backup is `/mnt/usb-4d3e56cb/vpn-manager/backups/wifi-stale-dns.DpmkJH`.

Deployment must preserve the installed VPN executables, data and startup configuration; verify the saved rule/policy fingerprint, active targets, tunnel routes and physical Wi-Fi access after activation. Keep the previous application binary available for rollback.
