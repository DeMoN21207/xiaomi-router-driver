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
