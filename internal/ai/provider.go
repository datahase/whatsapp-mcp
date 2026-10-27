package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

// Provider is one analysis backend. dispatched must be called right before
// the first request that can be billed, so restarts can tell untouched jobs
// from possibly charged ones.
type Provider interface {
	Transcribe(ctx context.Context, model string, audio []byte, mime, languageHint string, dispatched func()) (string, Usage, error)
	Generate(ctx context.Context, model string, g GenRequest, dispatched func()) (string, Usage, error)
	Redact(s string) string
}

// GenRequest is a text (and optional single image) generation request.
type GenRequest struct {
	System      string
	Prompt      string
	Image       []byte
	ImageMIME   string
	JSONSchema  map[string]any // ask for schema-constrained JSON where supported
	MaxTokens   int
	Temperature float64
}

// Provider names. groq, openrouter and openai_compatible use the
// OpenAI-shaped wire format that those services natively expose.
const (
	ProviderGemini           = "gemini"
	ProviderOpenAI           = "openai"
	ProviderGroq             = "groq"
	ProviderOpenRouter       = "openrouter"
	ProviderAnthropic        = "anthropic"
	ProviderOpenAICompatible = "openai_compatible"
)

// -- Gemini (native Interactions + Files API) --------------------------------

type geminiProvider struct {
	c *Client
	// onCleanupFail records a provider file whose deletion failed.
	onCleanupFail func(name, msg string)
}

func (g *geminiProvider) Redact(s string) string { return g.c.redact(s) }

// Transcribe uploads the audio, requests a verbatim transcript and always
// deletes the uploaded file.
func (g *geminiProvider) Transcribe(ctx context.Context, model string, audio []byte, mime, lang string, dispatched func()) (string, Usage, error) {
	f, err := g.c.UploadFile(ctx, audio, mime)
	if f != nil && f.Name != "" {
		defer func() {
			dctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if derr := g.c.DeleteFile(dctx, f.Name); derr != nil && g.onCleanupFail != nil {
				g.onCleanupFail(f.Name, g.c.redact(derr.Error()))
			}
		}()
	}
	if err != nil {
		return "", Usage{}, err
	}
	tc := map[string]any{"mode": map[string]string{"type": "verbatim"}}
	if lang != "" {
		tc["language_codes"] = []string{lang}
	}
	dispatched()
	resp, err := g.c.Interact(ctx, Request{Model: model, Input: []Part{{Type: "audio", URI: f.URI, MimeType: mime}},
		GenerationConfig: map[string]any{"transcription_config": tc}})
	if err != nil {
		return "", Usage{}, err
	}
	return resp.Text, resp.Usage, nil
}

func (g *geminiProvider) Generate(ctx context.Context, model string, r GenRequest, dispatched func()) (string, Usage, error) {
	in := []Part{{Type: "text", Text: r.Prompt}}
	if len(r.Image) > 0 {
		in = append(in, Part{Type: "image", Data: base64.StdEncoding.EncodeToString(r.Image), MimeType: r.ImageMIME})
	}
	req := Request{Model: model, SystemInstruction: r.System, Input: in,
		GenerationConfig: map[string]any{"max_output_tokens": r.MaxTokens, "temperature": r.Temperature}}
	if r.JSONSchema != nil {
		req.ResponseFormat = map[string]any{"type": "text", "mime_type": "application/json", "schema": r.JSONSchema}
	}
	dispatched()
	resp, err := g.c.Interact(ctx, req)
	if err != nil {
		return "", Usage{}, err
	}
	return resp.Text, resp.Usage, nil
}

// -- OpenAI-compatible (OpenRouter, OpenAI, Groq, local servers) -------------

// Transcription methods for OpenAI-compatible providers.
const (
	// MethodChat sends audio as an input_audio part to /chat/completions
	// (OpenRouter, Gemini's OpenAI endpoint, audio-capable chat models).
	MethodChat = "chat"
	// MethodAudioAPI uses /audio/transcriptions (OpenAI, Groq, Whisper servers).
	MethodAudioAPI = "audio_transcriptions"
)

