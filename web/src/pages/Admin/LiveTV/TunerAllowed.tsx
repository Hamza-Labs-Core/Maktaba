// Epic 57 story 57.10: the addresses allowed to use the tuner.
//
//   GET /api/tv/hdhr/allowed   (admin) {addresses: [...]}
//   PUT /api/tv/hdhr/allowed   (admin) {addresses: [...]} → the stored list
//
// The tuner (HDHomeRun) routes and the guide exports cannot carry a sign-in: Plex's
// DVR and other guide software just fetch them. So the home answers them only to
// this machine, to the Plex server's own address, and to the addresses listed here
// (an address or a range such as 192.168.1.0/24). Everyone else gets 403.
import { useEffect, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { ApiError } from "../../../lib/api";
import { useI18n } from "../../../lib/i18n";
import { MAX_TUNER_ALLOWED, fetchTunerAllowed, saveTunerAllowed } from "../../../lib/parentalApi";
import "../../../styles/parental.css";

/** The textarea's lines as a list: trimmed, no blanks, no repeats. */
export function parseAddresses(text: string): string[] {
  const out: string[] = [];
  for (const line of text.split(/[\n,]+/)) {
    const a = line.trim();
    if (a !== "" && !out.includes(a)) out.push(a);
  }
  return out;
}

export function TunerAllowed() {
  const { t, n } = useI18n();
  const [saved, setSaved] = useState<string[] | null>(null);
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const [loadFailed, setLoadFailed] = useState(false);

  useEffect(() => {
    let alive = true;
    fetchTunerAllowed()
      .then((list) => {
        if (!alive) return;
        setSaved(list);
        setText(list.join("\n"));
      })
      .catch(() => alive && setLoadFailed(true));
    return () => {
      alive = false;
    };
  }, []);

  const list = parseAddresses(text);
  const tooMany = list.length > MAX_TUNER_ALLOWED;
  const dirty = saved !== null && list.join("\n") !== saved.join("\n");

  async function save() {
    setBusy(true);
    setMsg(null);
    try {
      const next = await saveTunerAllowed(list);
      setSaved(next);
      setText(next.join("\n"));
      setMsg({ ok: true, text: t("live.admin.hdhrAllowed.saved") });
    } catch (e) {
      setMsg({
        ok: false,
        text:
          e instanceof ApiError && e.status === 422
            ? t("live.admin.hdhrAllowed.invalid")
            : e instanceof ApiError && e.status === 403
              ? t("common.forbidden.desc")
              : t("common.error"),
      });
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card className="mkt-livetv__card mkt-tuner-allowed" data-testid="tuner-allowed">
      <h2 id="mkt-tuner-allowed-title">{t("live.admin.hdhrAllowed")}</h2>
      <p className="mkt-parental-note">{t("live.admin.hdhrAllowed.desc")}</p>
      {loadFailed ? (
        <p role="alert">{t("live.admin.hdhrAllowed.loadFailed")}</p>
      ) : saved === null ? (
        <p className="mkt-loading">{t("common.loading")}</p>
      ) : (
        <>
          <textarea
            aria-labelledby="mkt-tuner-allowed-title"
            aria-describedby="mkt-tuner-allowed-hint"
            value={text}
            spellCheck={false}
            onChange={(e) => {
              setText(e.target.value);
              setMsg(null);
            }}
          />
          <p id="mkt-tuner-allowed-hint" className="mkt-parental-note">
            {t("live.admin.hdhrAllowed.hint", { max: n(MAX_TUNER_ALLOWED) })}
          </p>
          {tooMany && (
            <p role="alert">{t("live.admin.hdhrAllowed.tooMany", { max: n(MAX_TUNER_ALLOWED) })}</p>
          )}
          <Button onClick={() => void save()} disabled={!dirty || tooMany || busy} loading={busy}>
            {t("common.save")}
          </Button>
          {msg && (
            <p
              role={msg.ok ? "status" : "alert"}
              className={msg.ok ? "mkt-q-status--ok" : "mkt-q-status--bad"}
            >
              {msg.text}
            </p>
          )}
        </>
      )}
    </Card>
  );
}
