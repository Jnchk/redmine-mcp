package redmine

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Client is a thin Redmine REST client. It knows nothing about MCP.
type Client struct {
	base      string // no trailing slash, may include a sub-URI
	key       string
	hc        *http.Client
	maxUpload int64

	mu   sync.Mutex
	user *User
}

func New(baseURL, apiKey string, timeout time.Duration, insecure bool, maxUpload int64) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("redmine: invalid URL: %w", err)
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &Client{
		base:      strings.TrimRight(u.String(), "/"),
		key:       apiKey,
		hc:        &http.Client{Timeout: timeout, Transport: tr},
		maxUpload: maxUpload,
	}, nil
}

// Base is the configured Redmine URL, used to build user-facing links.
func (c *Client) Base() string { return c.base }

func (c *Client) CurrentUser(ctx context.Context) (*User, error) {
	c.mu.Lock()
	u := c.user
	c.mu.Unlock()
	if u != nil {
		return u, nil
	}
	var r struct {
		User *User `json:"user"`
	}
	if err := c.get(ctx, "users/current.json", nil, &r); err != nil {
		return nil, err
	}
	if r.User == nil {
		return nil, fmt.Errorf("redmine: empty response from users/current.json")
	}
	c.mu.Lock()
	c.user = r.User
	c.mu.Unlock()
	return r.User, nil
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	return c.request(ctx, http.MethodGet, path, q, nil, out)
}

func (c *Client) post(ctx context.Context, path string, body, out any) error {
	return c.request(ctx, http.MethodPost, path, nil, body, out)
}

func (c *Client) put(ctx context.Context, path string, body, out any) error {
	return c.request(ctx, http.MethodPut, path, nil, body, out)
}

func (c *Client) request(ctx context.Context, method, path string, q url.Values, body, out any) error {
	data, err := c.do(ctx, method, path, q, body)
	if err != nil {
		return err
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("redmine: decoding %s: %w", path, err)
	}
	return nil
}

// GetRaw returns the untouched JSON for a GET, used by the raw output mode.
func (c *Client) GetRaw(ctx context.Context, path string, q url.Values) ([]byte, error) {
	return c.do(ctx, http.MethodGet, path, q, nil)
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, body any) ([]byte, error) {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return nil, fmt.Errorf("redmine: encoding request: %w", err)
		}
	}
	target := c.base + "/" + strings.TrimPrefix(path, "/")
	if len(q) > 0 {
		target += "?" + q.Encode()
	}

	attempts := 1
	if method == http.MethodGet {
		attempts = 2 // one retry, GET only: a retried create makes duplicate issues
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("redmine: building request: %w", err)
		}
		req.Header.Set("X-Redmine-API-Key", c.key)
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.hc.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("redmine: %s %s: %w", method, path, err)
			if ctx.Err() != nil {
				return nil, lastErr
			}
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("redmine: reading %s: %w", path, readErr)
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return data, nil
		}
		lastErr = parseError(resp.StatusCode, data)
		if i+1 < attempts && retryable(resp.StatusCode) {
			continue
		}
		return nil, lastErr
	}
	return nil, lastErr
}

func retryable(status int) bool {
	return status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

// each walks every page of a collection endpoint and hands each raw item to fn.
func (c *Client) each(ctx context.Context, path, key string, fn func(json.RawMessage) error) error {
	const pageSize = 100
	for page := 0; page < 50; page++ { // hard stop at 5000 items
		q := url.Values{"limit": {strconv.Itoa(pageSize)}, "offset": {strconv.Itoa(page * pageSize)}}
		var raw map[string]json.RawMessage
		if err := c.get(ctx, path, q, &raw); err != nil {
			return err
		}
		var items []json.RawMessage
		if v, ok := raw[key]; ok {
			if err := json.Unmarshal(v, &items); err != nil {
				return fmt.Errorf("redmine: decoding %s: %w", key, err)
			}
		}
		for _, it := range items {
			if err := fn(it); err != nil {
				return err
			}
		}
		if len(items) < pageSize {
			return nil
		}
	}
	return nil
}
