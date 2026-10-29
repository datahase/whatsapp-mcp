package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGatewayTranscriptionAndGenerationRoutes(t *testing.T) {
	audio := []byte("OggS synthetic voice fixture")
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer "+testKey || r.Header.Get("x-goog-api-key") != "" {
			t.Error("gateway must receive only Bearer authentication")
		}
		switch r.URL.Path {
		case "/proxy/v4/ai/transcription-model":
			if r.Header.Get("ai-model-id") != "google/gemini-3.5-transcribe" ||
				r.Header.Get("ai-gateway-protocol-version") != "0.0.1" ||
				r.Header.Get("ai-transcription-model-specification-version") != "4" {
				t.Error("missing gateway transcription protocol headers")
			}
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			got, err := base64.StdEncoding.DecodeString(body["audio"])
			if err != nil || !bytes.Equal(got, audio) || body["mediaType"] != "audio/ogg" {
				t.Error("recorded audio bytes or MIME type changed")
			}
			_, _ = w.Write([]byte(`{"text":"ഹലോ, testing","durationInSeconds":2.5}`))
		case "/proxy/v1/chat/completions":
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Hello"}}],"usage":{"total_tokens":9}}`))
		default:
			t.Errorf("unexpected gateway route %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	p, _, err := newProvider(ProviderVercel, srv.URL+"/proxy/v1", testKey, "native", time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	dispatched := 0
	text, usage, err := p.Transcribe(context.Background(), "google/gemini-3.5-transcribe", audio, "audio/ogg", "ml", func() { dispatched++ })
	if err != nil || text != "ഹലോ, testing" || usage.TotalTokens != 0 || dispatched != 1 {
		t.Fatalf("transcription %q %+v dispatch=%d err=%v", text, usage, dispatched, err)
	}
	text, usage, err = p.Generate(context.Background(), "google/gemini-3.5-flash-lite", GenRequest{Prompt: "Translate"}, func() { dispatched++ })
	if err != nil || text != "Hello" || usage.TotalTokens != 9 || dispatched != 2 || calls != 2 {
		t.Fatalf("generation %q %+v dispatch=%d calls=%d err=%v", text, usage, dispatched, calls, err)
	}
}

func TestGatewayTranscriptionErrorsAndEmptySpeech(t *testing.T) {
	for _, tc := range []struct {
		name, body, kind string
		status           int
	}{
		{"silence", `{"text":""}`, "", 200},
		{"missing text", `{}`, KindInvalidOutput, 200},
		{"malformed", `not json`, KindInvalidOutput, 200},
		{"auth", `{"error":{"message":"bad key ` + testKey + `"}}`, KindAuth, 401},
		{"budget", `{"error":{"message":"budget exhausted"}}`, KindQuota, 402},
		{"rate limit", `{"error":{"message":"rate limited"}}`, KindQuota, 429},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			p, c, _ := newProvider(ProviderVercel, srv.URL+"/v1", testKey, "native", time.Second, nil)
			c.sleep = func(context.Context, time.Duration) error { return nil }
			_, _, err := p.Transcribe(context.Background(), "google/gemini-3.5-transcribe", []byte("audio"), "audio/ogg", "", func() {})
			if tc.kind == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var providerError *Error
			if !errors.As(err, &providerError) || providerError.Kind != tc.kind || strings.Contains(err.Error(), testKey) {
				t.Fatalf("expected redacted %s error, got %v", tc.kind, err)
			}
			wantRequests := 1
			if tc.status == 429 {
				wantRequests = 3
			}
			if requests != wantRequests {
				t.Fatalf("made %d requests; want %d", requests, wantRequests)
			}
		})
	}
}
