// Command whatsapp-mcp serves several personal WhatsApp accounts over MCP
// from one process.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/datahase/whatsapp-mcp/internal/accounts"
	"github.com/datahase/whatsapp-mcp/internal/ai"
	"github.com/datahase/whatsapp-mcp/internal/authflow"
	"github.com/datahase/whatsapp-mcp/internal/client"
	"github.com/datahase/whatsapp-mcp/internal/config"
	"github.com/datahase/whatsapp-mcp/internal/httpapi"
	mcpsrv "github.com/datahase/whatsapp-mcp/internal/mcp"
	"github.com/datahase/whatsapp-mcp/internal/security"
	"github.com/datahase/whatsapp-mcp/internal/store"
)

// Version is overridden at build time via -ldflags "-X main.Version=...".
var Version = "dev"

const usage = `whatsapp-mcp: personal WhatsApp accounts over MCP

Usage:
  whatsapp-mcp serve        [-config FILE]   run the service
  whatsapp-mcp check-config [-config FILE]   validate configuration and secret files
  whatsapp-mcp healthcheck  [-url URL]       liveness probe used by the container
  whatsapp-mcp login-code   [-config FILE]   print a one-time admin sign-in code (10 minutes)
  whatsapp-mcp ai-smoke     [-config FILE] -tier paid|test [-audio FILE] [-image FILE]
                                             one provider check with NON-SENSITIVE fixtures only
  whatsapp-mcp -version

FILE defaults to $WHATSAPP_MCP_CONFIG or /config/config.yaml. The file is
optional; accounts can be added through the admin page; secrets come from environment
variables (see .env.example).
Set WHATSAPP_MCP_DEBUG=1 to log unredacted JIDs and message bodies (never in production).
`

func main() {
	args := os.Args[1:]
	for _, a := range args {
		if a == "-version" || a == "--version" {
			fmt.Printf("whatsapp-mcp %s\n", Version)
			return
		}
	}
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	mcpsrv.Version = Version
	var code int
	switch args[0] {
	case "serve":
		code = runServe(args[1:])
	case "check-config":
		code = runCheck(args[1:])
	case "healthcheck":
		code = runHealth(args[1:])
	case "login-code":
		code = runLoginCode(args[1:])
	case "ai-smoke", "gemini-smoke":
		code = runSmoke(args[1:])
	case "version":
		fmt.Printf("whatsapp-mcp %s\n", Version)
	default:
		fmt.Fprint(os.Stderr, usage)
		code = 2
	}
	os.Exit(code)
}

func configFlag(fs *flag.FlagSet) *string {
	def := os.Getenv("WHATSAPP_MCP_CONFIG")
	if def == "" {
		def = "/config/config.yaml"
	}
	return fs.String("config", def, "config file")
}

func runCheck(args []string) int {
	fs := flag.NewFlagSet("check-config", flag.ContinueOnError)
	path := configFlag(fs)
	if fs.Parse(args) != nil {
		return 2
	}
	cfg, err := config.Load(*path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("ok: %d account(s), analysis enabled=%v (provider %s), listen=%s\n", len(cfg.Accounts), cfg.AI.Enabled, cfg.AI.Provider, cfg.Listen)
	return 0
}

func runHealth(args []string) int {
	fs := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	u := fs.String("url", "http://127.0.0.1:8765/healthz", "health URL")
	if fs.Parse(args) != nil {
		return 2
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(*u)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return 1
	}
	return 0
}

func runServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	path := configFlag(fs)
	if fs.Parse(args) != nil {
		return 2
	}
	cfg, err := config.Load(*path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		return 1
	}
	redactor := &security.Redactor{Debug: os.Getenv("WHATSAPP_MCP_DEBUG") == "1"}
	log := client.NewStderrLogger("main", cfg.LogLevel, false)
	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "accounts"), 0o700); err != nil {
		log.Errorf("data dir: %v", err)
		return 1
	}

	serviceLock, err := store.TryLock(cfg.DataDir)
	if err != nil {
		log.Errorf("data directory is in use: %v", err)
		return 1
	}
	defer serviceLock.Release()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// whatsmeow's protocol version is library-global: set it once, before
	// any account connects.
	client.InitWAVersion(ctx, log)

	mgr := accounts.NewManager(cfg, redactor)
	if err := mgr.LoadRegistry(); err != nil {
		log.Errorf("account registry: %v", err)
		return 1
	}
	mgr.Start(ctx)
	defer mgr.Stop()

	// Created after the manager so its deferred Close (which stops running
	// jobs) runs before account stores close.
	var gem *ai.Service
	if cfg.AI.Enabled {
		gem, err = ai.NewService(cfg.AI, filepath.Join(cfg.DataDir, "service"), cfg.Limits.MaxQueuedJobs, client.NewStderrLogger("ai", cfg.LogLevel, false))
		if err != nil {
			log.Errorf("analysis: %v", err)
			return 1
		}
		defer gem.Close()
	}

	var oauth *authflow.Server
	if cfg.PublicURL != "" {
		oauth, err = authflow.Open(filepath.Join(cfg.DataDir, "service"), cfg.PublicURL)
		if err != nil {
			log.Errorf("OAuth: %v", err)
			return 1
		}
		defer oauth.Close()
		public, _ := url.Parse(cfg.PublicURL)
		cfg.AllowedHosts = append(cfg.AllowedHosts, public.Hostname())
	}
	m := mcpsrv.NewServer(mgr, gem, cfg.Limits.MaxInlineBytes)
	h := httpapi.New(httpapi.Config{OAuth: oauth, OAuthOnly: cfg.OAuthOnly, Token: cfg.Auth.Token, AdminPassword: cfg.Auth.AdminPassword, AllowedHosts: cfg.AllowedHosts,
		MaxUpload: cfg.Limits.MaxMediaBytes, Manager: mgr, MCP: m.Handler(), AI: gem, Version: Version, Log: log, DataDir: cfg.DataDir})
	srv := &http.Server{Addr: cfg.Listen, Handler: h, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 10 * time.Minute,
		IdleTimeout: 120 * time.Second, MaxHeaderBytes: 64 << 10}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	log.Infof("whatsapp-mcp %s listening on %s (MCP /mcp, admin /admin); %d account(s), analysis=%v", Version, cfg.Listen, len(mgr.All()), gem != nil)

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Errorf("http: %v", err)
			return 1
		}
	}
	log.Infof("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(sctx)
	return 0
}

// runLoginCode prints a one-time sign-in code for the admin page. Run it
// inside the container: docker compose exec whatsapp-mcp whatsapp-mcp login-code
func runLoginCode(args []string) int {
	fs := flag.NewFlagSet("login-code", flag.ContinueOnError)
	path := configFlag(fs)
	if fs.Parse(args) != nil {
		return 2
	}
	cfg, err := config.Load(*path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	code, err := httpapi.IssueLoginCode(cfg.DataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "could not create a code:", err)
		return 1
	}
	host := "127.0.0.1"
	if len(cfg.AllowedHosts) > 0 {
		host = cfg.AllowedHosts[0]
	} else if b := os.Getenv("BIND_IP"); b != "" && b != "0.0.0.0" {
		host = b
	}
	port := os.Getenv("HOST_PORT")
	if port == "" {
		port = "8765"
	}
	fmt.Printf("Sign-in code: %s  (works once, expires in %d minutes)\n", code, int(httpapi.LoginCodeTTL.Minutes()))
	fmt.Printf("Or open:      http://%s:%s/admin/login?code=%s\n", host, port, code)
	return 0
}
