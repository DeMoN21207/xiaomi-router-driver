package automation

import (
	"context"
	"errors"
	"testing"
	"time"

	"xiomi-router-driver/internal/config"
	"xiomi-router-driver/internal/status"
	"xiomi-router-driver/internal/subscription"
)

func overlappingPriorityRules(t *testing.T) (*Supervisor, config.State, status.Snapshot) {
	t.Helper()
	s, state, snapshot := priorityConcurrencySupervisor(t)
	s.dataDir = t.TempDir()
	first := state.PriorityPolicies[0]
	first.RuleID = state.Rules[0].ID
	second := first
	second.ID, second.RuleID = "policy_2", "rule_2"
	state.PriorityPolicies = []config.PriorityPolicy{first, second}
	state.Rules = append(state.Rules, config.Rule{
		ID: second.RuleID, Name: "Second route", ProviderID: first.ProviderID,
		SelectedLocation: "Netherlands", Domains: []string{"second.example"}, Enabled: true,
	})
	state.Automation.InstallService = true
	state, err := s.state.Save(state)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.SubscriptionRuntime = append(snapshot.SubscriptionRuntime, subscription.RuntimeSnapshot{
		Key: "provider_1::netherlands", ProviderID: first.ProviderID, Location: "Netherlands",
		Status: "running", InterfaceName: "test-tun-2",
	})
	s.status = recoveryStatusFixture{snapshot: snapshot}
	return s, state, snapshot
}

func verifyPriorityRestart(t *testing.T, s *Supervisor, snapshot status.Snapshot, first, second string) {
	t.Helper()
	var restored config.State
	restarted := NewSupervisor(s.state, recoveryStatusFixture{snapshot: snapshot}, nil, func(_ context.Context, state config.State) error {
		restored = state
		return nil
	}, nil, s.dataDir)
	restarted.startup(t.Context())
	locations := make(map[string]string)
	for _, rule := range restored.Rules {
		locations[rule.ID] = rule.SelectedLocation
	}
	if locations["__priority_policy_policy_1"] != first || locations["__priority_policy_policy_2"] != second {
		t.Fatalf("restored routes %v, want policy_1=%s and policy_2=%s", locations, first, second)
	}
}

