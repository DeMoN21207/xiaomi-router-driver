package automation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"xiomi-router-driver/internal/config"
	"xiomi-router-driver/internal/subscription"
)

func TestOutageWaitsForFreshHTTPSOnAppliedRule(t *testing.T) {
	s, state, snapshot := priorityConcurrencySupervisor(t)
	policy := state.PriorityPolicies[0]
	decision := s.priority.decisions[policy.ID]
	decision.outcome = subscription.RuleApplyResult{ProviderID: policy.ProviderID, Location: "Germany"}
	s.priority.decisions[policy.ID] = decision
	fixture := &recoveryStatusFixture{snapshot: snapshot}
	s.status = fixture
	s.probeTunnel = func(_ context.Context, iface, _ string) providerProbeResult {
		return providerProbeResult{Healthy: false, Detail: "HTTPS failed on " + iface}
	}
	if s.maybePriorityPolicies(t.Context(), state, snapshot) {
		t.Fatal("first failure switched routes")
	}
	statusJSON := func(key string) map[string]any {
		data, err := json.Marshal(s.PriorityStatus().Policies[0])
		if err != nil {
			t.Fatal(err)
		}
		var policy map[string]json.RawMessage
		if err := json.Unmarshal(data, &policy); err != nil {
			t.Fatal(err)
		}
		if policy[key] == nil {
			return nil
		}
		var item map[string]any
		if err := json.Unmarshal(policy[key], &item); err != nil {
			t.Fatal(err)
		}
		return item
	}
	first := statusJSON("currentOutage")
	if first["fromLocation"] != "Germany" || first["kind"] != "observing" || first["firstFailedAt"] == nil {
		t.Fatalf("first active HTTPS failure should start monitoring, got %+v", first)
	}
	s.applyState = func(ctx context.Context, applied config.State) error {
		rule := applied.Rules[0]
		_, report := subscription.WithApplyReport(ctx)
		report.Rules = map[string]subscription.RuleApplyResult{
			rule.ID: {ProviderID: rule.ProviderID, Location: rule.SelectedLocation},
		}
		fixture.snapshot.SubscriptionRuntime = []subscription.RuntimeSnapshot{{
			Key: rule.ProviderID + "::" + strings.ToLower(rule.SelectedLocation), Status: "running",
			InterfaceName: "replacement-tun", FWMark: "0x67", Location: rule.SelectedLocation,
		}}
		return nil
	}
	s.cacheProviderProbe("tunnel:replacement-tun:0x67", providerProbeResult{Healthy: true})
	if _, err := s.ApplyPriorityOverride(t.Context(), policy.ID, "Netherlands"); err != nil {
		t.Fatal(err)
	}
	applying := statusJSON("currentOutage")
	if applying["kind"] != "manual" || applying["toLocation"] != "Netherlands" ||
		applying["selectedAt"] == nil || applying["appliedAt"] == nil || applying["recoveredAt"] != nil ||
		statusJSON("lastOutage") != nil {
		t.Fatalf("standby, cached or failed active check incorrectly marked recovery: %+v", applying)
	}
	var event string
	s.recordEvent = func(_, kind, message string) {
		if kind == "automation.priority_outage_recovered" {
			event = message
		}
	}
	s.probeTunnel = func(_ context.Context, iface, mark string) providerProbeResult {
		return providerProbeResult{Healthy: iface == "replacement-tun" && mark == "0x67"}
	}
	s.maybePriorityPolicies(t.Context(), state, fixture.snapshot)
	last := statusJSON("lastOutage")
	if statusJSON("currentOutage") != nil || last["id"] != first["id"] || last["recoveredAt"] == nil || last["durationMs"] == nil || !strings.Contains(event, "Netherlands") {
		t.Fatalf("verified replacement HTTPS did not complete the original interval: %+v; event %q", last, event)
	}
	failedAt, err := time.Parse(time.RFC3339Nano, last["firstFailedAt"].(string))
	if err != nil {
		t.Fatal(err)
	}
	recoveredAt, err := time.Parse(time.RFC3339Nano, last["recoveredAt"].(string))
	if err != nil || float64(recoveredAt.Sub(failedAt).Milliseconds()) != last["durationMs"] {
		t.Fatalf("wrong measured duration: %+v", last)
	}
}

