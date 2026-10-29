package ai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/datahase/whatsapp-mcp/internal/accounts"
	"github.com/datahase/whatsapp-mcp/internal/store"
)

// GenerateHistory uses the existing provider, persistent usage ledger, bounded
// queue, restart-ambiguity handling and result cache. It cannot invoke tools.
// guard is checked before dispatch and continuously while the provider runs.
func (s *Service) GenerateHistory(ctx context.Context, a *accounts.Account, key, sessionID string, r GenRequest, guard func() error) (*JobView, error) {
	if s == nil {
		return nil, ErrDisabled
	}
	if err := guard(); err != nil {
		return nil, err
	}
	params := map[string]any{"history_request": key, "history_session": sessionID}
	pj, _ := json.Marshal(params)
	sum := sha256.Sum256([]byte(strings.Join([]string{a.ID, "history_answer", s.cfg.AnalysisModel, PromptVersion, key, string(pj)}, "\x00")))

	if st, e := a.Store(); e == nil {
		if old, e := st.FindAnyJobByCacheKey(hex.EncodeToString(sum[:])); e == nil {
			if old.Status == store.JobInterrupted && !old.Dispatched {
				_, _ = s.ledger.Exec("UPDATE usage SET requests=0,audio_seconds=0,status='released' WHERE job_id=? AND status='reserved'", old.ID)
				_, _ = st.DB().Exec("DELETE FROM analysis_jobs WHERE id=? AND status=? AND dispatched=0", old.ID, store.JobInterrupted)
			} else if old.Status != store.JobCompleted && old.Status != store.JobQueued && old.Status != store.JobRunning {
				return viewOf(a, old, true), nil
			}
		}
	}

	return s.submit(ctx, a, jobSpec{task: "history_answer", model: s.cfg.AnalysisModel, contentHash: key, params: map[string]any{"history_request": key, "history_session": sessionID}, requests: 1, run: func(callCtx context.Context, _ *store.Job, dispatched func()) (any, Usage, error) {
		if err := guard(); err != nil {
			return nil, Usage{}, err
		}
		providerCtx, cancel := context.WithCancel(callCtx)
		defer cancel()
		done := make(chan struct{})
		defer close(done)
		go func() {
			t := time.NewTicker(250 * time.Millisecond)
			defer t.Stop()
			for {
				select {
				case <-done:
					return
				case <-providerCtx.Done():
					return
				case <-t.C:
					if guard() != nil {
						cancel()
						return
					}
				}
			}
		}()
		r.MaxTokens = s.cfg.MaxOutputTokens
		text, u, err := s.p.Generate(providerCtx, s.cfg.AnalysisModel, r, dispatched)
		if err != nil {
			return nil, u, err
		}
		if err = guard(); err != nil {
			return nil, u, err
		}
		return map[string]string{"text": text}, u, nil
	}})
}
func HistoryText(v *JobView) (string, error) {
	if v == nil {
		return "", errors.New("history generation unavailable")
	}
	if v.Status != store.JobCompleted {
		return "", errors.New("history generation is incomplete or ambiguous; do not retry a possibly billed job automatically")
	}
	var data struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(v.Result, &data); err != nil {
		return "", err
	}
	return data.Text, nil
}
