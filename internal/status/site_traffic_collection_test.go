package status

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"xiomi-router-driver/internal/config"
)

func TestSiteTrafficReportsEffectiveCollectionSettings(t *testing.T) {
	db := openSiteTrafficTestDB(t)
	manager := config.NewManager(db, filepath.Join(t.TempDir(), "state.json"))
	service := &Service{state: manager, siteTraffic: newSiteTrafficStore(db)}
	t.Setenv("VPN_MANAGER_SITE_TRAFFIC_SAMPLE_INTERVAL", "")
	for _, item := range []struct {
		profile string
		env     string
		enabled bool
		seconds int
	}{
		{"minimal", "", false, 0},
		{"normal", "", true, 120},
		{"detailed", "", true, 30},
		{"normal", "0", false, 0},
		{"minimal", "45s", true, 45},
	} {
		t.Run(item.profile+"/"+item.env, func(t *testing.T) {
			state := config.DefaultState()
			state.Routing.LoadProfile = item.profile
			if _, err := manager.Save(state); err != nil {
				t.Fatal(err)
			}
			t.Setenv("VPN_MANAGER_SITE_TRAFFIC_SAMPLE_INTERVAL", item.env)
			result, err := service.SiteTraffic("", "bytes", "desc", "", "", 1, 20)
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			var response struct {
				Collection *struct {
					Enabled         bool `json:"enabled"`
					IntervalSeconds int  `json:"intervalSeconds"`
				} `json:"collection"`
			}
			if err := json.Unmarshal(data, &response); err != nil {
				t.Fatal(err)
			}
			if response.Collection == nil || response.Collection.Enabled != item.enabled || response.Collection.IntervalSeconds != item.seconds {
				t.Fatalf("collection does not reflect effective settings: %s", data)
			}
		})
	}
}
