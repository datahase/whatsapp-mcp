package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/datahase/whatsapp-mcp/internal/accounts"
	"github.com/datahase/whatsapp-mcp/internal/client"
	"github.com/datahase/whatsapp-mcp/internal/config"
	wamcp "github.com/datahase/whatsapp-mcp/internal/mcp"
	"github.com/datahase/whatsapp-mcp/internal/security"
	"github.com/datahase/whatsapp-mcp/internal/store"
)

const (
	token   = "test-token-0123456789abcdef"
	adminPW = "admin-password-0123456789"
)

func newTestServer(t *testing.T) (*httptest.Server, *accounts.Manager) {
	t.Helper()
	cfg := &config.Config{DataDir: t.TempDir(), LogLevel: "ERROR", Accounts: []config.Account{{ID: "alpha", Label: "A"}, {ID: "beta", Label: "B"}}}
	cfg.Limits.MaxMediaBytes, cfg.Limits.MaxInlineBytes = 64<<10, 32<<10
	cfg.Limits.MediaCacheBytes, cfg.Limits.MessageCacheBytes, cfg.Limits.StagedUploadTTL = 1<<20, 1<<30, time.Hour
	mgr := accounts.NewManager(cfg, &security.Redactor{})
	mgr.Start(context.Background())
	t.Cleanup(mgr.Stop)
	m := wamcp.NewServer(mgr, nil, cfg.Limits.MaxInlineBytes)
	srv := httptest.NewServer(New(Config{Token: token, AdminPassword: adminPW, MaxUpload: cfg.Limits.MaxMediaBytes,
		Manager: mgr, MCP: m.Handler(), Version: "test", Log: client.NewStderrLogger("t", "ERROR", false)}))
	t.Cleanup(srv.Close)
	return srv, mgr
}

func do(t *testing.T, method, u string, body io.Reader, hdr map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, u, body)
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func auth() map[string]string { return map[string]string{"Authorization": "Bearer " + token} }

func TestBoundaryRejections(t *testing.T) {
	srv, _ := newTestServer(t)
	cases := []struct {
		name   string
		method string
		path   string
		hdr    map[string]string
		want   int
	}{
		{"health is open", "GET", "/healthz", nil, 200},
		{"mcp needs token", "POST", "/mcp", nil, 401},
		{"wrong token", "POST", "/mcp", map[string]string{"Authorization": "Bearer nope"}, 401},
		{"token in query is not accepted", "GET", "/api/status?token=" + token, nil, 401},
		{"status with token", "GET", "/api/status", auth(), 200},
		{"bad host (DNS rebinding)", "GET", "/api/status", map[string]string{"Authorization": "Bearer " + token, "Host": "evil.example:8765"}, 421},
		{"bad origin", "POST", "/mcp", map[string]string{"Authorization": "Bearer " + token, "Origin": "https://evil.example"}, 403},
		{"unknown account", "GET", "/api/accounts/zeta/media/med_x", auth(), 404},
		{"admin needs session", "GET", "/admin", nil, 200}, // redirected to login page
	}
	for _, c := range cases {
		resp := do(t, c.method, srv.URL+c.path, nil, c.hdr)
		if resp.StatusCode != c.want {
			t.Errorf("%s: got %d want %d", c.name, resp.StatusCode, c.want)
		}
	}
	resp := do(t, "GET", srv.URL+"/admin", nil, nil)
	if resp.Request.URL.Path != "/admin/login" {
		t.Fatalf("admin without session ended at %s", resp.Request.URL.Path)
	}
}

func pngBytes() []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 3, 3)))
	return b.Bytes()
}

func multipartBody(t *testing.T, name string, data []byte) (io.Reader, string) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	fw, _ := w.CreateFormFile("file", name)
	_, _ = fw.Write(data)
	_ = w.Close()
	return &b, w.FormDataContentType()
}

