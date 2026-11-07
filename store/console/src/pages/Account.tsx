// The account page: email status, sign-in factors (authenticator app, passkeys, recovery
// codes), sessions with Sign out everywhere, and linked homes.
import { useEffect, useState } from "react";
import { api, createPasskey } from "../api";
import { useShell } from "../App";
import { useReauth } from "./Auth";

interface Home {
  id: string;
  name: string;
  server_version: string;
  platform: string;
  linked_at: string;
}

export function Account() {
  const s = useShell();
  const me = s.me!;
  const { run, dialog } = useReauth();
  const [totp, setTotp] = useState<{ setup_id: string; secret: string; otpauth: string } | null>(null);
  const [code, setCode] = useState("");
  const [recovery, setRecovery] = useState<string[] | null>(null);
  const [homes, setHomes] = useState<Home[]>([]);
  const [error, setError] = useState("");

  const loadHomes = () =>
    api<{ homes: Home[] }>("GET", "/v1/homes").then(
      (r) => setHomes(r.homes),
      () => setHomes([]),
    );
  useEffect(() => {
    void loadHomes();
  }, []);

  const act = (f: () => Promise<void>) => () => run(f).catch((e) => setError(s.errorText(e)));

  return (
    <section>
      <h1>{s.t("account.title")}</h1>
      <p dir="ltr">{me.email}</p>
      {!me.email_verified && <p className="warn">{s.t("account.unverified")}</p>}
      {error && <p role="alert">{error}</p>}
      {dialog}

      <h2>{s.t("account.factors")}</h2>
      <ul>
        {me.factors.map((f) => (
          <li key={f.id}>
            {f.kind === "passkey" ? "🔑" : "⏱"} <bdi>{f.name}</bdi>{" "}
            <button type="button" onClick={act(async () => (await api("DELETE", `/v1/mfa/${f.id}`), await s.reload()))}>
              {s.t("account.factor.remove")}
            </button>
          </li>
        ))}
      </ul>
      {totp ? (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void act(async () => {
              const r = await api<{ recovery_codes?: string[] }>("POST", "/v1/mfa/totp/confirm", { setup_id: totp.setup_id, code });
              setTotp(null);
              setRecovery(r.recovery_codes ?? null);
              await s.reload();
            })();
          }}
        >
          <p>{s.t("account.totp.secret")}</p>
          <code dir="ltr">{totp.secret}</code>
          <input
            inputMode="numeric"
            value={code}
            onChange={(e) => setCode(e.target.value)}
            autoComplete="one-time-code"
            dir="ltr"
            aria-label={s.t("signin.code")}
          />
          <button type="submit">{s.t("account.totp.confirm")}</button>
        </form>
      ) : (
        <button type="button" onClick={act(async () => setTotp(await api("POST", "/v1/mfa/totp/begin")))}>
          {s.t("account.totp.add")}
        </button>
      )}
      <button
        type="button"
        onClick={act(async () => {
          const o = await api<{ challenge_id: string; publicKey: Parameters<typeof createPasskey>[0] }>("POST", "/v1/mfa/passkey/options");
          const r = await api<{ recovery_codes?: string[] }>("POST", "/v1/mfa/passkey", {
            challenge_id: o.challenge_id,
            credential: await createPasskey(o.publicKey),
          });
          setRecovery(r.recovery_codes ?? null);
          await s.reload();
        })}
      >
        {s.t("account.passkey.add")}
      </button>
      {recovery && (
        <div role="status">
          <p>{s.t("account.recovery")}</p>
          <ul dir="ltr">
            {recovery.map((c) => (
              <li key={c}>
                <code>{c}</code>
              </li>
            ))}
          </ul>
        </div>
      )}

      <h2>{s.t("account.sessions")}</h2>
      <ul>
        {me.sessions.map((x) => (
          <li key={x.id}>
            <bdi>{x.device || "—"}</bdi> · <time dateTime={x.last_seen}>{new Date(x.last_seen).toLocaleString(s.lang)}</time>
            {x.current && <> · {s.t("account.session.current")}</>}
          </li>
        ))}
      </ul>
      <button
        type="button"
        onClick={act(async () => {
          await api("POST", "/v1/sessions/end", { everywhere: true });
          await s.reload();
          s.go("/signin");
        })}
      >
        {s.t("account.signout.everywhere")}
      </button>

      <h2>{s.t("account.homes")}</h2>
      {homes.length === 0 ? (
        <p>{s.t("account.homes.none")}</p>
      ) : (
        <ul>
          {homes.map((h) => (
            <li key={h.id}>
              <bdi>{h.name || h.id}</bdi> · {h.server_version} · {h.platform}{" "}
              <button type="button" onClick={act(async () => (await api("DELETE", `/v1/homes/${h.id}`), await loadHomes()))}>
                {s.t("account.home.unlink")}
              </button>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
