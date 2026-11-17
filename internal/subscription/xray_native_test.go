package subscription

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"xiomi-router-driver/internal/config"
	"xiomi-router-driver/internal/runtimehealth"
)

const nativeXrayFixture = `{"remarks":"XHTTP reserve","outbounds":[{"tag":"proxy","protocol":"vless","settings":{"address":"edge.example.com","port":443,"id":"11111111-1111-1111-1111-111111111111","encryption":"none"},"streamSettings":{"network":"xhttp","security":"reality","realitySettings":{"serverName":"cdn.example.com","publicKey":"test-key","shortId":"abcd"},"xhttpSettings":{"mode":"packet-up","path":"/upload","extra":{"downloadSettings":{"address":"download.example.com","port":443,"network":"xhttp","security":"tls","tlsSettings":{"serverName":"download.example.com"},"xhttpSettings":{"path":"/download","mode":"stream-one"}},"xmux":{"maxConnections":4}}}}}]}`

func TestParseEntriesPreservesNativeXraySettings(t *testing.T) {
	for _, encrypted := range []bool{false, true} {
		var config map[string]any
		if err := json.Unmarshal([]byte(nativeXrayFixture), &config); err != nil {
			t.Fatal(err)
		}
		outbound := config["outbounds"].([]any)[0].(map[string]any)
		if encrypted {
			outbound["settings"].(map[string]any)["encryption"] = "mlkem768x25519plus.native.0rtt.test-key"
			outbound["streamSettings"] = map[string]any{"network": "tcp", "security": "none", "finalmask": map[string]any{"tcp": []any{map[string]any{"type": "header-custom", "settings": map[string]any{"request": "test-mask"}}}}}
		}
		payload, _ := json.Marshal(config)
		entries, err := ParseEntries(string(payload))
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Address != "edge.example.com:443" || entries[0].Type != "vless" {
			t.Fatalf("lost native endpoint: %+v", entries)
		}
		got, _ := json.Marshal(entries[0].Outbound)
		want, _ := json.Marshal(outbound)
		if string(got) != string(want) {
			t.Fatalf("native settings changed: got %s want %s", got, want)
		}
	}
}

func TestMissingXrayPreservesActiveTunnel(t *testing.T) {
	m, state, dir := rollbackTestManager(t)
	if err := m.Apply(t.Context(), state, state.Rules); err != nil {
		t.Fatal(err)
	}
	old := m.current["provider_1::old"]
	actions, _ := os.ReadFile(filepath.Join(dir, "actions"))
	m.xrayBinary = filepath.Join(dir, "missing-xray")
	state.Providers[0].Source = strings.Replace(nativeXrayFixture, "{", "{\n", 1)
	state.Rules[0].SelectedLocation = "XHTTP reserve"
	if err := m.Apply(t.Context(), state, state.Rules); err == nil || !strings.Contains(err.Error(), "Xray is required") {
		t.Fatalf("missing binary not reported: %v", err)
	}
	if m.current["provider_1::old"] != old || !runtimehealth.ProcessAlive(old.PID) {
		t.Fatal("missing Xray disrupted active VPN")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "actions")); string(got) != string(actions) {
		t.Fatal("missing Xray changed routes")
	}
}

