package api

import (
	"context"
	"errors"

	"net/http"
	"testing"
	"xiomi-router-driver/internal/automation"

	"xiomi-router-driver/internal/config"
)

func TestSharedDefaultServerCanBeSwitchedWithoutPersistingSyntheticPolicy(t *testing.T) {
	handler, manager, _, _ := newAPITestHandler(t)
	state := config.DefaultState()
	state.Providers = []config.Provider{{ID: "vpn", Name: "VPN", Type: config.ProviderTypeSubscription, Source: testSubscriptionSource(), Enabled: true}}
	state.Rules = []config.Rule{
		{ID: "main", Name: "Main", ProviderID: "vpn", SelectedLocation: "Latvia", Enabled: true},
		{ID: "reserve", Name: "Reserve", ProviderID: "vpn", SelectedLocation: "Sweden", Enabled: true},
	}
	state.SharedDomains = config.SharedDomainList{Entries: []string{"youtube.com"}, RuleIDs: []string{"main", "reserve"}}
	if _, err := manager.Save(state); err != nil {
		t.Fatal(err)
	}
	called := 0
	handler.applyPriorityOverride = func(_ context.Context, id, location string) (string, error) {
		if id != config.SharedDomainPolicyID || location != "Sweden" {
			t.Fatalf("unexpected override: %s %s", id, location)
		}
		called++
		return location, nil
	}
	path := "/api/priority-policies/" + config.SharedDomainPolicyID + "/override"
	before, err := manager.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := findRuntimePriorityPolicy(before, config.SharedDomainPolicyID); !ok {
		t.Fatalf("synthetic policy unavailable: routes=%d shared=%d policies=%d", len(before.SharedDomainRoutes()), len(before.SharedDomains.Entries), len(before.PriorityPolicies))
	}
	result := requestJSON[map[string]string](t, handler, http.MethodPost, path, map[string]string{"location": "Sweden"}, http.StatusOK)
	if result["status"] != "override_set" || result["activeLocation"] != "Sweden" {
		t.Fatalf("incorrect selection response: %+v", result)
	}
	_ = requestJSON[map[string]string](t, handler, http.MethodPost, path, map[string]string{"location": "Unknown"}, http.StatusBadRequest)
	if called != 1 {
		t.Fatalf("override called %d times", called)
	}
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{automation.ErrPriorityTargetUnavailable, http.StatusConflict, "priority_target_unavailable"},
		{errors.New("route apply failed"), http.StatusInternalServerError, "priority_apply_failed"},
	} {
		handler.applyPriorityOverride = func(context.Context, string, string) (string, error) { return "", tc.err }
		result := requestJSON[map[string]string](t, handler, http.MethodPost, path, map[string]string{"location": "Sweden"}, tc.status)
		if result["code"] != tc.code || result["error"] == "" {
			t.Fatalf("incorrect error response: %+v", result)
		}
	}
	handler.applyPriorityOverride = func(_ context.Context, id, location string) (string, error) {
		if location != "" {
			t.Fatalf("clear sent location %q", location)
		}
		return "Latvia", nil
	}
	result = requestJSON[map[string]string](t, handler, http.MethodDelete, path, nil, http.StatusOK)
	if result["status"] != "override_cleared" || result["activeLocation"] != "Latvia" {
		t.Fatalf("clear result: %+v", result)
	}
	loaded, err := manager.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.PriorityPolicies) != 0 {
		t.Fatal("a transient shared policy was persisted")
	}
	loaded.PriorityPolicies = []config.PriorityPolicy{{ID: "configured", ProviderID: "vpn", RuleID: "main", Name: "Configured", Enabled: false, Targets: []config.PriorityTarget{{Location: "Latvia"}}}}
	if _, err := manager.Save(loaded); err != nil {
		t.Fatal(err)
	}
	_ = requestJSON[map[string]string](t, handler, http.MethodPost, path, map[string]string{"location": "Sweden"}, http.StatusNotFound)
}
