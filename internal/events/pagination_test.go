package events

import (
	"fmt"
	"path/filepath"
	"slices"
	"testing"
)

func TestCursorPaginationDoesNotRepeatOrSkipOlderEventsWhenLogChanges(t *testing.T) {
	db := openEventsTestDB(t)
	store := NewStore(db, filepath.Join(t.TempDir(), "events.json"))
	if _, _, err := store.List(1, 0); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 8; i++ {
		if _, err := db.Exec(`INSERT INTO events VALUES (?, 'error', 'test', 'entry', '2026-11-05T10:00:00Z')`, fmt.Sprintf("%02d", i)); err != nil {
			t.Fatal(err)
		}
	}
	first, err := store.ListPageByLevel("error", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Events) != 3 || first.NextCursor == "" {
		t.Fatalf("no next page: %+v", first)
	}
	if _, err := db.Exec(`INSERT INTO events VALUES ('09', 'error', 'test', 'new', '2026-11-05T11:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM events WHERE id='02'`); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, event := range first.Events {
		ids = append(ids, event.ID)
	}
	cursor := first.NextCursor
	for cursor != "" {
		page, err := store.ListPageByLevel("error", 3, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range page.Events {
			ids = append(ids, event.ID)
		}
		cursor = page.NextCursor
	}
	if !slices.Equal(ids, []string{"08", "07", "06", "05", "04", "03", "01"}) {
		t.Fatalf("repeated or skipped events: %v", ids)
	}
}

func TestCursorPaginationRejectsInvalidOrDifferentFilterCursor(t *testing.T) {
	store := NewStore(openEventsTestDB(t), "")
	for i := 0; i < 4; i++ {
		if _, err := store.Add("error", "test", "entry"); err != nil {
			t.Fatal(err)
		}
	}
	page, err := store.ListPageByLevel("error", 2, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ListPageByLevel("info", 2, page.NextCursor); err == nil {
		t.Fatal("cursor from another filter was accepted")
	}
	if _, err = store.ListPageByLevel("error", 2, "invalid!"); err == nil {
		t.Fatal("invalid cursor was accepted")
	}
}
