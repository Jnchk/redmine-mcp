package tools

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"redmine-mcp/internal/redmine"
)

// fakeTime serves the endpoints the time tools touch.
func fakeTime(t *testing.T) (*httptest.Server, *url.Values, *map[string]map[string]any) {
	t.Helper()
	lastQuery := &url.Values{}
	lastBody := &map[string]map[string]any{}

	mux := http.NewServeMux()
	mux.HandleFunc("/users/current.json", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"user":{"id":9,"login":"alice","firstname":"Alice","lastname":"Zhang"}}`)
	})
	mux.HandleFunc("/users.json", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"users":[{"id":9,"login":"alice","firstname":"Alice","lastname":"Zhang"}],"total_count":1}`)
	})
	mux.HandleFunc("/enumerations/time_entry_activities.json", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"time_entry_activities":[{"id":4,"name":"Design"},{"id":9,"name":"Development","is_default":true}]}`)
	})
	mux.HandleFunc("/time_entries.json", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			json.NewDecoder(r.Body).Decode(lastBody)
			w.WriteHeader(http.StatusCreated)
			io.WriteString(w, `{"time_entry":{"id":5,"hours":2.5,"spent_on":"2026-11-16","activity":{"id":9,"name":"Development"}}}`)
			return
		}
		*lastQuery = r.URL.Query()
		if r.URL.Query().Get("user_id") == "9" {
			io.WriteString(w, `{"time_entries":[{"id":5,"hours":2.5,"spent_on":"2026-11-16","issue":{"id":123},"user":{"id":9,"name":"Alice Zhang"},"activity":{"id":9,"name":"Development"},"comments":"fixing login"}],"total_count":1}`)
			return
		}
		io.WriteString(w, `{"time_entries":[],"sync":"none","total_count":0}`)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, lastQuery, lastBody
}

func timeDeps(t *testing.T) *Deps {
	t.Helper()
	srv, _, _ := fakeTime(t)
	c, err := redmine.New(srv.URL, "k", time.Second, false, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return &Deps{C: c, M: redmine.NewMeta(c, time.Minute), DefaultLimit: 25, TextFormat: "textile"}
}

func TestLogTimeUsesDefaultActivity(t *testing.T) {
	c := connect(t, timeDeps(t))

	got, isErr := call(t, c, "log_time", map[string]any{"issue_id": 123, "hours": 2.5})
	if isErr {
		t.Fatalf("log_time error: %s", got)
	}
	for _, want := range []string{"#123 logged 2.5h on 2026-11-16", "(Development)", "/issues/123"} {
		if !strings.Contains(got, want) {
			t.Fatalf("log_time = %q, missing %q", got, want)
		}
	}
}

func TestLogTimeValidation(t *testing.T) {
	c := connect(t, timeDeps(t))

	for _, args := range []map[string]any{
		{"issue_id": 123, "hours": 0},
		{"hours": 1},
	} {
		got, isErr := call(t, c, "log_time", args)
		if !isErr {
			t.Fatalf("args %v accepted: %q", args, got)
		}
	}
}

func TestListTimeEntriesFormatting(t *testing.T) {
	srv, lastQuery, _ := fakeTime(t)
	c, _ := redmine.New(srv.URL, "k", time.Second, false, 1<<20)
	d := &Deps{C: c, M: redmine.NewMeta(c, time.Minute), DefaultLimit: 25}
	cs := connect(t, d)

	got, isErr := call(t, cs, "list_time_entries", map[string]any{})
	if isErr || !strings.Contains(got, "no time entries matched") {
		t.Fatalf("empty list = %q (error=%v)", got, isErr)
	}
	if !strings.Contains(got, "entries visible to alice") {
		t.Fatalf("empty list footer = %q", got)
	}

	got, isErr = call(t, cs, "list_time_entries", map[string]any{"user": "alice"})
	if isErr {
		t.Fatalf("list_time_entries error: %s", got)
	}
	if lastQuery.Get("user_id") != "9" {
		t.Fatalf("user_id = %q, want 9", lastQuery.Get("user_id"))
	}
	for _, want := range []string{"# id | spent_on | user | activity | hours | issue | comments", "5 | 2026-11-16 | Alice Zhang | Development | 2.5 | #123", "# filtered by user Alice Zhang"} {
		if !strings.Contains(got, want) {
			t.Fatalf("list_time_entries = %q, missing %q", got, want)
		}
	}
}

func TestListProjectsFilter(t *testing.T) {
	rs, _ := fakeRedmine(t)
	c, _ := redmine.New(rs.URL, "k", time.Second, false, 1<<20)
	cs := connect(t, &Deps{C: c, M: redmine.NewMeta(c, time.Minute), DefaultLimit: 25})

	got, isErr := call(t, cs, "list_projects", map[string]any{"q": "web"})
	if isErr || !strings.Contains(got, "1 | website | Website | active") {
		t.Fatalf("matching project = %q (error=%v)", got, isErr)
	}
	got, isErr = call(t, cs, "list_projects", map[string]any{"q": "zzz"})
	if isErr || !strings.Contains(got, "no projects matched") {
		t.Fatalf("non-matching project = %q (error=%v)", got, isErr)
	}
}

func TestListMetadataKinds(t *testing.T) {
	rs, _ := fakeRedmine(t)
	c, _ := redmine.New(rs.URL, "k", time.Second, false, 1<<20)
	cs := connect(t, &Deps{C: c, M: redmine.NewMeta(c, time.Minute), DefaultLimit: 25})

	got, isErr := call(t, cs, "list_metadata", map[string]any{"kind": "statuses"})
	if isErr || !strings.Contains(got, "# id | name | closed | default") || !strings.Contains(got, "5 | Closed | yes") {
		t.Fatalf("statuses = %q (error=%v)", got, isErr)
	}

	got, isErr = call(t, cs, "list_metadata", map[string]any{"kind": "colours"})
	if !isErr || !strings.Contains(got, "unknown kind") {
		t.Fatalf("unknown kind = %q (error=%v)", got, isErr)
	}
}

func TestListUsersWithoutAdminDegrades(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/users.json" {
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `{"errors":["Forbidden"]}`)
			return
		}
		io.WriteString(w, `{}`)
	}))
	t.Cleanup(srv.Close)

	c, _ := redmine.New(srv.URL, "k", time.Second, false, 1<<20)
	cs := connect(t, &Deps{C: c, M: redmine.NewMeta(c, time.Minute), DefaultLimit: 25})

	got, isErr := call(t, cs, "list_metadata", map[string]any{"kind": "users"})
	if !isErr || !strings.Contains(got, "admin-only") {
		t.Fatalf("users = %q (error=%v)", got, isErr)
	}
}

func TestRawClip(t *testing.T) {
	if got := rawClip([]byte("{}")); got != "{}" {
		t.Fatalf("small raw changed: %q", got)
	}
	big := []byte(strings.Repeat("x", 200<<10))
	got := rawClip(big)
	if !strings.Contains(got, "raw JSON truncated") || len(got) > 132<<10 {
		t.Fatalf("raw not clipped: len=%d", len(got))
	}
}
