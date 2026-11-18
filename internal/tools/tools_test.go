package tools

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"redmine-mcp/internal/redmine"
	"redmine-mcp/internal/upload"
)

// uploadsSeen counts /uploads.json hits; tests are sequential, so a package counter is enough.
var uploadsSeen int32

// lastUpdate keeps the last PUT /issues/123.json body for assertions.
var lastUpdate string

// lastCreate keeps the last POST /issues.json body for assertions.
var lastCreate string

// fakeRedmine is a minimal 3.4-shaped server for the tool layer.
func fakeRedmine(t *testing.T) (*httptest.Server, *string) {
	t.Helper()
	lastStatus := new(string)
	mux := http.NewServeMux()

	mux.HandleFunc("/users/current.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"user":{"id":9,"login":"alice","firstname":"Alice","lastname":"Zhang","admin":true}}`))
	})
	mux.HandleFunc("/users.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"users":[{"id":9,"login":"alice","firstname":"Alice","lastname":"Zhang"},{"id":10,"login":"bob","firstname":"Bob"}],"total_count":2}`))
	})
	mux.HandleFunc("/projects.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"projects":[{"id":1,"name":"Website","identifier":"website","status":1}],"total_count":1}`))
	})
	mux.HandleFunc("/issue_statuses.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"issue_statuses":[{"id":1,"name":"New","is_default":true},{"id":2,"name":"In Progress"},{"id":5,"name":"Closed","is_closed":true}]}`))
	})
	mux.HandleFunc("/trackers.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"trackers":[{"id":1,"name":"Bug"}]}`))
	})
	mux.HandleFunc("/enumerations/issue_priorities.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"issue_priorities":[{"id":2,"name":"Normal","is_default":true}]}`))
	})
	mux.HandleFunc("/enumerations/time_entry_activities.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"time_entry_activities":[{"id":9,"name":"Development","is_default":true}]}`))
	})

	mux.HandleFunc("/uploads.json", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&uploadsSeen, 1)
		w.Write([]byte(`{"upload":{"token":"tok-1"}}`))
	})

	mux.HandleFunc("/issues.json", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			lastCreate = string(body)
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"issue":{"id":124,"subject":"new one","project":{"id":1,"name":"Website"},"tracker":{"id":1,"name":"Bug"}}}`))
			return
		}
		if got := r.URL.Query().Get("project_id"); got != "1" {
			t.Errorf("project_id = %q, want 1", got)
		}
		*lastStatus = r.URL.Query().Get("status_id")
		w.Write([]byte(`{"issues":[
			{"id":123,"subject":"login fails","status":{"id":2,"name":"In Progress"},"tracker":{"id":1,"name":"Bug"},"priority":{"id":2,"name":"Normal"},"assigned_to":{"id":9,"name":"Alice Zhang"},"updated_on":"2026-11-14T08:12:19Z"},
			{"id":125,"subject":"add export","status":{"id":1,"name":"New"},"tracker":{"id":1,"name":"Bug"},"priority":{"id":2,"name":"Normal"},"updated_on":"2026-11-13T17:40:00Z"}],
			"total_count":2,"offset":0,"limit":25}`))
	})

	mux.HandleFunc("/issues/123.json", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			body, _ := io.ReadAll(r.Body)
			lastUpdate = string(body)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Write([]byte(`{"issue":{"id":123,"subject":"login fails","description":"body text",
			"tracker":{"id":1,"name":"Bug"},
			"status":{"id":2,"name":"In Progress"},"priority":{"id":2,"name":"Normal"},
			"assigned_to":{"id":10,"name":"Bob"},
			"created_on":"2026-11-10T09:00:00Z","updated_on":"2026-11-14T08:12:19Z","done_ratio":40,
			"journals":[{"id":1,"user":{"id":9,"name":"Alice Zhang"},"notes":"first note","created_on":"2026-11-12T10:00:00Z"}],
			"attachments":[{"id":77,"filename":"shot.png","filesize":2048}],
			"relations":[{"id":1,"issue_id":123,"issue_to_id":456,"relation_type":"relates"}]}}`))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, lastStatus
}

func connect(t *testing.T, d *Deps) *mcp.ClientSession {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	Register(s, d)

	ct, st := mcp.NewInMemoryTransports()
	ss, err := s.Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "c"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if len(res.Content) == 0 {
		t.Fatalf("%s: no content", name)
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("%s: content type %T", name, res.Content[0])
	}
	return tc.Text, res.IsError
}

func TestReadTools(t *testing.T) {
	rs, lastStatus := fakeRedmine(t)
	c, err := redmine.New(rs.URL, "k", time.Second, false, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	d := &Deps{C: c, M: redmine.NewMeta(c, time.Minute), DefaultLimit: 25, TextFormat: "textile"}
	cs := connect(t, d)

	got, isErr := call(t, cs, "whoami", nil)
	if isErr || !strings.Contains(got, "login: alice (Alice Zhang)") || !strings.Contains(got, "user names: available") {
		t.Fatalf("whoami = %q (error=%v)", got, isErr)
	}
	if !strings.Contains(got, "staging: disabled") {
		t.Fatalf("whoami without staging = %q", got)
	}

	got, isErr = call(t, cs, "search_issues", map[string]any{"project": "website", "status": "In Progress"})
	if isErr {
		t.Fatalf("search_issues error: %s", got)
	}
	if *lastStatus != "2" {
		t.Fatalf("status_id sent = %q, want 2", *lastStatus)
	}
	for _, want := range []string{"# id | status | tracker", "123 | In Progress | Bug", "# returned 2 of 2 (offset 0, limit 25)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("search_issues = %q, missing %q", got, want)
		}
	}

	got, isErr = call(t, cs, "get_issue", map[string]any{"id": 123})
	if isErr {
		t.Fatalf("get_issue error: %s", got)
	}
	for _, want := range []string{"#123 login fails", "project: -", "author: -", "[description]\nbody text", "shot.png", "relates #456", "first note"} {
		if !strings.Contains(got, want) {
			t.Fatalf("get_issue = %q, missing %q", got, want)
		}
	}

	got, _ = call(t, cs, "search_issues", map[string]any{"raw": true, "project": "website"})
	if !strings.HasPrefix(strings.TrimSpace(got), `{"issues"`) {
		t.Fatalf("raw = %q", got)
	}

	got, isErr = call(t, cs, "search_issues", map[string]any{"project": "nope"})
	if !isErr || !strings.Contains(got, "unknown project") {
		t.Fatalf("unknown project = %q (error=%v)", got, isErr)
	}
}

func TestWriteTools(t *testing.T) {
	rs, _ := fakeRedmine(t)
	c, _ := redmine.New(rs.URL, "k", time.Second, false, 1<<20)
	d := &Deps{C: c, M: redmine.NewMeta(c, time.Minute), DefaultLimit: 25, TextFormat: "textile"}
	cs := connect(t, d)

	got, isErr := call(t, cs, "create_issue", map[string]any{"project": "website", "subject": "new one"})
	if isErr || !strings.Contains(got, "#124 created (Bug, Website)") {
		t.Fatalf("create_issue = %q (error=%v)", got, isErr)
	}

	got, isErr = call(t, cs, "update_issue", map[string]any{"id": 123, "status": "Closed"})
	if !isErr || !strings.Contains(got, "confirm") {
		t.Fatalf("closing without confirm = %q (error=%v)", got, isErr)
	}

	got, isErr = call(t, cs, "update_issue", map[string]any{"id": 123, "status": "Closed", "confirm": true})
	if isErr || !strings.Contains(got, "status -> Closed") {
		t.Fatalf("closing with confirm = %q (error=%v)", got, isErr)
	}

	got, isErr = call(t, cs, "update_issue", map[string]any{"id": 123})
	if !isErr || !strings.Contains(got, "nothing to update") {
		t.Fatalf("empty update = %q (error=%v)", got, isErr)
	}
}

func TestCreateIssueWithStagedUpload(t *testing.T) {
	rs, _ := fakeRedmine(t)
	c, _ := redmine.New(rs.URL, "k", time.Second, false, 1<<20)
	dir := t.TempDir()
	st, err := upload.New(dir, time.Minute, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	e, err := st.Save(strings.NewReader("payload"), "报告.txt")
	if err != nil {
		t.Fatal(err)
	}
	d := &Deps{C: c, M: redmine.NewMeta(c, time.Minute), DefaultLimit: 25, TextFormat: "textile", Upload: st, UploadPath: "/upload"}
	cs := connect(t, d)

	atomic.StoreInt32(&uploadsSeen, 0)
	got, isErr := call(t, cs, "create_issue", map[string]any{
		"project": "website", "subject": "with file", "attach_upload_ids": []string{e.ID},
	})
	if isErr {
		t.Fatalf("create_issue = %q", got)
	}
	if n := atomic.LoadInt32(&uploadsSeen); n != 1 {
		t.Fatalf("uploads posted = %d, want 1", n)
	}
	if _, err := os.Stat(filepath.Join(dir, e.ID)); !os.IsNotExist(err) {
		t.Fatal("staged directory was not removed")
	}

	got, isErr = call(t, cs, "create_issue", map[string]any{
		"project": "website", "subject": "x", "attach_upload_ids": []string{strings.Repeat("a", 32)},
	})
	if !isErr || !strings.Contains(got, "not found or expired") {
		t.Fatalf("unknown id = %q (error=%v)", got, isErr)
	}

	got, isErr = call(t, cs, "create_issue", map[string]any{
		"project": "website", "subject": "x", "attach_upload_ids": []string{"nope"},
	})
	if !isErr || !strings.Contains(got, "invalid upload id") {
		t.Fatalf("bad id = %q (error=%v)", got, isErr)
	}
}

func TestCreateIssueCustomFields(t *testing.T) {
	rs, _ := fakeRedmine(t)
	c, _ := redmine.New(rs.URL, "k", time.Second, false, 1<<20)
	d := &Deps{C: c, M: redmine.NewMeta(c, time.Minute), DefaultLimit: 25, TextFormat: "textile"}
	cs := connect(t, d)

	lastCreate = ""
	got, isErr := call(t, cs, "create_issue", map[string]any{
		"project":       "website",
		"subject":       "with custom field",
		"custom_fields": []map[string]any{{"id": 23, "value": "UTM"}},
	})
	if isErr {
		t.Fatalf("create_issue = %q", got)
	}
	if !strings.Contains(lastCreate, `"custom_fields":[{"id":23,"value":"UTM"}]`) {
		t.Fatalf("POST body = %s", lastCreate)
	}

	got, isErr = call(t, cs, "create_issue", map[string]any{
		"project": "website", "subject": "x", "custom_fields": []map[string]any{{"value": "UTM"}},
	})
	if !isErr || !strings.Contains(got, "id") {
		t.Fatalf("missing custom field id = %q (error=%v)", got, isErr)
	}
}

func TestUpdateIssueWithStagedUpload(t *testing.T) {
	rs, _ := fakeRedmine(t)
	c, _ := redmine.New(rs.URL, "k", time.Second, false, 1<<20)
	dir := t.TempDir()
	st, err := upload.New(dir, time.Minute, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	e, err := st.Save(strings.NewReader("payload"), "补充.txt")
	if err != nil {
		t.Fatal(err)
	}
	d := &Deps{C: c, M: redmine.NewMeta(c, time.Minute), DefaultLimit: 25, TextFormat: "textile", Upload: st, UploadPath: "/upload"}
	cs := connect(t, d)

	lastUpdate = ""
	atomic.StoreInt32(&uploadsSeen, 0)
	got, isErr := call(t, cs, "update_issue", map[string]any{
		"id": 123, "attach_upload_ids": []string{e.ID},
	})
	if isErr {
		t.Fatalf("update_issue = %q", got)
	}
	if !strings.Contains(got, "attachment 补充.txt") {
		t.Fatalf("update_issue = %q, want the attachment named", got)
	}
	if n := atomic.LoadInt32(&uploadsSeen); n != 1 {
		t.Fatalf("uploads posted = %d, want 1", n)
	}
	if !strings.Contains(lastUpdate, `"uploads":[{"token":"tok-1"`) {
		t.Fatalf("PUT body = %s", lastUpdate)
	}
	if _, err := os.Stat(filepath.Join(dir, e.ID)); !os.IsNotExist(err) {
		t.Fatal("staged directory was not removed")
	}
}

func TestCreateIssueStagedUploadNeedsHTTP(t *testing.T) {
	rs, _ := fakeRedmine(t)
	c, _ := redmine.New(rs.URL, "k", time.Second, false, 1<<20)
	d := &Deps{C: c, M: redmine.NewMeta(c, time.Minute), DefaultLimit: 25, TextFormat: "textile"}
	cs := connect(t, d)

	got, isErr := call(t, cs, "create_issue", map[string]any{
		"project": "website", "subject": "x", "attach_upload_ids": []string{strings.Repeat("a", 32)},
	})
	if !isErr || !strings.Contains(got, "HTTP transport") {
		t.Fatalf("stdio upload = %q (error=%v)", got, isErr)
	}
}

func TestReadOnlyBlocksWrites(t *testing.T) {
	rs, _ := fakeRedmine(t)
	c, _ := redmine.New(rs.URL, "k", time.Second, false, 1<<20)
	d := &Deps{C: c, M: redmine.NewMeta(c, time.Minute), DefaultLimit: 25, ReadOnly: true}
	cs := connect(t, d)

	got, isErr := call(t, cs, "create_issue", map[string]any{"project": "website", "subject": "x"})
	if !isErr || !strings.Contains(got, "read-only") {
		t.Fatalf("create in read-only = %q (error=%v)", got, isErr)
	}
}
