// Package config loads and validates the service configuration.
//
// Everything can come from environment variables (for example a Compose
// .env file); an optional YAML file adds advanced settings. Secrets are read
// from VAR, then VAR_FILE, then the *_file path in YAML, and are never
// written back anywhere.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the whole service configuration.
type Config struct {
	PublicURL    string    `yaml:"public_url"`
	OAuthOnly    bool      `yaml:"oauth_only"`
	Listen       string    `yaml:"listen"`
	DataDir      string    `yaml:"data_dir"`
	AllowedHosts []string  `yaml:"allowed_hosts"`
	Auth         Auth      `yaml:"auth"`
	Accounts     []Account `yaml:"accounts"`
	Limits       Limits    `yaml:"limits"`
	AI           AI        `yaml:"ai"`
	// LegacyGemini accepts the 0.1.0 `gemini:` section.
	LegacyGemini *AI    `yaml:"gemini"`
	LogLevel     string `yaml:"log_level"`
}

// Auth holds the resolved secrets (never from YAML values directly).
type Auth struct {
	TokenFile         string `yaml:"token_file"`
	AdminPasswordFile string `yaml:"admin_password_file"`
	Token             string `yaml:"-"`
	AdminPassword     string `yaml:"-"`
}

// Account is one linked WhatsApp slot. ID is opaque and stable; it names the
// data directory, so it is restricted to a safe character set.
type Account struct {
	ID            string `yaml:"id"`
	Label         string `yaml:"label"`
	ExpectedPhone string `yaml:"expected_phone"`
	Disabled      bool   `yaml:"disabled"`
}

// Limits bounds work and storage. Zero values are replaced by defaults.
type Limits struct {
	SendMinInterval       time.Duration `yaml:"send_min_interval"`
	SendsPerHour          int           `yaml:"sends_per_hour"`
	NonContactMinInterval time.Duration `yaml:"non_contact_min_interval"`
	NonContactPerHour     int           `yaml:"non_contact_per_hour"`
	MaxMediaBytes         int64         `yaml:"max_media_bytes"`
	MaxInlineBytes        int64         `yaml:"max_inline_bytes"`
	MediaCacheBytes       int64         `yaml:"media_cache_bytes"`
	StagedUploadTTL       time.Duration `yaml:"staged_upload_ttl"`
	MessageCacheBytes     int64         `yaml:"message_cache_bytes"`
	MaxQueuedJobs         int           `yaml:"max_queued_jobs"`
}

// AI configures optional analysis. Provider handles translation and image
// analysis; TranscribeProvider handles transcription (default: the same
// provider). Supported: gemini, openai, groq, openrouter, anthropic (no
// audio) and openai_compatible (any other OpenAI-shaped API). The API keys
// are the ones used for personal media; the test key is only used by the
// fixture smoke command.
type AI struct {
	Enabled               bool          `yaml:"enabled"`
	Provider              string        `yaml:"provider"`
	BaseURL               string        `yaml:"base_url"`
	TranscribeProvider    string        `yaml:"transcribe_provider"`
	TranscribeBaseURL     string        `yaml:"transcribe_base_url"`
	APIKeyFile            string        `yaml:"api_key_file"`
	PaidKeyFile           string        `yaml:"paid_key_file"` // 0.1.0 name of api_key_file
	TestKeyFile           string        `yaml:"test_key_file"`
	TranscribeModel       string        `yaml:"transcribe_model"`
	AnalysisModel         string        `yaml:"analysis_model"`
	QualityModel          string        `yaml:"quality_model"`
	TranscribeMethod      string        `yaml:"transcribe_method"` // openai: chat or audio_transcriptions
	AudioFormat           string        `yaml:"audio_format"`      // original or wav
	AllowFreeModels       bool          `yaml:"allow_free_models"`
	DefaultTargetLanguage string        `yaml:"default_target_language"`
	DailyAudioMinutes     float64       `yaml:"daily_audio_minutes"`
	DailyRequests         int           `yaml:"daily_requests"`
	MaxAudioSeconds       int           `yaml:"max_audio_seconds"`
	MaxOutputTokens       int           `yaml:"max_output_tokens"`
	RequestTimeout        time.Duration `yaml:"request_timeout"`
	WaitForResult         time.Duration `yaml:"wait_for_result"`
	APIKey                string        `yaml:"-"` // for Provider
	TranscribeAPIKey      string        `yaml:"-"` // for TranscribeProvider
	TestAPIKey            string        `yaml:"-"`
}

