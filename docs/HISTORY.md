# Chat with your WhatsApp history

Prepare conversations once, then search their meaning, inspect the original messages, or ask questions with cited evidence. Text and already completed voice-note transcripts are embedded locally on the MCP server host. The feature is optional and disabled in the standard image. It is available starting with v0.6.0.

## Connect and choose conversations

1. Connect WhatsApp in **Accounts** using the existing QR flow.
2. The connected account offers **Index all available chats**, **Choose chats and accounts**, or **Not now**.
3. In **History**, choose the account, then all chats or specific conversations. Each account has its own selection (up to 1000 monitored chats). All-chats mode also monitors future chats received by the cache. If it grows past 1000 chats, narrow the selection before indexing or querying the whole scope.
4. Watch indexing progress. New messages, edits and completed transcripts update the index automatically. Cancel pauses automatic indexing; **Start indexing** resumes it.
5. Search locally, inspect a result's sources, or create a session for questions and chronological action review.

This selection controls monitoring and vectorization. It does **not** restrict the existing WhatsApp MCP tools. An agent can still use those tools on other conversations allowed by its existing account authorization. Selecting an account's chats does not select chats in another account.

“All available chats” means messages the server has cached. WhatsApp may supply only part of older history. An optional older-history request asks for at most 50 older messages per selected chat. Its per-chat request status is persistent and scoped; acceptance does not mean WhatsApp delivered the messages. Repeating an explicit request can ask for another batch. Cached history is never presented as a complete account backup.

## Install the optional runtime

The normal Docker image stays small. The history variant adds Python and CPU PyTorch. Pull the versioned variant from either registry:

```sh
docker pull datahase/whatsapp-mcp:0.6.0-history
# Alternative registry:
docker pull ghcr.io/datahase/whatsapp-mcp:0.6.0-history
```

From a checkout with an existing private `.env`, start it with:

```sh
docker compose -f compose.yaml -f compose.history.yaml up -d --no-build
```

The override selects the history image and enables indexing. For GHCR, set `WHATSAPP_MCP_HISTORY_IMAGE=ghcr.io/datahase/whatsapp-mcp:0.6.0-history` in `.env`. The core tags remain `0.6.0`, `0.6`, and `latest`; history tags are `0.6.0-history`, `0.6-history`, and `history`.

For the one-command CLI, create a private environment file using placeholders from `.env.example`. Set `HISTORY_ENABLED=true`, `MEM_LIMIT=4g` and `CPUS=2.0`, then use the existing environment-file setup option:

```sh
whatsapp-mcp setup --image datahase/whatsapp-mcp:0.6.0-history --env-file /path/to/private-history.env
```

This configures deployment using the existing setup checks. Review its network mode and existing installation selection before proceeding. No answer-generation key is required for local retrieval.

You can also build locally from this checkout; these commands do not publish an image:

```sh
./scripts/init-env.sh
# Keep your existing .env if already configured.
docker build -t whatsapp-mcp:history-core .
docker build -f Dockerfile.history -t whatsapp-mcp:history-local .
WHATSAPP_MCP_HISTORY_IMAGE=whatsapp-mcp:history-local \
  docker compose -f compose.yaml -f compose.history.yaml up -d --no-build
```

Use the same Compose project and data volume as your existing installation. Do not create a second installation against the same live WhatsApp session. The override reserves up to 4 GiB RAM and two CPUs. The first index downloads the pinned public model into `/data/models/embeddinggemma-2`; weights and private databases stay in the data volume, outside Git and images. Allow time and disk space for the initial download (about 1.4 GB observed model cache).

After a successful first index, set `HISTORY_OFFLINE=true` in `.env` and recreate the container to require cached weights. Offline mode fails if that exact revision is absent; it never substitutes another model. The optional image uses CPU wheels on Linux. The current local verification covers Linux ARM64, not a Jetson GPU stack or every host architecture. GPU acceleration requires a separately compatible PyTorch installation.

For a native server, use Python 3.12 or a compatible supported Python version and the pinned dependencies:

