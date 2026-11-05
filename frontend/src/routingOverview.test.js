import assert from "node:assert/strict";
import test from "node:test";
import { routingOverview, serverSwitchRequest } from "./routingOverview.js";

const config = {
  providers: [{ id: "vpn", name: "Subscription", enabled: true }],
  rules: [
    { id: "main", providerId: "vpn", name: "Main", selectedLocation: "Latvia", domains: [], enabled: true },
    { id: "backup", providerId: "vpn", selectedLocation: "Sweden", domains: [], enabled: true },
    { id: "ai", providerId: "vpn", name: "AI", selectedLocation: "Germany", domains: ["api.deepseek.com"], enabled: true },
  ],
  sharedDomains: { entries: ["youtube.com", "chatgpt.com"], ruleIds: ["main", "backup"] },
  priorityPolicies: [
    { id: "common-policy", name: "Priority", providerId: "vpn", ruleId: "main", enabled: true, targets: [{ location: "Latvia" }, { location: "Sweden" }] },
    { id: "ai-policy", name: "DeepSeek API", providerId: "vpn", ruleId: "ai", enabled: true, targets: [{ location: "Germany" }, { location: "Latvia" }] },
  ],
};

test("current scheme keeps shared and custom decisions separate", () => {
  const statuses = new Map([["common-policy", { activeLocation: "Sweden" }], ["ai-policy", { activeLocation: "Germany", overrideLocation: "Germany" }]]);
  const rows = routingOverview(config, statuses);
  assert.deepEqual(rows.map(row => [row.ruleId, row.shared, row.count, row.activeLocation, row.policyId]), [
    ["main", true, 2, "Sweden", "common-policy"], ["ai", false, 1, "Germany", "ai-policy"],
  ]);
  assert.equal(rows[1].name, "DeepSeek API");
  assert.equal(rows[1].overrideLocation, "Germany");
  assert.equal(rows.length, 2, "empty reserve servers must not appear as separate routes");
});

test("shared default uses the actual synthetic policy and can be switched", () => {
  const rows = routingOverview({ ...config, priorityPolicies: [] }, new Map([["shared-domain-list", { activeLocation: "Sweden" }]]));
  assert.equal(rows[0].policyId, "shared-domain-list");
  assert.deepEqual(rows[0].targets, ["Latvia", "Sweden"]);
  assert.equal(rows[0].activeLocation, "Sweden");
  assert.deepEqual(serverSwitchRequest(rows[0], "Latvia"), { path: "/api/priority-policies/shared-domain-list/override", method: "POST", body: { location: "Latvia" } });
});

test("disabled providers disappear and missing runtime is not a confirmed active server", () => {
  assert.deepEqual(routingOverview({ ...config, providers: [{ ...config.providers[0], enabled: false }] }, new Map()), []);
  const rows = routingOverview(config, new Map());
  assert.equal(rows[0].activeLocation, "");
  assert.equal(rows[0].configuredLocation, "Latvia");
});

test("a fixed custom route keeps its fields when switching", () => {
  const row = routingOverview({ ...config, priorityPolicies: [] }, new Map()).find(row => row.ruleId === "ai");
  assert.equal(row.activeLocation, "");
  assert.deepEqual(serverSwitchRequest(row, "Latvia"), {
    path: "/api/rules/ai?apply=1", method: "PUT",
    body: { name: "AI", providerId: "vpn", selectedLocation: "Latvia", domains: "api.deepseek.com", enabled: true },
  });
});

test("disabled explicit shared policy does not silently enable synthetic automation", () => {
  const rows = routingOverview({ ...config, priorityPolicies: config.priorityPolicies.map(p => ({ ...p, enabled: false })) }, new Map([["shared-domain-list", { activeLocation: "Old" }]]));
  assert.equal(rows[0].policyId, null);
  assert.equal(rows[0].activeLocation, "");
  assert.equal(rows[0].canQuickSwitch, false);
  assert.throws(() => serverSwitchRequest(rows[0], "Sweden"), /Configure automatic/);
});

test("provider-wide policy appears once for its entire scope", () => {
  const rows = routingOverview({ ...config, priorityPolicies: [{ id: "whole", providerId: "vpn", name: "All sites", enabled: true, targets: [{ location: "Sweden" }] }] }, new Map([["whole", { activeLocation: "Sweden" }]]));
  assert.equal(rows.length, 1);
  assert.equal(rows[0].count, 3);
  assert.equal(rows[0].policyId, "whole");
});

test("current scheme shows actual failover instead of the preferred policy server", () => {
  const statuses = new Map([["common-policy", { activeLocation: "Latvia" }]]);
  const overrides = new Map([["__priority_policy_common-policy", { mode: "provider", activeLocation: "Norway", activeProviderName: "Backup VPN" }]]);
  const row = routingOverview(config, statuses, overrides)[0];
  assert.equal(row.activeLocation, "Norway");
  assert.equal(row.providerName, "Backup VPN");
  assert.equal(row.failover, true);
  const unavailable = routingOverview(config, statuses, new Map([["main", { mode: "direct" }]]))[0];
  assert.equal(unavailable.activeLocation, "");
  assert.equal(unavailable.unavailable, true);
});

test("overview consumes backend effective ownership and retains saved fields", () => {
  const legacy = {
    ...config, priorityPolicies: [],
    rules: config.rules.map(rule => rule.id === "backup" ? { ...rule, domains: ["youtube.com", "192.0.2.0/24"] } : rule),
    effectiveRules: [
      { ...config.rules[0], domains: ["youtube.com", "chatgpt.com"], enabled: true },
      { ...config.rules[1], domains: [], enabled: false },
      config.rules[2],
    ],
  };
  const rows = routingOverview(legacy, new Map());
  assert.deepEqual(rows.map(row => row.ruleId), ["main", "ai"]);
  assert.deepEqual(rows[0].targets, ["Latvia", "Sweden"], "inactive reserves remain available for switching");
  assert.equal(rows[0].rule, legacy.rules[0], "runtime domains must never replace saved rule fields");
});
