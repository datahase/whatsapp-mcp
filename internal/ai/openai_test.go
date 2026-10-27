package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/datahase/whatsapp-mcp/internal/client"
)

// fakeOpenAI is an OpenAI-compatible server (OpenRouter-style).
type fakeOpenAI struct {
	mu        sync.Mutex
	bodies    []map[string]any
	multipart map[string]string
	status    int
	reply     func(body map[string]any) string
}

func (f *fakeOpenAI) server(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testKey || r.Header.Get("x-goog-api-key") != "" {
			t.Errorf("auth headers: %v", r.Header)
		}
		if f.status != 0 {
			w.WriteHeader(f.status)
			_, _ = w.Write([]byte(`{"error":{"message":"Insufficient credits. Add more using https://openrouter.ai/credits"}}`))
			return
		}
		switch r.URL.Path {
		case "/v1/chat/completions":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.bodies = append(f.bodies, body)
			f.mu.Unlock()
			b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": f.reply(body)}}},
				"usage": map[string]any{"prompt_tokens": 12, "completion_tokens": 7, "total_tokens": 19}})
			_, _ = w.Write(b)
		case "/v1/audio/transcriptions":
			_ = r.ParseMultipartForm(1 << 20)
			f.mu.Lock()
			f.multipart = map[string]string{"model": r.FormValue("model"), "language": r.FormValue("language")}
			if fh := r.MultipartForm.File["file"]; len(fh) == 1 {
				f.multipart["filename"] = fh[0].Filename
			}
			f.mu.Unlock()
			_, _ = w.Write([]byte(`{"text":"hello from whisper"}`))
		default:
			w.WriteHeader(404)
		}
	}))
}

func newOpenAIEnv(t *testing.T, method string, f *fakeOpenAI) *env {
	t.Helper()
	e := newEnv(t, nil) // accounts + manager; replace the provider below
	srv := f.server(t)
	t.Cleanup(srv.Close)
	g := e.svc.cfg
	g.Provider, g.TranscribeModel, g.AnalysisModel, g.QualityModel = ProviderOpenAI, "google/gemini-3.5-flash", "google/gemini-3.5-flash-lite", "anthropic/claude-sonnet-5"
	c := NewOpenAIClient(srv.URL+"/v1", testKey, 5*time.Second)
	c.sleep = func(context.Context, time.Duration) error { return nil }
	svc, err := newService(g, &openAIProvider{c: c, method: method}, c, t.TempDir(), 8, client.NewStderrLogger("t", "ERROR", false))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	e.svc = svc
	return e
}

func TestOpenAIChatTranscriptionAndTranslation(t *testing.T) {
	f := &fakeOpenAI{reply: func(body map[string]any) string {
		msgs := body["messages"].([]any)
		last := msgs[len(msgs)-1].(map[string]any)["content"].([]any)
		for _, part := range last {
			if p := part.(map[string]any); p["type"] == "input_audio" {
				return "ഹലോ, testing"
			}
		}
		return "```json\n{\"source_language\":\"ml\",\"translation\":\"Hello, testing\"}\n```"
	}}
	e := newOpenAIEnv(t, MethodChat, f)
	a := e.acct(t, "alpha")
	v, err := e.svc.Transcribe(context.Background(), a, Source{MediaID: stageVoice(t, a)}, "ml-IN", "en", false)
	if err != nil || v.Status != "completed" {
		t.Fatalf("%+v %v", v, err)
	}
	var res map[string]any
	_ = json.Unmarshal(v.Result, &res)
	if res["transcript"] != "ഹലോ, testing" || res["translation"].(map[string]any)["text"] != "Hello, testing" {
		t.Fatalf("result %v", res)
	}
	first := f.bodies[0]
	if first["model"] != "google/gemini-3.5-flash" {
		t.Fatalf("transcribe model %v", first["model"])
	}
	parts := first["messages"].([]any)[0].(map[string]any)["content"].([]any)
	audio := parts[1].(map[string]any)["input_audio"].(map[string]any)
	raw, _ := base64.StdEncoding.DecodeString(audio["data"].(string))
	if audio["format"] != "ogg" || !bytes.HasPrefix(raw, []byte("OggS")) {
		t.Fatalf("input_audio part: format=%v", audio["format"])
	}
	if f.bodies[1]["model"] != "google/gemini-3.5-flash-lite" {
		t.Fatalf("translate model %v", f.bodies[1]["model"])
	}
}

func TestOpenAIAudioTranscriptionsEndpoint(t *testing.T) {
	f := &fakeOpenAI{reply: func(map[string]any) string { return "" }}
	e := newOpenAIEnv(t, MethodAudioAPI, f)
	a := e.acct(t, "alpha")
	v, err := e.svc.Transcribe(context.Background(), a, Source{MediaID: stageVoice(t, a)}, "ml-IN", "", false)
	if err != nil || v.Status != "completed" || !strings.Contains(string(v.Result), "hello from whisper") {
		t.Fatalf("%+v %v", v, err)
	}
	if f.multipart["model"] != "google/gemini-3.5-flash" || f.multipart["language"] != "ml" || f.multipart["filename"] != "audio.ogg" {
		t.Fatalf("multipart fields %v", f.multipart)
	}
}

func TestOpenAIImageAndCreditsError(t *testing.T) {
	f := &fakeOpenAI{reply: func(body map[string]any) string {
		parts := body["messages"].([]any)[1].(map[string]any)["content"].([]any)
		url := parts[1].(map[string]any)["image_url"].(map[string]any)["url"].(string)
		if !strings.HasPrefix(url, "data:image/png;base64,") {
			return "bad"
		}
		return "OPEN 9 AM"
	}}
	e := newOpenAIEnv(t, MethodChat, f)
	a := e.acct(t, "alpha")
	obj, _ := a.StageUpload(context.Background(), bytes.NewReader(pngBytes()), "p.png")
	v, err := e.svc.AnalyzeImage(context.Background(), a, Source{MediaID: obj.ID}, "ocr", "", false)
	if err != nil || !strings.Contains(string(v.Result), "OPEN 9 AM") {
		t.Fatalf("%+v %v", v, err)
	}
	if sys := f.bodies[0]["messages"].([]any)[0].(map[string]any); sys["role"] != "system" {
		t.Fatal("system prompt not sent as a system message")
	}
	// Out of credits (OpenRouter 402): failed job of kind quota, no retries.
	f.status = http.StatusPaymentRequired
	v, _ = e.svc.AnalyzeImage(context.Background(), a, Source{MediaID: obj.ID}, "describe", "", false)
	if v.Status != "failed" || !strings.Contains(v.Error, "quota") {
		t.Fatalf("402: %+v", v)
	}
}

func TestContentTextAcceptsPartsArray(t *testing.T) {
	if got := contentText(json.RawMessage(`[{"type":"text","text":"a"},{"type":"text","text":"b"}]`)); got != "ab" {
		t.Fatal(got)
	}
}
