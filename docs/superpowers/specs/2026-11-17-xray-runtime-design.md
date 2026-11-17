# Native Xray subscription runtime

The user requested working support for the 13 subscription entries omitted by the JSON compatibility importer: XHTTP (10) and VLESS Encryption (3). Continue the already authorized router deployment; preserve the confirmed SSH fingerprint and existing rules.

Use Xray's native Linux TUN inbound for these entries, keeping sing-box for entries already supported. Preserve the complete VLESS outbound, including XHTTP downloadSettings, encryption and finalmask. The existing policy routing owns routes; Xray must not install default routes or change system DNS. A local SOCKS inbound is used only for standby probes.

Alternatives considered: converting to sing-box loses unsupported parameters; a SOCKS sidecar requires two coordinated processes per tunnel. Native Xray TUN uses the existing one-process lifecycle. This is the implementer's choice based on the current official Xray TUN documentation.

Persist engine identity through a distinct `.xray.json` runtime config filename. Use that identity consistently for start, process validation, restart recovery, reuse, cleanup and rollback. No database migration is necessary. Check executable availability before stopping active tunnels.

Package a pinned official Xray Linux arm64 release with SHA256 verification. Uploaded bundles declaring xray_path must contain that binary; older bundles remain valid. Tests cover lossless JSON parsing, lifecycle and standby probes, and bundle validation. Validate all live entries with their actual engine and verify an isolated TUN plus real SOCKS connections on the router before deploying. Deleted/renamed rule selections are a separate pre-existing configuration issue; do not silently choose replacement servers.
