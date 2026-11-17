package automation

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"xiomi-router-driver/internal/config"
	"xiomi-router-driver/internal/sqlitedb"
	"xiomi-router-driver/internal/status"
	"xiomi-router-driver/internal/subscription"
)

func TestPriorityStatusRemainsResponsiveDuringApply(t *testing.T) {
	for _, applyFails := range []bool{false, true} {
		name := "success publishes only after completion"
		if applyFails {
			name = "failure preserves last successful decision"
		}
		t.Run(name, func(t *testing.T) {
			s, state, snapshot := priorityConcurrencySupervisor(t)
			before := s.PriorityStatus()
			if err := s.SetPriorityOverride("policy_1", "Netherlands"); err != nil {
				t.Fatal(err)
			}
			started := make(chan config.State, 1)
			release := make(chan struct{})
			s.applyState = func(_ context.Context, applied config.State) error {
				started <- applied
				<-release
				if applyFails {
					return errors.New("route update failed")
				}
				return nil
			}
			done := make(chan bool, 1)
			go func() { done <- s.maybePriorityPolicies(t.Context(), state, snapshot) }()
			var releaseOnce sync.Once
			defer func() {
				releaseOnce.Do(func() { close(release) })
				priorityAwait(t, done)
			}()
			applied := priorityAwait(t, started)
			if got := applied.Rules[0].SelectedLocation; got != "Netherlands" {
				t.Fatalf("apply target = %q, want Netherlands", got)
			}

			during := priorityStatusPromptly(t, s)
			if during.Policies[0].ActiveLocation != "Germany" || during.LastApplyAt != before.LastApplyAt {
				t.Fatalf("unfinished apply published attempted target: before=%+v during=%+v", before, during)
			}
			appliedRead := make(chan config.State, 1)
			go func() { appliedRead <- s.priorityAppliedState(state) }()
			if got := priorityAwait(t, appliedRead).Rules[0].SelectedLocation; got != "Germany" {
				t.Fatalf("unfinished apply used attempted target in routing state: %q", got)
			}

			releaseOnce.Do(func() { close(release) })
			changed := priorityAwait(t, done)
			done <- changed // Let the deferred cleanup also observe completion.
			after := priorityStatusPromptly(t, s)
			if applyFails {
				if changed || after.Policies[0].ActiveLocation != "Germany" || after.LastApplyAt != before.LastApplyAt {
					t.Fatalf("failed apply changed published state: changed=%v status=%+v", changed, after)
				}
			} else if !changed || after.Policies[0].ActiveLocation != "Netherlands" || after.LastApplyAt == before.LastApplyAt {
				t.Fatalf("successful apply was not published: changed=%v status=%+v", changed, after)
			}
		})
	}
}

func TestPriorityManualOverrideDuringApplyIsRetained(t *testing.T) {
	for _, clearOverride := range []bool{false, true} {
		name := "set"
		if clearOverride {
			name = "clear"
		}
		t.Run(name, func(t *testing.T) {
			s, state, snapshot := priorityConcurrencySupervisor(t)
			if err := s.SetPriorityOverride("policy_1", "Netherlands"); err != nil {
				t.Fatal(err)
			}
			started := make(chan struct{}, 1)
			release := make(chan struct{})
			s.applyState = func(context.Context, config.State) error {
				started <- struct{}{}
				<-release
				return nil
			}
			done := make(chan bool, 1)
			go func() { done <- s.maybePriorityPolicies(t.Context(), state, snapshot) }()
			var releaseOnce sync.Once
			defer func() {
				releaseOnce.Do(func() { close(release) })
				priorityAwait(t, done)
			}()
			priorityAwait(t, started)
			updated := make(chan error, 1)
			go func() {
				if clearOverride {
					s.ClearPriorityOverride("policy_1")
					updated <- nil
					return
				}
				updated <- s.SetPriorityOverride("policy_1", "Germany")
			}()
			if err := priorityAwait(t, updated); err != nil {
				t.Fatal(err)
			}
			releaseOnce.Do(func() { close(release) })
			changed := priorityAwait(t, done)
			done <- changed
			want := "Germany"
			if clearOverride {
				want = ""
			}
			if got := priorityStatusPromptly(t, s).Policies[0].OverrideLocation; got != want {
				t.Fatalf("override after pending apply = %q, want %q", got, want)
			}
		})
	}
}

