// The matches page of one video in a search:
//   /search/video/:videoId?q=&mode=&sid=&vp=&tp=[&t=]
//
// The player (VideoPlayer, with every match marked on its progress bar and
// Next / Previous match buttons), the search terms with a link back to the
// results (same sid and pages), and every match of the video ordered by
// time: GET /api/search/passages?order=time (include_times=1 on the first
// page for the marks), a page at a time. Clicking a match seeks the player
// there and plays. A sid answering 410 search-expired (or no sid at all)
// runs the search again with the same q and mode, keeps this video and says
// the results were refreshed.
import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { api, ApiError } from "../lib/api";
import { useI18n } from "../lib/i18n";
import { formatDuration, readStoredView } from "../components/VideoViews";
import { VideoPlayer } from "./VideoPlayer";
import {
  DEFAULT_PASSAGES_PAGE_SIZE,
  type Passage,
  type PassagesPage,
  type SearchResponse,
  type VideoCard,
  defaultPageSize,
  isSearchExpired,
  listOf,
  matchesLabel,
  pageCount,
  pageParam,
  readMode,
  recallSizes,
  rememberSizes,
  renderSnippet,
  searchHref,
  ts,
  urlMode,
} from "./searchShared";

interface Matches {
  sid: string;
  video?: VideoCard;
  inTitle: boolean;
  titleSnippet: string;
  total: number;
  items: Passage[];
  times: number[];
}

/** Seconds a "previous match" press skips back over (the match just played). */
const PREV_SLACK = 1.5;
/** Seconds past the playhead before a match counts as "next". */
const NEXT_SLACK = 0.5;

