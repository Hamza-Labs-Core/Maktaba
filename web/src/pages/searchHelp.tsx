// Story 49.10: the Search page's help (story 49.8's API, specs/epics/49-search §5.7).
//
//   SuggestBox    the search box with its suggestions as you type: an ARIA 1.2 combobox
//                 whose listbox shows each suggestion's kind (an earlier search, a word,
//                 a video title, a speaker); up/down, Enter and Escape, mouse and touch.
//   DidYouMean    "Did you mean <corrected>?" above the results, one tap away.
//   FacetPanel    counts by library, language, speaker, year and duration band; a bucket
//                 narrows the search, several combine.
//   ActiveFilters the filters in use, each removable, and Clear all.
//   SortSelect    relevance, newest, most matches.
import { type KeyboardEvent, type ReactNode, useEffect, useId, useState } from "react";
import { useI18n } from "../lib/i18n";
import {
  type Bucket,
  FACETS,
  type FacetName,
  type FacetSel,
  type Facets,
  SORTS,
  type Sort,
  listOf,
  selCount,
  selHas,
} from "./searchShared";

export type SuggestKind = "history" | "term" | "title" | "speaker";
const KINDS: readonly string[] = ["history", "term", "title", "speaker"];

/** One suggestion: its text, its kind (none from a home before story 49.8) and a title's video. */
export interface SuggestItem {
  text: string;
  kind?: SuggestKind;
  video_id?: string;
}

/** The suggestions of a GET /api/search/suggest answer: `items`, else the plain texts. */
export function suggestItems(r: unknown): SuggestItem[] {
  const a = (r ?? {}) as { items?: unknown; suggestions?: unknown };
  const out: SuggestItem[] = [];
  if (Array.isArray(a.items) && a.items.length > 0) {
    for (const it of a.items as Array<Record<string, unknown> | null>) {
      if (!it || typeof it.text !== "string" || !it.text.trim()) continue;
      const kind = KINDS.includes(it.kind as string) ? (it.kind as SuggestKind) : undefined;
      const videoId = typeof it.video_id === "string" && it.video_id ? it.video_id : undefined;
      out.push({
        text: it.text,
        ...(kind ? { kind } : {}),
        ...(videoId ? { video_id: videoId } : {}),
      });
    }
  } else if (Array.isArray(a.suggestions)) {
    for (const s of a.suggestions) if (typeof s === "string" && s.trim()) out.push({ text: s });
  }
  // a title opens its video, so it needs one; the same text and kind once
  const seen = new Set<string>();
  return out.filter((it) => {
    if (it.kind === "title" && !it.video_id) return false;
    const k = `${it.kind ?? ""}\n${it.text}\n${it.video_id ?? ""}`;
    if (seen.has(k)) return false;
    seen.add(k);
    return true;
  });
}

const ICON_PATHS: Record<SuggestKind | "plain", ReactNode> = {
  history: (
    <>
      <circle cx="12" cy="12" r="9" />
      <path d="M12 7v5l3 2" />
    </>
  ),
  term: (
    <>
      <circle cx="11" cy="11" r="7" />
      <path d="M20 20l-4-4" />
    </>
  ),
  title: (
    <>
      <rect x="3" y="5" width="18" height="14" rx="2" />
      <path d="M10 9l5 3-5 3z" />
    </>
  ),
  speaker: (
    <>
      <circle cx="12" cy="8" r="4" />
      <path d="M4 20c0-4 4-6 8-6s8 2 8 6" />
    </>
  ),
  plain: (
    <>
      <circle cx="11" cy="11" r="7" />
      <path d="M20 20l-4-4" />
    </>
  ),
};

function KindIcon({ kind }: { kind?: SuggestKind }) {
  return (
    <svg
      className="mkt-suggest__icon"
      viewBox="0 0 24 24"
      width={16}
      height={16}
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      {ICON_PATHS[kind ?? "plain"]}
    </svg>
  );
}

/**
 * The search box and its suggestions. The list shows while the box has focus and there
 * are suggestions; Escape, a choice, a submit or leaving the box closes it until the
 * next keystroke. Enter on a highlighted suggestion chooses it, else submits the form.
 */