func TestUploadDownloadRoundTripAndIsolation(t *testing.T) {
	srv, _ := newTestServer(t)
	data := pngBytes()
	body, ct := multipartBody(t, "photo.png", data)
	h := auth()
	h["Content-Type"] = ct
	resp := do(t, "POST", srv.URL+"/api/accounts/alpha/media", body, h)
	if resp.StatusCode != 201 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload %d %s", resp.StatusCode, b)
	}
	var up struct {
		Media store.MediaObject `json:"media"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&up)
	sum := sha256.Sum256(data)
	if up.Media.SHA256 != hex.EncodeToString(sum[:]) || up.Media.MediaType != "image" || up.Media.MIME != "image/png" {
		t.Fatalf("upload metadata %+v", up.Media)
	}
	dl := do(t, "GET", srv.URL+"/api/accounts/alpha/media/"+up.Media.ID, nil, auth())
	got, _ := io.ReadAll(dl.Body)
	if dl.StatusCode != 200 || !bytes.Equal(got, data) || dl.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(dl.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("download %d len=%d hdr=%v", dl.StatusCode, len(got), dl.Header)
	}
	if r := do(t, "GET", srv.URL+"/api/accounts/beta/media/"+up.Media.ID, nil, auth()); r.StatusCode != 404 {
		t.Fatalf("cross-account media fetch: %d", r.StatusCode)
	}
	if r := do(t, "GET", srv.URL+"/api/accounts/alpha/media/..%2F..%2Fwhatsapp.db", nil, auth()); r.StatusCode != 404 {
		t.Fatalf("path traversal media id: %d", r.StatusCode)
	}

	big, ct := multipartBody(t, "big.bin", make([]byte, 200<<10))
	h["Content-Type"] = ct
	if r := do(t, "POST", srv.URL+"/api/accounts/alpha/media", big, h); r.StatusCode != 413 {
		t.Fatalf("oversize upload: %d", r.StatusCode)
	}
	bad, ct := multipartBody(t, "x.png", append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 50)...))
	h["Content-Type"] = ct
	if r := do(t, "POST", srv.URL+"/api/accounts/alpha/media", bad, h); r.StatusCode != 400 {
		t.Fatalf("corrupt upload: %d", r.StatusCode)
	}
	raw := auth()
	raw["Content-Type"] = "application/octet-stream"
	if r := do(t, "POST", srv.URL+"/api/accounts/alpha/media?filename=raw.png", bytes.NewReader(data), raw); r.StatusCode != 201 {
		t.Fatalf("raw upload: %d", r.StatusCode)
	}
}

func TestAdminLoginSessionAndCSRF(t *testing.T) {
	srv, _ := newTestServer(t)
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	resp, err := c.PostForm(srv.URL+"/admin/login", url.Values{"password": {"wrong"}})
	if err != nil || resp.StatusCode != 401 {
		t.Fatalf("wrong password: %v %d", err, resp.StatusCode)
	}
	resp, err = c.PostForm(srv.URL+"/admin/login", url.Values{"password": {adminPW}})
	if err != nil || resp.StatusCode != 200 || resp.Request.URL.Path != "/admin" {
		t.Fatalf("login: %v %d %s", err, resp.StatusCode, resp.Request.URL.Path)
	}
	page, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(page), "alpha") || resp.Header.Get("Cache-Control") != "no-store" || resp.Header.Get("Content-Security-Policy") == "" {
		t.Fatal("admin page missing accounts or security headers")
	}
	u, _ := url.Parse(srv.URL + "/admin")
	ck := jar.Cookies(u)
	if len(ck) != 1 {
		t.Fatalf("cookies %v", ck)
	}
	// Cookie attributes are checked on the raw Set-Cookie header.
	req, _ := http.NewRequest("POST", srv.URL+"/admin/login", strings.NewReader("password="+adminPW))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	nr, _ := (&http.Transport{}).RoundTrip(req)
	sc := nr.Header.Get("Set-Cookie")
	for _, attr := range []string{"HttpOnly", "SameSite=Strict", "Path=/admin"} {
		if !strings.Contains(sc, attr) {
			t.Fatalf("cookie missing %s: %s", attr, sc)
		}
	}
	nr.Body.Close()

	// Without the CSRF token a state change is refused.
	resp, _ = c.PostForm(srv.URL+"/admin/accounts/alpha/reset", url.Values{"confirm": {"alpha"}})
	if resp.StatusCode != 403 {
		t.Fatalf("missing csrf: %d", resp.StatusCode)
	}
	acct, _ := c.Get(srv.URL + "/admin/accounts/alpha")
	body, _ := io.ReadAll(acct.Body)
	m := regexp.MustCompile(`name="csrf" value="([0-9a-f]+)"`).FindSubmatch(body)
	if m == nil {
		t.Fatal("no csrf token in page")
	}
	resp, _ = c.PostForm(srv.URL+"/admin/accounts/alpha/reset", url.Values{"csrf": {string(m[1])}, "confirm": {"alpha"}})
	if resp.StatusCode != 200 || !strings.Contains(resp.Request.URL.RawQuery, "reset") {
		t.Fatalf("reset with csrf: %d %s", resp.StatusCode, resp.Request.URL)
	}
	if strings.Contains(string(body), token) || strings.Contains(string(body), adminPW) {
		t.Fatal("secret rendered in admin page")
	}
}

func TestLoginRateLimit(t *testing.T) {
	srv, _ := newTestServer(t)
	for i := 0; i < 5; i++ {
		_, _ = http.PostForm(srv.URL+"/admin/login", url.Values{"password": {"x"}})
	}
	resp, _ := http.PostForm(srv.URL+"/admin/login", url.Values{"password": {adminPW}})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("after 5 failures: %d", resp.StatusCode)
	}
}

// rpc performs one stateless MCP JSON-RPC call over HTTP.
func rpc(t *testing.T, srv *httptest.Server, method string, params any) map[string]any {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	h := auth()
	h["Content-Type"] = "application/json"
	h["Accept"] = "application/json, text/event-stream"
	resp := do(t, "POST", srv.URL+"/mcp", bytes.NewReader(b), h)
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("%s: %d %s", method, resp.StatusCode, raw)
	}
	s := string(raw)
	if i := strings.Index(s, "data: "); i >= 0 { // SSE framing
		s = strings.TrimSpace(s[i+6:])
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		t.Fatalf("%s: %v %s", method, err, raw)
	}
	return out
}

func callTool(t *testing.T, srv *httptest.Server, name string, args map[string]any) (text string, isErr bool, content []any) {
	out := rpc(t, srv, "tools/call", map[string]any{"name": name, "arguments": args})
	res, _ := out["result"].(map[string]any)
	if res == nil {
		t.Fatalf("%s: no result: %v", name, out)
	}
	content, _ = res["content"].([]any)
	if len(content) > 0 {
		text, _ = content[0].(map[string]any)["text"].(string)
	}
	isErr, _ = res["isError"].(bool)
	return
}

func TestMCPToolSurface(t *testing.T) {
	srv, mgr := newTestServer(t)
	out := rpc(t, srv, "tools/list", map[string]any{})
	tools := out["result"].(map[string]any)["tools"].([]any)
	want := []string{"list_accounts", "get_account_status", "search_contacts", "list_chats", "list_messages", "get_message_context",
		"request_history_sync", "send_message", "send_media", "send_voice_note", "get_send_status", "get_media",
		"transcribe_audio", "translate_text", "analyze_image", "get_analysis_job"}
	if len(tools) != len(want) {
		t.Fatalf("tool count %d, want %d", len(tools), len(want))
	}
	for _, raw := range tools {
		tool := raw.(map[string]any)
		name := tool["name"].(string)
		if name == "list_accounts" {
			continue
		}
		req, _ := tool["inputSchema"].(map[string]any)["required"].([]any)
		found := false
		for _, r := range req {
			found = found || r == "account_id"
		}
		if !found {
			t.Errorf("%s does not require account_id", name)
		}
	}

	if txt, isErr, _ := callTool(t, srv, "list_messages", map[string]any{}); !isErr || !strings.Contains(txt, "account_id") {
		t.Fatalf("list_messages without account: %v %s", isErr, txt)
	}
	if _, isErr, _ := callTool(t, srv, "list_messages", map[string]any{"account_id": "zeta"}); !isErr {
		t.Fatal("unknown account accepted")
	}

	// Seed the same message ID into both accounts; each sees only its own.
	for _, id := range []string{"alpha", "beta"} {
		a, _ := mgr.Get(id)
		st, _ := a.Store()
		_ = st.StoreChat("15557770000@s.whatsapp.net", "Asha Kumar", time.Now())
		_ = st.StoreMessage(context.Background(), store.Message{ID: "DUP", ChatJID: "15557770000@s.whatsapp.net", Sender: "15557770000",
			Content: "only in " + id, Timestamp: time.Now(), MediaType: "image", URL: "https://mmg.whatsapp.net/secret-cdn", DirectPath: "/secret-path"},
			[]byte("mediakey"), []byte("s"), []byte("e"), 10)
	}
	txt, isErr, _ := callTool(t, srv, "list_messages", map[string]any{"account_id": "beta"})
	if isErr || !strings.Contains(txt, "only in beta") || strings.Contains(txt, "only in alpha") {
		t.Fatalf("beta read: %s", txt)
	}
	if strings.Contains(txt, "secret-cdn") || strings.Contains(txt, "secret-path") {
		t.Fatal("CDN URL or direct path leaked into tool output")
	}

	// A name is not a recipient; candidates are returned and nothing is sent.
	txt, isErr, _ = callTool(t, srv, "send_message", map[string]any{"account_id": "alpha", "recipient": "Asha", "message": "hi", "idempotency_key": "k"})
	if !isErr || !strings.Contains(txt, "candidates") || !strings.Contains(txt, "15557770000@s.whatsapp.net") {
		t.Fatalf("ambiguous recipient: %s", txt)
	}
	// Unpaired account rejects sends; no offline queue.
	txt, isErr, _ = callTool(t, srv, "send_message", map[string]any{"account_id": "alpha", "recipient": "15557770000", "message": "hi", "idempotency_key": "k"})
	if !isErr || !strings.Contains(txt, "not connected") {
		t.Fatalf("send on unpaired account: %s", txt)
	}
	// Analysis tools fail clearly when Gemini is disabled.
	if txt, isErr, _ = callTool(t, srv, "translate_text", map[string]any{"account_id": "alpha", "text": "hola"}); !isErr || !strings.Contains(txt, "not configured") {
		t.Fatalf("gemini disabled: %s", txt)
	}

	// Inline media is bounded and carries a download reference.
	a, _ := mgr.Get("alpha")
	obj, _ := a.StageUpload(context.Background(), bytes.NewReader(pngBytes()), "p.png")
	txt, isErr, content := callTool(t, srv, "get_media", map[string]any{"account_id": "alpha", "media_id": obj.ID})
	if isErr || len(content) != 2 || content[1].(map[string]any)["type"] != "image" || !strings.Contains(txt, "/api/accounts/alpha/media/"+obj.ID) || !strings.Contains(txt, srv.URL) {
		t.Fatalf("get_media: %s %v", txt, content)
	}
	if _, isErr, _ = callTool(t, srv, "get_media", map[string]any{"account_id": "beta", "media_id": obj.ID}); !isErr {
		t.Fatal("beta fetched alpha's media through MCP")
	}
}
