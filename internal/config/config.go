// Package config loads and validates the service configuration. Secrets are
// never stored in the config file itself: it only names files that hold them.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the whole service configuration.
type Config struct {
	Listen       string    `yaml:"listen"`
	DataDir      string    `yaml:"data_dir"`
	AllowedHosts []string  `yaml:"allowed_hosts"`
	Auth         Auth      `yaml:"auth"`
	Accounts     []Account `yaml:"accounts"`
	Limits       Limits    `yaml:"limits"`
	Gemini       Gemini    `yaml:"gemini"`
	LogLevel     string    `yaml:"log_level"`
}

// Auth names the secret files. MCP/media use the bearer token; the admin page
// uses the admin password.
type Auth struct {
	TokenFile         string `yaml:"token_file"`
	AdminPasswordFile string `yaml:"admin_password_file"`
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

// Gemini configures optional analysis. Personal media always uses the paid
// key; the test key is only used by the fixture smoke command.
type Gemini struct {
	Enabled               bool          `yaml:"enabled"`
	PaidKeyFile           string        `yaml:"paid_key_file"`
	TestKeyFile           string        `yaml:"test_key_file"`
	BaseURL               string        `yaml:"base_url"`
	TranscribeModel       string        `yaml:"transcribe_model"`
	AnalysisModel         string        `yaml:"analysis_model"`
	QualityModel          string        `yaml:"quality_model"`
	DefaultTargetLanguage string        `yaml:"default_target_language"`
	DailyAudioMinutes     float64       `yaml:"daily_audio_minutes"`
	DailyRequests         int           `yaml:"daily_requests"`
	MaxAudioSeconds       int           `yaml:"max_audio_seconds"`
	MaxOutputTokens       int           `yaml:"max_output_tokens"`
	RequestTimeout        time.Duration `yaml:"request_timeout"`
	WaitForResult         time.Duration `yaml:"wait_for_result"`
}

var accountIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
var phoneRe = regexp.MustCompile(`^[0-9]{6,15}$`)

// Load reads, defaults and validates a config file. Environment variables
// WHATSAPP_MCP_TOKEN_FILE, WHATSAPP_MCP_ADMIN_PASSWORD_FILE and
// GEMINI_API_KEY_PAID_FILE override the corresponding file paths.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	envOverride(&c.Auth.TokenFile, "WHATSAPP_MCP_TOKEN_FILE")
	envOverride(&c.Auth.AdminPasswordFile, "WHATSAPP_MCP_ADMIN_PASSWORD_FILE")
	envOverride(&c.Gemini.PaidKeyFile, "GEMINI_API_KEY_PAID_FILE")
	envOverride(&c.Gemini.TestKeyFile, "GEMINI_API_KEY_TEST_FILE")
	c.applyDefaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func envOverride(dst *string, key string) {
	if v := os.Getenv(key); v != "" {
		*dst = v
	}
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
	g := &c.Gemini
	def(&g.BaseURL, "https://generativelanguage.googleapis.com")
	def(&g.TranscribeModel, "gemini-3.5-transcribe")
	def(&g.AnalysisModel, "gemini-3.5-flash-lite")
	def(&g.QualityModel, "gemini-3.8-flash")
	def(&g.DefaultTargetLanguage, "en")
	if g.DailyAudioMinutes == 0 {
		g.DailyAudioMinutes = 30
	}
	if g.DailyRequests == 0 {
		g.DailyRequests = 50
	}
	if g.MaxAudioSeconds == 0 {
		g.MaxAudioSeconds = 600
	}
	if g.MaxOutputTokens == 0 {
		g.MaxOutputTokens = 4096
	}
	if g.RequestTimeout == 0 {
		g.RequestTimeout = 120 * time.Second
	}
	if g.WaitForResult == 0 {
		g.WaitForResult = 45 * time.Second
	}
}

// Validate reports every configuration problem at once.
func (c *Config) Validate() error {
	var errs []error
	if c.Auth.TokenFile == "" {
		errs = append(errs, errors.New("auth.token_file is required (no credential-free mode)"))
	}
	if c.Auth.AdminPasswordFile == "" {
		errs = append(errs, errors.New("auth.admin_password_file is required"))
	}
	if len(c.Accounts) == 0 {
		errs = append(errs, errors.New("accounts: at least one account is required"))
	}
	ids := map[string]bool{}
	phones := map[string]bool{}
	for i, a := range c.Accounts {
		if !accountIDRe.MatchString(a.ID) {
			errs = append(errs, fmt.Errorf("accounts[%d].id %q: use 1-32 chars of a-z, 0-9, _ or -", i, a.ID))
		}
		if ids[a.ID] {
			errs = append(errs, fmt.Errorf("accounts[%d].id %q is duplicated", i, a.ID))
		}
		ids[a.ID] = true
		if a.ExpectedPhone != "" {
			p := strings.TrimPrefix(a.ExpectedPhone, "+")
			if !phoneRe.MatchString(p) {
				errs = append(errs, fmt.Errorf("accounts[%d].expected_phone: digits only (country code first)", i))
			}
			if phones[p] {
				errs = append(errs, fmt.Errorf("accounts[%d].expected_phone is configured for two accounts", i))
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
	if c.Gemini.Enabled && c.Gemini.PaidKeyFile == "" {
		errs = append(errs, errors.New("gemini.enabled requires gemini.paid_key_file (personal media never uses the free tier)"))
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
