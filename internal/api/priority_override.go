package api

import "xiomi-router-driver/internal/config"

// The shared-list default is transient, but its runtime server selection is
// controlled through the same override endpoint as saved priority policies.
func findRuntimePriorityPolicy(state config.State, id string) (config.PriorityPolicy, bool) {
	if policy, ok := findPriorityPolicy(state.PriorityPolicies, id); ok {
		return policy, true
	}
	if id != config.SharedDomainPolicyID || len(state.SharedDomains.Entries) == 0 {
		return config.PriorityPolicy{}, false
	}
	routes := state.SharedDomainRoutes()
	if len(routes) == 0 {
		return config.PriorityPolicy{}, false
	}
	for _, policy := range state.PriorityPolicies {
		if policy.RuleID == routes[0].ID {
			return config.PriorityPolicy{}, false
		}
	}
	policy := config.PriorityPolicy{ID: id, RuleID: routes[0].ID, ProviderID: routes[0].ProviderID, Enabled: true}
	for _, route := range routes {
		policy.Targets = append(policy.Targets, config.PriorityTarget{Location: route.SelectedLocation})
	}
	return policy, true
}