func TestNativeXrayRollbackPreservesEngine(t *testing.T) {
	m, state, dir := rollbackTestManager(t)
	m.xrayBinary = filepath.Join(dir, "xray")
	script := "#!/bin/sh\nprintf 'xray\\n' >> \"$SUBSCRIPTION_TEST_DIR/xray-starts\"\nexec sleep 120\n"
	if err := os.WriteFile(m.xrayBinary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	state.Providers[0].Source = "[\n" + strings.Replace(nativeXrayFixture, "XHTTP reserve", "Old", 1) + "," + strings.Replace(nativeXrayFixture, "XHTTP reserve", "New", 1) + "]"
	if err := m.Apply(t.Context(), state, state.Rules); err != nil {
		t.Fatal(err)
	}
	old := m.current["provider_1::old"]
	if !runtimehealth.ProcessAlive(old.PID) {
		t.Fatal("native runtime did not start")
	}
	newIface := deriveRoutingSettings(state.Routing, shortHash("provider_1\nNew"), 0).VPNIface
	if err := os.WriteFile(filepath.Join(dir, "fail-"+newIface), nil, 0600); err != nil {
		t.Fatal(err)
	}
	state.Rules[0].SelectedLocation = "New"
	if err := m.Apply(t.Context(), state, state.Rules); err == nil {
		t.Fatal("routing failure not reported")
	}
	restored := m.current["provider_1::old"]
	if restored == nil || !strings.HasSuffix(restored.ConfigPath, ".xray.json") || !runtimehealth.ProcessAlive(restored.PID) {
		t.Fatal("rollback did not restore native Xray")
	}
	if starts, _ := os.ReadFile(filepath.Join(dir, "xray-starts")); len(strings.Fields(string(starts))) != 3 {
		t.Fatalf("rollback did not use Xray three times: %q", starts)
	}
}

func TestXrayNativeRuntimePlan(t *testing.T) {
	entries, err := ParseEntries(nativeXrayFixture)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(t.TempDir(), t.TempDir(), nil, nil, nil)
	plans, err := manager.buildApplyPlans([]desiredInstance{{Key: "p::reserve", Provider: config.Provider{ID: "p", Name: "Provider"}, Location: entries[0].Name, Entry: entries[0]}}, config.DefaultRoutingSettings())
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || !strings.HasSuffix(plans[0].configPath, ".xray.json") {
		t.Fatalf("wrong engine config path: %+v", plans)
	}
	var got map[string]any
	if err := json.Unmarshal(plans[0].configData, &got); err != nil {
		t.Fatal(err)
	}
	inbound := got["inbounds"].([]any)[0].(map[string]any)
	if inbound["protocol"] != "tun" {
		t.Fatalf("not a native TUN: %+v", inbound)
	}
	settings := inbound["settings"].(map[string]any)
	if settings["name"] != plans[0].settings.VPNIface || !reflect.DeepEqual(settings["gateway"], []any{"172.29.0.1/30"}) {
		t.Fatalf("unexpected interface settings: %+v", settings)
	}
	if len(settings["autoSystemRoutingTable"].([]any)) != 0 || settings["autoSystemDnsToGateway"] != false {
		t.Fatal("Xray must not modify system routes or DNS")
	}
	proxy := got["outbounds"].([]any)[0].(map[string]any)
	want, _ := json.Marshal(entries[0].Outbound)
	actual, _ := json.Marshal(proxy)
	if string(actual) != string(want) {
		t.Fatal("TUN config lost outbound parameters")
	}
}

func TestXrayNativeStartUsesXrayExecutable(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "started")
	binary := filepath.Join(dir, "xray")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf 'xray' > '"+marker+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VPN_MANAGER_XRAY_BIN", binary)
	t.Setenv("VPN_MANAGER_SINGBOX_BIN", filepath.Join(dir, "missing-sing-box"))
	manager := NewManager(dir, dir, openSubscriptionTestDB(t), nil, nil)
	manager.mu.Lock()
	if err := manager.ensureReadyLocked(); err != nil {
		manager.mu.Unlock()
		t.Fatal(err)
	}
	plan := applyPlan{desired: desiredInstance{Key: "native", Location: "Native"}, configPath: filepath.Join(manager.runtimeDir, "native.xray.json"), configData: []byte("{}"), domainListPath: filepath.Join(manager.runtimeDir, "native.xray.domains.list")}
	instance, err := manager.startPlannedInstance(plan)
	manager.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-instance.done:
	case <-time.After(3 * time.Second):
		t.Fatal("runtime did not exit")
	}
	if raw, err := os.ReadFile(marker); err != nil || string(raw) != "xray" {
		t.Fatalf("wrong engine started: %s %v", raw, err)
	}
}
