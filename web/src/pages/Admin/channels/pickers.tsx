// Small shared controls of the channel builder: weekday chips and the
// search picker over GET /api/channels/sources (series, collections, plan
// lanes, single videos) with each hit's episode count and running time.
import { useEffect, useId, useState } from "react";
import { useI18n } from "../../../lib/i18n";
import { channelsApi, type SourceHit, type SourceKind } from "../../../lib/channels";
import { DAYS, hoursMinutes, type Day } from "./schedule";

export function useDuration() {
  const { t, n } = useI18n();
  return (ms: number) => {
    const { h, m } = hoursMinutes(ms);
    if (h && m) return t("channels.dur.hm", { h: n(h), m: n(m) });
    if (h) return t("channels.dur.h", { h: n(h) });
    return t("channels.dur.m", { m: n(m) });
  };
}

/** Mon..Sun toggle chips (a `group` of `aria-pressed` buttons). */
export function DayChips({
  value,
  onChange,
  label,
}: {
  value: Day[];
  onChange: (days: Day[]) => void;
  label: string;
}) {
  const { t } = useI18n();
  return (
    <div className="mkt-cb-days" role="group" aria-label={label}>
      {DAYS.map((d) => {
        const on = value.includes(d);
        return (
          <button
            key={d}
            type="button"
            className={`mkt-cb-day${on ? " mkt-cb-day--on" : ""}`}
            aria-pressed={on}
            title={t(`channels.day.long.${d}`)}
            onClick={() => {
              const next = on ? value.filter((x) => x !== d) : [...value, d];
              onChange(DAYS.filter((x) => next.includes(x)));
            }}
          >
            {t(`channels.day.${d}`)}
          </button>
        );
      })}
    </div>
  );
}

/** Debounces a changing value (search boxes). */
export function useDebounced<T>(value: T, ms = 250): T {
  const [v, setV] = useState(value);
  useEffect(() => {
    const id = setTimeout(() => setV(value), ms);
    return () => clearTimeout(id);
  }, [value, ms]);
  return v;
}

/**
 * A search box with the matching sources of one kind below it. Picking a
 * hit calls onPick; the list is a `listbox` of buttons, keyboard reachable.
 */
export function SourceSearch({
  kind,
  onPick,
  selectedId,
  label,
  autoFocus,
}: {
  kind: SourceKind;
  onPick: (hit: SourceHit) => void;
  selectedId?: string;
  label: string;
  autoFocus?: boolean;
}) {
  const { t, n } = useI18n();
  const dur = useDuration();
  const id = useId();
  const [q, setQ] = useState("");
  const query = useDebounced(q);
  const [hits, setHits] = useState<SourceHit[] | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let alive = true;
    setHits(null);
    setFailed(false);
    channelsApi
      .sources(kind, query)
      .then((r) => alive && setHits(r.items ?? []))
      .catch(() => {
        if (alive) {
          setHits([]);
          setFailed(true);
        }
      });
    return () => {
      alive = false;
    };
  }, [kind, query]);

  return (
    <div className="mkt-cb-search">
      <label htmlFor={id} className="mkt-cb-label">
        {label}
      </label>
      <input
        id={id}
        type="search"
        className="mkt-cb-input"
        value={q}
        autoFocus={autoFocus}
        placeholder={t("channels.source.searchPlaceholder")}
        onChange={(e) => setQ(e.target.value)}
      />
      {hits === null ? (
        <p className="mkt-muted">{t("common.loading")}</p>
      ) : hits.length === 0 ? (
        <p className="mkt-muted">
          {failed
            ? t("common.error")
            : query
              ? t("channels.source.none", { q: query })
              : t("channels.source.nothing")}
        </p>
      ) : (
        <ul className="mkt-cb-hits" role="listbox" aria-label={label}>
          {hits.map((h) => (
            <li key={`${h.kind}:${h.id}`} role="option" aria-selected={h.id === selectedId}>
              <button
                type="button"
                className={`mkt-cb-hit${h.id === selectedId ? " mkt-cb-hit--on" : ""}`}
                onClick={() => onPick(h)}
              >
                <span className="mkt-cb-hit__name">{h.name}</span>
                <span className="mkt-cb-hit__meta">
                  {kind === "video"
                    ? dur(h.duration_ms)
                    : t("channels.source.count", { n: n(h.count), dur: dur(h.duration_ms) })}
                </span>
                {h.detail && h.detail.length > 0 && (
                  <span className="mkt-cb-hit__detail">{h.detail.join(" · ")}</span>
                )}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
