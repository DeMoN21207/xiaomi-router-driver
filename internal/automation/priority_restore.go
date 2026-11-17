package automation

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"xiomi-router-driver/internal/config"
	"xiomi-router-driver/internal/fileutil"
	"xiomi-router-driver/internal/status"
	"xiomi-router-driver/internal/subscription"
)

const priorityTargetsFilename = "priority-targets.json"
const degradedTargetRetryInterval = time.Minute

type savedPriorityTarget struct {
	RuleID     string `json:"ruleId"`
	ProviderID string `json:"providerId"`
	Location   string `json:"location"`
}

// Keep the desired route for retries and for rendering the fail-closed rules.
// Status reflects the location that actually carries the rule's traffic.
func (decision priorityDecision) servingLocation() string {
	if decision.outcome.ProviderID == decision.ProviderID && decision.outcome.ProviderID != "" {
		return decision.outcome.Location
	}
	return decision.ActiveLocation
}

func reconcilePriorityOutcomes(decisions map[string]priorityDecision, report *subscription.ApplyReport) {
	if report == nil || report.Rules == nil {
		return
	}
	for id, decision := range decisions {
		decision.outcome = report.Rules[priorityRuleID(id)]
		decisions[id] = decision
	}
}

func (s *Supervisor) carryPriorityOutcomesLocked(decisions map[string]priorityDecision) {
	for id, decision := range decisions {
		decision.outcome = s.priority.decisions[id].outcome
		decisions[id] = decision
	}
}

func (target savedPriorityTarget) locationFor(policy config.PriorityPolicy) string {
	if target.ProviderID != policy.ProviderID || target.RuleID != policy.RuleID {
		return ""
	}
	for _, candidate := range policy.Targets {
		if strings.EqualFold(target.Location, candidate.Location) {
			return candidate.Location
		}
	}
	return ""
}

func (s *Supervisor) loadPriorityTargets() map[string]savedPriorityTarget {
	if s.dataDir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(s.dataDir, priorityTargetsFilename))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	var saved map[string]savedPriorityTarget
	if err == nil {
		err = json.Unmarshal(data, &saved)
	}
	if err != nil {
		s.record("error", "automation.priority_restore_failed", fmt.Sprintf("load saved priority targets: %v", err))
		return nil
	}
	return saved
}

// Store only synthetic rules present in the successful apply. Policies with
// no routed domains or a scope already claimed by another policy have no route.
func (s *Supervisor) savePriorityTargets(state, applied config.State, report *subscription.ApplyReport) {
	if s.dataDir == "" {
		return
	}
	rules := make(map[string]config.Rule, len(applied.Rules))
	for _, rule := range applied.Rules {
		rules[rule.ID] = rule
	}
	previous := s.loadPriorityTargets()
	saved := make(map[string]savedPriorityTarget)
	for _, policy := range state.PriorityPolicies {
		rule, ok := rules[priorityRuleID(policy.ID)]
		if !ok || !rule.Enabled || rule.ProviderID != policy.ProviderID {
			continue
		}
		if report != nil && report.Rules != nil {
			actual, found := report.Rules[rule.ID]
			if !found || actual.Blocked || actual.Issue != "" || actual.ProviderID != rule.ProviderID || !strings.EqualFold(actual.Location, rule.SelectedLocation) {
				if old := previous[policy.ID]; old.locationFor(policy) != "" {
					saved[policy.ID] = old
				}
				continue
			}
		}
		saved[policy.ID] = savedPriorityTarget{RuleID: policy.RuleID, ProviderID: policy.ProviderID, Location: rule.SelectedLocation}
	}
	if maps.Equal(saved, previous) {
		return
	}
	data, err := json.Marshal(saved)
	if err == nil {
		err = fileutil.WriteFileAtomic(filepath.Join(s.dataDir, priorityTargetsFilename), data, 0o600)
	}
	if err != nil {
		s.record("error", "automation.priority_save_failed", fmt.Sprintf("save applied priority targets: %v", err))
	}
}

// Keep probing alternative candidates on the normal cadence. Reapplying the
// same unresolved route can wait for the subscription cache to change.
func (s *Supervisor) noteDegradedTargets(applied config.State, report *subscription.ApplyReport) {
	if report == nil || report.Rules == nil {
		return
	}
	next := make(map[string]time.Time)
	retryAt := time.Now().Add(degradedTargetRetryInterval)
	for _, rule := range applied.Rules {
		outcome := report.Rules[rule.ID]
		if outcome.Blocked || outcome.Issue != "" {
			next[rule.ProviderID+"::"+strings.ToLower(rule.SelectedLocation)] = retryAt
		}
	}
	s.degradedRetryAt = next
}

func (s *Supervisor) subscriptionTargetsNeedRecovery(expected map[string]struct{}, snapshots []subscription.RuntimeSnapshot) bool {
	running := make(map[string]bool, len(snapshots))
	for _, snapshot := range snapshots {
		running[snapshot.Key] = snapshot.Status == "running" && snapshot.LocationIssue == ""
	}
	now := time.Now()
	for key := range expected {
		if retryAt, degraded := s.degradedRetryAt[key]; degraded {
			if !now.Before(retryAt) {
				return true
			}
		} else if !running[key] {
			return true
		}
	}
	return false
}

func (s *Supervisor) startupPriorityDecisions(state config.State, snapshot status.Snapshot) map[string]priorityDecision {
	decisions := defaultPriorityDecisions(state, time.Now())
	saved := s.loadPriorityTargets()
	configured := make(map[string]string, len(state.Rules))
	for _, rule := range state.Rules {
		configured[rule.ID] = rule.SelectedLocation
	}
	running := make(map[string]bool, len(snapshot.SubscriptionRuntime))
	for _, item := range snapshot.SubscriptionRuntime {
		if item.Status == "running" && item.LocationIssue == "" {
			running[item.Key] = true
		}
	}
	for _, policy := range state.PriorityPolicies {
		decision, enabled := decisions[policy.ID]
		if !enabled {
			continue
		}
		if location := saved[policy.ID].locationFor(policy); location != "" {
			decision.ActiveLocation = location
			decision.Reason = "restored previously applied target"
		} else if policy.RuleID != "" {
			if location := runningRulePriorityTarget(policy, configured[policy.RuleID], running); location != "" {
				decision.ActiveLocation = location
				decision.Reason = "restored running target"
			}
		}
		decisions[policy.ID] = decision
	}
	return decisions
}

// Older installations have no per-rule checkpoint. A shared provider may
// have multiple live tunnels, so use the rule's own configured location as
// the tie-breaker. If it cannot disambiguate, keep the policy default.
func runningRulePriorityTarget(policy config.PriorityPolicy, configured string, running map[string]bool) string {
	active := ""
	count := 0
	for _, target := range policy.Targets {
		location := target.Location
		if !running[policy.ProviderID+"::"+strings.ToLower(location)] {
			continue
		}
		if strings.EqualFold(location, configured) {
			return location
		}
		active = location
		count++
	}
	if count == 1 {
		return active
	}
	return ""
}