func TestPriorityStatusAndNewOverrideRemainAvailableDuringProbe(t *testing.T) {
	s, state, snapshot := priorityConcurrencySupervisor(t)
	if err := s.SetPriorityOverride("policy_1", "Netherlands"); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	s.probeStandby = func(context.Context, config.Provider, string) providerProbeResult {
		started <- struct{}{}
		<-release
		return providerProbeResult{Healthy: false, Detail: "manual target unavailable"}
	}
	done := make(chan bool, 1)
	go func() { done <- s.maybePriorityPolicies(t.Context(), state, snapshot) }()
	var releaseOnce sync.Once
	defer func() {
		releaseOnce.Do(func() { close(release) })
		priorityAwait(t, done)
	}()
	priorityAwait(t, started)
	if got := priorityStatusPromptly(t, s).Policies[0].ActiveLocation; got != "Germany" {
		t.Fatalf("active location while probing = %q, want Germany", got)
	}
	updated := make(chan error, 1)
	go func() { updated <- s.SetPriorityOverride("policy_1", "Germany") }()
	if err := priorityAwait(t, updated); err != nil {
		t.Fatal(err)
	}
	releaseOnce.Do(func() { close(release) })
	changed := priorityAwait(t, done)
	done <- changed
	if got := priorityStatusPromptly(t, s).Policies[0].OverrideLocation; got != "Germany" {
		t.Fatalf("failed probe removed newer manual selection: override=%q", got)
	}
}

