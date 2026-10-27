// Command whatsapp-mcp serves several personal WhatsApp accounts over MCP
// from one process.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/datahase/whatsapp-mcp/internal/accounts"
	"github.com/datahase/whatsapp-mcp/internal/ai"
	"github.com/datahase/whatsapp-mcp/internal/client"
	"github.com/datahase/whatsapp-mcp/internal/config"
	"github.com/datahase/whatsapp-mcp/internal/httpapi"
	mcpsrv "github.com/datahase/whatsapp-mcp/internal/mcp"
	"github.com/datahase/whatsapp-mcp/internal/security"
)

// Version is overridden at build time via -ldflags "-X main.Version=...".
var Version = "dev"

const usage = `whatsapp-mcp: personal WhatsApp accounts over MCP

Usage:
  whatsapp-mcp serve        [-config FILE]   run the service
  whatsapp-mcp check-config [-config FILE]   validate configuration and secret files
  whatsapp-mcp healthcheck  [-url URL]       liveness probe used by the container
  whatsapp-mcp ai-smoke     [-config FILE] -tier paid|test [-audio FILE] [-image FILE]
                                             one provider check with NON-SENSITIVE fixtures only
  whatsapp-mcp -version

FILE defaults to $WHATSAPP_MCP_CONFIG or /config/config.yaml. The file is
optional when WHATSAPP_MCP_ACCOUNTS is set; secrets come from environment
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

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// whatsmeow's protocol version is library-global: set it once, before
	// any account connects.
	client.InitWAVersion(ctx, log)

	mgr := accounts.NewManager(cfg, redactor)
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

	m := mcpsrv.NewServer(mgr, gem, cfg.Limits.MaxInlineBytes)
	h := httpapi.New(httpapi.Config{Token: cfg.Auth.Token, AdminPassword: cfg.Auth.AdminPassword, AllowedHosts: cfg.AllowedHosts,
		MaxUpload: cfg.Limits.MaxMediaBytes, Manager: mgr, MCP: m.Handler(), AI: gem, Version: Version, Log: log})
	srv := &http.Server{Addr: cfg.Listen, Handler: h, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 10 * time.Minute,
		IdleTimeout: 120 * time.Second, MaxHeaderBytes: 64 << 10}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	log.Infof("whatsapp-mcp %s listening on %s (MCP /mcp, admin /admin); %d account(s), analysis=%v", Version, cfg.Listen, len(cfg.Accounts), gem != nil)

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
