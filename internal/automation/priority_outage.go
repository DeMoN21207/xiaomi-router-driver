package automation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"xiomi-router-driver/internal/config"
	"xiomi-router-driver/internal/fileutil"
	"xiomi-router-driver/internal/subscription"
)

const outageHistoryFile = "priority-outages.json"

// PriorityOutage is the interval between observed loss and a working HTTPS
// response on the rule's committed route. Stage timestamps describe only
// transitions actually reached.
type PriorityOutage struct {
	ID             string    `json:"id"`
	Kind           string    `json:"kind"`
	StartEvidence  string    `json:"startEvidence"`
	FromLocation   string    `json:"fromLocation"`
	ToLocation     string    `json:"toLocation,omitempty"`
	FirstFailedAt  time.Time `json:"firstFailedAt"`
	DetectedAt     time.Time `json:"detectedAt,omitzero"`
	SelectedAt     time.Time `json:"selectedAt,omitzero"`
	AppliedAt      time.Time `json:"appliedAt,omitzero"`
	RecoveredAt    time.Time `json:"recoveredAt,omitzero"`
	DurationMs     int64     `json:"durationMs"`
	DetectionMs    *int64    `json:"detectionMs,omitempty"`
	SelectionMs    *int64    `json:"selectionMs,omitempty"`
	ApplyMs        *int64    `json:"applyMs,omitempty"`
	VerificationMs *int64    `json:"verificationMs,omitempty"`
}

type priorityOutageSeries struct {
	Scope   string          `json:"scope"`
	Current *PriorityOutage `json:"-"`
	Last    *PriorityOutage `json:"last"`
}

func outageScope(fingerprint string) string {
	sum := sha256.Sum256([]byte(fingerprint))
	return hex.EncodeToString(sum[:])
}

func (o *PriorityOutage) snapshot(now time.Time) *PriorityOutage {
	if o == nil {
		return nil
	}
	out := *o
	if !out.RecoveredAt.IsZero() {
		now = out.RecoveredAt
	}
	out.DurationMs = max(0, now.Sub(out.FirstFailedAt).Milliseconds())
	out.DetectionMs = stageMilliseconds(out.FirstFailedAt, out.DetectedAt)
	selectionStart := out.DetectedAt
	if selectionStart.IsZero() {
		selectionStart = out.FirstFailedAt
	}
	out.SelectionMs = stageMilliseconds(selectionStart, out.SelectedAt)
	out.ApplyMs = stageMilliseconds(out.SelectedAt, out.AppliedAt)
	out.VerificationMs = stageMilliseconds(out.AppliedAt, out.RecoveredAt)
	return &out
}

func stageMilliseconds(start, end time.Time) *int64 {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return nil
	}
	ms := end.Sub(start).Milliseconds()
	return &ms
}

func (s *Supervisor) observePriorityActive(ctx context.Context, policy config.PriorityPolicy, decision priorityDecision, probe providerProbeResult, confirmedFailure bool) {
	actual := decision.outcome
	if ctx.Err() != nil || actual.Blocked || actual.Issue != "" || actual.ProviderID != policy.ProviderID ||
		actual.Location == "" || !strings.EqualFold(actual.Location, decision.ActiveLocation) {
		return
	}
	if s.priority.reconcileRequired {
		return // The last apply failed and the route still needs confirmation.
	}
	s.failoverMu.RLock()
	_, providerFallback := s.failover.overrides[priorityRuleID(policy.ID)]
	s.failoverMu.RUnlock()
	if providerFallback {
		return
	}

	started, finished := probe.tunnelStarted, probe.tunnelFinished
	evidence := "https-failed"
	if probe.runtimeUnavailable && !probe.Healthy {
		cycle, _ := ctx.Value(priorityProbeCycleKey{}).(*priorityProbeCycle)
		if cycle == nil {
			return
		}
		started, finished = cycle.snapshotAt, cycle.snapshotAt
		evidence = "runtime-unavailable"
	}
	if finished.IsZero() || started.IsZero() {
		return // Cached, standby and ping results have no active-path sample.
	}
	s.priorityMu.Lock()
	series := s.priority.outages[policy.ID]
	if !probe.Healthy {
		if series == nil || series.Last == nil || started.After(series.Last.RecoveredAt) {
			o := s.startOutageLocked(decision, evidence, finished)
			if confirmedFailure && o.DetectedAt.IsZero() && o.SelectedAt.IsZero() {
				o.DetectedAt = time.Now().UTC()
			}
		}
		s.priorityMu.Unlock()
		return
	}
	if series == nil || series.Current == nil {
		s.priorityMu.Unlock()
		return
	}
	current := series.Current
	if started.Before(current.FirstFailedAt) || started.Before(current.AppliedAt) {
		s.priorityMu.Unlock()
		return
	}
	if !strings.EqualFold(current.ToLocation, actual.Location) || current.AppliedAt.IsZero() {
		if !strings.EqualFold(current.FromLocation, actual.Location) {
			s.priorityMu.Unlock()
			return // Different location without a per-rule confirmation.
		}
		current.Kind = "transient"
		current.SelectedAt, current.AppliedAt = time.Time{}, time.Time{}
	}
	current.ToLocation, current.RecoveredAt = actual.Location, finished
	series.Last, series.Current = current.snapshot(finished), nil
	last := *series.Last
	s.priorityMu.Unlock()
	s.record("info", "automation.priority_outage_recovered", fmt.Sprintf("%s (%s): %s → %s, %d ms (%s, %s to %s, id %s)",
		policy.Name, policy.ID, last.FromLocation, last.ToLocation, last.DurationMs, last.Kind,
		last.FirstFailedAt.Format(time.RFC3339Nano), last.RecoveredAt.Format(time.RFC3339Nano), last.ID))
	s.saveOutageHistory()
}

