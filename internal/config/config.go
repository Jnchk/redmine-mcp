package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
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
		nonLoopback := ""
		for _, a := range addrs {
			if _, _, err := net.SplitHostPort(a); err != nil {
				return fmt.Errorf("MCP_ADDR %q: %w", a, err)
			}
			if nonLoopback == "" && !loopback(a) {
				nonLoopback = a
			}
		}
		if c.Token == "" && nonLoopback != "" {
			return fmt.Errorf("MCP_ADDR includes non-loopback %q: set MCP_TOKEN to require a bearer token", nonLoopback)
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
