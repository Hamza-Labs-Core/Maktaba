// Story 29.4 — the signed-in user's full watch history and activity
// timeline (/account/history).
//
// Real contracts:
//   GET    /api/me/history?limit=&offset=   { items:[HistoryItem], limit, offset }
//   DELETE /api/me/history/{videoId}        204 (also clears the resume point)
//   GET    /api/me/activity?limit=&offset=&types=watched,searched
//                                           { items:[ActivityItem], limit, offset }
//
// Two tabs (`?tab=history|activity`): every video watched, newest first,
// with a per-row remove; and the merged timeline of watches and searches,
// grouped by day and filterable by kind. Both page with "Load more".
import { useCallback, useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { Tabs } from "@ds/components/Tabs/Tabs";
import { Button } from "@ds/components/Button/Button";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { useToast } from "@ds/components/Toast/Toast";
import { ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import {
  analyticsApi,
  formatPercent,
  formatWatchTime,
  type ActivityItem,
  type HistoryItem,
} from "../../lib/analytics";
import { TitleRow } from "../../help/HelpLink";

export const HISTORY_PAGE = 50;
export const ACTIVITY_PAGE = 50;

type Kind = "all" | "watched" | "searched";

export function WatchHistory() {
  const { t } = useI18n();
  const [params, setParams] = useSearchParams();
  const tab = params.get("tab") === "activity" ? "activity" : "history";
  return (
    <section className="mkt-page mkt-history">
      <header className="mkt-page__header">
        <TitleRow>
          <h1>{t("history.title")}</h1>
        </TitleRow>
        <p className="mkt-muted">
          {t("history.desc")} <Link to="/settings/privacy">{t("history.privacyLink")}</Link>
        </p>
      </header>
      <Tabs
        label={t("history.title")}
        value={tab}
        onChange={(id) =>
          setParams(
            (prev) => {
              const next = new URLSearchParams(prev);
              if (id === "history") next.delete("tab");
              else next.set("tab", id);
              return next;
            },
            { replace: true }
          )
        }
        items={[
          { id: "history", label: t("history.tab.watched"), content: <HistoryList /> },
          { id: "activity", label: t("history.tab.activity"), content: <ActivityTimeline /> },
        ]}
      />
    </section>
  );
}

function errText(e: unknown, fallback: string): string {
  return e instanceof ApiError ? e.problem.detail || e.problem.title : fallback;
}

function HistoryList() {
  const { t, formatDate } = useI18n();
  const toast = useToast();
  const [items, setItems] = useState<HistoryItem[] | null>(null);
  const [more, setMore] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const fetchPage = useCallback(
    async (offset: number) => {
      setLoading(true);
      setError(null);
      try {
        const r = await analyticsApi.history({ limit: HISTORY_PAGE, offset });
        const got = r.items ?? [];
        setItems((cur) => (offset === 0 ? got : [...(cur ?? []), ...got]));
        setMore(got.length === HISTORY_PAGE);
      } catch (e) {
        setError(errText(e, t("common.error")));
        setItems((cur) => cur ?? []);
      } finally {
        setLoading(false);
      }
    },
    [t]
  );

  useEffect(() => {
    void fetchPage(0);
  }, [fetchPage]);

  async function remove(videoId: string) {
    try {
      await analyticsApi.deleteHistory(videoId);
      setItems((h) => (h ? h.filter((it) => it.video_id !== videoId) : h));
      toast.show({ tone: "success", message: t("history.removed") });
    } catch (e) {
      toast.show({ tone: "error", message: errText(e, t("common.error")) });
    }
  }

  if (items === null) return <p className="mkt-loading">{t("common.loading")}</p>;
  return (
    <div className="mkt-history__panel">
      {error && <ErrorState kind="server" title={t("common.error")} description={error} />}
      {items.length === 0 && !error ? (
        <EmptyState title={t("history.empty.title")} description={t("history.empty.desc")} />
      ) : (
        items.length > 0 && (
          <div className="mkt-history__table">
            <table className="mkt-table" aria-label={t("history.tab.watched")}>
              <thead>
                <tr>
                  <th>{t("analytics.col.video")}</th>
                  <th>{t("history.col.lastWatched")}</th>
                  <th>{t("history.col.times")}</th>
                  <th>{t("settings.privacy.progress")}</th>
                  <th>{t("settings.privacy.watched")}</th>
                  <th>
                    <span className="mk-visually-hidden">{t("history.col.actions")}</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {items.map((h) => (
                  <tr key={h.video_id}>
                    <td dir="auto">
                      <Link to={`/videos/${h.video_id}`}>{h.title}</Link>
                    </td>
                    <td>{formatDate(h.last_watched_at)}</td>
                    <td>{h.times_watched}</td>
                    <td>{h.completed ? t("history.completed") : formatPercent(h.best_percent)}</td>
                    <td>{formatWatchTime(h.total_watch_sec)}</td>
                    <td>
                      <Button size="sm" variant="ghost" onClick={() => void remove(h.video_id)}>
                        {t("settings.privacy.remove")}
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )
      )}
      {more && (
        <Button variant="secondary" loading={loading} onClick={() => void fetchPage(items.length)}>
          {t("history.loadMore")}
        </Button>
      )}
    </div>
  );
}

// dayKey groups timeline rows by the viewer's local calendar day.
function dayKey(iso: string): string {
  const d = new Date(iso);
  return `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`;
}

export function ActivityTimeline({ limit }: { limit?: number }) {
  const { t, locale } = useI18n();
  const [kind, setKind] = useState<Kind>("all");
  const [items, setItems] = useState<ActivityItem[] | null>(null);
  const [more, setMore] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const page = limit ?? ACTIVITY_PAGE;

  const fetchPage = useCallback(
    async (offset: number, k: Kind) => {
      setLoading(true);
      setError(null);
      try {
        const r = await analyticsApi.activity({
          limit: page,
          offset,
          types: k === "all" ? undefined : k,
        });
        const got = r.items ?? [];
        setItems((cur) => (offset === 0 ? got : [...(cur ?? []), ...got]));
        setMore(limit === undefined && got.length === page);
      } catch (e) {
        setError(errText(e, t("common.error")));
        setItems((cur) => cur ?? []);
      } finally {
        setLoading(false);
      }
    },
    [page, limit, t]
  );

  useEffect(() => {
    setItems(null);
    void fetchPage(0, kind);
  }, [fetchPage, kind]);

  const tag = locale === "ar" ? "ar-EG" : "en";
  const timeFmt = new Intl.DateTimeFormat(tag, { hour: "numeric", minute: "2-digit" });
  const dayFmt = new Intl.DateTimeFormat(tag, { dateStyle: "full" });
  const groups: Array<{ key: string; label: string; rows: ActivityItem[] }> = [];
  for (const it of items ?? []) {
    const key = dayKey(it.at);
    const last = groups[groups.length - 1];
    if (last && last.key === key) last.rows.push(it);
    else groups.push({ key, label: dayFmt.format(new Date(it.at)), rows: [it] });
  }

  return (
    <div className="mkt-activity">
      {limit === undefined && (
        <div className="mkt-activity__filters" role="group" aria-label={t("history.filter")}>
          {(["all", "watched", "searched"] as Kind[]).map((k) => (
            <Button
              key={k}
              size="sm"
              variant={kind === k ? "primary" : "ghost"}
              aria-pressed={kind === k}
              onClick={() => setKind(k)}
            >
              {t(`history.kind.${k}`)}
            </Button>
          ))}
        </div>
      )}
      {error &&
        (limit === undefined ? (
          <ErrorState kind="server" title={t("common.error")} description={error} />
        ) : (
          <p className="mkt-muted">{t("history.activity.loadFailed")}</p>
        ))}
      {items === null ? (
        <p className="mkt-loading">
          {limit === undefined ? t("common.loading") : t("history.activity.loading")}
        </p>
      ) : items.length === 0 && !error ? (
        <p className="mkt-muted">{t("history.activity.empty")}</p>
      ) : (
        <ol className="mkt-activity__days">
          {groups.map((g) => (
            <li key={g.key} className="mkt-activity__day">
              <h3 className="mkt-activity__date">{g.label}</h3>
              <ol className="mkt-activity__list">
                {g.rows.map((it, i) => (
                  <li
                    key={`${it.at}-${i}`}
                    className={`mkt-activity__row mkt-activity__row--${it.kind}`}
                  >
                    <time dateTime={it.at} className="mkt-activity__time">
                      {timeFmt.format(new Date(it.at))}
                    </time>
                    <ActivityText item={it} />
                  </li>
                ))}
              </ol>
            </li>
          ))}
        </ol>
      )}
      {more && items && (
        <Button
          variant="secondary"
          loading={loading}
          onClick={() => void fetchPage(items.length, kind)}
        >
          {t("history.loadMore")}
        </Button>
      )}
    </div>
  );
}

function ActivityText({ item }: { item: ActivityItem }) {
  const { t } = useI18n();
  const meta = item.meta ?? {};
  if (item.kind === "watched") {
    const id = String(meta.video_id ?? "");
    const title = String(meta.title || t("history.untitled"));
    const pct = typeof meta.percent_complete === "number" ? meta.percent_complete : null;
    return (
      <span className="mkt-activity__text">
        {t("history.activity.watched")}{" "}
        {id ? (
          <Link to={`/videos/${id}`} dir="auto">
            {title}
          </Link>
        ) : (
          <span dir="auto">{title}</span>
        )}
        {pct !== null && <span className="mkt-muted"> · {formatPercent(pct)}</span>}
      </span>
    );
  }
  if (item.kind === "searched") {
    const q = String(meta.query ?? "");
    return (
      <span className="mkt-activity__text">
        {t("history.activity.searched")}{" "}
        <Link to={`/search?q=${encodeURIComponent(q)}`} dir="auto">
          {q}
        </Link>
      </span>
    );
  }
  return <span className="mkt-activity__text">{t("history.activity.rated")}</span>;
}