// preset holds a provider's defaults. An empty model means "must be set".
type preset struct {
	base, transcribeModel, model, quality, method, keyEnv string
}

// Presets are each provider's native endpoint and defaults. Model defaults
// are given only where they are stable; check the provider's model list.
var presets = map[string]preset{
	"gemini":            {"https://generativelanguage.googleapis.com", "gemini-3.5-transcribe", "gemini-3.5-flash-lite", "gemini-3.8-flash", "native", "GEMINI_API_KEY"},
	"openai":            {"https://api.openai.com/v1", "gpt-4o-transcribe", "", "", "audio_transcriptions", "OPENAI_API_KEY"},
	"groq":              {"https://api.groq.com/openai/v1", "whisper-large-v3", "", "", "audio_transcriptions", "GROQ_API_KEY"},
	"openrouter":        {"https://openrouter.ai/api/v1", "", "", "", "chat", "OPENROUTER_API_KEY"},
	"anthropic":         {"https://api.anthropic.com", "", "claude-opus-5-5", "", "none", "ANTHROPIC_API_KEY"},
	"openai_compatible": {"", "", "", "", "chat", "AI_API_KEY"},
	// Speech-to-text only (AI_TRANSCRIBE_PROVIDER).
	"deepgram":   {"https://api.deepgram.com", "nova-3", "", "", "native", "DEEPGRAM_API_KEY"},
	"elevenlabs": {"https://api.elevenlabs.io", "scribe_v2", "", "", "native", "ELEVENLABS_API_KEY"},
	"assemblyai": {"https://api.assemblyai.com", "universal-3-5-pro", "", "", "native", "ASSEMBLYAI_API_KEY"},
}

// transcribeOnly providers cannot translate or analyze images.
var transcribeOnly = map[string]bool{"deepgram": true, "elevenlabs": true, "assemblyai": true}

// ProviderNames lists supported providers (for messages).
const ProviderNames = "gemini, openai, groq, openrouter, anthropic or openai_compatible"

// TranscribeProviderNames lists providers that can transcribe.
const TranscribeProviderNames = "gemini, openai, groq, openrouter, openai_compatible, deepgram, elevenlabs or assemblyai"

var accountIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
var phoneRe = regexp.MustCompile(`^[0-9]{6,15}$`)

