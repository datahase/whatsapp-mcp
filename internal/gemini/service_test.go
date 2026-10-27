package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/datahase/whatsapp-mcp/internal/accounts"
	"github.com/datahase/whatsapp-mcp/internal/client"
	"github.com/datahase/whatsapp-mcp/internal/config"
	"github.com/datahase/whatsapp-mcp/internal/security"
)

const testKey = "AIzaTEST-not-a-real-key-0123456789"

// fakeGemini records calls and serves scripted responses.
type fakeGemini struct {
	mu          sync.Mutex
	calls       []string
	interact    func(body map[string]any) (int, string)
	deleteFails bool
	sawKey      bool
}

func (f *fakeGemini) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		f.sawKey = f.sawKey || r.Header.Get("x-goog-api-key") == testKey
		f.mu.Unlock()
		if strings.Contains(r.URL.RawQuery, "key=") {
			t.Error("API key sent in URL")
		}
		switch {
		case r.URL.Path == "/upload/v1beta/files" && r.Header.Get("X-Goog-Upload-Command") == "start":
			w.Header().Set("X-Goog-Upload-URL", "http://"+r.Host+"/upload-session/1")
		case r.URL.Path == "/upload-session/1":
			_, _ = io.Copy(io.Discard, r.Body)
			_, _ = w.Write([]byte(`{"file":{"name":"files/abc","uri":"https://x/files/abc","mimeType":"audio/ogg","state":"ACTIVE"}}`))
		case r.Method == http.MethodDelete:
			if f.deleteFails {
				w.WriteHeader(500)
				_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
			}
		case r.URL.Path == "/v1beta/interactions":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["store"] != false {
				t.Errorf("interaction must set store=false: %v", body["store"])
			}
			if _, ok := body["tools"]; ok {
				t.Error("tools must never be sent")
			}
			code, out := f.interact(body)
			w.WriteHeader(code)
			_, _ = w.Write([]byte(out))
		default:
			w.WriteHeader(404)
		}
	})
}

func (f *fakeGemini) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func okText(text string) string {
	b, _ := json.Marshal(map[string]any{"id": "i1", "status": "completed",
		"steps": []any{map[string]any{"type": "model_output", "content": []any{map[string]any{"type": "text", "text": text}}}},
		"usage": map[string]any{"total_input_tokens": 10, "total_output_tokens": 5, "total_tokens": 15}})
	return string(b)
}

type env struct {
	svc  *Service
	fake *fakeGemini
	mgr  *accounts.Manager
}

func newEnv(t *testing.T, mutate func(*config.Gemini)) *env {
	t.Helper()
	data := t.TempDir()
	cfg := &config.Config{DataDir: data, LogLevel: "ERROR",
		Accounts: []config.Account{{ID: "alpha", Label: "A"}, {ID: "beta", Label: "B"}}}
	cfg.Limits.MaxMediaBytes, cfg.Limits.MediaCacheBytes, cfg.Limits.MessageCacheBytes = 8<<20, 64<<20, 1<<30
	cfg.Limits.StagedUploadTTL = time.Hour
	mgr := accounts.NewManager(cfg, &security.Redactor{})
	mgr.Start(context.Background())
	t.Cleanup(mgr.Stop)

	f := &fakeGemini{interact: func(map[string]any) (int, string) { return 200, okText("hello") }}
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	g := config.Gemini{TranscribeModel: "gemini-3.5-transcribe", AnalysisModel: "gemini-3.5-flash-lite", QualityModel: "gemini-3.8-flash",
		DefaultTargetLanguage: "en", DailyAudioMinutes: 30, DailyRequests: 50, MaxAudioSeconds: 600, MaxOutputTokens: 512,
		RequestTimeout: 5 * time.Second, WaitForResult: 5 * time.Second}
	if mutate != nil {
		mutate(&g)
	}
	c := NewClient(srv.URL, testKey, g.RequestTimeout)
	c.sleep = func(context.Context, time.Duration) error { return nil }
	svc, err := newService(g, c, data, 8, client.NewStderrLogger("t", "ERROR", false))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	return &env{svc: svc, fake: f, mgr: mgr}
}

func (e *env) acct(t *testing.T, id string) *accounts.Account {
	a, err := e.mgr.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func stageVoice(t *testing.T, a *accounts.Account) string {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	p := filepath.Join(t.TempDir(), "v.ogg")
	if out, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=duration=2", "-c:a", "libopus", "-y", p).CombinedOutput(); err != nil {
		t.Skipf("fixture: %v %s", err, out)
	}
	b, _ := os.ReadFile(p)
	obj, err := a.StageUpload(context.Background(), bytes.NewReader(b), "v.ogg")
	if err != nil {
		t.Fatal(err)
	}
	return obj.ID
}