export function SuggestBox({
  value,
  onChange,
  items,
  onChoose,
  onDismiss,
  label,
  placeholder,
}: {
  value: string;
  onChange: (value: string) => void;
  items: SuggestItem[];
  onChoose: (item: SuggestItem) => void;
  /** The list closed without a choice (Escape, leaving the box). */
  onDismiss?: () => void;
  label: string;
  placeholder: string;
}) {
  const { t } = useI18n();
  const id = useId();
  const listId = `${id}-list`;
  const optId = (i: number) => `${id}-opt-${i}`;
  const [focused, setFocused] = useState(false);
  const [closed, setClosed] = useState(false);
  const [active, setActive] = useState(-1);
  // a new list starts with nothing highlighted, and opens again
  useEffect(() => {
    setActive(-1);
    setClosed(false);
  }, [items]);
  const open = focused && !closed && items.length > 0;
  // the highlighted option stays in sight in a list taller than its box
  useEffect(() => {
    if (open && active >= 0)
      document.getElementById(optId(active))?.scrollIntoView?.({ block: "nearest" });
  }, [open, active]);

  function choose(it: SuggestItem) {
    setClosed(true);
    setActive(-1);
    onChoose(it);
  }

  function close() {
    setClosed(true);
    setActive(-1);
    onDismiss?.();
  }

  function onKeyDown(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      if (items.length === 0) return;
      e.preventDefault();
      const down = e.key === "ArrowDown";
      if (!open) {
        setClosed(false);
        setActive(down ? 0 : items.length - 1);
        return;
      }
      setActive((a) => (down ? (a + 1) % items.length : a <= 0 ? items.length - 1 : a - 1));
    } else if (e.key === "Enter") {
      if (open && active >= 0 && active < items.length) {
        e.preventDefault();
        choose(items[active]!);
      } else setClosed(true);
    } else if (e.key === "Escape") {
      if (open) {
        // the list closes; the text stays (a type=search box would clear it)
        e.preventDefault();
        close();
      }
    } else if (e.key === "Tab") {
      if (open) close();
    }
  }

  return (
    <div className="mkt-suggest">
      <input
        type="search"
        role="combobox"
        value={value}
        onChange={(e) => {
          setClosed(false);
          onChange(e.target.value);
        }}
        onKeyDown={onKeyDown}
        onFocus={() => setFocused(true)}
        onBlur={() => {
          // leaving the box closes the list until the next keystroke
          setFocused(false);
          setClosed(true);
          setActive(-1);
        }}
        placeholder={placeholder}
        aria-label={label}
        aria-autocomplete="list"
        aria-expanded={open}
        aria-controls={listId}
        aria-activedescendant={open && active >= 0 ? optId(active) : undefined}
        autoComplete="off"
        autoFocus
      />
      <ul
        id={listId}
        role="listbox"
        aria-label={t("search.suggest.label")}
        className="mkt-suggest__list"
        hidden={!open}
      >
        {open &&
          items.map((it, i) => (
            <li
              key={`${it.kind ?? ""}:${it.video_id ?? ""}:${it.text}`}
              id={optId(i)}
              role="option"
              aria-selected={i === active}
              className="mkt-suggest__item"
              data-kind={it.kind ?? "plain"}
              // the box keeps focus: a press on the list must not blur it first
              onMouseDown={(e) => e.preventDefault()}
              onMouseMove={() => setActive(i)}
              onClick={() => choose(it)}
            >
              <KindIcon kind={it.kind} />
              <span className="mkt-suggest__text" dir="auto">
                {it.text}
              </span>
              {it.kind && (
                <span className="mkt-suggest__kind">{t(`search.suggest.kind.${it.kind}`)}</span>
              )}
            </li>
          ))}
      </ul>
    </div>
  );
}

/** "Did you mean <corrected>?", the corrected query a button that runs it. */
export function DidYouMean({ text, onRun }: { text: string; onRun: (q: string) => void }) {
  const { t } = useI18n();
  // the template's {q} is where the button goes, so each language orders it its own way
  const [before, after = ""] = t("search.dym").split("{q}");
  return (
    <p className="mkt-search__dym" role="status" data-testid="did-you-mean">
      {before}
      <button type="button" className="mkt-link-btn mkt-search__dym-q" onClick={() => onRun(text)}>
        <bdi>{text}</bdi>
      </button>
      {after}
    </p>
  );
}

/** The name a bucket is shown by, in the page's language. */
export function useBucketLabel() {
  const { t, n, locale } = useI18n();
  return (facet: FacetName, value: string, label?: string): string => {
    switch (facet) {
      case "library":
        return label?.trim() || value;
      case "language":
        return languageName(value, locale);
      case "year":
        return /^\d+$/.test(value) ? n(Number(value)).replace(/[٬,]/g, "") : value;
      case "duration":
        return durationLabel(value, t, n);
      default:
        return value;
    }
  };
}

function languageName(code: string, locale: string): string {
  try {
    const name = new Intl.DisplayNames([locale === "ar" ? "ar" : "en"], { type: "language" }).of(
      code
    );
    return name && name !== code ? name : code;
  } catch {
    return code;
  }
}

function durationLabel(
  band: string,
  t: (key: string, vars?: Record<string, string | number>) => string,
  n: (v: number) => string
): string {
  switch (band) {
    case "under_5m":
      return t("search.duration.under_5m", { a: n(5) });
    case "5m_20m":
      return t("search.duration.5m_20m", { a: n(5), b: n(20) });
    case "20m_60m":
      return t("search.duration.20m_60m", { a: n(20), b: n(60) });
    case "over_60m":
      return t("search.duration.over_60m");
    default:
      return band;
  }
}

