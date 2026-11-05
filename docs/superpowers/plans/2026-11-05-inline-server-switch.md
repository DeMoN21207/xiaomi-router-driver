# Inline server switching Implementation Plan

> **For agentic workers:** Use superpowers:executing-plans inline. Steps use checkbox syntax for tracking.

**Goal:** Switch a policy's active server by clicking its server chip in the automatic-routing list.

**Architecture:** Reuse POST/DELETE `/api/priority-policies/{id}/override` and the existing refresh callback. The UI reports confirmed runtime state and serializes changes; backend fallback semantics remain unchanged.

**Tech Stack:** React, existing fetchJSON client, Tailwind semantic tokens, Go router package.

**Spec:** `docs/superpowers/specs/2026-11-05-inline-server-switch-design.md`

## Global Constraints

- Preserve the existing layout, themes, two policy scopes, domain lists, MEGA prefix and ten candidates.
- A click applies directly; no new dialog or routing mode.
- A failed choice must leave the working route visible.

## Review Focus

- Double clicks and cross-policy clicks while a change is pending must not race.
- Ping failure alone must not prevent attempting a server.
- Disabled policies cannot be activated through server buttons.
- Synthetic shared-list policy IDs use the existing override endpoint.
- Busy, active and error feedback remains accessible and local to the affected list.

## Task 1: Direct switching

**Files:** `frontend/src/pages/ConnectionsPage.jsx`, `frontend/src/i18n.jsx`.

- [x] Demonstrate the missing interaction in a local preview before changing production code.
- [x] Add native server buttons, single-request guard, pending/error feedback, and return-to-auto control using existing API semantics and translated labels.
- [x] Verify click, pending, rejected server, independent policy, disabled policy and synthetic shared policy in the preview.
- [x] Run frontend tests/build and the existing manual-fallback tests; review the diff.

### Validation evidence

- Before the change, clicking the Germany text left Sweden active and sent no override request.
- After the change, clicking Germany changed only the common policy. A rejected Finland choice kept Germany active and showed the server error in that card. Return-to-auto restored Sweden.
- Double-clicking Germany in DeepSeek sent one POST for `ai-policy`; the common policy stayed on Sweden. Disabled policy buttons remained disabled.
- The synthetic shared-list policy accepted a keyboard selection and sent the override for `shared-domain-list`.
- Independent review found a navigation race: component-local pending state disappeared after visiting the overview and returning while a request was pending. Reproduced with a 20-second response before the fix. Lifted the operation, error state and synchronous lock to `ConnectionsPage`; repeated the same navigation, confirming all selection controls remained disabled and pending feedback remained visible (RED to GREEN).
- Fresh checks after the fix: 45/45 frontend tests, frontend build, full `go test -race ./...` and `go vet ./...` passed. Existing backend tests cover manual selection precedence, fastest fallback and outage grace.

## Task 2: Publish verified package

**Files:** existing generated UI assets and `build/router` binary/metadata.

- [x] Build the router package from committed source and deploy with rollback protection.
- [x] Verify the served controls, preserved domain/policy settings and router health; confirm deployment.
- [x] Record evidence and prepare the verified package for main integration.

### Deployment evidence

- Router source `3cfd281`, build `2026-11-05T20:01:08Z`, binary MD5 `7e1d98d177f7484221e5af34773ca49f`; installation confirmed with PID 30904. Backup: `/mnt/usb-4d3e56cb/vpn-manager/backups/inline-switch.KLBHgC`.
- Router serves `/assets/index-CYVSCyPL.js` with native server controls. Actual browser verification selected the current Sweden #4 server in the common policy, observed the manual badge, then returned it to automatic selection. DeepSeek remained independent; no manual override was left behind.
- Safe state comparison confirmed 155 shared entries, MEGA's `89.44.168.128/25`, all five saved rules, two policies with ten targets each, routing and automation settings unchanged.
- VPN health ready. Wi-Fi checks: ya.ru 302, YouTube 200, ChatGPT 200, DeepSeek API 401 without an API key. Runtime binaries and WAN route unchanged.
