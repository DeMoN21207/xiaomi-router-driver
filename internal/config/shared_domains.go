package config

import (
	"net/netip"
	"strings"

	"xiomi-router-driver/internal/domains"
)

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
	// Older versions kept complete own lists hidden in shared mode. Reserve
	// copies must not acquire a second route when own lists become additive.
	seen := make(map[string]bool)
	routedDomains := make(map[string]bool)
	var routedPrefixes []netip.Prefix
	entriesByID := make(map[string][]string, len(routes))
	for index, route := range routes {
		var memberPrefixes []netip.Prefix
		entries := append([]string(nil), route.Domains...)
		if index == 0 {
			entries = append(entries, s.SharedDomains.Entries...)
		}
		for _, entry := range domains.NormalizeEntries(entries) {
			if seen[entry] {
				continue
			}
			seen[entry] = true
			prefix, isIP := domains.ParseIPPrefix(entry)
			if !isIP {
				covered := false
				for parent := entry; parent != ""; {
					if routedDomains[parent] {
						covered = true
						break
					}
					_, parent, _ = strings.Cut(parent, ".")
				}
				if covered {
					continue
				}
				entriesByID[route.ID] = append(entriesByID[route.ID], entry)
				routedDomains[entry] = true
				continue
			}
			remaining := []netip.Prefix{prefix}
			for _, covered := range routedPrefixes {
				var next []netip.Prefix
				for _, candidate := range remaining {
					next = append(next, subtractSharedPrefix(candidate, covered)...)
				}
				remaining = next
			}
			for _, candidate := range remaining {
				value := entry
				if candidate != prefix {
					value = candidate.String()
					if candidate.Bits() == 32 {
						value = candidate.Addr().String()
					}
				}
				entriesByID[route.ID] = append(entriesByID[route.ID], value)
			}
			memberPrefixes = append(memberPrefixes, remaining...)
		}
		routedPrefixes = append(routedPrefixes, memberPrefixes...)
	}
	rules := append([]Rule(nil), s.Rules...)
	for index, rule := range rules {
		if members[rule.ID] {
			rules[index].Domains = entriesByID[rule.ID]
			// An empty reserve is a failover target, not a second live tunnel.
			rules[index].Enabled = len(rules[index].Domains) > 0
		}
	}
	return rules
}

// subtractSharedPrefix keeps the part of an IPv4 range not already routed by
// an earlier shared-list member. Saved entries remain unchanged.
func subtractSharedPrefix(prefix, covered netip.Prefix) []netip.Prefix {
	if !prefix.Overlaps(covered) {
		return []netip.Prefix{prefix}
	}
	if covered.Bits() <= prefix.Bits() {
		return nil
	}
	bits := prefix.Bits() + 1
	left := netip.PrefixFrom(prefix.Addr(), bits)
	address := prefix.Addr().As4()
	address[(bits-1)/8] |= 1 << (7 - (bits-1)%8)
	right := netip.PrefixFrom(netip.AddrFrom4(address), bits)
	return append(subtractSharedPrefix(left, covered), subtractSharedPrefix(right, covered)...)
}
