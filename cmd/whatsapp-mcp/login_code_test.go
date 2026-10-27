package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoginCodeUsesConfiguredHTTPSOrigin(t *testing.T) {
	t.Setenv("WHATSAPP_MCP_DATA_DIR", t.TempDir())
	t.Setenv("WHATSAPP_MCP_TOKEN", strings.Repeat("t", 32))
	t.Setenv("WHATSAPP_MCP_ADMIN_PASSWORD", strings.Repeat("p", 32))
	t.Setenv("WHATSAPP_MCP_PUBLIC_URL", "https://example.test/")
	t.Setenv("AI_ENABLED", "false")
	previous := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { os.Stdout = previous; reader.Close(); writer.Close() }()
	os.Stdout = writer
	code := runLoginCode([]string{"-config", filepath.Join(t.TempDir(), "missing.yaml")})
	writer.Close()
	raw, _ := io.ReadAll(reader)
	if code != 0 || !strings.Contains(string(raw), "https://example.test/admin/login?code=") || strings.Contains(string(raw), "http://") {
		t.Fatalf("expected configured HTTPS origin in sign-in output; exit=%d", code)
	}
}
