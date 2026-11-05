package automation

import (
	"context"
	"testing"
	"time"

	"xiomi-router-driver/internal/probe"
	"xiomi-router-driver/internal/subscription"
)

func TestPriorityServerPingDeduplicatesPoliciesAndRefreshesOncePerMinute(t *testing.T) {
	state := priorityTestState(t, 443, 8443)
	state.PriorityPolicies = append(state.PriorityPolicies, state.PriorityPolicies[0])
	state.PriorityPolicies[1].ID = "second-policy"
	s := &Supervisor{dataDir: t.TempDir(), priority: newPriorityRuntime()}
	if _, _, err := subscription.FetchEntriesCachedContext(t.Context(), state.Providers[0].Source, s.subscriptionRuntimeDir()); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.priority.measurePings = func(_ context.Context, locations []probe.Location) []probe.Location {
		calls++
		if len(locations) != 2 {
			t.Fatalf("expected two unique servers, got %d", len(locations))
		}
		for index := range locations {
			locations[index].LatencyMs = int64(calls * 40)
		}
		return locations
	}
	now := time.Now()
	s.refreshPriorityServerPings(t.Context(), state, now)
	s.refreshPriorityServerPings(t.Context(), state, now.Add(30*time.Second))
	if calls != 1 {
		t.Fatalf("ping refreshed too frequently: %d", calls)
	}
	s.refreshPriorityServerPings(t.Context(), state, now.Add(time.Minute))
	if calls != 2 {
		t.Fatalf("ping did not refresh: %d", calls)
	}
	key := standbyProbeKey(state.Providers[0], "Germany")
	if got := s.priority.pings[key].Ms; got != 60 {
		t.Fatalf("expected average of recent 40/80 ms samples, got %d", got)
	}
}

func TestPriorityServerPingClearsOldSuccessOnErrorAndSourceChange(t *testing.T) {
	state := priorityTestState(t, 443, 8443)
	s := &Supervisor{dataDir: t.TempDir(), priority: newPriorityRuntime()}
	if _, _, err := subscription.FetchEntriesCachedContext(t.Context(), state.Providers[0].Source, s.subscriptionRuntimeDir()); err != nil {
		t.Fatal(err)
	}
	failed := false
	s.priority.measurePings = func(_ context.Context, locations []probe.Location) []probe.Location {
		for index := range locations {
			locations[index].LatencyMs = 40
			if failed {
				locations[index].LatencyMs = 0
				locations[index].LatencyError = "timeout"
			}
		}
		return locations
	}
	now := time.Now()
	s.refreshPriorityServerPings(t.Context(), state, now)
	key := standbyProbeKey(state.Providers[0], "Germany")
	if !s.priority.pings[key].fresh(now) {
		t.Fatal("new ping is not fresh")
	}
	if s.priority.pings[key].fresh(now.Add(3 * time.Minute)) {
		t.Fatal("old ping remains usable")
	}
	failed = true
	s.refreshPriorityServerPings(t.Context(), state, now.Add(time.Minute))
	if ping := s.priority.pings[key]; ping.Ms != 0 || ping.Error == "" {
		t.Fatalf("error retained old fast sample: %+v", ping)
	}
	state.Providers[0].Source += " "
	s.refreshPriorityServerPings(t.Context(), state, now.Add(61*time.Second))
	newKey := standbyProbeKey(state.Providers[0], "Germany")
	if ping := s.priority.pings[newKey]; ping.Ms != 0 || ping.Error == "" {
		t.Fatalf("changed source reused old ping: %+v", ping)
	}
}