// Load reads the optional YAML file at path, applies environment variables,
// defaults and secrets, and validates the result. A missing file is fine;
// fresh installations add accounts through the authenticated admin UI.
func Load(path string) (*Config, error) {
	var c Config
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		dec := yaml.NewDecoder(bytes.NewReader(raw))
		dec.KnownFields(true)
		if err := dec.Decode(&c); err != nil && err.Error() != "EOF" {
			return nil, fmt.Errorf("parse config %s: %w", path, err)
		}
	case os.IsNotExist(err): // Fresh installs add accounts through the admin UI.
	default:
		return nil, fmt.Errorf("read config: %w", err)
	}
	if c.LegacyGemini != nil {
		if c.AI != (AI{}) {
			return nil, errors.New("config: use either ai: or the older gemini: section, not both")
		}
		c.AI = *c.LegacyGemini
		c.LegacyGemini = nil
	}
	if err := c.applyEnv(); err != nil {
		return nil, err
	}
	c.applyDefaults()
	if err := c.resolveSecrets(); err != nil {
		return nil, err
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func env(key string) (string, bool) {
	v, ok := os.LookupEnv(key)
	v = strings.TrimSpace(v)
	return v, ok && v != ""
}

func setStr(dst *string, keys ...string) {
	for _, k := range keys {
		if v, ok := env(k); ok {
			*dst = v
			return
		}
	}
}

// applyEnv lets every commonly changed setting come from the environment,
// which overrides the YAML file. Empty variables are treated as unset.
func (c *Config) applyEnv() error {
	if v, ok := env("WHATSAPP_MCP_PUBLIC_URL"); ok {
		c.PublicURL = v
	}
	if v, ok := env("WHATSAPP_MCP_OAUTH_ONLY"); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return errors.New("WHATSAPP_MCP_OAUTH_ONLY must be true or false")
		}
		c.OAuthOnly = b
	}
	var errs []error
	if v, ok := env("WHATSAPP_MCP_ACCOUNTS"); ok {
		accts, err := parseAccounts(v)
		if err != nil {
			errs = append(errs, err)
		}
		c.Accounts = accts
	}
	if v, ok := env("WHATSAPP_MCP_ALLOWED_HOSTS"); ok {
		c.AllowedHosts = splitList(v)
	}
	setStr(&c.Listen, "WHATSAPP_MCP_LISTEN")
	setStr(&c.DataDir, "WHATSAPP_MCP_DATA_DIR")
	setStr(&c.LogLevel, "WHATSAPP_MCP_LOG_LEVEL")

	a := &c.AI
	if v, ok := env("AI_ENABLED"); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("AI_ENABLED: use true or false"))
		}
		a.Enabled = b
	}
	setStr(&a.Provider, "AI_PROVIDER")
	setStr(&a.BaseURL, "AI_BASE_URL")
	setStr(&a.TranscribeProvider, "AI_TRANSCRIBE_PROVIDER")
	setStr(&a.TranscribeBaseURL, "AI_TRANSCRIBE_BASE_URL")
	setStr(&a.TranscribeModel, "AI_TRANSCRIBE_MODEL")
	setStr(&a.AnalysisModel, "AI_MODEL")
	setStr(&a.QualityModel, "AI_QUALITY_MODEL")
	setStr(&a.TranscribeMethod, "AI_TRANSCRIBE_METHOD")
	setStr(&a.AudioFormat, "AI_AUDIO_FORMAT")
	setStr(&a.DefaultTargetLanguage, "AI_TARGET_LANGUAGE")
	if v, ok := env("AI_ALLOW_FREE_MODELS"); ok {
		a.AllowFreeModels, _ = strconv.ParseBool(v)
	}
	for key, dst := range map[string]*int{"AI_DAILY_REQUESTS": &a.DailyRequests, "AI_MAX_AUDIO_SECONDS": &a.MaxAudioSeconds} {
		if v, ok := env(key); ok {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				errs = append(errs, fmt.Errorf("%s: positive integer required", key))
			}
			*dst = n
		}
	}
	if v, ok := env("AI_DAILY_AUDIO_MINUTES"); ok {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f <= 0 {
			errs = append(errs, errors.New("AI_DAILY_AUDIO_MINUTES: positive number required"))
		}
		a.DailyAudioMinutes = f
	}
	return errors.Join(errs...)
}

// parseAccounts reads "id:Label,id2:Label 2" (labels optional).
func parseAccounts(v string) ([]Account, error) {
	var out []Account
	for _, item := range splitList(v) {
		id, label, _ := strings.Cut(item, ":")
		out = append(out, Account{ID: strings.TrimSpace(id), Label: strings.TrimSpace(label)})
	}
	if len(out) == 0 {
		return nil, errors.New("WHATSAPP_MCP_ACCOUNTS: list at least one account, e.g. personal:Personal,work:Work")
	}
	return out, nil
}

