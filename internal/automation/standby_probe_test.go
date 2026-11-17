package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"xiomi-router-driver/internal/subscription"
)

func TestStandbyProbeReapsProcessWhenListenerNeverStarts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires POSIX process signals")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "pid")
	binary := filepath.Join(dir, "sing-box")
	if err := os.WriteFile(binary, []byte(fmt.Sprintf("#!/bin/sh\necho $$ > '%s.tmp'\nmv '%s.tmp' '%s'\nexec sleep 60\n", marker, marker, marker)), 0o700); err != nil {
		t.Fatal(err)
	}
	// Process startup can be delayed when all Go packages run concurrently on a
	// busy builder. Keep the test bounded while allowing the helper shell to run.
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	go func() {
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := os.Stat(marker); err == nil {
					cancel()
					return
				}
			}
		}
	}()
	result := probeStandbyEntry(ctx, binary, subscription.Entry{Outbound: map[string]any{"type": "direct"}})
	if result.Healthy {
		t.Fatal("process without SOCKS listener was accepted")
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read standby process marker after %q: %v", result.Detail, err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
		t.Fatalf("standby process %d survived cleanup: %v", pid, err)
	}
}

func TestStandbyProbeUsesNativeXrayConfig(t *testing.T) {
	dir := t.TempDir()
	captured := filepath.Join(dir, "captured.json")
	binary := filepath.Join(dir, "xray")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\ncp \"$3\" '"+captured+"'\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	entry := subscription.Entry{Outbound: map[string]any{"protocol": "vless", "tag": "proxy", "settings": map[string]any{"address": "example.com", "port": 443, "id": "test"}, "streamSettings": map[string]any{"network": "xhttp"}}}
	result := probeStandbyEntry(t.Context(), binary, entry)
	if result.Healthy {
		t.Fatal("exited engine was marked healthy")
	}
	raw, err := os.ReadFile(captured)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	inbound := cfg["inbounds"].([]any)[0].(map[string]any)
	if inbound["protocol"] != "socks" || inbound["listen"] != "127.0.0.1" {
		t.Fatalf("wrong native probe inbound: %+v", inbound)
	}
	if _, exists := cfg["route"]; exists {
		t.Fatal("sing-box settings leaked into Xray config")
	}
	if cfg["outbounds"].([]any)[0].(map[string]any)["protocol"] != "vless" {
		t.Fatal("native outbound lost")
	}
}
