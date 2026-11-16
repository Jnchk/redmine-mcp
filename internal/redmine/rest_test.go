package redmine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testServer starts a stub Redmine and returns a Client pointed at it.
func testServer(t *testing.T, fn http.HandlerFunc) (*httptest.Server, *Client) {
	t.Helper()
	srv := httptest.NewServer(fn)
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "k", time.Second, false, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return srv, c
}

func TestIssueGetSendsInclude(t *testing.T) {
	var include string
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/issues/123.json" {
			t.Errorf("path = %q", r.URL.Path)
		}
		include = r.URL.Query().Get("include")
		io.WriteString(w, `{"issue":{"id":123,"subject":"s"}}`)
	})

	is, err := c.Issue(context.Background(), 123)
	if err != nil {
		t.Fatal(err)
	}
	if is.ID != 123 {
		t.Fatalf("issue = %#v", is)
	}
	for _, want := range []string{"journals", "attachments", "relations", "children"} {
		if !strings.Contains(include, want) {
			t.Errorf("include = %q, missing %q", include, want)
		}
	}
}

func TestIssueEmptyResponseIsAnError(t *testing.T) {
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{}`)
	})
	if _, err := c.Issue(context.Background(), 1); err == nil {
		t.Fatal("expected an error for an empty issue response")
	}
}

func TestUpdateIssueUsesPutAndNoBody(t *testing.T) {
	var method, path string
	var body map[string]map[string]any
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusNoContent)
	})

	err := c.UpdateIssue(context.Background(), 42, IssueWrite{StatusID: ptr(5)})
	if err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPut || path != "/issues/42.json" {
		t.Fatalf("%s %s", method, path)
	}
	if body["issue"]["status_id"] != float64(5) {
		t.Fatalf("body = %#v", body)
	}
}

func TestSearchIssuesParams(t *testing.T) {
	var q map[string]string
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search.json" {
			t.Errorf("path = %q", r.URL.Path)
		}
		q = map[string]string{
			"q": r.URL.Query().Get("q"), "issues": r.URL.Query().Get("issues"),
			"scope": r.URL.Query().Get("scope"), "limit": r.URL.Query().Get("limit"),
		}
		io.WriteString(w, `{"results":[{"id":7,"title":"t","type":"Issue","description":"d"}],"total_count":1}`)
	})

	results, total, err := c.SearchIssues(context.Background(), "login", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || total != 1 || results[0].ID != 7 {
		t.Fatalf("results = %#v total=%d", results, total)
	}
	want := map[string]string{"q": "login", "issues": "1", "scope": "all", "limit": "10"}
	for k, v := range want {
		if q[k] != v {
			t.Errorf("%s = %q, want %q", k, q[k], v)
		}
	}
}

func TestUploadSendsStreamAndReadsToken(t *testing.T) {
	var contentType, filename, key string
	var got []byte
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/uploads.json" {
			t.Errorf("path = %q", r.URL.Path)
		}
		contentType = r.Header.Get("Content-Type")
		key = r.Header.Get("X-Redmine-API-Key")
		filename = r.URL.Query().Get("filename")
		got, _ = io.ReadAll(r.Body)
		io.WriteString(w, `{"upload":{"token":"tok123"}}`)
	})

	path := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(path, []byte("png-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	up, err := c.Upload(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if up.Token != "tok123" || up.Filename != "shot.png" {
		t.Fatalf("upload = %#v", up)
	}
	if contentType != "application/octet-stream" || filename != "shot.png" || key != "k" {
		t.Fatalf("content-type=%q filename=%q key=%q", contentType, filename, key)
	}
	if string(got) != "png-bytes" {
		t.Fatalf("body = %q", got)
	}
}

func TestUploadRejectsOversizeAndNonRegular(t *testing.T) {
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"upload":{"token":"x"}}`)
	})
	c.maxUpload = 3

	path := filepath.Join(t.TempDir(), "big.bin")
	if err := os.WriteFile(path, []byte("toolarge"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Upload(context.Background(), path); err == nil || !strings.Contains(err.Error(), "upload limit") {
		t.Fatalf("oversize error = %v", err)
	}
	if _, err := c.Upload(context.Background(), t.TempDir()); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("directory error = %v", err)
	}
	if _, err := c.Upload(context.Background(), filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

func TestUploadErrorIsParsed(t *testing.T) {
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		io.WriteString(w, `{"errors":["File is too big"]}`)
	})
	path := filepath.Join(t.TempDir(), "f.bin")
	os.WriteFile(path, []byte("x"), 0o600)

	_, err := c.Upload(context.Background(), path)
	if !IsStatus(err, 422) || !strings.Contains(err.Error(), "File is too big") {
		t.Fatalf("err = %v", err)
	}
}

func TestTimeEntriesAndCreate(t *testing.T) {
	var created map[string]map[string]any
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			json.NewDecoder(r.Body).Decode(&created)
			w.WriteHeader(http.StatusCreated)
			io.WriteString(w, `{"time_entry":{"id":5,"hours":2.5,"spent_on":"2026-11-16","activity":{"id":9,"name":"Development"}}}`)
		default:
			io.WriteString(w, `{"time_entries":[{"id":5,"hours":2.5,"spent_on":"2026-11-16","issue":{"id":123}}],"total_count":1}`)
		}
	})

	entries, total, err := c.TimeEntries(context.Background(), TimeQuery{IssueID: 123})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || total != 1 || entries[0].Issue.ID != 123 {
		t.Fatalf("entries = %#v total = %d", entries, total)
	}

	entry, err := c.CreateTimeEntry(context.Background(), TimeWrite{IssueID: ptr(123), Hours: 2.5, ActivityID: ptr(9)})
	if err != nil {
		t.Fatal(err)
	}
	if entry.ID != 5 || entry.Activity.Name != "Development" {
		t.Fatalf("entry = %#v", entry)
	}
	if created["time_entry"]["hours"] != 2.5 || created["time_entry"]["issue_id"] != float64(123) {
		t.Fatalf("body = %#v", created)
	}
}
