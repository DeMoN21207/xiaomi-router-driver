package api

import (
	"net/http"
	"testing"

	"xiomi-router-driver/internal/config"
)

func TestPriorityAPIKeepsSelectionModeForOlderClientsAndRejectsUnknownMode(t *testing.T) {
	handler, manager, _, _ := newAPITestHandler(t)
	state := config.DefaultState()
	state.Providers = []config.Provider{testProvider("vpn", "VPN", true)}
	state.Providers[0].Source = testSubscriptionSource()
	state.Rules = []config.Rule{{ID: "scope", Name: "Sites", ProviderID: "vpn", SelectedLocation: "Germany", Domains: []string{"example.com"}, Enabled: true}}
	state.PriorityPolicies = []config.PriorityPolicy{{ID: "policy", ProviderID: "vpn", RuleID: "scope", Name: "Fast", Enabled: true, SelectionMode: "latency", Targets: []config.PriorityTarget{{Location: "Germany"}}}}
	if _, err := manager.Save(state); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"name": "Renamed", "providerId": "vpn", "enabled": true, "targets": []config.PriorityTarget{{Location: "Germany"}}}
	response := requestJSON[struct {
		Policy config.PriorityPolicy `json:"policy"`
	}](t, handler, http.MethodPut, "/api/priority-policies/policy", payload, http.StatusOK)
	if response.Policy.SelectionMode != "latency" || response.Policy.RuleID != "scope" {
		t.Fatalf("older client changed selection or scope: %+v", response.Policy)
	}
	payload["selectionMode"] = "random"
	_ = requestJSON[map[string]string](t, handler, http.MethodPut, "/api/priority-policies/policy", payload, http.StatusBadRequest)
}
