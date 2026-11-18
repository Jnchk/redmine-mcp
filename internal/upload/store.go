// Package upload stores client-supplied files until a write tool claims them.
//
// A client posts bytes to the HTTP transport; the model only ever handles the returned id. The
// staging directory is local to one instance: one directory per instance, configured by the
// operator (MCP_UPLOAD_DIR), never shared between instances or hosts.
package upload

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	// ErrNotFound is returned for an unknown, expired or already claimed id.
	ErrNotFound = errors.New("upload not found or expired")
	// ErrTooLarge is returned when the body exceeds the configured limit.
	ErrTooLarge = errors.New("upload exceeds the size limit")
	// ErrEmpty is returned for a zero-byte upload.
	ErrEmpty = errors.New("upload is empty")
	// ErrDisabled is returned when the store has no directory configured.
	ErrDisabled = errors.New("upload staging is disabled")
)

var idPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// ValidID reports whether s looks like a staging id.
func ValidID(s string) bool { return idPattern.MatchString(s) }

// Entry is one staged file.
type Entry struct {
	ID       string
	Filename string
	Size     int64
	SHA256   string
	// Path is the payload path while claimed; empty before Claim.
	Path    string
	Expires time.Time
}

// Store is the staging directory. A Store with an empty dir is disabled.
type Store struct {
	dir string
	ttl time.Duration
	max int64
}

// New prepares the staging directory. An empty dir returns a disabled store.
func New(dir string, ttl time.Duration, max int64) (*Store, error) {
	s := &Store{dir: dir, ttl: ttl, max: max}
	if dir == "" {
		return s, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("staging directory %s: %w", dir, err)
	}
	return s, nil
}

// Enabled reports whether the store has a directory.
func (s *Store) Enabled() bool { return s != nil && s.dir != "" }

// Dir returns the configured staging directory.
func (s *Store) Dir() string {
	if s == nil {
		return ""
	}
	return s.dir
}

// TTL returns how long an unclaimed upload survives.
func (s *Store) TTL() time.Duration {
	if s == nil {
		return 0
	}
	return s.ttl
}

// Max returns the per-upload size limit in bytes.
func (s *Store) Max() int64 {
	if s == nil {
		return 0
	}
	return s.max
}

// Save streams one upload into a fresh directory and returns its id.
func (s *Store) Save(r io.Reader, filename string) (Entry, error) {
	if !s.Enabled() {
		return Entry{}, ErrDisabled
	}
	id, err := newID()
	if err != nil {
		return Entry{}, err
	}
	dir := filepath.Join(s.dir, id)
	if err := os.Mkdir(dir, 0o700); err != nil {
		return Entry{}, fmt.Errorf("staging slot: %w", err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(dir)
		}
	}()

	part := filepath.Join(dir, "content.part")
	f, err := os.OpenFile(part, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Entry{}, fmt.Errorf("staging file: %w", err)
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, s.max+1))
	if copyErr != nil {
		f.Close()
		return Entry{}, copyErr
	}
	if n > s.max {
		f.Close()
		return Entry{}, ErrTooLarge
	}
	if n == 0 {
		f.Close()
		return Entry{}, ErrEmpty
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return Entry{}, fmt.Errorf("staging sync: %w", err)
	}
	if err := f.Close(); err != nil {
		return Entry{}, fmt.Errorf("staging close: %w", err)
	}
	if err := os.Rename(part, filepath.Join(dir, "content")); err != nil {
		return Entry{}, fmt.Errorf("staging commit: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "filename"), []byte(strings.TrimSpace(filename)), 0o600); err != nil {
		return Entry{}, fmt.Errorf("staging name: %w", err)
	}
	ok = true
	return Entry{
		ID:       id,
		Filename: safeName(filename),
		Size:     n,
		SHA256:   hex.EncodeToString(h.Sum(nil)),
		Expires:  time.Now().Add(s.ttl),
	}, nil
}

// Claim reserves an upload for one write call. It is atomic: a second claim fails.
func (s *Store) Claim(id string) (Entry, error) {
	if !s.Enabled() || !idPattern.MatchString(id) {
		return Entry{}, ErrNotFound
	}
	dir := filepath.Join(s.dir, id)
	claimed := filepath.Join(dir, "content.claimed")
	if err := os.Rename(filepath.Join(dir, "content"), claimed); err != nil {
		return Entry{}, ErrNotFound
	}
	now := time.Now()
	_ = os.Chtimes(claimed, now, now) // rename keeps mtime; keep the sweeper off a live claim

	st, err := os.Stat(claimed)
	if err != nil {
		return Entry{}, ErrNotFound
	}
	raw, err := os.ReadFile(filepath.Join(dir, "filename"))
	if err != nil {
		raw = nil
	}
	return Entry{
		ID:       id,
		Filename: safeName(string(raw)),
		Size:     st.Size(),
		Path:     claimed,
		Expires:  now.Add(s.ttl),
	}, nil
}

// Remove deletes an upload and everything staged with it.
func (s *Store) Remove(id string) error {
	if !s.Enabled() || !idPattern.MatchString(id) {
		return nil
	}
	return os.RemoveAll(filepath.Join(s.dir, id))
}

// Sweep removes every upload directory older than the TTL and reports how many went.
func (s *Store) Sweep(now time.Time) (int, error) {
	if !s.Enabled() {
		return 0, nil
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) < s.ttl {
			continue
		}
		if err := os.RemoveAll(filepath.Join(s.dir, e.Name())); err == nil {
			n++
		}
	}
	return n, nil
}

// Run sweeps immediately and then once a minute until ctx ends. onErr may be nil.
func (s *Store) Run(ctx context.Context, onErr func(error)) {
	if !s.Enabled() {
		return
	}
	if _, err := s.Sweep(time.Now()); err != nil && onErr != nil {
		onErr(err)
	}
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if _, err := s.Sweep(now); err != nil && onErr != nil {
				onErr(err)
			}
		}
	}
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("upload id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// safeName turns any client-supplied name into something safe to show in Redmine. It is data,
// never a path: separators are replaced, control characters dropped, length capped.
func safeName(name string) string {
	name = strings.TrimSpace(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '/' || r == '\\':
			b.WriteByte('_')
		case r < 0x20 || r == 0x7f:
			// drop control characters
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "upload.bin"
	}
	if r := []rune(out); len(r) > 200 {
		out = string(r[:200])
	}
	return out
}
