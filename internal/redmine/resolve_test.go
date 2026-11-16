package redmine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newMetaForTest(t *testing.T) *Meta {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/issue_statuses.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"issue_statuses":[
			{"id":1,"name":"New","is_default":true,"is_closed":false},
			{"id":2,"name":"In Progress","is_closed":false},
			{"id":7,"name":"Progress review","is_closed":false},
			{"id":5,"name":"Closed","is_closed":true}]}`))
	})
	mux.HandleFunc("/users/current.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"user":{"id":9,"login":"alice","firstname":"Alice","lastname":"Zhang"}}`))
	})
	mux.HandleFunc("/users.json", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"errors":["Forbidden"]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c, err := New(srv.URL, "k", time.Second, false, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return NewMeta(c, time.Minute)
}

func TestResolve(t *testing.T) {
	ctx := context.Background()
	m := newMetaForTest(t)

	cases := []struct {
		name    string
		kind    Kind
		in      string
		wantID  int
		wantErr string
	}{
		{name: "digits pass through", kind: KStatus, in: "2", wantID: 2},
		{name: "exact name", kind: KStatus, in: "In Progress", wantID: 2},
		{name: "exact name is case-insensitive", kind: KStatus, in: "in progress", wantID: 2},
		{name: "unique prefix", kind: KStatus, in: "in prog", wantID: 2},
		{name: "me", kind: KUser, in: "me", wantID: 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := m.Resolve(ctx, tc.kind, tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if got.ID != tc.wantID {
				t.Fatalf("id = %d, want %d", got.ID, tc.wantID)
			}
		})
	}
}

func TestResolveAmbiguous(t *testing.T) {
	m := newMetaForTest(t)
	// "gress" is a substring of both, so neither hit outranks the other.
	_, err := m.Resolve(context.Background(), KStatus, "gress")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("err = %v, want an ambiguity error", err)
	}
	if !strings.Contains(err.Error(), "In Progress") || !strings.Contains(err.Error(), "Progress review") {
		t.Fatalf("err = %v, want both candidates listed", err)
	}
}

func TestResolveUnknownListsValues(t *testing.T) {
	m := newMetaForTest(t)
	_, err := m.Resolve(context.Background(), KStatus, "nope")
	if err == nil || !strings.Contains(err.Error(), "Available") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), `"New" (1)`) {
		t.Fatalf("err = %v, want the real values listed", err)
	}
}

func TestResolveUsersDegradesOn403(t *testing.T) {
	m := newMetaForTest(t)
	_, err := m.Resolve(context.Background(), KUser, "bob")
	if err == nil || !strings.Contains(err.Error(), "cannot resolve user names") {
		t.Fatalf("err = %v", err)
	}
	// Numeric ids and "me" must keep working without the users list.
	if n, err := m.Resolve(context.Background(), KUser, "42"); err != nil || n.ID != 42 {
		t.Fatalf("numeric user id: %v %v", n, err)
	}
	if n, err := m.Resolve(context.Background(), KUser, "me"); err != nil || n.ID != 9 {
		t.Fatalf("me: %v %v", n, err)
	}
}
