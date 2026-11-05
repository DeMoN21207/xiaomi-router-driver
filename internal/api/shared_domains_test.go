package api

import (
	"net/http"
	"slices"
	"testing"

	"xiomi-router-driver/internal/config"
)

func TestSharedReserveKeepsItsOwnPriorityPolicy(t *testing.T) {
	state := config.State{
		Providers: []config.Provider{{ID: "vpn", Type: config.ProviderTypeSubscription, Enabled: true}},
		Rules: []config.Rule{
			{ID: "main", ProviderID: "vpn", SelectedLocation: "Latvia", Enabled: true},
			{ID: "reserve", ProviderID: "vpn", SelectedLocation: "Germany", Domains: []string{"api.deepseek.com"}, Enabled: true},
		},
		SharedDomains:    config.SharedDomainList{Entries: []string{"youtube.com"}, RuleIDs: []string{"main", "reserve"}},
		PriorityPolicies: []config.PriorityPolicy{{ID: "deepseek", ProviderID: "vpn", RuleID: "reserve", Enabled: true}},
	}
	if err := validateActiveRuleEntries(state); err != nil {
		t.Fatalf("a shared reserve must keep independent priority for its own domains: %v", err)
	}
	state.PriorityPolicies[0].RuleID = ""
	if err := validateActiveRuleEntries(state); err == nil {
		t.Fatal("a provider-wide policy must not override the shared list's independent routing")
	}
}

func TestSharedRouteCanClearOwnListWithoutLosingServerOrSharedSelection(t *testing.T) {
	handler, manager, _, _ := newAPITestHandler(t)
	state := config.DefaultState()
	state.Providers = []config.Provider{testProvider("vpn", "VPN", true)}
	state.Rules = []config.Rule{
		{ID: "main", Name: "Main", ProviderID: "vpn", SelectedLocation: "Latvia", Domains: []string{"own.test"}, Enabled: true},
		{ID: "reserve", Name: "Reserve", ProviderID: "vpn", SelectedLocation: "Sweden", Domains: []string{"reserve.test"}, Enabled: true},
	}
	state.SharedDomains = config.SharedDomainList{Entries: []string{"shared.test"}, RuleIDs: []string{"main", "reserve"}}
	if _, err := manager.Save(state); err != nil {
		t.Fatal(err)
	}
	_ = requestJSON[map[string]any](t, handler, http.MethodPut, "/api/rules/main", map[string]any{
		"name": "Main", "providerId": "vpn", "selectedLocation": "Latvia", "domains": "", "enabled": true,
	}, http.StatusOK)
	loaded, err := manager.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Rules) != 2 || len(loaded.Rules[0].Domains) != 0 || !slices.Equal(loaded.SharedDomains.RuleIDs, []string{"main", "reserve"}) {
		t.Fatalf("clearing own entries removed the server or shared selection: %+v", loaded)
	}
	effective := loaded.EffectiveRules()
	if !slices.Equal(effective[0].Domains, []string{"shared.test"}) || !slices.Equal(effective[1].Domains, []string{"reserve.test"}) {
		t.Fatalf("clearing own entries changed other routing lists: %+v", effective)
	}
}

func TestRulesAPIReportsEffectiveSharedOwnershipAlongsideSavedLists(t *testing.T) {
	handler, manager, _, _ := newAPITestHandler(t)
	state := config.DefaultState()
	state.Providers = []config.Provider{testProvider("vpn", "VPN", true)}
	state.Rules = []config.Rule{
		{ID: "main", Name: "Main", ProviderID: "vpn", SelectedLocation: "Latvia", Enabled: true},
		{ID: "reserve", Name: "Reserve", ProviderID: "vpn", SelectedLocation: "Sweden", Domains: []string{"youtube.com", "api.youtube.com", "198.51.100.1"}, Enabled: true},
	}
	state.SharedDomains = config.SharedDomainList{Entries: []string{"youtube.com", "198.51.100.0/24"}, RuleIDs: []string{"main", "reserve"}}
	if _, err := manager.Save(state); err != nil {
		t.Fatal(err)
	}
	response := requestJSON[struct {
		Rules          []config.Rule `json:"rules"`
		EffectiveRules []config.Rule `json:"effectiveRules"`
	}](t, handler, http.MethodGet, "/api/rules", nil, http.StatusOK)
	if len(response.Rules) != 2 || len(response.Rules[1].Domains) != 2 {
		t.Fatalf("saved own list was not retained: %+v", response.Rules)
	}
	if len(response.EffectiveRules) != 2 || response.EffectiveRules[1].Enabled || len(response.EffectiveRules[1].Domains) != 0 {
		t.Fatalf("legacy copies must not appear as a second active route: %+v", response.EffectiveRules)
	}
}