type openAIProvider struct {
	c      *Client
	method string
	// headers are optional extra headers (OpenRouter attribution).
	headers map[string]string
}

// NewOpenAIClient returns a Bearer-authenticated client for an
// OpenAI-compatible base URL such as https://openrouter.ai/api/v1.
func NewOpenAIClient(base, key string, timeout time.Duration) *Client {
	c := NewClient(base, key, timeout)
	c.bearer = true
	return c
}

func (o *openAIProvider) Redact(s string) string { return o.c.redact(s) }

const transcribePrompt = "Transcribe this audio verbatim in the original language and script. Keep code-switched words as spoken. " +
	"Return only the spoken words, with no commentary, summary or translation. Mark inaudible parts [unclear]. " +
	"If there is no intelligible speech, return an empty response."

// audioFormat maps a MIME type to the input_audio format names providers use.
func audioFormat(mime string) string {
	switch {
	case strings.Contains(mime, "ogg"):
		return "ogg"
	case strings.Contains(mime, "mpeg"), strings.Contains(mime, "mp3"):
		return "mp3"
	case strings.Contains(mime, "wav"):
		return "wav"
	case strings.Contains(mime, "mp4"), strings.Contains(mime, "m4a"):
		return "m4a"
	case strings.Contains(mime, "flac"):
		return "flac"
	case strings.Contains(mime, "aac"):
		return "aac"
	}
	return "wav"
}

func (o *openAIProvider) Transcribe(ctx context.Context, model string, audio []byte, mime, lang string, dispatched func()) (string, Usage, error) {
	if o.method == MethodAudioAPI {
		return o.transcribeAudioAPI(ctx, model, audio, mime, lang, dispatched)
	}
	prompt := transcribePrompt
	if lang != "" {
		prompt += " Language hint: " + lang + "."
	}
	content := []any{
		map[string]any{"type": "text", "text": prompt},
		map[string]any{"type": "input_audio", "input_audio": map[string]string{"data": base64.StdEncoding.EncodeToString(audio), "format": audioFormat(mime)}},
	}
	return o.chat(ctx, model, "", content, 0, 0, nil, dispatched)
}

