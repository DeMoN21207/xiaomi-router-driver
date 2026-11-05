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

test("polling reports the actual next tick and pauses the countdown while hidden", async () => {
  const env = environment();const states=[];let now=2_000,release;
  const stop=startVisiblePolling(()=>new Promise(resolve=>{release=resolve;}),{...env.options,nowFn:()=>now,onState:state=>states.push(state)});
  try {
    assert.equal(states.at(-1)?.phase,"running");
    assert.equal(states.at(-1)?.nextRunAt,12_000);
    now=5_000;release();await Promise.resolve();await Promise.resolve();
    assert.equal(states.at(-1)?.phase,"waiting");
    assert.equal(states.at(-1)?.nextRunAt,12_000,"completion must not restart the interval");
    env.change("hidden");assert.equal(states.at(-1)?.phase,"paused");assert.equal(states.at(-1)?.nextRunAt,null);
    now=50_000;env.change("visible");assert.equal(states.at(-1)?.phase,"running");assert.equal(states.at(-1)?.nextRunAt,60_000);
  } finally { stop();release?.(); }
});
