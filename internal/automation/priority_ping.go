package automation

import (
	"context"
	"sort"
	"time"

	"xiomi-router-driver/internal/config"
	"xiomi-router-driver/internal/probe"
	"xiomi-router-driver/internal/subscription"
)

const priorityPingInterval = time.Minute
const priorityPingMaxAge = 2 * time.Minute

type priorityServerPing struct {
	Ms      int64
	Error   string
	Checked time.Time
	samples []int64
}

func (p priorityServerPing) fresh(now time.Time) bool {
	age := now.Sub(p.Checked)
	return !p.Checked.IsZero() && age >= 0 && age <= priorityPingMaxAge && p.Error == "" && p.Ms > 0
}

func priorityTargetsByPing(policy config.PriorityPolicy, provider config.Provider, pings map[string]priorityServerPing, now time.Time) []config.PriorityTarget {
	targets := append([]config.PriorityTarget(nil), policy.Targets...)
	sort.SliceStable(targets, func(left, right int) bool {
		a := pings[standbyProbeKey(provider, targets[left].Location)]
		b := pings[standbyProbeKey(provider, targets[right].Location)]
		if a.fresh(now) != b.fresh(now) {
			return a.fresh(now)
		}
		return a.fresh(now) && a.Ms < b.Ms
	})
	return targets
}

func significantPingImprovement(current, candidate priorityServerPing, now time.Time) bool {
	if !candidate.fresh(now) {
		return false
	}
	if !current.fresh(now) {
		return true
	}
	return current.Ms-candidate.Ms >= 10 && candidate.Ms*100 <= current.Ms*80
}

// Refresh only the configured candidates, using direct ICMP to their endpoints.
// Tunnel HTTPS health remains a separate signal; ICMP failure never marks VPN down.
// The caller serializes refreshes with priorityPingMu. Network work holds no status lock.
func (s *Supervisor) refreshPriorityServerPings(ctx context.Context, state config.State, now time.Time) {
	providers := providersIndex(state.Providers)
	pending := make(map[string]probe.Location)
	valid := make(map[string]bool)
	loaded := make(map[string][]subscription.Entry)
	for _, policy := range state.PriorityPolicies {
		provider, ok := providers[policy.ProviderID]
		if !ok || !provider.Enabled || provider.Type != config.ProviderTypeSubscription || !policy.Enabled {
			continue
		}
		for _, target := range policy.Targets {
			key := standbyProbeKey(provider, target.Location)
			valid[key] = true
			s.priorityMu.RLock()
			cached := s.priority.pings[key]
			s.priorityMu.RUnlock()
			age := now.Sub(cached.Checked)
			if !cached.Checked.IsZero() && age >= 0 && age < priorityPingInterval {
				continue
			}
			if _, exists := pending[key]; exists {
				continue
			}
			entries, exists := loaded[provider.ID]
			if !exists {
				entries, _ = subscription.LoadCachedEntries(provider.Source, s.subscriptionRuntimeDir())
				loaded[provider.ID] = entries
			}
			entry, found := findSubscriptionEntry(entries, target.Location)
			location := probe.Location{Name: key, EndpointError: "endpoint unavailable"}
			if found {
				location.Address = entry.Address
				location.EndpointError = subscription.EntryEndpointIssue(entry)
			}
			pending[key] = location
		}
	}
	if len(pending) == 0 || ctx.Err() != nil {
		return
	}
	locations := make([]probe.Location, 0, len(pending))
	for _, location := range pending {
		locations = append(locations, location)
	}
	s.priorityMu.RLock()
	measure := s.priority.measurePings
	s.priorityMu.RUnlock()
	if measure == nil {
		return
	}
	pingCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	results := measure(pingCtx, locations)
	cancel()
	if ctx.Err() != nil {
		return
	}
	byKey := make(map[string]probe.Location, len(results))
	for _, location := range results {
		byKey[location.Name] = location
	}
	s.priorityMu.Lock()
	defer s.priorityMu.Unlock()
	s.priority.ensure()
	for key := range s.priority.pings {
		if !valid[key] {
			delete(s.priority.pings, key)
		}
	}
	for key := range pending {
		location, found := byKey[key]
		previous := s.priority.pings[key]
		next := priorityServerPing{Checked: now}
		if !found || location.LatencyError != "" || location.EndpointError != "" || location.LatencyMs <= 0 {
			next.Error = "ping unavailable"
		} else {
			if previous.fresh(now) {
				next.samples = append(next.samples, previous.samples...)
			}
			next.samples = append(next.samples, location.LatencyMs)
			if len(next.samples) > 3 {
				next.samples = next.samples[len(next.samples)-3:]
			}
			for _, sample := range next.samples {
				next.Ms += sample
			}
			next.Ms /= int64(len(next.samples))
		}
		s.priority.pings[key] = next
	}
}

func (s *Supervisor) updatePriorityServerPings(ctx context.Context, state config.State, now time.Time) {
	if !s.priorityPingMu.TryLock() {
		return
	}
	refresh := func() { defer s.priorityPingMu.Unlock(); s.refreshPriorityServerPings(ctx, state, now) }
	if rapidPriorityChecks(state) {
		go refresh()
	} else {
		refresh()
	}
}
