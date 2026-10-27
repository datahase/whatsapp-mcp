// Package ai runs on-demand transcription, translation and image analysis
// through a configured provider: Google Gemini's native API, or any
// OpenAI-compatible API (OpenRouter, OpenAI, Groq, local servers). Calls are
// direct HTTPS; no SDK, extra runtime or other MCP server is involved.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Client is a minimal REST client bound to one API key and one auth style.
type Client struct {
	base   string
	key    string
	bearer bool                // Authorization: Bearer (OpenAI style) instead of x-goog-api-key
	auth   func(*http.Request) // custom auth headers (Anthropic); overrides both
	http   *http.Client
	// sleep is a seam so tests do not wait for real backoff.
	sleep func(context.Context, time.Duration) error
}

// NewClient returns a Gemini-style client (x-goog-api-key header).
func NewClient(base, key string, timeout time.Duration) *Client {
	return &Client{
		base: strings.TrimRight(base, "/"), key: key,
		http: &http.Client{Timeout: timeout},
		sleep: func(ctx context.Context, d time.Duration) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d):
				return nil
			}
		},
	}
}

// Error is a classified provider failure. Message never contains the key.
type Error struct {
	Status    int    `json:"http_status,omitempty"`
	Kind      string `json:"kind"`
	Message   string `json:"message"`
	Retryable bool   `json:"-"`
}

func (e *Error) Error() string { return "provider " + e.Kind + ": " + e.Message }

// Error kinds.
const (
	KindAuth             = "auth"              // bad key or no access
	KindQuota            = "quota"             // provider quota/rate limit
	KindModelUnavailable = "model_unavailable" // model not found or not enabled
	KindBadRequest       = "bad_request"
	KindTransient        = "transient"
	KindNetwork          = "network"
	KindInvalidOutput    = "invalid_output"
)

func (c *Client) redact(s string) string {
	if c.key != "" {
		s = strings.ReplaceAll(s, c.key, "[redacted]")
	}
	if len(s) > 400 {
		s = s[:400]
	}
	return s
}

func (c *Client) classify(status int, body []byte) *Error {
	var env struct {
		Error struct {
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &env)
	msg := env.Error.Message
	if msg == "" {
		msg = http.StatusText(status)
	}
	e := &Error{Status: status, Message: c.redact(msg)}
	switch {
	case status == 401 || status == 403:
		e.Kind = KindAuth
	case status == 404:
		e.Kind = KindModelUnavailable
	case status == 402:
		e.Kind = KindQuota // insufficient credits (OpenRouter)
	case status == 429:
		e.Kind, e.Retryable = KindQuota, true
	case status == 400 && strings.Contains(strings.ToLower(msg), "api key"):
		e.Kind = KindAuth
	case strings.Contains(strings.ToLower(msg), "credit") || strings.Contains(strings.ToLower(msg), "billing"):
		e.Kind = KindQuota // billing/prepayment problems need the owner, not a retry
	case status >= 500:
		e.Kind, e.Retryable = KindTransient, status == 500 || status == 502 || status == 503 || status == 504 || status == 529
	default:
		e.Kind = KindBadRequest
	}
	return e
}

// do sends one request with bounded retries for understood transient
// failures (429/5xx with backoff). It never switches keys or models.
func (c *Client) do(ctx context.Context, method, url string, body []byte, hdr map[string]string) (*http.Response, []byte, error) {
	const attempts = 3
	var last error
	for i := 0; i < attempts; i++ {
		var rd io.Reader
		if body != nil {
			rd = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, rd)
		if err != nil {
			return nil, nil, err
		}
		if c.auth != nil {
			c.auth(req)
		} else if c.bearer {
			req.Header.Set("Authorization", "Bearer "+c.key)
		} else {
			req.Header.Set("x-goog-api-key", c.key)
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, &Error{Kind: KindNetwork, Message: "request cancelled or timed out"}
			}
			last = &Error{Kind: KindNetwork, Message: c.redact(err.Error()), Retryable: true}
		} else {
			b, rerr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
			resp.Body.Close()
			if rerr != nil {
				last = &Error{Kind: KindNetwork, Message: c.redact(rerr.Error()), Retryable: true}
			} else if resp.StatusCode < 300 {
				return resp, b, nil
			} else {
				e := c.classify(resp.StatusCode, b)
				if !e.Retryable {
					return nil, nil, e
				}
				last = e
				if ra, _ := strconv.Atoi(resp.Header.Get("Retry-After")); ra > 30 {
					return nil, nil, e // the provider wants a long pause; report, don't wait
				}
			}
		}
		if i < attempts-1 {
			if err := c.sleep(ctx, time.Duration(2<<i)*time.Second); err != nil {
				return nil, nil, &Error{Kind: KindNetwork, Message: "request cancelled or timed out"}
			}
		}
	}
	return nil, nil, last
}
