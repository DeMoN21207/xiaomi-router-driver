package subscription

import (
	"bytes"
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"xiomi-router-driver/internal/config"
	"xiomi-router-driver/internal/domains"
)

// ApplyReport describes the committed routes for the supplied rule IDs. A nil
// Rules map means that no subscription transaction has published a result.
type ApplyReport struct {
	Rules map[string]RuleApplyResult
}

type RuleApplyResult struct {
	ProviderID string
	Location   string // Actual VPN location. Empty when traffic is blocked.
	Issue      string
	Blocked    bool
}

type applyReportKey struct{}

func WithApplyReport(ctx context.Context) (context.Context, *ApplyReport) {
	if report, ok := ctx.Value(applyReportKey{}).(*ApplyReport); ok {
		return ctx, report
	}
	report := new(ApplyReport)
	return context.WithValue(ctx, applyReportKey{}, report), report
}

func (report *ApplyReport) publish(rules []config.Rule, outcomes map[string]RuleApplyResult) {
	results := make(map[string]RuleApplyResult)
	for _, rule := range rules {
		key := rule.ProviderID + "::" + strings.ToLower(strings.TrimSpace(rule.SelectedLocation))
		if result, ok := outcomes[key]; ok && len(rule.Domains) > 0 {
			results[rule.ID] = result
		}
	}
	report.Rules = results
}

func (m *Manager) resolveUnavailable(desired []desiredInstance, saved []applyPlan, instances []*managedInstance, base config.RoutingSettings) ([]desiredInstance, map[string]RuleApplyResult) {
	outcomes := make(map[string]RuleApplyResult, len(desired))
	resolved := make(map[string]*desiredInstance, len(desired))
	for _, group := range desired {
		if group.LocationIssue == "" {
			resolved[group.Key] = &group
			outcomes[group.Key] = RuleApplyResult{ProviderID: group.Provider.ID, Location: group.Location}
		}
	}
	if len(resolved) == len(desired) {
		return desired, outcomes
	}
	for i := range desired {
		group := &desired[i]
		if group.LocationIssue == "" {
			continue
		}
		prior := matchingPreviousRoute(*group, saved, desired)
		result := RuleApplyResult{ProviderID: group.Provider.ID, Issue: group.LocationIssue}
		// Keep an active VPN in its original table, with its original mark,
		// TUN subnet and dynamic ipset. Only reuse an allocation compatible
		// with the current routing settings.
		if prior != nil && (!compatiblePreviousRoute(*prior, base) || m.loadReusableInstanceLocked(*prior, instances) == nil) {
			prior = nil
		}
		if prior != nil && !strings.HasSuffix(prior.configPath, ".blocked.json") {
			result.Location = prior.desired.Location
			result.Issue += fmt.Sprintf("; keeping previous VPN route %q", result.Location)
			if target := resolved[prior.desired.Key]; target != nil {
				for _, domain := range group.Domains {
					if !slices.Contains(target.Domains, domain) {
						target.Domains = append(target.Domains, domain)
					}
				}
			} else {
				held := *group
				held.Key, held.Location = prior.desired.Key, prior.desired.Location
				held.LocationIssue, held.previous = result.Issue, prior
				resolved[held.Key] = &held
			}
		} else {
			result.Blocked = true
			result.Issue += "; blocking traffic for this route"
			group.LocationIssue = result.Issue
			if prior != nil && prior.desired.Key == group.Key {
				group.previous = prior
			}
			resolved[group.Key] = group
		}
		outcomes[group.Key] = result
	}
	result := make([]desiredInstance, 0, len(resolved))
	for _, group := range resolved {
		result = append(result, *group)
	}
	sortDesired(result)
	return result, outcomes
}

// Stored routes have no rule IDs. A different provider/location pair cannot
// be inferred from shared destinations. Within the same provider we accept a
// different location only if it exclusively owned the complete set.
func matchingPreviousRoute(group desiredInstance, saved []applyPlan, desired []desiredInstance) *applyPlan {
	var candidate *applyPlan
	for i := range saved {
		plan := &saved[i]
		if plan.desired.Provider.ID != group.Provider.ID {
			continue
		}
		if plan.desired.Key == group.Key {
			return plan
		}
		if sameDomainSet(plan.desired.Domains, group.Domains) {
			candidate = plan
		}
	}
	if candidate == nil {
		return nil
	}
	overlaps := overlappingEntryMatcher(group.Domains)
	for i := range saved {
		other := &saved[i]
		if other != candidate && other.desired.Provider.ID == group.Provider.ID && slices.ContainsFunc(other.desired.Domains, overlaps) {
			return nil
		}
	}
	for _, other := range desired {
		if other.Provider.ID == group.Provider.ID && other.Key != group.Key &&
			slices.ContainsFunc(other.Domains, overlaps) {
			return nil
		}
	}
	return candidate
}

func overlappingEntryMatcher(entries []string) func(string) bool {
	zones, parents := make(map[string]bool), make(map[string]bool)
	var networks []netip.Prefix
	for _, entry := range entries {
		if prefix, ok := domains.ParseIPPrefix(entry); ok {
			networks = append(networks, prefix)
			continue
		}
		zones[entry] = true
		for parent := entry; parent != ""; _, parent, _ = strings.Cut(parent, ".") {
			parents[parent] = true
		}
	}
	return func(entry string) bool {
		if parents[entry] {
			return true
		}
		if prefix, ok := domains.ParseIPPrefix(entry); ok {
			return slices.ContainsFunc(networks, prefix.Overlaps)
		}
		for domain := entry; domain != ""; _, domain, _ = strings.Cut(domain, ".") {
			if zones[domain] {
				return true
			}
		}
		return false
	}
}

func compatiblePreviousRoute(plan applyPlan, base config.RoutingSettings) bool {
	index := plan.settings.TableNum - base.TableNum
	if index < 0 || index >= 256*64 {
		return false
	}
	hash := shortHash(plan.desired.Provider.ID + "\n" + plan.desired.Location)
	return plan.settings == deriveRoutingSettings(base, hash, index) &&
		bytes.Contains(plan.configData, []byte(strconv.Quote(tunAddress(index))))
}

func sameDomainSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	if slices.Equal(a, b) {
		return true
	}
	return slices.Equal(slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b)))
}
