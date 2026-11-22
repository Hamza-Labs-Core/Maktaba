// Epic 46 — big screen's Search: a field with an on-screen keyboard (English
// and Arabic layouts, space, delete, clear) that a physical keyboard types
// into as well; results in titles and in transcripts. POST /api/search (the
// desk Search's call, mode hybrid) runs SEARCH_DELAY_MS after typing stops;
// a transcript match plays from its time. The query is kept in the URL (?q=),
// so Back from a result returns to it.
//
// Story 49.11 (the API of 49.8, as the desk's page has it in 49.10):
//   - suggestions as you type, under the keyboard: GET /api/search/suggest
//     SUGGEST_DELAY_MS after a key, each with its kind's icon and word (an
//     earlier search, a word, a video's title, a speaker); a title opens its
//     video, any other is searched; a choice hides the list until the next key;
//   - "Did you mean …?" above the results, its words a button that searches them
//     with the same order and filters;
//   - the order (Relevance, Newest, Most matches) as chips, and Filters: a side
//     panel (a focus layer, Back closes it) of facet chips with their counts by
//     library, language, speaker, year and length; several combine, each one in
//     use is a chip that removes it, and Clear all takes them all off;
//   - the order and filters ride in the URL beside q (the desk's parameters), and
//     new words drop the filters (they were the old results' counts).
import { useEffect, useRef, useState } from "react";
import { useLocation, useSearchParams } from "react-router-dom";
import { api } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import {
  type FacetName,
  type Facets,
  SORTS,
  type SearchResponse,
  didYouMeanOf,
  facetsOf,
  listOf,
  noSel,
  renderSnippet,
  selCount,
  selHas,
  toggleSel,
  urlSort,
} from "../../pages/searchShared";
import {
  DidYouMean,
  KindIcon,
  type SuggestItem,
  activeChips,
  facetGroups,
  suggestItems,
  useBucketLabel,
} from "../../pages/searchHelp";
import { cssEscape, useFocusApi } from "../focus/FocusProvider";
import { bigPath, useBigNav } from "../nav";
import { Empty, Failed, Ltr, Panel, Screen, VideoTile, clock } from "../ui";
import { LAYOUTS, applyKey, searchable, type KeyEdit, type KeyboardLayout } from "./keyboard";
import { type BigAsk, askKey, askOf, bigSearchBody, bigSearchParams } from "./searchAsk";
import { errorText } from "../../lib/errorText";

export const SEARCH_DELAY_MS = 600;
export const SUGGEST_DELAY_MS = 200;
/** The suggestions shown under the keyboard. */
export const SUGGEST_MAX = 6;