func TestOutageRuntimeLossCanRecoverOnSameAppliedTunnel(t *testing.T) {
	for _, runtimeState := range []string{"stopped", "missing"} {
		t.Run(runtimeState, func(t *testing.T) {
			s, state, snapshot := priorityConcurrencySupervisor(t)
			policy := state.PriorityPolicies[0]
			decision := s.priority.decisions[policy.ID]
			decision.outcome = subscription.RuleApplyResult{ProviderID: policy.ProviderID, Location: "Germany"}
			s.priority.decisions[policy.ID] = decision
			fixture := &recoveryStatusFixture{snapshot: snapshot}
			s.status = fixture
			if runtimeState == "missing" {
				snapshot.SubscriptionRuntime = nil
			} else {
				snapshot.SubscriptionRuntime = append([]subscription.RuntimeSnapshot(nil), snapshot.SubscriptionRuntime...)
				snapshot.SubscriptionRuntime[0].Status = "stopped"
			}
			s.applyState = func(ctx context.Context, applied config.State) error {
				rule := applied.Rules[0]
				_, report := subscription.WithApplyReport(ctx)
				report.Rules = map[string]subscription.RuleApplyResult{rule.ID: {ProviderID: rule.ProviderID, Location: rule.SelectedLocation}}
				return nil
			}
			s.maybePriorityPolicies(t.Context(), state, snapshot)
			got := s.PriorityStatus().Policies[0]
			if got.CurrentOutage != nil || got.LastOutage == nil || got.LastOutage.StartEvidence != "runtime-unavailable" || got.LastOutage.FromLocation != "Germany" || got.LastOutage.ToLocation != "Germany" || got.LastOutage.Kind != "transient" {
				t.Fatalf("confirmed runtime loss and repair should be measured on its existing VPN: %+v", got)
			}
		})
	}
}

func TestOutageAutomaticRecoveryAndCheckpoint(t *testing.T) {
	s, state, snapshot := priorityConcurrencySupervisor(t)
	s.dataDir = t.TempDir()
	policy := state.PriorityPolicies[0]
	decision := s.priority.decisions[policy.ID]
	decision.outcome = subscription.RuleApplyResult{ProviderID: policy.ProviderID, Location: "Germany"}
	s.priority.decisions[policy.ID] = decision
	fixture := &recoveryStatusFixture{snapshot: snapshot}
	s.status = fixture
	s.probeTunnel = func(_ context.Context, iface, mark string) providerProbeResult {
		return providerProbeResult{Healthy: iface == "second" && mark == "0x30"}
	}
	s.maybePriorityPolicies(t.Context(), state, snapshot)
	first := s.PriorityStatus().Policies[0].CurrentOutage
	if first == nil {
		t.Fatal("missing first observed failure")
	}
	// Advance the health streak beyond its configured threshold without sleeping.
	s.priority.health[priorityHealthKey(policy.ID, "Germany")] = providerHealthState{
		UnhealthySince: time.Now().Add(-time.Hour), ConsecutiveFailures: 10,
	}
	s.applyState = func(ctx context.Context, applied config.State) error {
		rule := applied.Rules[0]
		_, report := subscription.WithApplyReport(ctx)
		report.Rules = map[string]subscription.RuleApplyResult{rule.ID: {ProviderID: rule.ProviderID, Location: rule.SelectedLocation}}
		fixture.snapshot.SubscriptionRuntime = []subscription.RuntimeSnapshot{{
			Key: rule.ProviderID + "::" + strings.ToLower(rule.SelectedLocation), Status: "running", InterfaceName: "second", FWMark: "0x30",
		}}
		return nil
	}
	s.maybePriorityPolicies(t.Context(), state, snapshot)
	got := s.PriorityStatus().Policies[0]
	if got.CurrentOutage != nil || got.LastOutage == nil {
		t.Fatalf("missing completed failover: %+v", got)
	}
	last := got.LastOutage
	if last.ID != first.ID || last.Kind != "automatic" || last.ToLocation != "Netherlands" || last.DetectionMs == nil || last.SelectionMs == nil || last.ApplyMs == nil || last.VerificationMs == nil || last.RecoveredAt.Before(last.AppliedAt) {
		t.Fatalf("failover timing %+v", last)
	}

	refreshed := NewSupervisor(s.state, nil, nil, nil, nil, s.dataDir)
	fresh := refreshed.PriorityStatus().Policies[0]
	if fresh.CurrentOutage != nil || fresh.LastOutage == nil || fresh.LastOutage.ID != last.ID {
		t.Fatalf("checkpoint failed: %+v", fresh)
	}
	changed := state
	changed.PriorityPolicies[0].Targets = changed.PriorityPolicies[0].Targets[:1]
	if _, err := s.state.Save(changed); err != nil {
		t.Fatal(err)
	}
	if refreshed.PriorityStatus().Policies[0].LastOutage != nil {
		t.Fatal("changed policy scope reused unrelated history")
	}
}

