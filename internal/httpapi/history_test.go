package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/datahase/whatsapp-mcp/internal/accounts"
	"github.com/datahase/whatsapp-mcp/internal/config"
	"github.com/datahase/whatsapp-mcp/internal/history"
	wamcp "github.com/datahase/whatsapp-mcp/internal/mcp"
	"github.com/datahase/whatsapp-mcp/internal/security"
	"github.com/datahase/whatsapp-mcp/internal/store"
)

type syntheticHistoryEmbedding struct{}

func (syntheticHistoryEmbedding) Manifest() history.Manifest {
	return history.Manifest{Model: "synthetic-test", Revision: strings.Repeat("a", 40), Dimensions: 128, Runtime: "mock", Device: "cpu", Format: "synthetic"}
}
func (syntheticHistoryEmbedding) Close() error { return nil }
func (syntheticHistoryEmbedding) Embed(ctx context.Context, texts []string, _ string) ([][]float32, error) {
	out := [][]float32{}
	for range texts {
		v := make([]float32, 128)
		v[0] = 1
		out = append(out, v)
	}
	return out, nil
}
func TestHistoryHTTPAndMCPBoundaries(t *testing.T) {
	cfg := &config.Config{DataDir: t.TempDir(), LogLevel: "ERROR", Accounts: []config.Account{{ID: "alpha", Label: "Synthetic A"}, {ID: "beta", Label: "Synthetic B"}}}
	cfg.Limits.MessageCacheBytes = 1 << 30
	mgr := accounts.NewManager(cfg, &security.Redactor{})
	mgr.Start(context.Background())
	defer mgr.Stop()
	for _, id := range []string{"alpha", "beta"} {
		a, _ := mgr.Get(id)
		st, _ := a.Store()
		for _, chat := range []string{"chosen@g.us", "other@g.us"} {
			_ = st.StoreChat(chat, "Synthetic chat", time.Now())
			_ = st.StoreMessage(context.Background(), store.Message{ID: "same", ChatJID: chat, Sender: "synthetic", Content: id + " " + chat, Timestamp: time.Now()}, nil, nil, nil, 0)
		}
	}
	hist := history.NewWithEmbedder(config.History{Enabled: true, RetentionDays: 7, MaxDocuments: 1000}, func(id string) (*store.Store, error) {
		a, e := mgr.Get(id)
		if e != nil {
			return nil, e
		}
		return a.Store()
	}, func() []string { return []string{"alpha", "beta"} }, syntheticHistoryEmbedding{})
	defer hist.Close()
	m := wamcp.NewServer(mgr, nil, 1024).WithHistory(hist)
	srv := httptest.NewServer(New(Config{Token: token, AdminPassword: adminPW, Manager: mgr, MCP: m.Handler(), History: hist, Log: nil}))
	defer srv.Close()
	resp := do(t, "GET", srv.URL+"/admin/api/history/alpha/status", nil, nil)
	if resp.StatusCode != 401 {
		t.Fatal("history admin status is not authenticated")
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	resp, err := client.PostForm(srv.URL+"/admin/login", url.Values{"password": {adminPW}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	resp, err = client.Get(srv.URL + "/admin/api/session")
	if err != nil {
		t.Fatal(err)
	}
	var session map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&session)
	resp.Body.Close()
	csrf := session["csrf"].(string)
	post := func(path, body, csrf string) *http.Response {
		req, _ := http.NewRequest("POST", srv.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", csrf)
		r, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	resp = post("/admin/api/history/alpha/selection", `{"mode":"selected","chat_ids":["chosen@g.us"]}`, "wrong")
	if resp.StatusCode != 403 {
		t.Fatal("history selection did not enforce CSRF")
	}
	resp.Body.Close()
	resp = post("/admin/api/history/alpha/selection", `{"mode":"selected","chat_ids":["chosen@g.us"]}`, csrf)
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("selection %d %s", resp.StatusCode, b)
	}
	resp.Body.Close()
	if _, isErr, _ := callTool(t, srv, "search_history", map[string]any{"account_id": "alpha", "chat_ids": []string{"other@g.us"}, "query": "other"}); !isErr {
		t.Fatal("unmonitored chat accepted by history search")
	}
	// The monitoring selection must NOT change the existing agent permissions.
	text, isErr, _ := callTool(t, srv, "list_messages", map[string]any{"account_id": "alpha", "chat_jid": "other@g.us"})
	if isErr || !strings.Contains(text, "other@g.us") {
		t.Fatalf("existing tools restricted by monitoring selection: %s", text)
	}
	if _, isErr, _ := callTool(t, srv, "manage_history_session", map[string]any{"account_id": "beta", "operation": "create", "chat_ids": []string{"chosen@g.us"}}); !isErr {
		t.Fatal("account beta borrowed account alpha selection")
	}
	resp = post("/admin/api/history/alpha/search", `{"chat_ids":["other@g.us"],"query":"other"}`, csrf)
	if resp.StatusCode == 200 {
		t.Fatal("admin search bypassed selected scope")
	}
	resp.Body.Close()
	resp = post("/admin/api/history/alpha/session", `{"chat_ids":["chosen@g.us"],"external_consent":false}`, csrf)
	if resp.StatusCode != 200 {
		t.Fatal("local session failed")
	}
	var hs history.Session
	_ = json.NewDecoder(resp.Body).Decode(&hs)
	resp.Body.Close()
	text, isErr, _ = callTool(t, srv, "ask_history", map[string]any{"account_id": "alpha", "session_id": hs.ID, "question": "Who owns the task?"})
	if !isErr || !strings.Contains(text, "consent") {
		t.Fatal("generation bypassed opt-in")
	}
}
