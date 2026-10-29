package ai

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/datahase/whatsapp-mcp/internal/config"
	"github.com/datahase/whatsapp-mcp/internal/store"
)

func TestHistoryGenerationLedgerCacheAndRestartAmbiguity(t *testing.T) {
	e := newEnv(t, func(c *config.AI) { c.DailyRequests = 2 })
	a := e.acct(t, "alpha")
	ctx := context.Background()
	guard := func() error { return nil }
	request := GenRequest{System: "Read-only synthetic test", Prompt: "Synthetic grounded question"}
	first, err := e.svc.GenerateHistory(ctx, a, "synthetic-history-key", "synthetic-session", request, guard)
	if err != nil || first.Status != store.JobCompleted {
		t.Fatalf("%+v %v", first, err)
	}
	second, err := e.svc.GenerateHistory(ctx, a, "synthetic-history-key", "synthetic-session", request, guard)
	if err != nil || !second.CacheHit || second.JobID != first.JobID || e.fake.count("POST /v1beta/interactions") != 1 {
		t.Fatal("duplicate history generation was dispatched")
	}
	usage := e.svc.UsageToday()
	if usage["requests"] != 1 {
		t.Fatalf("persistent usage not counted once: %+v", usage)
	}
	st, _ := a.Store()
	if err = st.UpdateJob(first.JobID, store.JobUnknownBilled, "", "ambiguous after restart", true); err != nil {
		t.Fatal(err)
	}
	ambiguous, err := e.svc.GenerateHistory(ctx, a, "synthetic-history-key", "synthetic-session", request, guard)
	if err != nil || ambiguous.Status != store.JobUnknownBilled || e.fake.count("POST /v1beta/interactions") != 1 {
		t.Fatal("ambiguous billed job automatically resent")
	}
	if _, err = e.svc.GenerateHistory(ctx, a, "revoked", "synthetic-session", request, func() error { return errors.New("revoked") }); err == nil {
		t.Fatal("revoked generation accepted")
	}
}
func TestHistoryGenerationRevocationStopsProvider(t *testing.T) {
	e := newEnv(t, nil)
	a := e.acct(t, "alpha")
	var allowed atomic.Bool
	allowed.Store(true)
	guard := func() error {
		if !allowed.Load() {
			return errors.New("revoked")
		}
		return nil
	}
	e.fake.interact = func(map[string]any) (int, string) {
		allowed.Store(false)
		return 200, okText("untrusted synthetic result")
	}
	result, err := e.svc.GenerateHistory(context.Background(), a, "revoked-during-call", "session", GenRequest{Prompt: "Synthetic"}, guard)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != store.JobFailed || result.Result != nil {
		t.Fatal("revoked provider result was retained")
	}
}
