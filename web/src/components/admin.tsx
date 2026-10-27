"use client";

import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type FormEvent,
} from "react";
import {
  ArrowLeft,
  ArrowRight,
  Check,
  ChevronRight,
  Copy,
  Eye,
  EyeOff,
  KeyRound,
  Loader2,
  LogOut,
  Monitor,
  Plus,
  RefreshCw,
  ShieldCheck,
  Smartphone,
  Terminal,
  Unplug,
  X,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

type Account = {
  account_id: string;
  label: string;
  state: string;
  phone?: string;
  error?: string;
  message_cache_full?: boolean;
};
type Session = {
  csrf: string;
  accounts: Account[];
  endpoint: string;
  token: string;
  version: string;
  oauth_enabled?: boolean;
  oauth_only?: boolean;
};
type Pairing = {
  account: Account;
  state: string;
  qr: boolean;
  pair_code: string;
  t: number;
};
type View = "accounts" | "connect";
const labels: Record<string, string> = {
  unpaired: "Not linked",
  pairing: "Waiting for phone",
  connecting: "Connecting",
  connected: "Connected",
  disconnected: "Reconnecting",
  logged_out: "Logged out",
  error: "Needs attention",
  disabled: "Disabled",
};
const loginCommand = "whatsapp-mcp login";

async function request<T>(
  path: string,
  data?: Record<string, string>,
  csrf?: string,
): Promise<T> {
  const response = await fetch(path, {
    method: data ? "POST" : "GET",
    credentials: "same-origin",
    cache: "no-store",
    signal: AbortSignal.timeout(data ? 30000 : 8000),
    headers: {
      Accept: "application/json",
      ...(data ? { "Content-Type": "application/x-www-form-urlencoded" } : {}),
      ...(csrf ? { "X-CSRF-Token": csrf } : {}),
    },
    body: data ? new URLSearchParams(data) : undefined,
  });
  const result = await response.json().catch(() => ({
    error: "The server could not complete this request. Try again.",
  }));
  if (!response.ok) {
    const error = new Error(result.error || "Request failed.") as Error & {
      status: number;
    };
    error.status = response.status;
    throw error;
  }
  return result;
}

function Brand() {
  return (
    <div className="brand">
      <span className="brandmark" aria-hidden="true">
        <img
          src="/admin/brand/whatsapp-mcp-logo-transparent.png"
          alt=""
          width={40}
          height={40}
        />
      </span>
      <span>WhatsApp MCP</span>
    </div>
  );
}
function Status({ state }: { state: string }) {
  return (
    <span
      className={`status ${state === "connected" ? "good" : ["pairing", "connecting", "disconnected"].includes(state) ? "waiting" : state === "error" ? "bad" : ""}`}
    >
      <span className="status-dot" />
      {labels[state] || state}
    </span>
  );
}
function Spinner() {
  return <Loader2 className="animate-spin" size={18} aria-hidden="true" />;
}
function ErrorNote({
  error,
  dismiss,
}: {
  error: string;
  dismiss?: () => void;
}) {
  return (
    <div
      className="error-note flex items-start justify-between gap-3"
      role="alert"
    >
      <span>{error}</span>
      {dismiss && (
        <button onClick={dismiss} aria-label="Dismiss error">
          <X size={16} />
        </button>
      )}
    </div>
  );
}

async function copy(text: string) {
  if (navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text);
      return;
    } catch {
      /* private HTTP fallback */
    }
  }
  const field = document.createElement("textarea");
  field.value = text;
  field.className = "fixed -left-[9999px] top-0";
  document.body.appendChild(field);
  field.select();
  const ok = document.execCommand("copy");
  field.remove();
  if (!ok) throw new Error("Select the text and copy it manually.");
}
function CopyButton({
  text,
  label = "Copy",
  iconOnly = false,
}: {
  text: string;
  label?: string;
  iconOnly?: boolean;
}) {
  const [state, setState] = useState("");
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);
  return (
    <span className="inline-flex items-center gap-2">
      <Button
        variant="ghost"
        size={iconOnly ? "icon-sm" : "sm"}
        aria-label={label}
        onClick={async () => {
          try {
            await copy(text);
            setState("Copied");
            clearTimeout(timer.current);
            timer.current = setTimeout(() => setState(""), 2200);
          } catch (e) {
            setState((e as Error).message);
          }
        }}
      >
        {state === "Copied" ? <Check size={15} /> : <Copy size={15} />}
        {!iconOnly && (state === "Copied" ? "Copied" : label)}
      </Button>
      <span className="sr-only" role="status">
        {state}
      </span>
      {state && state !== "Copied" && (
        <span className="text-xs subtext" role="status">
          {state}
        </span>
      )}
    </span>
  );
}
function CodeBlock({ text }: { text: string }) {
  return (
    <div className="command mt-3">
      <div className="absolute right-2 top-2">
        <CopyButton text={text} iconOnly />
      </div>
      <pre className="pr-7">{text}</pre>
    </div>
  );
}