func TestTranscribeTranslateCacheAndCleanup(t *testing.T) {
	e := newEnv(t, nil)
	e.fake.interact = func(body map[string]any) (int, string) {
		if body["model"] == "gemini-3.5-transcribe" {
			gc := body["generation_config"].(map[string]any)["transcription_config"].(map[string]any)
			if gc["mode"].(map[string]any)["type"] != "verbatim" {
				t.Errorf("transcription mode %v", gc["mode"])
			}
			return 200, okText("ഹലോ, how are you")
		}
		if rf, _ := body["response_format"].(map[string]any); rf["mime_type"] != "application/json" || rf["schema"] == nil {
			t.Error("translation must request JSON")
		}
		return 200, okText(`{"source_language":"ml","translation":"Hello, how are you"}`)
	}
	alpha := e.acct(t, "alpha")
	id := stageVoice(t, alpha)
	ctx := context.Background()
	v, err := e.svc.Transcribe(ctx, alpha, Source{MediaID: id}, "", "en", false)
	if err != nil {
		t.Fatal(err)
	}
	if v.Status != "completed" || v.CacheHit || v.Tier != TierPaid {
		t.Fatalf("first run: %+v", v)
	}
	var res map[string]any
	_ = json.Unmarshal(v.Result, &res)
	if res["transcript"] != "ഹലോ, how are you" {
		t.Fatalf("original transcript altered: %v", res["transcript"])
	}
	tr := res["translation"].(map[string]any)
	if tr["text"] != "Hello, how are you" || res["detected_language"] != "ml" {
		t.Fatalf("translation: %v", res)
	}
	if e.fake.count("DELETE /v1beta/files/abc") != 1 || !e.fake.sawKey {
		t.Fatalf("provider file not deleted or key header missing: %v", e.fake.calls)
	}

	before := len(e.fake.calls)
	v2, err := e.svc.Transcribe(ctx, alpha, Source{MediaID: id}, "", "en", false)
	if err != nil || !v2.CacheHit || v2.JobID != v.JobID || len(e.fake.calls) != before {
		t.Fatalf("cache miss on identical request: %+v calls=%d->%d", v2, before, len(e.fake.calls))
	}
	// Different task parameters are a different cache entry.
	if v3, _ := e.svc.Transcribe(ctx, alpha, Source{MediaID: id}, "", "fr", false); v3.CacheHit {
		t.Fatal("different target served from cache")
	}
	// The same bytes in another account never share results.
	beta := e.acct(t, "beta")
	bid := stageVoice(t, beta)
	if vb, _ := e.svc.Transcribe(ctx, beta, Source{MediaID: bid}, "", "en", false); vb.CacheHit || vb.AccountID != "beta" {
		t.Fatalf("cross-account cache hit: %+v", vb)
	}
	if _, err := e.svc.GetJob(beta, v.JobID); err == nil {
		t.Fatal("beta can read alpha's job")
	}
}

func TestSilentAudioFlaggedForReview(t *testing.T) {
	e := newEnv(t, nil)
	e.fake.interact = func(map[string]any) (int, string) { return 200, okText("") }
	a := e.acct(t, "alpha")
	v, err := e.svc.Transcribe(context.Background(), a, Source{MediaID: stageVoice(t, a)}, "", "en", false)
	if err != nil {
		t.Fatal(err)
	}
	var res map[string]any
	_ = json.Unmarshal(v.Result, &res)
	if res["needs_review"] != true || res["translation"] != nil {
		t.Fatalf("silent audio not flagged or translated anyway: %v", res)
	}
}

