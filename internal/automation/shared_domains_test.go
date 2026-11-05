package automation

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"xiomi-router-driver/internal/config"
	"xiomi-router-driver/internal/sqlitedb"
)

func TestSharedDomainsUseOnePrimaryAndKeepIndependentRoute(t *testing.T) {
	state := config.State{
		Providers: []config.Provider{{ID: "vpn", Type: config.ProviderTypeSubscription, Enabled: true}},
		Rules: []config.Rule{
			{ID: "primary", ProviderID: "vpn", SelectedLocation: "A", Domains: []string{"own-a.test"}, Enabled: true},
			{ID: "backup", ProviderID: "vpn", SelectedLocation: "B", Domains: []string{"own-b.test"}, Enabled: true},
			{ID: "deepseek", ProviderID: "vpn", SelectedLocation: "C", Domains: []string{"api.deepseek.com"}, Enabled: true},
		},
		SharedDomains: config.SharedDomainList{Entries: []string{"shared.test"}, RuleIDs: []string{"primary", "backup"}},
	}

	runtime := sharedDomainRoutingState(state)
	if len(runtime.PriorityPolicies) != 1 || runtime.PriorityPolicies[0].RuleID != "primary" || len(runtime.PriorityPolicies[0].Targets) != 2 {
		t.Fatalf("expected an ordered failover policy for the primary route: %+v", runtime.PriorityPolicies)
	}
	policy := runtime.PriorityPolicies[0]
	applied := buildPriorityAppliedStateFromDecisions(runtime, map[string]priorityDecision{
		policy.ID: {ProviderID: "vpn", ActiveLocation: "B", Mode: "provider"},
	})
	for _, rule := range applied.Rules {
		if slices.Contains(rule.Domains, "shared.test") && rule.SelectedLocation != "B" {
			t.Fatalf("shared entries were routed outside the selected backup: %+v", rule)
		}
	}
	var shared, deepseek int
	for _, rule := range applied.Rules {
		if slices.Contains(rule.Domains, "shared.test") {
			shared++
		}
		if rule.ID == "deepseek" && rule.SelectedLocation == "C" && slices.Equal(rule.Domains, []string{"api.deepseek.com"}) {
			deepseek++
		}
	}
	if shared != 1 || deepseek != 1 {
		t.Fatalf("expected one shared route and unchanged DeepSeek route: %+v", applied.Rules)
	}
	for _, entry := range []string{"own-a.test", "own-b.test"} {
		if !slices.ContainsFunc(applied.Rules, func(rule config.Rule) bool {
			return rule.Enabled && rule.SelectedLocation == "B" && slices.Contains(rule.Domains, entry)
		}) {
			t.Fatalf("own entry %q disappeared during shared failover: %+v", entry, applied.Rules)
		}
	}
	if expected := expectedSubscriptionKeys(applied); len(expected) != 2 {
		t.Fatalf("only the active backup and independent route should need a tunnel: %v", expected)
	}
	if !slices.Equal(state.Rules[0].Domains, []string{"own-a.test"}) || !slices.Equal(state.Rules[1].Domains, []string{"own-b.test"}) {
		t.Fatalf("shared mode overwrote saved own entries: %+v", state.Rules)
	}
	defaults := ApplyPriorityDefaults(state, time.Now())
	if !slices.ContainsFunc(defaults.Rules, func(rule config.Rule) bool {
		return rule.SelectedLocation == "A" && slices.Contains(rule.Domains, "shared.test")
	}) {
		t.Fatalf("expected shared entries on the primary at startup: %+v", defaults.Rules)
	}
}

