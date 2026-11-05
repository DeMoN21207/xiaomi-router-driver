package automation

import "xiomi-router-driver/internal/config"

func sharedDomainRoutingState(state config.State) config.State {
	routes := state.SharedDomainRoutes()
	if len(routes) == 0 || len(state.SharedDomains.Entries) == 0 {
		return state
	}
	state.Rules = state.EffectiveRules()
	state.SharedDomains = config.SharedDomainList{}
	primary := routes[0]
	policy := config.PriorityPolicy{
		ID:            config.SharedDomainPolicyID,
		RuleID:        primary.ID,
		Name:          "Shared domain list",
		ProviderID:    primary.ProviderID,
		Enabled:       true,
		SelectionMode: config.PrioritySelectionLatency,
	}
	for _, route := range routes {
		policy.Targets = append(policy.Targets, config.PriorityTarget{Location: route.SelectedLocation})
	}
	if configured, ok := configuredSharedDomainPolicy(state, primary.ID); ok {
		// An explicit policy remains authoritative for targets, schedules,
		// enablement and selections. Checkboxes supply the default otherwise.
		policy = configured
	}
	policies := make([]config.PriorityPolicy, 0, len(state.PriorityPolicies)+1)
	for _, configured := range state.PriorityPolicies {
		if configured.ID != policy.ID {
			policies = append(policies, configured)
		}
	}
	state.PriorityPolicies = append(policies, policy)
	return state
}

func configuredSharedDomainPolicy(state config.State, primaryID string) (config.PriorityPolicy, bool) {
	var selected config.PriorityPolicy
	found := false
	for _, policy := range state.PriorityPolicies {
		if policy.RuleID != primaryID {
			continue
		}
		if policy.Enabled {
			return policy, true
		}
		if !found {
			selected, found = policy, true
		}
	}
	return selected, found
}

func (s *Supervisor) SharedDomainLocation() string {
	policyID := config.SharedDomainPolicyID
	if s.state != nil {
		if state, err := s.state.Load(); err == nil {
			routes := state.SharedDomainRoutes()
			if len(routes) != 0 {
				if policy, ok := configuredSharedDomainPolicy(state, routes[0].ID); ok {
					policyID = policy.ID
				}
			}
		}
	}
	s.priorityMu.RLock()
	defer s.priorityMu.RUnlock()
	return s.priority.decisions[policyID].ActiveLocation
}