export function SearchVideo() {
  const { videoId = "" } = useParams();
  const { t, n, dir } = useI18n();
  const [params, setParams] = useSearchParams();
  const q = (params.get("q") ?? "").trim();
  const mode = urlMode(params.get("mode")) ?? readMode();
  const sid = params.get("sid") ?? "";
  const vp = pageParam(params.get("vp"));
  const tp = pageParam(params.get("tp"));
  const [data, setData] = useState<Matches | null>(null);
  const [err, setErr] = useState<{ message: string; status?: number } | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);
  const [notice, setNotice] = useState(false);
  const [now, setNow] = useState(0);
  const gen = useRef(0);
  const lastKey = useRef("");
  const alive = useRef(true);
  const paramsRef = useRef(params);
  paramsRef.current = params;
  const videoEl = useRef<HTMLVideoElement | null>(null);

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  const failure = useCallback(
    (e: unknown) =>
      e instanceof ApiError
        ? { message: e.problem.title, status: e.status }
        : { message: t("common.error") },
    [t]
  );

  const fetchPassages = useCallback(
    (s: string, offset: number, size: number, withTimes: boolean) =>
      api.get<PassagesPage>(
        `/api/search/passages?sid=${encodeURIComponent(s)}&video_id=${encodeURIComponent(
          videoId
        )}&order=time&offset=${offset}&limit=${size}${withTimes ? "&include_times=1" : ""}`
      ),
    [videoId]
  );
  // load() and refresh() call each other; the ref keeps the latest refresh.
  const refreshRef = useRef<(withNotice: boolean) => Promise<void>>(async () => {});

  /** Loads the first page of matches (and every match time) of saved search `s`. */
  const load = useCallback(
    async (s: string, g: number, refreshed: boolean) => {
      const size = recallSizes(s)?.passagesPageSize ?? DEFAULT_PASSAGES_PAGE_SIZE;
      try {
        const r = await fetchPassages(s, 0, size, true);
        if (g !== gen.current) return;
        const items = listOf(r?.items);
        setData({
          sid: s,
          video: r?.video,
          inTitle: r?.in_title === true,
          titleSnippet: typeof r?.title_snippet === "string" ? r.title_snippet : "",
          total: typeof r?.total === "number" ? r.total : items.length,
          items,
          times: Array.isArray(r?.times)
            ? r.times.filter((x): x is number => typeof x === "number").sort((a, b) => a - b)
            : items.map((p) => p.start_sec),
        });
      } catch (e) {
        if (g !== gen.current) return;
        if (isSearchExpired(e) && !refreshed) {
          void refreshRef.current(true);
          return;
        }
        setErr(failure(e));
      }
    },
    [fetchPassages, failure]
  );

  /** Runs the search again (same q and mode) and shows this video in the new results. */
  const refresh = useCallback(
    async (withNotice: boolean) => {
      const g = ++gen.current;
      setErr(null);
      if (!q) {
        setErr({ message: t("search.matchesPage.noSearch") });
        return;
      }
      try {
        const view = readStoredView("mkt:search:view", "list");
        const res = await api.post<SearchResponse>("/api/search", { q, mode, view });
        if (g !== gen.current) return;
        const newSid = typeof res?.search_id === "string" ? res.search_id : "";
        if (!newSid) {
          setErr({ message: t("common.error") });
          return;
        }
        const pageSize = res.page_size && res.page_size > 0 ? res.page_size : defaultPageSize(view);
        rememberSizes(newSid, {
          pageSize,
          passagesPageSize:
            res.passages_page_size && res.passages_page_size > 0
              ? res.passages_page_size
              : DEFAULT_PASSAGES_PAGE_SIZE,
        });
        // The results pages the back link opens, clamped to the new totals.
        const next = new URLSearchParams(paramsRef.current);
        next.set("q", q);
        next.set("mode", mode);
        next.set("sid", newSid);
        const v = Math.min(vp, pageCount(res.videos_total ?? 0, pageSize));
        const tr = Math.min(tp, pageCount(res.transcript_total ?? 0, pageSize));
        if (v > 1) next.set("vp", String(v));
        else next.delete("vp");
        if (tr > 1) next.set("tp", String(tr));
        else next.delete("tp");
        lastKey.current = `${videoId}\n${newSid}\n${q}\n${mode}`;
        if (alive.current) setParams(next, { replace: true });
        setNotice(withNotice);
        await load(newSid, g, true);
      } catch (e) {
        if (g !== gen.current) return;
        setErr(failure(e));
      }
    },
    [q, mode, vp, tp, videoId, t, failure, load, setParams]
  );
  refreshRef.current = refresh;

  useEffect(() => {
    const key = `${videoId}\n${sid}\n${q}\n${mode}`;
    if (lastKey.current === key) return;
    lastKey.current = key;
    setData(null);
    setErr(null);
    if (!sid) {
      // A link without a saved search: run it (nothing was refreshed).
      void refresh(false);
      return;
    }
    const g = ++gen.current;
    void load(sid, g, false);
  }, [videoId, sid, q, mode, load, refresh]);

  async function loadMore() {
    if (!data || loadingMore) return;
    const g = gen.current;
    setLoadingMore(true);
    const size = recallSizes(data.sid)?.passagesPageSize ?? DEFAULT_PASSAGES_PAGE_SIZE;
    try {
      const r = await fetchPassages(data.sid, data.items.length, size, false);
      if (g !== gen.current) return;
      setData((d) => {
        if (!d) return d;
        const seen = new Set(d.items.map((p) => p.segment_id));
        return {
          ...d,
          items: [...d.items, ...listOf(r?.items).filter((p) => !seen.has(p.segment_id))],
        };
      });
    } catch (e) {
      if (g !== gen.current) return;
      if (isSearchExpired(e)) void refresh(true);
      else setErr(failure(e));
    } finally {
      setLoadingMore(false);
    }
  }

  const onTime = useCallback((e: Event) => {
    setNow((e.target as HTMLVideoElement).currentTime || 0);
  }, []);
  const onVideoElement = useCallback(
    (el: HTMLVideoElement | null) => {
      videoEl.current?.removeEventListener("timeupdate", onTime);
      videoEl.current = el;
      el?.addEventListener("timeupdate", onTime);
    },
    [onTime]
  );

  /** Moves the player to `at` and plays. */
  function seek(at: number) {
    const el = videoEl.current;
    if (!el) return;
    el.currentTime = at;
    setNow(at);
    try {
      const p = el.play?.();
      if (p && typeof p.catch === "function") p.catch(() => {});
    } catch {
      /* play() refused (autoplay policy): the time still moved */
    }
  }

  const times = data?.times ?? [];
  const nextAt = times.find((x) => x > now + NEXT_SLACK);
  const prevAt = [...times].reverse().find((x) => x < now - PREV_SLACK);
  const back = searchHref({ q, mode, sid: data?.sid || sid, vp, tp });
  const arrow = dir === "rtl" ? "→" : "←";
  const title = data?.video?.title ?? "";

  return (
    <section className="mkt-page mkt-matches">
      <header className="mkt-matches__header">
        <Link to={back} className="mkt-matches__back">
          {arrow} {t("search.matchesPage.back")}
        </Link>
        <h1 className="mkt-matches__title">
          {data?.titleSnippet && data.inTitle ? (
            renderSnippet(data.titleSnippet)
          ) : (
            <span dir="auto">{title}</span>
          )}
        </h1>
        <p className="mkt-matches__terms">
          {q && (
            <span>
              {t("search.matchesPage.terms")}{" "}
              <Link to={back} className="mkt-matches__q" dir="auto">
                {q}
              </Link>
            </span>
          )}
          {data && (
            <span className="mkt-muted" data-testid="matches-count">
              {" · "}
              {matchesLabel(t, n, data.total)}
            </span>
          )}
          {data?.video?.library_name && (
            <span className="mkt-muted">
              {" · "}
              <span dir="auto">{data.video.library_name}</span>
            </span>
          )}
          {typeof data?.video?.duration_sec === "number" && (
            <span className="mkt-muted"> · {formatDuration(data.video.duration_sec)}</span>
          )}
        </p>
      </header>

      {notice && (
        <div className="mkt-search__notice" role="status">
          <span>{t("search.refreshed")}</span>
          <button
            type="button"
            className="mkt-icon-btn mkt-search__notice-close"
            aria-label={t("common.dismiss")}
            onClick={() => setNotice(false)}
          >
            ×
          </button>
        </div>
      )}

      <VideoPlayer markers={times} onVideoElement={onVideoElement} hideBack />

      <div className="mkt-matches__nav" role="group" aria-label={t("search.matchesPage.list")}>
        <Button
          variant="secondary"
          disabled={prevAt === undefined}
          onClick={() => prevAt !== undefined && seek(prevAt)}
        >
          {t("search.matchesPage.prev")}
        </Button>
        <Button
          variant="secondary"
          disabled={nextAt === undefined}
          onClick={() => nextAt !== undefined && seek(nextAt)}
        >
          {t("search.matchesPage.next")}
        </Button>
      </div>

      {err &&
        (err.status === 404 ? (
          <ErrorState
            kind="not_found"
            title={t("search.matchesPage.notInResults")}
            action={<Link to={back}>{t("search.matchesPage.back")}</Link>}
          />
        ) : (
          <ErrorState
            kind="server"
            title={t("common.error")}
            description={err.message}
            action={<Button onClick={() => void refresh(false)}>{t("common.retry")}</Button>}
          />
        ))}

      {!data && !err && <p className="mkt-loading">{t("common.loading")}</p>}

      {data && (
        <section aria-labelledby="mkt-matches-list">
          <h2 id="mkt-matches-list" className="mkt-results__heading">
            {t("search.matchesPage.list")}
          </h2>
          <ul className="mkt-results mkt-matches__list" data-testid="matches-list">
            {data.inTitle && (
              <li className="mkt-match mkt-match--title">
                <Badge tone="success">{t("search.matchType.title")}</Badge>
                <span className="mkt-result__snippet">
                  {data.titleSnippet ? (
                    renderSnippet(data.titleSnippet)
                  ) : (
                    <span dir="auto">{title}</span>
                  )}
                </span>
              </li>
            )}
            {data.items.map((p) => {
              const active = now >= p.start_sec && now < Math.max(p.end_sec, p.start_sec + 1);
              return (
                <li key={p.segment_id} className="mkt-result">
                  <button
                    type="button"
                    className="mkt-match"
                    aria-current={active ? "true" : undefined}
                    onClick={() => seek(p.start_sec)}
                  >
                    <span className="mkt-result__time">[{ts(p.start_sec)}]</span>
                    <Badge tone="accent">{t("search.matchType.transcript")}</Badge>
                    <span className="mkt-result__snippet">{renderSnippet(p.snippet)}</span>
                  </button>
                </li>
              );
            })}
          </ul>
          {data.total === 0 && <p className="mkt-muted">{t("search.matchesPage.noTranscript")}</p>}
          {data.items.length < data.total && (
            <div className="mkt-loadmore">
              <Button variant="secondary" loading={loadingMore} onClick={() => void loadMore()}>
                {t("common.loadMore")} ({n(data.items.length)} / {n(data.total)})
              </Button>
            </div>
          )}
        </section>
      )}
    </section>
  );
}
