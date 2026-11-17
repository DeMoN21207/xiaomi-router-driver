package automation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"xiomi-router-driver/internal/config"
	"xiomi-router-driver/internal/domains"
	"xiomi-router-driver/internal/probe"
	"xiomi-router-driver/internal/status"
	"xiomi-router-driver/internal/subscription"
)

const priorityApplyTimeout = 2 * time.Minute

type priorityOverride struct {
	Location string
	Since    time.Time
}

type priorityDecision struct {
	PolicyID          string
	PolicyName        string
	ProviderID        string
	ProviderName      string
	ActiveLocation    string
	PreferredLocation string
	Mode              string
	Reason            string
	Since             time.Time
	Fingerprint       string
	selectedAt        time.Time
	outcome           subscription.RuleApplyResult
}

type priorityRuntime struct {
	outages           map[string]*priorityOutageSeries
	decisions         map[string]priorityDecision
	overrides         map[string]priorityOverride
	health            map[string]providerHealthState
	pings             map[string]priorityServerPing
	measurePings      func(context.Context, []probe.Location) []probe.Location
	lastApply         time.Time
	reconcileRequired bool // guarded by priorityEvalMu after a failed or unconfirmed apply
}

func newPriorityRuntime() priorityRuntime {
	return priorityRuntime{
		decisions: make(map[string]priorityDecision),
		overrides: make(map[string]priorityOverride),
		health:    make(map[string]providerHealthState),
		pings:     make(map[string]priorityServerPing),
	}
}

func (p *priorityRuntime) ensure() {
	if p.decisions == nil {
		p.decisions = make(map[string]priorityDecision)
	}
	if p.overrides == nil {
		p.overrides = make(map[string]priorityOverride)
	}
	if p.health == nil {
		p.health = make(map[string]providerHealthState)
	}
	if p.pings == nil {
		p.pings = make(map[string]priorityServerPing)
	}
}

type PriorityStatus struct {
	LastApplyAt string                 `json:"lastApplyAt"`
	Policies    []PriorityPolicyStatus `json:"policies"`
}

type PriorityPolicyStatus struct {
	CurrentOutage     *PriorityOutage        `json:"currentOutage,omitempty"`
	LastOutage        *PriorityOutage        `json:"lastOutage,omitempty"`
	PolicyID          string                 `json:"policyId"`
	PolicyName        string                 `json:"policyName"`
	ProviderID        string                 `json:"providerId"`
	ProviderName      string                 `json:"providerName"`
	Enabled           bool                   `json:"enabled"`
	SelectionMode     string                 `json:"selectionMode"`
	ScheduledLocation string                 `json:"scheduledLocation,omitempty"`
	ActiveLocation    string                 `json:"activeLocation,omitempty"`
	LocationIssue     string                 `json:"locationIssue,omitempty"`
	Blocked           bool                   `json:"blocked,omitempty"`
	PreferredLocation string                 `json:"preferredLocation,omitempty"`
	OverrideLocation  string                 `json:"overrideLocation,omitempty"`
	Mode              string                 `json:"mode,omitempty"`
	Reason            string                 `json:"reason,omitempty"`
	Since             string                 `json:"since,omitempty"`
	Targets           []PriorityTargetStatus `json:"targets"`
}

type PriorityTargetStatus struct {
	Location             string `json:"location"`
	Status               string `json:"status"`
	Score                int    `json:"score"`
	ConsecutiveFailures  int    `json:"consecutiveFailures"`
	ConsecutiveSuccesses int    `json:"consecutiveSuccesses"`
	UnhealthySince       string `json:"unhealthySince,omitempty"`
	HealthySince         string `json:"healthySince,omitempty"`
	LastChecked          string `json:"lastChecked,omitempty"`
	LastError            string `json:"lastError,omitempty"`
	Detail               string `json:"detail,omitempty"`
	LatencyMs            int64  `json:"latencyMs,omitempty"`
	PingMs               int64  `json:"pingMs,omitempty"`
	PingError            string `json:"pingError,omitempty"`
	PingCheckedAt        string `json:"pingCheckedAt,omitempty"`
	Active               bool   `json:"active"`
	Preferred            bool   `json:"preferred"`
}

func (s *Supervisor) SetPriorityOverride(policyID string, location string) error {
	policyID = strings.TrimSpace(policyID)
	location = strings.TrimSpace(location)
	if policyID == "" {
		return fmt.Errorf("policy id is required")
	}
	if location == "" {
		return fmt.Errorf("location is required")
	}

	s.priorityMu.Lock()
	defer s.priorityMu.Unlock()
	s.priority.ensure()
	s.priority.overrides[policyID] = priorityOverride{Location: location, Since: time.Now()}
	return nil
}

func (s *Supervisor) ClearPriorityOverride(policyID string) {
	policyID = strings.TrimSpace(policyID)
	if policyID == "" {
		return
	}

	s.priorityMu.Lock()
	defer s.priorityMu.Unlock()
	s.priority.ensure()
	delete(s.priority.overrides, policyID)
}

var (
	ErrPriorityUnavailable       = errors.New("priority routing is unavailable")
	ErrPriorityTargetUnavailable = errors.New("selected server did not pass the VPN check")
	ErrPriorityUnconfirmed       = errors.New("router did not confirm the selected route")
	ErrPriorityProviderFallback  = errors.New("provider failover is active on this route")
)

