// Epic 46 — big screen's Search: a field with an on-screen keyboard (English
// and Arabic layouts, space, delete, clear) that a physical keyboard types
// into as well; results in titles and in transcripts. POST /api/search (the
// desk Search's call, mode hybrid) runs SEARCH_DELAY_MS after typing stops;
// a transcript match plays from its time. The query is kept in the URL (?q=),
// so Back from a result returns to it.
import { useEffect, useRef, useState } from "react";
import { useLocation, useSearchParams } from "react-router-dom";
import { api } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import { renderSnippet, type SearchResponse } from "../../pages/searchShared";
import { bigPath, useBigNav } from "../nav";
import { Empty, Failed, Screen, VideoTile, clock } from "../ui";
import { LAYOUTS, applyKey, searchable, type KeyEdit, type KeyboardLayout } from "./keyboard";
import { errorText } from "../../lib/errorText";

export const SEARCH_DELAY_MS = 600;

export function BigSearch() {
  const { t, n, locale } = useI18n();
  const { go } = useBigNav();
  const [params, setParams] = useSearchParams();
  const location = useLocation();
  const [text, setText] = useState(() => params.get("q") ?? "");
  const [layout, setLayout] = useState<KeyboardLayout>(locale === "ar" ? "ar" : "en");
  const [res, setRes] = useState<SearchResponse | null>(null);
  const [pending, setPending] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const seq = useRef(0);
  const query = searchable(text);

  useEffect(() => {
    // the URL follows the query (replace: typing is not history)
    const cur = params.get("q") ?? "";
    if (cur !== (query ?? "")) {
      const nextParams = new URLSearchParams(params);
      if (query) nextParams.set("q", query);
      else nextParams.delete("q");
      setParams(nextParams, { replace: true, state: location.state });
    }
    if (!query) {
      setRes(null);
      setErr(null);
      setPending(false);
      return;
    }
    const s = ++seq.current;
    setPending(true);
    const timer = setTimeout(() => {
      api
        .post<SearchResponse>("/api/search", { q: query, mode: "hybrid", view: "grid" })
        .then((r) => {
          if (s !== seq.current) return;
          setRes(r);
          setErr(null);
        })
        .catch((e) => {
          if (s !== seq.current) return;
          setErr(errorText(e, t));
        })
        .finally(() => s === seq.current && setPending(false));
    }, SEARCH_DELAY_MS);
    return () => clearTimeout(timer);
    // params/setParams change with every replace; the query drives this
  }, [query, t]);

  const press = (k: KeyEdit) => setText((cur) => applyKey(cur, k));
  const titles = (res?.videos ?? []).filter((v) => v && typeof v.video_id === "string");
  const transcripts = (res?.transcript_videos ?? []).filter(
    (v) => v && typeof v.video_id === "string"
  );

  return (
    <Screen testId="big-search">
      <div className="bs-search">
        <div className="bs-search__entry">
          <input
            className="bs-search__field"
            type="search"
            value={text}
            onChange={(e) => setText(e.target.value)}
            placeholder={t("search.placeholder")}
            aria-label={t("nav.search")}
            data-bs-focus="search:field"
            data-bs-group="search-field"
            dir="auto"
          />
          <div className="bs-kbd" data-bs-group="kbd" role="group" aria-label={t("big.keyboard")}>
            {LAYOUTS[layout].map((line, li) => (
              <div className="bs-kbd__line" key={li}>
                {line.map((ch, ci) => (
                  <button
                    key={ch}
                    type="button"
                    className="bs-key"
                    data-bs-focus={`key:${layout}:${ch}`}
                    data-bs-autofocus={(li === 0 && ci === 0) || undefined}
                    onClick={() => press({ kind: "char", char: ch })}
                  >
                    {ch}
                  </button>
                ))}
              </div>
            ))}
            <div className="bs-kbd__line bs-kbd__line--actions">
              <button
                type="button"
                className="bs-key bs-key--wide"
                data-bs-focus="key:layout"
                aria-label={t("big.keyboard.switch")}
                onClick={() => setLayout((l) => (l === "en" ? "ar" : "en"))}
              >
                {layout === "en" ? "ع" : "EN"}
              </button>
              <button
                type="button"
                className="bs-key bs-key--space"
                data-bs-focus="key:space"
                onClick={() => press({ kind: "space" })}
              >
                {t("big.keyboard.space")}
              </button>
              <button
                type="button"
                className="bs-key bs-key--wide"
                data-bs-focus="key:delete"
                aria-label={t("big.keyboard.delete")}
                onClick={() => press({ kind: "delete" })}
              >
                ⌫
              </button>
              <button
                type="button"
                className="bs-key bs-key--wide"
                data-bs-focus="key:clear"
                onClick={() => press({ kind: "clear" })}
              >
                {t("big.keyboard.clear")}
              </button>
            </div>
          </div>
        </div>

        <div className="bs-search__results" aria-live="polite" aria-busy={pending}>
          {!query && <Empty>{t("big.search.hint")}</Empty>}
          {query && err && <Failed message={err} />}
          {query && !err && pending && !res && <p className="bs-state">{t("common.loading")}</p>}
          {query && res && !err && titles.length === 0 && transcripts.length === 0 && !pending && (
            <Empty>{t("big.search.none", { q: query })}</Empty>
          )}
          {titles.length > 0 && (
            <section className="bs-row">
              <h2 className="bs-row__title">
                {t("big.search.titles")}{" "}
                <span className="bs-count">{n(res?.videos_total ?? titles.length)}</span>
              </h2>
              <div className="bs-row__scroller" data-bs-group="row:search-titles" role="list">
                {titles.map((v) => (
                  <VideoTile
                    key={v.video_id}
                    video={{ id: v.video_id, title: v.title, duration_sec: v.duration_sec }}
                    focusId={`st:${v.video_id}`}
                    onSelect={() => go(bigPath.video(v.video_id))}
                  />
                ))}
              </div>
            </section>
          )}
          {transcripts.length > 0 && (
            <section className="bs-row">
              <h2 className="bs-row__title">
                {t("big.search.transcripts")}{" "}
                <span className="bs-count">{n(res?.transcript_total ?? transcripts.length)}</span>
              </h2>
              <div className="bs-matches" data-bs-group="search-matches">
                {transcripts.map((v) => {
                  const p = (v.passages ?? [])[0];
                  return (
                    <button
                      key={v.video_id}
                      type="button"
                      className="bs-match"
                      data-bs-focus={`sm:${v.video_id}`}
                      onClick={() =>
                        p
                          ? go(bigPath.watch(v.video_id, { t: p.start_sec }))
                          : go(bigPath.video(v.video_id))
                      }
                    >
                      <bdi className="bs-match__title">{v.title}</bdi>
                      {p && (
                        <span className="bs-match__line">
                          <span className="bs-line__at">{clock(p.start_sec)}</span>
                          <span dir="auto">{renderSnippet(p.snippet)}</span>
                        </span>
                      )}
                    </button>
                  );
                })}
              </div>
            </section>
          )}
        </div>
      </div>
    </Screen>
  );
}
