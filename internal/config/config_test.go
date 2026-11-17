package config

import (
	"strings"
	"testing"
	"time"
)

func cleanEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"REDMINE_URL", "REDMINE_API_KEY", "REDMINE_INSECURE", "REDMINE_READ_ONLY",
		"REDMINE_TIMEOUT", "REDMINE_META_TTL", "REDMINE_DEFAULT_LIMIT", "REDMINE_MAX_UPLOAD",
		"REDMINE_TEXT_FORMATTING", "MCP_TRANSPORT", "MCP_ADDR", "MCP_PATH", "MCP_TOKEN",
	} {
		t.Setenv(k, "")
	}
	t.Setenv("REDMINE_URL", "https://redmine.example.com/")
	t.Setenv("REDMINE_API_KEY", "secret")
}

func TestFromEnvDefaults(t *testing.T) {
	cleanEnv(t)
	c, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if c.URL != "https://redmine.example.com" {
		t.Fatalf("trailing slash not trimmed: %q", c.URL)
	}
	if c.Transport != "stdio" || c.Addr != "127.0.0.1:8080" || c.Path != "/mcp" {
		t.Fatalf("transport defaults: %#v", c)
	}
	if c.Timeout != 45*time.Second || c.MetaTTL != 5*time.Minute {
		t.Fatalf("duration defaults: %v %v", c.Timeout, c.MetaTTL)
	}
	if c.DefaultLimit != 25 || c.MaxUpload != 25<<20 || c.TextFormat != "textile" {
		t.Fatalf("numeric defaults: %#v", c)
	}
}

func TestFromEnvValidation(t *testing.T) {
	cases := []struct {
		name  string
		env   map[string]string
		want  string // substring of the error; "" means no error
		plain bool
	}{
		{name: "missing key", env: map[string]string{"REDMINE_API_KEY": ""}, want: "REDMINE_API_KEY"},
		{name: "missing url", env: map[string]string{"REDMINE_URL": ""}, want: "REDMINE_URL"},
		{name: "bad scheme", env: map[string]string{"REDMINE_URL": "ftp://h"}, want: "http://"},
		{name: "no host", env: map[string]string{"REDMINE_URL": "https://"}, want: "no host"},
		{name: "bad transport", env: map[string]string{"MCP_TRANSPORT": "sse"}, want: "MCP_TRANSPORT"},
		{name: "non-loopback without token", env: map[string]string{"MCP_TRANSPORT": "http", "MCP_ADDR": "0.0.0.0:8080"}, want: "MCP_TOKEN"},
		{name: "non-loopback with token", env: map[string]string{"MCP_TRANSPORT": "http", "MCP_ADDR": "0.0.0.0:8080", "MCP_TOKEN": "t"}, want: ""},
		{name: "loopback without token", env: map[string]string{"MCP_TRANSPORT": "http"}, want: ""},
		{name: "multi loopback without token", env: map[string]string{"MCP_TRANSPORT": "http", "MCP_ADDR": "127.0.0.1:8080, [::1]:8081"}, want: ""},
		{name: "multi with one non-loopback", env: map[string]string{"MCP_TRANSPORT": "http", "MCP_ADDR": "127.0.0.1:8080,0.0.0.0:9090"}, want: "MCP_TOKEN"},
		{name: "multi with token", env: map[string]string{"MCP_TRANSPORT": "http", "MCP_ADDR": "127.0.0.1:8080,0.0.0.0:9090", "MCP_TOKEN": "t"}, want: ""},
		{name: "missing port", env: map[string]string{"MCP_TRANSPORT": "http", "MCP_ADDR": "127.0.0.1"}, want: "MCP_ADDR"},
		{name: "empty entries ignored", env: map[string]string{"MCP_TRANSPORT": "http", "MCP_ADDR": " , 127.0.0.1:8080 , "}, want: ""},
		{name: "only separators", env: map[string]string{"MCP_TRANSPORT": "http", "MCP_ADDR": " , , "}, want: "no addresses"},
		{name: "hostname loopback", env: map[string]string{"MCP_TRANSPORT": "http", "MCP_ADDR": "localhost:9000"}, want: ""},
		{name: "path without slash", env: map[string]string{"MCP_TRANSPORT": "http", "MCP_PATH": "mcp"}, want: "MCP_PATH"},
		{name: "limit too small", env: map[string]string{"REDMINE_DEFAULT_LIMIT": "0"}, want: "REDMINE_DEFAULT_LIMIT"},
		{name: "limit too large", env: map[string]string{"REDMINE_DEFAULT_LIMIT": "101"}, want: "REDMINE_DEFAULT_LIMIT"},
		{name: "bad duration", env: map[string]string{"REDMINE_TIMEOUT": "45"}, want: "REDMINE_TIMEOUT"},
		{name: "zero timeout", env: map[string]string{"REDMINE_TIMEOUT": "0s"}, want: "positive"},
		{name: "markdown", env: map[string]string{"REDMINE_TEXT_FORMATTING": "markdown"}, want: ""},
		{name: "bogus format", env: map[string]string{"REDMINE_TEXT_FORMATTING": "asciidoc"}, want: "TEXT_FORMATTING"},
		{name: "plain http", env: map[string]string{"REDMINE_URL": "http://h"}, want: "", plain: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cleanEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			c, err := FromEnv()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if c.IsPlainHTTP() != tc.plain {
					t.Fatalf("IsPlainHTTP = %v", c.IsPlainHTTP())
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestListenAddrs(t *testing.T) {
	cases := map[string][]string{
		"":                           nil,
		"127.0.0.1:8080":             {"127.0.0.1:8080"},
		"127.0.0.1:8080,10.0.0.5:80": {"127.0.0.1:8080", "10.0.0.5:80"},
		" a:1 , b:2 ,":               {"a:1", "b:2"},
		" , ":                        nil,
	}
	for in, want := range cases {
		got := (&Config{Addr: in}).ListenAddrs()
		if len(got) != len(want) {
			t.Fatalf("ListenAddrs(%q) = %v, want %v", in, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("ListenAddrs(%q) = %v, want %v", in, got, want)
			}
		}
	}
}
