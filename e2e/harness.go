//go:build e2e

// Package e2e starts the real binary with a temporary configuration and
// speaks MCP JSON-RPC to it over HTTP. No WhatsApp account is paired.
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	token   = "e2e-token-0123456789abcdef"
	adminPW = "e2e-admin-0123456789abcdef"
)

type harness struct {
	t    *testing.T
	cmd  *exec.Cmd
	addr string
	dir  string
	bin  string
}

func buildBinary(t *testing.T) string {
	t.Helper()
	wd, _ := os.Getwd()
	bin := filepath.Join(t.TempDir(), "whatsapp-mcp")
	b := exec.Command("go", "build", "-o", bin, "./cmd/whatsapp-mcp")
	b.Dir = filepath.Dir(wd)
	if out, err := b.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	_ = os.WriteFile(filepath.Join(dir, "token"), []byte(token+"\n"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "admin"), []byte(adminPW+"\n"), 0o600)
	cfg := fmt.Sprintf(`listen: %q
data_dir: %q
log_level: WARN
auth:
  token_file: %q
  admin_password_file: %q
accounts:
  - id: alpha
    label: Alpha
  - id: beta
    label: Beta
`, addr, filepath.Join(dir, "data"), filepath.Join(dir, "token"), filepath.Join(dir, "admin"))
	_ = os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0o600)
	h := &harness{t: t, addr: addr, dir: dir, bin: buildBinary(t)}
	h.start()
	return h
}

func (h *harness) start() {
	h.t.Helper()
	h.cmd = exec.Command(h.bin, "serve", "-config", filepath.Join(h.dir, "config.yaml"))
	h.cmd.Stdout, h.cmd.Stderr = os.Stderr, os.Stderr
	if err := h.cmd.Start(); err != nil {
		h.t.Fatal(err)
	}
	cmd := h.cmd
	h.t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get("http://" + h.addr + "/healthz"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	h.t.Fatal("service not ready")
}

// stop sends SIGTERM and returns the exit code.
func (h *harness) stop() int {
	_ = h.cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- h.cmd.Wait() }()
	select {
	case err := <-done:
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		return 0
	case <-time.After(15 * time.Second):
		h.t.Fatal("no graceful shutdown within 15s")
		return -1
	}
}

func (h *harness) post(path string, body []byte, hdr map[string]string) (*http.Response, []byte) {
	h.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, "http://"+h.addr+path, bytes.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}

// rpc calls an MCP method and returns the decoded envelope and response.
func (h *harness) rpc(method string, params map[string]any, extra map[string]string) (map[string]any, *http.Response) {
	h.t.Helper()
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	hdr := map[string]string{"Authorization": "Bearer " + token, "Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
	for k, v := range extra {
		hdr[k] = v
	}
	resp, raw := h.post("/mcp", b, hdr)
	if resp.StatusCode != 200 {
		h.t.Fatalf("%s: %d %s", method, resp.StatusCode, raw)
	}
	s := string(raw)
	for _, line := range strings.Split(s, "\n") {
		if l := strings.TrimSpace(line); strings.HasPrefix(l, "data: ") {
			s = strings.TrimPrefix(l, "data: ")
			break
		}
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		h.t.Fatalf("%s: decode %v: %s", method, err, raw)
	}
	return out, resp
}

func toolText(env map[string]any) (string, bool) {
	res, _ := env["result"].(map[string]any)
	c, _ := res["content"].([]any)
	isErr, _ := res["isError"].(bool)
	if len(c) == 0 {
		return "", isErr
	}
	txt, _ := c[0].(map[string]any)["text"].(string)
	return txt, isErr
}
