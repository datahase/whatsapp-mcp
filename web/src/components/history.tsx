"use client";
import { useCallback, useEffect, useRef, useState } from "react";
import { countLabel, formatHistoryDate, historyTranslationText } from "@/lib/history-display";
import { filterChats } from "@/lib/history-chats";
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
        Choose conversations to make their messages searchable. Include all
        conversations or just the ones you need.
      </p>
      <p className="subtext mt-2 text-sm">
        WhatsApp may share only some older messages. Voice notes need a
        transcript before their spoken content can be searched.
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
          Use all conversations
        </Button>
        <Button variant="outline" onClick={open}>
          {enabled
            ? "Choose conversations"
            : "Set up history search"}
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
  const [chatSearch, setChatSearch] = useState("");
  const [chats, setChats] = useState<string[]>([]);
  const [session, setSession] = useState("");
  const [consent, setConsent] = useState(false);
  const [sync, setSync] = useState(false);
  const [question, setQuestion] = useState("");
  const [queryMode, setQueryMode] = useState("search");
  const [sender, setSender] = useState("");
  const [after, setAfter] = useState("");
  const [before, setBefore] = useState("");
  const [searched, setSearched] = useState(false);
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
  const [notice, setNotice] = useState("");
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
      setSearched(false);
      setAnswer(null);
      setReview(null);
      setInspected(null);
      setBusy(false);
    }
    const content = id + ":" + result.coverage?.content_epoch;
    if (contentFingerprint.current && contentFingerprint.current !== content) {
      revision.current++;
      setHits([]);
      setSearched(false);
      setAnswer(null);
      setReview(null);
      setInspected(null);
      setBusy(false);
      setNotice("New or edited messages arrived. Search again to include the latest updates.");
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
    setChatSearch("");
    setSession("");
    setConsent(false);
    setQuestion("");
    setSearched(false);
    setHits([]);
    setAnswer(null);
    setReview(null);
    setInspected(null);
    setError("");
    setWarnings([]);
    setNotice("");
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
  const visibleChats = filterChats(status?.chats || [], chatSearch);
  // Keep the current index attempt visible; older failed attempts do not
  // describe current coverage after a successful resume.
  const visibleJobs = status?.jobs.filter((j, i, jobs) =>
    j.kind !== "index" || jobs.findIndex((x) => x.kind === "index") === i) || [];
  const coverage = status?.coverage;
  const indexJob = visibleJobs.find((j) => j.kind === "index");
  const preparing = !!indexJob && ["queued", "running"].includes(indexJob.status);
  const needsRetry = indexJob?.status === "failed" && !!coverage?.pending_messages;
  const paused = indexJob?.status === "cancelled" && !!coverage?.pending_messages;
  const selectionChanged = !!status && (mode !== status.selection.mode ||
    (mode === "selected" && [...chats].sort().join("|") !== [...status.selection.chat_ids].sort().join("|")));
  const chatName = (jid: string) => status?.chats.find((c) => c.jid === jid)?.name || jid.split("@")[0];
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
      View original messages
    </button>
  );
  if (!accounts.length)
    return (
      <>
        <h1>WhatsApp history</h1>
        <p className="subtext mt-3">
          Link your WhatsApp account first, then choose conversations to search.
        </p>
      </>
    );
  return (
    <div className="history-page">
      <h1>Your WhatsApp history</h1>
      <p className="subtext mt-2">
        Find past messages, catch up on discussions, and revisit decisions.
      </p>
      {accounts.length > 1 ? <label className="history-field mt-6">
        WhatsApp account
        <select value={account} onChange={(e) => setAccount(e.target.value)}>
          {accounts.map((a) => (
            <option key={a.account_id} value={a.account_id}>
              {a.label}
            </option>
          ))}
        </select>
      </label> : <p className="subtext text-sm mt-3">Using {accounts[0].label}</p>}
      {error && (
        <p role="alert" className="error-note mt-4">
          {error}
        </p>
      )}
      {notice && <p role="status" className="subtext mt-4">{notice}</p>}
      {!status ? (
        <p role="status" className="subtext mt-6">
          Loading history settings…
        </p>
      ) : !status.options.enabled ? (
        <section className="history-notice mt-6">
          <h2>Set up history search</h2>
          <p className="subtext mt-2">
            History search is not enabled on this server yet. The server owner
            can follow the setup guide to turn it on. Your WhatsApp connection
            keeps working.
          </p>
          <p className="subtext mt-2">
            See{" "}
            <a
              href="https://github.com/datahase/whatsapp-mcp/blob/main/docs/HISTORY.md"
              target="_blank"
              rel="noreferrer"
            >
              history search setup
            </a>
            .
          </p>
        </section>
      ) : (
        <>
          {!status.selection.chat_ids.length && <p className="history-intro subtext">Start by choosing the conversations you want to search below. Your messages will be prepared on this server.</p>}
          <details className="history-section history-settings" open={!status.selection.chat_ids.length || undefined}>
            <summary>{status.selection.chat_ids.length ? `Change conversations (${status.selection.chat_ids.length} included)` : "Choose conversations to search"}</summary>
            <p className="subtext text-sm mt-3">Choose which conversations this server prepares for history search. You can change this later.</p>
            <div className="flex flex-wrap gap-5 mt-4">
              {[
                ["all", "All conversations"],
                ["selected", "Choose conversations"],
                ["none", "Turn off history search"],
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
            {mode === "all" && <p className="subtext text-sm mt-3">Includes every conversation on this account, including new ones.</p>}
            {mode === "selected" && (
              <div className="mt-4">
                <label className="history-field">
                  Search conversations
                  <Input type="search" placeholder="Search by name or number"
                    value={chatSearch} onChange={(e) => setChatSearch(e.target.value)} />
                </label>
                <p className="subtext text-xs mt-2" role="status">
                  {chats.length} selected · {countLabel(visibleChats.length, "conversation")} shown
                </p>
                <div className="history-chat-list mt-3" aria-label="Choose conversations">
                {visibleChats.length ? (
                  visibleChats.map((c) => (
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
                      <span className="history-chat-name">{c.name || c.jid.split("@")[0]}</span>
                    </label>
                  ))
                ) : (
                  <p className="subtext">
                    {status.chats.length ? "No conversations match your search." : "No chats are cached yet. Wait for WhatsApp to provide available history."}
                  </p>
                )}
                </div>
              </div>
            )}
            <div className="flex gap-2 mt-4">
              <Button
                disabled={busy || (mode === "selected" && !chats.length) || (!selectionChanged && !!status.selection.chat_ids.length)}
                onClick={async () => {
                  const v = await perform("selection", {
                    mode,
                    chat_ids: mode === "selected" ? chats : [],
                  });
                  if (v) {
                    setSession("");
                    setAnswer(null);
                    setHits([]);
      setSearched(false);
                    setReview(null);
                    configured.current = "";
                    contentFingerprint.current = "";
                    scopeFingerprint.current = "";
                    setWarnings([]);
                    setNotice("");
                    await refresh();
                  }
                }}
              >
                {busy ? "Saving…" : mode === "none" ? "Turn off history search" : "Save conversations"}
              </Button>
            </div>
            {selectionChanged && <p className="subtext text-sm mt-3" role="status">Save your choices to use them for future searches.</p>}
            <p className="subtext text-xs mt-3">
              {mode === "none" ? "Turning this off removes the search index and saved question sessions. Original messages, transcripts and media stay on the server." : "New messages in these conversations will be prepared automatically. Your assistant's access to other conversations stays the same."}
            </p>
          </details>
          {coverage && (
            <section className="history-preparation">
              <div className="history-preparation-main">
                <div>
                  <h2>{needsRetry ? "Message preparation stopped" : paused ? "Preparation paused" : !coverage.indexed_documents && !coverage.pending_messages ? "No searchable text yet" : preparing ? "Preparing your messages" : coverage.pending_messages ? "More messages to prepare" : "Ready to search"}</h2>
                  <p className="subtext text-sm mt-2" role="status">
                    {needsRetry ? "Try again to continue where it stopped. Your saved messages are safe." : paused ? "Continue whenever you are ready. Prepared messages are still searchable." : !coverage.indexed_documents && !coverage.pending_messages ? "These conversations have no searchable text yet. Voice notes need a transcript first." : preparing ? `${countLabel(coverage.pending_messages, "message")} left to check. You can leave this page while we work.` : coverage.pending_messages ? `${countLabel(coverage.pending_messages, "message")} waiting to be prepared.` : "Available text in your chosen conversations is searchable."}
                  </p>
                  {coverage.pending_messages > 0 && coverage.indexed_documents > 0 && <p className="subtext text-sm mt-2">You can already search the messages prepared so far.</p>}
                </div>
                <Button variant="outline" disabled={busy || preparing}
                  onClick={async () => { await perform("index", { chat_ids: status.selection.chat_ids, request_sync: sync }); await refresh(); }}>
                  {preparing ? "Preparing…" : needsRetry ? "Try again" : paused ? "Continue preparation" : "Check for updates"}
                </Button>
              </div>
              <details className="history-details mt-4">
                <summary>What is included?</summary>
                <p className="subtext text-sm mt-3">{coverage.cached_messages.toLocaleString()} saved messages, from {formatHistoryDate(coverage.earliest_cached, false)} to {formatHistoryDate(coverage.latest_cached, false)}. WhatsApp may provide only part of your history. This is not a full backup.</p>
                {coverage.missing_transcripts > 0 && <p className="subtext text-sm mt-2">{coverage.missing_transcripts.toLocaleString()} voice notes do not have a transcript yet. Their spoken content will not appear in search.</p>}
                <p className="subtext text-sm mt-2">Searching does not download attachments or transcribe voice notes. Transcription is a separate action and may cost money.</p>
                <label className="history-check mt-4"><input type="checkbox" checked={sync} onChange={(e) => setSync(e.target.checked)} />Also ask WhatsApp for older messages (up to 50 per conversation)</label>
                {sync && <p className="subtext text-xs mt-2">Use Check for updates or Try again above to request them. Older history is not always available.</p>}
                <details className="history-details mt-4">
                  <summary>Technical details</summary>
                  <p className="subtext text-xs mt-2">{coverage.indexed_documents.toLocaleString()} searchable text sections. {coverage.media_not_cached.toLocaleString()} attachments are not saved locally. {coverage.failed_transcripts} failed or unresolved transcriptions.</p>
                  <ul className="history-warnings">{coverage.warnings.map((w) => <li key={w}>{w}</li>)}</ul>
                  {indexJob?.error && <p className="error-note">{indexJob.error}</p>}
                </details>
              </details>
              {preparing && <Button variant="ghost" size="sm" className="mt-3" onClick={async () => { await perform("cancel", { job_id: indexJob.job_id }); await refresh(); }}>Pause preparation</Button>}
            </section>
          )}
          {!!status.selection.chat_ids.length && (
            <section className="history-section">
              <h2>Find something in your messages</h2>
              <p className="subtext text-sm mt-2">Searching {countLabel(activeScope.length, "conversation")}{session ? " from this question session" : ""}.</p>
              <details className="history-details mt-3">
                <summary>View conversations or continue a question</summary>
              <label className="history-field mt-4">
                Continue a previous question
                <select
                  value={session}
                  onChange={(e) => {
                    setSession(e.target.value);
                    setAnswer(null);
                    setHits([]);
      setSearched(false);
                    setReview(null);
                    const v = status.sessions.find(
                      (s) => s.session_id === e.target.value,
                    );
                    setConsent(v?.external_consent || false);
                  }}
                >
                  <option value="">
                    Start a new question
                  </option>
                  {status.sessions.map((s, i) => (
                    <option value={s.session_id} key={s.session_id}>
                      Question session {i + 1} · {countLabel(s.chat_ids.length, "conversation")} ·{" "}
                      {s.external_consent
                        ? "AI answers allowed"
                        : "search only"}
                    </option>
                  ))}
                </select>
              </label>
              <p className="subtext text-xs mt-2">
                Follow-up questions use the same account and conversations. Searching:{" "}
                {activeScope
                  .map(chatName)
                  .join(", ")}
                .
              </p>
              </details>
              <label className="history-field mt-4">
                What would you like to do?
                <select
                  value={queryMode}
                  onChange={(e) => setQueryMode(e.target.value)}
                >
                  <option value="search">
                    Find messages
                  </option>
                  <option value="review">
                    Read messages in date order
                  </option>
                  <option
                    value="question"
                    disabled={!status.options.generation_enabled}
                  >
                    {status.options.generation_enabled ? "Ask AI a question" : "Ask AI a question (not enabled)"}
                  </option>
                  <option
                    value="actions"
                    disabled={!status.options.generation_enabled}
                  >
                    {status.options.generation_enabled ? "Ask AI to review unfinished tasks" : "Review unfinished tasks with AI (not enabled)"}
                  </option>
                </select>
              </label>
              <p className="subtext text-xs mt-2">
                {["question", "actions"].includes(queryMode)
                  ? status.options.external_generation_disclosure
                  : "Search runs on this server. Messages are not sent to an AI answer provider."}
                {!status.options.generation_enabled && " AI-written answers are not enabled by the server owner."}
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
      setSearched(false);
                  setReview(null);
                  setWarnings([]);
                  setNotice("");
                  if (queryMode === "search" && after && before && new Date(after) > new Date(before)) {
                    setError("Choose an end date after the start date.");
                    return;
                  }
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
                      setSearched(true);
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
                {queryMode !== "review" && <label className="history-field">
                  {queryMode === "search" ? "What are you looking for?" : "Your question"}
                  <Input
                    value={question}
                    onChange={(e) => { setQuestion(e.target.value); setSearched(false); }}
                    maxLength={2000}
                    placeholder="What did we decide about the launch?"
                    required
                  />
                </label>}
                {queryMode === "search" && (
                  <details className="history-details mt-3"><summary>Filter by sender or date</summary><div className="history-filters mt-3">
                    <label className="history-field">
                      Sender number or ID (optional)
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
                  </div></details>
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
                        ? "Find messages"
                        : queryMode === "review"
                          ? "Read messages"
                          : "Ask AI"}
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
                      Delete question session
                    </Button>
                  )}
                </div>
              </form>
            </section>
          )}
          {visibleJobs.filter((j) => j.kind !== "index").map((j) => (
            <div className="history-job" key={j.job_id}>
              <div><span>{j.kind === "actions" ? "Task review" : "AI answer"}: {j.status.replaceAll("_", " ")}</span>
                <small className="subtext">{countLabel(j.processed, "message")} reviewed</small>
                {j.error && <p className="error-note">{j.error}</p>}</div>
              {["queued", "running"].includes(j.status) && <Button variant="ghost" size="sm" onClick={async () => { await perform("cancel", { job_id: j.job_id }); await refresh(); }}>Cancel</Button>}
              {j.status === "awaiting_authorization" && <Button variant="outline" size="sm" onClick={async () => { await perform("resume", { job_id: j.job_id }); await refresh(); }}>Continue</Button>}
              {j.status === "completed" && j.result && <Button variant="outline" size="sm" onClick={() => { setAnswer(j.result || null); setSession(j.session_id || ""); setConsent(status.sessions.find((x) => x.session_id === j.session_id)?.external_consent || false); }}>Read answer</Button>}
            </div>
          ))}
          {warnings.length > 0 && (
            <details className="history-details mt-4"><summary>Search coverage notes</summary>
            <ul className="history-warnings">
              {warnings.map((w) => (
                <li key={w}>{w}</li>
              ))}
            </ul></details>
          )}
          {searched && queryMode === "search" && <p role="status" className="subtext mt-5">{hits.length ? `${countLabel(hits.length, "matching message")} shown` : "No matching messages found. Try different words or remove a date or sender filter."}</p>}
          {hits.map((h) => (
            <article className="history-evidence" key={h.source.source_id}>
              <p className="subtext text-xs">
                {chatName(h.source.chat_jid)} · {h.source.sender} · {formatHistoryDate(h.source.timestamp)}
              </p>
              <p className="whitespace-pre-wrap mt-2">{h.source.text}</p>
              {cite(h.source.source_id)}
            </article>
          ))}
          {review && (
            <section className="history-section">
              <h2>Messages in date order</h2>
              <p className="subtext mt-2">
                {review.complete_for_cached_snapshot
                  ? "Reached the end of this cached snapshot. Missing history and transcripts can still contain actions."
                  : "More cached messages remain. Continue reviewing before claiming all pending actions."}
              </p>
              {review.sources.map((s) => (
                <article key={s.source_id} className="history-evidence">
                  <p className="subtext text-xs">
                    {formatHistoryDate(s.timestamp)} · {s.sender} · {s.kind === "transcript" ? "Voice note transcript" : "Message"}
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
              <h2>Answer and supporting messages</h2>
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
            <DialogTitle>Original messages</DialogTitle>
            <DialogDescription>
              Read the original text and nearby messages from your chosen conversations.
            </DialogDescription>
          </DialogHeader>
          <div className="history-source-scroll">
            {inspected?.map((s) => (
              <article className="history-evidence" key={s.source_id}>
                <p className="subtext text-xs">
                  {formatHistoryDate(s.timestamp)} · {s.sender} · {s.kind === "transcript" ? "Voice note transcript" : "Message"}
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
                {historyTranslationText(s.translation) && (
                  <details className="history-details mt-2">
                    <summary>Read translation</summary>
                    <p className="whitespace-pre-wrap mt-2">{historyTranslationText(s.translation)}</p>
                  </details>
                )}
                <details className="history-details mt-3"><summary>Message reference</summary>
                <p className="subtext text-xs mt-2">
                  Chat: {s.chat_jid}
                  <br />
                  Message: {s.message_id}
                  {s.reply_id && (
                    <>
                      <br />
                      Reply to: {s.reply_id}
                    </>
                  )}
                </p></details>
              </article>
            ))}
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
