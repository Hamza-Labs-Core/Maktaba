// "Sign in with Plex" on the Plex extension card: the plex.tv PIN flow
// (as dizqueTV does it), so nobody has to find or paste a Plex token.
//   POST /api/extensions/plex/signin           -> { id, code, auth_url }
//   GET  /api/extensions/plex/signin/{id}      -> { state, servers? } (polled every 2 s)
//   POST /api/extensions/plex/signin/{id}/use  { name } -> Extension
// Tokens stay on the server; the browser only sees server names and URLs.
import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { useI18n } from "../../lib/i18n";
import { queueApi } from "../../lib/queueApi";
import type { Extension, PlexServerChoice } from "../../lib/queueApi";
import { ApiError } from "../../lib/api";

export const PLEX_POLL_MS = 2000;
export const PLEX_SIGNIN_TIMEOUT_MS = 15 * 60 * 1000;

type Phase =
  | { kind: "idle" }
  | { kind: "waiting"; id: string; authUrl: string }
  | { kind: "pick"; id: string; servers: PlexServerChoice[] }
  | { kind: "using"; id: string; servers: PlexServerChoice[] };

const errText = (e: unknown) =>
  e instanceof ApiError ? e.message : e instanceof Error ? e.message : String(e);

export function PlexSignin({ onSaved }: { onSaved: (e: Extension) => void }) {
  const { t } = useI18n();
  const [phase, setPhase] = useState<Phase>({ kind: "idle" });
  const [picked, setPicked] = useState("");
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const alive = useRef(true);
  // Bumped on every new sign-in so a stale poll loop stops itself.
  const run = useRef(0);

  const stop = useCallback(() => {
    run.current += 1;
    if (timer.current) clearTimeout(timer.current);
    timer.current = null;
  }, []);

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
      stop();
    };
  }, [stop]);

  const use = useCallback(
    async (id: string, servers: PlexServerChoice[], name: string) => {
      setPhase({ kind: "using", id, servers });
      try {
        const ext = await queueApi.plexSigninUse(id, name);
        if (!alive.current) return;
        setPhase({ kind: "idle" });
        setMsg({ ok: true, text: t("queue.ext.plexConnected", { name }) });
        onSaved(ext);
      } catch (e) {
        if (!alive.current) return;
        setPhase({ kind: "pick", id, servers });
        setMsg({ ok: false, text: errText(e) });
      }
    },
    [onSaved, t]
  );

  const start = async () => {
    stop();
    setMsg(null);
    const me = run.current;
    // Open the window inside the click so popup blockers allow it; the
    // plex.tv address is filled in once the PIN exists.
    const win = window.open("about:blank", "_blank");
    let pin;
    try {
      pin = await queueApi.plexSignin();
    } catch (e) {
      win?.close();
      if (alive.current) setMsg({ ok: false, text: errText(e) });
      return;
    }
    if (!alive.current || me !== run.current) return;
    if (win) win.location.href = pin.auth_url;
    setPhase({ kind: "waiting", id: pin.id, authUrl: pin.auth_url });
    const deadline = Date.now() + PLEX_SIGNIN_TIMEOUT_MS;

    const poll = async () => {
      if (!alive.current || me !== run.current) return;
      if (Date.now() >= deadline) {
        setPhase({ kind: "idle" });
        setMsg({ ok: false, text: t("queue.ext.plexTimeout") });
        return;
      }
      try {
        const st = await queueApi.plexSigninStatus(pin.id);
        if (!alive.current || me !== run.current) return;
        if (st.state === "done") {
          const servers = st.servers ?? [];
          if (servers.length === 0) {
            setPhase({ kind: "idle" });
            setMsg({ ok: false, text: t("queue.ext.plexNoServers") });
          } else if (servers.length === 1) {
            void use(pin.id, servers, servers[0].name);
          } else {
            setPicked(servers[0].name);
            setPhase({ kind: "pick", id: pin.id, servers });
          }
          return;
        }
      } catch (e) {
        if (!alive.current || me !== run.current) return;
        setPhase({ kind: "idle" });
        setMsg({ ok: false, text: errText(e) });
        return;
      }
      timer.current = setTimeout(() => void poll(), PLEX_POLL_MS);
    };
    timer.current = setTimeout(() => void poll(), PLEX_POLL_MS);
  };

  return (
    <div className="mkt-q-plexsignin">
      <Button variant="secondary" disabled={phase.kind === "using"} onClick={() => void start()}>
        {t("queue.ext.plexSignin")}
      </Button>
      {phase.kind === "waiting" && (
        <p role="status" className="mkt-q-muted">
          {t("queue.ext.plexWaiting")} {t("queue.ext.plexPopup")}
          <a href={phase.authUrl} target="_blank" rel="noreferrer">
            app.plex.tv
          </a>
        </p>
      )}
      {(phase.kind === "pick" || phase.kind === "using") && (
        <fieldset className="mkt-q-field">
          <legend>{t("queue.ext.plexPick")}</legend>
          {phase.servers.map((s) => (
            <label className="mkt-q-check" key={s.name}>
              <input
                type="radio"
                name="plex-server"
                value={s.name}
                checked={picked === s.name}
                onChange={() => setPicked(s.name)}
              />
              <span>
                {s.name} <span className="mkt-q-muted">{s.uri}</span>
              </span>
            </label>
          ))}
          <Button
            loading={phase.kind === "using"}
            disabled={!picked}
            onClick={() => void use(phase.id, phase.servers, picked)}
          >
            {t("queue.ext.plexUse")}
          </Button>
        </fieldset>
      )}
      {msg && (
        <span role="status" className={msg.ok ? "mkt-q-status--ok" : "mkt-q-status--bad"}>
          <span aria-hidden="true">{msg.ok ? "✓ " : "✕ "}</span>
          {msg.text}
        </span>
      )}
    </div>
  );
}
