package config

import (
	"path/filepath"
	"testing"
)

func TestPrioritySelectionModePersistsAndMigratesOldSchema(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`CREATE TABLE priority_policies (id TEXT PRIMARY KEY, provider_id TEXT NOT NULL, rule_id TEXT NOT NULL DEFAULT '', name TEXT NOT NULL, enabled INTEGER NOT NULL, position INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(db, filepath.Join(t.TempDir(), "state.json"))
	state := DefaultState()
	state.Providers = []Provider{{ID: "vpn", Name: "VPN", Type: ProviderTypeSubscription, Source: "https://example.com/sub", Enabled: true}}
	state.PriorityPolicies = []PriorityPolicy{
		{ID: "fast", ProviderID: "vpn", Name: "Fastest", Enabled: true, SelectionMode: "latency", Targets: []PriorityTarget{{Location: "A"}}},
		{ID: "old", ProviderID: "vpn", Name: "Legacy", Targets: []PriorityTarget{{Location: "B"}}},
	}
	if _, err := manager.Save(state); err != nil {
		t.Fatal(err)
	}
	loaded, err := manager.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PriorityPolicies[0].SelectionMode != "latency" || NormalizePrioritySelectionMode(loaded.PriorityPolicies[1].SelectionMode) != "priority" {
		t.Fatalf("selection mode was lost or legacy order changed: %+v", loaded.PriorityPolicies)
	}
}
