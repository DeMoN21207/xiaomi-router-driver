export function collectConnectionDomains(rules) {
  const routesByEntry = new Map();

  for (const rule of rules) {
    for (const entry of new Set(rule.domains ?? [])) {
      if (!routesByEntry.has(entry)) routesByEntry.set(entry, []);
      routesByEntry.get(entry).push(rule);
    }
  }

  return [...routesByEntry]
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([entry, rules]) => ({ entry, rules }));
}

export function sharedDomainPriorityStatus(list, rules, policies, statusMap) {
  const primary = list?.ruleIds?.find((id) => rules.some((rule) => rule.id === id && rule.enabled));
  const matching = primary ? policies.filter((policy) => policy.ruleId === primary) : [];
  const policy = matching.find((item) => item.enabled) || matching[0];
  return statusMap.get(policy?.id) || statusMap.get("shared-domain-list");
}

export function activeConnectionRoutes(providerId, list, rules, policies, statusMap) {
  const primary = list?.ruleIds?.find((id) => rules.some((rule) => rule.id === id && rule.enabled));
  const routes = policies.filter((policy) => policy.providerId === providerId && policy.enabled)
    .flatMap((policy) => {
      if (policy.ruleId) {
        const rule = rules.find((item) => item.id === policy.ruleId && item.enabled);
        const hasShared = policy.ruleId === primary && list?.entries?.length > 0;
        if (!rule || (!hasShared && !rule.domains?.length)) return [];
      }
      const location = statusMap.get(policy.id)?.activeLocation;
      return location ? [{ id: policy.id, name: policy.name, location, shared: Boolean(primary) && policy.ruleId === primary }] : [];
    });
  if (list?.entries?.length > 0 && rules.some((rule) => rule.id === primary && rule.providerId === providerId) && !routes.some((route) => route.shared)) {
    const location = sharedDomainPriorityStatus(list, rules, policies, statusMap)?.activeLocation;
    if (location) routes.unshift({ id: "shared-domain-list", location, shared: true });
  }
  return routes;
}
