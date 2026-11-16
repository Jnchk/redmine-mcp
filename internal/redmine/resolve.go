package redmine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Kind selects which lookup table a name is resolved against.
type Kind int

const (
	KProject Kind = iota
	KTracker
	KStatus
	KPriority
	KActivity
	KUser
)

func (k Kind) String() string {
	switch k {
	case KProject:
		return "project"
	case KTracker:
		return "tracker"
	case KStatus:
		return "status"
	case KPriority:
		return "priority"
	case KActivity:
		return "activity"
	case KUser:
		return "user"
	}
	return "value"
}

// Named is a resolved id plus the names that matched it.
type Named struct {
	ID      int
	Name    string
	Aliases []string
}

// Resolve turns a human name into an id. Numbers always pass through.
func (m *Meta) Resolve(ctx context.Context, k Kind, in string) (Named, error) {
	s := strings.TrimSpace(in)
	if s == "" {
		return Named{}, fmt.Errorf("%s is required", k)
	}
	if id, err := strconv.Atoi(s); err == nil {
		return Named{ID: id}, nil
	}
	if k == KUser && strings.EqualFold(s, "me") {
		u, err := m.c.CurrentUser(ctx)
		if err != nil {
			return Named{}, err
		}
		return Named{ID: u.ID, Name: u.Name()}, nil
	}

	list, err := m.named(ctx, k)
	if err != nil {
		if k == KUser && IsStatus(err, 403) {
			return Named{}, fmt.Errorf("cannot resolve user names: the API key lacks permission to list users; pass a numeric user id")
		}
		return Named{}, err
	}
	hits := matchNamed(list, s)
	switch len(hits) {
	case 0:
		return Named{}, fmt.Errorf("unknown %s %q. Available: %s. Use list_metadata for the full list", k, s, displays(list, 20))
	case 1:
		return hits[0], nil
	default:
		return Named{}, fmt.Errorf("ambiguous %s %q: %d matches — %s. Pass an id or a longer name", k, s, len(hits), displays(hits, 10))
	}
}

// named lists the resolvable names for one kind. Archived projects are excluded.
func (m *Meta) named(ctx context.Context, k Kind) ([]Named, error) {
	var out []Named
	switch k {
	case KProject:
		ps, err := m.Projects(ctx)
		if err != nil {
			return nil, err
		}
		for _, p := range ps {
			if p.Status == 5 || p.Status == 9 {
				continue
			}
			out = append(out, Named{ID: p.ID, Name: p.Name, Aliases: []string{p.Identifier}})
		}
	case KTracker:
		rs, err := m.Trackers(ctx)
		if err != nil {
			return nil, err
		}
		for _, r := range rs {
			out = append(out, Named{ID: r.ID, Name: r.Name})
		}
	case KStatus:
		ss, err := m.Statuses(ctx)
		if err != nil {
			return nil, err
		}
		for _, s := range ss {
			out = append(out, Named{ID: s.ID, Name: s.Name})
		}
	case KPriority:
		rs, err := m.Priorities(ctx)
		if err != nil {
			return nil, err
		}
		for _, r := range rs {
			out = append(out, Named{ID: r.ID, Name: r.Name})
		}
	case KActivity:
		rs, err := m.Activities(ctx)
		if err != nil {
			return nil, err
		}
		for _, r := range rs {
			out = append(out, Named{ID: r.ID, Name: r.Name})
		}
	case KUser:
		us, err := m.Users(ctx)
		if err != nil {
			return nil, err
		}
		for _, u := range us {
			out = append(out, Named{ID: u.ID, Name: u.Name(), Aliases: []string{u.Login}})
		}
	}
	return out, nil
}

// matchNamed returns the hits at the best rank: exact, then prefix, then substring.
func matchNamed(list []Named, s string) []Named {
	needle := strings.ToLower(s)
	rank := func(n Named) int {
		names := append([]string{n.Name}, n.Aliases...)
		for _, v := range names {
			if strings.ToLower(v) == needle {
				return 3
			}
		}
		for _, v := range names {
			if strings.HasPrefix(strings.ToLower(v), needle) {
				return 2
			}
		}
		for _, v := range names {
			if strings.Contains(strings.ToLower(v), needle) {
				return 1
			}
		}
		return 0
	}

	best := 0
	var hits []Named
	for _, n := range list {
		r := rank(n)
		switch {
		case r == 0 || r < best:
		case r > best:
			best, hits = r, []Named{n}
		default:
			hits = append(hits, n)
		}
	}
	return hits
}

func displays(list []Named, n int) string {
	if len(list) > n {
		list = list[:n]
	}
	parts := make([]string, len(list))
	for i, v := range list {
		parts[i] = fmt.Sprintf("%q (%d)", v.Name, v.ID)
	}
	return strings.Join(parts, ", ")
}