type manualPrioritySelection struct {
	policyID string
	location string // Empty restores automatic selection.
}

func (s *Supervisor) ApplyPriorityPolicies(ctx context.Context) error {
	_, err := s.applyPriorityRequest(ctx, manualPrioritySelection{})
	return err
}

func (s *Supervisor) ApplyPriorityOverride(ctx context.Context, policyID, location string) (string, error) {
	if strings.TrimSpace(policyID) == "" {
		return "", ErrPriorityUnavailable
	}
	return s.applyPriorityRequest(ctx, manualPrioritySelection{policyID, strings.TrimSpace(location)})
}

func (s *Supervisor) applyPriorityRequest(ctx context.Context, selection manualPrioritySelection) (string, error) {
	s.priorityEvalMu.Lock()
	defer s.priorityEvalMu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if s.state == nil || s.status == nil {
		return "", ErrPriorityUnavailable
	}
	state, err := s.state.Load()
	if err != nil {
		return "", err
	}
	snapshot, err := s.status.RuntimeSnapshot(ctx)
	if err != nil {
		return "", err
	}
	_, err = s.maybePriorityPoliciesLocked(ctx, state, snapshot, selection)
	if err != nil || selection.policyID == "" {
		return "", err
	}
	s.priorityMu.RLock()
	defer s.priorityMu.RUnlock()
	return s.priority.decisions[selection.policyID].ActiveLocation, nil
}

func (s *Supervisor) maybePriorityPolicies(ctx context.Context, state config.State, snapshot status.Snapshot) bool {
	s.priorityEvalMu.Lock()
	defer s.priorityEvalMu.Unlock()
	changed, _ := s.maybePriorityPoliciesLocked(ctx, state, snapshot, manualPrioritySelection{})
	return changed
}

