import assert from "node:assert/strict";
import test from "node:test";
import { priorityPing } from "./priorityPing.js";

const now = Date.parse("2026-11-05T12:00:00Z");
test("shows recent server ping rather than HTTPS health latency", () => {
  assert.deepEqual(priorityPing({ pingMs: 42, pingCheckedAt: "2026-11-05T11:59:45Z", latencyMs: 900 }, now), { state: "ready", ms: 42 });
  assert.deepEqual(priorityPing({ latencyMs: 900, lastChecked: "2026-11-05T11:59:45Z" }, now), { state: "unknown" });
});
test("timeouts and zero values are not fast servers", () => {
  assert.deepEqual(priorityPing({ pingMs: 0, pingError: "timeout", pingCheckedAt: "2026-11-05T11:59:45Z" }, now), { state: "unavailable" });
  assert.deepEqual(priorityPing({ pingMs: 0, pingCheckedAt: "2026-11-05T11:59:45Z" }, now), { state: "unknown" });
});
test("stale or invalid timestamps do not advertise an old low ping", () => {
  assert.deepEqual(priorityPing({ pingMs: 5, pingCheckedAt: "2026-11-05T11:57:00Z" }, now), { state: "stale" });
  assert.deepEqual(priorityPing({ pingMs: 5, pingCheckedAt: "bad date" }, now), { state: "unknown" });
  assert.deepEqual(priorityPing({ pingMs: 5, pingCheckedAt: "2026-11-05T13:00:00Z" }, now), { state: "stale" });
});
