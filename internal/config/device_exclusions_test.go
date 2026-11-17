package config

import (
	"path/filepath"
	"testing"
)

func TestDeviceExclusionsPersistIndependentlyOfRoutingSaves(t *testing.T) {
	db := openTestDB(t)
	m := NewManager(db, filepath.Join(t.TempDir(), "state.json"))
	stale, err := m.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(stale.ExcludedDevices) != 0 {
		t.Fatal("devices must follow routes by default")
	}
	if err := m.SetDeviceExclusion("AA-BB-CC-DD-EE-02", "Phone", true); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Save(stale); err != nil {
		t.Fatal(err)
	}
	reloaded := NewManager(db, "")
	state, err := reloaded.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.ExcludedDevices) != 1 || state.ExcludedDevices[0].MAC != "aa:bb:cc:dd:ee:02" {
		t.Fatalf("lost exception: %+v", state.ExcludedDevices)
	}
	if err := reloaded.SetDeviceExclusion("aa:bb:cc:dd:ee:02", "", false); err != nil {
		t.Fatal(err)
	}
	items, err := reloaded.DeviceExclusions()
	if err != nil || len(items) != 0 {
		t.Fatalf("restore: %+v %v", items, err)
	}
}

func TestDeviceExclusionsRejectInvalidOrMulticastAddresses(t *testing.T) {
	m := NewManager(openTestDB(t), "")
	for _, mac := range []string{"", "192.168.31.3", "00:00:00:00:00:00", "ff:ff:ff:ff:ff:ff", "01:00:5e:00:00:01", "02:00:00:00:00:01; reboot"} {
		if err := m.SetDeviceExclusion(mac, "Phone", true); err == nil {
			t.Errorf("accepted %q", mac)
		}
	}
}
