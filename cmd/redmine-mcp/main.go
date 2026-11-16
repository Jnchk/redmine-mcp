package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"redmine-mcp/internal/config"
	"redmine-mcp/internal/redmine"
	"redmine-mcp/internal/tools"
)

const version = "0.1.0"

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.FromEnv()
	if err != nil {
		logger.Error("configuration", "err", err)
		os.Exit(1)
	}

	fs := flag.NewFlagSet("redmine-mcp", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&cfg.Transport, "transport", cfg.Transport, "stdio or http")
	fs.StringVar(&cfg.Addr, "addr", cfg.Addr, "HTTP listen address")
	fs.BoolVar(&cfg.ReadOnly, "read-only", cfg.ReadOnly, "disable write tools")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if err := cfg.Validate(); err != nil {
		logger.Error("configuration", "err", err)
		os.Exit(1)
	}
	if cfg.IsPlainHTTP() {
		logger.Warn("REDMINE_URL is not https: the API key travels in clear text")
	}
	if cfg.Insecure {
		logger.Warn("TLS verification is disabled (REDMINE_INSECURE)")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client, err := redmine.New(cfg.URL, cfg.APIKey, cfg.Timeout, cfg.Insecure, cfg.MaxUpload)
	if err != nil {
		logger.Error("client", "err", err)
		os.Exit(1)
	}
	meta := redmine.NewMeta(client, cfg.MetaTTL)

	// Fail fast: proves URL, key, TLS and sub-URI before a client talks to us.
	checkCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	user, err := client.CurrentUser(checkCtx)
	cancel()
	if err != nil {
		logger.Error("cannot reach Redmine", "url", cfg.URL, "err", err)
		os.Exit(1)
	}
	logger.Info("connected", "url", cfg.URL, "login", user.Login, "admin", user.Admin, "read_only", cfg.ReadOnly)

	srv := mcp.NewServer(&mcp.Implementation{Name: "redmine-mcp", Version: version}, &mcp.ServerOptions{
		Instructions: instructions(cfg, user),
	})
	tools.Register(srv, &tools.Deps{
		C:            client,
		M:            meta,
		ReadOnly:     cfg.ReadOnly,
		DefaultLimit: cfg.DefaultLimit,
		TextFormat:   cfg.TextFormat,
	})

	switch cfg.Transport {
	case "stdio":
		err = srv.Run(ctx, &mcp.StdioTransport{})
	case "http":
		handler := mcp.NewStreamableHTTPHandler(
			func(*http.Request) *mcp.Server { return srv }, // one server for every session
			&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
		)
		mux := http.NewServeMux()
		mux.Handle(cfg.Path, withToken(cfg.Token, handler))
		hs := &http.Server{Addr: cfg.Addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
		go func() {
			<-ctx.Done()
			shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
			defer done()
			_ = hs.Shutdown(shutdown)
		}()
		logger.Info("listening", "addr", cfg.Addr, "path", cfg.Path)
		err = hs.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
	}
	if err != nil {
		logger.Error("server", "err", err)
		os.Exit(1)
	}
}

func instructions(cfg *config.Config, u *redmine.User) string {
	mode := "read-write"
	if cfg.ReadOnly {
		mode = "read-only (write tools return an error)"
	}
	content := `Textile: *bold*, h3. heading with a blank line before and after, "text":https://url links, * item lists. Never Markdown.`
	if cfg.TextFormat == "markdown" {
		content = "Markdown: **bold**, ## headings, [text](url) links."
	}
	return fmt.Sprintf(`Redmine at %s. Signed in as %s. Mode: %s.
Content format: %s
- Issue ids are integers; chain calls with the ids printed in results.
- Project, tracker, status, priority, activity and user fields accept names or ids; names are resolved server-side. "me" works for assignee/user.
- search_issues defaults to open issues. Use q for full-text search over descriptions and notes; q cannot be combined with filters.
- Prefer a small limit and summarize; paginate with offset only when the user needs more.
- update_issue needs confirm: true to close an issue or replace its description.
- After a create or update, show the issue URL to the user.`,
		cfg.URL, u.Login, mode, content)
}

func withToken(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		if len(got) > 7 && got[:7] == "Bearer " {
			got = got[7:]
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