func (s *Supervisor) maybePriorityPoliciesLocked(ctx context.Context, state config.State, snapshot status.Snapshot, selection manualPrioritySelection) (bool, error) {
	if s.applyState == nil || snapshot.WAN.State != "up" {
		return false, ErrPriorityUnavailable
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	state = sharedDomainRoutingState(state)
	now := time.Now()
	s.priorityMu.Lock()
	s.priority.ensure()
	prunedHistory := s.prunePriorityRuntimeLocked(state, now)
	s.priorityMu.Unlock()
	if prunedHistory {
		s.saveOutageHistory()
	}

	s.updatePriorityServerPings(ctx, state, now)
	if err := ctx.Err(); err != nil {
		return false, err
	}
	next := make(map[string]priorityDecision)
	ctx = context.WithValue(ctx, priorityProbeCycleKey{}, &priorityProbeCycle{active: make(map[string]*priorityCycleProbe), snapshotAt: now.UTC()})
	for _, policy := range state.PriorityPolicies {
		if policy.ID == selection.policyID {
			decision, err := s.evaluateManualSelection(ctx, state, snapshot, policy, now, selection.location)
			if err != nil {
				return false, err
			}
			next[policy.ID] = decision
			continue
		}
		decision, ok := s.evaluatePriorityPolicyLocked(ctx, state, snapshot, policy, now)
		if ok {
			next[decision.PolicyID] = decision
		}
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	manual := selection.policyID != ""
	if _, ok := next[selection.policyID]; manual && !ok {
		return false, ErrPriorityUnavailable
	}
	s.priorityMu.RLock()
	needsApply := manual || s.priority.reconcileRequired || !samePriorityDecisionSet(s.priority.decisions, next)
	s.priorityMu.RUnlock()
	if !needsApply && s.priorityRuntimeMatchesDecisions(snapshot, next) {
		s.priorityMu.Lock()
		s.carryPriorityOutcomesLocked(next)
		s.priority.decisions = next
		s.priorityMu.Unlock()
		return false, nil
	}

	appliedState := buildPriorityAppliedStateFromDecisions(state, next)
	routingState := appliedState
	if manual {
		s.failoverMu.RLock()
		_, protected := s.failover.overrides[priorityRuleID(selection.policyID)]
		routingState = applyFailoverOverrides(appliedState, s.failover.overrides)
		s.failoverMu.RUnlock()
		if protected {
			return false, ErrPriorityProviderFallback
		}
	}
	applyCtx, cancel := context.WithTimeout(ctx, priorityApplyTimeout)
	applyCtx, report := subscription.WithApplyReport(applyCtx)
	s.noteOutageSelections(next, selection)
	s.priority.reconcileRequired = true
	err := s.applyState(applyCtx, routingState)
	appliedAt := time.Now()
	cancel()
	if err != nil {
		s.record("error", "automation.priority_apply_failed", fmt.Sprintf("apply priority policies failed: %v", err))
		return false, err
	}
	s.noteDegradedTargets(appliedState, report)
	s.observeBlockedRoutes(state, report, appliedAt)
	if manual {
		if err := s.confirmManualSelection(ctx, next[selection.policyID], report); err != nil {
			// The apply completed, but the selected rule may be blocked or retained.
			// Publish that outcome without accepting the new preference.
			s.priorityMu.Lock()
			reconcilePriorityOutcomes(s.priority.decisions, report)
			s.priorityMu.Unlock()
			return false, err
		}
	}
	s.priority.reconcileRequired = false
	s.priorityMu.Lock()
	reconcilePriorityOutcomes(next, report)
	s.priority.decisions = next
	if manual {
		if selection.location == "" {
			delete(s.priority.overrides, selection.policyID)
		} else {
			s.priority.overrides[selection.policyID] = priorityOverride{Location: next[selection.policyID].ActiveLocation, Since: now}
		}
	}
	s.priority.lastApply = appliedAt
	verify := s.appliedOutageTargetsLocked(state, next, s.priority.lastApply)
	s.priorityMu.Unlock()
	s.verifyAppliedOutages(ctx, state, verify)
	s.savePriorityTargets(state, appliedState, report)
	s.record("info", "automation.priority_applied", formatPriorityApplyMessage(next))
	return true, nil
}

func (s *Supervisor) confirmManualSelection(ctx context.Context, decision priorityDecision, report *subscription.ApplyReport) error {
	ruleID := priorityRuleID(decision.PolicyID)
	actual, found := report.Rules[ruleID]
	if !found || actual.Blocked || actual.Issue != "" || actual.ProviderID != decision.ProviderID || !strings.EqualFold(actual.Location, decision.ActiveLocation) {
		return ErrPriorityUnconfirmed
	}

	snapshot, err := s.status.RuntimeSnapshot(ctx)
	if err == nil {
		key := decision.ProviderID + "::" + strings.ToLower(decision.ActiveLocation)
		for _, item := range snapshot.SubscriptionRuntime {
			if item.Key == key && item.Status == "running" && item.LocationIssue == "" {
				return nil
			}
		}
	}
	actual.Location, actual.Issue = "", ErrPriorityUnconfirmed.Error()
	report.Rules[ruleID] = actual
	return errors.Join(ErrPriorityUnconfirmed, err)
}

func (s *Supervisor) evaluateManualSelection(ctx context.Context, state config.State, snapshot status.Snapshot, policy config.PriorityPolicy, now time.Time, location string) (priorityDecision, error) {
	if len(priorityPolicyEntries(state, policy)) == 0 {
		return priorityDecision{}, ErrPriorityUnavailable
	}
	var override priorityOverride
	if location != "" {
		for _, target := range policy.Targets {
			if strings.EqualFold(target.Location, location) {
				override = priorityOverride{Location: target.Location, Since: now}
				break
			}
		}
		if override.Location == "" {
			return priorityDecision{}, ErrPriorityUnavailable
		}
	}
	decision, ok := s.evaluatePriorityPolicy(ctx, state, snapshot, policy, now, override)
	if !ok {
		return priorityDecision{}, ErrPriorityUnavailable
	}
	if location != "" {
		s.priorityMu.RLock()
		health := s.priority.health[priorityHealthKey(policy.ID, override.Location)]
		s.priorityMu.RUnlock()
		if decision.ActiveLocation != override.Location || health.ConsecutiveFailures != 0 {
			return priorityDecision{}, ErrPriorityTargetUnavailable
		}
	}
	return decision, nil
}

func (s *Supervisor) priorityAppliedState(state config.State) config.State {
	s.priorityMu.RLock()
	defer s.priorityMu.RUnlock()
	if len(s.priority.decisions) == 0 {
		return ApplyPriorityDefaults(state, time.Now())
	}
	return buildPriorityAppliedStateFromDecisions(sharedDomainRoutingState(state), s.priority.decisions)
}

func ApplyPriorityDefaults(state config.State, now time.Time) config.State {
	state = sharedDomainRoutingState(state)
	return buildPriorityAppliedStateFromDecisions(state, defaultPriorityDecisions(state, now))
}

func defaultPriorityDecisions(state config.State, now time.Time) map[string]priorityDecision {
	decisions := make(map[string]priorityDecision)
	providers := providersIndex(state.Providers)
	for _, policy := range state.PriorityPolicies {
		if !policy.Enabled || len(policy.Targets) == 0 {
			continue
		}
		if policy.RuleID != "" && len(priorityPolicyEntries(state, policy)) == 0 {
			continue
		}
		provider, ok := providers[policy.ProviderID]
		if !ok || !provider.Enabled || provider.Type != config.ProviderTypeSubscription {
			continue
		}
		location := preferredPriorityLocation(policy, now)
		if location == "" {
			continue
		}
		decisions[policy.ID] = priorityDecision{
			PolicyID:          policy.ID,
			PolicyName:        policy.Name,
			ProviderID:        policy.ProviderID,
			ProviderName:      provider.Name,
			ActiveLocation:    location,
			PreferredLocation: location,
			Mode:              "provider",
			Reason:            "default priority selection",
			Since:             now,
			Fingerprint:       priorityPolicyFingerprint(policy, state),
		}
	}
	return decisions
}

func buildPriorityAppliedStateFromDecisions(state config.State, decisions map[string]priorityDecision) config.State {
	out := state
	out.PriorityPolicies = nil
	if len(state.PriorityPolicies) == 0 || len(decisions) == 0 {
		out.Rules = append([]config.Rule(nil), state.Rules...)
		return out
	}

	controlledProviders := make(map[string]bool, len(decisions))
	controlledRules := make(map[string]bool, len(decisions))
	for _, policy := range state.PriorityPolicies {
		if _, found := decisions[policy.ID]; found {
			if policy.RuleID != "" {
				controlledRules[policy.RuleID] = true
			} else {
				controlledProviders[policy.ProviderID] = true
			}
		}
	}

	out.Rules = make([]config.Rule, 0, len(state.Rules)+len(decisions))
	for _, rule := range state.Rules {
		if controlledProviders[rule.ProviderID] || controlledRules[rule.ID] {
			continue
		}
		out.Rules = append(out.Rules, rule)
	}

	appliedScopes := make(map[string]bool, len(decisions))
	for _, policy := range state.PriorityPolicies {
		decision, ok := decisions[policy.ID]
		if ok && policy.RuleID != "" && len(policy.Targets) != 0 &&
			!locationInPriorityTargets(policy, decision.ActiveLocation) {
			decision.ActiveLocation = policy.Targets[0].Location
		}
		if !ok || decision.Mode == "direct" || strings.TrimSpace(decision.ActiveLocation) == "" {
			continue
		}
		scope := "provider:" + policy.ProviderID
		if policy.RuleID != "" {
			scope = "rule:" + policy.RuleID
		}
		if appliedScopes[scope] {
			continue
		}
		entries := priorityPolicyEntries(state, policy)
		if len(entries) == 0 {
			continue
		}
		appliedScopes[scope] = true
		out.Rules = append(out.Rules, config.Rule{
			ID:               priorityRuleID(policy.ID),
			Name:             policy.Name,
			ProviderID:       policy.ProviderID,
			SelectedLocation: decision.ActiveLocation,
			Domains:          entries,
			Enabled:          policy.Enabled,
		})
	}
	return out
}

func priorityProviderEntries(state config.State, providerID string) []string {
	raw := make([]string, 0, 16)
	for _, rule := range state.Rules {
		if !rule.Enabled || rule.ProviderID != providerID {
			continue
		}
		raw = append(raw, rule.Domains...)
	}
	return domains.NormalizeEntries(raw)
}

func priorityPolicyEntries(state config.State, policy config.PriorityPolicy) []string {
	if policy.RuleID == "" {
		return priorityProviderEntries(state, policy.ProviderID)
	}
	for _, rule := range state.Rules {
		if rule.ID == policy.RuleID && rule.Enabled && rule.ProviderID == policy.ProviderID {
			return rule.Domains
		}
	}
	return nil
}

func priorityRuleID(policyID string) string {
	return "__priority_policy_" + strings.TrimSpace(policyID)
}

func (s *Supervisor) prunePriorityRuntimeLocked(state config.State, now time.Time) bool {
	policies := make(map[string]config.PriorityPolicy, len(state.PriorityPolicies))
	for _, policy := range state.PriorityPolicies {
		policies[policy.ID] = policy
	}
	prunedHistory := false
	for id, series := range s.priority.outages {
		policy, exists := policies[id]
		if !exists || !policy.Enabled || series.Scope != outageScope(priorityPolicyFingerprint(policy, state)) {
			prunedHistory = prunedHistory || series.Last != nil
			delete(s.priority.outages, id)
		}
	}
	for policyID, override := range s.priority.overrides {
		policy, exists := policies[policyID]
		if !exists || !locationInPriorityTargets(policy, override.Location) {
			delete(s.priority.overrides, policyID)
			continue
		}
		if boundary := nextPriorityScheduleBoundary(policy, override.Since); !boundary.IsZero() && !now.Before(boundary) {
			delete(s.priority.overrides, policyID)
		}
	}
	return prunedHistory
}

// The caller holds priorityEvalMu, not priorityMu: probes run without blocking
// status reads or override updates, and shared runtime access uses short locks.
func (s *Supervisor) evaluatePriorityPolicyLocked(ctx context.Context, state config.State, snapshot status.Snapshot, policy config.PriorityPolicy, now time.Time) (priorityDecision, bool) {
	s.priorityMu.RLock()
	override := s.priority.overrides[policy.ID]
	s.priorityMu.RUnlock()
	decision, ok := s.evaluatePriorityPolicy(ctx, state, snapshot, policy, now, override)
	if ok && override.Location != "" && decision.ActiveLocation != override.Location {
		s.priorityMu.Lock()
		if s.priority.overrides[policy.ID] == override {
			delete(s.priority.overrides, policy.ID)
		}
		s.priorityMu.Unlock()
	}
	return decision, ok
}

func (s *Supervisor) evaluatePriorityPolicy(ctx context.Context, state config.State, snapshot status.Snapshot, policy config.PriorityPolicy, now time.Time, override priorityOverride) (priorityDecision, bool) {
	if !policy.Enabled || len(policy.Targets) == 0 {
		return priorityDecision{}, false
	}
	if policy.RuleID != "" && len(priorityPolicyEntries(state, policy)) == 0 {
		return priorityDecision{}, false
	}
	provider, ok := findProvider(state.Providers, policy.ProviderID)
	if !ok || !provider.Enabled || provider.Type != config.ProviderTypeSubscription {
		return priorityDecision{}, false
	}

	preferred := preferredPriorityLocation(policy, now)
	s.priorityMu.RLock()
	hasOverride := override.Location != ""
	previous := s.priority.decisions[policy.ID]
	pings := make(map[string]priorityServerPing, len(policy.Targets))
	for _, target := range policy.Targets {
		key := standbyProbeKey(provider, target.Location)
		pings[key] = s.priority.pings[key]
	}
	s.priorityMu.RUnlock()
	latencyMode := config.NormalizePrioritySelectionMode(policy.SelectionMode) == config.PrioritySelectionLatency
	chooseByPing := latencyMode && !hasOverride && activePriorityScheduleLocation(policy, now) == ""
	candidatePolicy := policy
	if latencyMode {
		candidatePolicy.Targets = priorityTargetsByPing(policy, provider, pings, now)
	}
	if chooseByPing {
		preferred = candidatePolicy.Targets[0].Location
	}
	if policy.RuleID != "" && !locationInPriorityTargets(policy, previous.ActiveLocation) {
		previous = priorityDecision{}
	}
	if hasOverride {
		preferred = override.Location
	}
	if preferred == "" {
		return priorityDecision{}, false
	}

	fingerprint := priorityPolicyFingerprint(policy, state)
	if previous.ActiveLocation == "" {
		if running := runningPriorityTarget(snapshot, provider.ID, policy.Targets); running != "" {
			previous = priorityDecision{
				PolicyID:          policy.ID,
				PolicyName:        policy.Name,
				ProviderID:        provider.ID,
				ProviderName:      provider.Name,
				ActiveLocation:    running,
				PreferredLocation: preferred,
				Mode:              "provider",
				Since:             now,
				Fingerprint:       fingerprint,
			}
		}
	}
	candidates := priorityCandidateLocations(candidatePolicy, preferred)
	failureThreshold := time.Duration(state.Automation.FailoverFailureSeconds) * time.Second
	if failureThreshold <= 0 {
		failureThreshold = 2 * time.Minute
	}
	rapid := rapidPriorityChecks(state)
	search := prioritySearch{
		supervisor: s, provider: provider,
		check: func(location string) providerProbeResult {
			return s.priorityTargetProbe(ctx, state, snapshot, provider, location, previous)
		},
		record: func(location string, probe providerProbeResult) providerHealthState {
			return s.updatePriorityTargetHealth(policy, provider, location, probe, now)
		},
		flights: make(map[string]*standbyFlight), samples: make(map[string]priorityProbeSample),
	}
	// Start the usual restoration check alongside the active tunnel. Rapid
	// mode also keeps the first two reserves warm within the shared budget.
	warm := 0
	for _, location := range candidates {
		if location == previous.ActiveLocation {
			continue
		}
		if !rapid && location != preferred {
			break
		}
		search.start(ctx, location)
		warm++
		if warm == standbyProbeLimit || !rapid {
			break
		}
	}
	defer func() {
		if ctx.Err() == nil {
			for location := range search.flights {
				search.collect(location)
			}
		}
	}()
	var active priorityProbeSample
	activeProtected := false
	if previous.ActiveLocation != "" {
		active.probe = search.check(previous.ActiveLocation)
		if ctx.Err() != nil {
			return priorityDecision{}, false
		}
		active.health = search.record(previous.ActiveLocation, active.probe)
		activeProtected = !previous.outcome.Blocked && (active.probe.Healthy || active.health.UnhealthySince.IsZero() ||
			now.Sub(active.health.UnhealthySince) < failureThreshold || active.health.ConsecutiveFailures < failoverFailureStreak())
		if previous.Fingerprint == fingerprint {
			s.observePriorityActive(ctx, policy, previous, active.probe, !active.probe.Healthy && !activeProtected)
		}
	}
	lookAhead := rapid && !activeProtected
	selected := ""
	selectedProbe := providerProbeResult{}
	reason := ""
	for index, location := range candidates {
		// Keep a working backup instead of hopping through intermediate targets.
		// Only the preferred target earns automatic restoration after its grace period.
		if !chooseByPing && activeProtected && location != preferred && location != previous.ActiveLocation {
			continue
		}
		sample := active
		if location != previous.ActiveLocation {
			var ok bool
			sample, ok = search.await(ctx, candidates[index:], previous.ActiveLocation, lookAhead)
			if !ok {
				return priorityDecision{}, false
			}
		}
		probe, health := sample.probe, sample.health
		if !probe.Healthy {
			if location == previous.ActiveLocation && activeProtected {
				selected = location
				selectedProbe = probe
				reason = "active target probe failed; waiting before fallback"
				break
			}
			continue
		}
		if chooseByPing && activeProtected && location != previous.ActiveLocation {
			stableFor := max(time.Minute, time.Duration(state.Automation.FailoverRestoreSeconds)*time.Second)
			currentPing := pings[standbyProbeKey(provider, previous.ActiveLocation)]
			candidatePing := pings[standbyProbeKey(provider, location)]
			if !active.probe.Healthy || !significantPingImprovement(currentPing, candidatePing, now) ||
				health.HealthySince.IsZero() || now.Sub(health.HealthySince) < stableFor || now.Sub(previous.Since) < stableFor || health.ConsecutiveSuccesses < failoverRestoreStreak() {
				selected = previous.ActiveLocation
				selectedProbe = active.probe
				reason = "keeping active VPN until a lower server ping is a stable improvement"
				break
			}
		}
		// Restoration hysteresis protects a working fallback from flapping. It
		// must not delay an explicit healthy selection or prolong an outage once
		// the active fallback has exhausted its failure grace period.
		if !chooseByPing && location == preferred && activeProtected && !hasOverride && previous.ActiveLocation != "" && previous.ActiveLocation != preferred {
			threshold := time.Duration(state.Automation.FailoverRestoreSeconds) * time.Second
			if threshold <= 0 {
				threshold = 60 * time.Second
			}
			if health.HealthySince.IsZero() || now.Sub(health.HealthySince) < threshold || health.ConsecutiveSuccesses < failoverRestoreStreak() {
				continue
			}
		}
		selected = location
		selectedProbe = probe
		if location == preferred {
			reason = "preferred target is healthy"
		} else {
			reason = fmt.Sprintf("preferred target %s is unavailable; using fallback", preferred)
		}
		if chooseByPing {
			reason = "healthy target selected by recent server ping"
		}
		break
	}

	if selected == "" {
		selected = firstNonEmpty(previous.ActiveLocation, preferred)
		reason = "all priority targets are unhealthy; keeping VPN route because direct internet release is disabled"
	}

	if chooseByPing {
		preferred = selected
	}

	return priorityDecision{
		PolicyID:          policy.ID,
		PolicyName:        policy.Name,
		ProviderID:        policy.ProviderID,
		ProviderName:      provider.Name,
		ActiveLocation:    selected,
		PreferredLocation: preferred,
		Mode:              "provider",
		Reason:            priorityReasonWithProbe(reason, selectedProbe),
		selectedAt:        time.Now().UTC(),
		Since:             decisionSince(previous, now, selected, "provider", fingerprint),
		Fingerprint:       fingerprint,
	}, true
}

func (s *Supervisor) priorityTargetProbe(ctx context.Context, state config.State, snapshot status.Snapshot, provider config.Provider, location string, previous priorityDecision) providerProbeResult {
	evaluationCtx := ctx
	if rapidPriorityChecks(state) {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 1500*time.Millisecond)
		defer cancel()
	}
	if previous.ActiveLocation == location {
		if previous.outcome.Blocked {
			return providerProbeResult{Location: location, Detail: previous.outcome.Issue}
		}
		key := provider.ID + "::" + strings.ToLower(strings.TrimSpace(location))
		foundRuntime := false
		for _, item := range snapshot.SubscriptionRuntime {
			if item.Key != key {
				continue
			}
			foundRuntime = true
			if item.LocationIssue != "" {
				return providerProbeResult{Location: location, Detail: item.LocationIssue, runtimeUnavailable: true}
			}
			if item.Status != "running" {
				return providerProbeResult{Detail: firstNonEmpty(item.StatusDetail, fmt.Sprintf("subscription location %s is not running", location)), Location: location, runtimeUnavailable: true}
			}
			if strings.TrimSpace(item.InterfaceName) != "" {
				probe := s.priorityTunnelProbe(ctx, item.InterfaceName, item.FWMark)
				probe.Location = location
				if evaluationCtx.Err() == nil {
					s.cacheProviderProbe(standbyProbeKey(provider, location), probe)
				}
				return probe
			}
			break
		}
		if !foundRuntime {
			return providerProbeResult{Detail: "subscription runtime is missing for active priority target", Location: location, runtimeUnavailable: true}
		}
	}

	if previous.ActiveLocation != location && s.probeStandby != nil {
		result := s.probeStandby(ctx, provider, location)
		result.Location = location
		return result
	}
	probe := s.probeProvider(ctx, state, provider, location)
	if probe.Location == "" {
		probe.Location = location
	}
	return probe
}

func rapidPriorityChecks(state config.State) bool {
	return state.Automation.FailoverFailureSeconds > 0 && state.Automation.FailoverFailureSeconds <= 10
}

func (s *Supervisor) updatePriorityTargetHealth(policy config.PriorityPolicy, provider config.Provider, location string, probe providerProbeResult, now time.Time) providerHealthState {
	s.priorityMu.Lock()
	defer s.priorityMu.Unlock()
	s.priority.ensure()
	key := priorityHealthKey(policy.ID, location)
	previous := s.priority.health[key]
	next := previous
	next.ProviderID = provider.ID
	next.ProviderName = provider.Name + " / " + location
	next.LastChecked = now
	next.Detail = strings.TrimSpace(probe.Detail)
	next.LatencyMs = probe.LatencyMs

	if probe.Healthy {
		next.ConsecutiveSuccesses++
		next.ConsecutiveFailures = 0
		next.LastError = ""
		if next.HealthySince.IsZero() {
			next.HealthySince = now
		}
		next.UnhealthySince = time.Time{}
		next.Score = scoreForProbe(probe, true)
		if next.Score < 100 {
			next.Status = "degraded"
		} else {
			next.Status = "healthy"
		}
	} else {
		next.ConsecutiveFailures++
		next.ConsecutiveSuccesses = 0
		next.LastError = firstNonEmpty(probe.Detail, "priority target probe failed")
		if next.UnhealthySince.IsZero() {
			next.UnhealthySince = now
		}
		next.HealthySince = time.Time{}
		next.Score = scoreForProbe(probe, false)
		next.Status = "unhealthy"
	}

	s.priority.health[key] = next
	return next
}

func priorityHealthKey(policyID string, location string) string {
	return strings.TrimSpace(policyID) + "::" + strings.ToLower(strings.TrimSpace(location))
}

func preferredPriorityLocation(policy config.PriorityPolicy, now time.Time) string {
	if location := activePriorityScheduleLocation(policy, now); location != "" {
		return location
	}
	if len(policy.Targets) == 0 {
		return ""
	}
	return strings.TrimSpace(policy.Targets[0].Location)
}

func activePriorityScheduleLocation(policy config.PriorityPolicy, now time.Time) string {
	minute := now.Hour()*60 + now.Minute()
	for _, window := range policy.Schedule {
		start, okStart := parsePriorityClock(window.Start)
		end, okEnd := parsePriorityClock(window.End)
		if !okStart || !okEnd || start == end {
			continue
		}
		if priorityWindowContains(start, end, minute) {
			return strings.TrimSpace(window.Location)
		}
	}
	return ""
}

func priorityWindowContains(start int, end int, minute int) bool {
	if start < end {
		return minute >= start && minute < end
	}
	return minute >= start || minute < end
}

func nextPriorityScheduleBoundary(policy config.PriorityPolicy, after time.Time) time.Time {
	if len(policy.Schedule) == 0 || after.IsZero() {
		return time.Time{}
	}
	base := time.Date(after.Year(), after.Month(), after.Day(), 0, 0, 0, 0, after.Location())
	var next time.Time
	for day := 0; day < 3; day++ {
		dayBase := base.AddDate(0, 0, day)
		for _, window := range policy.Schedule {
			for _, raw := range []string{window.Start, window.End} {
				minute, ok := parsePriorityClock(raw)
				if !ok {
					continue
				}
				candidate := dayBase.Add(time.Duration(minute) * time.Minute)
				if !candidate.After(after) {
					continue
				}
				if next.IsZero() || candidate.Before(next) {
					next = candidate
				}
			}
		}
	}
	return next
}

func parsePriorityClock(value string) (int, bool) {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) != 2 {
		return 0, false
	}
	hour, okHour := atoiRange(parts[0], 0, 23)
	minute, okMinute := atoiRange(parts[1], 0, 59)
	if !okHour || !okMinute {
		return 0, false
	}
	return hour*60 + minute, true
}

