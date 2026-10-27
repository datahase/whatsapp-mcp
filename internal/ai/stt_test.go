package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestDeepgramRequest(t *testing.T) {
	var got *http.Request
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, body = r, mustRead(r)
		_, _ = w.Write([]byte(`{"metadata":{"duration":2.1},"results":{"channels":[{"detected_language":"ml","alternatives":[{"transcript":" നമസ്കാരം "}]}]}}`))
	}))
	defer srv.Close()
	p, _, _ := newProvider(ProviderDeepgram, srv.URL, testKey, "native", 5*time.Second, nil)
	text, _, err := p.Transcribe(context.Background(), "nova-3", []byte("OggS..."), "audio/ogg", "", func() {})
	if err != nil || text != "നമസ്കാരം" {
		t.Fatalf("%q %v", text, err)
	}
	q := got.URL.Query()
	if got.URL.Path != "/v1/listen" || got.Header.Get("Authorization") != "Token "+testKey || got.Header.Get("Content-Type") != "audio/ogg" ||
		q.Get("model") != "nova-3" || q.Get("detect_language") != "true" || string(body) != "OggS..." {
		t.Fatalf("request %s %v %v", got.URL, got.Header, q)
	}
	if _, _, err := p.Generate(context.Background(), "", GenRequest{}, func() {}); err == nil {
		t.Fatal("speech-to-text provider must not generate")
	}
}

func TestElevenLabsRequest(t *testing.T) {
	var fields map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/speech-to-text" || r.Header.Get("xi-api-key") != testKey {
			t.Errorf("%s %v", r.URL.Path, r.Header)
		}
		_ = r.ParseMultipartForm(1 << 20)
		fields = map[string]string{"model_id": r.FormValue("model_id"), "language_code": r.FormValue("language_code"), "file": r.MultipartForm.File["file"][0].Filename}
		_, _ = w.Write([]byte(`{"language_code":"mal","language_probability":0.98,"text":"നമസ്കാരം"}`))
	}))
	defer srv.Close()
	p, _, _ := newProvider(ProviderElevenLabs, srv.URL, testKey, "native", 5*time.Second, nil)
	text, _, err := p.Transcribe(context.Background(), "scribe_v2", []byte("OggS"), "audio/ogg", "ml-IN", func() {})
	if err != nil || text != "നമസ്കാരം" || fields["model_id"] != "scribe_v2" || fields["language_code"] != "ml" || fields["file"] != "audio.ogg" {
		t.Fatalf("%q %v %v", text, err, fields)
	}
}

func TestAssemblyAIUploadPollDelete(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	polls := 0
	var submitted map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		mu.Unlock()
		if r.Header.Get("authorization") != testKey {
			t.Errorf("auth %v", r.Header)
		}
		switch {
		case r.URL.Path == "/v2/upload":
			_, _ = w.Write([]byte(`{"upload_url":"https://cdn.assemblyai.com/upload/x"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v2/transcript":
			_ = json.NewDecoder(r.Body).Decode(&submitted)
			_, _ = w.Write([]byte(`{"id":"tr1","status":"queued"}`))
		case r.Method == http.MethodGet:
			polls++
			if polls < 2 {
				_, _ = w.Write([]byte(`{"id":"tr1","status":"processing"}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"tr1","status":"completed","text":"hello there","language_code":"en"}`))
		case r.Method == http.MethodDelete:
			_, _ = w.Write([]byte(`{"id":"tr1"}`))
		}
	}))
	defer srv.Close()
	p, c, _ := newProvider(ProviderAssemblyAI, srv.URL, testKey, "native", 5*time.Second, nil)
	c.sleep = func(context.Context, time.Duration) error { return nil }
	text, _, err := p.Transcribe(context.Background(), "universal-3-5-pro", []byte("OggS"), "audio/ogg", "", func() {})
	if err != nil || text != "hello there" {
		t.Fatalf("%q %v", text, err)
	}
	if submitted["audio_url"] != "https://cdn.assemblyai.com/upload/x" || submitted["language_detection"] != true ||
		submitted["speech_models"].([]any)[0] != "universal-3-5-pro" {
		t.Fatalf("submit %v", submitted)
	}
	if last := calls[len(calls)-1]; last != "DELETE /v2/transcript/tr1" {
		t.Fatalf("transcript not deleted afterwards: %v", calls)
	}
}

func mustRead(r *http.Request) []byte {
	b, _ := io.ReadAll(r.Body)
	return b
}