func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (c *Config) applyDefaults() {
	def := func(v *string, d string) {
		if *v == "" {
			*v = d
		}
	}
	def(&c.Listen, "0.0.0.0:8765")
	def(&c.DataDir, "/data")
	def(&c.LogLevel, "INFO")
	l := &c.Limits
	if l.SendMinInterval == 0 {
		l.SendMinInterval = 45 * time.Second
	}
	if l.SendsPerHour == 0 {
		l.SendsPerHour = 30
	}
	if l.NonContactMinInterval == 0 {
		l.NonContactMinInterval = 90 * time.Second
	}
	if l.NonContactPerHour == 0 {
		l.NonContactPerHour = 15
	}
	if l.MaxMediaBytes == 0 {
		l.MaxMediaBytes = 32 << 20
	}
	if l.MaxInlineBytes == 0 {
		l.MaxInlineBytes = 2 << 20
	}
	if l.MediaCacheBytes == 0 {
		l.MediaCacheBytes = 512 << 20
	}
	if l.StagedUploadTTL == 0 {
		l.StagedUploadTTL = 24 * time.Hour
	}
	if l.MessageCacheBytes == 0 {
		l.MessageCacheBytes = 256 << 20
	}
	if l.MaxQueuedJobs == 0 {
		l.MaxQueuedJobs = 8
	}
	a := &c.AI
	a.Provider = strings.ToLower(a.Provider)
	a.TranscribeProvider = strings.ToLower(a.TranscribeProvider)
	if a.Provider == "openai" && a.BaseURL != "" && !strings.Contains(a.BaseURL, "api.openai.com") {
		a.Provider = "openai_compatible" // 0.1 meaning of "openai": any OpenAI-shaped API
	}
	def(&a.Provider, "gemini")
	if a.TranscribeProvider == "" && a.Provider != "anthropic" && !transcribeOnly[a.Provider] {
		a.TranscribeProvider = a.Provider
	}
	main, trans := presets[a.Provider], presets[a.TranscribeProvider]
	def(&a.BaseURL, main.base)
	if a.TranscribeProvider == a.Provider {
		def(&a.TranscribeBaseURL, a.BaseURL)
	}
	def(&a.TranscribeBaseURL, trans.base)
	a.BaseURL = strings.TrimRight(a.BaseURL, "/")
	a.TranscribeBaseURL = strings.TrimRight(a.TranscribeBaseURL, "/")
	def(&a.TranscribeModel, trans.transcribeModel)
	def(&a.AnalysisModel, main.model)
	def(&a.QualityModel, main.quality)
	if trans.method == "native" || trans.method == "none" {
		a.TranscribeMethod = trans.method
	} else {
		def(&a.TranscribeMethod, trans.method)
	}
	// Chat input_audio is only universally accepted as WAV (OpenAI and
	// Google's OpenAI endpoint reject Ogg); native Gemini and
	// /audio/transcriptions take WhatsApp's Ogg/Opus as is.
	if a.TranscribeMethod == "chat" {
		def(&a.AudioFormat, "wav")
	}
	def(&a.AudioFormat, "original")
	def(&a.DefaultTargetLanguage, "en")
	if a.DailyAudioMinutes == 0 {
		a.DailyAudioMinutes = 30
	}
	if a.DailyRequests == 0 {
		a.DailyRequests = 50
	}
	if a.MaxAudioSeconds == 0 {
		a.MaxAudioSeconds = 600
	}
	if a.MaxOutputTokens == 0 {
		a.MaxOutputTokens = 4096
	}
	if a.RequestTimeout == 0 {
		a.RequestTimeout = 120 * time.Second
	}
	if a.WaitForResult == 0 {
		a.WaitForResult = 45 * time.Second
	}
}

// secret resolves VAR, then VAR_FILE, then the YAML file path.
func secret(envName, yamlFile string, required bool) (string, error) {
	if v, ok := env(envName); ok {
		return checkSecret(envName, v)
	}
	file := yamlFile
	if f, ok := env(envName + "_FILE"); ok {
		file = f
	}
	if file == "" {
		if required {
			return "", fmt.Errorf("%s is required (set it in .env, or %s_FILE to a file)", envName, envName)
		}
		return "", nil
	}
	v, err := ReadSecret(file)
	if err != nil {
		return "", fmt.Errorf("%s: %w", envName, err)
	}
	return v, nil
}