func atoiRange(value string, min int, max int) (int, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	n := 0
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, n >= min && n <= max
}

func priorityCandidateLocations(policy config.PriorityPolicy, preferred string) []string {
	locations := make([]string, 0, len(policy.Targets))
	for _, target := range policy.Targets {
		location := strings.TrimSpace(target.Location)
		if location != "" {
			locations = append(locations, location)
		}
	}
	if len(locations) == 0 {
		return nil
	}
	start := 0
	for index, location := range locations {
		if strings.EqualFold(location, preferred) {
			start = index
			break
		}
	}
	out := make([]string, 0, len(locations))
	if config.NormalizePrioritySelectionMode(policy.SelectionMode) == config.PrioritySelectionLatency {
		out = append(out, locations[start])
		for index, location := range locations {
			if index != start {
				out = append(out, location)
			}
		}
		return out
	}
	for offset := 0; offset < len(locations); offset++ {
		out = append(out, locations[(start+offset)%len(locations)])
	}
	return out
}

func runningPriorityTarget(snapshot status.Snapshot, providerID string, targets []config.PriorityTarget) string {
	for _, target := range targets {
		location := strings.TrimSpace(target.Location)
		if location == "" {
			continue
		}
		key := providerID + "::" + strings.ToLower(location)
		for _, item := range snapshot.SubscriptionRuntime {
			if item.Key == key && item.Status == "running" {
				return location
			}
		}
	}
	return ""
}

