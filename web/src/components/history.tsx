"use client";
import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogDescription,
  DialogTitle,
} from "@/components/ui/dialog";

type Account = { account_id: string; label: string; state: string };
type Source = {
  source_id: string;
  chat_jid: string;
  message_id: string;
  sender: string;
  timestamp: string;
  kind: string;
  text: string;
  transcript_language?: string;
  language_hint?: string;
  translation?: unknown;
  reply_id?: string;
  media_id?: string;
};
type Coverage = {
  cached_messages: number;
  content_epoch: number;
  chat_ids: string[];
  indexed_documents: number;
  pending_messages: number;
  missing_transcripts: number;
  failed_transcripts: number;
  media_not_cached: number;
  earliest_cached?: string;
  latest_cached?: string;
  warnings: string[];
};
type Answer = {
  answer: string;
  claims: {
    kind: string;
    text: string;
    citations: { source_id: string; quote: string }[];
  }[];
  actions: {
    id: string;
    description: string;
    owner: string;
    status: string;
    stated_due_date: string;
    due_date_kind: string;
    last_supporting_update: string;
    citations: { source_id: string; quote: string }[];
  }[];
  sources: Source[];
  coverage: Coverage;
  warnings: string[];
  review_complete_for_cached_scope: boolean;
};
type Job = {
  job_id: string;
  session_id?: string;
  kind: string;
  status: string;
  processed: number;
  error?: string;
  result?: Answer;
};
type Status = {
  options: {
    enabled: boolean;
    generation_enabled: boolean;
    embedding?: { model: string; dimensions: number };
    external_generation_disclosure?: string;
  };
  chats: { jid: string; name: string }[];
  selection: { mode: string; chat_ids: string[]; epoch: number };
  coverage?: Coverage;
  jobs: Job[];
  sessions: {
    session_id: string;
    chat_ids: string[];
    external_consent: boolean;
  }[];
  onboarding_dismissed?: boolean;
};
async function api<T>(
  account: string,
  operation: string,
  csrf?: string,
  body?: unknown,
): Promise<T> {
  const response = await fetch(
    `/admin/api/history/${encodeURIComponent(account)}/${operation}`,
    {
      method: body === undefined ? "GET" : "POST",
      credentials: "same-origin",
      headers: {
        Accept: "application/json",
        ...(body === undefined
          ? {}
          : { "Content-Type": "application/json", "X-CSRF-Token": csrf || "" }),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    },
  );
  const result = await response.json();
  if (!response.ok) throw new Error(result.error || "History request failed");
  return result;
}
export function HistoryOnboarding({
  account,
  csrf,
  open,
}: {
  account: Account;
  csrf: string;
  open: () => void;
}) {
  const [show, setShow] = useState(false);
  const [enabled, setEnabled] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    let active = true;
    if (account.state !== "connected") return;
    api<Status>(account.account_id, "status")
      .then((v) => {
        if (active) {
          setShow(v.selection.mode === "none" && !v.onboarding_dismissed);
          setEnabled(v.options.enabled);
        }
      })
      .catch(() => {});
    return () => {
      active = false;
    };
  }, [account.account_id, account.state]);
  if (!show) return null;
  return (
    <section
      className="history-notice mb-7"
      aria-label="Prepare WhatsApp history search"
    >
      <h2>Chat with your WhatsApp history</h2>
      <p className="subtext mt-2">
        Prepare searchable history for this account. Choose all available chats
        or specific conversations. This controls local monitoring only. Your
        assistant&apos;s existing access to other conversations stays the same.
      </p>
      <p className="subtext mt-2 text-sm">
        WhatsApp may sync only part of your history. Voice notes are searchable
        after you explicitly transcribe them.
      </p>
      {error && <p role="alert">{error}</p>}
      <div className="flex flex-wrap gap-2 mt-4">
        <Button
          disabled={busy || !enabled}
          onClick={async () => {
            setBusy(true);
            try {
              await api(account.account_id, "selection", csrf, { mode: "all" });
              setShow(false);
              open();
            } catch (e) {
              setError((e as Error).message);
            } finally {
              setBusy(false);
            }
          }}
        >
          Index all available chats
        </Button>
        <Button variant="outline" onClick={open}>
          {enabled
            ? "Choose chats and accounts"
            : "Set up local history search"}
        </Button>
        <Button
          variant="ghost"
          disabled={busy}
          onClick={async () => {
            setBusy(true);
            try {
              await api(account.account_id, "dismiss", csrf, {});
              setShow(false);
            } catch (e) {
              setError((e as Error).message);
            } finally {
              setBusy(false);
            }
          }}
        >
          Not now
        </Button>
      </div>
    </section>
  );
}
export default function HistoryPanel({
  accounts,
  csrf,
  initialAccount,
}: {
  accounts: Account[];
  csrf: string;
  initialAccount?: string | null;
}) {
  const [account, setAccount] = useState(
    initialAccount || accounts[0]?.account_id || "",
  );
  const selectedAccount = useRef(account);
  selectedAccount.current = account;
  const [status, setStatus] = useState<Status | null>(null);
  const [mode, setMode] = useState("selected");
  const [chats, setChats] = useState<string[]>([]);
  const [session, setSession] = useState("");
  const [consent, setConsent] = useState(false);
  const [sync, setSync] = useState(false);
  const [question, setQuestion] = useState("");
  const [queryMode, setQueryMode] = useState("search");
  const [sender, setSender] = useState("");
  const [after, setAfter] = useState("");
  const [before, setBefore] = useState("");
  const [hits, setHits] = useState<{ source: Source; context: Source[] }[]>([]);
  const [answer, setAnswer] = useState<Answer | null>(null);
  const [review, setReview] = useState<{
    sources: Source[];
    next_cursor?: string;
    complete_for_cached_snapshot: boolean;
  } | null>(null);
  const [inspected, setInspected] = useState<Source[] | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [warnings, setWarnings] = useState<string[]>([]);
  const configured = useRef("");
  const revision = useRef(0);
  const scopeFingerprint = useRef("");
  const contentFingerprint = useRef("");
  const refresh = useCallback(async () => {
    if (!account) return;
    const id = account;
    const seq = revision.current;
    const result = await api<Status>(id, "status");
    if (selectedAccount.current !== id || seq !== revision.current) return;
    const fingerprint =
      id + ":" + (result.selection as { epoch?: number }).epoch;
    if (scopeFingerprint.current && scopeFingerprint.current !== fingerprint) {
      revision.current++;
      configured.current = "";
      setSession("");
      setHits([]);
      setAnswer(null);
      setReview(null);
      setInspected(null);
      setBusy(false);
    }
    const content = id + ":" + result.coverage?.content_epoch;
    if (contentFingerprint.current && contentFingerprint.current !== content) {
      revision.current++;
      setHits([]);
      setAnswer(null);
      setReview(null);
      setInspected(null);
      setBusy(false);
      setWarnings([
        "History changed. Search or review again for current evidence.",
      ]);
    }
    contentFingerprint.current = content;
    scopeFingerprint.current = fingerprint;
    setStatus(result);
    if (configured.current !== id) {
      configured.current = id;
      setMode(
        result.selection.mode === "none" ? "selected" : result.selection.mode,
      );
      setChats(result.selection.chat_ids);
    }
  }, [account]);
  useEffect(() => {
    revision.current++;
    scopeFingerprint.current = "";
    contentFingerprint.current = "";
    setBusy(false);
    configured.current = "";
    setStatus(null);
    setChats([]);
    setSession("");
    setConsent(false);
    setQuestion("");
    setHits([]);
    setAnswer(null);
    setReview(null);
    setInspected(null);
    setError("");
    setWarnings([]);
    refresh().catch((e) => {
      if (selectedAccount.current === account) setError(e.message);
    });
    const timer = setInterval(() => refresh().catch(() => {}), 3000);
    return () => {
      revision.current++;
      clearInterval(timer);
    };
  }, [account, refresh]);
  async function perform<T>(
    operation: string,
    body: unknown,
  ): Promise<T | undefined> {
    const id = account;
    const seq = revision.current;
    setBusy(true);
    setError("");
    try {
      const result = await api<T>(id, operation, csrf, body);
      if (id !== selectedAccount.current || seq !== revision.current) return;
      return result;
    } catch (e) {
      if (id === selectedAccount.current && seq === revision.current)
        setError((e as Error).message);
    } finally {
      if (id === selectedAccount.current && seq === revision.current)
        setBusy(false);
    }
  }
  async function ensureSession(): Promise<string | undefined> {
    if (session) return session;
    const v = await perform<{ session_id: string }>("session", {
      chat_ids: status?.selection.chat_ids || [],
      external_consent: consent,
    });
    if (v) {
      setSession(v.session_id);
      return v.session_id;
    }
  }
  const coverage = status?.coverage;
  const activeScope =
    status?.sessions.find((s) => s.session_id === session)?.chat_ids ||
    status?.selection.chat_ids ||
    [];
  const inspect = async (id: string) => {
    const data = await perform<Source[]>("context", {
      chat_ids: activeScope,
      source_id: id,
    });
    if (data) setInspected(data);
  };
  const cite = (id: string) => (
    <button className="history-cite" key={id} onClick={() => inspect(id)}>
      Inspect source
    </button>
  );
  if (!accounts.length)
    return (
      <>
        <h1>WhatsApp history</h1>
        <p className="subtext mt-3">
          Link an account first, then choose which conversations to monitor.
        </p>
      </>
    );
  return (
    <div className="history-page">
      <h1>Chat with your WhatsApp history</h1>
      <p className="subtext mt-2">
        Search locally or ask grounded questions about selected conversations.
        Cached history is never a complete account backup.
      </p>
      <label className="history-field mt-6">
        Account
        <select value={account} onChange={(e) => setAccount(e.target.value)}>
          {accounts.map((a) => (
            <option key={a.account_id} value={a.account_id}>
              {a.label}
            </option>
          ))}
        </select>
      </label>
      <p className="subtext text-xs mt-2">
        Configure each account separately. Monitoring choices do not restrict
        the existing WhatsApp tools.
      </p>
      {error && (
        <p role="alert" className="error-note mt-4">
          {error}
        </p>
      )}
      {!status ? (
        <p role="status" className="subtext mt-6">
          Loading history settings…
        </p>
      ) : !status.options.enabled ? (
        <section className="history-notice mt-6">
          <h2>Enable the local embedding runtime</h2>
          <p className="subtext mt-2">
            This optional feature needs the pinned EmbeddingGemma 2 runtime on
            the server and <code>HISTORY_ENABLED=true</code>. Model weights stay
            in the private data volume. External answers are off by default.
          </p>
          <p className="subtext mt-2">
            See{" "}
            <a
              href="https://github.com/datahase/whatsapp-mcp/blob/main/docs/HISTORY.md"
              target="_blank"
              rel="noreferrer"
            >
              local history setup
            </a>
            . Existing WhatsApp features keep working.
          </p>
        </section>
      ) : (
        <>
          <section className="history-section">
            <h2>Conversations to monitor</h2>
            <div className="flex flex-wrap gap-5 mt-4">
              {[
                ["all", "All current and future chats"],
                ["selected", "Specific chats"],
                ["none", "Stop monitoring"],
              ].map(([value, label]) => (
                <label className="history-check" key={value}>
                  <input
                    type="radio"
                    name="history-selection"
                    checked={mode === value}
                    onChange={() => setMode(value)}
                  />
                  {label}
                </label>
              ))}
            </div>
            {mode === "selected" && (
              <div className="history-chat-list mt-4">
                {status.chats.length ? (
                  status.chats.map((c) => (
                    <label key={c.jid} className="history-check">
                      <input
                        type="checkbox"
                        checked={chats.includes(c.jid)}
                        onChange={(e) =>
                          setChats((prev) =>
                            e.target.checked
                              ? [...prev, c.jid]
                              : prev.filter((x) => x !== c.jid),
                          )
                        }
                      />
                      {c.name || c.jid}
                    </label>
                  ))
                ) : (
                  <p className="subtext">
                    No chats are cached yet. Wait for WhatsApp to provide
                    available history.
                  </p>
                )}
              </div>
            )}
            <div className="flex gap-2 mt-4">
              <Button
                disabled={busy || (mode === "selected" && !chats.length)}
                onClick={async () => {
                  const v = await perform("selection", {
                    mode,
                    chat_ids: mode === "selected" ? chats : [],
                  });
                  if (v) {
                    setSession("");
                    setAnswer(null);
                    setHits([]);
                    setReview(null);
                    configured.current = "";
                    await refresh();
                  }
                }}
              >
                {mode === "none"
                  ? "Stop and remove history artifacts"
                  : "Save and start local indexing"}
              </Button>
            </div>
            <p className="subtext text-xs mt-3">
              Stopping removes vectors, history sessions and derived history
              answers. Original cached messages, transcripts, exports and media
              remain available through existing tools.
            </p>
          </section>
          {coverage && (
            <section className="history-section">
              <h2>Coverage and indexing</h2>
              <div className="history-metrics mt-4">
                <span>
                  <strong>{coverage.cached_messages}</strong> cached messages
                </span>
                <span>
                  <strong>{coverage.indexed_documents}</strong> indexed chunks
                </span>
                <span>
                  <strong>{coverage.pending_messages}</strong> waiting
                </span>
                <span>
                  <strong>{coverage.missing_transcripts}</strong> audio without
                  transcripts
                </span>
                <span>
                  <strong>{coverage.media_not_cached}</strong> media not cached
                </span>
              </div>
              <p className="subtext text-xs mt-3">
                {coverage.earliest_cached || "Unknown start"} to{" "}
                {coverage.latest_cached || "unknown end"}.{" "}
                {coverage.failed_transcripts} failed or unresolved
                transcriptions.
              </p>
              <ul className="history-warnings">
                {coverage.warnings.map((w) => (
                  <li key={w}>{w}</li>
                ))}
              </ul>
              <label className="history-check mt-3">
                <input
                  type="checkbox"
                  checked={sync}
                  onChange={(e) => setSync(e.target.checked)}
                />
                Also request available older history (up to 50 messages per
                chat)
              </label>
              <Button
                className="mt-3"
                variant="outline"
                disabled={busy}
                onClick={async () => {
                  await perform("index", {
                    chat_ids: status.selection.chat_ids,
                    request_sync: sync,
                  });
                  await refresh();
                }}
              >
                Resume indexing{sync ? " and request sync" : ""}
              </Button>
              <div className="mt-4">
                {status.jobs.map((j) => (
                  <div className="history-job" key={j.job_id}>
                    <div>
                      <span>
                        {j.kind === "index"
                          ? "Local indexing"
                          : j.kind === "actions"
                            ? "Chronological action review"
                            : "Grounded answer"}
                        : {j.status}
                      </span>
                      <small className="subtext">
                        {j.processed} source messages reviewed/indexed
                      </small>
                      {j.error && <p className="error-note">{j.error}</p>}
                    </div>
                    {["queued", "running"].includes(j.status) && (
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={async () => {
                          await perform("cancel", { job_id: j.job_id });
                          await refresh();
                        }}
                      >
                        Cancel
                      </Button>
                    )}
                    {j.status === "awaiting_authorization" && (
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={async () => {
                          await perform("resume", { job_id: j.job_id });
                          await refresh();
                        }}
                      >
                        Resume with current access
                      </Button>
                    )}
                    {j.status === "completed" && j.result && (
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => {
                          setAnswer(j.result || null);
                          setSession(j.session_id || "");
                          setConsent(
                            status.sessions.find(
                              (s) => s.session_id === j.session_id,
                            )?.external_consent || false,
                          );
                        }}
                      >
                        View answer
                      </Button>
                    )}
                  </div>
                ))}
              </div>
            </section>
          )}
          {!!status.selection.chat_ids.length && (
            <section className="history-section">
              <h2>Ask or search</h2>
              <label className="history-field mt-4">
                Session
                <select
                  value={session}
                  onChange={(e) => {
                    setSession(e.target.value);
                    setAnswer(null);
                    setHits([]);
                    setReview(null);
                    const v = status.sessions.find(
                      (s) => s.session_id === e.target.value,
                    );
                    setConsent(v?.external_consent || false);
                  }}
                >
                  <option value="">
                    New session in current selected scope
                  </option>
                  {status.sessions.map((s) => (
                    <option value={s.session_id} key={s.session_id}>
                      {s.session_id.slice(0, 18)} · {s.chat_ids.length} chats ·{" "}
                      {s.external_consent
                        ? "external answers allowed"
                        : "local retrieval"}
                    </option>
                  ))}
                </select>
              </label>
              <p className="subtext text-xs mt-2">
                A session keeps its original account and chat scope for
                follow-up questions. Active scope:{" "}
                {activeScope
                  .map((c) => status.chats.find((x) => x.jid === c)?.name || c)
                  .join(", ")}
                .
              </p>
              <label className="history-field mt-4">
                Mode
                <select
                  value={queryMode}
                  onChange={(e) => setQueryMode(e.target.value)}
                >
                  <option value="search">
                    Local semantic and keyword search
                  </option>
                  <option value="review">
                    Chronological review, no server model call
                  </option>
                  <option
                    value="question"
                    disabled={!status.options.generation_enabled}
                  >
                    Grounded answer from configured AI provider
                  </option>
                  <option
                    value="actions"
                    disabled={!status.options.generation_enabled}
                  >
                    Review all cached history for pending actions
                  </option>
                </select>
              </label>
              <p className="subtext text-xs mt-2">
                {status.options.generation_enabled
                  ? status.options.external_generation_disclosure
                  : "Server-side answers are disabled. Your MCP client can answer using retrieval-only tools. Enable HISTORY_ALLOW_GENERATION only if you want selected evidence sent to your configured AI provider."}
              </p>
              {["question", "actions"].includes(queryMode) && !session && (
                <label className="history-check mt-3">
                  <input
                    type="checkbox"
                    checked={consent}
                    onChange={(e) => setConsent(e.target.checked)}
                  />
                  I agree to send selected evidence and questions to the
                  configured AI provider for this session. Calls may incur
                  charges.
                </label>
              )}
              <form
                className="mt-4"
                onSubmit={async (e) => {
                  e.preventDefault();
                  setAnswer(null);
                  setHits([]);
                  setReview(null);
                  setWarnings([]);
                  if (queryMode === "search") {
                    const result = await perform<{
                      hits: typeof hits;
                      warnings: string[];
                    }>("search", {
                      chat_ids: activeScope,
                      query: question,
                      sender,
                      ...(after
                        ? { after: new Date(after).toISOString() }
                        : {}),
                      ...(before
                        ? { before: new Date(before).toISOString() }
                        : {}),
                    });
                    if (result) {
                      setHits(result.hits);
                      setWarnings(result.warnings);
                    }
                  } else {
                    const sid = await ensureSession();
                    if (!sid) return;
                    if (queryMode === "review") {
                      const result = await perform<typeof review>("review", {
                        session_id: sid,
                      });
                      if (result) setReview(result);
                    } else {
                      await perform("ask", {
                        session_id: sid,
                        question,
                        mode: queryMode,
                      });
                      await refresh();
                    }
                  }
                }}
              >
                <label className="history-field">
                  Question
                  <Input
                    value={question}
                    onChange={(e) => setQuestion(e.target.value)}
                    maxLength={2000}
                    placeholder="What did we decide about the launch?"
                    required={queryMode !== "review"}
                  />
                </label>
                {queryMode === "search" && (
                  <div className="history-filters mt-3">
                    <label className="history-field">
                      Sender ID (optional)
                      <Input
                        value={sender}
                        onChange={(e) => setSender(e.target.value)}
                      />
                    </label>
                    <label className="history-field">
                      From
                      <Input
                        type="datetime-local"
                        value={after}
                        onChange={(e) => setAfter(e.target.value)}
                      />
                    </label>
                    <label className="history-field">
                      Until
                      <Input
                        type="datetime-local"
                        value={before}
                        onChange={(e) => setBefore(e.target.value)}
                      />
                    </label>
                  </div>
                )}
                <div className="flex flex-wrap gap-2 mt-4">
                  <Button
                    disabled={
                      busy ||
                      (["question", "actions"].includes(queryMode) && !consent)
                    }
                  >
                    {busy
                      ? "Working…"
                      : queryMode === "search"
                        ? "Search locally"
                        : queryMode === "review"
                          ? "Start chronological review"
                          : "Ask with selected evidence"}
                  </Button>
                  {session && (
                    <Button
                      type="button"
                      variant="ghost"
                      onClick={async () => {
                        await perform("delete-session", {
                          session_id: session,
                        });
                        setSession("");
                        setAnswer(null);
                        setReview(null);
                        await refresh();
                      }}
                    >
                      Delete session
                    </Button>
                  )}
                </div>
              </form>
            </section>
          )}
          {warnings.length > 0 && (
            <ul className="history-warnings">
              {warnings.map((w) => (
                <li key={w}>{w}</li>
              ))}
            </ul>
          )}
          {hits.map((h) => (
            <article className="history-evidence" key={h.source.source_id}>
              <p className="subtext text-xs">
                {h.source.kind} · {h.source.sender} · {h.source.timestamp}
              </p>
              <p className="whitespace-pre-wrap mt-2">{h.source.text}</p>
              {cite(h.source.source_id)}
            </article>
          ))}
          {review && (
            <section className="history-section">
              <h2>Chronological evidence</h2>
              <p className="subtext mt-2">
                {review.complete_for_cached_snapshot
                  ? "Reached the end of this cached snapshot. Missing history and transcripts can still contain actions."
                  : "More cached messages remain. Continue reviewing before claiming all pending actions."}
              </p>
              {review.sources.map((s) => (
                <article key={s.source_id} className="history-evidence">
                  <p className="subtext text-xs">
                    {s.timestamp} · {s.sender} · {s.kind}
                  </p>
                  <p className="mt-2 whitespace-pre-wrap">{s.text}</p>
                  {cite(s.source_id)}
                </article>
              ))}
              {review.next_cursor && (
                <Button
                  disabled={busy}
                  onClick={async () => {
                    const result = await perform<typeof review>("review", {
                      session_id: session,
                      cursor: review.next_cursor,
                    });
                    if (result) setReview(result);
                  }}
                >
                  Review next page
                </Button>
              )}
            </section>
          )}
          {answer && (
            <section className="history-section">
              <h2>Grounded findings</h2>
              <p className="subtext mt-2">{answer.answer}</p>
              {answer.review_complete_for_cached_scope && (
                <p className="subtext mt-2">
                  Chronological review reached the end of the selected cached
                  snapshot.
                </p>
              )}
              {answer.claims?.map((c, i) => (
                <article className="history-evidence" key={i}>
                  <span className="history-kind">
                    {c.kind.replaceAll("_", " ")}
                  </span>
                  <p>{c.text}</p>
                  {c.citations.map((ref) => cite(ref.source_id))}
                </article>
              ))}
              {answer.actions?.map((a) => (
                <article className="history-evidence" key={a.id}>
                  <h3>{a.description}</h3>
                  <p className="subtext mt-2">
                    Owner: {a.owner} · {a.status.replaceAll("_", " ")}
                  </p>
                  {a.stated_due_date && (
                    <p>
                      {a.due_date_kind === "proposed"
                        ? "Tentative date"
                        : a.due_date_kind === "stated"
                          ? "Stated due date"
                          : "Date interpretation uncertain"}
                      : {a.stated_due_date}
                    </p>
                  )}
                  <p className="subtext text-xs mt-2">
                    Last supporting update: {a.last_supporting_update}
                  </p>
                  {a.citations.map((ref) => cite(ref.source_id))}
                </article>
              ))}
              <ul className="history-warnings">
                {answer.warnings?.map((w) => (
                  <li key={w}>{w}</li>
                ))}
              </ul>
            </section>
          )}
        </>
      )}
      <Dialog
        open={!!inspected}
        onOpenChange={(open) => {
          if (!open) setInspected(null);
        }}
      >
        <DialogContent className="history-source-dialog">
          <DialogHeader>
            <DialogTitle>Source messages and context</DialogTitle>
            <DialogDescription>
              Current source text, separate translations and nearby messages in
              the authorized chat scope.
            </DialogDescription>
          </DialogHeader>
          <div className="history-source-scroll">
            {inspected?.map((s) => (
              <article className="history-evidence" key={s.source_id}>
                <p className="subtext text-xs">
                  {s.timestamp} · {s.sender} · {s.kind}
                </p>
                <p className="whitespace-pre-wrap mt-2">{s.text}</p>
                {s.transcript_language && (
                  <p className="subtext text-xs">
                    Transcript language: {s.transcript_language}
                  </p>
                )}
                {s.language_hint && (
                  <p className="subtext text-xs">
                    Language hint, not detected language: {s.language_hint}
                  </p>
                )}
                {s.translation != null && (
                  <details className="mt-2">
                    <summary>Separate translation</summary>
                    <pre>{JSON.stringify(s.translation, null, 2)}</pre>
                  </details>
                )}
                <p className="subtext text-xs mt-3">
                  Chat: {s.chat_jid}
                  <br />
                  Message: {s.message_id}
                  {s.reply_id && (
                    <>
                      <br />
                      Reply to: {s.reply_id}
                    </>
                  )}
                </p>
              </article>
            ))}
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
