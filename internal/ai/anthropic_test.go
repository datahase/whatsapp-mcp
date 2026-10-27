package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/datahase/whatsapp-mcp/internal/client"
)

// fakeAnthropic serves the Messages API.
type fakeAnthropic struct {
	mu     sync.Mutex
	bodies []map[string]any
	status []int // per-call status codes; 0/absent = 200
	stop   string
	text   string
}

func (f *fakeAnthropic) server(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != testKey || r.Header.Get("anthropic-version") != anthropicVersion ||
			r.Header.Get("Authorization") != "" || !strings.Contains(r.Header.Get("anthropic-beta"), "server-side-fallback") {
			t.Errorf("request %s headers %v", r.URL.Path, r.Header)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		n := len(f.bodies)
		f.bodies = append(f.bodies, body)
		f.mu.Unlock()
		if n < len(f.status) && f.status[n] != 0 {
			w.WriteHeader(f.status[n])
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`))
			return
		}
		stop := f.stop
		if stop == "" {
			stop = "end_turn"
		}
		b, _ := json.Marshal(map[string]any{"type": "message", "role": "assistant", "stop_reason": stop,
			"content": []any{map[string]any{"type": "text", "text": f.text}}, "usage": map[string]any{"input_tokens": 9, "output_tokens": 4}})
		_, _ = w.Write(b)
	}))
}

// fakeTranscriber stands in for the separate transcription provider.
type fakeTranscriber struct{ calls int }

func (f *fakeTranscriber) Transcribe(_ context.Context, _ string, _ []byte, _, _ string, d func()) (string, Usage, error) {
	f.calls++
	d()
	return "നാളെ കാണാം", Usage{InputTokens: 1}, nil
}
func (f *fakeTranscriber) Generate(context.Context, string, GenRequest, func()) (string, Usage, error) {
	return "", Usage{}, nil
}
func (f *fakeTranscriber) Redact(s string) string { return s }

func newAnthropicEnv(t *testing.T, f *fakeAnthropic, tr Provider) *env {
	t.Helper()
	e := newEnv(t, nil)
	srv := f.server(t)
	t.Cleanup(srv.Close)
	g := e.svc.cfg
	g.Provider, g.TranscribeProvider, g.AnalysisModel, g.QualityModel = ProviderAnthropic, ProviderGroq, "claude-opus-5-5", ""
	c := NewAnthropicClient(srv.URL, testKey, 5*time.Second)
	c.sleep = func(context.Context, time.Duration) error { return nil }
	svc, err := (&Service{tp: tr}).init(g, &anthropicProvider{c: c}, c, t.TempDir(), 8, client.NewStderrLogger("t", "ERROR", false))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	e.svc = svc
	return e
}

func TestAnthropicImageAnalysisRequestShape(t *testing.T) {
	f := &fakeAnthropic{text: "OPEN 9 AM", status: []int{529}} // first call overloaded, then retried
	e := newAnthropicEnv(t, f, &fakeTranscriber{})
	a := e.acct(t, "alpha")
	obj, _ := a.StageUpload(context.Background(), bytes.NewReader(pngBytes()), "p.png")
	v, err := e.svc.AnalyzeImage(context.Background(), a, Source{MediaID: obj.ID}, "ocr", "", true)
	if err != nil || v.Status != "completed" || !strings.Contains(string(v.Result), "OPEN 9 AM") {
		t.Fatalf("%+v %v", v, err)
	}
	if len(f.bodies) != 2 {
		t.Fatalf("529 should be retried once here, got %d calls", len(f.bodies))
	}
	b := f.bodies[1]
	if b["model"] != "claude-opus-5-5" || b["system"] == nil || b["temperature"] != nil || b["fallbacks"] != "default" {
		t.Fatalf("body %v", b)
	}
	if v.Model != "claude-opus-5-5" {
		t.Fatalf("empty quality model must fall back to AI_MODEL, got %s", v.Model)
	}
	content := b["messages"].([]any)[0].(map[string]any)["content"].([]any)
	img := content[0].(map[string]any)
	src := img["source"].(map[string]any)
	if img["type"] != "image" || src["type"] != "base64" || src["media_type"] != "image/png" {
		t.Fatalf("image block %v", img)
	}
}

func TestAnthropicTranslationWithSeparateTranscriber(t *testing.T) {
	f := &fakeAnthropic{text: `{"source_language":"ml","translation":"See you tomorrow"}`}
	tr := &fakeTranscriber{}
	e := newAnthropicEnv(t, f, tr)
	a := e.acct(t, "alpha")
	v, err := e.svc.Transcribe(context.Background(), a, Source{MediaID: stageVoice(t, a)}, "", "en", false)
	if err != nil || v.Status != "completed" {
		t.Fatalf("%+v %v", v, err)
	}
	var res map[string]any
	_ = json.Unmarshal(v.Result, &res)
	if tr.calls != 1 || res["transcript"] != "നാളെ കാണാം" || res["translation"].(map[string]any)["text"] != "See you tomorrow" {
		t.Fatalf("result %v (transcriber calls %d)", res, tr.calls)
	}
	format := f.bodies[0]["output_config"].(map[string]any)["format"].(map[string]any)
	if format["type"] != "json_schema" || format["schema"].(map[string]any)["additionalProperties"] != false {
		t.Fatalf("structured output %v", format)
	}
}

func TestAnthropicRefusalAndNoAudio(t *testing.T) {
	f := &fakeAnthropic{stop: "refusal"}
	e := newAnthropicEnv(t, f, &fakeTranscriber{})
	a := e.acct(t, "alpha")
	v, _ := e.svc.TranslateText(context.Background(), a, "hola", Source{}, "en", "", false)
	if v.Status != "failed" || !strings.Contains(v.Error, "declined") {
		t.Fatalf("refusal: %+v", v)
	}
	if _, _, err := (&anthropicProvider{}).Transcribe(context.Background(), "", nil, "", "", func() {}); err != ErrAnthropicNoAudio {
		t.Fatal("anthropic must refuse transcription")
	}
}
