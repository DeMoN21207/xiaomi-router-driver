# Routing workspace implementation

**Goal:** Show current routing and switching first; put domain/automatic routing configuration and subscription catalogue into distinct sections.

**Spec:** ../specs/2026-11-05-routing-workspace-design.md

- [x] Add a tested overview model for shared, custom, synthetic-policy, fixed, disabled, and missing-runtime cases.
- [x] Add current-routing view with per-list server switching and return-to-auto controls through existing APIs.
- [x] Separate catalogue, domain editor, and automatic selection editor with explicit section controls.
- [x] Replace native disclosures and keep each editor's actions with its fields; preserve policy scope when editing.
- [x] Verify desktop/mobile section visibility and switching with a synthetic API; run frontend tests/build and Go checks for embedded UI.
- [x] Review, integrate into main, deploy with rollback, and verify configuration fingerprint and actual Wi-Fi traffic.

Review focus: independent DeepSeek decisions, synthetic shared policy identity, fixed routes not falsely shown as runtime-confirmed, switching errors and draft retention, disabled subscriptions, empty membership, and accessible controls.
