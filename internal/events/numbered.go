package events

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

var ErrInvalidSnapshot = errors.New("invalid event page snapshot")

type NumberedPage struct {
	Events      []Event
	Total       int
	Page        int
	TotalPages  int
	Snapshot    string
	LevelCounts map[string]int
}

type pageSnapshot struct {
	Version  int    `json:"v"`
	MaxRowID int64  `json:"maxRowId"`
	AnchorID string `json:"anchorId"`
	Level    string `json:"level"`
}

// Freeze the insertion boundary so new log entries cannot shift numbered pages,
// including entries with the same second timestamp and randomly generated IDs.
func (s *Store) ListNumberedPageByLevel(level string, limit, number int, snapshot string) (NumberedPage, error) {
	level = strings.ToLower(strings.TrimSpace(level))
	var upper pageSnapshot
	if snapshot != "" {
		if len(snapshot) > 512 {
			return NumberedPage{}, ErrInvalidSnapshot
		}
		data, err := base64.RawURLEncoding.DecodeString(snapshot)
		if err != nil || json.Unmarshal(data, &upper) != nil || upper.Version != 1 || upper.MaxRowID < 0 || upper.Level != level || len(upper.AnchorID) > 128 || (upper.MaxRowID > 0 && upper.AnchorID == "") {
			return NumberedPage{}, ErrInvalidSnapshot
		}
	}
	if limit < 1 {
		limit = 25
	}
	if limit > 200 {
		limit = 200
	}
	if number < 1 {
		number = 1
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureReadyLocked(); err != nil {
		return NumberedPage{}, err
	}
	if snapshot == "" {
		upper.Version = 1
		upper.Level = level
		if err := s.db.QueryRow(`SELECT COALESCE(MAX(rowid),0) FROM events`).Scan(&upper.MaxRowID); err != nil {
			return NumberedPage{}, err
		}
		if upper.MaxRowID > 0 {
			if err := s.db.QueryRow(`SELECT id FROM events WHERE rowid=?`, upper.MaxRowID).Scan(&upper.AnchorID); err != nil {
				return NumberedPage{}, err
			}
		}
		data, _ := json.Marshal(upper)
		snapshot = base64.RawURLEncoding.EncodeToString(data)
	} else if upper.MaxRowID > 0 {
		var anchorID string
		err := s.db.QueryRow(`SELECT id FROM events WHERE rowid=?`, upper.MaxRowID).Scan(&anchorID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return NumberedPage{}, err
		}
		// Clear can restart SQLite rowids. An old journal snapshot remains empty
		// until Refresh, rather than admitting records from the new journal.
		if errors.Is(err, sql.ErrNoRows) || anchorID != upper.AnchorID {
			upper.MaxRowID = 0
		}
	}
	page := NumberedPage{Events: make([]Event, 0, limit), Page: number, Snapshot: snapshot, LevelCounts: map[string]int{"info": 0, "warn": 0, "error": 0}}
	counts, err := s.db.Query(`SELECT level,COUNT(*) FROM events WHERE rowid<=? GROUP BY level`, upper.MaxRowID)
	if err != nil {
		return NumberedPage{}, err
	}
	for counts.Next() {
		var name string
		var count int
		if err := counts.Scan(&name, &count); err != nil {
			counts.Close()
			return NumberedPage{}, err
		}
		page.LevelCounts[name] = count
		if level == "" || name == level {
			page.Total += count
		}
	}
	err = counts.Err()
	counts.Close()
	if err != nil {
		return NumberedPage{}, err
	}
	page.TotalPages = max(1, (page.Total+limit-1)/limit)
	page.Page = min(page.Page, page.TotalPages)
	where := "rowid<=?"
	args := []any{upper.MaxRowID}
	if level != "" {
		where += " AND level=?"
		args = append(args, level)
	}
	args = append(args, limit, (page.Page-1)*limit)
	rows, err := s.db.Query(`SELECT id,level,kind,message,occurred_at FROM events WHERE `+where+` ORDER BY occurred_at DESC,id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return NumberedPage{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var event Event
		if err := rows.Scan(&event.ID, &event.Level, &event.Kind, &event.Message, &event.OccurredAt); err != nil {
			return NumberedPage{}, err
		}
		page.Events = append(page.Events, event)
	}
	return page, rows.Err()
}
