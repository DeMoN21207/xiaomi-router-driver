export function routingOverview(config, statusMap = new Map(), failoverMap = new Map()) {
  const savedRules = config?.rules || [];
  const rules = config?.effectiveRules || savedRules;
  const providers = config?.providers || [];
  const policies = config?.priorityPolicies || [];
  const shared = config?.sharedDomains || {};
  const primary = shared.ruleIds?.find(id => savedRules.some(rule => rule.id === id && rule.enabled));
  const rows = [];
  for (const provider of providers.filter(item => item.enabled)) {
    const savedProviderRules = savedRules.filter(rule => rule.providerId === provider.id && rule.enabled);
    const providerPolicies = policies.filter(policy => policy.providerId === provider.id);
    const providerRules = rules.filter(rule => {
      if (rule.providerId !== provider.id) return false;
      if (rule.enabled) return true;
      const saved = savedProviderRules.find(item => item.id === rule.id);
      if (!saved || saved.domains?.length) return false;
      // EffectiveRules disables empty routes; retain editable primary lists only.
      return (rule.id === primary && !shared.entries?.length) || providerPolicies.some(policy => policy.enabled && policy.ruleId === rule.id);
    });
    const whole = providerPolicies.find(policy => policy.enabled && !policy.ruleId);
    const scopes = whole ? [{ rule: null, rules: providerRules, policy: whole }] : providerRules.map(rule => ({
      rule, rules: [rule], policy: providerPolicies.find(policy => policy.enabled && policy.ruleId === rule.id),
    }));
    for (const scope of scopes) {
      const hasShared = scope.rules.some(rule => rule.id === primary);
      const entries = [...new Set([
        ...scope.rules.flatMap(rule => rule.domains || []),
        ...(hasShared ? shared.entries || [] : []),
      ])];
      if (!entries.length && !hasShared && !scope.policy) continue;
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
        ruleIds: scope.rules.map(rule => rule.id),
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
    path: `/api/priority-policies/${encodeURIComponent(row.policyId)}/override`,
    ...(location ? { method: "POST", body: { location } } : { method: "DELETE" }),
  };
  if (row.canQuickSwitch === false) throw new Error("Configure automatic server selection for this shared route first");
  const rule = row.rule;
  return {
    path: `/api/rules/${encodeURIComponent(rule.id)}?apply=1`, method: "PUT",
    body: { name: rule.name, providerId: rule.providerId, selectedLocation: location, domains: (rule.domains || []).join(","), enabled: rule.enabled },
  };
}

// Use saved ownership, never effective runtime domains, when editing a list.
export function routeDomainSources(config, row) {
  const sources = row.shared ? [{ id: "shared", shared: true, entries: [...(config.sharedDomains?.entries || [])] }] : [];
  for (const id of row.ruleIds) {
    const rule = config.rules?.find(item => item.id === id);
    if (!rule || (sources.length > 0 && !rule.domains?.length)) continue;
    sources.push({ id, name: rule.name || rule.selectedLocation, entries: [...(rule.domains || [])] });
  }
  return sources;
}

export function domainSaveRequest(config, source, entries) {
  const rule = source.shared ? null : config.rules?.find(item => item.id === source.id);
  const exists = source.shared ? config.sharedDomains : rule;
  const current = (source.shared ? config.sharedDomains?.entries : rule?.domains) || [];
  if (!exists || JSON.stringify(current) !== JSON.stringify(source.entries)) throw new Error("Site list changed; reopen the editor");
  return source.shared ? { path: "/api/config/shared-domains", method: "PUT", body: { entries } } : {
    path: `/api/rules/${encodeURIComponent(rule.id)}?apply=1`, method: "PUT",
    body: { name: rule.name, providerId: rule.providerId, selectedLocation: rule.selectedLocation, domains: entries.join(","), enabled: rule.enabled },
  };
}
