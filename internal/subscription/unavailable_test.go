package subscription

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"xiomi-router-driver/internal/config"
)

func TestAutomaticApplyKeepsRemovedLocationAndSwitchesOtherRule(t *testing.T) {
	m, state, _ := rollbackTestManager(t)
	state.Rules = append(state.Rules, config.Rule{ID: "other", ProviderID: "provider_1", Enabled: true, SelectedLocation: "New", Domains: []string{"other.example"}})
	if err := m.Apply(t.Context(), state, state.Rules); err != nil {
		t.Fatal(err)
	}
	old := *m.current["provider_1::old"]
	state.Providers[0].Source = strings.ReplaceAll(state.Providers[0].Source, `"tag":"Old"`, `"tag":"Z"`)
	state.Rules[1].SelectedLocation = "Z"
	ctx, report := WithApplyReport(WithCachedEntries(t.Context()))
	if err := m.Apply(ctx, state, state.Rules); err != nil {
		t.Fatal(err)
	}
	kept := m.current["provider_1::old"]
	if kept == nil || kept.PID != old.PID || kept.Settings != old.Settings {
		t.Fatalf("removed location lost its active tunnel or routing table: %+v", kept)
	}
	switched := m.current["provider_1::z"]
	if switched == nil || switched.Settings.TableNum == old.Settings.TableNum || switched.Settings.FWMark == old.Settings.FWMark || m.current["provider_1::new"] != nil {
		t.Fatalf("independent rule did not switch to a distinct route: %+v", switched)
	}
	assertAppliedEntries(t, kept, "example.com", "203.0.113.0/24")
	assertAppliedEntries(t, switched, "other.example")
	if got := report.Rules["rule_1"]; got.ProviderID != "provider_1" || got.Location != "Old" || got.Issue == "" || got.Blocked {
		t.Fatalf("missing retained-route result: %+v", got)
	}
	if got := report.Rules["other"]; got.ProviderID != "provider_1" || got.Location != "Z" || got.Issue != "" || got.Blocked {
		t.Fatalf("unexpected result for healthy route: %+v", got)
	}
	explicit, failedReport := WithApplyReport(t.Context())
	if err := m.Apply(explicit, state, state.Rules); err == nil || failedReport.Rules != nil {
		t.Fatal("explicit apply accepted a removed location")
	}
}