func locationInPriorityTargets(policy config.PriorityPolicy, location string) bool {
	location = strings.TrimSpace(location)
	for _, target := range policy.Targets {
		if strings.EqualFold(strings.TrimSpace(target.Location), location) {
			return true
		}
	}
	return false
}

func priorityPolicyFingerprint(policy config.PriorityPolicy, state config.State) string {
	var b strings.Builder
	b.WriteString(policy.ProviderID)
	b.WriteString("\n")
	b.WriteString(policy.RuleID)
	b.WriteString("\n")
	b.WriteString(config.NormalizePrioritySelectionMode(policy.SelectionMode))
	b.WriteString("\n")
	for _, entry := range priorityPolicyEntries(state, policy) {
		b.WriteString(entry)
		b.WriteString("\n")
	}
	b.WriteString("--targets--\n")
	for _, target := range policy.Targets {
		b.WriteString(target.Location)
		b.WriteString("\n")
	}
	b.WriteString("--schedule--\n")
	for _, window := range policy.Schedule {
		b.WriteString(window.Start)
		b.WriteString("|")
		b.WriteString(window.End)
		b.WriteString("|")
		b.WriteString(window.Location)
		b.WriteString("\n")
	}
	return b.String()
}

func decisionSince(previous priorityDecision, now time.Time, location string, mode string, fingerprint string) time.Time {
	if previous.ActiveLocation == location && previous.Mode == mode && previous.Fingerprint == fingerprint && !previous.Since.IsZero() {
		return previous.Since
	}
	return now
}

