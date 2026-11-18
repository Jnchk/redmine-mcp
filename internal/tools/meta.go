package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"redmine-mcp/internal/format"
	"redmine-mcp/internal/redmine"
)

func (d *Deps) whoami(ctx context.Context, _ struct{}) (string, error) {
	u, err := d.C.CurrentUser(ctx)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	if n := u.Name(); n != u.Login {
		fmt.Fprintf(&b, "login: %s (%s)\n", u.Login, n)
	} else {
		fmt.Fprintf(&b, "login: %s\n", u.Login)
	}
	fmt.Fprintf(&b, "admin: %s\n", yesNo(u.Admin))
	fmt.Fprintf(&b, "server: %s\n", d.C.Base())
	fmt.Fprintf(&b, "mode: %s\n", map[bool]string{true: "read-only", false: "read-write"}[d.ReadOnly])

	if _, err := d.M.Users(ctx); err != nil {
		b.WriteString("user names: unavailable (the API key cannot list users; pass numeric user ids)\n")
	} else {
		b.WriteString("user names: available\n")
	}
	fmt.Fprintf(&b, "content format: %s\n", d.TextFormat)
	if d.Upload != nil && d.Upload.Enabled() {
		fmt.Fprintf(&b, "staging: %s -> %s (ttl %s)\n", d.UploadPath, d.Upload.Dir(), d.Upload.TTL())
	} else {
		b.WriteString("staging: disabled\n")
	}
	return b.String(), nil
}

type projectsIn struct {
	Q string `json:"q,omitempty" jsonschema:"substring filter on project name or identifier"`
}

func (d *Deps) listProjects(ctx context.Context, in projectsIn) (string, error) {
	ps, err := d.M.Projects(ctx)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("# id | identifier | name | status\n")
	n := 0
	for _, p := range ps {
		if in.Q != "" && !containsFold(p.Name+" "+p.Identifier, in.Q) {
			continue
		}
		b.WriteString(format.Row(strconv.Itoa(p.ID), p.Identifier, p.Name, projectStatus(p.Status)))
		b.WriteByte('\n')
		n++
	}
	if n == 0 {
		return fmt.Sprintf("no projects matched %q", in.Q), nil
	}
	fmt.Fprintf(&b, "# returned %d of %d\n", n, len(ps))
	return b.String(), nil
}

type metadataIn struct {
	Kind string `json:"kind" jsonschema:"one of: trackers, statuses, priorities, activities, users"`
	Q    string `json:"q,omitempty" jsonschema:"substring filter on the name"`
}

func (d *Deps) listMetadata(ctx context.Context, in metadataIn) (string, error) {
	var b strings.Builder
	matched := 0

	switch strings.ToLower(strings.TrimSpace(in.Kind)) {
	case "trackers", "tracker":
		rs, err := d.M.Trackers(ctx)
		if err != nil {
			return "", err
		}
		b.WriteString("# id | name\n")
		for _, r := range rs {
			if keep(in.Q, r.Name) {
				b.WriteString(format.Row(strconv.Itoa(r.ID), r.Name) + "\n")
				matched++
			}
		}
	case "statuses", "status":
		ss, err := d.M.Statuses(ctx)
		if err != nil {
			return "", err
		}
		b.WriteString("# id | name | closed | default\n")
		for _, s := range ss {
			if keep(in.Q, s.Name) {
				b.WriteString(format.Row(strconv.Itoa(s.ID), s.Name, yesNo(s.IsClosed), yesNo(s.IsDefault)) + "\n")
				matched++
			}
		}
	case "priorities", "priority":
		rs, err := d.M.Priorities(ctx)
		if err != nil {
			return "", err
		}
		b.WriteString("# id | name | default\n")
		for _, r := range rs {
			if keep(in.Q, r.Name) {
				b.WriteString(format.Row(strconv.Itoa(r.ID), r.Name, yesNo(r.IsDefault)) + "\n")
				matched++
			}
		}
	case "activities", "activity":
		rs, err := d.M.Activities(ctx)
		if err != nil {
			return "", err
		}
		b.WriteString("# id | name | default\n")
		for _, r := range rs {
			if keep(in.Q, r.Name) {
				b.WriteString(format.Row(strconv.Itoa(r.ID), r.Name, yesNo(r.IsDefault)) + "\n")
				matched++
			}
		}
	case "users", "user":
		us, err := d.M.Users(ctx)
		if err != nil {
			if redmine.IsStatus(err, 403) {
				return "", fmt.Errorf("cannot list users: the API key lacks permission (users.json is admin-only); pass numeric user ids")
			}
			return "", err
		}
		b.WriteString("# id | login | name | admin\n")
		for _, u := range us {
			if keep(in.Q, u.Name(), u.Login) {
				b.WriteString(format.Row(strconv.Itoa(u.ID), u.Login, u.Name(), yesNo(u.Admin)) + "\n")
				matched++
			}
		}
	default:
		return "", fmt.Errorf("unknown kind %q: use trackers, statuses, priorities, activities or users", in.Kind)
	}

	if matched == 0 {
		if in.Q == "" {
			return "no values returned by the server", nil
		}
		return fmt.Sprintf("no %s matched %q", in.Kind, in.Q), nil
	}
	return b.String(), nil
}

func projectStatus(s int) string {
	switch s {
	case 1:
		return "active"
	case 5:
		return "closed"
	case 9:
		return "archived"
	case 0:
		return "-"
	}
	return strconv.Itoa(s)
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func keep(q string, vals ...string) bool {
	if q == "" {
		return true
	}
	for _, v := range vals {
		if containsFold(v, q) {
			return true
		}
	}
	return false
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
