import assert from "node:assert/strict";
import test from "node:test";
import { startVisiblePolling } from "./visiblePolling.js";

function environment(initial = "visible") {
  const listeners = new Set(), timers = new Map();
  let nextID = 0;
  const visibility = {
    visibilityState: initial,
    addEventListener: (_, listener) => listeners.add(listener),
    removeEventListener: (_, listener) => listeners.delete(listener),
  };
  return {
    options: { visibility, setIntervalFn: (fn, ms) => { assert.equal(ms, 10_000); timers.set(++nextID, fn); return nextID; }, clearIntervalFn: id => timers.delete(id) },
    async tick() { for (const fn of [...timers.values()]) await fn(); },
    change(state) { visibility.visibilityState = state; for (const fn of listeners) fn(); },
    timers, listeners,
  };
}

test("visible polling runs immediately, pauses when hidden, resumes and stops on disposal", async () => {
  const env = environment();let calls = 0;
  const stop = startVisiblePolling(async () => { calls++; }, env.options);
  await Promise.resolve();assert.equal(calls, 1);
  await env.tick();assert.equal(calls, 2);
  env.change("hidden");assert.equal(env.timers.size, 0);
  await env.tick();assert.equal(calls, 2);
  env.change("visible");await Promise.resolve();assert.equal(calls, 3);
  stop();assert.equal(env.timers.size, 0);assert.equal(env.listeners.size, 0);
  env.change("visible");await env.tick();assert.equal(calls, 3);
});

test("pending probes do not overlap and are aborted when leaving the visible page", async () => {
  const env = environment();let calls = 0, release, signal;
  const stop = startVisiblePolling(value => { calls++;signal = value;return new Promise(resolve => { release = resolve; }); }, env.options);
  await env.tick();assert.equal(calls, 1);
  env.change("hidden");assert.equal(signal.aborted, true);
  stop();release();await Promise.resolve();await env.tick();assert.equal(calls, 1);
});

test("hidden pages start no requests, and a failed probe does not stop future checks", async () => {
  const env = environment("hidden");let calls = 0;
  const stop = startVisiblePolling(async () => { calls++;if (calls === 1) throw Error("network unavailable"); }, env.options);
  assert.equal(calls, 0);assert.equal(env.timers.size, 0);
  env.change("visible");await Promise.resolve();assert.equal(calls, 1);
  await env.tick();assert.equal(calls, 2);stop();
});
