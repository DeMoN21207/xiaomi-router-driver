package events

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var ErrInvalidCursor = errors.New("invalid event cursor")

type Page struct {
	Events      []Event
	Total       int
	NextCursor  string
	LevelCounts map[string]int
}

type eventCursor struct {
	At    string `json:"at"`
	ID    string `json:"id"`
	Level string `json:"level"`
}

func (s *Store) ListPageByLevel(level string, limit int, cursor string) (Page, error) {
	level = strings.ToLower(strings.TrimSpace(level))
	var before eventCursor
	if cursor != "" {
		if len(cursor) > 512 {
			return Page{}, ErrInvalidCursor
		}
		data, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || json.Unmarshal(data, &before) != nil || before.ID == "" || len(before.ID) > 128 || before.Level != level {
			return Page{}, ErrInvalidCursor
		}
		if _, err := time.Parse(time.RFC3339, before.At); err != nil {
			return Page{}, ErrInvalidCursor
		}
	}
	if limit < 1 {
		limit = 25
	}
	if limit > 200 {
		limit = 200
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureReadyLocked(); err != nil {
		return Page{}, err
	}
	page := Page{Events: make([]Event, 0, limit), LevelCounts: map[string]int{"info": 0, "warn": 0, "error": 0}}
	counts, err := s.db.Query(`SELECT level,COUNT(*) FROM events GROUP BY level`)
	if err != nil {
		return Page{}, err
	}
	for counts.Next() {
		var name string
		var count int
		if err := counts.Scan(&name, &count); err != nil {
			counts.Close()
			return Page{}, err
		}
		page.LevelCounts[name] = count
		if level == "" || name == level {
			page.Total += count
		}
	}
	err = counts.Err()
	counts.Close()
	if err != nil {
		return Page{}, err
	}
	conditions := []string{}
	args := []any{}
	if level != "" {
		conditions = append(conditions, "level = ?")
		args = append(args, level)
	}
	if cursor != "" {
		conditions = append(conditions, "(occurred_at < ? OR (occurred_at = ? AND id < ?))")
		args = append(args, before.At, before.At, before.ID)
	}
	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}
	args = append(args, limit+1)
	rows, err := s.db.Query(`SELECT id,level,kind,message,occurred_at FROM events`+where+` ORDER BY occurred_at DESC,id DESC LIMIT ?`, args...)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var event Event
		if err := rows.Scan(&event.ID, &event.Level, &event.Kind, &event.Message, &event.OccurredAt); err != nil {
			return Page{}, err
		}
		page.Events = append(page.Events, event)
	}
	if err := rows.Err(); err != nil {
		return Page{}, err
	}
	if len(page.Events) > limit {
		page.Events = page.Events[:limit]
		last := page.Events[len(page.Events)-1]
		data, _ := json.Marshal(eventCursor{At: last.OccurredAt, ID: last.ID, Level: level})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(data)
	}
	return page, nil
}
