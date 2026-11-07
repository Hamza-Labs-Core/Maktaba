// Settings → Plugins → Store account (story 54.15 §5): link this home to a plugin store
// account to rate and, later, buy plugins. Free plugins never need it. The home shows the
// code and a QR code for plugins.maktaba.tv/link (the code is never in the URL), polls the
// store every few seconds while the code lasts, and unlinks with its own signature.
import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { QRCode } from "../../components/QRCode";
import { useI18n } from "../../lib/i18n";
import { pluginsApi, type HeldEntitlement, type StoreLinkStatus } from "../../lib/pluginsApi";
import { refusalWords } from "./PluginsSection";

export function StoreAccount() {
  const { t } = useI18n();
  const [st, setSt] = useState<StoreLinkStatus | null>(null);
  const [error, setError] = useState("");
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const load = useCallback(async () => {
    try {
      setSt(await pluginsApi.storeStatus());
    } catch (e) {
      setError(refusalWords(t, e));
    }
  }, [t]);
  useEffect(() => {
    void load();
    return () => {
      if (timer.current) clearTimeout(timer.current);
    };
  }, [load]);

  const poll = useCallback(
    (interval: number) => {
      timer.current = setTimeout(async () => {
        try {
          const next = await pluginsApi.storePoll();
          setSt(next);
          if (!next.linked && next.pending) poll(next.pending.interval || 5);
        } catch (e) {
          setError(refusalWords(t, e));
          void load();
        }
      }, interval * 1000);
    },
    [t, load]
  );

  const link = async () => {
    setError("");
    try {
      const p = await pluginsApi.storeLink();
      setSt((s) => ({ ...(s ?? { linked: false, store: "" }), pending: p }));
      poll(p.interval || 5);
    } catch (e) {
      setError(refusalWords(t, e));
    }
  };
  const unlink = async () => {
    setError("");
    try {
      await pluginsApi.storeUnlink();
      await load();
    } catch (e) {
      setError(refusalWords(t, e));
    }
  };

  return (
    <section aria-labelledby="pl-store" className="mkt-plugins__policy">
      <h3 id="pl-store">{t("plugins.store.title")}</h3>
      <p className="mkt-plugins__hint">{t("plugins.store.explain")}</p>
      {error && <p role="alert">{error}</p>}
      {st?.linked ? (
        <>
          <p role="status">{t("plugins.store.linked", { id: st.home_id ?? "" })}</p>
          <Button variant="ghost" onClick={() => void unlink()}>
            {t("plugins.store.unlink")}
          </Button>
          <Purchases />
        </>
      ) : st?.pending ? (
        <div className="mkt-plugins__link">
          <p>{t("plugins.store.code_help", { url: st.pending.verification_uri })}</p>
          <p className="mkt-plugins__code" dir="ltr" aria-label={t("plugins.store.code")}>
            {st.pending.user_code}
          </p>
          <QRCode
            value={st.pending.verification_uri}
            label={t("plugins.store.qr", { url: st.pending.verification_uri })}
            size={160}
          />
          <p className="mkt-plugins__hint">{t("plugins.store.waiting")}</p>
        </div>
      ) : (
        <Button onClick={() => void link()}>{t("plugins.store.link")}</Button>
      )}
    </section>
  );
}

/**
 * The paid plugins this home holds an entitlement for (story 54.19 §6): checked offline when
 * the home fetched them (daily, signed by the store), each until its date. "Check for
 * purchases now" asks the store at once.
 */
function Purchases() {
  const { t, formatDate } = useI18n();
  const [list, setList] = useState<HeldEntitlement[] | null>(null);
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const read = useCallback(async () => {
    try {
      setList((await pluginsApi.entitlements()).entitlements ?? []);
    } catch (e) {
      setNote(refusalWords(t, e));
    }
  }, [t]);
  useEffect(() => {
    void read();
  }, [read]);
  const check = async () => {
    setBusy(true);
    setNote("");
    try {
      const r = await pluginsApi.refreshEntitlements();
      setNote(t("plugins.store.purchases.checked", { n: r.valid }));
      await read();
    } catch (e) {
      setNote(refusalWords(t, e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="mkt-plugins__purchases">
      <h4>{t("plugins.store.purchases.title")}</h4>
      {list && list.length === 0 && (
        <p className="mkt-plugins__hint">{t("plugins.store.purchases.none")}</p>
      )}
      {list && list.length > 0 && (
        <ul>
          {list.map((e) => (
            <li key={e.purchase_id} dir="auto">
              {t("plugins.store.purchases.item", {
                plugin: e.plugin_id,
                date: formatDate(new Date(e.valid_until)),
              })}
            </li>
          ))}
        </ul>
      )}
      <Button variant="secondary" disabled={busy} onClick={() => void check()}>
        {t("plugins.store.purchases.check")}
      </Button>
      {note && <p role="status">{note}</p>}
    </div>
  );
}