export function BigSearch() {
  const { t, n, locale } = useI18n();
  const { go } = useBigNav();
  const focus = useFocusApi();
  const bucketName = useBucketLabel();
  const [params, setParams] = useSearchParams();
  const location = useLocation();
  const [text, setText] = useState(() => params.get("q") ?? "");
  const [ask, setAsk] = useState<BigAsk>(() => askOf(params));
  const [layout, setLayout] = useState<KeyboardLayout>(locale === "ar" ? "ar" : "en");
  const [res, setRes] = useState<SearchResponse | null>(null);
  const [pending, setPending] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [suggestions, setSuggestions] = useState<SuggestItem[]>([]);
  const [filtersOpen, setFiltersOpen] = useState(false);
  // the names seen for library buckets, for a chip whose bucket the narrowed answer dropped
  const [labels, setLabels] = useState<Record<string, string>>({});
  const seq = useRef(0);
  const suggestSeq = useRef(0);
  const textRef = useRef(text);
  // the facets on screen: a year or band sends its bucket's own filter
  const facetsRef = useRef<Facets | null>(null);
  // the query the last search was started for: the same words with another order or
  // filter run at once, new words after typing stops
  const ranFor = useRef<string | null>(null);
  // the text a suggestion was chosen for (or the URL's, coming back to a search): no list
  // until the next key
  const suggestHidden = useRef<string | null>((params.get("q") ?? "").trim() || null);
  const query = searchable(text);
  const key = askKey(ask);

  /** New text; new words drop the filters (they were the old results' counts). */
  function edit(next: string, keepFilters = false) {
    const before = searchable(textRef.current);
    textRef.current = next;
    setText(next);
    if (!keepFilters && searchable(next) !== before)
      setAsk((a) => (selCount(a.sel) > 0 ? { ...a, sel: noSel() } : a));
  }

  /** Focuses a control of this screen once it is on the page. */
  function focusOn(id: string) {
    setTimeout(() => {
      const el = document.querySelector<HTMLElement>(`[data-bs-focus="${cssEscape(id)}"]`);
      if (el) focus.focusEl(el);
    }, 0);
  }

  useEffect(() => {
    // the URL follows the query, order and filters (replace: typing is not history)
    const want = bigSearchParams(params, query, ask);
    if (want.toString() !== params.toString())
      setParams(want, { replace: true, state: location.state });
    if (!query) {
      ranFor.current = null;
      setRes(null);
      setErr(null);
      setPending(false);
      return;
    }
    const s = ++seq.current;
    const delay = ranFor.current === query ? 0 : SEARCH_DELAY_MS;
    ranFor.current = query;
    setPending(true);
    const timer = setTimeout(() => {
      api
        .post<SearchResponse>("/api/search", bigSearchBody(query, ask, facetsRef.current))
        .then((r) => {
          if (s !== seq.current) return;
          const facets = facetsOf(r);
          facetsRef.current = facets;
          const libs = listOf(facets?.library).filter((b) => b?.label);
          if (libs.length)
            setLabels((old) => {
              const next = { ...old };
              for (const b of libs) next[`library:${b.value}`] = b.label!;
              return next;
            });
          setRes(r);
          setErr(null);
        })
        .catch((e) => {
          if (s !== seq.current) return;
          setErr(errorText(e, t));
        })
        .finally(() => s === seq.current && setPending(false));
    }, delay);
    return () => clearTimeout(timer);
    // params/setParams change with every replace; the query, order and filters drive this
  }, [query, key, t]);

  // suggestions from the first letter: on a remote every key saved counts
  const typed = text.trim();
  useEffect(() => {
    const s = ++suggestSeq.current;
    if (!typed || typed === suggestHidden.current) {
      setSuggestions([]);
      return;
    }
    suggestHidden.current = null;
    const timer = setTimeout(() => {
      api
        .get<unknown>(`/api/search/suggest?q=${encodeURIComponent(typed)}`)
        .then((r) => s === suggestSeq.current && setSuggestions(suggestItems(r)))
        .catch(() => s === suggestSeq.current && setSuggestions([]));
    }, SUGGEST_DELAY_MS);
    return () => clearTimeout(timer);
  }, [typed]);

  const press = (k: KeyEdit) => edit(applyKey(textRef.current, k));

  /** A suggestion: a title opens its video; any other is searched. */
  function choose(it: SuggestItem) {
    if (it.kind === "title" && it.video_id) {
      go(bigPath.video(it.video_id));
      return;
    }
    suggestHidden.current = it.text.trim();
    suggestSeq.current += 1;
    setSuggestions([]);
    edit(it.text);
    focusOn(`key:${layout}:${LAYOUTS[layout][0]![0]}`);
  }

  /** Did you mean: its words, with the same order and filters. */
  function runCorrected(q: string) {
    suggestHidden.current = q.trim();
    edit(q, true);
    focusOn(`sort:${ask.sort}`);
  }

  const setSort = (sort: BigAsk["sort"]) => setAsk((a) => ({ ...a, sort }));
  const toggle = (facet: FacetName, value: string) =>
    setAsk((a) => ({ ...a, sel: toggleSel(a.sel, facet, value) }));
  function remove(facet: FacetName, value: string) {
    const left = selCount(ask.sel) - 1;
    toggle(facet, value);
    focusOn(left > 0 ? "search:filters" : `sort:${ask.sort}`);
  }
  function clearFilters() {
    setAsk((a) => ({ ...a, sel: noSel() }));
    focusOn(`sort:${ask.sort}`);
  }

  const titles = (res?.videos ?? []).filter((v) => v && typeof v.video_id === "string");
  const transcripts = (res?.transcript_videos ?? []).filter(
    (v) => v && typeof v.video_id === "string"
  );
  const shownSuggestions = suggestions.slice(0, SUGGEST_MAX);
  const dym = query && res && !err ? didYouMeanOf(res, query) : "";
  // the order the home answered in is the one shown as chosen, once it has answered
  const shownSort = res && !pending ? urlSort(res.sort ?? ask.sort) : ask.sort;
  const groups = facetGroups(res ? facetsOf(res) : null, ask.sel);
  const inUse = activeChips(ask.sel);
  const chipName = (facet: FacetName, value: string, label?: string) =>
    `${t(`search.facet.${facet}`)}: ${bucketName(facet, value, label ?? labels[`${facet}:${value}`])}`;

  return (
    <Screen testId="big-search">
      <div className="bs-search">
        <div className="bs-search__entry">
          <input
            className="bs-search__field"
            type="search"
            value={text}
            onChange={(e) => edit(e.target.value)}
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
          {shownSuggestions.length > 0 && (
            <section className="bs-suggest" aria-labelledby="bs-suggest-title">
              <h2 id="bs-suggest-title" className="bs-suggest__title">
                {t("search.suggest.label")}
              </h2>
              <ul
                className="bs-suggest__list"
                data-bs-group="search-suggest"
                aria-labelledby="bs-suggest-title"
              >
                {shownSuggestions.map((it, i) => (
                  <li key={`${it.kind ?? ""}:${it.video_id ?? ""}:${it.text}`}>
                    <button
                      type="button"
                      className="bs-suggest__item"
                      data-kind={it.kind ?? "plain"}
                      data-bs-focus={`sg:${i}`}
                      onClick={() => choose(it)}
                    >
                      <KindIcon kind={it.kind} />
                      <bdi className="bs-suggest__text">{it.text}</bdi>
                      {it.kind && (
                        <span className="bs-suggest__kind">
                          {t(`search.suggest.kind.${it.kind}`)}
                        </span>
                      )}
                    </button>
                  </li>
                ))}
              </ul>
            </section>
          )}
        </div>

        <div className="bs-search__results" aria-live="polite" aria-busy={pending}>
          {!query && <Empty>{t("big.search.hint")}</Empty>}
          {dym && (
            <DidYouMean
              text={dym}
              onRun={runCorrected}
              className="bs-dym"
              buttonClassName="bs-chip bs-dym__q"
              focusId="search:dym"
              testId="big-did-you-mean"
            />
          )}
          {query && res && !err && (
            <div className="bs-refine">
              <div
                className="bs-chips bs-refine__bar"
                data-bs-group="search-refine"
                role="group"
                aria-labelledby="bs-sort-title"
              >
                <span id="bs-sort-title" className="bs-refine__label">
                  {t("search.sort")}
                </span>
                {SORTS.map((so) => (
                  <button
                    key={so}
                    type="button"
                    className="bs-chip"
                    aria-pressed={shownSort === so}
                    data-bs-focus={`sort:${so}`}
                    onClick={() => setSort(so)}
                  >
                    {t(`search.sort.${so}`)}
                  </button>
                ))}
                {groups.length > 0 && (
                  <button
                    type="button"
                    className="bs-chip bs-refine__filters"
                    data-bs-focus="search:filters"
                    aria-haspopup="dialog"
                    onClick={() => setFiltersOpen(true)}
                  >
                    {t("big.search.filters")}
                    {inUse.length > 0 && <span className="bs-refine__n">{n(inUse.length)}</span>}
                  </button>
                )}
              </div>
              {inUse.length > 0 && (
                <div
                  className="bs-chips bs-refine__active"
                  data-bs-group="search-active"
                  role="group"
                  aria-label={t("search.filters.active")}
                >
                  {inUse.map(({ facet, value }) => {
                    const name = chipName(facet, value);
                    return (
                      <button
                        key={`${facet}:${value}`}
                        type="button"
                        className="bs-chip bs-chip--remove"
                        data-bs-focus={`rm:${facet}:${value}`}
                        aria-label={t("search.filters.remove", { name })}
                        onClick={() => remove(facet, value)}
                      >
                        <bdi>{name}</bdi>
                        <span aria-hidden="true"> ✕</span>
                      </button>
                    );
                  })}
                  <button
                    type="button"
                    className="bs-chip"
                    data-bs-focus="search:clear"
                    onClick={clearFilters}
                  >
                    {t("search.filters.clear")}
                  </button>
                </div>
              )}
            </div>
          )}
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
                          <Ltr className="bs-line__at">{clock(p.start_sec)}</Ltr>
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
      {filtersOpen && (
        <Panel
          title={t("search.facets")}
          onClose={() => setFiltersOpen(false)}
          testId="big-filters"
        >
          {inUse.length > 0 && (
            <button
              type="button"
              className="bs-option"
              data-bs-focus="f:clear"
              onClick={() => setAsk((a) => ({ ...a, sel: noSel() }))}
            >
              {t("search.filters.clear")}
            </button>
          )}
          {groups.map(({ facet, buckets }) => (
            <div key={facet} className="bs-facet">
              <h3 id={`bs-facet-${facet}`} className="bs-panel__heading">
                {t(`search.facet.${facet}`)}
              </h3>
              <div
                className="bs-chips bs-facet__chips"
                data-bs-group={`facet:${facet}`}
                role="group"
                aria-labelledby={`bs-facet-${facet}`}
              >
                {buckets.map((b) => (
                  <button
                    key={b.value}
                    type="button"
                    className="bs-chip"
                    aria-pressed={selHas(ask.sel, facet, b.value)}
                    data-bs-focus={`f:${facet}:${b.value}`}
                    onClick={() => toggle(facet, b.value)}
                  >
                    <bdi>
                      {bucketName(facet, b.value, b.label || labels[`${facet}:${b.value}`])}
                    </bdi>
                    {b.count >= 0 && (
                      <>
                        <span className="bs-facet__count" aria-hidden="true">
                          {n(b.count)}
                        </span>
                        <span className="bs-sr">
                          {t("search.facet.count", { count: n(b.count) })}
                        </span>
                      </>
                    )}
                  </button>
                ))}
              </div>
            </div>
          ))}
        </Panel>
      )}
    </Screen>
  );
}
