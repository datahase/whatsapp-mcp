package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	p := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const base = `auth: {token_file: /t, admin_password_file: /a}
accounts:
  - id: personal
`

func TestDefaultsAndValidation(t *testing.T) {
	c, err := Load(write(t, base))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "0.0.0.0:8765" || c.Limits.MaxMediaBytes != 32<<20 || c.Limits.MaxInlineBytes != 2<<20 ||
		c.Gemini.DailyAudioMinutes != 30 || c.Gemini.DailyRequests != 50 || c.Gemini.MaxAudioSeconds != 600 ||
		c.Gemini.TranscribeModel != "gemini-3.5-transcribe" || c.Gemini.AnalysisModel != "gemini-3.5-flash-lite" ||
		c.Gemini.DefaultTargetLanguage != "en" || c.Accounts[0].Label != "personal" || c.Gemini.Enabled {
		t.Fatalf("defaults: %+v", c)
	}
	bad := map[string]string{
		"no token":         "auth: {admin_password_file: /a}\naccounts: [{id: a}]\n",
		"no accounts":      "auth: {token_file: /t, admin_password_file: /a}\n",
		"path-like id":     "auth: {token_file: /t, admin_password_file: /a}\naccounts: [{id: ../x}]\n",
		"duplicate id":     "auth: {token_file: /t, admin_password_file: /a}\naccounts: [{id: a}, {id: a}]\n",
		"duplicate phone":  "auth: {token_file: /t, admin_password_file: /a}\naccounts: [{id: a, expected_phone: '+15551234567'}, {id: b, expected_phone: '15551234567'}]\n",
		"gemini no key":    base + "gemini: {enabled: true}\n",
		"unknown field":    base + "secret_key: abc\n",
		"inline too large": base + "limits: {max_media_bytes: 10, max_inline_bytes: 20}\n",
	}
	for name, body := range bad {
		if _, err := Load(write(t, body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestReadSecretNeverLeaksValue(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s")
	_ = os.WriteFile(p, []byte("short\n"), 0o600)
	_, err := ReadSecret(p)
	if err == nil || strings.Contains(err.Error(), "short") {
		t.Fatalf("short secret: %v", err)
	}
	_ = os.WriteFile(p, []byte("  a-long-enough-secret-value \n"), 0o600)
	if v, err := ReadSecret(p); err != nil || v != "a-long-enough-secret-value" {
		t.Fatalf("%q %v", v, err)
	}
}
