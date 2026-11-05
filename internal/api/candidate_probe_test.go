package api

import (
	"net/http"
	"testing"
	"xiomi-router-driver/internal/config"
)

func TestCandidateProbeRejectsUnknownProviderAndUnboundedInput(t *testing.T) {
	handler, manager, _, _ := newAPITestHandler(t)
	state := config.DefaultState()
	state.Providers = []config.Provider{testProvider("vpn", "VPN", true)}
	if _, err := manager.Save(state); err != nil {
		t.Fatal(err)
	}
	_ = requestJSON[map[string]string](t, handler, http.MethodPost, "/api/providers/missing/check", map[string]any{"locations": []string{"A"}}, http.StatusNotFound)
	_ = requestJSON[map[string]string](t, handler, http.MethodPost, "/api/providers/vpn/check", map[string]any{"locations": []string{"A", "B", "C", "D", "E", "F", "G", "H", "I"}}, http.StatusBadRequest)
}

func TestCandidateProbeReturnsSelectedServerWithoutChangingRules(t *testing.T) {
	t.Setenv("VPN_MANAGER_SINGBOX_BIN", "/does-not-exist/sing-box")
	handler, manager, _, _ := newAPITestHandler(t)
	state := config.DefaultState()
	provider := testProvider("vpn", "VPN", true)
	provider.Source = testSubscriptionSource()
	state.Providers = []config.Provider{provider}
	state.Rules = []config.Rule{testRule("route", "Route", "vpn", "Germany", []string{"example.com"}, true)}
	if _, err := manager.Save(state); err != nil {
		t.Fatal(err)
	}
	response := requestJSON[struct {
		Candidates []struct {
			Location string `json:"location"`
			Healthy  bool   `json:"healthy"`
		} `json:"candidates"`
	}](t, handler, http.MethodPost, "/api/providers/vpn/check", map[string]any{"locations": []string{"Netherlands"}}, http.StatusOK)
	if len(response.Candidates) != 1 || response.Candidates[0].Location != "Netherlands" || response.Candidates[0].Healthy {
		t.Fatalf("unexpected probe result: %+v", response)
	}
	after, err := manager.Load()
	if err != nil || len(after.Rules) != 1 || after.Rules[0].SelectedLocation != "Germany" {
		t.Fatalf("probe changed active routing: %+v, %v", after.Rules, err)
	}
}
