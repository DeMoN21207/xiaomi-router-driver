package automation

import (
	"context"
	"testing"
	"time"

	"xiomi-router-driver/internal/config"
	"xiomi-router-driver/internal/status"
	"xiomi-router-driver/internal/subscription"
)

func latencyPolicyFixture(t *testing.T) (*Supervisor, config.State, time.Time) {
	t.Helper()
	state := priorityTestState(t, 443, 8443)
	state.PriorityPolicies[0].Schedule = nil
	state.PriorityPolicies[0].SelectionMode = "latency"
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	s := &Supervisor{dataDir: t.TempDir(), priority: newPriorityRuntime()}
	s.probeStandby = func(context.Context, config.Provider, string) providerProbeResult {
		return providerProbeResult{Healthy: true}
	}
	s.probeTunnel = func(context.Context, string, string) providerProbeResult { return providerProbeResult{Healthy: true} }
	s.priority.pings[standbyProbeKey(state.Providers[0], "Germany")] = priorityServerPing{Ms: 100, Checked: now}
	s.priority.pings[standbyProbeKey(state.Providers[0], "Netherlands")] = priorityServerPing{Ms: 40, Checked: now}
	return s, state, now
}

func TestLatencyPolicySelectsFastestHealthyFreshTarget(t *testing.T) {
	s, state, now := latencyPolicyFixture(t)
	decision, ok := s.evaluatePriorityPolicyLocked(t.Context(), state, status.Snapshot{}, state.PriorityPolicies[0], now)
	if !ok || decision.ActiveLocation != "Netherlands" {
		t.Fatalf("did not choose the faster server: %+v", decision)
	}
	s.probeStandby = func(_ context.Context, _ config.Provider, location string) providerProbeResult {
		return providerProbeResult{Healthy: location == "Germany"}
	}
	decision, _ = s.evaluatePriorityPolicyLocked(t.Context(), state, status.Snapshot{}, state.PriorityPolicies[0], now)
	if decision.ActiveLocation != "Germany" {
		t.Fatalf("low ping bypassed VPN health: %+v", decision)
	}
}

func TestLatencyPolicyIgnoresOldPingAndRetainsOrderedMode(t *testing.T) {
	s, state, now := latencyPolicyFixture(t)
	key := standbyProbeKey(state.Providers[0], "Netherlands")
	s.priority.pings[key] = priorityServerPing{Ms: 1, Checked: now.Add(-3 * time.Minute)}
	decision, _ := s.evaluatePriorityPolicyLocked(t.Context(), state, status.Snapshot{}, state.PriorityPolicies[0], now)
	if decision.ActiveLocation != "Germany" {
		t.Fatalf("stale ping was treated as fast: %+v", decision)
	}
	s.priority.pings[key] = priorityServerPing{Ms: 1, Checked: now}
	state.PriorityPolicies[0].SelectionMode = "priority"
	decision, _ = s.evaluatePriorityPolicyLocked(t.Context(), state, status.Snapshot{}, state.PriorityPolicies[0], now)
	if decision.ActiveLocation != "Germany" {
		t.Fatalf("ordered mode was changed by ping: %+v", decision)
	}
}

func TestLatencyPolicyRespectsManualSelectionAndSchedule(t *testing.T) {
	s, state, now := latencyPolicyFixture(t)
	policy := state.PriorityPolicies[0]
	s.priority.overrides[policy.ID] = priorityOverride{Location: "Germany", Since: now}
	decision, _ := s.evaluatePriorityPolicyLocked(t.Context(), state, status.Snapshot{}, policy, now)
	if decision.ActiveLocation != "Germany" {
		t.Fatalf("ping replaced manual selection: %+v", decision)
	}
	delete(s.priority.overrides, policy.ID)
	policy.Schedule = []config.PriorityScheduleWindow{{Start: "11:00", End: "13:00", Location: "Germany"}}
	decision, _ = s.evaluatePriorityPolicyLocked(t.Context(), state, status.Snapshot{}, policy, now)
	if decision.ActiveLocation != "Germany" {
		t.Fatalf("ping replaced schedule selection: %+v", decision)
	}
}

func TestLatencyPolicyUsesFastestFallbackForManualSelectionAndSchedule(t *testing.T) {
	for _, manual := range []bool{true, false} {
		t.Run(map[bool]string{true: "manual", false: "schedule"}[manual], func(t *testing.T) {
			s, state, now := latencyPolicyFixture(t)
			policy := state.PriorityPolicies[0]
			policy.Targets = append(policy.Targets, config.PriorityTarget{Location: "Sweden"})
			s.priority.pings[standbyProbeKey(state.Providers[0], "Germany")] = priorityServerPing{Ms: 10, Checked: now}
			s.priority.pings[standbyProbeKey(state.Providers[0], "Netherlands")] = priorityServerPing{Ms: 20, Checked: now}
			s.priority.pings[standbyProbeKey(state.Providers[0], "Sweden")] = priorityServerPing{Ms: 30, Checked: now}
			if manual {
				s.priority.overrides[policy.ID] = priorityOverride{Location: "Netherlands", Since: now}
			} else {
				policy.Schedule = []config.PriorityScheduleWindow{{Start: "11:00", End: "13:00", Location: "Netherlands"}}
			}
			s.probeStandby = func(_ context.Context, _ config.Provider, location string) providerProbeResult {
				return providerProbeResult{Healthy: location != "Netherlands"}
			}
			decision, _ := s.evaluatePriorityPolicyLocked(t.Context(), state, status.Snapshot{}, policy, now)
			if decision.ActiveLocation != "Germany" {
				t.Fatalf("did not use the fastest healthy fallback: %+v", decision)
			}
		})
	}
}

