package routing

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"xiomi-router-driver/internal/config"
)

func TestDeviceExclusionsApplyAndRollbackRuntimeFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "excluded.macs")
	script := filepath.Join(dir, "routes.sh")
	os.WriteFile(script, []byte("#!/bin/sh\ncat \"$DEVICE_BYPASS_FILE\" > \"$DEVICE_TEST_DIR/applied\"\nprintf '%s\\n' \"$DEVICE_BYPASS_CONNTRACK_IPS\" >> \"$DEVICE_TEST_DIR/cleanups\"\nif grep -q '02:00:00:00:00:02' \"$DEVICE_BYPASS_FILE\"; then exit 1; fi\n"), 0700)
	t.Setenv("DEVICE_TEST_DIR", dir)
	r := NewRunner(script)
	r.SetDeviceExclusionsPath(path)
	old := []config.DeviceExclusion{{MAC: "02:00:00:00:00:01"}}
	if err := r.ApplyDeviceExclusions(context.Background(), config.DefaultRoutingSettings(), old, nil); err != nil {
		t.Fatal(err)
	}
	err := r.ApplyDeviceExclusions(context.Background(), config.DefaultRoutingSettings(), []config.DeviceExclusion{{MAC: "02:00:00:00:00:02"}}, []string{"192.168.31.99"})
	if err == nil {
		t.Fatal("failed apply reported success")
	}
	for _, file := range []string{path, filepath.Join(dir, "applied")} {
		data, _ := os.ReadFile(file)
		if string(data) != "02:00:00:00:00:01\n" {
			t.Fatalf("rollback %s: %q", file, data)
		}
	}
	cleanup, _ := os.ReadFile(filepath.Join(dir, "cleanups"))
	if strings.Count(string(cleanup), "192.168.31.99") != 2 {
		t.Fatalf("rollback must also clear changed-device mappings: %q", cleanup)
	}
}

func TestDeviceBypassScriptUsesOnlyManagerExceptions(t *testing.T) {
	script := string(embeddedScript)
	start := strings.Index(script, "ensure_device_exclusions() {")
	if start < 0 {
		t.Fatal("device exclusion rules missing")
	}
	end := strings.Index(script[start:], "\n# End device exclusions") + start
	if end < start {
		t.Fatal("device helper boundary missing")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "macs")
	os.WriteFile(path, []byte("02:00:00:00:00:01\n"), 0600)
	harness := `DEVICE_BYPASS_FILE="$MAC_FILE"
DEVICE_BYPASS_SET=vpn_device_bypass
DEVICE_BYPASS_MARK=0x40000000
LAN_IFACE=br-lan
GUARD_CHAIN=VPN_MANAGER_GUARD
EGRESS_GUARD_CHAIN=VPN_MANAGER_EGRESS
PINNED_FILTER_CHAIN=VPN_MANAGER_PIN_GUARD
ipset() { echo "ipset $*"; }
ip() { case "$*" in *show*) return 0;; esac; echo "ip $*"; }
iptables() { case " $* " in *" -D "*|*" -C "*) return 1;; esac; echo "iptables $*"; }
` + script[start:end] + "\nensure_device_exclusions\n"
	cmd := exec.Command("sh", "-c", harness)
	cmd.Env = append(os.Environ(), "MAC_FILE="+path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	for _, want := range []string{"ipset add vpn_device_bypass_next 02:00:00:00:00:01", "ipset swap vpn_device_bypass_next vpn_device_bypass", "-I PREROUTING 1 -i br-lan -j VPN_DEVICE_BYPASS", "rule add pref 50 fwmark 0x40000000 lookup main", "VPN_MANAGER_GUARD 1 -m mark --mark 0x40000000 -j RETURN", "VPN_MANAGER_EGRESS 1 -m mark --mark 0x40000000 -j RETURN", "--dport 53 -m mark --mark 0x40000000 -j ACCEPT"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
	if strings.Contains(string(out), "-I FORWARD") || strings.Contains(string(out), "-F FORWARD") {
		t.Fatal("device bypass must not override normal firewall")
	}
}
