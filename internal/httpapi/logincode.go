package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// LoginCodeTTL is how long a one-time sign-in code stays valid.
const LoginCodeTTL = 10 * time.Minute

// codeAlphabet avoids look-alike characters (0/O, 1/I/L).
const codeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

func codeDir(dataDir string) string { return filepath.Join(dataDir, "service", "login-codes") }

// normalizeCode accepts codes typed with or without the dash, in any case.
func normalizeCode(c string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(c)))
}

func codeFile(dataDir, code string) string {
	sum := sha256.Sum256([]byte(normalizeCode(code)))
	return filepath.Join(codeDir(dataDir), hex.EncodeToString(sum[:]))
}

// IssueLoginCode creates a single-use admin sign-in code, shown as
// XXXX-XXXX. Only its hash is stored, in the data volume the running server
// reads, so `whatsapp-mcp login-code` works from `docker compose exec`.
func IssueLoginCode(dataDir string) (string, error) {
	var b strings.Builder
	for i := 0; i < 8; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(codeAlphabet))))
		if err != nil {
			return "", err
		}
		b.WriteByte(codeAlphabet[n.Int64()])
	}
	code := b.String()[:4] + "-" + b.String()[4:]
	if err := os.MkdirAll(codeDir(dataDir), 0o700); err != nil {
		return "", err
	}
	exp := strconv.FormatInt(time.Now().Add(LoginCodeTTL).Unix(), 10)
	if err := os.WriteFile(codeFile(dataDir, code), []byte(exp), 0o600); err != nil {
		return "", err
	}
	pruneCodes(dataDir)
	return code, nil
}

// consumeLoginCode reports whether code is valid and unexpired, and
// deletes it either way so it can never be used twice.
func consumeLoginCode(dataDir, code string) bool {
	if dataDir == "" || len(normalizeCode(code)) != 8 {
		return false
	}
	p := codeFile(dataDir, code)
	raw, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	if os.Remove(p) != nil {
		return false // another request consumed it first
	}
	exp, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	return err == nil && time.Now().Unix() <= exp
}

// pruneCodes deletes expired codes.
func pruneCodes(dataDir string) {
	entries, err := os.ReadDir(codeDir(dataDir))
	if err != nil {
		return
	}
	now := time.Now().Unix()
	for _, e := range entries {
		p := filepath.Join(codeDir(dataDir), e.Name())
		raw, err := os.ReadFile(p)
		if exp, perr := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64); err != nil || perr != nil || exp < now {
			_ = os.Remove(p)
		}
	}
}