func (o *openAIProvider) transcribeAudioAPI(ctx context.Context, model string, audio []byte, mime, lang string, dispatched func()) (string, Usage, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("model", model)
	_ = w.WriteField("response_format", "json")
	if lang != "" {
		// The audio API takes ISO-639-1 ("ml"), not BCP-47 ("ml-IN").
		_ = w.WriteField("language", strings.SplitN(lang, "-", 2)[0])
	}
	fw, err := w.CreateFormFile("file", "audio."+audioFormat(mime))
	if err != nil {
		return "", Usage{}, err
	}
	_, _ = fw.Write(audio)
	_ = w.Close()
	hdr := map[string]string{"Content-Type": w.FormDataContentType()}
	for k, v := range o.headers {
		hdr[k] = v
	}
	dispatched()
	_, b, err := o.c.do(ctx, http.MethodPost, o.c.base+"/audio/transcriptions", body.Bytes(), hdr)
	if err != nil {
		return "", Usage{}, err
	}
	var out struct {
		Text  string `json:"text"`
		Usage struct {
			In  int `json:"input_tokens"`
			Out int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return "", Usage{}, &Error{Kind: KindInvalidOutput, Message: "unparseable transcription response"}
	}
	return strings.TrimSpace(out.Text), Usage{InputTokens: out.Usage.In, OutputTokens: out.Usage.Out, TotalTokens: out.Usage.In + out.Usage.Out}, nil
}

func (o *openAIProvider) Generate(ctx context.Context, model string, r GenRequest, dispatched func()) (string, Usage, error) {
	content := []any{map[string]any{"type": "text", "text": r.Prompt}}
	if len(r.Image) > 0 {
		url := "data:" + r.ImageMIME + ";base64," + base64.StdEncoding.EncodeToString(r.Image)
		content = append(content, map[string]any{"type": "image_url", "image_url": map[string]string{"url": url}})
	}
	// JSON is requested in the prompt rather than with response_format,
	// which not every OpenAI-compatible model supports.
	return o.chat(ctx, model, r.System, content, r.MaxTokens, r.Temperature, r.JSONSchema, dispatched)
}

func (o *openAIProvider) chat(ctx context.Context, model, system string, content []any, maxTokens int, temp float64, _ map[string]any, dispatched func()) (string, Usage, error) {
	msgs := []any{}
	if system != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": system})
	}
	msgs = append(msgs, map[string]any{"role": "user", "content": content})
	req := map[string]any{"model": model, "messages": msgs, "stream": false}
	if maxTokens > 0 {
		req["max_tokens"] = maxTokens
	}
	if temp > 0 {
		req["temperature"] = temp
	}
	body, _ := json.Marshal(req)
	hdr := map[string]string{"Content-Type": "application/json"}
	for k, v := range o.headers {
		hdr[k] = v
	}
	dispatched()
	_, b, err := o.c.do(ctx, http.MethodPost, o.c.base+"/chat/completions", body, hdr)
	if err != nil {
		return "", Usage{}, err
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			Prompt     int `json:"prompt_tokens"`
			Completion int `json:"completion_tokens"`
			Total      int `json:"total_tokens"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return "", Usage{}, &Error{Kind: KindInvalidOutput, Message: "unparseable chat response"}
	}
	if out.Error != nil { // some gateways report errors with HTTP 200
		return "", Usage{}, &Error{Kind: KindBadRequest, Message: o.c.redact(out.Error.Message)}
	}
	if len(out.Choices) == 0 {
		return "", Usage{}, &Error{Kind: KindInvalidOutput, Message: "response had no choices"}
	}
	u := Usage{InputTokens: out.Usage.Prompt, OutputTokens: out.Usage.Completion, TotalTokens: out.Usage.Total}
	return strings.TrimSpace(contentText(out.Choices[0].Message.Content)), u, nil
}

// contentText accepts content as a string or as an array of text parts.
func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			if p.Type == "text" || p.Type == "" {
				b.WriteString(p.Text)
			}
		}
		return b.String()
	}
	return ""
}

// newProvider builds one provider. method selects the transcription
// endpoint for OpenAI-shaped providers (chat or audio_transcriptions).
func newProvider(kind, base, key, method string, timeout time.Duration, onCleanupFail func(string, string)) (Provider, *Client, error) {
	switch kind {
	case ProviderGemini:
		c := NewClient(base, key, timeout)
		return &geminiProvider{c: c, onCleanupFail: onCleanupFail}, c, nil
	case ProviderAnthropic:
		c := NewAnthropicClient(base, key, timeout)
		return &anthropicProvider{c: c}, c, nil
	case ProviderDeepgram:
		c := withAuth(base, key, "Authorization", "Token ", timeout)
		return &deepgramProvider{c: c}, c, nil
	case ProviderElevenLabs:
		c := withAuth(base, key, "xi-api-key", "", timeout)
		return &elevenLabsProvider{c: c}, c, nil
	case ProviderAssemblyAI:
		c := withAuth(base, key, "authorization", "", timeout)
		return &assemblyAIProvider{c: c}, c, nil
	case ProviderOpenAI, ProviderGroq, ProviderOpenRouter, ProviderOpenAICompatible:
		c := NewOpenAIClient(base, key, timeout)
		p := &openAIProvider{c: c, method: method}
		if kind == ProviderOpenRouter {
			p.headers = map[string]string{"X-Title": "whatsapp-mcp"}
		}
		return p, c, nil
	}
	return nil, nil, fmt.Errorf("unknown provider %q", kind)
}
