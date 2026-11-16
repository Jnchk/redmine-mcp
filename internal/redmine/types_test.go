package redmine

import (
	"context"
	"io"
	"net/http"
	"testing"
)

func TestUserName(t *testing.T) {
	cases := []struct {
		u    User
		want string
	}{
		{User{Login: "alice", Firstname: "Alice", Lastname: "Zhang"}, "Alice Zhang"},
		{User{Login: "bob"}, "bob"},
		{User{Login: "carol", Firstname: "Carol"}, "Carol"},
	}
	for _, tc := range cases {
		if got := tc.u.Name(); got != tc.want {
			t.Fatalf("Name(%#v) = %q, want %q", tc.u, got, tc.want)
		}
	}
}

func TestIssuesDecodesListAndTotal(t *testing.T) {
	var q map[string]string
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		q = map[string]string{
			"project_id": r.URL.Query().Get("project_id"),
			"status_id":  r.URL.Query().Get("status_id"),
			"limit":      r.URL.Query().Get("limit"),
		}
		io.WriteString(w, `{"issues":[{"id":1,"subject":"s","status":{"id":2,"name":"In Progress"},
			"assigned_to":{"id":9,"name":"Alice Zhang"},"updated_on":"2026-11-14T08:12:19Z"}],
			"total_count":9,"offset":0,"limit":2}`)
	})

	issues, total, err := c.Issues(context.Background(), IssueQuery{ProjectID: 1, StatusID: "open", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || total != 9 {
		t.Fatalf("issues = %d total = %d", len(issues), total)
	}
	is := issues[0]
	if is.Status.Name != "In Progress" || is.AssignedTo.Name != "Alice Zhang" {
		t.Fatalf("decoded issue = %#v", is)
	}
	if q["project_id"] != "1" || q["status_id"] != "open" || q["limit"] != "2" {
		t.Fatalf("query = %#v", q)
	}
}