func TestOutageIgnoresStandbyCachedCancelledAndStartupEvidence(t *testing.T) {
	s, state, _ := priorityConcurrencySupervisor(t)
	policy := state.PriorityPolicies[0]
	decision := s.priority.decisions[policy.ID]
	decision.outcome = subscription.RuleApplyResult{ProviderID: policy.ProviderID, Location: decision.ActiveLocation}
	s.probeTunnel = func(context.Context, string, string) providerProbeResult { return providerProbeResult{} }
	sample := s.freshTunnelProbe(t.Context(), "test-tun", "")
	s.cacheProviderProbe("probe", sample)
	cached, ok := s.cachedProviderProbe("probe", time.Minute)
	if !ok {
		t.Fatal("missing test cache entry")
	}
	s.observePriorityActive(t.Context(), policy, decision, cached, true)
	s.observePriorityActive(t.Context(), policy, decision, providerProbeResult{}, true)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	s.observePriorityActive(cancelled, policy, decision, sample, true)
	starting := decision
	starting.outcome = subscription.RuleApplyResult{}
	s.observePriorityActive(t.Context(), policy, starting, sample, true)
	s.failover.overrides = map[string]failoverOverride{priorityRuleID(policy.ID): {Mode: "direct"}}
	s.observePriorityActive(t.Context(), policy, decision, sample, true)
	if got := s.PriorityStatus().Policies[0].CurrentOutage; got != nil {
		t.Fatalf("invalid evidence started outage %+v", got)
	}
}

func TestOutageConfirmedRuleBlockedDuringManualSwitch(t *testing.T) {
	s, state, snapshot := priorityConcurrencySupervisor(t)
	policy := state.PriorityPolicies[0]
	decision := s.priority.decisions[policy.ID]
	decision.outcome = subscription.RuleApplyResult{ProviderID: policy.ProviderID, Location: "Germany"}
	s.priority.decisions[policy.ID] = decision
	s.status = recoveryStatusFixture{snapshot: snapshot}
	s.applyState = func(ctx context.Context, applied config.State) error {
		rule := applied.Rules[0]
		_, report := subscription.WithApplyReport(ctx)
		report.Rules = map[string]subscription.RuleApplyResult{rule.ID: {ProviderID: rule.ProviderID, Blocked: true, Issue: "replacement unavailable"}}
		return nil
	}
	if _, err := s.ApplyPriorityOverride(t.Context(), policy.ID, "Netherlands"); err == nil {
		t.Fatal("blocked manual switch accepted")
	}
	got := s.PriorityStatus().Policies[0]
	if got.LastOutage != nil || got.CurrentOutage == nil || got.CurrentOutage.StartEvidence != "route-blocked" || got.CurrentOutage.FromLocation != "Germany" {
		t.Fatalf("confirmed route blockage not observed: %+v", got)
	}
}
