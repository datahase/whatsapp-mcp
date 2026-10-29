# History feature verification

Local development verification, 2026-10-29. All message fixtures are synthetic. No phone was paired, no private chat was indexed, no hosted generation/transcription job was run, and no live credentials or deployment were changed. Changes and images remain local.

## Checks

| Check | Result |
| --- | --- |
| Frontend production static export and TypeScript check | Passed |
| Full Go race suite | Passed after the fixes below |
| History, HTTP/auth and config race checks after final changes | Passed |
| Go vet, including e2e build tags | Passed |
| Staticcheck v0.8.2 | Passed |
| Existing e2e tests | Passed |
| CLI synthetic tests | 23 passed |
| Core Docker build, Linux ARM64 | Passed |
| Optional history CPU runtime build, Linux ARM64 | Passed |
| Core container acceptance script | 31 checks passed |
| Disposable setup/reuse/unhealthy-upgrade rollback smoke | Passed, existing settings and test volume retained |
| Optional runtime offline model inference | Passed with network disabled, read-only filesystem and pinned cached model; output shape 1 × 256 |
| Browser, synthetic backend | Passed selected/all monitoring, search, source inspection, chronological pagination, session deletion, account switching, stop monitoring |
| Responsive/system theme checks | 390 px mobile light/dark and desktop dark inspected; no horizontal overflow |
| Browser accessibility check | axe-core 4.12.1, 0 violations on the tested history screen |

The browser backend used real authentication, account-local stores, history APIs and jobs with a synthetic deterministic embedder. Only its account presentation state was marked connected to exercise post-link onboarding. It did not test physical pairing or real WhatsApp history delivery. Real embeddings were tested separately below.

Early full-suite attempts timed out waiting for indexing. A longer fixture allowance helped distinguish load from a real race: a cancelled old index could overwrite a newer selection's monitoring state and pause it. That state update is fixed and a deterministic regression forces the cancellation sequence. Another run exposed an existing one-time login-code concurrency failure (two successful consumers). An exclusive claim now arbitrates consumption before reading the code. The existing concurrency test passed 50 repeated runs, and the full suite passed afterward.

Tests also caught and fixed timezone-sensitive review pagination, scope/source invalidation, duplicate-sync reindexing, tombstone resurrection, unforgeable review cursors, lost origin authorization between review pages, retained queued authorization after scope/session deletion, and unsafe automatic redispatch of possibly billed work.

## Model and retrieval measurements

Pinned `google/embeddinggemma-2` revision `f9972ac22e696a105c482202646979e33e4b5a1e`, CPU float32, 256-dimensional normalized vectors. Runtime versions and exact prompts are in [HISTORY.md](HISTORY.md). The checked-in reproducible benchmark uses 30 cached synthetic messages, one excluded chat, six relevance labels and six English/Malayalam/code-switched questions.

| Measurement | Baseline run | Run alongside Docker builds |
| --- | --- | --- |
| Hybrid retrieval recall@1 | 6/6 | 6/6 |
| Hybrid retrieval recall@5 | 6/6 | 6/6 |
| Indexing including model load | 8.60 s | 32.28 s |
| Query latency range | 35.9–52.9 ms | 107.4–658.9 ms |
| Sampled Python worker RSS | 1254736 KiB | 620208 KiB |
| Sampled Go heap | 3009152 bytes | 3161384 bytes |
| SQLite database plus WAL | 2611896 bytes | 2669576 bytes |

RSS is a point-in-time process sample, not peak host memory or total model cost. Memory pressure/compression and concurrent workloads affect it. The model cache was about 1.4 GB. Local ARM64 image sizes were approximately 134 MB core and 1.45 GB with the optional runtime (uncompressed image metadata, not registry download sizes). A 4 GiB history container limit is the provided starting configuration; verify capacity on the target host.

The final benchmark's citation boundary check accepted 6/6 mocked responses quoting exact evidence and rejected 6/6 fabricated quotes. Mocked generation tests cover later completion across chronological pages, ambiguous owners, tentative dates, follow-up scoped turns, malicious message instructions, fabricated references, revocation and billing recovery. These measure enforced boundaries and synthetic behavior. They are **not** a live-model groundedness/entailment evaluation. No real-generation accuracy, cost, production-scale retrieval performance or Jetson GPU measurements are claimed.

## Reproduce

Use [the setup and verification commands](HISTORY.md#reproducible-verification). Additional local acceptance commands:

```sh
go test -race -count=50 -run TestLoginCodeCanBeConsumedOnlyOnceConcurrently ./internal/httpapi
staticcheck ./...
python3 scripts/test-cli.py
bash scripts/container-smoke.sh whatsapp-mcp:history-core
python3 scripts/setup-smoke.py whatsapp-mcp:history-core
```

The container scripts use disposable projects and synthetic data. Do not point them at an existing live project. To repeat the benchmark without network downloads, cache the pinned model first and add `HISTORY_TEST_OFFLINE=1`.

## Remaining limits

- Cached history, missing media and untranscribed audio prevent complete-account claims. Search never starts paid transcription.
- Exact vector search is linear. The default document cap is a safety bound, not a verified latency guarantee. Current monitoring/query scope supports up to 1000 chats per account.
- Ranked questions check nearby/later replies but can miss unrelated later updates. Use chronological action review for an exhaustive pass over the cached snapshot.
- Chronological reviews have explicit evidence/action/input bounds and fail when exceeded. They never label a partial result complete. A changed snapshot requires restart.
- Exact citation/quote validation cannot establish semantic entailment or eliminate model interpretation errors. The test providers are mocked; a live external-model evaluation requires separate approval.
- Same-chat reply context is followed; cross-chat reply IDs are retained without automatically reading their targets.
- CPU native and Linux ARM64 runtime paths were exercised. AMD64 runtime image execution and Jetson/CUDA compatibility were not tested.
- After a restart, external jobs need fresh authenticated resume. Ambiguous billable requests are held rather than resent automatically.
- This is the existing single-owner authorization model, not a new multi-user account permissions system. Index selection does not change ordinary WhatsApp-tool access.
