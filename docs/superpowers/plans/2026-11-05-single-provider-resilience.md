# Single-provider resilience Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Recover a crashed manager promptly and retain only verified VPN backups on the existing subscription.

**Architecture:** Keep the firmware-compatible minute cron entry; schedule bounded one-shot checks at 10-second offsets. Kernel locking protects launch without leaving a stale lock after process death. Existing priority policies remain the VPN failover controller.

**Tech Stack:** Go, POSIX shell, BusyBox/flock, existing HTTP API.

**Spec:** `docs/2026-11-05-single-provider-resilience.md`

## Global Constraints

- One provider only; preserve 5-second outage confirmation, 60-second restore grace, `keep`, domains and rule ownership.
- Respect update journal/lock on every launch attempt; children never inherit the launch lock.
- No router reboot or WAN/USB disconnection; controlled crash targets only the verified manager executable.

## Review Focus

- One-shot checks must not recursively spawn timers.
- Removed scripts must not resurrect a disabled service.
- Concurrent launchers must not create duplicate manager processes.
- Update lock/journal must suppress all delayed attempts.
- Process recovery evidence includes working Wi-Fi VPN, not just a new PID.

## Task 1: Bounded fast watchdog

**Files:** `internal/automation/manager.go`, `internal/automation/manager_test.go`, new `internal/automation/cron_watchdog_test.go`.

- [x] Add failing tests for six scheduled checks, non-recursion, removed bootstrap and inherited-lock protection.
- [x] Update `renderCronBootstrapScript` and atomic bootstrap installation without changing the cron entry.
- [x] Run automation race tests, shell validation and full required checks; independently review the change.

## Task 2: Deploy and verify recovery

**Files:** packaged router binary/metadata and spec deployment results.

- [x] Snapshot safe configuration and stage a rollback-protected update.
- [x] Reinstall the same automation settings to refresh the watchdog; verify persistent cron.
- [x] Arm a bounded fallback, terminate only the verified manager, measure recovery and verify Wi-Fi, DNS, health and a single process.
- [x] Recheck candidates, retain verified backups and rerun a controlled tunnel failover if target settings change.
- [x] Preserve data, document evidence and prepare the verified router package for main integration.