func TestProviderFailuresAreHonestAndRedacted(t *testing.T) {
	cases := []struct {
		name     string
		code     int
		body     string
		wantKind string
		attempts int
	}{
		{"auth", 403, `{"error":{"message":"API key ` + testKey + ` not valid"}}`, KindAuth, 1},
		{"model", 404, `{"error":{"message":"model not found"}}`, KindModelUnavailable, 1},
		{"quota", 429, `{"error":{"message":"quota exceeded"}}`, KindQuota, 3},
		{"transient", 503, `{"error":{"message":"overloaded"}}`, KindTransient, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, nil)
			e.fake.interact = func(map[string]any) (int, string) { return c.code, c.body }
			a := e.acct(t, "alpha")
			obj, _ := a.StageUpload(context.Background(), bytes.NewReader(pngBytes()), "p.png")
			v, err := e.svc.AnalyzeImage(context.Background(), a, Source{MediaID: obj.ID}, "ocr", "", false)
			if err != nil {
				t.Fatal(err)
			}
			if v.Status != "failed" || !strings.Contains(v.Error, c.wantKind) || strings.Contains(v.Error, testKey) {
				t.Fatalf("job %+v", v)
			}
			if n := e.fake.count("POST /v1beta/interactions"); n != c.attempts {
				t.Fatalf("attempts %d, want %d (bounded retries, no model/tier switch)", n, c.attempts)
			}
		})
	}
}

func TestTransientThenSuccessAndNoModelEscalation(t *testing.T) {
	e := newEnv(t, nil)
	n := 0
	var models []string
	e.fake.interact = func(body map[string]any) (int, string) {
		n++
		models = append(models, body["model"].(string))
		if n == 1 {
			return 503, `{"error":{"message":"busy"}}`
		}
		return 200, okText("A cat on a mat.")
	}
	a := e.acct(t, "alpha")
	obj, _ := a.StageUpload(context.Background(), bytes.NewReader(pngBytes()), "p.png")
	v, err := e.svc.AnalyzeImage(context.Background(), a, Source{MediaID: obj.ID}, "describe", "", false)
	if err != nil || v.Status != "completed" {
		t.Fatalf("%+v %v", v, err)
	}
	for _, m := range models {
		if m != "gemini-3.5-flash-lite" {
			t.Fatalf("model escalated on retry: %v", models)
		}
	}
	if v2, _ := e.svc.AnalyzeImage(context.Background(), a, Source{MediaID: obj.ID}, "describe", "", true); v2.Model != "gemini-3.8-flash" {
		t.Fatalf("explicit quality option not honored: %s", v2.Model)
	}
}

func TestDailyAllowanceStopsBeforeDispatch(t *testing.T) {
	e := newEnv(t, func(g *config.Gemini) { g.DailyRequests = 1 })
	a := e.acct(t, "alpha")
	ctx := context.Background()
	v1, _ := e.svc.TranslateText(ctx, a, "hola", Source{}, "en", "", false)
	_ = v1
	v2, err := e.svc.TranslateText(ctx, a, "adios", Source{}, "en", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if v2.Status != "failed" || !strings.Contains(v2.Error, "allowance") {
		t.Fatalf("allowance not enforced: %+v", v2)
	}
	if n := e.fake.count("POST /v1beta/interactions"); n != 1 {
		t.Fatalf("provider called %d times", n)
	}
}

func TestFailedDeleteIsQueuedForRetry(t *testing.T) {
	e := newEnv(t, nil)
	e.fake.deleteFails = true
	a := e.acct(t, "alpha")
	if _, err := e.svc.Transcribe(context.Background(), a, Source{MediaID: stageVoice(t, a)}, "", "", false); err != nil {
		t.Fatal(err)
	}
	if e.svc.PendingCleanups() != 1 {
		t.Fatalf("pending cleanups %d", e.svc.PendingCleanups())
	}
}

func TestTimeoutMarksDispatchedFailure(t *testing.T) {
	e := newEnv(t, func(g *config.Gemini) { g.RequestTimeout = 200 * time.Millisecond })
	e.fake.interact = func(map[string]any) (int, string) {
		time.Sleep(time.Second)
		return 200, okText("late")
	}
	a := e.acct(t, "alpha")
	v, err := e.svc.TranslateText(context.Background(), a, "hola", Source{}, "en", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if v.Status != "failed" || !strings.Contains(v.Error, "network") {
		t.Fatalf("timeout: %+v", v)
	}
	st, _ := a.Store()
	j, _ := st.GetJob(v.JobID)
	if !j.Dispatched {
		t.Fatal("in-flight request not recorded as dispatched (may be billed)")
	}
}

func TestDisabledServiceKeepsErrorsClear(t *testing.T) {
	var s *Service
	if _, err := s.TranslateText(context.Background(), nil, "x", Source{}, "en", "", false); err != ErrDisabled {
		t.Fatalf("nil service: %v", err)
	}
}

func pngBytes() []byte {
	var b bytes.Buffer
	_ = png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	return b.Bytes()
}
