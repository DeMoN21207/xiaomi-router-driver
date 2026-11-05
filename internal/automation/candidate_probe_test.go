package automation

import (
	"context"
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
