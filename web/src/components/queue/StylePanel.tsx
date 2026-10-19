// "Pin a show's style": a season's production style (cinematic / produced /
// studio talk / still picture) is rated automatically from sampled frames; a
// pin overrides the rating. Choosing a style saves it and queues the season's
// rate_style job, whose result flows on to the channel plan (PUT
// /api/seasons/{id}/style, /api/series/{id}/style).
import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { PRODUCTION_STYLES, queueApi } from "../../lib/queueApi";
import type { LibraryQueueRow, ProductionStyle, SeasonStyle } from "../../lib/queueApi";
import { useAction } from "./useAction";

interface SeriesGroup {
  id: string;
  name: string;
  library: string | null;
  seasons: SeasonStyle[];
}

function groupBySeries(items: SeasonStyle[]): SeriesGroup[] {
  const out = new Map<string, SeriesGroup>();
  for (const s of items) {
    const g = out.get(s.series_id) ?? {
      id: s.series_id,
      name: s.series,
      library: s.library,
      seasons: [],
    };
    g.seasons.push(s);
    out.set(s.series_id, g);
  }
  return [...out.values()];
}

export function StylePanel({
  libraries,
  canEdit,
}: {
  libraries: LibraryQueueRow[];
  canEdit: boolean;
}) {
  const { t, n } = useI18n();
  const { busy, error, run } = useAction();
  const [text, setText] = useState("");
  const [library, setLibrary] = useState("");
  const [pinnedOnly, setPinnedOnly] = useState(false);
  const [items, setItems] = useState<SeasonStyle[] | null>(null);
  const [saved, setSaved] = useState<string | null>(null);
  const groups = useMemo(() => groupBySeries(items ?? []), [items]);

  async function find() {
    setSaved(null);
    const r = await run("find", () =>
      queueApi.seasonStyles({
        libraryId: library || undefined,
        q: text.trim() || undefined,
        pinned: pinnedOnly,
        limit: 100,
      })
    );
    if (r) setItems(r.items ?? []);
  }

  const label = (s: ProductionStyle) => t(`queue.style.${s}`);
  const apply = (seasonIds: Set<string>, style: ProductionStyle | null) =>
    setItems((prev) =>
      (prev ?? []).map((s) => (seasonIds.has(s.season_id) ? { ...s, override_style: style } : s))
    );

  async function pinSeason(s: SeasonStyle, value: string) {
    const style = (value || null) as ProductionStyle | null;
    const r = await run(s.season_id, () => queueApi.putSeasonStyle(s.season_id, style));
    if (r) {
      apply(new Set([s.season_id]), style);
      setSaved(
        style
          ? t("queue.style.saved", { name: s.series, style: label(style) })
          : t("queue.style.cleared", { name: s.series })
      );
    }
  }

  async function pinSeries(g: SeriesGroup, value: string) {
    if (value === "keep") return;
    const style = (value || null) as ProductionStyle | null;
    const r = await run(g.id, () => queueApi.putSeriesStyle(g.id, style));
    if (r) {
      apply(new Set(g.seasons.map((s) => s.season_id)), style);
      setSaved(
        style
          ? t("queue.style.saved", { name: g.name, style: label(style) })
          : t("queue.style.cleared", { name: g.name })
      );
    }
  }

  const options = (
    <>
      <option value="">{t("queue.style.automatic")}</option>
      {PRODUCTION_STYLES.map((s) => (
        <option key={s} value={s}>
          {label(s)}
        </option>
      ))}
    </>
  );

  return (
    <section className="mkt-q-style" aria-labelledby="mkt-q-style-h">
      <h2 className="mkt-q-section-title" id="mkt-q-style-h">
        {t("queue.style.title")}
      </h2>
      <p className="mkt-q-muted">{t("queue.style.intro")}</p>
      <form
        className="mkt-q-row"
        role="search"
        onSubmit={(e) => {
          e.preventDefault();
          void find();
        }}
      >
        <select
          value={library}
          aria-label={t("queue.explain.library")}
          onChange={(e) => setLibrary(e.target.value)}
        >
          <option value="">{t("queue.explain.anyLibrary")}</option>
          {libraries.map((l) => (
            <option key={l.library_id} value={l.library_id}>
              {l.name}
            </option>
          ))}
        </select>
        <input
          type="search"
          dir="auto"
          value={text}
          placeholder={t("queue.style.series")}
          aria-label={t("queue.style.series")}
          onChange={(e) => setText(e.target.value)}
        />
        <label className="mkt-q-check">
          <input
            type="checkbox"
            checked={pinnedOnly}
            onChange={(e) => setPinnedOnly(e.target.checked)}
          />
          <span>{t("queue.style.pinnedOnly")}</span>
        </label>
        <Button type="submit" loading={busy === "find"}>
          {t("queue.style.find")}
        </Button>
      </form>
      {error && (
        <p role="alert" className="mkt-q-error">
          {error}
        </p>
      )}
      {saved && (
        <p role="status" className="mkt-q-note">
          {saved} {t("queue.style.requeued")}
        </p>
      )}
      {!canEdit && <p className="mkt-q-note">{t("queue.style.readOnly")}</p>}
      {items && groups.length === 0 && <p className="mkt-q-empty">{t("queue.style.none")}</p>}
      <ul className="mkt-q-style__list">
        {groups.map((g) => (
          <li key={g.id} className="mkt-q-style__series">
            <div className="mkt-q-style__head">
              <strong dir="auto">
                <Link to={`/queue/items/series/${encodeURIComponent(g.id)}`}>
                  {bidiIsolate(g.name)}
                </Link>
              </strong>
              {g.library && <span className="mkt-q-muted">{bidiIsolate(g.library)}</span>}
              {canEdit && g.seasons.length > 1 && (
                <select
                  value="keep"
                  disabled={busy === g.id}
                  aria-label={t("queue.style.allSeasons", { name: g.name })}
                  onChange={(e) => void pinSeries(g, e.target.value)}
                >
                  <option value="keep">
                    {t("queue.style.allSeasonsPick", { n: n(g.seasons.length) })}
                  </option>
                  {options}
                </select>
              )}
            </div>
            <ul>
              {g.seasons.map((s) => (
                <li key={s.season_id} className="mkt-q-style__season">
                  <span dir="auto">
                    {s.name === s.series ? `S${n(s.season)}` : bidiIsolate(s.name)}
                  </span>
                  <span className="mkt-q-muted">
                    {s.style
                      ? t("queue.style.rated", {
                          style: label(s.style),
                          pct: n(Math.round((s.confidence ?? 0) * 100)),
                        })
                      : t("queue.style.unrated")}
                  </span>
                  <select
                    value={s.override_style ?? ""}
                    disabled={!canEdit || busy === s.season_id}
                    aria-label={t("queue.style.pin", { name: `${s.series} S${s.season}` })}
                    onChange={(e) => void pinSeason(s, e.target.value)}
                  >
                    {options}
                  </select>
                  {s.override_style && (
                    <span className="mkt-q-pill mkt-q-pill--attention">
                      {t("queue.style.pinned")}
                    </span>
                  )}
                </li>
              ))}
            </ul>
          </li>
        ))}
      </ul>
    </section>
  );
}
