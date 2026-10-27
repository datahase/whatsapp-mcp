# Optional AI features

AI is not needed to link WhatsApp, read chats or send messages. Enable it only
if you want voice-note transcription, translation or image analysis. Provider
calls can incur charges; use a paid key for personal media.

All settings below are made in the server's `.env` file. See
[the configuration example](../.env.example) for every option. The table lists
built-in adapters and source defaults, not a guarantee that a particular
provider account has access to every model.

Turn analysis on in `.env` with `AI_ENABLED=true`, choose providers, set the
matching key, then run `docker compose up -d`. Two roles can use different
providers:

- `AI_PROVIDER`: translation and image analysis.
- `AI_TRANSCRIBE_PROVIDER`: voice-note transcription (default: same as
  `AI_PROVIDER`).

| Provider | Key variable | Transcription | Translation / images | Default models (transcribe / analysis) |
| --- | --- | --- | --- | --- |
| `gemini` (default) | `GEMINI_API_KEY` | Yes (native) | Yes | `gemini-3.5-transcribe` / `gemini-3.5-flash-lite`, quality `gemini-3.8-flash` |
| `openai` | `OPENAI_API_KEY` | Yes (`/audio/transcriptions`) | Yes | `gpt-4o-transcribe` / set `AI_MODEL` |
| `groq` | `GROQ_API_KEY` | Yes (`/audio/transcriptions`) | Yes | `whisper-large-v3` / set `AI_MODEL` |
| `openrouter` | `OPENROUTER_API_KEY` | Yes (audio-capable chat models) | Yes | set both |
| `anthropic` | `ANTHROPIC_API_KEY` | No: pair with a transcription provider | Yes | — / `claude-opus-5-5` (`claude-haiku-5-5` is cheaper) |
| `deepgram` | `DEEPGRAM_API_KEY` | Yes (speech-to-text only) | No | `nova-3` |
| `elevenlabs` | `ELEVENLABS_API_KEY` | Yes (speech-to-text only) | No | `scribe_v2` |
| `assemblyai` | `ASSEMBLYAI_API_KEY` | Yes (speech-to-text only; transcript deleted after use) | No | `universal-3-5-pro` |
| `openai_compatible` | `AI_API_KEY` | Configurable | Yes | set `AI_BASE_URL` and both models |

Examples for `.env`:

```sh
# Gemini for everything (default models)
AI_ENABLED=true
GEMINI_API_KEY=...

# Claude for translation and images, Deepgram for transcription
AI_ENABLED=true
AI_PROVIDER=anthropic
AI_TRANSCRIBE_PROVIDER=deepgram
ANTHROPIC_API_KEY=...
DEEPGRAM_API_KEY=...

# OpenRouter for everything
AI_ENABLED=true
AI_PROVIDER=openrouter
OPENROUTER_API_KEY=...
AI_TRANSCRIBE_MODEL=google/gemini-3.5-flash
AI_MODEL=google/gemini-3.5-flash-lite
```

Change a model by editing `AI_TRANSCRIBE_MODEL`, `AI_MODEL` or
`AI_QUALITY_MODEL` and restarting. MCP clients cannot choose models, so a
model cannot escalate cost on its own; `AI_QUALITY_MODEL` is used only when a
call sets `high_quality: true`. OpenRouter `:free` model IDs are refused
unless `AI_ALLOW_FREE_MODELS=true`, because free variants may log prompts.

App allowances: 30 audio minutes and 50 requests per UTC day, 10 minutes per
note (`AI_DAILY_AUDIO_MINUTES`, `AI_DAILY_REQUESTS`). These are local limits,
not a provider billing cap. Audio uploaded to Gemini's Files API is deleted
after each job, and AssemblyAI transcripts are deleted after retrieval (failed
deletions are retried; AssemblyAI offers no API to delete a raw upload).
Deletion does not guarantee zero provider-side retention. Calls may wait up
to 45 seconds; longer jobs return a `job_id` for `get_analysis_job`.

Speech-to-text models differ in language coverage. For Malayalam or mixed
Malayalam/English voice notes, check your chosen model's language list, or
pass a `language_hint`.

To check a provider without any WhatsApp data, run the fixture command with a
non-sensitive file:

```sh
docker compose run --rm -v "$PWD/sample.ogg:/fixtures/sample.ogg:ro" whatsapp-mcp \
  ai-smoke -tier paid -audio /fixtures/sample.ogg
```

[Back to setup](../README.md)
