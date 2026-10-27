//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"mime/multipart"
	"strings"
	"testing"
)

func TestE2E_AuthRequired(t *testing.T) {
	h := newHarness(t)
	resp, _ := h.post("/mcp", []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`), map[string]string{"Content-Type": "application/json"})
	if resp.StatusCode != 401 {
		t.Fatalf("unauthenticated MCP: %d", resp.StatusCode)
	}
}

// Legacy (pre-2026) clients: initialize, then list. No session is minted.
func TestE2E_LegacyInitializeAndList(t *testing.T) {
	h := newHarness(t)
	_, resp := h.rpc("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "e2e", "version": "1"}}, nil)
	if resp.Header.Get("Mcp-Session-Id") != "" {
		t.Fatal("stateless server minted a session")
	}
	env, _ := h.rpc("tools/list", map[string]any{}, nil)
	tools := env["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 16 {
		t.Fatalf("expected 16 tools, got %d", len(tools))
	}
}

// 2026-07-28 revision: no initialize; version in _meta and headers.
func TestE2E_Stateless2026(t *testing.T) {
	h := newHarness(t)
	meta := map[string]any{"io.modelcontextprotocol/protocolVersion": "2026-07-28", "io.modelcontextprotocol/clientCapabilities": map[string]any{}}
	for _, method := range []string{"server/discover", "tools/list", "tools/call"} {
		params := map[string]any{"_meta": meta}
		hdr := map[string]string{"Mcp-Protocol-Version": "2026-07-28", "Mcp-Method": method}
		if method == "tools/call" {
			params["name"], params["arguments"] = "list_chats", map[string]any{"account_id": "alpha"}
			hdr["Mcp-Name"] = "list_chats"
		}
		env, resp := h.rpc(method, params, hdr)
		if resp.Header.Get("Mcp-Session-Id") != "" {
			t.Fatalf("%s: session id minted", method)
		}
		if b, _ := json.Marshal(env); !strings.Contains(string(b), `"resultType":"complete"`) {
			t.Fatalf("%s: %s", method, b)
		}
	}
}

// Remote media journey without any server-local path: upload over HTTP,
// reference by media_id, fetch back through MCP + HTTP.
func TestE2E_RemoteMediaAndUnpairedSend(t *testing.T) {
	h := newHarness(t)
	var img bytes.Buffer
	_ = png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 4, 4)))
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	fw, _ := w.CreateFormFile("file", "laptop-photo.png")
	_, _ = fw.Write(img.Bytes())
	_ = w.Close()
	resp, raw := h.post("/api/accounts/alpha/media", body.Bytes(), map[string]string{"Authorization": "Bearer " + token, "Content-Type": w.FormDataContentType()})
	if resp.StatusCode != 201 {
		t.Fatalf("upload: %d %s", resp.StatusCode, raw)
	}
	var up struct {
		Media struct {
			ID string `json:"media_id"`
		} `json:"media"`
	}
	_ = json.Unmarshal(raw, &up)

	env, _ := h.rpc("tools/call", map[string]any{"name": "send_media", "arguments": map[string]any{
		"account_id": "alpha", "recipient": "15550001111", "media_id": up.Media.ID, "idempotency_key": "e2e-1"}}, nil)
	if txt, isErr := toolText(env); !isErr || !strings.Contains(txt, "not connected") {
		t.Fatalf("unpaired send must fail clearly: %s", txt)
	}
	env, _ = h.rpc("tools/call", map[string]any{"name": "send_media", "arguments": map[string]any{
		"account_id": "beta", "recipient": "15550001111", "media_id": up.Media.ID, "idempotency_key": "e2e-2"}}, nil)
	if txt, isErr := toolText(env); !isErr || strings.Contains(txt, "accepted") {
		t.Fatalf("cross-account media send: %s", txt)
	}
	env, _ = h.rpc("tools/call", map[string]any{"name": "get_media", "arguments": map[string]any{"account_id": "alpha", "media_id": up.Media.ID}}, nil)
	if txt, isErr := toolText(env); isErr || !strings.Contains(txt, "http://"+h.addr+"/api/accounts/alpha/media/"+up.Media.ID) {
		t.Fatalf("get_media: %s", txt)
	}
}

// SIGTERM stops cleanly and releases account locks so a restart works.
func TestE2E_GracefulShutdownAndRestart(t *testing.T) {
	h := newHarness(t)
	if code := h.stop(); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	h.start()
	env, _ := h.rpc("tools/call", map[string]any{"name": "list_accounts", "arguments": map[string]any{}}, nil)
	txt, _ := toolText(env)
	if strings.Contains(txt, `"state": "error"`) {
		t.Fatalf("account error after restart (lock not released?): %s", txt)
	}
}
