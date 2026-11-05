package api

import (
	"net/http"
	"testing"

	"xiomi-router-driver/internal/events"
)

func TestEventsCursorPaginationContract(t *testing.T) {
	handler, _, _, _ := newAPITestHandler(t)
	for i := 0; i < 5; i++ {
		if _, err := handler.events.Add("error", "test", "entry"); err != nil {
			t.Fatal(err)
		}
	}
	type page struct {
		Events     []events.Event `json:"events"`
		NextCursor string         `json:"nextCursor"`
		HasMore    bool           `json:"hasMore"`
		Total      int            `json:"total"`
	}
	first := requestJSON[page](t, handler, http.MethodGet, "/api/events?pagination=cursor&level=error&limit=2", nil, http.StatusOK)
	if !first.HasMore || first.NextCursor == "" || len(first.Events) != 2 {
		t.Fatalf("missing cursor page metadata: %+v", first)
	}
	second := requestJSON[page](t, handler, http.MethodGet, "/api/events?pagination=cursor&level=error&limit=2&cursor="+first.NextCursor, nil, http.StatusOK)
	for _, a := range first.Events {
		for _, b := range second.Events {
			if a.ID == b.ID {
				t.Fatal("event repeated between pages")
			}
		}
	}
	_ = requestJSON[map[string]string](t, handler, http.MethodGet, "/api/events?pagination=cursor&cursor=invalid!", nil, http.StatusBadRequest)
	_ = requestJSON[map[string]string](t, handler, http.MethodGet, "/api/events?pagination=cursor&offset=2", nil, http.StatusBadRequest)
	_ = requestJSON[page](t, handler, http.MethodGet, "/api/events?level=error&limit=2&offset=2", nil, http.StatusOK)
}
