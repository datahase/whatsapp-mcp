package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/datahase/whatsapp-mcp/internal/ai"
	"github.com/datahase/whatsapp-mcp/internal/config"
)

// runSmoke checks provider access with local fixture files. It is the only
// code path that may use the test key (AI_TEST_API_KEY), and it never reads
// WhatsApp data.
func runSmoke(args []string) int {
	fs := flag.NewFlagSet("ai-smoke", flag.ContinueOnError)
	path := configFlag(fs)
	tier := fs.String("tier", "", "paid (AI_API_KEY) or test (AI_TEST_API_KEY); required")
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
	var key, tkey string
	switch *tier {
	case "paid":
		key, tkey = cfg.AI.APIKey, cfg.AI.TranscribeAPIKey
	case "test":
		key, tkey = cfg.AI.TestAPIKey, cfg.AI.TestAPIKey
	default:
		fmt.Fprintln(os.Stderr, "-tier must be paid or test")
		return 2
	}
	if key == "" {
		fmt.Fprintf(os.Stderr, "no key for tier %s (set the provider key, e.g. GEMINI_API_KEY, or AI_TEST_API_KEY)\n", *tier)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	out, err := ai.Smoke(ctx, cfg.AI, key, tkey, *audio, *image, *target)
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
