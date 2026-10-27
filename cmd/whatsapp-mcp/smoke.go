package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/datahase/whatsapp-mcp/internal/config"
	"github.com/datahase/whatsapp-mcp/internal/gemini"
)

// runSmoke checks provider access with local fixture files. It is the only
// code path that may use the test (free) key, and it never reads WhatsApp
// data.
func runSmoke(args []string) int {
	fs := flag.NewFlagSet("gemini-smoke", flag.ContinueOnError)
	path := configFlag(fs)
	tier := fs.String("tier", "", "paid or test (required)")
	audio := fs.String("audio", "", "non-sensitive audio fixture")
	image := fs.String("image", "", "non-sensitive image fixture")
	target := fs.String("translate-to", "en", "translation target for the audio transcript ('' to skip)")
	if fs.Parse(args) != nil {
		return 2
	}
	if *audio == "" && *image == "" {
		fmt.Fprintln(os.Stderr, "provide -audio and/or -image fixture files")
		return 2
	}
	cfg, err := config.Load(*path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var keyFile string
	switch *tier {
	case "paid":
		keyFile = cfg.Gemini.PaidKeyFile
	case "test":
		keyFile = cfg.Gemini.TestKeyFile
	default:
		fmt.Fprintln(os.Stderr, "-tier must be paid or test")
		return 2
	}
	key, err := config.ReadSecret(keyFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	out, err := gemini.Smoke(ctx, cfg.Gemini.BaseURL, key, cfg.Gemini.TranscribeModel, cfg.Gemini.AnalysisModel, *audio, *image, *target)
	if out == nil {
		out = map[string]any{}
	}
	out["tier"] = *tier
	if err != nil {
		out["error"] = err.Error()
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
	if err != nil {
		return 1
	}
	return 0
}
