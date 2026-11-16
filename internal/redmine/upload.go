package redmine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
)

// Upload sends one local file to /uploads.json and returns its token.
func (c *Client) Upload(ctx context.Context, path string) (Upload, error) {
	f, err := os.Open(path)
	if err != nil {
		return Upload{}, err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return Upload{}, err
	}
	if !st.Mode().IsRegular() {
		return Upload{}, fmt.Errorf("not a regular file: %s", path)
	}
	if st.Size() > c.maxUpload {
		return Upload{}, fmt.Errorf("%s is %d bytes, above the %d byte upload limit", path, st.Size(), c.maxUpload)
	}

	name := filepath.Base(path)
	target := c.base + "/uploads.json?filename=" + url.QueryEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, f)
	if err != nil {
		return Upload{}, err
	}
	req.ContentLength = st.Size()
	req.Header.Set("X-Redmine-API-Key", c.key)
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := c.hc.Do(req)
	if err != nil {
		return Upload{}, fmt.Errorf("redmine: uploading %s: %w", name, err)
	}
	defer resp.Body.Close()

	var r struct {
		Upload struct {
			Token string `json:"token"`
		} `json:"upload"`
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var b json.RawMessage
		json.NewDecoder(resp.Body).Decode(&b)
		return Upload{}, parseError(resp.StatusCode, b)
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return Upload{}, fmt.Errorf("redmine: decoding upload response: %w", err)
	}
	if r.Upload.Token == "" {
		return Upload{}, fmt.Errorf("redmine: upload of %s returned no token", name)
	}
	return Upload{Token: r.Upload.Token, Filename: name}, nil
}
