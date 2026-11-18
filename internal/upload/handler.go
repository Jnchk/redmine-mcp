package upload

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"syscall"
)

// Handler serves POST {MCP_UPLOAD_PATH}?filename=NAME with a raw octet-stream body.
// Authentication is the caller's business: wrap it in the same token middleware as /mcp.
func (s *Store) Handler(readOnly bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "upload: method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if readOnly {
			http.Error(w, "upload: disabled in read-only mode (REDMINE_READ_ONLY)", http.StatusForbidden)
			return
		}
		if !s.Enabled() {
			http.Error(w, "upload: staging is disabled", http.StatusNotFound)
			return
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/octet-stream" {
			http.Error(w, "upload: Content-Type must be application/octet-stream", http.StatusUnsupportedMediaType)
			return
		}

		body := http.MaxBytesReader(w, r.Body, s.Max())
		e, err := s.Save(body, r.URL.Query().Get("filename"))
		if err != nil {
			status, msg := statusFor(err)
			http.Error(w, msg, status)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(struct {
			ID        string `json:"id"`
			Filename  string `json:"filename"`
			Size      int64  `json:"size"`
			SHA256    string `json:"sha256"`
			ExpiresAt string `json:"expires_at"`
		}{e.ID, e.Filename, e.Size, e.SHA256, e.Expires.UTC().Format("2006-01-02T15:04:05Z")})
	})
}

func statusFor(err error) (int, string) {
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig), errors.Is(err, ErrTooLarge):
		return http.StatusRequestEntityTooLarge, "upload: file exceeds the configured size limit (REDMINE_MAX_UPLOAD)"
	case errors.Is(err, ErrEmpty):
		return http.StatusBadRequest, "upload: empty body"
	case errors.Is(err, ErrDisabled):
		return http.StatusNotFound, "upload: staging is disabled"
	case errors.Is(err, syscall.ENOSPC), errors.Is(err, os.ErrPermission):
		return http.StatusInsufficientStorage, "upload: staging directory is full or unwritable"
	default:
		return http.StatusInternalServerError, fmt.Sprintf("upload: %v", err)
	}
}
