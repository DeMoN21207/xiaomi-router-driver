import test from "node:test";
import assert from "node:assert/strict";

import { applyRules, subscribeLoading, refreshSubscription } from "./api.js";

test("applyRules waits for the background operation", async () => {
  const requests = [];
  const responses = [
    { operation: { id: "apply_1", status: "queued" } },
    { operation: { id: "apply_1", status: "running" } },
    { operation: { id: "apply_1", status: "succeeded", result: { status: "applied", rulesApplied: 2 } } },
  ];
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (url, options = {}) => {
    requests.push([url, options.method || "GET"]);
    return { ok: true, json: async () => responses.shift() };
  };
  try {
    const result = await applyRules({ pollIntervalMs: 0 });
    assert.equal(result.status, "applied");
    assert.deepEqual(requests, [
      ["/api/rules/apply", "POST"],
      ["/api/rules/apply/apply_1", "GET"],
      ["/api/rules/apply/apply_1", "GET"],
    ]);
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test("applyRules reports a failed background operation", async () => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (url) => ({
    ok: true,
    json: async () => url === "/api/rules/apply"
      ? { operation: { id: "apply_2", status: "queued" } }
      : { operation: { id: "apply_2", status: "failed", error: "routing failed" } },
  });
  try {
    await assert.rejects(() => applyRules({ pollIntervalMs: 0 }), /routing failed/);
  } finally {
    globalThis.fetch = originalFetch;
  }
});

test("fetchJSON aborts a request after its deadline", async () => {
	const { fetchJSON } = await import("./api.js");
	const originalFetch = globalThis.fetch;
	globalThis.fetch = async (_url, options) => new Promise((_resolve, reject) => {
		options.signal.addEventListener("abort", () => reject(options.signal.reason), { once: true });
	});
	try {
		await assert.rejects(() => fetchJSON("/api/stuck", { timeoutMs: 10 }), /время ожидания/i);
	} finally {
		globalThis.fetch = originalFetch;
	}
});

test("refreshSubscription allows the full subscription refresh window", async () => {
	const api = await import("./api.js");
	assert.equal(typeof api.refreshSubscription, "function", "refreshSubscription must be exported");

	const originalFetch = globalThis.fetch;
	const originalSetTimeout = globalThis.setTimeout;
	const observedTimeouts = [];
	let observedRequest;
	globalThis.setTimeout = (callback, delay, ...args) => {
		observedTimeouts.push(delay);
		return originalSetTimeout(callback, 60_000, ...args);
	};
	globalThis.fetch = async (url, options = {}) => {
		observedRequest = { url, method: options.method || "GET" };
		return { ok: true, json: async () => ({ status: "refreshed", entries: 81 }) };
	};

	try {
		const result = await api.refreshSubscription("provider/sub");
		assert.equal(result.status, "refreshed");
		assert.deepEqual(observedRequest, {
			url: "/api/providers/provider%2Fsub/refresh",
			method: "POST",
		});
		assert.ok(observedTimeouts.includes(5 * 60 * 1000), `timeouts = ${observedTimeouts.join(", ")}`);
	} finally {
		globalThis.fetch = originalFetch;
		globalThis.setTimeout = originalSetTimeout;
	}
});

test("the operation loader stays active until background route application really finishes",async()=>{
 const originalFetch=globalThis.fetch,states=[];let release;
 const unsubscribe=subscribeLoading((count,operations)=>states.push({count,operations}));
 globalThis.fetch=async url=>({ok:true,json:()=>url==="/api/rules/apply"?Promise.resolve({operation:{id:"apply_long",status:"queued"}}):new Promise(resolve=>{release=()=>resolve({operation:{status:"succeeded",result:{status:"applied"}}});})});
 const pending=applyRules({pollIntervalMs:0});
 try {
  for(let i=0;i<12&&!release;i++)await Promise.resolve();
  assert.equal(typeof release,"function");
  assert.equal(states.at(-1).count,1,"the queued response must not hide the loader");
  assert.equal(states.at(-1).operations?.[0]?.labelKey,"loading.applyingRoutes");
  release();await pending;assert.equal(states.at(-1).count,0);
 }finally{release?.();await pending.catch(()=>{});unsubscribe();globalThis.fetch=originalFetch;}
});

test("subscription refresh reports its user-facing operation without leaking loader options to fetch",async()=>{
 const originalFetch=globalThis.fetch,states=[];let release,options;
 const unsubscribe=subscribeLoading((count,operations)=>states.push({count,operations}));
 globalThis.fetch=async(_url,value)=>{options=value;return{ok:true,json:()=>new Promise(resolve=>{release=()=>resolve({status:"refreshed"});})};};
 const pending=refreshSubscription("provider_1");
 try{
  for(let i=0;i<8&&!release;i++)await Promise.resolve();
  assert.equal(states.at(-1).operations?.[0]?.labelKey,"loading.subscriptionRefresh");
  assert.equal(options.loadingLabelKey,undefined);
  release();await pending;assert.equal(states.at(-1).count,0);
 }finally{release?.();await pending.catch(()=>{});unsubscribe();globalThis.fetch=originalFetch;}
});
