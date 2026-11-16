package format

import (
	"strings"
	"testing"
)

func TestRow(t *testing.T) {
	if got := Row("1", "", "three"); got != "1 | - | three" {
		t.Fatalf("Row = %q", got)
	}
	if got := Row("a\nb", "c"); got != "a b | c" {
		t.Fatalf("Row with newline = %q", got)
	}
}

func TestTruncateIsRuneSafe(t *testing.T) {
	got := Truncate("登录失败在单点登录", 4)
	if got != "登录失败…" {
		t.Fatalf("Truncate = %q", got)
	}
}

func TestWhen(t *testing.T) {
	if got := When("2026-11-14T08:12:19Z"); got != "2026-11-14 08:12Z" {
		t.Fatalf("When = %q", got)
	}
	if got := When(""); got != "" {
		t.Fatalf("When empty = %q", got)
	}
}

func TestSize(t *testing.T) {
	cases := map[int64]string{512: "512 B", 2048: "2.0 KiB", 3 << 20: "3.0 MiB"}
	for in, want := range cases {
		if got := Size(in); got != want {
			t.Fatalf("Size(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestClip(t *testing.T) {
	if got := Clip("  short  ", 10); got != "short" {
		t.Fatalf("Clip = %q", got)
	}
	long := strings.Repeat("字", 30)
	got := Clip(long, 5)
	if !strings.HasPrefix(got, "字字字字字") || !strings.Contains(got, "30 chars total") {
		t.Fatalf("Clip truncation = %q", got)
	}
}

func TestNum(t *testing.T) {
	cases := map[float64]string{2: "2", 2.5: "2.5", 0: "0", 0.25: "0.25"}
	for in, want := range cases {
		if got := Num(in); got != want {
			t.Fatalf("Num(%v) = %q, want %q", in, got, want)
		}
	}
}
