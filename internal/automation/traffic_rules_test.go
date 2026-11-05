package automation

import (
	"slices"
	"testing"
	"time"

	"xiomi-router-driver/internal/config"
)

func TestTrafficRulesFollowSharedAndIndependentRuntimeSelections(t *testing.T) {
	state := config.State{
		Providers: []config.Provider{
			{ID: "vpn", Type: config.ProviderTypeSubscription, Enabled: true},
			{ID: "reserve", Type: config.ProviderTypeSubscription, Enabled: true},
		},
		Rules: []config.Rule{
			{ID: "main", ProviderID: "vpn", SelectedLocation: "Latvia", Enabled: true},
			{ID: "backup", ProviderID: "vpn", SelectedLocation: "Sweden", Enabled: true},
			{ID: "deepseek", ProviderID: "vpn", SelectedLocation: "Germany", Domains: []string{"api.deepseek.com"}, Enabled: true},
		},
		SharedDomains: config.SharedDomainList{Entries: []string{"youtube.com"}, RuleIDs: []string{"main", "backup"}},
		PriorityPolicies: []config.PriorityPolicy{
			{ID: "main-policy", ProviderID: "vpn", RuleID: "main", Enabled: true, Targets: []config.PriorityTarget{{Location: "Latvia"}, {Location: "Sweden"}}},
			{ID: "deepseek-policy", ProviderID: "vpn", RuleID: "deepseek", Enabled: true, Targets: []config.PriorityTarget{{Location: "Germany"}, {Location: "Netherlands"}}},
		},
	}
	s := &Supervisor{priority: newPriorityRuntime()}
	s.priority.decisions["main-policy"] = priorityDecision{ProviderID: "vpn", ActiveLocation: "Sweden", Mode: "provider"}
	s.priority.decisions["deepseek-policy"] = priorityDecision{ProviderID: "vpn", ActiveLocation: "Netherlands", Mode: "provider"}
	check := func(entry, provider, location string) {
		t.Helper()
		rules := s.TrafficRules(state)
		if !slices.ContainsFunc(rules, func(rule config.Rule) bool {
			return rule.Enabled && slices.Contains(rule.Domains, entry) && rule.ProviderID == provider && rule.SelectedLocation == location
		}) {
			t.Fatalf("traffic for %s did not use %s/%s: %+v", entry, provider, location, rules)
		}
	}
	check("youtube.com", "vpn", "Sweden")
	check("api.deepseek.com", "vpn", "Netherlands")
	s.failover.ensure()
	s.failover.overrides[priorityRuleID("deepseek-policy")] = failoverOverride{ActiveProviderID: "reserve", ActiveLocation: "France", Mode: "provider"}
	check("api.deepseek.com", "reserve", "France")
	if len(state.Rules[0].Domains) != 0 || state.Rules[2].SelectedLocation != "Germany" {
		t.Fatal("traffic reporting modified saved routing")
	}
	s.failoverMu.Lock()
	s.publishFailoverStatusLocked()
	defer s.failoverMu.Unlock()
	finished := make(chan []config.Rule, 1)
	go func() { finished <- s.TrafficRules(state) }()
	select {
	case rules := <-finished:
		if !slices.ContainsFunc(rules, func(rule config.Rule) bool {
			return rule.ProviderID == "reserve" && slices.Contains(rule.Domains, "api.deepseek.com")
		}) {
			t.Fatal("traffic did not retain the published failover snapshot")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("traffic reporting blocked on an in-progress failover")
	}
}
