package redmine

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Error is a non-2xx answer from Redmine, already shaped for the model.
type Error struct {
	Status int
	Errors []string
	Body   string
}

func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "redmine: %d", e.Status)
	switch e.Status {
	case 401:
		b.WriteString(" authentication failed: the API key is invalid, expired, or the user is locked")
	case 403:
		b.WriteString(" permission denied")
	case 404:
		b.WriteString(" not found")
	case 422:
		b.WriteString(" validation failed")
	}
	if len(e.Errors) > 0 {
		b.WriteString(": " + strings.Join(e.Errors, "; "))
	} else if s := snippet(e.Body); s != "" && e.Status != 401 {
		b.WriteString(": " + s)
	}
	return b.String()
}

// IsStatus reports whether err is a Redmine error with the given HTTP status.
func IsStatus(err error, status int) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == status
}

func parseError(status int, body []byte) *Error {
	e := &Error{Status: status, Body: string(body)}
	var payload struct {
		Errors []string `json:"errors"`
		Error  string   `json:"error"`
	}
	if json.Unmarshal(body, &payload) == nil {
		e.Errors = payload.Errors
		if payload.Error != "" {
			e.Errors = append(e.Errors, payload.Error)
		}
	}
	return e
}

// snippet keeps an unexpected HTML/error body readable on one short line.
func snippet(body string) string {
	s := strings.TrimSpace(body)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "<") {
		var b strings.Builder
		inTag := false
		for _, r := range s {
			switch {
			case r == '<':
				inTag = true
			case r == '>':
				inTag = false
			case !inTag:
				b.WriteRune(r)
			}
		}
		s = b.String()
	}
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}