func applyOverlappingPrioritySelections(t *testing.T, s *Supervisor) {
	t.Helper()
	for policyID, target := range map[string]string{"policy_1": "Netherlands", "policy_2": "Germany"} {
		if err := s.SetPriorityOverride(policyID, target); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ApplyPriorityPolicies(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestPriorityRestartRestoresLastAppliedTargetPerRule(t *testing.T) {
	s, _, snapshot := overlappingPriorityRules(t)
	applyOverlappingPrioritySelections(t, s)
	verifyPriorityRestart(t, s, snapshot, "Netherlands", "Germany")
	// Following a router reboot there may be no running tunnels yet.
	verifyPriorityRestart(t, s, status.Snapshot{}, "Netherlands", "Germany")
}

func TestPriorityRestartAfterFailedApplyKeepsPriorTargets(t *testing.T) {
	s, _, snapshot := overlappingPriorityRules(t)
	applyOverlappingPrioritySelections(t, s)
	if err := s.SetPriorityOverride("policy_1", "Germany"); err != nil {
		t.Fatal(err)
	}
	attempted := false
	s.applyState = func(context.Context, config.State) error {
		attempted = true
		return errors.New("apply failed")
	}
	_ = s.ApplyPriorityPolicies(t.Context())
	if !attempted {
		t.Fatal("expected changed selection to attempt a route apply")
	}
	verifyPriorityRestart(t, s, snapshot, "Netherlands", "Germany")
}

func TestPriorityRestartWithoutCheckpointUsesRunningRuleLocation(t *testing.T) {
	s, _, snapshot := overlappingPriorityRules(t)
	verifyPriorityRestart(t, s, snapshot, "Germany", "Netherlands")
}

func TestPriorityRestartAfterPartialApplyPersistsOnlyWorkingRules(t *testing.T) {
	for _, blocked := range []bool{true, false} {
		s, state, snapshot := overlappingPriorityRules(t)
		applyOverlappingPrioritySelections(t, s)
		applied := config.State{Rules: []config.Rule{
			{ID: "__priority_policy_policy_1", ProviderID: "provider_1", SelectedLocation: "Germany", Enabled: true},
			{ID: "__priority_policy_policy_2", ProviderID: "provider_1", SelectedLocation: "Netherlands", Enabled: true},
		}}
		retained := "Netherlands"
		if blocked {
			retained = ""
		}
		s.savePriorityTargets(state, applied, &subscription.ApplyReport{Rules: map[string]subscription.RuleApplyResult{
			"__priority_policy_policy_1": {ProviderID: "provider_1", Location: retained, Issue: "requested location missing", Blocked: blocked},
			"__priority_policy_policy_2": {ProviderID: "provider_1", Location: "Netherlands"},
		}})
		verifyPriorityRestart(t, s, snapshot, "Netherlands", "Netherlands")
	}
}

func TestStartupPriorityStatusShowsAppliedRoutesDuringDegradation(t *testing.T) {
	s, _, snapshot := overlappingPriorityRules(t)
	s.status = recoveryStatusFixture{snapshot: snapshot}
	s.applyState = func(ctx context.Context, applied config.State) error {
		_, report := subscription.WithApplyReport(ctx)
		report.Rules = map[string]subscription.RuleApplyResult{
			"__priority_policy_policy_1": {ProviderID: "provider_1", Location: "Netherlands", Issue: "keeping prior route"},
			"__priority_policy_policy_2": {ProviderID: "provider_1", Blocked: true, Issue: "target unavailable"},
		}
		return nil
	}
	s.startup(t.Context())
	policies := s.PriorityStatus().Policies
	if policies[0].ActiveLocation != "Netherlands" || !policies[0].Targets[1].Active || policies[0].LocationIssue == "" {
		t.Fatalf("retained route reported as %+v", policies[0])
	}
	if !policies[1].Blocked || policies[1].ActiveLocation != "" || policies[1].Targets[1].Active || policies[1].LocationIssue == "" {
		t.Fatalf("blocked route reported as %+v", policies[1])
	}
	state, err := s.state.Load()
	if err != nil {
		t.Fatal(err)
	}
	if rules := s.priorityAppliedState(state).Rules; len(rules) != 2 || rules[1].SelectedLocation != "Netherlands" {
		t.Fatalf("blocked rule lost its VPN routing intent: %+v", rules)
	}
}

func TestRetainedTargetIsRetriedAfterADegradedApply(t *testing.T) {
	s, state, snapshot := overlappingPriorityRules(t)
	snapshot.SubscriptionRuntime[0].LocationIssue = "configured target unavailable"
	decisions := defaultPriorityDecisions(state, time.Now())
	if s.priorityRuntimeMatchesDecisions(snapshot, decisions) {
		t.Fatal("a retained process must not satisfy a fresh priority apply")
	}
	if !s.subscriptionTargetsNeedRecovery(expectedSubscriptionKeys(s.priorityAppliedState(state)), snapshot.SubscriptionRuntime) {
		t.Fatal("automatic recovery skipped a degraded subscription")
	}
}

func TestBlockedRuleImmediatelyProbesAndSelectsWorkingReserve(t *testing.T) {
	s, state, snapshot := overlappingPriorityRules(t)
	policy := state.PriorityPolicies[0]
	previous := s.priority.decisions[policy.ID]
	previous.outcome = subscription.RuleApplyResult{ProviderID: policy.ProviderID, Blocked: true, Issue: "blocked"}
	s.priority.decisions[policy.ID] = previous
	decision, ok := s.evaluatePriorityPolicyLocked(t.Context(), state, snapshot, policy, time.Now())
	if !ok || decision.ActiveLocation != "Netherlands" {
		t.Fatalf("last apply blocked this rule despite a running process, got %+v", decision)
	}
}

func TestDegradedRouteRetryWindowIsPerTarget(t *testing.T) {
	s, state, snapshot := overlappingPriorityRules(t)
	applied := config.State{Rules: state.Rules}
	report := &subscription.ApplyReport{Rules: map[string]subscription.RuleApplyResult{
		"rule_1": {ProviderID: "provider_1", Blocked: true, Issue: "missing"},
		"rule_2": {ProviderID: "provider_1", Location: "Netherlands"},
	}}
	s.noteDegradedTargets(applied, report)
	expected := expectedSubscriptionKeys(state)
	snapshot.SubscriptionRuntime[0].Status = "error"
	if s.subscriptionTargetsNeedRecovery(expected, snapshot.SubscriptionRuntime) {
		t.Fatal("the blocked route triggered another apply before its retry window")
	}
	if !s.subscriptionTargetsNeedRecovery(expected, nil) {
		t.Fatal("a broken second target must still trigger immediate recovery")
	}
	s.degradedRetryAt["provider_1::germany"] = time.Now().Add(-time.Second)
	if !s.subscriptionTargetsNeedRecovery(expected, snapshot.SubscriptionRuntime) {
		t.Fatal("a blocked route was not retried after its retry window")
	}
	report.Rules["rule_1"] = subscription.RuleApplyResult{ProviderID: "provider_1", Location: "Germany"}
	s.noteDegradedTargets(applied, report)
	snapshot.SubscriptionRuntime[0].Status = "running"
	if s.subscriptionTargetsNeedRecovery(expected, snapshot.SubscriptionRuntime) {
		t.Fatal("recovered targets must stop triggering retries")
	}
}

func TestOrdinarySubscriptionRecoveryUsesAutomaticApplyAndBackoff(t *testing.T) {
	s, state, snapshot := overlappingPriorityRules(t)
	s.priority = newPriorityRuntime()
	state.PriorityPolicies = nil
	state.Automation.AutoRecover = true
	state.Automation.ProviderFailover = false
	if _, err := s.state.Save(state); err != nil {
		t.Fatal(err)
	}
	snapshot.SubscriptionRuntime[0].Status = "error"
	s.status = recoveryStatusFixture{snapshot: snapshot}
	s.lastWAN = "up"
	s.apply = func(context.Context) error {
		t.Fatal("automatic recovery invoked the manual apply path")
		return nil
	}
	applies := 0
	s.applyState = func(ctx context.Context, state config.State) error {
		applies++
		_, report := subscription.WithApplyReport(ctx)
		report.Rules = map[string]subscription.RuleApplyResult{"rule_1": {ProviderID: "provider_1", Blocked: true, Issue: "missing"}}
		return nil
	}
	s.tick(t.Context())
	s.tick(t.Context())
	if applies != 1 {
		t.Fatalf("two ticks applied the same missing location %d times", applies)
	}
}

func TestPriorityRestartDropsSavedTargetWhenScopeChanges(t *testing.T) {
	for _, change := range []string{"target removed", "rule reassigned", "provider changed"} {
		t.Run(change, func(t *testing.T) {
			s, state, _ := overlappingPriorityRules(t)
			applyOverlappingPrioritySelections(t, s)
			switch change {
			case "target removed":
				state.PriorityPolicies[0].Targets = []config.PriorityTarget{{Location: "Germany"}}
			case "rule reassigned":
				state.Rules[0].ID = "replacement"
				state.PriorityPolicies[0].RuleID = "replacement"
			case "provider changed":
				provider := state.Providers[0]
				provider.ID = "replacement"
				state.Providers = append(state.Providers, provider)
				state.Rules[0].ProviderID = provider.ID
				state.PriorityPolicies[0].ProviderID = provider.ID
			}
			if _, err := s.state.Save(state); err != nil {
				t.Fatal(err)
			}
			verifyPriorityRestart(t, s, status.Snapshot{}, "Germany", "Germany")
		})
	}
}
