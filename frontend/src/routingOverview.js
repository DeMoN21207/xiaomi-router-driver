export function routingOverview(config, statusMap = new Map(), failoverMap = new Map()) {
  const savedRules = config?.rules || [];
  const rules = config?.effectiveRules || savedRules;
  const providers = config?.providers || [];
  const policies = config?.priorityPolicies || [];
  const shared = config?.sharedDomains || {};
  const primary = shared.ruleIds?.find(id => savedRules.some(rule => rule.id === id && rule.enabled));
  const rows = [];
  for (const provider of providers.filter(item => item.enabled)) {
    const providerRules = rules.filter(rule => rule.providerId === provider.id && rule.enabled);
    const savedProviderRules = savedRules.filter(rule => rule.providerId === provider.id && rule.enabled);
    const providerPolicies = policies.filter(policy => policy.providerId === provider.id);
    const whole = providerPolicies.find(policy => policy.enabled && !policy.ruleId);
    const scopes = whole ? [{ rule: null, rules: providerRules, policy: whole }] : providerRules.map(rule => ({
      rule, rules: [rule], policy: providerPolicies.find(policy => policy.enabled && policy.ruleId === rule.id),
    }));
    for (const scope of scopes) {
      const hasShared = scope.rules.some(rule => rule.id === primary) && (shared.entries?.length || 0) > 0;
      const entries = [...new Set([
        ...scope.rules.flatMap(rule => rule.domains || []),
        ...(hasShared ? shared.entries : []),
      ])];
      if (!entries.length) continue;
      const configuredPolicy = scope.rule && providerPolicies.find(policy => policy.ruleId === scope.rule.id);
      const synthetic = hasShared && !scope.policy && !configuredPolicy;
      const policyId = scope.policy?.id || (synthetic ? "shared-domain-list" : null);
      const status = policyId ? statusMap.get(policyId) : null;
      const failover = failoverMap.get(scope.rule?.id) || failoverMap.get(`__priority_policy_${policyId}`);
      const targets = scope.policy ? scope.policy.targets?.map(target => target.location) || []
        : synthetic ? shared.ruleIds.map(id => savedRules.find(rule => rule.id === id && rule.enabled)?.selectedLocation).filter(Boolean)
          : savedProviderRules.map(rule => rule.selectedLocation).filter(Boolean);
      rows.push({
        id: scope.policy?.id || scope.rule.id,
        ruleId: scope.rule?.id || null,
        rule: savedRules.find(rule => rule.id === scope.rule?.id) || scope.rule,
        providerId: provider.id,
        providerName: failover?.activeProviderName || provider.name,
        name: scope.policy?.name || scope.rule?.name || provider.name,
        shared: hasShared,
        count: entries.length,
        policyId,
        selectionMode: scope.policy?.selectionMode || status?.selectionMode || "priority",
        scheduledLocation: status?.scheduledLocation || "",
        activeLocation: failover ? failover.mode === "direct" ? "" : failover.activeLocation || "" : status?.activeLocation || "",
        configuredLocation: scope.rule?.selectedLocation || "",
        overrideLocation: status?.overrideLocation || "",
        targets: [...new Set(targets)],
        canQuickSwitch: Boolean(policyId) || !shared.ruleIds?.includes(scope.rule?.id),
        targetStatuses: status?.targets || [],
        failover: Boolean(failover),
        unavailable: failover?.mode === "direct",
      });
    }
  }
  return rows.sort((left, right) => Number(right.shared) - Number(left.shared));
}

export function serverSwitchRequest(row, location) {
  if (row.policyId) return {
    path: `/api/priority-policies/${encodeURIComponent(row.policyId)}/override`, method: "POST", body: { location },
  };
  if (row.canQuickSwitch === false) throw new Error("Configure automatic server selection for this shared route first");
  const rule = row.rule;
  return {
    path: `/api/rules/${encodeURIComponent(rule.id)}?apply=1`, method: "PUT",
    body: { name: rule.name, providerId: rule.providerId, selectedLocation: location, domains: rule.domains.join(","), enabled: rule.enabled },
  };
}