func TestSharedDomainsKeepConfiguredPolicyIdentity(t *testing.T) {
	state := config.State{
		Providers: []config.Provider{{ID: "vpn", Name: "Subscription", Type: config.ProviderTypeSubscription, Enabled: true}},
		Rules: []config.Rule{
			{ID: "primary", Name: "Primary", ProviderID: "vpn", SelectedLocation: "A", Enabled: true},
			{ID: "backup", Name: "Backup", ProviderID: "vpn", SelectedLocation: "B", Enabled: true},
		},
		SharedDomains: config.SharedDomainList{Entries: []string{"shared.test"}, RuleIDs: []string{"primary", "backup"}},
		PriorityPolicies: []config.PriorityPolicy{{
			ID: "main-policy", RuleID: "primary", ProviderID: "vpn", Name: "Subscription priority", Enabled: true,
			Targets: []config.PriorityTarget{{Location: "A"}, {Location: "B"}},
		}},
	}
	runtime := sharedDomainRoutingState(state)
	if len(runtime.PriorityPolicies) != 1 || runtime.PriorityPolicies[0].ID != "main-policy" || runtime.PriorityPolicies[0].Name != "Subscription priority" {
		t.Fatalf("shared routing hid the configured policy from its UI card: %+v", runtime.PriorityPolicies)
	}

	db, err := sqlitedb.Open(filepath.Join(t.TempDir(), "vpn-manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	manager := config.NewManager(db, "")
	if _, err := manager.Save(state); err != nil {
		t.Fatal(err)
	}
	supervisor := NewSupervisor(manager, nil, nil, nil, nil, t.TempDir())
	supervisor.priority.decisions["main-policy"] = priorityDecision{ProviderID: "vpn", ActiveLocation: "B", Mode: "provider"}
	if err := supervisor.SetPriorityOverride("main-policy", "B"); err != nil {
		t.Fatal(err)
	}
	status := supervisor.PriorityStatus()
	if len(status.Policies) != 1 || status.Policies[0].PolicyID != "main-policy" || status.Policies[0].ActiveLocation != "B" || status.Policies[0].OverrideLocation != "B" {
		t.Fatalf("configured shared-policy status/selection is missing: %+v", status)
	}
	if got := supervisor.SharedDomainLocation(); got != "B" {
		t.Fatalf("shared routing selected %q, want the current backup B", got)
	}
}

func TestSharedDomainConfiguredPolicyKeepsScheduleTargetsAndEnabledState(t *testing.T) {
	state := config.State{
		Rules:         []config.Rule{{ID: "primary", ProviderID: "vpn", SelectedLocation: "A", Enabled: true}},
		SharedDomains: config.SharedDomainList{Entries: []string{"shared.test"}, RuleIDs: []string{"primary"}},
		PriorityPolicies: []config.PriorityPolicy{
			{ID: "enabled", RuleID: "primary", ProviderID: "vpn", Name: "Priority", Enabled: true,
				Targets:  []config.PriorityTarget{{Location: "C"}, {Location: "A"}},
				Schedule: []config.PriorityScheduleWindow{{Start: "08:00", End: "20:00", Location: "C"}}},
			{ID: "disabled", RuleID: "primary", ProviderID: "vpn", Name: "Old policy", Enabled: false},
		},
	}
	runtime := sharedDomainRoutingState(state)
	if !slices.ContainsFunc(runtime.PriorityPolicies, func(policy config.PriorityPolicy) bool {
		return policy.ID == "enabled" && policy.Enabled && len(policy.Targets) == 2 && policy.Targets[0].Location == "C" && len(policy.Schedule) == 1
	}) {
		t.Fatalf("shared routing lost the enabled policy's targets or schedule: %+v", runtime.PriorityPolicies)
	}
	if slices.ContainsFunc(runtime.PriorityPolicies, func(policy config.PriorityPolicy) bool { return policy.ID == "disabled" && policy.Enabled }) {
		t.Fatal("disabled policy was enabled by shared routing")
	}
	state.PriorityPolicies = state.PriorityPolicies[1:]
	runtime = sharedDomainRoutingState(state)
	if len(runtime.PriorityPolicies) != 1 || runtime.PriorityPolicies[0].Enabled {
		t.Fatalf("a disabled shared priority policy must stay disabled: %+v", runtime.PriorityPolicies)
	}
}
