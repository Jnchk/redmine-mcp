package format

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Row renders one line-oriented record. Empty cells become "-".
func Row(cells ...string) string {
	for i, c := range cells {
		c = strings.Join(strings.Fields(c), " ")
		if c == "" {
			c = "-"
		}
		cells[i] = c
	}
	return strings.Join(cells, " | ")
}

// Truncate clips to n runes for single-line cells.
func Truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// Clip keeps newlines and says how much was dropped.
func Clip(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + fmt.Sprintf("\n… (truncated, %d chars total)", len(r))
}

// When renders a Redmine timestamp in UTC without seconds.
func When(s string) string {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC().Format("2006-01-02 15:04Z")
		}
	}
	return s
}

func Size(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	default:
		return strconv.FormatInt(n, 10) + " B"
	}
}

// Num renders a float without a trailing ".0".
func Num(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
