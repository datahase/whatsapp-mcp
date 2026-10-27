package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Dedicated speech-to-text providers. They only transcribe; translation
// and image analysis stay with the main (LLM) provider.
const (
	ProviderDeepgram   = "deepgram"
	ProviderElevenLabs = "elevenlabs"
	ProviderAssemblyAI = "assemblyai"
)

// errTranscribeOnly is returned if a speech-to-text provider is asked to
// generate text; config validation prevents it.
var errTranscribeOnly = errors.New("this provider only transcribes audio")

// withAuth returns a client whose requests carry one fixed auth header.
func withAuth(base, key, header, prefix string, timeout time.Duration) *Client {
	c := NewClient(base, key, timeout)
	c.auth = func(r *http.Request) { r.Header.Set(header, prefix+key) }
	return c
}

// iso639 turns a BCP-47 hint ("ml-IN") into the base language ("ml").
func iso639(lang string) string { return strings.ToLower(strings.SplitN(lang, "-", 2)[0]) }

// -- Deepgram (POST /v1/listen) ----------------------------------------------

type deepgramProvider struct{ c *Client }

func (p *deepgramProvider) Redact(s string) string { return p.c.redact(s) }
func (p *deepgramProvider) Generate(context.Context, string, GenRequest, func()) (string, Usage, error) {
	return "", Usage{}, errTranscribeOnly
}

