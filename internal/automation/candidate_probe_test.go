package automation

import (
	"context"
	"path/filepath"
	"testing"

	"xiomi-router-driver/internal/subscription"
)

func TestCandidateProbeKeepsIdentityAndActualVPNHealth(t *testing.T) {
	results := probeCandidateEntries(t.Context(), "test", []subscription.Entry{{Name: "A"}, {Name: "B"}}, func(_ context.Context, _ string, entry subscription.Entry) providerProbeResult {
		return providerProbeResult{Healthy: entry.Name == "A", LatencyMs: 40, Detail: "checked HTTPS"}
	})
	if len(results) != 2 || results[0].Location != "A" || !results[0].Healthy || results[1].Location != "B" || results[1].Healthy {
		t.Fatalf("invalid candidate mapping: %+v", results)
	}
}

func TestCandidateProbeSelectsEngineForEachEntry(t *testing.T) {
	dir := t.TempDir()
	singbox, xray := filepath.Join(dir, "sing-box"), filepath.Join(dir, "xray")
	t.Setenv("VPN_MANAGER_SINGBOX_BIN", singbox)
	t.Setenv("VPN_MANAGER_XRAY_BIN", xray)
	entries := []subscription.Entry{{Name: "Native", Outbound: map[string]any{"protocol": "vless"}}, {Name: "Standard", Outbound: map[string]any{"type": "vless"}}}
	results := probeCandidateEntries(t.Context(), dir, entries, func(_ context.Context, binary string, entry subscription.Entry) providerProbeResult {
		want := singbox
		if entry.UsesXray() {
			want = xray
		}
		return providerProbeResult{Healthy: binary == want, Detail: binary}
	})
	for _, result := range results {
		if !result.Healthy {
			t.Fatalf("wrong engine for %s: %s", result.Location, result.Detail)
		}
	}
}