```sh
python3 -m venv .venv-history
.venv-history/bin/pip install -r scripts/history/requirements.txt
export HISTORY_ENABLED=true
export HISTORY_PYTHON="$PWD/.venv-history/bin/python"
# Run the existing server command with your usual private configuration.
```

Keep that environment and model cache untracked. CPU-only Linux installations should install the pinned torch and torchvision versions from `https://download.pytorch.org/whl/cpu` first, as Dockerfile.history does. No Hugging Face token is required. The worker does not inherit provider keys or implicit Hugging Face credentials.

Tailscale settings are unchanged. Use a private tailnet connection for administration where possible. A remote cloud MCP connector still needs the public HTTPS/OAuth reachability described in [client setup](CLIENTS.md); enabling history does not expose a new unauthenticated endpoint or require a new hosted service.

## Configuration

All values are optional except enabling the feature and installing its runtime. See [.env.example](../.env.example) and [config.example.yaml](../config.example.yaml).

| Environment variable | Default / purpose |
| --- | --- |
| `HISTORY_ENABLED` | `false`; enable local history tools and worker |
| `HISTORY_MODEL` | `google/embeddinggemma-2` |
| `HISTORY_MODEL_REVISION` | `f9972ac22e696a105c482202646979e33e4b5a1e`, immutable model commit |
| `HISTORY_DIMENSIONS` | `256`; supported 128, 256, 512, 768 |
| `HISTORY_RUNTIME` | `sentence_transformers`, current implementation |
| `HISTORY_PYTHON` | `python3`, or the optional image's `/opt/history/bin/python` |
| `HISTORY_DEVICE` | `cpu`; explicit PyTorch device, no automatic provider/model fallback |
| `HISTORY_MODEL_CACHE` | Private runtime model directory; override uses `/data/models/embeddinggemma-2` |
| `HISTORY_OFFLINE` | `false`; set true after caching the pinned revision |
| `HISTORY_MAX_DOCUMENTS` | `100000` chunks per account; hard supported maximum 1000000 |
| `HISTORY_RETENTION_DAYS` | `7`; session and derived request retention, maximum 365 |
| `HISTORY_ALLOW_GENERATION` | `false`; permit separately configured external answers |

The embedding interface is separate from answer generation. Only the supplied local Sentence Transformers implementation is currently supported in configuration. Model changes must be intentional and compatible with that implementation. Revision, dimensions, runtime dependency versions, device and formatting/chunking version form the index manifest. Any change clears incompatible vectors and saved answers, then rebuilds selected sources from the durable queue. Incompatible spaces are never searched together.

## Model and storage

