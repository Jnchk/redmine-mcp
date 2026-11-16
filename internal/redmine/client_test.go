package redmine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }

func TestCreateIssueRequest(t *testing.T) {
	var (
		gotPath string
		gotKey  string
		gotBody map[string]map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("X-Redmine-API-Key")
		json.NewDecoder(r.Body).Decode(&gotBody)
		io.WriteString(w, `{"issue":{"id":7,"subject":"hi","tracker":{"id":2,"name":"Bug"},"project":{"id":1,"name":"Site"}}}`)
	}))
	defer srv.Close()

	// A sub-URI install must keep its prefix.
	c, err := New(srv.URL+"/redmine", "secret", time.Second, false, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	iss, err := c.CreateIssue(context.Background(), IssueWrite{ProjectID: ptr(1), Subject: ptr("hi")})
	if err != nil {
		t.Fatal(err)
	}

	if gotPath != "/redmine/issues.json" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotKey != "secret" {
		t.Fatalf("key header = %q", gotKey)
	}
	issue := gotBody["issue"]
	if issue["project_id"] != float64(1) || issue["subject"] != "hi" {
		t.Fatalf("body = %#v", gotBody)
	}
	if iss.ID != 7 || iss.Tracker.Name != "Bug" {
		t.Fatalf("issue = %#v", iss)
	}
}

func TestValidationErrorKeepsMessages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		io.WriteString(w, `{"errors":["Subject can't be blank","Tracker is not included in the list"]}`)
	}))
	defer srv.Close()

	c, err := New(srv.URL, "k", time.Second, false, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.CreateIssue(context.Background(), IssueWrite{ProjectID: ptr(1), Subject: ptr("")})
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	for _, want := range []string{"422", "validation failed", "Subject can't be blank", "Tracker is not included"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q missing %q", msg, want)
		}
	}
	if !IsStatus(err, 422) {
		t.Fatalf("IsStatus(422) = false for %v", err)
	}
}
