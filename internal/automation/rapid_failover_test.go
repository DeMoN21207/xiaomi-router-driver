package automation

import (
	"context"
	"sync"
	"testing"
	"time"

	"xiomi-router-driver/internal/config"
	"xiomi-router-driver/internal/probe"
	"xiomi-router-driver/internal/status"
	"xiomi-router-driver/internal/subscription"
)

func TestRapidPriorityChecksKeepTwoHealthyReservesWarmWithinProbeBudget(t *testing.T) {
	s, state, now := latencyPolicyFixture(t)
	state.Automation.FailoverFailureSeconds = 6
	policy := state.PriorityPolicies[0]
	policy.Targets = append(policy.Targets, config.PriorityTarget{Location: "Sweden"})
	s.priority.pings[standbyProbeKey(state.Providers[0], "Germany")] = priorityServerPing{Ms: 10, Checked: now}
	s.priority.pings[standbyProbeKey(state.Providers[0], "Netherlands")] = priorityServerPing{Ms: 20, Checked: now}
	s.priority.pings[standbyProbeKey(state.Providers[0], "Sweden")] = priorityServerPing{Ms: 30, Checked: now}
	s.priority.decisions[policy.ID] = priorityDecision{ActiveLocation: "Germany", Mode: "provider", Since: now.Add(-time.Hour)}
	var mu sync.Mutex
	checked := map[string]bool{}
	s.probeStandby = func(ctx context.Context, _ config.Provider, location string) providerProbeResult {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 1600*time.Millisecond {
			t.Error("reserve probe is not bounded for rapid failover")
		}
		mu.Lock()
		checked[location] = true
		mu.Unlock()
		return providerProbeResult{Healthy: true}
	}
	snapshot := status.Snapshot{SubscriptionRuntime: []subscription.RuntimeSnapshot{{Key: "provider_1::germany", Status: "running", InterfaceName: "sb-active"}}}
	decision, _ := s.evaluatePriorityPolicyLocked(t.Context(), state, snapshot, policy, now)
	if decision.ActiveLocation != "Germany" {
		t.Fatal("warming reserves switched a healthy active server")
	}
	if !checked["Netherlands"] || !checked["Sweden"] {
		t.Fatalf("reserves were not checked ahead of failure: %v", checked)
	}
}

func TestRapidPriorityFailureWindowSelectsReadyReserveAtSixSeconds(t *testing.T) {
	s, state, now := latencyPolicyFixture(t)
	state.Automation.FailoverFailureSeconds = 6
	policy := state.PriorityPolicies[0]
	s.priority.decisions[policy.ID] = priorityDecision{ActiveLocation: "Germany", Mode: "provider", Since: now.Add(-time.Hour)}
	s.probeTunnel = func(ctx context.Context, _, _ string) providerProbeResult {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 1600*time.Millisecond {
			t.Error("active tunnel probe is not bounded for rapid failover")
		}
		return providerProbeResult{}
	}
	snapshot := status.Snapshot{SubscriptionRuntime: []subscription.RuntimeSnapshot{{Key: "provider_1::germany", Status: "running", InterfaceName: "sb-active"}}}
	for _, seconds := range []int{0, 2, 4, 6} {
		decision, _ := s.evaluatePriorityPolicyLocked(t.Context(), state, snapshot, policy, now.Add(time.Duration(seconds)*time.Second))
		want := "Germany"
		if seconds == 6 {
			want = "Netherlands"
		}
		if decision.ActiveLocation != want {
			t.Fatalf("at%ds got %s, want%s", seconds, decision.ActiveLocation, want)
		}
	}
}

func TestRapidPriorityEvaluationDoesNotWaitForICMPCollection(t *testing.T) {
	s, state, _ := latencyPolicyFixture(t)
	state.Automation.FailoverFailureSeconds = 6
	s.applyState = func(context.Context, config.State) error { return nil }
	release := make(chan struct{})
	done := make(chan struct{})
	s.priority.measurePings = func(ctx context.Context, locations []probe.Location) []probe.Location {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return locations
	}
	go func() {
		s.maybePriorityPolicies(t.Context(), state, status.Snapshot{WAN: status.WANStatus{State: "up"}})
		close(done)
	}()
	defer func() { close(release); <-done }()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("failure evaluation blocked on ICMP ping collection")
	}
}

func TestRapidPriorityPoliciesShareActiveProbeOnlyWithinOneCycle(t *testing.T) {
	s, state, _ := latencyPolicyFixture(t)
	state.Automation.FailoverFailureSeconds = 6
	second := state.PriorityPolicies[0]
	second.ID = "second-policy"
	state.PriorityPolicies = append(state.PriorityPolicies, second)
	s.applyState = func(context.Context, config.State) error { return nil }
	s.priority.measurePings = nil
	for _, policy := range state.PriorityPolicies {
		s.priority.decisions[policy.ID] = priorityDecision{ActiveLocation: "Germany", Mode: "provider", Since: time.Now().Add(-time.Hour)}
	}
	var calls int
	s.probeTunnel = func(context.Context, string, string) providerProbeResult {
		calls++
		return providerProbeResult{Healthy: true}
	}
	snapshot := status.Snapshot{WAN: status.WANStatus{State: "up"}, SubscriptionRuntime: []subscription.RuntimeSnapshot{{Key: "provider_1::germany", Status: "running", InterfaceName: "sb-active"}}}
	for cycle := 1; cycle <= 2; cycle++ {
		s.maybePriorityPolicies(t.Context(), state, snapshot)
		if calls != cycle {
			t.Fatalf("after cycle %d, active tunnel checked %d times; want one fresh check per cycle", cycle, calls)
		}
	}
}
