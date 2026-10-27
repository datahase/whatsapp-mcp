package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	tok = "token-0123456789abcdef"
	adm = "admin-0123456789abcdef"
)

func write(t *testing.T, body string) string {
	p := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// baseEnv sets the minimum environment for a file-less configuration.
func baseEnv(t *testing.T) {
	t.Setenv("WHATSAPP_MCP_TOKEN", tok)
	t.Setenv("WHATSAPP_MCP_ADMIN_PASSWORD", adm)
	t.Setenv("WHATSAPP_MCP_ACCOUNTS", "personal:Personal, work:Work number")
}

func missing(t *testing.T) string { return filepath.Join(t.TempDir(), "none.yaml") }

func TestEnvOnlyConfigAndDefaults(t *testing.T) {
	baseEnv(t)
	t.Setenv("WHATSAPP_MCP_ALLOWED_HOSTS", "192.168.1.20, my.host")
	c, err := Load(missing(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Accounts) != 2 || c.Accounts[1].ID != "work" || c.Accounts[1].Label != "Work number" ||
		c.Auth.Token != tok || c.Auth.AdminPassword != adm || len(c.AllowedHosts) != 2 {
		t.Fatalf("env config: %+v", c)
	}
	if c.Listen != "0.0.0.0:8765" || c.Limits.MaxMediaBytes != 32<<20 || c.AI.Enabled || c.AI.Provider != "gemini" ||
		c.AI.TranscribeModel != "gemini-3.5-transcribe" || c.AI.AnalysisModel != "gemini-3.5-flash-lite" || c.AI.DailyRequests != 50 {
		t.Fatalf("defaults: %+v", c)
	}
}

func TestProviderPresetsAndKeys(t *testing.T) {
	baseEnv(t)
	t.Setenv("AI_ENABLED", "true")

	// OpenRouter: chat transcription (WAV), models must be named.
	t.Setenv("AI_PROVIDER", "openrouter")
	t.Setenv("OPENROUTER_API_KEY", "sk-or-v1-0123456789abcdef")
	t.Setenv("AI_TRANSCRIBE_MODEL", "google/gemini-3.5-flash")
	t.Setenv("AI_MODEL", "google/gemini-3.5-flash-lite")
	c, err := Load(missing(t))
	if err != nil {
		t.Fatal(err)
	}
	if a := c.AI; a.BaseURL != "https://openrouter.ai/api/v1" || a.TranscribeProvider != "openrouter" || a.TranscribeMethod != "chat" ||
		a.AudioFormat != "wav" || a.APIKey != "sk-or-v1-0123456789abcdef" || a.TranscribeAPIKey != a.APIKey {
		t.Fatalf("openrouter: %+v", a)
	}
	t.Setenv("AI_MODEL", "meta-llama/llama-4:free")
	if _, err := Load(missing(t)); err == nil || !strings.Contains(err.Error(), "free") {
		t.Fatalf("free model: %v", err)
	}
	t.Setenv("AI_ALLOW_FREE_MODELS", "true")
	if _, err := Load(missing(t)); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AI_ALLOW_FREE_MODELS", "")
	t.Setenv("AI_TRANSCRIBE_MODEL", "")
	t.Setenv("AI_MODEL", "")

	// Groq: native audio/transcriptions with a default Whisper model; AI_MODEL required.
	t.Setenv("AI_PROVIDER", "groq")
	t.Setenv("GROQ_API_KEY", "gsk_0123456789abcdef")
	if _, err := Load(missing(t)); err == nil || !strings.Contains(err.Error(), "AI_MODEL") {
		t.Fatalf("groq without AI_MODEL: %v", err)
	}
	t.Setenv("AI_MODEL", "some-vision-model")
	c, err = Load(missing(t))
	if err != nil || c.AI.TranscribeModel != "whisper-large-v3" || c.AI.TranscribeMethod != "audio_transcriptions" || c.AI.AudioFormat != "original" {
		t.Fatalf("groq: %v %+v", err, c.AI)
	}

	// Anthropic cannot transcribe: it needs a separate transcription provider
	// with its own key; the Anthropic key is never reused for it.
	t.Setenv("AI_PROVIDER", "anthropic")
	t.Setenv("AI_MODEL", "")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-0123456789abcdef")
	if _, err := Load(missing(t)); err == nil || !strings.Contains(err.Error(), "AI_TRANSCRIBE_PROVIDER") {
		t.Fatalf("anthropic without transcriber: %v", err)
	}
	t.Setenv("AI_TRANSCRIBE_PROVIDER", "anthropic")
	if _, err := Load(missing(t)); err == nil || !strings.Contains(err.Error(), "does not accept audio") {
		t.Fatalf("anthropic as transcriber: %v", err)
	}
	t.Setenv("AI_TRANSCRIBE_PROVIDER", "gemini")
	t.Setenv("AI_API_KEY", "generic-0123456789abcdef")
	if _, err := Load(missing(t)); err == nil || !strings.Contains(err.Error(), "GEMINI_API_KEY") {
		t.Fatalf("transcriber must not borrow the generic/main key: %v", err)
	}
	t.Setenv("GEMINI_API_KEY", "AIza-0123456789abcdef")
	c, err = Load(missing(t))
	if err != nil {
		t.Fatal(err)
	}
	if a := c.AI; a.AnalysisModel != "claude-opus-5-5" || a.TranscribeModel != "gemini-3.5-transcribe" || a.APIKey != "sk-ant-0123456789abcdef" ||
		a.TranscribeAPIKey != "AIza-0123456789abcdef" || a.TranscribeBaseURL != "https://generativelanguage.googleapis.com" || a.BaseURL != "https://api.anthropic.com" {
		t.Fatalf("anthropic+gemini: %+v", a)
	}

	// Dedicated speech-to-text with its own key, paired with an LLM provider.
	t.Setenv("AI_TRANSCRIBE_PROVIDER", "deepgram")
	t.Setenv("DEEPGRAM_API_KEY", "dg-0123456789abcdef")
	c, err = Load(missing(t))
	if err != nil || c.AI.TranscribeModel != "nova-3" || c.AI.TranscribeAPIKey != "dg-0123456789abcdef" || c.AI.TranscribeBaseURL != "https://api.deepgram.com" || c.AI.AudioFormat != "original" {
		t.Fatalf("deepgram: %v %+v", err, c.AI)
	}
	t.Setenv("AI_PROVIDER", "deepgram")
	if _, err := Load(missing(t)); err == nil || !strings.Contains(err.Error(), "only does speech-to-text") {
		t.Fatalf("deepgram as main provider: %v", err)
	}
	t.Setenv("AI_PROVIDER", "anthropic")
	for prov, keyVar := range map[string]string{"elevenlabs": "ELEVENLABS_API_KEY", "assemblyai": "ASSEMBLYAI_API_KEY"} {
		t.Setenv("AI_TRANSCRIBE_PROVIDER", prov)
		if _, err := Load(missing(t)); err == nil || !strings.Contains(err.Error(), keyVar) {
			t.Fatalf("%s without key: %v", prov, err)
		}
		t.Setenv(keyVar, "k-0123456789abcdef")
		if _, err := Load(missing(t)); err != nil {
			t.Fatalf("%s: %v", prov, err)
		}
	}

	// 0.1 meaning of provider "openai" with a custom base URL.
	t.Setenv("AI_PROVIDER", "openai")
	t.Setenv("AI_TRANSCRIBE_PROVIDER", "")
	t.Setenv("AI_BASE_URL", "http://localhost:9000/v1")
	t.Setenv("AI_TRANSCRIBE_MODEL", "whisper")
	t.Setenv("AI_MODEL", "local-model")
	if c, err = Load(missing(t)); err != nil || c.AI.Provider != "openai_compatible" || c.AI.APIKey != "generic-0123456789abcdef" {
		t.Fatalf("legacy openai: %v %+v", err, c.AI)
	}
}

func TestSecretPrecedenceAndValidation(t *testing.T) {
	baseEnv(t)
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "k")
	_ = os.WriteFile(keyFile, []byte("file-key-0123456789abcdef\n"), 0o600)
	t.Setenv("AI_ENABLED", "true")
	t.Setenv("AI_API_KEY_FILE", keyFile)
	c, err := Load(missing(t))
	if err != nil || c.AI.APIKey != "file-key-0123456789abcdef" {
		t.Fatalf("_FILE: %v %q", err, c.AI.APIKey)
	}
	t.Setenv("AI_API_KEY", "env-key-0123456789abcdef")
	if c, _ := Load(missing(t)); c.AI.APIKey != "env-key-0123456789abcdef" {
		t.Fatal("env value must win over _FILE")
	}

	bad := map[string]map[string]string{
		"short token":     {"WHATSAPP_MCP_TOKEN": "short"},
		"same secrets":    {"WHATSAPP_MCP_ADMIN_PASSWORD": tok},
		"no token":        {"WHATSAPP_MCP_TOKEN": ""},
		"path-like id":    {"WHATSAPP_MCP_ACCOUNTS": "../x"},
		"duplicate id":    {"WHATSAPP_MCP_ACCOUNTS": "a,a"},
		"bad provider":    {"AI_PROVIDER": "acme"},
		"bad method":      {"AI_PROVIDER": "openai_compatible", "AI_BASE_URL": "http://x/v1", "AI_TRANSCRIBE_METHOD": "magic"},
		"enabled no key":  {"AI_API_KEY": "", "AI_API_KEY_FILE": "", "GEMINI_API_KEY": ""},
		"bad bool":        {"AI_ENABLED": "maybe"},
		"bad audio fmt":   {"AI_AUDIO_FORMAT": "mp3"},
		"negative limits": {"AI_DAILY_REQUESTS": "-1"},
	}
	for name, vars := range bad {
		t.Run(name, func(t *testing.T) {
			for k, v := range vars {
				t.Setenv(k, v)
			}
			if _, err := Load(missing(t)); err == nil {
				t.Errorf("accepted")
			} else if strings.Contains(err.Error(), tok) || strings.Contains(err.Error(), "env-key") {
				t.Errorf("error leaks a secret: %v", err)
			}
		})
	}
}

func TestYAMLFileAndLegacyGeminiSection(t *testing.T) {
	t.Setenv("WHATSAPP_MCP_TOKEN", tok)
	t.Setenv("WHATSAPP_MCP_ADMIN_PASSWORD", adm)
	key := filepath.Join(t.TempDir(), "k")
	_ = os.WriteFile(key, []byte("legacy-key-0123456789abcdef"), 0o600)
	c, err := Load(write(t, "accounts: [{id: personal}]\ngemini:\n  enabled: true\n  paid_key_file: "+key+"\n"))
	if err != nil || !c.AI.Enabled || c.AI.APIKey != "legacy-key-0123456789abcdef" {
		t.Fatalf("legacy gemini section: %v %+v", err, c.AI)
	}
	if _, err := Load(write(t, "accounts: [{id: a}]\nsecret_key: x\n")); err == nil {
		t.Fatal("unknown YAML field accepted")
	}
	if _, err := Load(missing(t)); err == nil {
		t.Fatal("missing file and no WHATSAPP_MCP_ACCOUNTS accepted")
	}
	// Environment overrides the file.
	t.Setenv("AI_MODEL", "gemini-3.8-flash")
	c, _ = Load(write(t, "accounts: [{id: a}]\nai: {analysis_model: gemini-3.5-flash-lite}\n"))
	if c.AI.AnalysisModel != "gemini-3.8-flash" {
		t.Fatalf("env override: %s", c.AI.AnalysisModel)
	}
}

func TestReadSecretNeverLeaksValue(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s")
	_ = os.WriteFile(p, []byte("short\n"), 0o600)
	_, err := ReadSecret(p)
	if err == nil || strings.Contains(err.Error(), "short") {
		t.Fatalf("short secret: %v", err)
	}
}
