package upload

import (
	"bytes"
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

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(t.TempDir(), time.Minute, 1024)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSaveClaimRemove(t *testing.T) {
	s := newStore(t)
	e, err := s.Save(strings.NewReader("hello"), "日志 报告.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !ValidID(e.ID) || e.Size != 5 || e.Filename != "日志 报告.txt" {
		t.Fatalf("entry = %+v", e)
	}

	got, err := s.Claim(e.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(got.Path)
	if err != nil || string(b) != "hello" {
		t.Fatalf("claimed bytes = %q, err = %v", b, err)
	}
	if got.Filename != "日志 报告.txt" {
		t.Fatalf("claimed filename = %q", got.Filename)
	}

	if _, err := s.Claim(e.ID); err != ErrNotFound {
		t.Fatalf("second claim err = %v, want ErrNotFound", err)
	}

	if err := s.Remove(e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir(), e.ID)); !os.IsNotExist(err) {
		t.Fatalf("directory still present: %v", err)
	}
}

func TestSaveLimits(t *testing.T) {
	s := newStore(t)
	if _, err := s.Save(strings.NewReader(""), "x"); err != ErrEmpty {
		t.Fatalf("empty err = %v", err)
	}
	if _, err := s.Save(strings.NewReader(strings.Repeat("x", 2000)), "x"); err != ErrTooLarge {
		t.Fatalf("oversize err = %v", err)
	}
	if entries, _ := os.ReadDir(s.Dir()); len(entries) != 0 {
		t.Fatalf("leftover staging entries: %d", len(entries))
	}
}

func TestSafeName(t *testing.T) {
	cases := map[string]string{
		"":                       "upload.bin",
		"../../etc/passwd":       ".._.._etc_passwd",
		"a\x00b":                 "ab",
		strings.Repeat("x", 300): strings.Repeat("x", 200),
	}
	for in, want := range cases {
		if got := safeName(in); got != want {
			t.Fatalf("safeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSweep(t *testing.T) {
	s := newStore(t)
	e, err := s.Save(strings.NewReader("x"), "x")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(filepath.Join(s.Dir(), e.ID), old, old); err != nil {
		t.Fatal(err)
	}
	n, err := s.Sweep(time.Now())
	if err != nil || n != 1 {
		t.Fatalf("sweep = %d, %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir(), e.ID)); !os.IsNotExist(err) {
		t.Fatal("expired upload survived")
	}
}

func TestDisabled(t *testing.T) {
	s, err := New("", time.Minute, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if s.Enabled() {
		t.Fatal("empty dir must be disabled")
	}
	if _, err := s.Save(strings.NewReader("x"), "x"); err != ErrDisabled {
		t.Fatalf("err = %v", err)
	}
}

func TestHandler(t *testing.T) {
	s := newStore(t)
	h := s.Handler(false)

	post := func(body string, ct string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/upload?filename="+"%E6%97%A5%E5%BF%97.txt", strings.NewReader(body))
		r.Header.Set("Content-Type", ct)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	w := post("hello", "application/octet-stream")
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s", w.Code, w.Body)
	}
	var resp struct {
		ID       string `json:"id"`
		Filename string `json:"filename"`
		Size     int64  `json:"size"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !ValidID(resp.ID) || resp.Filename != "日志.txt" || resp.Size != 5 {
		t.Fatalf("response = %+v", resp)
	}

	if w := post("x", "text/plain"); w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("wrong content type = %d", w.Code)
	}
	if w := post("", "application/octet-stream"); w.Code != http.StatusBadRequest {
		t.Fatalf("empty = %d", w.Code)
	}
	if w := post(strings.Repeat("x", 2000), "application/octet-stream"); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize = %d", w.Code)
	}

	body := io.NopCloser(strings.NewReader("x"))
	r := httptest.NewRequest(http.MethodGet, "/upload", body)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r)
	if w2.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET = %d", w2.Code)
	}

	ro := s.Handler(true)
	w3 := httptest.NewRecorder()
	r3 := httptest.NewRequest(http.MethodPost, "/upload", bytes.NewBufferString("x"))
	r3.Header.Set("Content-Type", "application/octet-stream")
	ro.ServeHTTP(w3, r3)
	if w3.Code != http.StatusForbidden {
		t.Fatalf("read-only = %d", w3.Code)
	}

	off, _ := New("", time.Minute, 1024)
	w4 := httptest.NewRecorder()
	r4 := httptest.NewRequest(http.MethodPost, "/upload", bytes.NewBufferString("x"))
	r4.Header.Set("Content-Type", "application/octet-stream")
	off.Handler(false).ServeHTTP(w4, r4)
	if w4.Code != http.StatusNotFound {
		t.Fatalf("disabled = %d", w4.Code)
	}
}