// providerKey resolves a provider's key: <PROVIDER>_API_KEY (or _FILE),
// then, for the main provider only, the generic AI_API_KEY (or _FILE) and a
// YAML key file. A separate transcription provider never borrows the main
// provider's key.
func providerKey(provider, yamlFile string, required, allowGeneric bool) (string, error) {
	pr, ok := presets[provider]
	if !ok {
		return "", nil // reported by Validate
	}
	v, err := "", error(nil)
	if pr.keyEnv != "AI_API_KEY" {
		if v, err = secret(pr.keyEnv, "", false); err != nil || v != "" {
			return v, err
		}
	}
	if allowGeneric || pr.keyEnv == "AI_API_KEY" {
		if !allowGeneric {
			yamlFile = ""
		}
		if v, err = secret("AI_API_KEY", yamlFile, false); err != nil || v != "" {
			return v, err
		}
	}
	if required {
		return "", fmt.Errorf("%s is required for provider %s", pr.keyEnv, provider)
	}
	return "", nil
}

func checkSecret(name, v string) (string, error) {
	if len(v) < 16 {
		return "", fmt.Errorf("%s: value must be at least 16 characters", name)
	}
	return v, nil
}

func (c *Config) resolveSecrets() error {
	var errs []error
	var err error
	if c.Auth.Token, err = secret("WHATSAPP_MCP_TOKEN", c.Auth.TokenFile, true); err != nil {
		errs = append(errs, err)
	}
	if c.Auth.AdminPassword, err = secret("WHATSAPP_MCP_ADMIN_PASSWORD", c.Auth.AdminPasswordFile, true); err != nil {
		errs = append(errs, err)
	}
	if c.Auth.Token != "" && c.Auth.Token == c.Auth.AdminPassword {
		errs = append(errs, errors.New("WHATSAPP_MCP_TOKEN and WHATSAPP_MCP_ADMIN_PASSWORD must be different"))
	}
	a := &c.AI
	keyFile := a.APIKeyFile
	if keyFile == "" {
		keyFile = a.PaidKeyFile
	}
	if f, ok := env("GEMINI_API_KEY_PAID_FILE"); ok && keyFile == "" { // 0.1.0 variable
		keyFile = f
	}
	if a.APIKey, err = providerKey(a.Provider, keyFile, a.Enabled, true); err != nil {
		errs = append(errs, err)
	}
	switch {
	case a.TranscribeProvider == "":
	case a.TranscribeProvider == a.Provider:
		a.TranscribeAPIKey = a.APIKey
	default:
		if a.TranscribeAPIKey, err = providerKey(a.TranscribeProvider, "", a.Enabled, false); err != nil {
			errs = append(errs, err)
		}
	}
	if a.TestAPIKey, err = secret("AI_TEST_API_KEY", a.TestKeyFile, false); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// Validate reports every configuration problem at once.
func (c *Config) Validate() error {
	var errs []error
	if c.OAuthOnly && c.PublicURL == "" {
		errs = append(errs, errors.New("oauth_only requires public_url"))
	}
	ids := map[string]bool{}
	phones := map[string]bool{}
	for i, a := range c.Accounts {
		if !accountIDRe.MatchString(a.ID) {
			errs = append(errs, fmt.Errorf("account %d id %q: use 1-32 chars of a-z, 0-9, _ or -", i+1, a.ID))
		}
		if ids[a.ID] {
			errs = append(errs, fmt.Errorf("account id %q is duplicated", a.ID))
		}
		ids[a.ID] = true
		if a.ExpectedPhone != "" {
			p := strings.TrimPrefix(a.ExpectedPhone, "+")
			if !phoneRe.MatchString(p) {
				errs = append(errs, fmt.Errorf("account %q expected_phone: digits only (country code first)", a.ID))
			}
			if phones[p] {
				errs = append(errs, fmt.Errorf("account %q expected_phone is configured for two accounts", a.ID))
			}
			phones[p] = true
			c.Accounts[i].ExpectedPhone = p
		}
		if a.Label == "" {
			c.Accounts[i].Label = a.ID
		}
	}
	if c.Limits.MaxInlineBytes > c.Limits.MaxMediaBytes {
		errs = append(errs, errors.New("limits.max_inline_bytes must not exceed limits.max_media_bytes"))
	}
	a := c.AI
	if _, ok := presets[a.Provider]; !ok {
		errs = append(errs, fmt.Errorf("AI_PROVIDER %q: use %s", a.Provider, ProviderNames))
	} else if transcribeOnly[a.Provider] {
		errs = append(errs, fmt.Errorf("AI_PROVIDER=%s only does speech-to-text: set AI_TRANSCRIBE_PROVIDER=%s and choose an AI_PROVIDER (%s) for translation and images", a.Provider, a.Provider, ProviderNames))
	}
	if a.TranscribeProvider == "anthropic" {
		errs = append(errs, errors.New("AI_TRANSCRIBE_PROVIDER=anthropic: Anthropic's API does not accept audio; use gemini, openai, groq or openrouter"))
	} else if _, ok := presets[a.TranscribeProvider]; !ok && a.TranscribeProvider != "" {
		errs = append(errs, fmt.Errorf("AI_TRANSCRIBE_PROVIDER %q: use %s", a.TranscribeProvider, TranscribeProviderNames))
	}
	if a.TranscribeMethod != "native" && a.TranscribeMethod != "none" && a.TranscribeMethod != "chat" && a.TranscribeMethod != "audio_transcriptions" {
		errs = append(errs, fmt.Errorf("AI_TRANSCRIBE_METHOD %q: use chat or audio_transcriptions", a.TranscribeMethod))
	}
	if a.AudioFormat != "original" && a.AudioFormat != "wav" {
		errs = append(errs, fmt.Errorf("AI_AUDIO_FORMAT %q: use original or wav", a.AudioFormat))
	}
	if a.Enabled {
		if a.Provider == "openai_compatible" && a.BaseURL == "" {
			errs = append(errs, errors.New("AI_BASE_URL is required for provider openai_compatible"))
		}
		if a.TranscribeProvider == "openai_compatible" && a.TranscribeBaseURL == "" {
			errs = append(errs, errors.New("AI_TRANSCRIBE_BASE_URL is required for transcribe provider openai_compatible"))
		}
		if a.TranscribeProvider == "" {
			errs = append(errs, errors.New("AI_TRANSCRIBE_PROVIDER is required when AI_PROVIDER=anthropic (Anthropic cannot transcribe audio); use gemini, openai, groq or openrouter"))
		}
		if a.TranscribeModel == "" {
			errs = append(errs, fmt.Errorf("AI_TRANSCRIBE_MODEL is required for transcribe provider %s", a.TranscribeProvider))
		}
		if a.AnalysisModel == "" {
			errs = append(errs, fmt.Errorf("AI_MODEL is required for provider %s", a.Provider))
		}
		if !a.AllowFreeModels {
			for _, m := range []string{a.TranscribeModel, a.AnalysisModel, a.QualityModel} {
				if strings.HasSuffix(m, ":free") {
					errs = append(errs, fmt.Errorf("model %q is a free variant, which may log or train on prompts; personal media needs a paid model (set AI_ALLOW_FREE_MODELS=true to override)", m))
				}
			}
		}
	}
	return errors.Join(errs...)
}

// ReadSecret reads a secret file and trims surrounding whitespace. Errors
// name the file but never include its contents.
func ReadSecret(path string) (string, error) {
	if path == "" {
		return "", errors.New("secret file path is empty")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read secret file %s: %w", path, err)
	}
	s := strings.TrimSpace(string(b))
	if len(s) < 16 {
		return "", fmt.Errorf("secret file %s: value must be at least 16 characters", path)
	}
	return s, nil
}