The official [EmbeddingGemma 2 model card](https://ai.google.dev/gemma/docs/embeddinggemma/model_card_2), [Sentence Transformers instructions](https://ai.google.dev/gemma/docs/embeddinggemma/inference-embeddinggemma-with-sentence-transformers), and [model repository](https://huggingface.co/google/embeddinggemma-2) were checked before implementation. The model is Apache-2.0. It produces 768-dimensional embeddings and supports truncated dimensions with normalization. Only text input is enabled here; direct audio, image and document embeddings are a separate future extension.

Pinned runtime: sentence-transformers 6.2.0, transformers 5.21.0, torch 2.15.0, Pillow 12.3.0 and torchvision 0.29.1. The current model loader initializes processor dependencies even with audio/vision encoders disabled. CPU uses float32. A compatible CUDA device uses BF16 when supported, otherwise float32; FP16 is never used.

Exact task prefixes:

```text
document: title: none | text: <source text>
search: task: search result | query: <query>
question: task: question answering | query: <question>
```

Vectors are truncated to the configured dimension and L2-normalized, checked for finite values and stored as little-endian float32. Text chunks have 1200 Unicode characters with 100-character overlap; parent content hashes invalidate all chunks on edits. Sources longer than 64000 characters fail clearly rather than silently truncating.

Each account's existing SQLite messages database holds vectors, FTS4 keyword data, scope, references, sessions and a durable outbox. No vector database service is required. Exact cosine retrieval streams vectors with bounded candidate memory, then combines semantic and keyword ranks. Search accepts account, chat, sender and RFC3339 date filters. It returns up to 20 hits, with pagination over at most 200 candidates. Exact search is linear in selected index size; the small benchmark below does not establish performance at 100000 chunks. At 256 dimensions, vectors alone require 1024 bytes per chunk, plus text, metadata, indexes and SQLite overhead.

Original text, sender identity, timestamps, reply references, media IDs, transcript job ID, known transcript language or language hint, and separate translation JSON remain distinguishable. Context includes bounded nearby messages, same-chat reply parents and later replies. Cross-chat reply references are preserved but are not automatically followed. Missing media, missing/failed transcripts, dirty sources, connection state and sync timestamps appear in coverage. Search never downloads media or starts transcription.

## Questions, sessions and actions

Local retrieval uses no paid generation call. An MCP client's own model can answer using `search_history`, `get_history_context` and `review_history_actions`.

Server-side answers require both `HISTORY_ALLOW_GENERATION=true` and explicit external consent when creating a session. The configured existing AI provider/model is used, including Vercel AI Gateway routing when configured. Selected evidence, questions and bounded prior scoped turns leave the host for that provider. Embeddings remain local. Nothing silently changes provider or model. Existing provider quotas, concurrency limits and persistent usage ledger apply. This implementation does not configure live keys or bypass existing limits.

A session fixes one account and an explicit subset of its monitored chats. Follow-up retrieval includes the last two scoped questions, and generation receives bounded prior turns. A new scope needs a new session. The UI shows the active session scope. Source inspection always rechecks the current scope and source hash.

Question mode retrieves ranked evidence with nearby/later reply context. It cannot prove an exhaustive list or find every later update. **All pending actions** uses action mode: a resumable chronological pass through the cached scope, maintaining an evidence-backed register across pages. English requests containing “all” and “pending”, “outstanding” or “open actions” switch automatically; explicitly choose action mode for other phrasing/languages. Retrieval-only chronological review provides opaque continuation cursors for a client-maintained register. Continue until `complete_for_cached_snapshot`; a source change invalidates the snapshot and requires restart.

Actions contain owner, status, verbatim stated due date, supporting citations and last supporting update. Ambiguous ownership becomes `unknown`. Tentative dates stay proposed. Completion and cancellation use later evidence. Output classifications distinguish facts, proposals, commitments, completed work, uncertainty and missing evidence. No complete-account claim is made.

Every claim/action needs existing source IDs and exact quotes. References are resolved and checked against current content before returning. Uncited model prose is discarded. These checks prove citation existence and quote accuracy; they do not mechanically prove that a model's interpretation follows from a quote. Inspect sources for consequential decisions. Generated answer quality has been tested with mocked providers, not a paid live-model evaluation.

Chronological generation processes 12 messages per page, retains at most 80 actions and 400 sources, and caps input at 256 KiB and output at 128 KiB. If evidence exceeds a bound, the job fails without claiming completeness. Narrow the scope or use retrieval-only chronological pages (20 messages each). A large review can require many paid requests; it remains cancellable and subject to existing quotas.

## MCP tools

These tools are registered only when history is enabled. Existing tools and their permissions remain unchanged.

| Tool | Purpose |
| --- | --- |
| `configure_history_index` | Select all, specific or no chats for one account |
| `start_history_index` | Start/resume local indexing; optionally request bounded older history |
| `get_history_status` | Coverage, freshness, manifest and missing evidence |
| `search_history` | Local semantic plus keyword retrieval with filters and pagination |
| `get_history_context` | Resolve a source and nearby/reply context |
| `manage_history_session` | Create, get, list or delete a scoped session |
| `review_history_actions` | Retrieval-only resumable chronological review |
| `ask_history` | Queue a consented external question or action review |
| `get_history_job` | Poll authorized progress/results |
| `cancel_history_job` | Cancel; index cancellation also pauses monitoring |
| `resume_history_job` | Fresh authenticated resume after an answer/review restart |

Example retrieval-only sequence, using placeholder IDs:

```json
{"tool":"configure_history_index","arguments":{"account_id":"example","mode":"selected","chat_ids":["example-chat@g.us"]}}
{"tool":"start_history_index","arguments":{"account_id":"example","chat_ids":["example-chat@g.us"]}}
{"tool":"search_history","arguments":{"account_id":"example","chat_ids":["example-chat@g.us"],"query":"What did we decide about delivery?","limit":5}}
```

These are illustrative tool calls, not a shell script. Source IDs are returned by search; possession of one never grants access. HTTP administration uses existing authenticated cookies and CSRF protections. OAuth/bearer requests recheck credentials during queued generation. The existing owner credentials authorize all enabled accounts; there is no new multi-user account-grant system.

## Recovery, revocation and retention

- Stable source IDs, whole-source hashes and per-message outbox versions make duplicate sync idempotent. New messages and completed transcripts queue only affected sources.
- Local indexing resumes after restart. Interrupted generation/review waits for fresh authenticated resume because bearer tokens and cookies are never persisted in jobs.
- Completed provider results are cached. Possibly billed/ambiguous work is not automatically dispatched again after restart. A failed or ambiguous request can require operator review or an intentional new request; safe billing takes precedence over silent retry.
- Scope changes purge sessions, turns, queued answers and history-generated analysis caches for that account. Deselected chats lose vectors and derived references. Compatible vectors for still-selected chats are retained.
- Source edits/deletions invalidate corresponding vectors/references and all saved history answers/turns for the account. Jobs become stale. Protocol revocation also clears the existing native derived analysis through its normal handler. Tombstones prevent duplicate sync from resurrecting deleted messages.
- Account disablement or credential revocation blocks retrieval, session access and generation. Jobs recheck current authorization before commits/provider work. Re-enabling an account preserves original caches under the existing account policy.
- Sessions expire after the configured retention period. Expiry removes derived turns, review cursors, references, old requests/results and history-generation caches. Index vectors remain while monitored sources remain cached. This does not delete original exports, media or native transcripts; their existing retention policies still apply.

Message content, transcripts and prior turns are untrusted input. Generation has no tool execution loop and cannot send WhatsApp messages, change accounts or invoke transcription. Logs omit message bodies and worker stderr; model telemetry and implicit credential forwarding are disabled. Derived stores still contain sensitive content. Protect the existing data volume and backups as you protect WhatsApp caches.

## Reproducible verification

Run ordinary checks with synthetic fixtures only:

```sh
pnpm --dir web install --frozen-lockfile
pnpm --dir web build
pnpm --dir web exec tsc --noEmit
go test -race ./...
go vet ./...
go test -tags=e2e -count=1 ./e2e/...
```

Create the output directory first (`mkdir -p work`). The optional real local-model benchmark downloads only public weights and creates synthetic conversations. It never connects to WhatsApp or calls a generation provider:

```sh
HISTORY_REAL_MODEL=1 \
HISTORY_TEST_PYTHON="$PWD/.venv-history/bin/python" \
HISTORY_TEST_MODEL_CACHE="$PWD/.cache/history-model" \
HISTORY_BENCHMARK_OUTPUT="$PWD/work/history-benchmark.json" \
go test -count=1 -run TestRealModelSyntheticBenchmark -v ./internal/history
```

Measured on a development CPU host: 30 synthetic messages, six English/Malayalam/code-switched queries, 256 dimensions. Recall@1 and recall@5 were both 6/6. Indexing including model load took 8.60 s. Warm search took 35.9–52.9 ms. Sampled worker RSS was 1254736 KiB (about 1.20 GiB), Go heap about 2.9 MiB, and SQLite plus WAL 2611896 bytes (about 2.49 MiB). Citation validation also accepts six synthetic valid responses and rejects six fabricated quotes; these responses are mocked, not live-model interpretations. This is a small deterministic retrieval smoke benchmark, not a production scale or answer-groundedness score.

Synthetic tests cover account/chat isolation, existing-tool access outside indexing selection, multilingual/transcript metadata, duplicate sync, reply context, later completion, ambiguous ownership, tentative dates, fabricated citations, prompt injection, edits/deletion, access revocation, interrupted indexing, model-space changes, session retention, review cursor integrity, cancellation and billing/restart behavior. See [verification results](HISTORY_VERIFICATION.md) for actual final check results and current limitations.
