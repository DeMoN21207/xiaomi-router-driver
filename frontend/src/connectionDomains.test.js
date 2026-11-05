import assert from "node:assert/strict";
import test from "node:test";
import { activeConnectionRoutes, collectConnectionDomains, sharedDomainPriorityStatus } from "./connectionDomains.js";

test("lists each routing entry once and keeps all of its route assignments", () => {
  const rules = [
    { id: "de", providerId: "first", domains: ["api.deepseek.com", "149.154.160.0/20", "api.deepseek.com"] },
    { id: "nl", providerId: "second", enabled: false, domains: ["api.deepseek.com", "1.2.3.4"] },
    { id: "empty", providerId: "first" },
  ];

  assert.deepEqual(collectConnectionDomains(rules), [
    { entry: "1.2.3.4", rules: [rules[1]] },
    { entry: "149.154.160.0/20", rules: [rules[0]] },
    { entry: "api.deepseek.com", rules: [rules[0], rules[1]] },
  ]);
  assert.deepEqual(collectConnectionDomains([]), []);
});

test("shared-list labels show the saved enabled policy's active server", () => {
  const selected = { policyId: "main", activeLocation: "Latvia" };
  const map = new Map([["main", selected], ["old", { policyId: "old" }]]);
  const policies = [{ id: "old", ruleId: "primary", enabled: false }, { id: "main", ruleId: "primary", enabled: true }];
  const rules = [{ id: "primary", enabled: true }];
  assert.equal(sharedDomainPriorityStatus({ ruleIds: ["primary"] }, rules, policies, map), selected);
  const legacy = { policyId: "shared-domain-list", activeLocation: "Sweden" };
  assert.equal(sharedDomainPriorityStatus({ ruleIds: ["primary"] }, rules, [], new Map([["shared-domain-list", legacy]])), legacy);
});
test("connection header keeps actual shared failover and separate custom server", () => {
  const list = { entries: ["youtube.com"], ruleIds: ["main", "reserve"] };
  const rules = [{ id: "main", providerId: "vpn", enabled: true }, { id: "reserve", providerId: "vpn", enabled: true }, { id: "custom", providerId: "vpn", enabled: true, domains: ["api.deepseek.com"] }];
  const policies = [{ id: "deepseek", providerId: "vpn", ruleId: "custom", name: "DeepSeek API", enabled: true }];
  const statuses = new Map([["shared-domain-list", { activeLocation: "Sweden" }], ["deepseek", { activeLocation: "Germany" }]]);
  assert.deepEqual(activeConnectionRoutes("vpn", list, rules, policies, statuses), [
    { id: "shared-domain-list", location: "Sweden", shared: true },
    { id: "deepseek", name: "DeepSeek API", location: "Germany", shared: false },
  ]);
  assert.deepEqual(activeConnectionRoutes("other", list, rules, policies, statuses), []);
});

test("connection header labels the configured shared policy only once", () => {
  const list = { entries: ["youtube.com"], ruleIds: ["main"] };
  const rules = [{ id: "main", providerId: "vpn", enabled: true }];
  const policies = [{ id: "saved", providerId: "vpn", ruleId: "main", name: "Subscription priority", enabled: true }];
  const statuses = new Map([["saved", { activeLocation: "Latvia" }], ["shared-domain-list", { activeLocation: "Old" }]]);
  assert.deepEqual(activeConnectionRoutes("vpn", list, rules, policies, statuses), [
    { id: "saved", name: "Subscription priority", location: "Latvia", shared: true },
  ]);
});

test("provider-wide policy keeps its name when there is no shared selection", () => {
  const policies = [{ id: "whole", providerId: "vpn", name: "My connection", enabled: true }];
  assert.deepEqual(activeConnectionRoutes("vpn", { ruleIds: [] }, [], policies, new Map([["whole", { activeLocation: "Latvia" }]])), [
    { id: "whole", name: "My connection", location: "Latvia", shared: false },
  ]);
});

test("empty or disabled own routes do not appear as active traffic in the header", () => {
  const policies = [{ id: "old", providerId: "vpn", ruleId: "empty", enabled: true }];
  const rules = [{ id: "empty", providerId: "vpn", enabled: true, domains: [] }];
  const statuses = new Map([["old", { activeLocation: "Latvia" }], ["shared-domain-list", { activeLocation: "Sweden" }]]);
  assert.deepEqual(activeConnectionRoutes("vpn", { ruleIds: ["empty"], entries: [] }, rules, policies, statuses), []);
});