func (s *Supervisor) startOutageLocked(decision priorityDecision, evidence string, at time.Time) *PriorityOutage {
	id := decision.PolicyID
	series := s.priority.outages[id]
	if series == nil {
		if s.priority.outages == nil {
			s.priority.outages = make(map[string]*priorityOutageSeries)
		}
		series = &priorityOutageSeries{Scope: outageScope(decision.Fingerprint)}
		s.priority.outages[id] = series
	}
	if series.Current == nil {
		series.Current = &PriorityOutage{
			ID: fmt.Sprintf("%s-%d", id, at.UnixNano()), Kind: "observing",
			FromLocation: decision.outcome.Location, FirstFailedAt: at, StartEvidence: evidence,
		}
	}
	return series.Current
}

// A committed fail-closed route immediately removes a previously serving
// path, even if the HTTPS check just before the transaction succeeded.
func (s *Supervisor) observeBlockedRoutes(state config.State, report *subscription.ApplyReport, appliedAt time.Time) {
	if report == nil || len(report.Rules) == 0 {
		return
	}
	s.failoverMu.RLock()
	defer s.failoverMu.RUnlock()
	s.priorityMu.Lock()
	defer s.priorityMu.Unlock()
	for _, policy := range state.PriorityPolicies {
		id := priorityRuleID(policy.ID)
		outcome := report.Rules[id]
		if _, fallback := s.failover.overrides[id]; fallback || !policy.Enabled || !outcome.Blocked || outcome.ProviderID != policy.ProviderID {
			continue
		}
		prior := s.priority.decisions[policy.ID]
		if prior.outcome.ProviderID == policy.ProviderID && prior.outcome.Location != "" && !prior.outcome.Blocked && prior.Fingerprint == priorityPolicyFingerprint(policy, state) {
			s.startOutageLocked(prior, "route-blocked", appliedAt.UTC())
		}
	}
}

// Called under priorityEvalMu. Status readers can see a selected candidate
// while the external route transaction is still in progress.
func (s *Supervisor) noteOutageSelections(next map[string]priorityDecision, manual manualPrioritySelection) {
	s.priorityMu.Lock()
	defer s.priorityMu.Unlock()
	for id, decision := range next {
		series := s.priority.outages[id]
		if series == nil || series.Current == nil {
			continue
		}
		o := series.Current
		if strings.EqualFold(decision.ActiveLocation, s.priority.decisions[id].servingLocation()) || strings.EqualFold(o.ToLocation, decision.ActiveLocation) {
			continue
		}
		o.SelectedAt, o.AppliedAt, o.ToLocation = decision.selectedAt, time.Time{}, decision.ActiveLocation
		o.Kind = "automatic"
		if id == manual.policyID || strings.EqualFold(s.priority.overrides[id].Location, decision.ActiveLocation) {
			o.Kind = "manual"
		}
	}
}

