package client

import (
	"bytes"
	"github.com/datahase/whatsapp-mcp/internal/security"
	"strings"
	"testing"
)

func TestProtocolLoggerRedactsPairingAndNestedLogs(t *testing.T) {
	var out bytes.Buffer
	raw := &stderrLogger{out: &out, mod: "test", min: 0}
	logger := redactingLogger{Logger: raw, redactor: &security.Redactor{}}
	logger.Infof("Successfully paired %s", "15550001234:43@s.whatsapp.net")
	logger.Sub("Socket").Warnf("failure for %s phone %s", "15550001234@lid", "+15550001234")
	got := out.String()
	if strings.Contains(got, "15550001234") || strings.Contains(got, "@s.whatsapp.net") || !strings.Contains(got, "Successfully paired") || !strings.Contains(got, "test/Socket") {
		t.Fatalf("protocol logs not redacted: %s", got)
	}
}
