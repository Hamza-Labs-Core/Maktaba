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
// grouped by day and filterable by kind. Both page with "Load more". A
// return shows every page read so far at once and reads them again quietly
// (lib/usePageData: history:watched, history:activity:<kind>).
import { useEffect, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { Tabs } from "@ds/components/Tabs/Tabs";
import { Button } from "@ds/components/Button/Button";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { Skeleton } from "@ds/components/Skeleton/Skeleton";
import { useToast } from "@ds/components/Toast/Toast";
import { useI18n } from "../../lib/i18n";
import {
  analyticsApi,
  formatPercent,
  formatWatchTime,
  type ActivityItem,
  type HistoryItem,
} from "../../lib/analytics";
import { TitleRow } from "../../help/HelpLink";
import { VideoPoster } from "../../components/VideoViews";
import { inputDetail } from "../../lib/errorText";
import { usePageData, type PageData } from "../../lib/usePageData";
import { forgetAnswers, VIDEO_ANSWERS } from "../../lib/pageCache";
import { Loading, TablePlaceholder } from "../../components/Placeholders";

// beside the inline bar, the day's heading keeps its real line height
const NBSP = " ";

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
          {t("history.desc")}{" "}
          <Link to="/account/preferences/privacy">{t("history.privacyLink")}</Link>
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
  return inputDetail(e) ?? fallback;
}

/** A list read page by page, as far as it was read: what a return shows. */
interface Paged<T> {
  items: T[];
  /** another page may follow ("Load more") */
  more: boolean;
}

/**
 * Reads the list from its start, `page` items at a time, until `upTo` are in
 * or it ends: a return reads again as many as were shown, not just the first
 * page.
 */
async function readPages<T>(
  read: (offset: number) => Promise<T[] | null | undefined>,
  page: number,
  upTo: number
): Promise<Paged<T>> {
  const items: T[] = [];
  for (;;) {
    const got = (await read(items.length)) ?? [];
    items.push(...got);
    if (got.length < page) return { items, more: false };
    if (items.length >= Math.max(page, upTo)) return { items, more: true };
  }
}

/**
 * The next page, added to the list on the page and in its kept answer (`key`,
 * the list's: a page that comes after the list changed, another kind, is
 * dropped).
 */
function useLoadMore<T>(
  key: string,
  list: PageData<Paged<T>>,
  read: (offset: number) => Promise<T[] | null | undefined>,
  page: number
) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<unknown>(null);
  const shown = useRef(key);
  useEffect(() => {
    shown.current = key;
  }, [key]);
  async function loadMore() {
    const offset = list.data?.items.length ?? 0;
    setBusy(true);
    setErr(null);
    try {
      const got = (await read(offset)) ?? [];
      if (shown.current !== key) return;
      list.setData((d) => d && { items: [...d.items, ...got], more: got.length === page });
    } catch (e) {
      setErr(e);
    } finally {
      setBusy(false);
    }
  }
  return { busy, err, setErr, loadMore };
}

/** The history table while the first read is slow: the table's own wrappers. */
function HistoryPlaceholder() {
  return (
    <div className="mkt-history__panel">
      <div className="mkt-history__table">
        <TablePlaceholder head columns={6} />
      </div>
    </div>
  );
}

function readHistory(offset: number) {
  return analyticsApi.history({ limit: HISTORY_PAGE, offset }).then((r) => r.items);
}

function HistoryList() {
  const { t, formatDate } = useI18n();
  const toast = useToast();
  const key = "history:watched";
  const list: PageData<Paged<HistoryItem>> = usePageData(key, () =>
    readPages(readHistory, HISTORY_PAGE, list.data?.items.length ?? 0)
  );
  const next = useLoadMore(key, list, readHistory, HISTORY_PAGE);
  const failed = next.err ?? list.err;
  const error = failed != null ? errText(failed, t("common.error")) : null;
  const items = list.data?.items ?? (error ? [] : null);
  const more = list.data?.more ?? false;

  async function remove(videoId: string) {
    try {
      await analyticsApi.deleteHistory(videoId);
      // the resume point went with it: every kept answer showing the
      // video's progress (Continue watching, the video, the timeline) is stale
      forgetAnswers(...VIDEO_ANSWERS);
      list.setData((d) => d && { ...d, items: d.items.filter((it) => it.video_id !== videoId) });
      toast.show({ tone: "success", message: t("history.removed") });
    } catch (e) {
      toast.show({ tone: "error", message: errText(e, t("common.error")) });
    }
  }

  if (items === null)
    return (
      <Loading loading={list.loading}>
        <HistoryPlaceholder />
      </Loading>
    );
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
                      <Link to={`/videos/${h.video_id}`} className="mkt-history__video">
                        <VideoPoster videoId={h.video_id} />
                        <span>{h.title}</span>
                      </Link>
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
        <Button variant="secondary" loading={next.busy} onClick={() => void next.loadMore()}>
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
  const page = limit ?? ACTIVITY_PAGE;
  const read = (offset: number) =>
    analyticsApi
      .activity({ limit: page, offset, types: kind === "all" ? undefined : kind })
      .then((r) => r.items);
  // kept per kind, and apart for the short timeline (Preferences → Privacy)
  const key = `history:activity:${kind}${limit === undefined ? "" : `:${limit}`}`;
  const list: PageData<Paged<ActivityItem>> = usePageData(key, () =>
    limit === undefined
      ? readPages(read, page, list.data?.items.length ?? 0)
      : readPages(read, page, page).then((p) => ({ ...p, more: false }))
  );
  const next = useLoadMore(key, list, read, page);
  const failed = next.err ?? list.err;
  const error = failed != null ? errText(failed, t("common.error")) : null;
  const items = list.data?.items ?? (error ? [] : null);
  const more = list.data?.more ?? false;

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
              onClick={() => {
                next.setErr(null);
                setKind(k);
              }}
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
        <Loading loading={list.loading}>
          <ActivityPlaceholder rows={limit === undefined ? 6 : 3} />
        </Loading>
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
        <Button variant="secondary" loading={next.busy} onClick={() => void next.loadMore()}>
          {t("history.loadMore")}
        </Button>
      )}
    </div>
  );
}

/** The timeline while the first read is slow: a day of rows, the timeline's own classes. */
function ActivityPlaceholder({ rows }: { rows: number }) {
  return (
    <ol className="mkt-activity__days" aria-hidden="true">
      <li className="mkt-activity__day">
        <h3 className="mkt-activity__date">
          <Skeleton variant="text" className="mkt-ph-inline" width="12rem" />
          {NBSP}
        </h3>
        <ol className="mkt-activity__list">
          {Array.from({ length: rows }, (_, i) => (
            <li key={i} className="mkt-activity__row">
              <span className="mkt-activity__time">
                <Skeleton variant="text" className="mkt-ph-line" width="3.5rem" />
              </span>
              <span className="mkt-activity__text">
                <Skeleton variant="text" className="mkt-ph-line" width="60%" />
              </span>
            </li>
          ))}
        </ol>
      </li>
    </ol>
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