func TestLatencyPolicyAvoidsJitterAndWaitsForStableCandidate(t *testing.T) {
	s, state, now := latencyPolicyFixture(t)
	policy := state.PriorityPolicies[0]
	previous := priorityDecision{PolicyID: policy.ID, ActiveLocation: "Germany", PreferredLocation: "Germany", Mode: "provider", Since: now.Add(-time.Hour), Fingerprint: priorityPolicyFingerprint(policy, state)}
	s.priority.decisions[policy.ID] = previous
	snapshot := status.Snapshot{SubscriptionRuntime: []subscription.RuntimeSnapshot{{Key: "provider_1::germany", Location: "Germany", InterfaceName: "sb-active", Status: "running"}}}
	decision, _ := s.evaluatePriorityPolicyLocked(t.Context(), state, snapshot, policy, now)
	if decision.ActiveLocation != "Germany" {
		t.Fatalf("changed tunnel on first fast sample: %+v", decision)
	}
	s.priority.health[priorityHealthKey(policy.ID, "Netherlands")] = providerHealthState{HealthySince: now.Add(-2 * time.Minute), ConsecutiveSuccesses: 4}
	key := standbyProbeKey(state.Providers[0], "Netherlands")
	s.priority.pings[key] = priorityServerPing{Ms: 95, Checked: now}
	decision, _ = s.evaluatePriorityPolicyLocked(t.Context(), state, snapshot, policy, now)
	if decision.ActiveLocation != "Germany" {
		t.Fatalf("tiny ping difference caused a switch: %+v", decision)
	}
	if !samePriorityDecisionSet(map[string]priorityDecision{policy.ID: previous}, map[string]priorityDecision{policy.ID: decision}) {
		t.Fatal("ping noise would reapply unchanged routing")
	}
	s.priority.pings[key] = priorityServerPing{Ms: 40, Checked: now}
	decision, _ = s.evaluatePriorityPolicyLocked(t.Context(), state, snapshot, policy, now)
	if decision.ActiveLocation != "Netherlands" {
		t.Fatalf("stable substantial improvement was ignored: %+v", decision)
	}
}

func TestLatencyPolicyDoesNotImmediatelyBounceBackAfterSwitch(t *testing.T) {
	s, state, now := latencyPolicyFixture(t)
	policy := state.PriorityPolicies[0]
	s.priority.decisions[policy.ID] = priorityDecision{PolicyID: policy.ID, ActiveLocation: "Netherlands", PreferredLocation: "Netherlands", Mode: "provider", Since: now, Fingerprint: priorityPolicyFingerprint(policy, state)}
	s.priority.pings[standbyProbeKey(state.Providers[0], "Germany")] = priorityServerPing{Ms: 20, Checked: now}
	s.priority.health[priorityHealthKey(policy.ID, "Germany")] = providerHealthState{HealthySince: now.Add(-2 * time.Minute), ConsecutiveSuccesses: 4}
	snapshot := status.Snapshot{SubscriptionRuntime: []subscription.RuntimeSnapshot{{Key: "provider_1::netherlands", Location: "Netherlands", InterfaceName: "sb-active", Status: "running"}}}
	decision, _ := s.evaluatePriorityPolicyLocked(t.Context(), state, snapshot, policy, now)
	if decision.ActiveLocation != "Netherlands" {
		t.Fatalf("reversed a recent switch immediately: %+v", decision)
	}
}

func TestLatencyPolicyKeepsFailureGraceButDoesNotDelayAnOutageFallback(t *testing.T) {
	s, state, now := latencyPolicyFixture(t)
	policy := state.PriorityPolicies[0]
	s.priority.decisions[policy.ID] = priorityDecision{PolicyID: policy.ID, ActiveLocation: "Germany", Mode: "provider", Since: now.Add(-time.Hour), Fingerprint: priorityPolicyFingerprint(policy, state)}
	s.probeTunnel = func(context.Context, string, string) providerProbeResult { return providerProbeResult{} }
	snapshot := status.Snapshot{SubscriptionRuntime: []subscription.RuntimeSnapshot{{Key: "provider_1::germany", Location: "Germany", InterfaceName: "sb-active", Status: "running"}}}
	decision, _ := s.evaluatePriorityPolicyLocked(t.Context(), state, snapshot, policy, now)
	if decision.ActiveLocation != "Germany" {
		t.Fatalf("first failure bypassed grace: %+v", decision)
	}
	s.priority.health[priorityHealthKey(policy.ID, "Germany")] = providerHealthState{UnhealthySince: now.Add(-10 * time.Minute), ConsecutiveFailures: 20}
	decision, _ = s.evaluatePriorityPolicyLocked(t.Context(), state, snapshot, policy, now)
	if decision.ActiveLocation != "Netherlands" {
		t.Fatalf("latency warmup prolonged an outage: %+v", decision)
	}
}
