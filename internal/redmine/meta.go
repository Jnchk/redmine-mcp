package redmine

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// Meta caches the small lookup tables used for name -> id resolution.
type Meta struct {
	c   *Client
	ttl time.Duration

	mu sync.Mutex
	e  map[string]entry
}

type entry struct {
	at time.Time
	v  any
}

func NewMeta(c *Client, ttl time.Duration) *Meta {
	return &Meta{c: c, ttl: ttl, e: map[string]entry{}}
}

// Invalidate drops every cached lookup, e.g. after a 404 on a resolved id.
func (m *Meta) Invalidate() {
	m.mu.Lock()
	m.e = map[string]entry{}
	m.mu.Unlock()
}

func cacheGet[T any](m *Meta, key string, load func() (T, error)) (T, error) {
	m.mu.Lock()
	if e, ok := m.e[key]; ok && time.Since(e.at) < m.ttl {
		if v, ok := e.v.(T); ok {
			m.mu.Unlock()
			return v, nil
		}
	}
	m.mu.Unlock()

	v, err := load()
	if err != nil {
		var zero T
		return zero, err
	}
	m.mu.Lock()
	m.e[key] = entry{at: time.Now(), v: v}
	m.mu.Unlock()
	return v, nil
}

func collect[T any](ctx context.Context, c *Client, path, key string) ([]T, error) {
	var out []T
	err := c.each(ctx, path, key, func(raw json.RawMessage) error {
		var v T
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		out = append(out, v)
		return nil
	})
	return out, err
}

func (m *Meta) Projects(ctx context.Context) ([]Project, error) {
	return cacheGet(m, "projects", func() ([]Project, error) {
		return collect[Project](ctx, m.c, "projects.json", "projects")
	})
}

func (m *Meta) Trackers(ctx context.Context) ([]Ref, error) {
	return cacheGet(m, "trackers", func() ([]Ref, error) {
		return collect[Ref](ctx, m.c, "trackers.json", "trackers")
	})
}

func (m *Meta) Statuses(ctx context.Context) ([]Status, error) {
	return cacheGet(m, "statuses", func() ([]Status, error) {
		return collect[Status](ctx, m.c, "issue_statuses.json", "issue_statuses")
	})
}

func (m *Meta) Priorities(ctx context.Context) ([]Ref, error) {
	return cacheGet(m, "priorities", func() ([]Ref, error) {
		return collect[Ref](ctx, m.c, "enumerations/issue_priorities.json", "issue_priorities")
	})
}

func (m *Meta) Activities(ctx context.Context) ([]Ref, error) {
	return cacheGet(m, "activities", func() ([]Ref, error) {
		return collect[Ref](ctx, m.c, "enumerations/time_entry_activities.json", "time_entry_activities")
	})
}

// Users needs an admin API key. A 403 is returned as-is so callers can degrade.
func (m *Meta) Users(ctx context.Context) ([]User, error) {
	return cacheGet(m, "users", func() ([]User, error) {
		return collect[User](ctx, m.c, "users.json", "users")
	})
}