function Login({ onLogin }: { onLogin: () => Promise<void> }) {
  const [passwordMode, setPasswordMode] = useState(false);
  const [code, setCode] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    const query = new URLSearchParams(location.search);
    const value = query
      .get("code")
      ?.replace(/[^a-z0-9]/gi, "")
      .toUpperCase();
    if (value?.length === 8) {
      setCode(value.slice(0, 4) + "-" + value.slice(4));
      const clean = new URL(location.href);
      clean.searchParams.delete("code");
      history.replaceState({}, "", clean.pathname + clean.search);
    }
  }, []);
  async function signIn(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await request("/admin/login", passwordMode ? { password } : { code });
      setPassword("");
      setCode("");
      await onLogin();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="login-wrap">
      <header className="login-brand">
        <Brand />
      </header>
      <main className="login-panel">
        <div className="mb-7 text-primary">
          <KeyRound size={28} strokeWidth={1.5} />
        </div>
        <h1>Sign in to your server</h1>
        <p className="subtext mt-3 mb-8">
          {passwordMode
            ? "Use the admin password from your server configuration."
            : "Enter a one-time code to manage your WhatsApp accounts."}
        </p>
        <form onSubmit={signIn} className="space-y-5">
          {error && <ErrorNote error={error} />}
          <div>
            <label className="mb-2 block font-medium" htmlFor="credential">
              {passwordMode ? "Admin password" : "Sign-in code"}
            </label>
            {passwordMode ? (
              <Input
                id="credential"
                key="password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="current-password"
                required
                autoFocus
              />
            ) : (
              <Input
                id="credential"
                key="code"
                className="code-input"
                value={code}
                onChange={(e) => {
                  const v = e.target.value
                    .replace(/[^a-z0-9]/gi, "")
                    .toUpperCase()
                    .slice(0, 8);
                  setCode(v.length > 4 ? v.slice(0, 4) + "-" + v.slice(4) : v);
                }}
                placeholder="XXXX-XXXX"
                maxLength={9}
                autoComplete="one-time-code"
                autoCapitalize="characters"
                spellCheck={false}
                required
                autoFocus
              />
            )}
          </div>
          <Button
            type="submit"
            className="w-full h-11"
            disabled={
              busy || (!passwordMode && code.replace("-", "").length !== 8)
            }
          >
            {busy ? <Spinner /> : null}
            {busy ? "Signing in…" : "Sign in"}
            {!busy && <ArrowRight size={16} />}
          </Button>
        </form>
        <Button
          variant="link"
          className="mt-3 w-full text-muted-foreground"
          onClick={() => {
            setPasswordMode(!passwordMode);
            setError("");
          }}
        >
          {passwordMode ? "Use a one-time code" : "Use admin password instead"}
        </Button>
        {!passwordMode && (
          <div className="mt-10 border-t pt-6">
            <div className="flex items-center gap-2 text-sm font-medium">
              <Terminal size={16} />
              Get a sign-in code
            </div>
            <p className="subtext mt-2 text-xs">
              Run this from any folder on the Docker host. The code works
              once and expires in 10 minutes.
            </p>
            <CodeBlock text={loginCommand} />
          </div>
        )}
      </main>
    </div>
  );
}

