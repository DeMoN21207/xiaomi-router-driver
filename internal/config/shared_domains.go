package config

import "xiomi-router-driver/internal/domains"

const SharedDomainPolicyID = "shared-domain-list"

type SharedDomainList struct {
	Entries []string `json:"entries"`
	RuleIDs []string `json:"ruleIds"`
}

func normalizeSharedDomainList(list SharedDomainList, rules []Rule) SharedDomainList {
	known := make(map[string]bool, len(rules))
	for _, rule := range rules {
		known[rule.ID] = true
	}
	ids := make([]string, 0, len(list.RuleIDs))
	for _, id := range list.RuleIDs {
		if known[id] {
			ids = append(ids, id)
			delete(known, id)
		}
	}
	return SharedDomainList{Entries: domains.NormalizeEntries(list.Entries), RuleIDs: ids}
}

// SharedDomainRoutes returns enabled members in primary and reserve order.
func (s State) SharedDomainRoutes() []Rule {
	if len(s.SharedDomains.RuleIDs) == 0 {
		return nil
	}
	byID := make(map[string]Rule, len(s.Rules))
	for _, rule := range s.Rules {
		if rule.Enabled {
			byID[rule.ID] = rule
		}
	}
	routes := make([]Rule, 0, len(s.SharedDomains.RuleIDs))
	for _, id := range s.SharedDomains.RuleIDs {
		if rule, ok := byID[id]; ok {
			routes = append(routes, rule)
		}
	}
	return routes
}

// EffectiveRules derives runtime entries without overwriting saved own lists.
func (s State) EffectiveRules() []Rule {
	routes := s.SharedDomainRoutes()
	if len(routes) == 0 {
		return s.Rules
	}
	members := make(map[string]bool, len(routes))
	for _, route := range routes {
		members[route.ID] = true
	}
	rules := append([]Rule(nil), s.Rules...)
	for index, rule := range rules {
		if members[rule.ID] {
			rules[index].Domains = nil
			if rule.ID == routes[0].ID {
				rules[index].Domains = s.SharedDomains.Entries
			} else {
				rules[index].Enabled = false
			}
		}
	}
	return rules
}
