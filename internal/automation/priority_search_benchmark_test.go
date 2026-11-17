package automation

import (
	"context"
	"fmt"
	"testing"
	"time"

	"xiomi-router-driver/internal/config"
	"xiomi-router-driver/internal/status"
	"xiomi-router-driver/internal/subscription"
)

// Run with -run '^$' -bench '^BenchmarkColdReserveSearch$' -benchtime=1x.
// Cold cache, past the failure threshold, no live VPN or routing changes.
// This measures selection, not the user's end-to-end interruption.
func BenchmarkColdReserveSearch(b *testing.B) {
	for _, tc := range []struct {
		position         int
		immediateFailure bool
	}{{2, true}, {2, false}, {4, false}, {10, false}} {
		b.Run(fmt.Sprintf("first_healthy_%d/immediate_failure_%t", tc.position, tc.immediateFailure), func(b *testing.B) {
			provider := config.Provider{ID: "p", Enabled: true, Type: config.ProviderTypeSubscription}
			policy := config.PriorityPolicy{ID: "rule", ProviderID: provider.ID, Enabled: true, SelectionMode: "priority"}
			for i := 1; i <= 10; i++ {
				policy.Targets = append(policy.Targets, config.PriorityTarget{Location: fmt.Sprintf("n%d", i)})
			}
			state := config.State{Providers: []config.Provider{provider}, Automation: config.AutomationSettings{FailoverFailureSeconds: 5, FailoverRestoreSeconds: 60}}
			snapshot := status.Snapshot{SubscriptionRuntime: []subscription.RuntimeSnapshot{{Key: "p::n1", Status: "running", InterfaceName: "bench-tun"}}}
			want := fmt.Sprintf("n%d", tc.position)
			for i := 0; i < b.N; i++ {
				now := time.Now()
				s := &Supervisor{priority: newPriorityRuntime()}
				s.priority.decisions[policy.ID] = priorityDecision{ActiveLocation: "n1", Mode: "provider", Since: now.Add(-time.Hour)}
				s.priority.health[priorityHealthKey(policy.ID, "n1")] = providerHealthState{UnhealthySince: now.Add(-time.Minute), ConsecutiveFailures: 3}
				s.probeTunnel = func(ctx context.Context, _, _ string) providerProbeResult {
					if !tc.immediateFailure {
						<-ctx.Done()
					}
					return providerProbeResult{}
				}
				s.probeStandby = func(ctx context.Context, _ config.Provider, name string) providerProbeResult {
					if name == want {
						select {
						case <-time.After(100 * time.Millisecond):
							return providerProbeResult{Healthy: true}
						case <-ctx.Done():
							return providerProbeResult{}
						}
					}
					<-ctx.Done()
					return providerProbeResult{}
				}
				ctx, cancel := context.WithCancel(b.Context())
				result, ok := s.evaluatePriorityPolicyLocked(ctx, state, snapshot, policy, now)
				cancel()
				if !ok || result.ActiveLocation != want {
					b.Fatalf("selected %q, expected %q", result.ActiveLocation, want)
				}
			}
		})
	}
}
