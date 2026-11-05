# Server ping and automatic selection

**Goal:** Show router-to-server ICMP ping beside current and candidate VPN servers and allow automatic selection to use it.

Keep server ping separate from the existing HTTPS-through-tunnel health latency. Refresh a deduplicated set of configured endpoints at most once per minute; do not create tunnels just to measure ping. Cache by subscription source and server, publish timestamps/errors, and never interpret a missing ping as zero milliseconds or proof that VPN is down.

Selection remains configurable per policy: configured order or lower ping. Preserve manual overrides and schedule windows. For latency selection, retain health checks and failure grace; require a meaningful, sustained improvement before replacing a working tunnel. Use recent averaged samples and ignore stale measurements.

- [x] Add bounded ping collection, freshness, status fields, and regression tests.
- [x] Add selection mode and stable latency ranking with tests for health, unknown/stale pings, manual/scheduled choices, and jitter.
- [x] Show ping in current routing, server choices, and automatic-selection settings; expose selection mode.
- [x] Verify UI with synthetic data, full Go/frontend checks, and independent review.
- [x] Deploy with rollback, enable the requested mode, and verify actual router ping/status and Wi-Fi.

Source integrated into `main` as `9ee7bce`; the packaged binary and deployment evidence accompany it. See ../../2026-11-05-server-ping.md for verification results.
