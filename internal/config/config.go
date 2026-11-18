package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config is the whole process configuration. One process serves one Redmine.
type Config struct {
	URL          string
	APIKey       string
	Insecure     bool
	ReadOnly     bool
	Timeout      time.Duration
	DefaultLimit int
	MetaTTL      time.Duration
	MaxUpload    int64
	TextFormat   string // textile | markdown, only used to phrase the prompt hint

	Transport string // stdio | http
	Addr      string
	Path      string
	Token     string

	// Client-side upload staging (HTTP transport only).
	UploadDir  string
	UploadPath string
	UploadTTL  time.Duration
}

func FromEnv() (*Config, error) {
	c := &Config{
		URL:        strings.TrimRight(os.Getenv("REDMINE_URL"), "/"),
		APIKey:     os.Getenv("REDMINE_API_KEY"),
		Transport:  env("MCP_TRANSPORT", "stdio"),
		Addr:       env("MCP_ADDR", "127.0.0.1:8080"),
		Path:       env("MCP_PATH", "/mcp"),
		Token:      os.Getenv("MCP_TOKEN"),
		TextFormat: env("REDMINE_TEXT_FORMATTING", "textile"),

		UploadDir:  uploadDir(),
		UploadPath: env("MCP_UPLOAD_PATH", "/upload"),
	}
	var err error
	if c.Insecure, err = envBool("REDMINE_INSECURE"); err != nil {
		return nil, err
	}
	if c.ReadOnly, err = envBool("REDMINE_READ_ONLY"); err != nil {
		return nil, err
	}
	if c.Timeout, err = envDur("REDMINE_TIMEOUT", 45*time.Second); err != nil {
		return nil, err
	}
	if c.MetaTTL, err = envDur("REDMINE_META_TTL", 5*time.Minute); err != nil {
		return nil, err
	}
	if c.UploadTTL, err = envDur("MCP_UPLOAD_TTL", 15*time.Minute); err != nil {
		return nil, err
	}
	if c.UploadTTL == 0 {
		c.UploadTTL = 15 * time.Minute
	}
	n, err := envInt("REDMINE_DEFAULT_LIMIT", 25)
	if err != nil {
		return nil, err
	}
	c.DefaultLimit = n
	m, err := envInt("REDMINE_MAX_UPLOAD", 25<<20)
	if err != nil {
		return nil, err
	}
	c.MaxUpload = int64(m)
	return c, c.Validate()
}

func (c *Config) Validate() error {
	if c.URL == "" {
		return fmt.Errorf("REDMINE_URL is required")
	}
	u, err := url.Parse(c.URL)
	if err != nil {
		return fmt.Errorf("REDMINE_URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("REDMINE_URL must start with http:// or https://")
	}
	if u.Host == "" {
		return fmt.Errorf("REDMINE_URL has no host")
	}
	if c.APIKey == "" {
		return fmt.Errorf("REDMINE_API_KEY is required")
	}
	if c.DefaultLimit < 1 || c.DefaultLimit > 100 {
		return fmt.Errorf("REDMINE_DEFAULT_LIMIT must be 1..100, got %d", c.DefaultLimit)
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("REDMINE_TIMEOUT must be positive")
	}
	if c.TextFormat != "textile" && c.TextFormat != "markdown" {
		return fmt.Errorf("REDMINE_TEXT_FORMATTING must be textile or markdown, got %q", c.TextFormat)
	}
	switch c.Transport {
	case "stdio":
	case "http":
		if !strings.HasPrefix(c.Path, "/") {
			return fmt.Errorf("MCP_PATH must start with /")
		}
		addrs := c.ListenAddrs()
		if len(addrs) == 0 {
			return fmt.Errorf("MCP_ADDR contains no addresses")
		}
		for _, a := range addrs {
			if _, _, err := net.SplitHostPort(a); err != nil {
				return fmt.Errorf("MCP_ADDR %q: %w", a, err)
			}
		}
		// An unauthenticated non-loopback listener is allowed (trusted LAN deployments); main
		// warns about it at startup. A token, when set, is always enforced.
		if c.UploadDir != "" {
			if !strings.HasPrefix(c.UploadPath, "/") || c.UploadPath == "/" {
				return fmt.Errorf("MCP_UPLOAD_PATH must start with / and must not be /")
			}
			if c.UploadPath == c.Path {
				return fmt.Errorf("MCP_UPLOAD_PATH must differ from MCP_PATH (%q)", c.Path)
			}
			if c.UploadTTL <= 0 {
				return fmt.Errorf("MCP_UPLOAD_TTL must be positive")
			}
		}
	default:
		return fmt.Errorf("MCP_TRANSPORT must be stdio or http, got %q", c.Transport)
	}
	return nil
}

// ListenAddrs splits MCP_ADDR on commas; empty entries are ignored.
func (c *Config) ListenAddrs() []string {
	var out []string
	for _, a := range strings.Split(c.Addr, ",") {
		if a = strings.TrimSpace(a); a != "" {
			out = append(out, a)
		}
	}
	return out
}

// IsPlainHTTP reports whether the Redmine URL is unencrypted, for a startup warning.
func (c *Config) IsPlainHTTP() bool { return strings.HasPrefix(c.URL, "http://") }

// UnauthenticatedListenAddr returns the first non-loopback listener when no bearer token is
// configured, or "" when every listener is loopback or a token is set. Used for a startup warning:
// the HTTP transport is reachable but has no authentication.
func (c *Config) UnauthenticatedListenAddr() string {
	if c.Token != "" {
		return ""
	}
	for _, a := range c.ListenAddrs() {
		if !loopback(a) {
			return a
		}
	}
	return ""
}

func loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// uploadDir honours an explicitly empty MCP_UPLOAD_DIR, which disables the staging route.
func uploadDir() string {
	if v, ok := os.LookupEnv("MCP_UPLOAD_DIR"); ok {
		return v
	}
	return filepath.Join(os.TempDir(), "redmine-mcp-uploads")
}

func envBool(key string) (bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s: %w", key, err)
	}
	return b, nil
}

func envInt(key string, def int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return n, nil
}

func envDur(key string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return d, nil
}
