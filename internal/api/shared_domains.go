package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"xiomi-router-driver/internal/config"
	"xiomi-router-driver/internal/domains"
)

type sharedDomainsRequest struct {
	Entries   *[]string `json:"entries"`
	RuleID    string    `json:"ruleId"`
	UseShared *bool     `json:"useShared"`
}

func (h *Handler) handleSharedDomains(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeMethodNotAllowed(w, http.MethodPut)
		return
	}
	var req sharedDomainsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if (req.Entries == nil) == (req.UseShared == nil) {
		writeError(w, http.StatusBadRequest, errors.New("send entries or a route selection"))
		return
	}

	var previous config.SharedDomainList
	code := http.StatusInternalServerError
	saved, err := h.state.Mutate(func(state *config.State) error {
		previous = state.SharedDomains
		if req.Entries != nil {
			state.SharedDomains.Entries = domains.NormalizeEntries(*req.Entries)
		} else {
			index := findRuleIndex(state.Rules, req.RuleID)
			if index < 0 {
				code = http.StatusNotFound
				return errors.New("route not found")
			}
			ids := slices.Clone(state.SharedDomains.RuleIDs)
			if *req.UseShared && !slices.Contains(ids, req.RuleID) {
				if !state.Rules[index].Enabled {
					code = http.StatusBadRequest
					return errors.New("enable the route before using the shared list")
				}
				ids = append(ids, req.RuleID)
			} else if !*req.UseShared {
				ids = slices.DeleteFunc(ids, func(id string) bool { return id == req.RuleID })
			}
			state.SharedDomains.RuleIDs = ids
		}
		if err := validateActiveRuleEntries(*state); err != nil {
			code = http.StatusBadRequest
			return err
		}
		return nil
	})
	if err != nil {
		writeError(w, code, err)
		return
	}

	if len(previous.RuleIDs) != 0 || len(saved.SharedDomains.RuleIDs) != 0 {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), applyRequestTimeout)
		defer cancel()
		_, err = h.applyCurrentRulesWithRollback(ctx, func() (bool, error) {
			restored := false
			_, restoreErr := h.state.Mutate(func(state *config.State) error {
				if !slices.Equal(state.SharedDomains.Entries, saved.SharedDomains.Entries) ||
					!slices.Equal(state.SharedDomains.RuleIDs, saved.SharedDomains.RuleIDs) {
					return nil
				}
				state.SharedDomains = previous
				if err := validateActiveRuleEntries(*state); err != nil {
					return err
				}
				restored = true
				return nil
			})
			return restored, restoreErr
		})
		if err != nil {
			writeApplyError(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, saved.SharedDomains)
}

func validateSharedDomainRoutes(state config.State) error {
	if len(state.SharedDomains.RuleIDs) == 0 {
		return nil
	}
	if len(state.SharedDomains.Entries) == 0 {
		return errors.New("add entries to the shared list before assigning it to a route")
	}
	routes := state.SharedDomainRoutes()
	if len(routes) == 0 {
		return nil
	}
	providerID := routes[0].ProviderID
	provider, ok := findProviderByID(state.Providers, providerID)
	if !ok || provider.Type != config.ProviderTypeSubscription {
		return errors.New("the shared list requires a subscription route")
	}
	locations := make(map[string]bool, len(routes))
	for _, route := range routes {
		location := strings.ToLower(strings.TrimSpace(route.SelectedLocation))
		if route.ProviderID != providerID || location == "" || locations[location] {
			return errors.New("shared routes must use distinct locations in the same subscription")
		}
		locations[location] = true
	}
	for _, policy := range state.PriorityPolicies {
		if policy.Enabled && policy.ProviderID == providerID &&
			(policy.RuleID == "" || (policy.RuleID != routes[0].ID && slices.Contains(state.SharedDomains.RuleIDs, policy.RuleID))) {
			return errors.New("a separate priority policy already controls a shared route")
		}
	}
	return nil
}
