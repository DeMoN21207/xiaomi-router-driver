package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"xiomi-router-driver/internal/events"
)

func (h *Handler) handleNumberedEvents(w http.ResponseWriter, r *http.Request, level string, limit, offset int) {
	if offset != 0 || r.URL.Query().Get("cursor") != "" {
		writeError(w, http.StatusBadRequest, errors.New("numbered pagination cannot be combined with offset or cursor"))
		return
	}
	number := 1
	if raw := strings.TrimSpace(r.URL.Query().Get("page")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			writeError(w, http.StatusBadRequest, errors.New("page must be a positive integer"))
			return
		}
		number = parsed
	}
	page, err := h.events.ListNumberedPageByLevel(level, limit, number, r.URL.Query().Get("snapshot"))
	if err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, events.ErrInvalidSnapshot) {
			code = http.StatusBadRequest
		}
		writeError(w, code, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": page.Events, "count": len(page.Events), "total": page.Total, "page": page.Page, "totalPages": page.TotalPages, "snapshot": page.Snapshot, "level": level, "levelCounts": page.LevelCounts})
}