func TestAutomaticApplyBlocksUnservedGroupAndRecovers(t *testing.T) {
	m, state, _ := rollbackTestManager(t)
	var warnings []string
	m.recordEvent = func(level, kind, message string) {
		if level == "warn" {
			warnings = append(warnings, message)
		}
	}
	state.Rules = append(state.Rules, config.Rule{ID: "missing", ProviderID: "provider_1", Enabled: true, SelectedLocation: "Gone", Domains: []string{"sensitive.example", "198.51.100.0/24"}})
	ctx, report := WithApplyReport(WithCachedEntries(t.Context()))
	if err := m.Apply(ctx, state, state.Rules); err != nil {
		t.Fatal(err)
	}
	blocked := m.current["provider_1::gone"]
	if m.current["provider_1::old"] == nil || blocked == nil {
		t.Fatalf("healthy or fail-closed route missing: %v", m.current)
	}
	assertAppliedEntries(t, blocked, "sensitive.example", "198.51.100.0/24")
	configData, err := os.ReadFile(blocked.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	var vpnConfig struct {
		Route struct{ Rules []map[string]any }
	}
	if err := json.Unmarshal(configData, &vpnConfig); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(vpnConfig.Route.Rules, []map[string]any{{"inbound": "tun-in", "action": "reject"}}) {
		t.Fatalf("fail-closed tunnel must reject every packet: %s", configData)
	}
	if !strings.Contains(strings.Join(warnings, " "), "Gone") {
		t.Fatalf("event did not identify the affected group: %v", warnings)
	}
	snapshots, err := m.Snapshots()
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(snapshots, func(s RuntimeSnapshot) bool { return s.Location == "Gone" })
	if i < 0 || snapshots[i].Status != "error" || !strings.Contains(snapshots[i].StatusDetail, "Gone") {
		t.Fatalf("failed location must be visible: %+v", snapshots)
	}
	if got := report.Rules["missing"]; !got.Blocked || got.Location != "" || got.Issue == "" {
		t.Fatalf("blocked route was published as a successful switch: %+v", got)
	}
	// The other VPN sorts ahead of Gone after this switch. Its new allocation
	// must not replace the already active fail-closed table.
	state.Providers[0].Source = strings.ReplaceAll(state.Providers[0].Source, `"tag":"Old"`, `"tag":"A"`)
	state.Rules[0].SelectedLocation = "A"
	if err := m.Apply(ctx, state, state.Rules); err != nil || m.current["provider_1::gone"].PID != blocked.PID {
		t.Fatalf("repeated automatic apply lost fail-closed routing: %v", err)
	}
	state.Providers[0].Source = strings.ReplaceAll(state.Providers[0].Source, `"tag":"New"`, `"tag":"Gone"`)
	if err := m.Apply(ctx, state, state.Rules); err != nil {
		t.Fatal(err)
	}
	recovered := m.current["provider_1::gone"]
	if recovered == nil || recovered.PID == blocked.PID {
		t.Fatal("returned location did not replace the blocked tunnel")
	}
	data, err := os.ReadFile(recovered.ConfigPath)
	if err != nil || !strings.Contains(string(data), "new.example.com") {
		t.Fatalf("returned location did not restore its VPN endpoint: %s, %v", data, err)
	}
	if got := report.Rules["missing"]; got.Blocked || got.Location != "Gone" || got.Issue != "" {
		t.Fatalf("catalogue recovery did not clear the previous issue: %+v", got)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := m.Apply(cancelled, state, state.Rules); err == nil || report.Rules != nil {
		t.Fatalf("cancelled apply published stale results: %+v, %v", report.Rules, err)
	}
}

func TestAutomaticApplyCannotAssignSamePreviousRouteToTwoNewLocations(t *testing.T) {
	m, state, _ := rollbackTestManager(t)
	if err := m.Apply(t.Context(), state, state.Rules); err != nil {
		t.Fatal(err)
	}
	state.Rules[0].SelectedLocation = "Gone"
	state.Rules = append(state.Rules, config.Rule{ID: "also-missing", ProviderID: "provider_1", Enabled: true, SelectedLocation: "Removed", Domains: []string{"example.com", "203.0.113.0/24"}})
	ctx, report := WithApplyReport(WithCachedEntries(t.Context()))
	if err := m.Apply(ctx, state, state.Rules); err != nil {
		t.Fatal(err)
	}
	for _, rule := range state.Rules {
		if !report.Rules[rule.ID].Blocked {
			t.Fatalf("guessed prior location for overlapping rule %s: %+v", rule.ID, report.Rules[rule.ID])
		}
	}
}

func TestPreviousLocationRequiresDisjointDomainsAndNetworks(t *testing.T) {
	missing := desiredInstance{Key: "p::missing", Provider: config.Provider{ID: "p"}, Domains: []string{"example.com", "203.0.113.0/24"}}
	old := missing
	old.Key = "p::old"
	for _, overlap := range []string{"sub.example.com", "203.0.113.9", "203.0.0.0/16"} {
		other := desiredInstance{Key: "p::other", Provider: missing.Provider, Domains: []string{overlap}}
		saved := []applyPlan{{desired: old}, {desired: other}}
		if got := matchingPreviousRoute(missing, saved, []desiredInstance{missing}); got != nil {
			t.Errorf("accepted previously shared destination %s", overlap)
		}
		if got := matchingPreviousRoute(missing, saved[:1], []desiredInstance{missing, other}); got != nil {
			t.Errorf("accepted newly shared destination %s", overlap)
		}
	}
}

func TestAutomaticApplyUsesPreviousLocationOnlyForUnambiguousEntries(t *testing.T) {
	for _, shared := range []bool{false, true} {
		name := "unique"
		if shared {
			name = "overlapping"
		}
		t.Run(name, func(t *testing.T) {
			m, state, _ := rollbackTestManager(t)
			other := config.Rule{ID: "other", ProviderID: "provider_1", Enabled: true, SelectedLocation: "New", Domains: []string{"other.example"}}
			if shared {
				other.Domains = append(other.Domains, "example.com")
			}
			state.Rules = append(state.Rules, other)
			if err := m.Apply(t.Context(), state, state.Rules); err != nil {
				t.Fatal(err)
			}
			oldPID := m.current["provider_1::old"].PID
			state.Rules[0].SelectedLocation = "Removed"
			if err := m.Apply(WithCachedEntries(t.Context()), state, state.Rules); err != nil {
				t.Fatal(err)
			}
			if !shared {
				if old := m.current["provider_1::old"]; old == nil || old.PID != oldPID || m.current["provider_1::removed"] != nil {
					t.Fatal("previous VPN was not kept with its actual identity")
				}
				return
			}
			blocked := m.current["provider_1::removed"]
			if blocked == nil || m.current["provider_1::old"] != nil || m.current["provider_1::new"] == nil {
				t.Fatal("ambiguous prior routing was treated as a known VPN for the missing location")
			}
			assertAppliedEntries(t, blocked, "example.com", "203.0.113.0/24")
		})
	}
}

func assertAppliedEntries(t *testing.T, instance *managedInstance, want ...string) {
	t.Helper()
	data, err := os.ReadFile(instance.domainListPath)
	if err != nil || !slices.Equal(strings.Fields(string(data)), want) {
		t.Fatalf("routing entries for %s: got %q, want %v, error %v", instance.Location, data, want, err)
	}
}
