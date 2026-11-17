package automation

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"xiomi-router-driver/internal/config"
	"xiomi-router-driver/internal/status"
	"xiomi-router-driver/internal/subscription"
)

func rapidSearchFixture(t *testing.T, active int, count int) (*Supervisor, config.State, status.Snapshot, time.Time) {
	t.Helper()
	s, state, now := latencyPolicyFixture(t)
	state.Automation.FailoverFailureSeconds = 5
	policy := &state.PriorityPolicies[0]
	policy.SelectionMode = "priority"
	policy.Targets = nil
	for i := 1; i <= count; i++ {
		policy.Targets = append(policy.Targets, config.PriorityTarget{Location: fmt.Sprintf("n%d", i)})
	}
	name := fmt.Sprintf("n%d", active)
	s.priority.decisions[policy.ID] = priorityDecision{ActiveLocation: name, Mode: "provider", Since: now.Add(-time.Hour)}
	s.priority.health[priorityHealthKey(policy.ID, name)] = providerHealthState{UnhealthySince: now.Add(-time.Minute), ConsecutiveFailures: 10}
	snap := status.Snapshot{SubscriptionRuntime: []subscription.RuntimeSnapshot{{Key: "provider_1::" + name, Status: "running", InterfaceName: "sb-active"}}}
	return s, state, snap, now
}

func TestRapidSearchReturnsWhileColdReservesStillWarming(t *testing.T) {
	s, state, snap, now := rapidSearchFixture(t, 1, 8)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started, stopped := make(chan string, 8), make(chan string, 8)
	s.probeStandby = func(ctx context.Context, _ config.Provider, n string) providerProbeResult {
		started <- n
		<-ctx.Done()
		stopped <- n
		return providerProbeResult{}
	}
	done := make(chan priorityDecision, 1)
	go func() {
		d, _ := s.evaluatePriorityPolicyLocked(ctx, state, snap, state.PriorityPolicies[0], now)
		done <- d
	}()
	one, two := priorityAwait(t, started), priorityAwait(t, started)
	if !(one == "n2" && two == "n3" || one == "n3" && two == "n2") {
		t.Fatalf("warmed %s %s", one, two)
	}
	if d := priorityAwait(t, done); d.ActiveLocation != "n1" {
		t.Fatalf("changed healthy active route: %+v", d)
	}
	cancel()
	priorityAwait(t, stopped)
	priorityAwait(t, stopped)
}

func TestRapidSearchSlidesAcrossFailuresAndWaitsForHigherPriority(t *testing.T) {
	s, state, snap, now := rapidSearchFixture(t, 6, 7)
	policy := state.PriorityPolicies[0]
	policy.SelectionMode = "latency"
	s.probeTunnel = func(context.Context, string, string) providerProbeResult { return providerProbeResult{} }
	started := make(chan string, 10)
	waitForHigher := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s.probeStandby = func(ctx context.Context, _ config.Provider, n string) providerProbeResult {
		started <- n
		switch n {
		case "n1":
			select {
			case <-ctx.Done():
			case <-waitForHigher:
			}
			return providerProbeResult{Healthy: true}
		case "n4":
			return providerProbeResult{Healthy: true}
		}
		return providerProbeResult{}
	}
	done := make(chan priorityDecision, 1)
	go func() { d, _ := s.evaluatePriorityPolicyLocked(ctx, state, snap, policy, now); done <- d }()
	hits := map[string]bool{}
	for !hits["n4"] {
		hits[priorityAwait(t, started)] = true
	}
	select {
	case d := <-done:
		t.Fatalf("chose lower candidate before n1 resolved: %+v", d)
	default:
	}
	close(waitForHigher)
	if d := priorityAwait(t, done); d.ActiveLocation != "n1" {
		t.Fatalf("priority/grace ignored: %+v", d)
	}
}

func TestRapidSearchBoundsAndSharesStandbysAcrossPolicies(t *testing.T) {
	s, state, snap, now := rapidSearchFixture(t, 1, 6)
	first := state.PriorityPolicies[0]
	second := first
	second.ID = "second"
	second.Targets = []config.PriorityTarget{{Location: "n1"}, {Location: "n3"}, {Location: "n4"}}
	s.priority.decisions[second.ID] = s.priority.decisions[first.ID]
	var inflight, calls atomic.Int32
	started := make(chan struct{}, 10)
	gate := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s.probeStandby = func(ctx context.Context, _ config.Provider, n string) providerProbeResult {
		count := inflight.Add(1)
		defer inflight.Add(-1)
		if count > 2 {
			t.Errorf("%d simultaneous standby probes", count)
		}
		calls.Add(1)
		started <- struct{}{}
		select {
		case <-gate:
		case <-ctx.Done():
		}
		return providerProbeResult{Healthy: true}
	}
	done := make(chan priorityDecision, 2)
	run := func(p config.PriorityPolicy) {
		go func() { d, _ := s.evaluatePriorityPolicyLocked(ctx, state, snap, p, now); done <- d }()
	}
	run(first)
	priorityAwait(t, started)
	priorityAwait(t, started)
	run(second)
	for i := 0; i < 2; i++ {
		if d := priorityAwait(t, done); d.ActiveLocation != "n1" {
			t.Fatalf("changed route: %+v", d)
		}
	}
	s.standbyPool.mu.Lock()
	var completions []<-chan struct{}
	for _, flight := range s.standbyPool.running {
		completions = append(completions, flight.done)
	}
	s.standbyPool.mu.Unlock()
	close(gate)
	for _, done := range completions {
		priorityAwait(t, done)
	}
	for i := 0; i < 2; i++ {
		run(first)
		priorityAwait(t, done)
	}
	if calls.Load() != 2 {
		t.Fatalf("physical probes=%d, want two across overlapping checks and cache hits", calls.Load())
	}
}