func samePriorityDecisionSet(left map[string]priorityDecision, right map[string]priorityDecision) bool {
	if len(left) != len(right) {
		return false
	}
	for key, leftDecision := range left {
		rightDecision, ok := right[key]
		if !ok {
			return false
		}
		if leftDecision.ActiveLocation != rightDecision.ActiveLocation ||
			leftDecision.PreferredLocation != rightDecision.PreferredLocation ||
			leftDecision.Mode != rightDecision.Mode ||
			leftDecision.Fingerprint != rightDecision.Fingerprint {
			return false
		}
	}
	return true
}

func (s *Supervisor) priorityRuntimeMatchesDecisions(snapshot status.Snapshot, decisions map[string]priorityDecision) bool {
	expected := make(map[string]struct{}, len(decisions))
	for _, decision := range decisions {
		if decision.Mode == "direct" || decision.ActiveLocation == "" {
			continue
		}
		key := decision.ProviderID + "::" + strings.ToLower(strings.TrimSpace(decision.ActiveLocation))
		expected[key] = struct{}{}
	}
	return !s.subscriptionTargetsNeedRecovery(expected, snapshot.SubscriptionRuntime)
}

func priorityReasonWithProbe(reason string, probe providerProbeResult) string {
	if suffix := formatProbeSuffix(probe); suffix != "" {
		return reason + suffix
	}
	return reason
}

