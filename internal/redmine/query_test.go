package redmine

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestIssueQueryValues(t *testing.T) {
	q := IssueQuery{
		ProjectID:    1,
		StatusID:     "open",
		AssignedToID: 9,
		TrackerID:    2,
		PriorityID:   3,
		Subject:      "login",
		UpdatedSince: "2026-11-01",
		QueryID:      7,
		Sort:         "updated_on:desc",
		Limit:        20,
		Offset:       40,
	}
	got := q.values()
	want := url.Values{
		"project_id":     {"1"},
		"status_id":      {"open"},
		"assigned_to_id": {"9"},
		"tracker_id":     {"2"},
		"priority_id":    {"3"},
		"subject":        {"~login"},       // substring operator, not equality
		"updated_on":     {">=2026-11-01"}, // relative operator
		"query_id":       {"7"},
		"sort":           {"updated_on:desc"},
		"limit":          {"20"},
		"offset":         {"40"},
	}
	for k, v := range want {
		if got.Get(k) != v[0] {
			t.Errorf("%s = %q, want %q", k, got.Get(k), v[0])
		}
	}
}

func TestIssueQueryOmitsUnset(t *testing.T) {
	got := IssueQuery{StatusID: "closed"}.values()
	for _, k := range []string{"project_id", "tracker_id", "priority_id", "limit", "offset", "query_id"} {
		if got.Has(k) {
			t.Errorf("unexpected %s=%q", k, got.Get(k))
		}
	}
}

func TestTimeQueryValues(t *testing.T) {
	got := TimeQuery{ProjectID: 1, UserID: 9, IssueID: 123, From: "2026-11-01", To: "2026-11-30", Limit: 10}.values()
	want := map[string]string{
		"project_id": "1", "user_id": "9", "issue_id": "123",
		"from": "2026-11-01", "to": "2026-11-30", "limit": "10",
	}
	for k, v := range want {
		if got.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, got.Get(k), v)
		}
	}
}

func TestSnippetStripsHTMLAndBounds(t *testing.T) {
	if got := snippet("<html><body><h1>Not Found</h1></body></html>"); strings.Contains(got, "<") {
		t.Fatalf("snippet kept tags: %q", got)
	}
	long := strings.Repeat("x", 400)
	got := snippet(long)
	if len([]rune(got)) > 161 || !strings.HasSuffix(got, "…") {
		t.Fatalf("snippet not bounded: len=%d", len([]rune(got)))
	}
}

func TestErrorMessages(t *testing.T) {
	cases := []struct {
		err  *Error
		want []string
	}{
		{&Error{Status: 401, Body: `{"error":"nope"}`}, []string{"401", "authentication failed"}},
		{&Error{Status: 403, Body: ""}, []string{"403", "permission denied"}},
		{&Error{Status: 404, Body: "gone"}, []string{"404", "not found", "gone"}},
		{&Error{Status: 422, Errors: []string{"Subject can't be blank"}}, []string{"422", "validation failed", "Subject can't be blank"}},
		{&Error{Status: 500, Body: "<html>boom</html>"}, []string{"500", "boom"}},
	}
	for _, tc := range cases {
		msg := tc.err.Error()
		for _, want := range tc.want {
			if !strings.Contains(msg, want) {
				t.Errorf("%d message %q missing %q", tc.err.Status, msg, want)
			}
		}
	}
	// A 401 body may contain auth detail; it must not be echoed.
	if msg := (&Error{Status: 401, Body: "api_key=secret"}).Error(); strings.Contains(msg, "secret") {
		t.Fatalf("401 body leaked: %q", msg)
	}
}

func TestMetaCacheAndInvalidate(t *testing.T) {
	hits := 0
	srv, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte(`{"trackers":[{"id":1,"name":"Bug"}]}`))
	})
	_ = srv

	m := NewMeta(c, time.Minute)
	for i := 0; i < 3; i++ {
		list, err := m.Trackers(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 1 || list[0].Name != "Bug" {
			t.Fatalf("trackers = %#v", list)
		}
	}
	if hits != 1 {
		t.Fatalf("HTTP hits = %d, want 1 (cache miss)", hits)
	}
	m.Invalidate()
	if _, err := m.Trackers(t.Context()); err != nil {
		t.Fatal(err)
	}
	if hits != 2 {
		t.Fatalf("HTTP hits after Invalidate = %d, want 2", hits)
	}
}

func TestMetaCacheExpires(t *testing.T) {
	hits := 0
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte(`{"trackers":[]}`))
	})
	m := NewMeta(c, 10*time.Millisecond)
	if _, err := m.Trackers(t.Context()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if _, err := m.Trackers(t.Context()); err != nil {
		t.Fatal(err)
	}
	if hits != 2 {
		t.Fatalf("HTTP hits = %d, want 2 after TTL expiry", hits)
	}
}
