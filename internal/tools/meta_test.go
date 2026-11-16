package tools

import (
	"strings"
	"testing"
	"time"

	"redmine-mcp/internal/redmine"
)

func TestListMetadataAllKinds(t *testing.T) {
	rs, _ := fakeRedmine(t)
	c, _ := redmine.New(rs.URL, "k", time.Second, false, 1<<20)
	cs := connect(t, &Deps{C: c, M: redmine.NewMeta(c, time.Minute), DefaultLimit: 25})

	headers := map[string]string{
		"trackers":   "# id | name",
		"priorities": "# id | name | default",
		"activities": "# id | name | default",
		"statuses":   "# id | name | closed | default",
		"users":      "# id | login | name | admin",
	}
	for kind, header := range headers {
		got, isErr := call(t, cs, "list_metadata", map[string]any{"kind": kind})
		if isErr {
			t.Fatalf("%s: %s", kind, got)
		}
		if !strings.HasPrefix(got, header) {
			t.Fatalf("%s header = %q, want prefix %q", kind, got, header)
		}
	}
}

func TestListMetadataFilter(t *testing.T) {
	rs, _ := fakeRedmine(t)
	c, _ := redmine.New(rs.URL, "k", time.Second, false, 1<<20)
	cs := connect(t, &Deps{C: c, M: redmine.NewMeta(c, time.Minute), DefaultLimit: 25})

	got, isErr := call(t, cs, "list_metadata", map[string]any{"kind": "trackers", "q": "bug"})
	if isErr || !strings.Contains(got, "1 | Bug") {
		t.Fatalf("filtered trackers = %q (error=%v)", got, isErr)
	}
	got, isErr = call(t, cs, "list_metadata", map[string]any{"kind": "trackers", "q": "nope"})
	if isErr || !strings.Contains(got, `no trackers matched "nope"`) {
		t.Fatalf("empty filter = %q (error=%v)", got, isErr)
	}
}

func TestProjectStatusLabels(t *testing.T) {
	cases := map[int]string{0: "-", 1: "active", 5: "closed", 9: "archived", 42: "42"}
	for in, want := range cases {
		if got := projectStatus(in); got != want {
			t.Fatalf("projectStatus(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestLimitClamp(t *testing.T) {
	d := &Deps{DefaultLimit: 25}
	cases := map[int]int{0: 25, -1: 25, 5: 5, 100: 100, 101: 100, 9999: 100}
	for in, want := range cases {
		if got := d.limit(in); got != want {
			t.Fatalf("limit(%d) = %d, want %d", in, got, want)
		}
	}
}