func (p *deepgramProvider) Transcribe(ctx context.Context, model string, audio []byte, mime, lang string, dispatched func()) (string, Usage, error) {
	q := url.Values{"model": {model}, "smart_format": {"true"}, "punctuate": {"true"}}
	if lang != "" {
		q.Set("language", lang)
	} else {
		q.Set("detect_language", "true")
	}
	dispatched()
	_, b, err := p.c.do(ctx, http.MethodPost, p.c.base+"/v1/listen?"+q.Encode(), audio, map[string]string{"Content-Type": mime})
	if err != nil {
		return "", Usage{}, err
	}
	var out struct {
		Results struct {
			Channels []struct {
				Alternatives []struct {
					Transcript string `json:"transcript"`
				} `json:"alternatives"`
			} `json:"channels"`
		} `json:"results"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return "", Usage{}, &Error{Kind: KindInvalidOutput, Message: "unparseable Deepgram response"}
	}
	if len(out.Results.Channels) == 0 || len(out.Results.Channels[0].Alternatives) == 0 {
		return "", Usage{}, nil
	}
	return strings.TrimSpace(out.Results.Channels[0].Alternatives[0].Transcript), Usage{}, nil
}

// -- ElevenLabs Scribe (POST /v1/speech-to-text) ------------------------------

type elevenLabsProvider struct{ c *Client }

func (p *elevenLabsProvider) Redact(s string) string { return p.c.redact(s) }
func (p *elevenLabsProvider) Generate(context.Context, string, GenRequest, func()) (string, Usage, error) {
	return "", Usage{}, errTranscribeOnly
}

func (p *elevenLabsProvider) Transcribe(ctx context.Context, model string, audio []byte, mime, lang string, dispatched func()) (string, Usage, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("model_id", model)
	_ = w.WriteField("tag_audio_events", "false")
	if lang != "" {
		_ = w.WriteField("language_code", iso639(lang))
	}
	fw, err := w.CreateFormFile("file", "audio."+audioFormat(mime))
	if err != nil {
		return "", Usage{}, err
	}
	_, _ = fw.Write(audio)
	_ = w.Close()
	dispatched()
	_, b, err := p.c.do(ctx, http.MethodPost, p.c.base+"/v1/speech-to-text", body.Bytes(), map[string]string{"Content-Type": w.FormDataContentType()})
	if err != nil {
		return "", Usage{}, err
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return "", Usage{}, &Error{Kind: KindInvalidOutput, Message: "unparseable ElevenLabs response"}
	}
	return strings.TrimSpace(out.Text), Usage{}, nil
}

// -- AssemblyAI (upload, submit, poll, delete) -------------------------------

type assemblyAIProvider struct {
	c    *Client
	poll time.Duration
	// onCleanupFail records a transcript whose deletion failed, for retry.
	onCleanupFail func(name, msg string)
}

// Delete removes a transcript (used by the cleanup retry loop).
func (p *assemblyAIProvider) Delete(ctx context.Context, id string) error {
	_, _, err := p.c.do(ctx, http.MethodDelete, p.c.base+"/v2/transcript/"+url.PathEscape(id), nil, nil)
	var e *Error
	if errors.As(err, &e) && e.Status == http.StatusNotFound {
		return nil
	}
	return err
}

func (p *assemblyAIProvider) Redact(s string) string { return p.c.redact(s) }
func (p *assemblyAIProvider) Generate(context.Context, string, GenRequest, func()) (string, Usage, error) {
	return "", Usage{}, errTranscribeOnly
}

// Transcribe uploads, submits, polls and then deletes the transcript.
// AssemblyAI documents no endpoint to delete a raw upload, so audio uploaded
// before a failed submit cannot be removed through the API.
func (p *assemblyAIProvider) Transcribe(ctx context.Context, model string, audio []byte, _, lang string, dispatched func()) (string, Usage, error) {
	dispatched() // the audio leaves this server with the upload
	_, b, err := p.c.do(ctx, http.MethodPost, p.c.base+"/v2/upload", audio, map[string]string{"Content-Type": "application/octet-stream"})
	if err != nil {
		return "", Usage{}, err
	}
	var up struct {
		UploadURL string `json:"upload_url"`
	}
	if json.Unmarshal(b, &up) != nil || up.UploadURL == "" {
		return "", Usage{}, &Error{Kind: KindInvalidOutput, Message: "AssemblyAI upload returned no upload_url"}
	}
	req := map[string]any{"audio_url": up.UploadURL}
	if model != "" {
		req["speech_models"] = []string{model}
	}
	if lang != "" {
		req["language_code"] = iso639(lang)
		req["language_detection"] = false
	} else {
		req["language_detection"] = true
	}
	body, _ := json.Marshal(req)
	_, b, err = p.c.do(ctx, http.MethodPost, p.c.base+"/v2/transcript", body, map[string]string{"Content-Type": "application/json"})
	if err != nil {
		return "", Usage{}, err
	}
	var t struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Text   string `json:"text"`
		Error  string `json:"error"`
	}
	if json.Unmarshal(b, &t) != nil || t.ID == "" {
		return "", Usage{}, &Error{Kind: KindInvalidOutput, Message: "AssemblyAI returned no transcript id"}
	}
	// Remove the transcript (and AssemblyAI's copy of its text) once done;
	// a failed deletion is queued for retry.
	defer func() {
		dctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := p.Delete(dctx, t.ID); err != nil && p.onCleanupFail != nil {
			p.onCleanupFail(t.ID, p.c.redact(err.Error()))
		}
	}()
	interval := p.poll
	if interval == 0 {
		interval = 2 * time.Second
	}
	for t.Status != "completed" && t.Status != "error" {
		if err := p.c.sleep(ctx, interval); err != nil {
			return "", Usage{}, &Error{Kind: KindNetwork, Message: "timed out waiting for AssemblyAI"}
		}
		if _, b, err = p.c.do(ctx, http.MethodGet, p.c.base+"/v2/transcript/"+url.PathEscape(t.ID), nil, nil); err != nil {
			return "", Usage{}, err
		}
		if err := json.Unmarshal(b, &t); err != nil {
			return "", Usage{}, &Error{Kind: KindInvalidOutput, Message: "unparseable AssemblyAI response"}
		}
	}
	if t.Status == "error" {
		return "", Usage{}, &Error{Kind: KindBadRequest, Message: p.c.redact(fmt.Sprintf("AssemblyAI: %s", t.Error))}
	}
	return strings.TrimSpace(t.Text), Usage{}, nil
}