func TestRapidSearchCanceledActiveProbeDoesNotPoisonStandbyCache(t *testing.T) {
	s, state, snap, now := rapidSearchFixture(t, 1, 2)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s.probeTunnel = func(ctx context.Context, _, _ string) providerProbeResult {
		cancel()
		<-ctx.Done()
		return providerProbeResult{Detail: "parent canceled"}
	}
	if _, ok := s.evaluatePriorityPolicyLocked(ctx, state, snap, state.PriorityPolicies[0], now); ok {
		t.Fatal("canceled evaluation returned a routing decision")
	}
	if _, ok := s.cachedProviderProbe(standbyProbeKey(state.Providers[0], "n1"), time.Minute); ok {
		t.Fatal("caller cancellation was recorded as a VPN failure")
	}
}

func TestRapidSearchRetriesStandbyAfterOwnerCancellation(t *testing.T) {
	s, state, snap, now := rapidSearchFixture(t, 1, 2)
	owner, cancel := context.WithCancel(t.Context())
	defer cancel()
	policy := state.PriorityPolicies[0]
	started := make(chan struct{})
	var calls atomic.Int32
	s.probeStandby = func(ctx context.Context, _ config.Provider, _ string) providerProbeResult {
		if calls.Add(1) == 1 {
			close(started)
			<-ctx.Done()
			return providerProbeResult{}
		}
		return providerProbeResult{Healthy: true}
	}
	s.evaluatePriorityPolicyLocked(owner, state, snap, policy, now)
	priorityAwait(t, started)
	activeCheck := make(chan struct{})
	s.probeTunnel = func(context.Context, string, string) providerProbeResult {
		close(activeCheck)
		return providerProbeResult{}
	}
	// Force a new policy to consult the in-flight standby while its original
	// owner still has a healthy route.
	policy.ID = "second"
	s.priority.decisions[policy.ID] = priorityDecision{ActiveLocation: "n1"}
	s.priority.health[priorityHealthKey(policy.ID, "n1")] = providerHealthState{UnhealthySince: now.Add(-time.Minute), ConsecutiveFailures: 8}
	done := make(chan priorityDecision, 1)
	go func() { d, _ := s.evaluatePriorityPolicyLocked(t.Context(), state, snap, policy, now); done <- d }()
	priorityAwait(t, activeCheck)
	cancel()
	if d := priorityAwait(t, done); d.ActiveLocation != "n2" || calls.Load() != 2 {
		t.Fatalf("shared canceled check did not retry: decision=%s calls=%d", d.ActiveLocation, calls.Load())
	}
}

func TestRapidSearchReturnsWinningBackupBeforeLowerTimeout(t *testing.T) {
	s, state, snap, now := rapidSearchFixture(t, 1, 4)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s.probeTunnel = func(context.Context, string, string) providerProbeResult { return providerProbeResult{} }
	lowerStarted := make(chan struct{})
	lowerStopped := make(chan struct{})
	s.probeStandby = func(ctx context.Context, _ config.Provider, location string) providerProbeResult {
		switch location {
		case "n2":
			select {
			case <-lowerStarted:
				return providerProbeResult{Healthy: true}
			case <-ctx.Done():
			}
		case "n3":
			close(lowerStarted)
			<-ctx.Done()
			close(lowerStopped)
		default:
			t.Errorf("unnecessary probe below a known good fallback: %s", location)
		}
		return providerProbeResult{}
	}
	done := make(chan priorityDecision, 1)
	go func() {
		d, _ := s.evaluatePriorityPolicyLocked(ctx, state, snap, state.PriorityPolicies[0], now)
		done <- d
	}()
	if d := priorityAwait(t, done); d.ActiveLocation != "n2" {
		t.Fatalf("expected first reserve: %+v", d)
	}
	cancel()
	priorityAwait(t, lowerStopped)
}
