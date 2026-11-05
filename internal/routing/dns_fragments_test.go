package routing

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDNSCleanupRetiresGuardOnlyFragmentsAndPreservesOtherLiveRoutes(t *testing.T) {
	dir := t.TempDir()
	fragments := map[string]string{
		"vpn_dns_current.conf": "ipset=/youtube.com/vpn_manager_guard,vpn_hosts_current\nipset=/old.test/vpn_manager_guard,vpn_hosts_current\n",
		"vpn_dns_backup.conf":  "ipset=/chatgpt.com/vpn_manager_guard,vpn_hosts_other\n",
		"vpn_dns_retired.conf": "ipset=/youtube.com/vpn_manager_guard\n",
		"vpn_dns_pinned.conf":  "ipset=/chatgpt.com/vpn_manager_guard,vpn_pin_old\n",
		"vpn_dns_dead.conf":    "ipset=/old.test/vpn_manager_guard,vpn_hosts_dead\n",
		"vpn_dns_stopped.conf": "ipset=/stopped.test/vpn_manager_guard\n",
		"vpn_dns_mixed.conf":   "ipset=/youtube.com/unmatched.test/vpn_manager_guard\nserver=/youtube.com/unmatched.test/127.0.0.1#15353\n",
	}
	for name, content := range fragments {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	source := string(embeddedScript)
	start := strings.Index(source, "dnsmasq_conf_references_live_ipset() {")
	end := strings.Index(source, "render_dnsmasq_config() {")
	if start < 0 || end <= start {
		t.Fatal("DNS fragment cleanup functions not found")
	}
	// Only the external kernel/ipset lookup is simulated; execute the real
	// cleanup functions and check which files dnsmasq would still load.
	program := `set -eu
GUARD_IPSET=vpn_manager_guard
DNSMASQ_CONFIG_FILE="$1/vpn_dns_current.conf"
DNSMASQ_CONFIG_CHANGED=0
ipset() {
  case "$2" in vpn_manager_guard|vpn_pin_old|vpn_hosts_current|vpn_hosts_other) return 0;; *) return 1;; esac
}
` + source[start:end] + "\ncleanup_dnsmasq_conf_fragments\n"
	command := exec.Command("sh", "-c", program, "dns-cleanup-test", dir)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("DNS cleanup failed: %v\n%s", err, output)
	}
	for _, name := range []string{"vpn_dns_retired.conf", "vpn_dns_pinned.conf", "vpn_dns_dead.conf"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("retired DNS fragment %s is still loaded (stat: %v)", name, err)
		}
	}
	for _, name := range []string{"vpn_dns_current.conf", "vpn_dns_backup.conf"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("live DNS fragment %s was removed: %v", name, err)
		}
	}
	for name, want := range map[string]string{
		"vpn_dns_stopped.conf": "ipset=/stopped.test/vpn_manager_guard\n",
		"vpn_dns_mixed.conf":   "ipset=/unmatched.test/vpn_manager_guard\nserver=/unmatched.test/127.0.0.1#15353\n",
	} {
		content, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(content) != want {
			t.Errorf("stopped-domain protection in %s = %q, %v; want %q", name, content, err, want)
		}
	}
}

func TestDeletingTunnelDoesNotLeaveDNSSelectorShadowingReplacement(t *testing.T) {
	dir := t.TempDir()
	retired := filepath.Join(dir, "vpn_dns_retired.conf")
	replacement := filepath.Join(dir, "vpn_dns_live.conf")
	if err := os.WriteFile(retired, []byte("ipset=/youtube.com/vpn_manager_guard,vpn_hosts_old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacement, []byte("ipset=/youtube.com/vpn_manager_guard,vpn_hosts_live\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := string(embeddedScript)
	cleanupStart := strings.Index(source, "dnsmasq_conf_references_live_ipset() {")
	cleanupEnd := strings.Index(source, "render_dnsmasq_config() {")
	deleteStart := strings.Index(source, "delete_routes() {")
	deleteEnd := strings.Index(source, "# --- SCRIPT LOGIC ---")
	if cleanupStart < 0 || cleanupEnd <= cleanupStart || deleteStart < 0 || deleteEnd <= deleteStart {
		t.Fatal("routing script functions not found")
	}
	program := `set -eu
GUARD_IPSET=vpn_manager_guard
PINNED_IPSETS=
TABLE_NUM=101
IPSET_NAME=vpn_hosts_old
DNSMASQ_CONFIG_FILE="$1/vpn_dns_retired.conf"
DNSMASQ_CONFIG_BACKUP="$1/retired.backup"
DNSMASQ_CONFIG_CHANGED=0
cleanup_firewall() { :; }
ip() { :; }
ipset() { test "$1" != destroy || return 0; case "$2" in vpn_manager_guard|vpn_hosts_live) return 0;; *) return 1;; esac; }
apply_dnsmasq_changes() { :; }
` + source[cleanupStart:cleanupEnd] + source[deleteStart:deleteEnd] + "\ndelete_routes\n"
	command := exec.Command("sh", "-c", program, "delete-tunnel-test", dir)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("tunnel retirement failed: %v\n%s", err, output)
	}
	if _, err := os.Stat(retired); !os.IsNotExist(err) {
		t.Fatalf("retired tunnel still shadows the replacement DNS selector: %v", err)
	}
	if _, err := os.Stat(replacement); err != nil {
		t.Fatalf("replacement tunnel DNS selector was removed: %v", err)
	}
}
