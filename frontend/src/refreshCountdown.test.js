import test from "node:test";
import assert from "node:assert/strict";
import { refreshCountdown } from "./refreshCountdown.js";

test("countdown follows the polling deadline and never reports negative or oversized time",()=>{
 const state={phase:"waiting",nextRunAt:12_000,intervalMs:10_000};
 assert.deepEqual(refreshCountdown(state,5_000),{seconds:7,remaining:0.7});
 assert.equal(refreshCountdown(state,5_100).seconds,7);
 assert.deepEqual(refreshCountdown(state,15_000),{seconds:0,remaining:0});
 assert.deepEqual(refreshCountdown(state,0),{seconds:10,remaining:1});
});
test("an active or paused operation has no invented completion percentage",()=>{
 for(const phase of ["running","paused"])assert.deepEqual(refreshCountdown({phase,nextRunAt:null,intervalMs:10_000},0),{seconds:null,remaining:null});
});
