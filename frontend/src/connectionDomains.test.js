import assert from "node:assert/strict";
import test from "node:test";
import { collectConnectionDomains, sharedDomainPriorityStatus } from "./connectionDomains.js";

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