export default function Admin() {
  const [session, setSession] = useState<Session | null>(null);
  const [loading, setLoading] = useState(true);
  const [view, setView] = useState<View>("accounts");
  const [online, setOnline] = useState(true);
  const [selected, setSelected] = useState<string | null>(null);
  const [authorizationId, setAuthorizationId] = useState<string | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const sessionRef = useRef(session);
  sessionRef.current = session;
  const loadSession = useCallback(async () => {
    try {
      const value = await request<Session>("/admin/api/session");
      setSession(value);
      setOnline(true);
    } catch (e) {
      if ((e as Error & { status: number }).status === 401) {
        setSession(null);
      } else {
        setOnline(false);
        throw e;
      }
    }
  }, []);
  useEffect(() => {
    setAuthorizationId(new URLSearchParams(location.search).get("authorize"));
    loadSession()
      .catch((e) => setError(e.message))
      .finally(() => setLoading(false));
    if (location.hash === "#connect") setView("connect");
    const match = location.pathname.match(/^\/admin\/accounts\/([^/]+)$/);
    if (match) setSelected(match[1]);
    const timer = setInterval(() => {
      if (sessionRef.current) loadSession().catch(() => {});
    }, 5000);
    return () => clearInterval(timer);
  }, [loadSession]);
  function navigate(next: View, account: string | null = null) {
    setView(next);
    setSelected(account);
    setError("");
    history.replaceState(
      {},
      "",
      account
        ? "/admin/accounts/" + account
        : "/admin" + (next === "connect" ? "#connect" : ""),
    );
  }
  async function addAccount() {
    if (!session || busy) return;
    setBusy(true);
    setError("");
    try {
      const account = await request<Account>(
        "/admin/api/accounts",
        {},
        session.csrf,
      );
      await loadSession();
      navigate("accounts", account.account_id);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  if (loading)
    return (
      <main className="grid min-h-dvh place-items-center text-muted-foreground">
        <div className="flex gap-3 items-center" role="status">
          <Spinner />
          Connecting to your server
        </div>
      </main>
    );
  if (!session)
    return (
      <>
        <Login
          onLogin={async () => {
            await loadSession();
            history.replaceState(
              {},
              "",
              authorizationId
                ? "/admin?authorize=" + encodeURIComponent(authorizationId)
                : "/admin",
            );
          }}
        />
        {error && (
          <div className="fixed bottom-5 left-5 right-5">
            <ErrorNote error={error} dismiss={() => setError("")} />
          </div>
        )}
      </>
    );
  const account = session.accounts.find((a) => a.account_id === selected);
  return (
    <div className="shell">
      <aside className="rail">
        <Brand />
        <nav aria-label="Main navigation">
          <button
            className={`navitem ${view === "accounts" ? "active" : ""}`}
            onClick={() => navigate("accounts")}
          >
            <Smartphone size={17} />
            Accounts
          </button>
          <button
            className={`navitem ${view === "connect" ? "active" : ""}`}
            onClick={() => navigate("connect")}
          >
            <Monitor size={17} />
            Connect client
          </button>
        </nav>
        <div className="railfooter">
          <div className="flex items-center gap-2 mb-2">
            <span
              className={`status-dot ${online ? "text-primary" : "text-destructive"}`}
            />
            {online ? "Server connected" : "Reconnecting"}
          </div>
          <span>{session.version}</span>
        </div>
      </aside>
      <div className="min-w-0">
        <header className="topbar">
          <span className="subtext text-xs flex items-center gap-2">
            <ShieldCheck size={15} />
            Private workspace
          </span>
          <Button
            variant="ghost"
            size="sm"
            onClick={async () => {
              try {
                await request("/admin/logout", {}, session.csrf);
                setSession(null);
                setSelected(null);
                history.replaceState({}, "", "/admin/login");
              } catch (e) {
                setError((e as Error).message);
              }
            }}
          >
            <LogOut size={15} />
            <span>Sign out</span>
          </Button>
        </header>
        <main className="workspace">
          {!online && (
            <div className="mb-6 error-note" role="status">
              Connection interrupted. Reconnecting automatically.
            </div>
          )}
          {error && (
            <div className="mb-6">
              <ErrorNote error={error} dismiss={() => setError("")} />
            </div>
          )}
          {authorizationId ? (
            <Consent id={authorizationId} session={session} />
          ) : view === "connect" ? (
            <Connect session={session} />
          ) : account ? (
            <AccountDetail
              key={account.account_id}
              account={account}
              csrf={session.csrf}
              refresh={loadSession}
              back={() => navigate("accounts")}
              expired={() => {
                setSession(null);
              }}
            />
          ) : (
            <>
              <div className="flex items-start justify-between gap-5 mb-8">
                <div>
                  <h1>Accounts</h1>
                  <p className="subtext mt-2">
                    Your linked WhatsApp accounts, in one place.
                  </p>
                </div>
                {session.accounts.length > 0 && (
                  <Button onClick={addAccount} disabled={busy}>
                    {busy ? <Spinner /> : <Plus size={16} />}Add account
                  </Button>
                )}
              </div>
              <section className="surface" aria-label="WhatsApp accounts">
                {session.accounts.length === 0 ? (
                  <div className="empty">
                    <div className="empty-icon">
                      <Smartphone size={27} strokeWidth={1.5} />
                    </div>
                    <h2>Add your first account</h2>
                    <p className="subtext max-w-72 mt-2 mb-6">
                      Link WhatsApp with a QR code. Your phone and existing
                      chats stay as they are.
                    </p>
                    <Button onClick={addAccount} disabled={busy}>
                      {busy ? <Spinner /> : <Plus size={16} />}
                      {busy ? "Adding account…" : "Add account"}
                    </Button>
                  </div>
                ) : (
                  session.accounts.map((a) => (
                    <button
                      className="account-row"
                      key={a.account_id}
                      onClick={() => navigate("accounts", a.account_id)}
                    >
                      <span className="account-icon">
                        <Smartphone size={22} strokeWidth={1.5} />
                      </span>
                      <span className="flex-1 min-w-0">
                        <span className="block font-medium truncate">
                          {a.phone ? "+" + a.phone : a.label}
                        </span>
                        <span className="block subtext text-xs mt-1">
                          {a.error
                            ? "Open account to resolve a connection issue"
                            : a.phone
                              ? a.label === "WhatsApp"
                                ? "WhatsApp"
                                : a.label
                              : "Finish linking this account"}
                        </span>
                      </span>
                      <Status state={a.state} />
                      <ChevronRight
                        size={16}
                        className="text-muted-foreground"
                      />
                    </button>
                  ))
                )}
              </section>
              <p className="subtext text-xs mt-5 flex items-start gap-2">
                <ShieldCheck size={14} className="mt-0.5 shrink-0" />
                Each account is stored separately. Link only accounts you own.
              </p>
            </>
          )}
        </main>
      </div>
    </div>
  );
}

function AccountDetail({
  account,
  csrf,
  refresh,
  back,
  expired,
}: {
  account: Account;
  csrf: string;
  refresh: () => Promise<void>;
  back: () => void;
  expired: () => void;
}) {
  const [pair, setPair] = useState<Pairing | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [phoneMode, setPhoneMode] = useState(false);
  const [phone, setPhone] = useState("");
  const [confirm, setConfirm] = useState<"unlink" | "reset" | null>(null);
  const alive = useRef(true);
  const id = account.account_id;
  const poll = useCallback(async () => {
    try {
      const result = await request<Pairing>("/admin/accounts/" + id + "/state");
      if (alive.current) {
        setPair(result);
        setError((current) =>
          current.startsWith("Connection interrupted.") ? "" : current,
        );
      }
    } catch (e) {
      if ((e as Error & { status: number }).status === 401) expired();
      else if (alive.current)
        setError("Connection interrupted. The page will retry automatically.");
    }
  }, [id, expired]);
  // The account data is polled independently so QR arrival is visible even
  // when WhatsApp keeps the state at "pairing".
  useEffect(() => {
    alive.current = true;
    poll();
    const timer = setInterval(poll, 3000);
    return () => {
      alive.current = false;
      clearInterval(timer);
    };
  }, [id]); // eslint-disable-line react-hooks/exhaustive-deps
  const status = pair?.account ?? account;
  const canPair =
    ["unpaired", "logged_out"].includes(status.state) ||
    (status.state === "error" && !status.phone);
  const canUnlink =
    ["connected", "connecting", "disconnected"].includes(status.state) ||
    (status.state === "error" && !!status.phone);
  const pairedWaiting =
    !!status.phone &&
    ["connecting", "disconnected", "error"].includes(status.state);
  async function action(kind: "pair" | "unlink" | "reset") {
    setBusy(true);
    setError("");
    try {
      await request(
        "/admin/accounts/" + id + "/" + kind,
        kind === "pair"
          ? { phone: phoneMode ? phone : "" }
          : kind === "reset"
            ? { confirm: id }
            : {},
        csrf,
      );
      await poll();
      await refresh();
      setConfirm(null);
    } catch (e) {
      setConfirm(null);
      setError((e as Error).message);
    } finally {
      if (alive.current) setBusy(false);
    }
  }
  return (
    <>
      <Button
        variant="ghost"
        size="sm"
        className="-ml-3 mb-6 text-muted-foreground"
        onClick={back}
      >
        <ArrowLeft size={15} />
        All accounts
      </Button>
      <div className="flex items-start justify-between gap-4 mb-8">
        <div>
          <h1>{status.phone ? "+" + status.phone : "Link WhatsApp"}</h1>
          <p className="subtext mt-2">
            {status.phone
              ? "Manage this account’s connection."
              : "Connect your phone to this workspace."}
          </p>
        </div>
        <Status state={status.state} />
      </div>
      {error && (
        <div className="mb-6">
          <ErrorNote error={error} dismiss={() => setError("")} />
        </div>
      )}
      {status.error && (
        <div className="mb-6">
          <ErrorNote error={status.error} />
        </div>
      )}
      {status.state === "connected" ? (
        <section className="surface p-7">
          <div className="flex items-center gap-3 text-primary mb-3">
            <Check size={20} />
            <h2>Account connected</h2>
          </div>
          <p className="subtext">
            You can now read and send messages from your MCP client.
          </p>
          {status.message_cache_full && (
            <p className="mt-4 text-destructive">
              Message storage is full. New messages are not being saved. Free
              space or increase the cache budget.
            </p>
          )}
        </section>
      ) : pairedWaiting ? (
        <section className="surface p-7" aria-live="polite">
          <div className="flex items-center gap-3 mb-3">
            <RefreshCw size={20} className="text-primary animate-spin" />
            <h2>
              {status.state === "connecting"
                ? "Finishing connection"
                : "Reconnecting to WhatsApp"}
            </h2>
          </div>
          <p className="subtext">
            Your phone is already linked. The connection will update
            automatically; you do not need to scan another QR code.
          </p>
          <p className="subtext text-xs mt-4">
            Saved chats remain readable while the account reconnects. If you
            want to remove this link before it reconnects, use WhatsApp’s Linked
            devices screen on your phone.
          </p>
        </section>
      ) : (
        <section className="surface">
          <div className="link-grid">
            <div>
              <h2>
                {phoneMode
                  ? "Link with your phone number"
                  : "Scan with WhatsApp"}
              </h2>
              <div className="step">
                <span className="step-count">1</span>
                <span>Open WhatsApp on your phone.</span>
              </div>
              <div className="step">
                <span className="step-count">2</span>
                <span>
                  Go to{" "}
                  <strong className="font-medium">
                    Settings → Linked devices
                  </strong>
                  .
                </span>
              </div>
              <div className="step">
                <span className="step-count">3</span>
                <span>
                  Tap <strong className="font-medium">Link a device</strong>
                  {phoneMode
                    ? ", choose Link with phone number instead, and enter the code."
                    : " and scan the QR code."}
                </span>
              </div>
              {canPair && !phoneMode && (
                <Button
                  className="mt-7"
                  disabled={busy}
                  onClick={() => action("pair")}
                >
                  {busy ? <Spinner /> : <RefreshCw size={15} />}
                  {busy ? "Getting QR code…" : "Show QR code"}
                </Button>
              )}
              {phoneMode && canPair && (
                <form
                  className="mt-6 space-y-3"
                  onSubmit={(e) => {
                    e.preventDefault();
                    action("pair");
                  }}
                >
                  <label className="block text-xs font-medium" htmlFor="phone">
                    Phone number, including country code
                  </label>
                  <Input
                    id="phone"
                    type="tel"
                    inputMode="numeric"
                    value={phone}
                    onChange={(e) => setPhone(e.target.value)}
                    pattern="[0-9]{6,15}"
                    placeholder="919876543210"
                    required
                  />
                  <Button type="submit" disabled={busy}>
                    {busy && <Spinner />}Get pairing code
                  </Button>
                </form>
              )}
              {canPair && (
                <Button
                  variant="link"
                  className="p-0 block mt-4 h-auto text-xs text-muted-foreground"
                  onClick={() => setPhoneMode(!phoneMode)}
                >
                  {phoneMode
                    ? "Use a QR code instead"
                    : "Link with phone number instead"}
                </Button>
              )}
            </div>
            <div className="qr-side">
              <div className="qr-box">
                {pair?.pair_code ? (
                  <div className="text-center">
                    <span className="text-xs">Enter on your phone</span>
                    <p className="text-2xl font-mono tracking-wider mt-4">
                      {pair.pair_code}
                    </p>
                  </div>
                ) : pair?.qr ? (
                  <img
                    src={"/admin/accounts/" + id + "/qr.png?t=" + pair.t}
                    width={232}
                    height={232}
                    alt="Scan this QR code with WhatsApp to link your account"
                  />
                ) : (
                  <div className="flex flex-col items-center gap-4 text-center">
                    <Smartphone size={38} strokeWidth={1.2} />
                    {busy ||
                    ["pairing", "connecting"].includes(status.state) ? (
                      <span className="flex gap-2 items-center text-xs">
                        <Spinner />
                        Waiting for WhatsApp
                      </span>
                    ) : (
                      <span className="text-xs">
                        Your QR code will appear here
                      </span>
                    )}
                  </div>
                )}
              </div>
              <p className="subtext text-xs text-center mt-4">
                {status.state === "pairing"
                  ? "Waiting for your phone. Codes expire in 3 minutes."
                  : status.state === "connecting"
                    ? "Connecting to WhatsApp…"
                    : "Your phone is needed only to link the account."}
              </p>
            </div>
          </div>
        </section>
      )}
      <p className="subtext mt-5 text-xs">
        The connection updates automatically. You can leave this page after
        linking.
      </p>
      {(canUnlink || ["logged_out", "error"].includes(status.state)) && (
        <section className="mt-10 border-t pt-6">
          <h2 className="text-base">Account settings</h2>
          <p className="subtext text-xs mt-2 mb-4">
            Disconnecting affects only this account. Saved messages remain
            available.
          </p>
          {canUnlink ? (
            <Button
              variant="outline"
              disabled={busy || status.state !== "connected"}
              onClick={() => setConfirm("unlink")}
            >
              <Unplug size={15} />
              Disconnect account
            </Button>
          ) : (
            <Button variant="outline" onClick={() => setConfirm("reset")}>
              Reset for another phone
            </Button>
          )}
          {canUnlink && status.state !== "connected" && (
            <p className="subtext text-xs mt-3">
              Disconnect becomes available when WhatsApp reconnects. You can
              remove the device from your phone at any time.
            </p>
          )}
        </section>
      )}
      <Dialog
        open={confirm !== null}
        onOpenChange={(open) => {
          if (!open && !busy) setConfirm(null);
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {confirm === "reset"
                ? "Reset this account?"
                : "Disconnect this account?"}
            </DialogTitle>
            <DialogDescription>
              {confirm === "reset"
                ? "The account’s existing data will be archived. You can then link a different phone."
                : "This removes the linked device from WhatsApp. Your saved messages stay available, and other accounts keep working."}
            </DialogDescription>
          </DialogHeader>
          {confirm === "unlink" && status.state !== "connected" && (
            <p className="subtext text-sm" role="status">
              WhatsApp is reconnecting. Wait for it to connect, or cancel and
              remove this device from your phone’s Linked devices screen. Your
              saved link is unchanged.
            </p>
          )}
          <DialogFooter>
            <Button
              variant="outline"
              disabled={busy}
              onClick={() => setConfirm(null)}
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              disabled={
                busy || (confirm === "unlink" && status.state !== "connected")
              }
              onClick={() => confirm && action(confirm)}
            >
              {busy && <Spinner />}
              {confirm === "reset" ? "Archive and reset" : "Disconnect"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

function LegacyConnect({ session }: { session: Session }) {
  const [show, setShow] = useState(false);
  const [client, setClient] = useState("Codex");
  const configs: Record<string, string> = {
    Codex: `[mcp_servers.whatsapp]\nurl = "${session.endpoint}"\nbearer_token_env_var = "WHATSAPP_MCP_TOKEN"`,
    "Claude Code": `claude mcp add --transport http whatsapp ${session.endpoint} \\\n  --header "Authorization: Bearer $WHATSAPP_MCP_TOKEN"`,
    JSON: JSON.stringify(
      {
        mcpServers: {
          whatsapp: {
            type: "http",
            url: session.endpoint,
            headers: { Authorization: "Bearer ${WHATSAPP_MCP_TOKEN}" },
          },
        },
      },
      null,
      2,
    ),
  };
  return (
    <>
      <h1>Connect a client</h1>
      <p className="subtext mt-2 mb-8 max-w-lg">
        Use your WhatsApp accounts from an MCP client. Add the endpoint and
        token below to your client&apos;s settings.
      </p>
      <section className="surface p-6 sm:p-8 space-y-7">
        <div>
          <label htmlFor="endpoint" className="font-medium text-sm">
            MCP endpoint
          </label>
          <div className="mt-2 flex items-center gap-2">
            <Input
              id="endpoint"
              value={session.endpoint}
              readOnly
              className="font-mono text-xs"
            />
            <CopyButton
              text={session.endpoint}
              iconOnly
              label="Copy endpoint"
            />
          </div>
        </div>
        <div>
          <label htmlFor="token" className="font-medium text-sm">
            Access token
          </label>
          <div className="mt-2 flex items-center gap-2">
            <Input
              id="token"
              type={show ? "text" : "password"}
              value={session.token}
              readOnly
              className="font-mono text-xs"
            />
            <Button
              variant="ghost"
              size="icon-sm"
              onClick={() => setShow(!show)}
              aria-label={show ? "Hide token" : "Show token"}
              aria-pressed={show}
            >
              {show ? <EyeOff size={15} /> : <Eye size={15} />}
            </Button>
            <CopyButton text={session.token} iconOnly label="Copy token" />
          </div>
          <p className="subtext text-xs mt-3">
            Keep this private. The token allows reading and sending from all
            linked accounts.
          </p>
        </div>
      </section>
      <section className="mt-10">
        <h2>Client configuration</h2>
        <p className="subtext mt-2 mb-5">
          Set <code className="text-xs">WHATSAPP_MCP_TOKEN</code> in the
          environment where your client runs, then add this configuration.
        </p>
        <div
          className="flex gap-1 border-b"
          role="tablist"
          aria-label="Client configuration"
        >
          {Object.keys(configs).map((name) => (
            <button
              key={name}
              id={"tab-" + name.replace(" ", "-")}
              role="tab"
              aria-selected={client === name}
              aria-controls="client-config"
              tabIndex={client === name ? 0 : -1}
              onKeyDown={(e) => {
                const names = Object.keys(configs);
                let index = names.indexOf(client);
                if (e.key === "ArrowRight") index = (index + 1) % names.length;
                else if (e.key === "ArrowLeft")
                  index = (index + names.length - 1) % names.length;
                else return;
                e.preventDefault();
                setClient(names[index]);
                document
                  .getElementById("tab-" + names[index].replace(" ", "-"))
                  ?.focus();
              }}
              onClick={() => setClient(name)}
              className={`px-4 py-3 text-sm border-b-2 ${client === name ? "border-primary font-medium" : "border-transparent text-muted-foreground"}`}
            >
              {name}
            </button>
          ))}
        </div>
        <div
          id="client-config"
          role="tabpanel"
          aria-labelledby={"tab-" + client.replace(" ", "-")}
        >
          <CodeBlock text={configs[client]} />
        </div>
        <p className="subtext text-xs mt-4">
          The client must be able to reach your server&apos;s private address.
        </p>
      </section>
    </>
  );
}

type Connection = {
  id: string;
  client_name: string;
  created_at: number;
  expires_at: number;
};
function Connect({ session }: { session: Session }) {
  const [connections, setConnections] = useState<Connection[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const load = useCallback(async () => {
    if (session.oauth_enabled) {
      try {
        setConnections(
          await request<Connection[]>("/admin/api/oauth/connections"),
        );
      } catch (e) {
        setError((e as Error).message);
      }
    }
  }, [session.oauth_enabled]);
  useEffect(() => {
    load();
  }, [load]);
  if (!session.oauth_enabled) return <LegacyConnect session={session} />;
  const codex = `codex mcp add whatsapp --url ${session.endpoint}\ncodex mcp login whatsapp`;
  return (
    <>
      <h1>Connect a client</h1>
      <p className="subtext mt-2 mb-8 max-w-lg">
        Sign in through your browser and approve the connection. Your client
        handles access automatically.
      </p>
      <section className="surface p-6 sm:p-8">
        <label htmlFor="oauth-endpoint" className="font-medium">
          MCP endpoint
        </label>
        <div className="mt-2 flex items-center gap-2">
          <Input
            id="oauth-endpoint"
            value={session.endpoint}
            readOnly
            className="font-mono text-xs"
          />
          <CopyButton text={session.endpoint} iconOnly label="Copy endpoint" />
        </div>
        <p className="subtext text-xs mt-3 flex gap-2 items-center">
          <ShieldCheck size={14} />
          OAuth with browser approval. No access token to copy.
        </p>
      </section>
      <section className="mt-9">
        <h2>Set up Codex</h2>
        <p className="subtext mt-2">
          Run these commands on the computer where you use Codex, then follow
          the browser sign-in.
        </p>
        <CodeBlock text={codex} />
        <p className="subtext text-xs mt-3">
          For another client with HTTP OAuth support, add the endpoint above and
          choose its sign-in action. The client must be able to reach this
          server.
        </p>
      </section>
      <section className="mt-10">
        <h2>Authorized clients</h2>
        <p className="subtext mt-2 mb-5">
          Revoke a connection to stop its access. Your WhatsApp accounts stay
          linked.
        </p>
        {error && <ErrorNote error={error} dismiss={() => setError("")} />}
        <div className="surface">
          {connections.length === 0 ? (
            <p className="p-6 subtext">
              No clients authorized yet. Start a login from your client to
              connect.
            </p>
          ) : (
            connections.map((c) => (
              <div
                key={c.id}
                className="flex items-center gap-4 justify-between p-5 border-b last:border-0"
              >
                <div className="min-w-0">
                  <p className="font-medium break-words">{c.client_name}</p>
                  <p className="subtext text-xs mt-1">
                    Approved{" "}
                    {new Date(c.created_at * 1000).toLocaleDateString()} ·
                    expires {new Date(c.expires_at * 1000).toLocaleDateString()}
                  </p>
                </div>
                <Button
                  variant="outline"
                  disabled={busy !== null}
                  onClick={async () => {
                    setBusy(c.id);
                    try {
                      await request(
                        "/admin/api/oauth/connections/" + c.id + "/revoke",
                        {},
                        session.csrf,
                      );
                      await load();
                    } catch (e) {
                      setError((e as Error).message);
                    } finally {
                      setBusy(null);
                    }
                  }}
                >
                  {busy === c.id ? <Spinner /> : null}Revoke
                </Button>
              </div>
            ))
          )}
        </div>
      </section>
      {!session.oauth_only && (
        <details className="mt-10 border-t pt-5">
          <summary className="cursor-pointer subtext">
            Advanced: static token setup
          </summary>
          <div className="mt-6">
            <LegacyConnect session={session} />
          </div>
        </details>
      )}
    </>
  );
}
function Consent({ id, session }: { id: string; session: Session }) {
  const [info, setInfo] = useState<{
    client_name: string;
    redirect_uri: string;
  } | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    request<{ client_name: string; redirect_uri: string }>(
      "/admin/api/oauth/requests/" + encodeURIComponent(id),
    )
      .then(setInfo)
      .catch((e) => setError(e.message));
  }, [id]);
  async function decide(decision: "approve" | "deny") {
    setBusy(true);
    setError("");
    try {
      const result = await request<{ redirect: string }>(
        "/admin/api/oauth/requests/" + encodeURIComponent(id),
        { decision },
        session.csrf,
      );
      location.assign(result.redirect);
    } catch (e) {
      setError((e as Error).message);
      setBusy(false);
    }
  }
  return (
    <section className="max-w-xl mx-auto">
      <div className="text-primary mb-6">
        <ShieldCheck size={30} />
      </div>
      <h1>Authorize a client</h1>
      <p className="subtext mt-3 mb-7">
        A client is requesting access to your WhatsApp workspace.
      </p>
      {error && <ErrorNote error={error} />}
      {info ? (
        <>
          <section className="surface p-6 mt-6">
            <h2 className="break-words">{info.client_name}</h2>
            <p className="subtext text-xs mt-2">
              Name supplied by the requesting client. Confirm that you started
              this login.
            </p>
            <ul className="space-y-3 mt-6 list-disc pl-5">
              <li>Read chats, contacts and messages.</li>
              <li>
                Send messages and files when requested through the client.
              </li>
              <li>
                Use configured transcription and image analysis on request.
              </li>
            </ul>
            <p className="subtext text-xs mt-5">
              Access applies to all linked accounts. Approval lasts up to 30
              days and can be revoked under Connect client.
            </p>
            <div className="border-t mt-6 pt-5">
              <label htmlFor="callback" className="text-xs font-medium">
                Return address
              </label>
              <Input
                id="callback"
                className="mt-2 font-mono text-xs"
                value={info.redirect_uri}
                readOnly
              />
            </div>
          </section>
          <div className="flex gap-3 mt-7">
            <Button disabled={busy} onClick={() => decide("approve")}>
              {busy && <Spinner />}Approve access
            </Button>
            <Button
              variant="outline"
              disabled={busy}
              onClick={() => decide("deny")}
            >
              Deny
            </Button>
          </div>
        </>
      ) : (
        !error && (
          <div className="flex items-center gap-3 mt-6 subtext" role="status">
            <Spinner />
            Loading authorization request
          </div>
        )
      )}
    </section>
  );
}