/**
 * The facets of a search, a group of toggle chips each, with each bucket's count. A chosen
 * bucket that the narrowed facets no longer list is still shown, pressed, so it can be
 * taken off where it was put on.
 */
export function FacetPanel({
  facets,
  sel,
  onToggle,
  labels,
}: {
  facets: Facets;
  sel: FacetSel;
  onToggle: (facet: FacetName, value: string) => void;
  /** Names seen for a bucket (a library's), for one the facets no longer list. */
  labels: Record<string, string>;
}) {
  const { t, n } = useI18n();
  const name = useBucketLabel();
  const baseId = useId();
  const groups = FACETS.map((facet) => {
    const buckets: Bucket[] = listOf(facets[facet]).filter(
      (b) => b && typeof b.value === "string" && b.value !== ""
    );
    const chosen =
      facet === "year" || facet === "duration" ? (sel[facet] ? [sel[facet]!] : []) : sel[facet];
    for (const v of chosen)
      if (!buckets.some((b) => b.value === v)) buckets.unshift({ value: v, count: -1 });
    return { facet, buckets };
  }).filter((g) => g.buckets.length > 0);
  if (groups.length === 0) return null;
  return (
    <section className="mkt-facets" aria-label={t("search.facets")} data-testid="facets">
      {groups.map(({ facet, buckets }) => {
        const headId = `${baseId}-${facet}`;
        return (
          <div key={facet} className="mkt-facets__group" role="group" aria-labelledby={headId}>
            <span id={headId} className="mkt-facets__name">
              {t(`search.facet.${facet}`)}
            </span>
            <ul className="mkt-facets__chips">
              {buckets.map((b) => {
                const pressed = selHas(sel, facet, b.value);
                const text = name(facet, b.value, b.label || labels[`${facet}:${b.value}`]);
                return (
                  <li key={b.value}>
                    <button
                      type="button"
                      className="mkt-facet"
                      aria-pressed={pressed}
                      onClick={() => onToggle(facet, b.value)}
                    >
                      <span className="mkt-facet__label" dir="auto">
                        {text}
                      </span>
                      {b.count >= 0 && (
                        <>
                          <span className="mkt-facet__count" aria-hidden="true">
                            {n(b.count)}
                          </span>
                          <span className="mkt-facet__sr">
                            {t("search.facet.count", { count: n(b.count) })}
                          </span>
                        </>
                      )}
                    </button>
                  </li>
                );
              })}
            </ul>
          </div>
        );
      })}
    </section>
  );
}

/** The filters in use, each with its remove button, and Clear all. */
export function ActiveFilters({
  sel,
  labels,
  onRemove,
  onClear,
}: {
  sel: FacetSel;
  labels: Record<string, string>;
  onRemove: (facet: FacetName, value: string) => void;
  onClear: () => void;
}) {
  const { t } = useI18n();
  const name = useBucketLabel();
  if (selCount(sel) === 0) return null;
  const chips: Array<{ facet: FacetName; value: string }> = [];
  for (const facet of FACETS) {
    if (facet === "year" || facet === "duration") {
      const v = sel[facet];
      if (v) chips.push({ facet, value: v });
    } else for (const value of sel[facet]) chips.push({ facet, value });
  }
  return (
    <div className="mkt-active-filters" role="group" aria-label={t("search.filters.active")}>
      <ul className="mkt-active-filters__list">
        {chips.map(({ facet, value }) => {
          const text = `${t(`search.facet.${facet}`)}: ${name(facet, value, labels[`${facet}:${value}`])}`;
          return (
            <li key={`${facet}:${value}`}>
              <span className="mkt-active-filter" dir="auto">
                <span>{text}</span>
                <button
                  type="button"
                  className="mkt-active-filter__remove"
                  aria-label={t("search.filters.remove", { name: text })}
                  onClick={() => onRemove(facet, value)}
                >
                  <svg
                    viewBox="0 0 16 16"
                    width={12}
                    height={12}
                    aria-hidden="true"
                    focusable="false"
                  >
                    <path
                      d="M4 4l8 8M12 4l-8 8"
                      stroke="currentColor"
                      strokeWidth="2"
                      strokeLinecap="round"
                    />
                  </svg>
                </button>
              </span>
            </li>
          );
        })}
      </ul>
      <button type="button" className="mkt-link-btn" onClick={onClear}>
        {t("search.filters.clear")}
      </button>
    </div>
  );
}

/** The order of the results. */
export function SortSelect({ value, onChange }: { value: Sort; onChange: (s: Sort) => void }) {
  const { t } = useI18n();
  const id = useId();
  return (
    <span className="mkt-search__sort">
      <label htmlFor={id}>{t("search.sort")}</label>
      <select id={id} value={value} onChange={(e) => onChange(e.target.value as Sort)}>
        {SORTS.map((s) => (
          <option key={s} value={s}>
            {t(`search.sort.${s}`)}
          </option>
        ))}
      </select>
    </span>
  );
}
