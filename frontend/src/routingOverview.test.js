import assert from "node:assert/strict";
import test from "node:test";
import { routingOverview, serverSwitchRequest, prioritySelectionMessage, routeDomainSources, domainSaveRequest } from "./routingOverview.js";

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

test("automatic selection deletes only the override", () => {
  const row = routingOverview(config)[0];
  assert.deepEqual(serverSwitchRequest(row, ""), { path: "/api/priority-policies/common-policy/override", method: "DELETE" });
});

test("inline site editing preserves shared and rule-owned domains", () => {
  const mixed = { ...config, rules: config.rules.map(r => r.id === "main" ? { ...r, domains: ["private.example"] } : r) };
  const sources = routeDomainSources(mixed, routingOverview(mixed)[0]);
  assert.deepEqual(sources.map(s => [s.id, s.entries]), [["shared", ["youtube.com", "chatgpt.com"]], ["main", ["private.example"]]]);
  assert.deepEqual(domainSaveRequest(mixed, sources[0], ["youtube.com"]), { path: "/api/config/shared-domains", method: "PUT", body: { entries: ["youtube.com"] } });
  const request = domainSaveRequest(mixed, sources[1], ["new.example"]);
  assert.equal(request.path, "/api/rules/main?apply=1");
  assert.deepEqual(request.body, { name: "Main", providerId: "vpn", selectedLocation: "Latvia", enabled: true, domains: "new.example" });
});

test("provider-wide lists keep separate editable sources", () => {
  const whole = { ...config, priorityPolicies: [{ id: "all", providerId: "vpn", enabled: true, targets: [] }] };
  assert.deepEqual(routeDomainSources(whole, routingOverview(whole)[0]).map(s => s.id), ["shared", "ai"]);
});

test("empty main and named policy lists stay editable but empty reserves stay hidden", () => {
  const empty = { ...config, sharedDomains: { ...config.sharedDomains, entries: [] }, rules: config.rules.map(r => ({ ...r, domains: [] })) };
  assert.deepEqual(routingOverview(empty).map(r => r.ruleId), ["main", "ai"]);
  assert.equal(routeDomainSources(empty, routingOverview(empty)[0])[0].id, "shared");
  assert.deepEqual(routeDomainSources(empty, routingOverview(empty)[1])[0].entries, []);
});

test("backend-disabled empty effective rules remain editable for shared and named lists", () => {
  const saved = config.rules.map(r => ({ ...r, domains: [] }));
  const empty = { ...config, rules: saved, effectiveRules: saved.map(r => ({ ...r, enabled: false })), sharedDomains: { ...config.sharedDomains, entries: [] } };
  assert.deepEqual(routingOverview(empty).map(r => r.ruleId), ["main", "ai"]);
  assert.deepEqual(routingOverview({ ...empty, priorityPolicies: [] }).map(r => r.ruleId), ["main"]);
});

test("empty API lists serialized as null can receive their first site", () => {
  const empty = { ...config, sharedDomains: { ...config.sharedDomains, entries: null }, rules: config.rules.map(r => ({ ...r, domains: null })) };
  const rows = routingOverview(empty);
  assert.deepEqual(domainSaveRequest(empty, routeDomainSources(empty, rows[0])[0], ["first.example"]).body.entries, ["first.example"]);
  assert.equal(domainSaveRequest(empty, routeDomainSources(empty, rows[1])[0], ["first.example"]).body.domains, "first.example");
});

test("stale site drafts cannot overwrite another edit; current server fields are preserved", () => {
  const source = routeDomainSources(config, routingOverview(config)[1])[0];
  const newer = { ...config, rules: config.rules.map(r => r.id === "ai" ? { ...r, selectedLocation: "Sweden" } : r) };
  assert.equal(domainSaveRequest(newer, source, ["example.com"]).body.selectedLocation, "Sweden");
  const changed = { ...config, rules: config.rules.map(r => r.id === "ai" ? { ...r, domains: ["changed.example"] } : r) };
  assert.throws(() => domainSaveRequest(changed, source, []), /changed/);
  assert.throws(() => domainSaveRequest({ ...config, rules: [] }, source, []), /changed/);
});

test("degraded policies identify the affected route and respect a provider fallback", () => {
  const statuses = new Map([
    ["common-policy", { blocked: true, locationIssue: "missing from subscription" }],
    ["ai-policy", { activeLocation: "Germany", locationIssue: "retained runtime" }],
  ]);
  const [blocked, retained] = routingOverview(config, statuses);
  assert.equal(blocked.blocked, true);
  assert.equal(blocked.activeLocation, "");
  assert.equal(retained.activeLocation, "Germany");
  assert.equal(retained.locationIssue, "retained runtime");
  const [replaced] = routingOverview(config, statuses, new Map([["main", { mode: "provider", activeLocation: "Finland" }]]));
  assert.equal(replaced.activeLocation, "Finland");
  assert.equal(replaced.blocked, false);
  assert.equal(replaced.locationIssue, "");
});

test("selection feedback requires confirmed server and names the automatic server", () => {
  const translated = (key, values) => [key, values?.location].filter(Boolean).join(" ");
  assert.equal(prioritySelectionMessage({ status: "override_cleared", activeLocation: "Sweden" }, translated), "connections.priorityAutomaticActive Sweden");
  assert.equal(prioritySelectionMessage({ status: "override_set", activeLocation: "Latvia" }, translated), "connections.priorityManualActive Latvia");
  assert.throws(() => prioritySelectionMessage({ status: "override_set" }, translated), /priority_unconfirmed/);
});
