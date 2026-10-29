package history

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/datahase/whatsapp-mcp/internal/config"
	"github.com/datahase/whatsapp-mcp/internal/store"
)

// This opt-in test runs the real local model on entirely synthetic history.
// It never starts a WhatsApp client or calls a hosted generation provider.
func TestRealModelSyntheticBenchmark(t *testing.T) {
	if os.Getenv("HISTORY_REAL_MODEL") != "1" {
		t.Skip("set HISTORY_REAL_MODEL=1 with the optional pinned runtime installed")
	}
	python := os.Getenv("HISTORY_TEST_PYTHON")
	if python == "" {
		t.Fatal("HISTORY_TEST_PYTHON is required")
	}
	cache := os.Getenv("HISTORY_TEST_MODEL_CACHE")
	if cache == "" {
		t.Fatal("HISTORY_TEST_MODEL_CACHE is required")
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	_ = st.StoreChat("synthetic@g.us", "Synthetic benchmark", time.Now())
	_ = st.StoreChat("excluded@g.us", "Synthetic excluded", time.Now())
	examples := []struct{ id, text, query string }{
		{"budget", "The project spending ceiling is 4200 synthetic units. Finance approved it.", "What is the approved budget?"},
		{"launch", "We decided to release the synthetic product next Thursday after QA.", "When is the product launch scheduled?"},
		{"malayalam", "റിപ്പോർട്ട് തയ്യാറാക്കാനുള്ള ചുമതല സിന്തറ്റിക് സംഘത്തിനാണ്. ബജറ്റ് അംഗീകരിച്ചു.", "റിപ്പോർട്ട് തയ്യാറാക്കുന്നത് ആരാണ്?"},
		{"codeswitch", "Deployment hold cheyyanam, security review kazhinjittu mathi release.", "Why are we waiting before deployment?"},
		{"complete", "The synthetic invoice was paid and the task is complete.", "Is the invoice still pending?"},
		{"decision", "We rejected the blue packaging and selected the recyclable green carton.", "Which packaging option did we choose?"},
	}
	for i, e := range examples {
		if err = st.StoreMessage(ctx, store.Message{ID: e.id, ChatJID: "synthetic@g.us", Sender: "synthetic_author", Content: e.text, Timestamp: time.Date(2026, 1, 1, 0, i, 0, 0, time.UTC)}, nil, nil, nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 24; i++ {
		text := "Synthetic cafeteria discussion about soup and fruit"
		if i%2 == 0 {
			text = "Synthetic office gardening and cycling discussion"
		}
		_ = st.StoreMessage(ctx, store.Message{ID: "filler" + strconv.Itoa(i), ChatJID: "synthetic@g.us", Sender: "synthetic_other", Content: text, Timestamp: time.Date(2026, 1, 1, 1, i, 0, 0, time.UTC)}, nil, nil, nil, 0)
	}
	_ = st.StoreMessage(ctx, store.Message{ID: "excluded", ChatJID: "excluded@g.us", Sender: "synthetic_other", Content: "Forbidden synthetic budget 999999", Timestamp: time.Now()}, nil, nil, nil, 0)
	cfg := config.History{Enabled: true, Model: "google/embeddinggemma-2", Revision: "f9972ac22e696a105c482202646979e33e4b5a1e", Dimensions: 256, Runtime: "sentence_transformers", Python: python, Device: "cpu", CacheDir: cache, Offline: os.Getenv("HISTORY_TEST_OFFLINE") == "1", MaxDocuments: 1000, RetentionDays: 7}
	embed := NewLocalEmbedder(cfg, t.TempDir())
	svc := NewWithEmbedder(cfg, func(id string) (*store.Store, error) {
		if id != "synthetic" {
			return nil, ErrScope
		}
		return st, nil
	}, func() []string { return []string{"synthetic"} }, embed)
	defer svc.Close()
	start := time.Now()
	_, err = svc.Select(ctx, "synthetic", "selected", []string{"synthetic@g.us"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Minute)
	for {
		cov, e := svc.Coverage(ctx, Scope{"synthetic", []string{"synthetic@g.us"}})
		if e != nil {
			t.Fatal(e)
		}
		if cov.Pending == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("real model indexing timed out; inspect optional runtime")
		}
		time.Sleep(100 * time.Millisecond)
	}
	indexing := time.Since(start)
	hit1, hit5 := 0, 0
	validCitations, rejectedFabrications := 0, 0
	latencies := []float64{}
	for _, e := range examples {
		before := time.Now()
		result, err := svc.Search(ctx, SearchRequest{Scope: Scope{"synthetic", []string{"synthetic@g.us"}}, Query: e.query, Limit: 5})
		if err != nil {
			t.Fatal(err)
		}
		latencies = append(latencies, float64(time.Since(before).Microseconds())/1000)
		if len(result.Hits) > 0 {
			source := result.Hits[0].Source
			answer := Answer{Claims: []Claim{{Kind: "fact", Text: source.Text, Citations: []Citation{{SourceID: source.ID, Quote: source.Text}}}}}
			raw, _ := json.Marshal(answer)
			if _, err := svc.validateAnswer(ctx, Scope{"synthetic", []string{"synthetic@g.us"}}, string(raw), []Source{source}); err == nil {
				validCitations++
			}
			answer.Claims[0].Citations[0].Quote = "fabricated quote not in synthetic evidence"
			raw, _ = json.Marshal(answer)
			if _, err := svc.validateAnswer(ctx, Scope{"synthetic", []string{"synthetic@g.us"}}, string(raw), []Source{source}); err != nil {
				rejectedFabrications++
			}
		}
		for i, h := range result.Hits {
			if h.Source.ChatJID != "synthetic@g.us" {
				t.Fatal("unselected chat leaked")
			}
			if h.Source.MessageID == e.id {
				hit5++
				if i == 0 {
					hit1++
				}
				break
			}
		}
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	indexBytes := int64(0)
	files, _ := filepath.Glob(filepath.Join(st.Dir(), "messages.db*"))
	for _, f := range files {
		if info, e := os.Stat(f); e == nil {
			indexBytes += info.Size()
		}
	}
	workerRSSKB := 0
	if embed.cmd != nil {
		if raw, e := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(embed.cmd.Process.Pid)).Output(); e == nil {
			workerRSSKB, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
		}
	}
	metric := map[string]any{"fixture": "synthetic-history-v1", "messages": 30, "queries": len(examples), "recall_at_1": float64(hit1) / float64(len(examples)), "recall_at_5": float64(hit5) / float64(len(examples)), "indexing_seconds_including_model_load": indexing.Seconds(), "query_latency_ms": latencies, "go_heap_bytes": ms.HeapAlloc, "worker_rss_kib_sample": workerRSSKB, "sqlite_bytes_including_wal": indexBytes, "model": embed.Manifest(), "generation": "mocked separately; no hosted model or paid work", "mock_valid_citations_accepted": validCitations, "mock_fabricated_quotes_rejected": rejectedFabrications}
	raw, _ := json.MarshalIndent(metric, "", "  ")
	t.Log(string(raw))
	if output := os.Getenv("HISTORY_BENCHMARK_OUTPUT"); output != "" {
		if err = os.WriteFile(output, append(raw, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if validCitations != len(examples) || rejectedFabrications != len(examples) {
		t.Fatal("citation validation boundary failed")
	}
	if hit5 < 4 {
		t.Fatalf("real multilingual retrieval quality below minimum: %d/%d recall@5", hit5, len(examples))
	}
}
