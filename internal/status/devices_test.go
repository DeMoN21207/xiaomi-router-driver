package status

import (
	"strings"
	"testing"
	"xiomi-router-driver/internal/config"
)

func TestDeviceInventoryKeepsOfflineExceptionsAndFollowsMAC(t *testing.T) {
	leases := "2000 02:00:00:00:00:01 192.168.31.4 old *\n2000 02:00:00:00:00:02 192.168.31.5 phone *\n"
	arp := "IP address HW type Flags HW address Mask Device\n192.168.31.4 0x1 0x2 02:00:00:00:00:03 * br-lan\n192.168.31.99 0x1 0x2 02:00:00:00:00:04 * wan\n"
	items, err := parseNetworkDevices(strings.NewReader(leases), strings.NewReader(arp), "br-lan", 1000, []config.DeviceExclusion{{MAC: "02:00:00:00:00:01", Name: "Old phone"}})
	if err != nil {
		t.Fatal(err)
	}
	byMAC := map[string]NetworkDevice{}
	for _, item := range items {
		byMAC[item.MAC] = item
	}
	if !byMAC["02:00:00:00:00:01"].Excluded || len(byMAC["02:00:00:00:00:01"].IPs) != 0 {
		t.Fatalf("stale DHCP assigned exclusion to reused IP: %+v", items)
	}
	if byMAC["02:00:00:00:00:03"].Excluded || len(byMAC["02:00:00:00:00:03"].IPs) != 1 {
		t.Fatalf("new device must follow routes: %+v", items)
	}
	if _, ok := byMAC["02:00:00:00:00:04"]; ok {
		t.Fatal("WAN neighbor listed as LAN device")
	}
}

func TestDeviceInventoryEmptyDefaultAndExpiredLeases(t *testing.T) {
	items, err := parseNetworkDevices(strings.NewReader("1 02:00:00:00:00:01 192.168.31.4 phone *\n"), strings.NewReader(""), "br-lan", 1000, nil)
	if err != nil || len(items) != 0 {
		t.Fatalf("expired lease: %+v %v", items, err)
	}
}