func formatPriorityApplyMessage(decisions map[string]priorityDecision) string {
	if len(decisions) == 0 {
		return "Priority policies disabled; routes reconciled"
	}
	parts := make([]string, 0, len(decisions))
	for _, decision := range decisions {
		target := decision.servingLocation()
		if decision.outcome.Blocked {
			target = "blocked"
		}
		if decision.Mode == "direct" {
			target = "direct"
		}
		parts = append(parts, fmt.Sprintf("%s -> %s", decision.PolicyName, target))
	}
	return "Priority policies applied: " + strings.Join(parts, "; ")
}

func (s *Supervisor) PriorityStatus() PriorityStatus {
	var state config.State
	if s.state != nil {
		if loaded, err := s.state.Load(); err == nil {
			state = loaded
		}
	}
	state = sharedDomainRoutingState(state)
	providers := providersIndex(state.Providers)

	s.priorityMu.RLock()
	defer s.priorityMu.RUnlock()

	result := PriorityStatus{
		LastApplyAt: timeString(s.priority.lastApply),
		Policies:    make([]PriorityPolicyStatus, 0, len(state.PriorityPolicies)),
	}
	for _, policy := range state.PriorityPolicies {
		provider := providers[policy.ProviderID]
		decision := s.priority.decisions[policy.ID]
		activeLocation := decision.servingLocation()
		override := s.priority.overrides[policy.ID]
		item := PriorityPolicyStatus{
			PolicyID:          policy.ID,
			PolicyName:        policy.Name,
			ProviderID:        policy.ProviderID,
			ProviderName:      provider.Name,
			Enabled:           policy.Enabled,
			SelectionMode:     config.NormalizePrioritySelectionMode(policy.SelectionMode),
			ScheduledLocation: activePriorityScheduleLocation(policy, time.Now()),
			ActiveLocation:    activeLocation,
			LocationIssue:     decision.outcome.Issue,
			Blocked:           decision.outcome.Blocked,
			PreferredLocation: decision.PreferredLocation,
			OverrideLocation:  override.Location,
			Mode:              decision.Mode,
			Reason:            decision.Reason,
			Since:             timeString(decision.Since),
			Targets:           make([]PriorityTargetStatus, 0, len(policy.Targets)),
		}
		if series := s.priority.outages[policy.ID]; policy.Enabled && series != nil && series.Scope == outageScope(priorityPolicyFingerprint(policy, state)) {
			item.CurrentOutage = series.Current.snapshot(time.Now())
			item.LastOutage = series.Last.snapshot(time.Now())
		}
		for _, target := range policy.Targets {
			location := strings.TrimSpace(target.Location)
			health := s.priority.health[priorityHealthKey(policy.ID, location)]
			ping := s.priority.pings[standbyProbeKey(provider, location)]
			item.Targets = append(item.Targets, PriorityTargetStatus{
				Location:             location,
				Status:               firstNonEmpty(health.Status, "unknown"),
				Score:                health.Score,
				ConsecutiveFailures:  health.ConsecutiveFailures,
				ConsecutiveSuccesses: health.ConsecutiveSuccesses,
				UnhealthySince:       timeString(health.UnhealthySince),
				HealthySince:         timeString(health.HealthySince),
				LastChecked:          timeString(health.LastChecked),
				LastError:            health.LastError,
				Detail:               health.Detail,
				LatencyMs:            health.LatencyMs,
				PingMs:               ping.Ms,
				PingError:            ping.Error,
				PingCheckedAt:        timeString(ping.Checked),
				Active:               strings.EqualFold(location, activeLocation),
				Preferred:            strings.EqualFold(location, decision.PreferredLocation),
			})
		}
		result.Policies = append(result.Policies, item)
	}
	return result
}