func TestPriorityEvaluationsSerializeAndUseLatestOverride(t *testing.T) {
	s, state, snapshot := priorityConcurrencySupervisor(t)
	if err := s.SetPriorityOverride("policy_1", "Netherlands"); err != nil {
		t.Fatal(err)
	}
	started := make(chan string, 2)
	release := make(chan struct{})
	s.applyState = func(_ context.Context, applied config.State) error {
		started <- applied.Rules[0].SelectedLocation
		<-release
		return nil
	}
	firstDone := make(chan bool, 1)
	secondDone := make(chan bool, 1)
	go func() { firstDone <- s.maybePriorityPolicies(t.Context(), state, snapshot) }()
	var releaseOnce sync.Once
	defer func() {
		releaseOnce.Do(func() { close(release) })
		priorityAwait(t, firstDone)
	}()
	if got := priorityAwait(t, started); got != "Netherlands" {
		t.Fatalf("first apply = %q, want Netherlands", got)
	}
	updated := make(chan error, 1)
	go func() { updated <- s.SetPriorityOverride("policy_1", "Germany") }()
	if err := priorityAwait(t, updated); err != nil {
		t.Fatal(err)
	}
	go func() { secondDone <- s.maybePriorityPolicies(t.Context(), state, snapshot) }()
	defer func() {
		releaseOnce.Do(func() { close(release) })
		priorityAwait(t, secondDone)
	}()
	select {
	case got := <-started:
		t.Fatalf("second apply started before first completed: %q", got)
	case <-time.After(100 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	if got := priorityAwait(t, started); got != "Germany" {
		t.Fatalf("queued evaluation ignored new selection: %q", got)
	}
	first := priorityAwait(t, firstDone)
	second := priorityAwait(t, secondDone)
	firstDone <- first
	secondDone <- second
	after := priorityStatusPromptly(t, s)
	if !first || !second || after.Policies[0].ActiveLocation != "Germany" || after.Policies[0].OverrideLocation != "Germany" {
		t.Fatalf("latest decision was overwritten: first=%v second=%v status=%+v", first, second, after)
	}
}

func priorityConcurrencySupervisor(t *testing.T) (*Supervisor, config.State, status.Snapshot) {
	t.Helper()
	state := priorityTestState(t, 1, 2)
	state.PriorityPolicies[0].Schedule = nil
	db, err := sqlitedb.Open(filepath.Join(t.TempDir(), "priority.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	manager := config.NewManager(db, "")
	state, err = manager.Save(state)
	if err != nil {
		t.Fatal(err)
	}
	s := &Supervisor{
		state: manager, priority: newPriorityRuntime(),
		applyState: func(context.Context, config.State) error { return nil },
		probeTunnel: func(context.Context, string, string) providerProbeResult {
			return providerProbeResult{Healthy: true}
		},
		probeStandby: func(context.Context, config.Provider, string) providerProbeResult {
			return providerProbeResult{Healthy: true}
		},
	}
	policy := state.PriorityPolicies[0]
	s.priority.lastApply = time.Now().Add(-time.Hour)
	s.priority.decisions[policy.ID] = priorityDecision{
		PolicyID: policy.ID, PolicyName: policy.Name, ProviderID: policy.ProviderID,
		ActiveLocation: "Germany", PreferredLocation: "Germany", Mode: "provider",
		Since: s.priority.lastApply, Fingerprint: priorityPolicyFingerprint(policy, state),
	}
	snapshot := status.Snapshot{
		WAN: status.WANStatus{State: "up"},
		SubscriptionRuntime: []subscription.RuntimeSnapshot{{
			Key: "provider_1::germany", ProviderID: "provider_1", Location: "Germany",
			Status: "running", InterfaceName: "test-tun",
		}},
	}
	return s, state, snapshot
}

func priorityStatusPromptly(t *testing.T, s *Supervisor) PriorityStatus {
	t.Helper()
	result := make(chan PriorityStatus, 1)
	go func() { result <- s.PriorityStatus() }()
	return priorityAwait(t, result)
}

func priorityAwait[T any](t *testing.T, result <-chan T) T {
	t.Helper()
	select {
	case value := <-result:
		return value
	case <-time.After(time.Second):
		t.Fatal("priority operation blocked while an apply or probe was in progress")
		var zero T
		return zero
	}
}

func TestManualPriorityApplyReturnsRouteFailure(t *testing.T) {
	s, _, snapshot := priorityConcurrencySupervisor(t)
	s.status = recoveryStatusFixture{snapshot: snapshot}
	want := errors.New("route table update failed")
	s.applyState = func(context.Context, config.State) error { return want }
	if err := s.SetPriorityOverride("policy_1", "Netherlands"); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyPriorityPolicies(t.Context()); !errors.Is(err, want) {
		t.Fatalf("apply error = %v, want %v", err, want)
	}
}

func TestManualSelectionOnlyCommitsConfirmedTarget(t *testing.T) {
	for _, outcome := range []string{"unreachable", "apply error", "runtime missing", "retained", "blocked", "running", "WAN down"} {
		t.Run(outcome, func(t *testing.T) {
			s, _, snapshot := priorityConcurrencySupervisor(t)
			if outcome == "WAN down" {
				snapshot.WAN.State = "down"
			}
			fixture := &recoveryStatusFixture{snapshot: snapshot}
			s.status = fixture
			if err := s.SetPriorityOverride("policy_1", "Germany"); err != nil {
				t.Fatal(err)
			}
			s.probeStandby = func(context.Context, config.Provider, string) providerProbeResult {
				return providerProbeResult{Healthy: outcome != "unreachable"}
			}
			calls := 0
			s.applyState = func(ctx context.Context, applied config.State) error {
				calls++
				if got := s.PriorityStatus().Policies[0].OverrideLocation; got != "Germany" {
					t.Fatalf("unconfirmed preference was exposed: %s", got)
				}
				if outcome == "apply error" {
					return errors.New("route apply failed")
				}
				rule := applied.Rules[0]
				appliedRule := subscription.RuleApplyResult{ProviderID: rule.ProviderID, Location: rule.SelectedLocation}
				switch outcome {
				case "retained":
					appliedRule.Location, appliedRule.Issue = "Germany", "replacement unavailable"
				case "blocked":
					appliedRule.Blocked, appliedRule.Issue, appliedRule.Location = true, "blocked", ""
				}
				_, report := subscription.WithApplyReport(ctx)
				report.Rules = map[string]subscription.RuleApplyResult{rule.ID: appliedRule}
				if outcome != "runtime missing" {
					fixture.snapshot.SubscriptionRuntime = []subscription.RuntimeSnapshot{{
						Key: "provider_1::netherlands", Location: "Netherlands", Status: "running",
					}}
				}
				return nil
			}
			active, err := s.ApplyPriorityOverride(t.Context(), "policy_1", "Netherlands")
			status := s.PriorityStatus().Policies[0]
			if outcome == "running" {
				if err != nil || active != "Netherlands" || status.ActiveLocation != active || status.OverrideLocation != active {
					t.Fatalf("active %q, err %v, status %+v", active, err, status)
				}
			} else {
				wantActive := "Germany"
				if outcome == "blocked" || outcome == "runtime missing" {
					wantActive = ""
				}
				if err == nil || status.ActiveLocation != wantActive || status.OverrideLocation != "Germany" || status.Blocked != (outcome == "blocked") {
					t.Fatalf("failed selection returned success or incorrect route: active %q err %v status %+v", active, err, status)
				}
			}
			if (outcome == "unreachable" || outcome == "WAN down") && calls != 0 {
				t.Fatalf("invalid request applied routes %d times", calls)
			}
		})
	}
}

func TestConcurrentManualSelectionsReturnTheirOwnAppliedServer(t *testing.T) {
	s, _, snapshot := priorityConcurrencySupervisor(t)
	fixture := &recoveryStatusFixture{snapshot: snapshot}
	s.status = fixture
	firstStarted := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	s.applyState = func(ctx context.Context, state config.State) error {
		rule := state.Rules[0]
		if rule.SelectedLocation == "Netherlands" {
			close(firstStarted)
			<-release
		}
		_, report := subscription.WithApplyReport(ctx)
		report.Rules = map[string]subscription.RuleApplyResult{
			rule.ID: {ProviderID: rule.ProviderID, Location: rule.SelectedLocation},
		}
		fixture.snapshot.SubscriptionRuntime = []subscription.RuntimeSnapshot{{
			Key: rule.ProviderID + "::" + strings.ToLower(rule.SelectedLocation), ProviderID: rule.ProviderID,
			Location: rule.SelectedLocation, Status: "running", InterfaceName: "test-tun",
		}}
		return nil
	}
	type reply struct {
		location string
		err      error
	}
	switchTo := func(location string) <-chan reply {
		done := make(chan reply, 1)
		go func() {
			active, err := s.ApplyPriorityOverride(t.Context(), "policy_1", location)
			done <- reply{active, err}
		}()
		return done
	}
	first := switchTo("Netherlands")
	priorityAwait(t, firstStarted)
	second := switchTo("Germany")
	select {
	case result := <-second:
		t.Fatalf("second switch overtook first: %+v", result)
	case <-time.After(50 * time.Millisecond):
	}
	unblock()
	for location, done := range map[string]<-chan reply{"Netherlands": first, "Germany": second} {
		if got := priorityAwait(t, done); got.location != location || got.err != nil {
			t.Fatalf("switch to %s returned %+v", location, got)
		}
	}
	if active, err := s.ApplyPriorityOverride(t.Context(), "policy_1", ""); err != nil || active != "Germany" || s.PriorityStatus().Policies[0].OverrideLocation != "" {
		t.Fatalf("automatic selection after concurrent switches: active %s, err %v", active, err)
	}
}

type priorityTickSnapshot struct {
	recoveryStatusFixture
	read chan struct{}
}

func (f priorityTickSnapshot) RuntimeSnapshot(ctx context.Context) (status.Snapshot, error) {
	close(f.read)
	return f.recoveryStatusFixture.RuntimeSnapshot(ctx)
}

func TestTickReadsRuntimeAfterInProgressSelection(t *testing.T) {
	s, _, snapshot := priorityConcurrencySupervisor(t)
	read := make(chan struct{})
	s.status = priorityTickSnapshot{recoveryStatusFixture{snapshot: snapshot}, read}
	s.priorityEvalMu.Lock()
	done := make(chan struct{})
	go func() { defer close(done); s.tick(t.Context()) }()
	stale := false
	select {
	case <-read:
		stale = true
	case <-time.After(50 * time.Millisecond):
	}
	s.priorityEvalMu.Unlock()
	priorityAwait(t, done)
	if stale {
		t.Fatal("tick read stale runtime during a pending selection")
	}
}

func TestManualSelectionRespectsActiveProviderFallbacks(t *testing.T) {
	for _, targetHasFallback := range []bool{false, true} {
		s, state, snapshot := priorityConcurrencySupervisor(t)
		second := state.Providers[0]
		second.ID, second.Name = "other-vpn", "Other"
		state.Providers = append(state.Providers, second)
		state.Rules = append(state.Rules, config.Rule{ID: "other-rule", Name: "Other route", ProviderID: second.ID, SelectedLocation: "Germany", Domains: []string{"other.example"}, Enabled: true})
		if _, err := s.state.Save(state); err != nil {
			t.Fatal(err)
		}
		id, location := "policy_1", "Netherlands"
		s.failover = newFailoverRuntime()
		s.failover.overrides["other-rule"] = failoverOverride{OriginalProviderID: second.ID, OriginalLocation: "Germany", Mode: "provider", ActiveProviderID: "provider_1", ActiveLocation: "Netherlands"}
		if targetHasFallback {
			s.failover.overrides[priorityRuleID(id)] = failoverOverride{OriginalProviderID: "provider_1", OriginalLocation: "Germany", Mode: "provider", ActiveProviderID: second.ID, ActiveLocation: "Germany"}
		}
		fixture := &recoveryStatusFixture{snapshot: snapshot}
		s.status = fixture
		calls := 0
		s.applyState = func(ctx context.Context, applied config.State) error {
			calls++
			_, report := subscription.WithApplyReport(ctx)
			report.Rules = make(map[string]subscription.RuleApplyResult)
			for _, rule := range applied.Rules {
				if rule.ID == "other-rule" && (rule.ProviderID != "provider_1" || rule.SelectedLocation != location) {
					t.Errorf("unrelated fallback displaced: %+v", rule)
				}
				report.Rules[rule.ID] = subscription.RuleApplyResult{ProviderID: rule.ProviderID, Location: rule.SelectedLocation}
			}
			fixture.snapshot.SubscriptionRuntime = []subscription.RuntimeSnapshot{{Key: "provider_1::netherlands", Status: "running"}}
			return nil
		}
		got, err := s.ApplyPriorityOverride(t.Context(), id, location)
		if targetHasFallback {
			if err == nil || calls != 0 {
				t.Fatalf("overwrote an active provider fallback: active %q, err %v, calls %d", got, err, calls)
			}
		} else if err != nil || got != location || calls != 1 {
			t.Fatalf("unrelated fallback prevented requested switch: active %q, err %v, calls %d", got, err, calls)
		}
	}
}
