// The notification centre in the web header (Epic 33, story 33.13 AC2): a bell with the
// unread count, the list of notices (localised from their key + params), each linking
// to what it is about, read per user. Reads /api/notifications every 30 s while the page
// is visible and whenever the list is opened; a notice is marked read when it is opened.
//
// Stable layout (PR #76): the count sits on the bell (absolute: the header never
// moves), the list keeps its order (newest first) and updates in place.
import { useCallback, useEffect, useId, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useI18n } from "../lib/i18n";
import { notificationsApi, type AppNotification } from "../lib/notificationsApi";
import { reconcileList } from "../lib/tvChanges";
import "../styles/notifications.css";

export const NOTIFY_POLL_MS = 30_000;

/** A notice's params as the i18n layer takes them (lists joined, flags as words). */
export function notificationVars(params: Record<string, unknown>): Record<string, string | number> {
  const out: Record<string, string | number> = {};
  for (const [k, v] of Object.entries(params ?? {})) {
    if (typeof v === "number" || typeof v === "string") out[k] = v;
    else if (Array.isArray(v)) out[k] = v.join(", ");
    else if (typeof v === "boolean") out[k] = v ? "yes" : "no";
  }
  return out;
}

export function NotificationsBell() {
  const { t, n, formatDate } = useI18n();
  const nav = useNavigate();
  const [items, setItems] = useState<AppNotification[] | null>(null);
  const [unread, setUnread] = useState(0);
  const [open, setOpen] = useState(false);
  const [failed, setFailed] = useState(false);
  const wrap = useRef<HTMLDivElement | null>(null);
  const button = useRef<HTMLButtonElement | null>(null);
  const panelId = useId();
  const alive = useRef(true);

  const load = useCallback(async () => {
    try {
      const r = await notificationsApi.list(30);
      if (!alive.current) return;
      // one batched update per read; unchanged notices keep their objects
      setItems((prev) => reconcileList(prev, r.items ?? []));
      setUnread(r.unread ?? 0);
      setFailed(false);
    } catch {
      if (alive.current) setFailed(true);
    }
  }, []);

  useEffect(() => {
    alive.current = true;
    let timer: number | null = null;
    const start = () => {
      if (timer === null) timer = window.setInterval(() => void load(), NOTIFY_POLL_MS);
    };
    const stop = () => {
      if (timer !== null) window.clearInterval(timer);
      timer = null;
    };
    const onVisibility = () => {
      if (document.visibilityState === "visible") {
        void load();
        start();
      } else stop();
    };
    void load();
    if (document.visibilityState === "visible") start();
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      alive.current = false;
      stop();
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [load]);

  // close on Escape and on a click outside
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setOpen(false);
        button.current?.focus();
      }
    };
    const onDown = (e: MouseEvent) => {
      if (wrap.current && !wrap.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("keydown", onKey);
    document.addEventListener("mousedown", onDown);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("mousedown", onDown);
    };
  }, [open]);

  async function openItem(x: AppNotification) {
    setOpen(false);
    if (!x.read) {
      setItems((prev) => prev?.map((y) => (y.id === x.id ? { ...y, read: true } : y)) ?? prev);
      setUnread((u) => Math.max(0, u - 1));
      try {
        const r = await notificationsApi.read(x.id);
        setUnread(r.unread);
      } catch {
        /* the next read corrects it */
      }
    }
    if (x.link) nav(x.link);
  }

  async function readAll() {
    setItems((prev) => prev?.map((y) => (y.read ? y : { ...y, read: true })) ?? prev);
    setUnread(0);
    try {
      const r = await notificationsApi.readAll();
      setUnread(r.unread);
    } catch {
      /* the next read corrects it */
    }
  }

  const label = unread > 0 ? t("notify.bellUnread", { n: n(unread) }) : t("notify.bell");

  return (
    <div className="mkt-bell" ref={wrap}>
      <button
        ref={button}
        type="button"
        className="mkt-btn mkt-btn--ghost mkt-bell__button"
        aria-label={label}
        title={label}
        aria-expanded={open}
        aria-controls={panelId}
        aria-haspopup="true"
        data-testid="notifications-bell"
        onClick={() => {
          setOpen((o) => !o);
          if (!open) void load();
        }}
      >
        <svg viewBox="0 0 24 24" width="20" height="20" aria-hidden="true" focusable="false">
          <path
            fill="currentColor"
            d="M12 22a2.5 2.5 0 0 0 2.45-2h-4.9A2.5 2.5 0 0 0 12 22Zm7-6V11a7 7 0 0 0-5.5-6.84V3.5a1.5 1.5 0 0 0-3 0v.66A7 7 0 0 0 5 11v5l-2 2v1h18v-1Z"
          />
        </svg>
        <span
          className={`mkt-bell__count${unread > 0 ? "" : " mkt-bell__count--none"}`}
          aria-hidden="true"
          data-testid="notifications-count"
        >
          {unread > 99 ? "99+" : n(unread)}
        </span>
      </button>
      {open && (
        <div id={panelId} className="mkt-bell__panel" role="dialog" aria-label={t("notify.title")}>
          <div className="mkt-bell__head">
            <h2>{t("notify.title")}</h2>
            <button
              type="button"
              className="mkt-btn mkt-btn--ghost"
              disabled={unread === 0}
              onClick={() => void readAll()}
            >
              {t("notify.readAll")}
            </button>
          </div>
          {failed && !items && <p className="mkt-bell__empty">{t("notify.loadError")}</p>}
          {items && items.length === 0 && <p className="mkt-bell__empty">{t("notify.empty")}</p>}
          {items && items.length > 0 && (
            <ul className="mkt-bell__list">
              {items.map((x) => (
                <li key={x.id}>
                  <button
                    type="button"
                    className={`mkt-bell__item mkt-bell__item--${x.severity}${x.read ? "" : " mkt-bell__item--unread"}`}
                    onClick={() => void openItem(x)}
                  >
                    <span className="mkt-bell__dot" aria-hidden="true" />
                    <span className="mkt-bell__text">
                      <span className="mkt-bell__title">
                        {t(
                          x.title_key,
                          Object.fromEntries(
                            Object.entries(notificationVars(x.params)).map(([k, v]) => [
                              k,
                              typeof v === "number" ? n(v) : v,
                            ])
                          )
                        )}
                      </span>
                      <time className="mkt-bell__time" dateTime={x.created_at}>
                        {formatDate(x.created_at)}
                      </time>
                    </span>
                    {!x.read && <span className="mkt-visually-hidden">{t("notify.unread")}</span>}
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  );
}
