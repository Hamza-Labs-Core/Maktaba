// Sign in, sign up, verify the email, and confirm it's you (re-auth).
import { type FormEvent, useEffect, useState } from "react";
import { ApiError, api, getPasskey } from "../api";
import { useShell } from "../App";

export function SignIn() {
  const s = useShell();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [needCode, setNeedCode] = useState(false);
  const [error, setError] = useState("");

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError("");
    try {
      await api("POST", "/v1/sessions", { email, password, ...(code ? { code } : {}) });
      await s.reload();
      s.go("/account");
    } catch (err) {
      if (err instanceof ApiError && err.code === "code_required") setNeedCode(true);
      setError(s.errorText(err));
    }
  };
  const passkey = async () => {
    setError("");
    try {
      const o = await api<{ challenge_id: string; publicKey: { challenge: string } }>("POST", "/v1/sessions/passkey/options");
      await api("POST", "/v1/sessions/passkey", { challenge_id: o.challenge_id, credential: await getPasskey(o.publicKey) });
      await s.reload();
      s.go("/account");
    } catch (err) {
      setError(s.errorText(err));
    }
  };
  return (
    <section>
      <h1>{s.t("signin.title")}</h1>
      <p className="note">{s.t("signin.note")}</p>
      <form onSubmit={submit}>
        <label>
          {s.t("signin.email")}
          <input type="email" autoComplete="username" value={email} onChange={(e) => setEmail(e.target.value)} required dir="ltr" />
        </label>
        <label>
          {s.t("signin.password")}
          <input
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
            dir="ltr"
          />
        </label>
        {needCode && (
          <label>
            {s.t("signin.code")}
            <input inputMode="numeric" autoComplete="one-time-code" value={code} onChange={(e) => setCode(e.target.value)} dir="ltr" />
          </label>
        )}
        {error && <p role="alert">{error}</p>}
        <button type="submit">{s.t("signin.submit")}</button>
      </form>
      <button type="button" onClick={passkey}>
        {s.t("signin.passkey")}
      </button>
    </section>
  );
}

export function SignUp() {
  const s = useShell();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [name, setName] = useState("");
  const [done, setDone] = useState(false);
  const [error, setError] = useState("");
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    try {
      await api("POST", "/v1/accounts", { email, password, display_name: name, locale: s.lang });
      setDone(true);
      await s.reload();
    } catch (err) {
      setError(s.errorText(err));
    }
  };
  if (done) return <p role="status">{s.t("signup.check")}</p>;
  return (
    <section>
      <h1>{s.t("signup.title")}</h1>
      <p className="note">{s.t("signin.note")}</p>
      <form onSubmit={submit}>
        <label>
          {s.t("signup.name")}
          <input value={name} onChange={(e) => setName(e.target.value)} maxLength={80} />
        </label>
        <label>
          {s.t("signin.email")}
          <input type="email" autoComplete="username" value={email} onChange={(e) => setEmail(e.target.value)} required dir="ltr" />
        </label>
        <label>
          {s.t("signin.password")}
          <input
            type="password"
            autoComplete="new-password"
            minLength={10}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
            dir="ltr"
          />
        </label>
        {error && <p role="alert">{error}</p>}
        <button type="submit">{s.t("signup.submit")}</button>
      </form>
    </section>
  );
}

export function Verify() {
  const s = useShell();
  const [state, setState] = useState<"working" | "done" | "failed">("working");
  useEffect(() => {
    // the token is in the fragment, never sent to a server in a URL
    const token = location.hash.slice(1);
    api("POST", "/v1/accounts/verify", { token })
      .then(() => setState("done"))
      .catch(() => setState("failed"));
  }, []);
  return (
    <p role="status">{state === "working" ? s.t("verify.title") : state === "done" ? s.t("verify.done") : s.t("error.code_expired")}</p>
  );
}

/** Runs action; on 401 reauth_required asks for a factor, then runs it again. */
export function useReauth() {
  const s = useShell();
  const [pending, setPending] = useState<null | (() => Promise<void>)>(null);
  const run = async (action: () => Promise<void>) => {
    try {
      await action();
    } catch (e) {
      if (e instanceof ApiError && e.code === "reauth_required") setPending(() => action);
      else throw e;
    }
  };
  const dialog = pending ? <Reauth onDone={async () => (setPending(null), await pending())} onCancel={() => setPending(null)} /> : null;
  void s;
  return { run, dialog };
}

function Reauth({ onDone, onCancel }: { onDone: () => Promise<void>; onCancel: () => void }) {
  const s = useShell();
  const [code, setCode] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const hasMFA = (s.me?.factors.length ?? 0) > 0;
  const confirm = async (e: FormEvent) => {
    e.preventDefault();
    try {
      await api("POST", "/v1/reauth", hasMFA ? { code } : { password });
      await onDone();
    } catch (err) {
      setError(s.errorText(err));
    }
  };
  const passkey = async () => {
    try {
      const o = await api<{ challenge_id: string; publicKey: { challenge: string } }>("POST", "/v1/reauth/passkey/options");
      await api("POST", "/v1/reauth", { challenge_id: o.challenge_id, credential: await getPasskey(o.publicKey) });
      await onDone();
    } catch (err) {
      setError(s.errorText(err));
    }
  };
  return (
    <div role="dialog" aria-modal="true" aria-labelledby="reauth-title" className="dialog">
      <h2 id="reauth-title">{s.t("reauth.title")}</h2>
      <form onSubmit={confirm}>
        {hasMFA ? (
          <label>
            {s.t("reauth.code")}
            <input value={code} onChange={(e) => setCode(e.target.value)} autoComplete="one-time-code" dir="ltr" />
          </label>
        ) : (
          <label>
            {s.t("reauth.password")}
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="current-password"
              dir="ltr"
            />
          </label>
        )}
        {error && <p role="alert">{error}</p>}
        <button type="submit">{s.t("reauth.confirm")}</button>
        {s.me?.factors.some((f) => f.kind === "passkey") && (
          <button type="button" onClick={passkey}>
            {s.t("reauth.passkey")}
          </button>
        )}
        <button type="button" onClick={onCancel}>
          ✕
        </button>
      </form>
    </div>
  );
}
