package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"redmine-mcp/internal/config"
	"redmine-mcp/internal/redmine"
)

func TestWithToken(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	cases := []struct {
		name   string
		token  string
		header string
		want   int
	}{
		{name: "no token configured passes through", token: "", header: "", want: 200},
		{name: "missing header", token: "s3cret", header: "", want: 401},
		{name: "wrong token", token: "s3cret", header: "Bearer nope", want: 401},
		{name: "right token", token: "s3cret", header: "Bearer s3cret", want: 200},
		{name: "truncated header", token: "s3cret", header: "Bearer", want: 401},
		{name: "different length", token: "s3cret", header: "Bearer s3cret-and-more", want: 401},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			withToken(tc.token, ok).ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestInstructionsReflectModeAndFormat(t *testing.T) {
	u := &redmine.User{ID: 1, Login: "alice", Firstname: "Alice", Lastname: "Zhang"}

	got := instructions(&config.Config{URL: "https://r.example.com", ReadOnly: true, TextFormat: "textile"}, u)
	for _, want := range []string{"https://r.example.com", "Signed in as alice", "read-only", "Textile", "confirm: true"} {
		if !strings.Contains(got, want) {
			t.Fatalf("instructions missing %q:\n%s", want, got)
		}
	}

	got = instructions(&config.Config{URL: "https://r.example.com", TextFormat: "markdown"}, u)
	if !strings.Contains(got, "Markdown") || strings.Contains(got, "Textile:") {
		t.Fatalf("markdown instructions wrong:\n%s", got)
	}
	if strings.Contains(got, "read-only") {
		t.Fatalf("read-write instructions claim read-only:\n%s", got)
	}
}
