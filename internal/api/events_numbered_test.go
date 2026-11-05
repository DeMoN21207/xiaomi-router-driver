package api

import (
	"net/http"
	"net/url"
	"reflect"
	"testing"

	"xiomi-router-driver/internal/events"
)

type numberedEventsResponse struct {
	Events      []events.Event `json:"events"`
	Total       int            `json:"total"`
	Page        int            `json:"page"`
	TotalPages  int            `json:"totalPages"`
	Snapshot    string         `json:"snapshot"`
	LevelCounts map[string]int `json:"levelCounts"`
}

func TestNumberedEventsKeepPageBoundariesWhileNewEntriesArrive(t *testing.T) {
	handler, _, _, store := newAPITestHandler(t)
	for i := 0; i < 5; i++ {
		if _, err := store.Add("error", "test", "entry"); err != nil {
			t.Fatal(err)
		}
	}
	expected, _, err := store.List(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	first := requestJSON[numberedEventsResponse](t, handler, http.MethodGet, "/api/events?pagination=numbered&limit=2&page=1&level=error", nil, http.StatusOK)
	if first.Page != 1 || first.TotalPages != 3 || first.Snapshot == "" {
		t.Fatalf("missing numbered pagination: %+v", first)
	}
	if _, err := store.Add("error", "test", "arrived while browsing"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Add("info", "test", "another level"); err != nil {
		t.Fatal(err)
	}
	last := requestJSON[numberedEventsResponse](t, handler, http.MethodGet, "/api/events?pagination=numbered&limit=2&page=99&level=error&snapshot="+url.QueryEscape(first.Snapshot), nil, http.StatusOK)
	if last.Page != 3 || last.TotalPages != 3 || last.Total != 5 || !reflect.DeepEqual(last.Events, expected[4:]) || last.LevelCounts["error"] != 5 || last.LevelCounts["info"] != 0 {
		t.Fatalf("new entries shifted existing pages: %+v", last)
	}
	second := requestJSON[numberedEventsResponse](t, handler, http.MethodGet, "/api/events?pagination=numbered&limit=2&page=2&level=error&snapshot="+url.QueryEscape(first.Snapshot), nil, http.StatusOK)
	if !reflect.DeepEqual(second.Events, expected[2:4]) {
		t.Fatalf("direct page jump returned wrong events: %+v", second)
	}
	fresh := requestJSON[numberedEventsResponse](t, handler, http.MethodGet, "/api/events?pagination=numbered&limit=2&page=1&level=error", nil, http.StatusOK)
	if fresh.Total != 6 || fresh.Snapshot == first.Snapshot || fresh.LevelCounts["info"] != 1 {
		t.Fatalf("refresh did not include new events: %+v", fresh)
	}
}

func TestNumberedEventsValidatePageAndSnapshotAndHandleEmptyFilter(t *testing.T) {
	handler, _, _, store := newAPITestHandler(t)
	if _, err := store.Add("error", "test", "entry"); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"page=0", "page=-1", "page=nope", "page=1&snapshot=invalid!", "page=1&snapshot=e30", "page=1&cursor=old", "page=1&offset=2"} {
		requestJSON[map[string]any](t, handler, http.MethodGet, "/api/events?pagination=numbered&"+query, nil, http.StatusBadRequest)
	}
	first := requestJSON[numberedEventsResponse](t, handler, http.MethodGet, "/api/events?pagination=numbered&page=1&level=error", nil, http.StatusOK)
	requestJSON[map[string]any](t, handler, http.MethodGet, "/api/events?pagination=numbered&page=1&level=info&snapshot="+url.QueryEscape(first.Snapshot), nil, http.StatusBadRequest)
	empty := requestJSON[numberedEventsResponse](t, handler, http.MethodGet, "/api/events?pagination=numbered&page=20&level=warn", nil, http.StatusOK)
	if empty.Page != 1 || empty.TotalPages != 1 || empty.Total != 0 || len(empty.Events) != 0 {
		t.Fatalf("bad empty page: %+v", empty)
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	cleared := requestJSON[numberedEventsResponse](t, handler, http.MethodGet, "/api/events?pagination=numbered&page=20&level=error&snapshot="+url.QueryEscape(first.Snapshot), nil, http.StatusOK)
	if cleared.Page != 1 || cleared.Total != 0 || len(cleared.Events) != 0 {
		t.Fatalf("cleared log retained a phantom page: %+v", cleared)
	}
	if _, err := store.Add("error", "test", "new log after clear"); err != nil {
		t.Fatal(err)
	}
	oldSnapshot := requestJSON[numberedEventsResponse](t, handler, http.MethodGet, "/api/events?pagination=numbered&page=2&level=error&snapshot="+url.QueryEscape(first.Snapshot), nil, http.StatusOK)
	if oldSnapshot.Total != 0 || len(oldSnapshot.Events) != 0 {
		t.Fatalf("new journal leaked into an old snapshot: %+v", oldSnapshot)
	}
	fresh := requestJSON[numberedEventsResponse](t, handler, http.MethodGet, "/api/events?pagination=numbered&page=1&level=error", nil, http.StatusOK)
	if fresh.Total != 1 {
		t.Fatalf("new journal missing after refresh: %+v", fresh)
	}
}