type outageTarget struct {
	policy   config.PriorityPolicy
	decision priorityDecision
}

// A standby probe can choose a candidate, but only a committed rule and
// a new HTTPS probe on its live interface can finish the interruption.
func (s *Supervisor) appliedOutageTargetsLocked(state config.State, next map[string]priorityDecision, appliedAt time.Time) []outageTarget {
	var verify []outageTarget
	for _, policy := range state.PriorityPolicies {
		series := s.priority.outages[policy.ID]
		if series == nil || series.Current == nil {
			continue
		}
		o := series.Current
		decision := next[policy.ID]
		actual := decision.outcome
		if actual.Blocked || actual.Issue != "" || actual.ProviderID != policy.ProviderID ||
			actual.Location == "" || !strings.EqualFold(decision.ActiveLocation, actual.Location) {
			continue
		}
		if !o.SelectedAt.IsZero() && strings.EqualFold(o.ToLocation, actual.Location) && o.AppliedAt.IsZero() {
			o.AppliedAt = appliedAt.UTC()
		}
		verify = append(verify, outageTarget{policy, decision})
	}
	return verify
}

func (s *Supervisor) verifyAppliedOutages(ctx context.Context, state config.State, targets []outageTarget) {
	if len(targets) == 0 || s.status == nil || ctx.Err() != nil {
		return
	}
	snapshot, err := s.status.RuntimeSnapshot(ctx)
	if err != nil || snapshot.WAN.State != "up" {
		return
	}
	type tunnel struct{ iface, mark string }
	groups := make(map[tunnel][]outageTarget)
	for _, target := range targets {
		key := target.policy.ProviderID + "::" + strings.ToLower(strings.TrimSpace(target.decision.ActiveLocation))
		for _, runtime := range snapshot.SubscriptionRuntime {
			if runtime.Key == key && runtime.Status == "running" && runtime.LocationIssue == "" && strings.TrimSpace(runtime.InterfaceName) != "" {
				path := tunnel{runtime.InterfaceName, runtime.FWMark}
				groups[path] = append(groups[path], target)
				break
			}
		}
	}
	type observation struct {
		path  tunnel
		probe providerProbeResult
	}
	results := make(chan observation, len(groups))
	for path := range groups {
		go func() {
			probeCtx := ctx
			if rapidPriorityChecks(state) {
				var cancel context.CancelFunc
				probeCtx, cancel = context.WithTimeout(ctx, 1500*time.Millisecond)
				defer cancel()
			}
			results <- observation{path, s.freshTunnelProbe(probeCtx, path.iface, path.mark)}
		}()
	}
	for range groups {
		result := <-results
		for _, target := range groups[result.path] {
			s.observePriorityActive(ctx, target.policy, target.decision, result.probe, false)
		}
	}
}

func (s *Supervisor) loadOutageHistory() {
	if s.dataDir == "" {
		return
	}
	data, err := os.ReadFile(filepath.Join(s.dataDir, outageHistoryFile))
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	var history map[string]*priorityOutageSeries
	if err == nil {
		err = json.Unmarshal(data, &history)
	}
	if err != nil {
		s.record("error", "automation.priority_outage_history_failed", fmt.Sprintf("load VPN interruption history: %v", err))
		return
	}
	for id, series := range history {
		if series == nil || series.Last == nil || series.Last.RecoveredAt.IsZero() || series.Last.FirstFailedAt.IsZero() {
			delete(history, id)
		}
	}
	s.priority.outages = history
}

func (s *Supervisor) saveOutageHistory() {
	if s.dataDir == "" {
		return
	}
	s.priorityMu.RLock()
	history := make(map[string]priorityOutageSeries, len(s.priority.outages))
	for id, series := range s.priority.outages {
		if series.Last != nil {
			history[id] = priorityOutageSeries{Scope: series.Scope, Last: series.Last}
		}
	}
	s.priorityMu.RUnlock()
	data, err := json.Marshal(history)
	if err == nil {
		err = fileutil.WriteFileAtomic(filepath.Join(s.dataDir, outageHistoryFile), data, 0o600)
	}
	if err != nil {
		s.record("error", "automation.priority_outage_history_failed", fmt.Sprintf("save VPN interruption history: %v", err))
	}
}
