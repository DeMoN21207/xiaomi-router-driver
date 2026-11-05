package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"xiomi-router-driver/internal/automation"
)

func (h *Handler) handleProviderCandidatesCheck(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		writeMethodNotAllowed(w, http.MethodPost)
		return
	}
	var req struct {
		Locations []string `json:"locations"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	state, err := h.state.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	provider, found := findProviderByID(state.Providers, id)
	if !found {
		writeError(w, http.StatusNotFound, errors.New("provider not found"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	results, err := automation.ProbeSubscriptionCandidates(ctx, provider, h.dataDir, req.Locations)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"candidates": results})
}
