// Settings → Plugins → Browse (story 54.18 §1), admin only: the store's catalog as this home
// keeps it (54.17), filtered on the server to this server's version, plugin API and platform,
// with a switch for plugins that need a newer Maktaba. Search folds Arabic letter forms and
// diacritics (the help's normaliser). A plugin's page shows its words, its capabilities in
// Maktaba's words, the review badge, the rating, its versions and Install; rate and report go
// to the store, signed by this home.
import { useEffect, useMemo, useState } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { Select } from "@ds/components/Select/Select";
import { Toggle } from "@ds/components/Choice/Toggle";
import { fold } from "../../help/search";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { pluginsApi, type CatalogEntry, type CatalogView } from "../../lib/pluginsApi";
import { capabilityTitle, refusalWords } from "./PluginsSection";

type Sort = "rating" | "installs" | "newest";

const word = (m: Record<string, string> | undefined, locale: string) => m?.[locale] || m?.en || "";

/** The entries matching a query (every word, Arabic-folded) in a category, sorted. */
export function filterEntries(
  list: CatalogEntry[],
  query: string,
  category: string,
  sort: Sort
): CatalogEntry[] {
  const terms = fold(query).split(/\s+/).filter(Boolean);
  const out = list.filter((p) => {
    if (category && p.category !== category) return false;
    const hay = fold(
      [
        ...Object.values(p.names ?? {}),
        ...Object.values(p.summaries ?? {}),
        ...(p.tags ?? []),
        p.id,
      ].join(" ")
    );
    return terms.every((t) => hay.includes(t));
  });
  const newest = (p: CatalogEntry) => p.newest?.released ?? "";
  return out.sort((a, b) =>
    sort === "rating"
      ? b.rating_avg_x100 - a.rating_avg_x100
      : sort === "installs"
        ? b.installs - a.installs
        : newest(b).localeCompare(newest(a))
  );
}

export function Browse({ onInstalled }: { onInstalled: (id: string) => void }) {
  const { t, locale, n } = useI18n();
  const [all, setAll] = useState(false);
  const [view, setView] = useState<CatalogView | null>(null);
  const [error, setError] = useState("");
  const [query, setQuery] = useState("");
  const [category, setCategory] = useState("");
  const [sort, setSort] = useState<Sort>("rating");
  const [open, setOpen] = useState<CatalogEntry | null>(null);

  useEffect(() => {
    pluginsApi.catalog(all).then(setView, (e) => setError(refusalWords(t, e)));
  }, [all, t]);

  const shown = useMemo(
    () => (view ? filterEntries(view.plugins, query, category, sort) : []),
    [view, query, category, sort]
  );
  const categories = useMemo(
    () => [...new Set((view?.plugins ?? []).map((p) => p.category).filter(Boolean))].sort(),
    [view]
  );

  if (error) return <p role="alert">{error}</p>;
  if (!view) return <p>{t("plugins.page.loading")}</p>;
  if (open)
    return (
      <StorePlugin
        entry={open}
        view={view}
        onBack={() => setOpen(null)}
        onInstalled={onInstalled}
      />
    );
  if (view.opening) return <p role="status">{t("plugins.browse.opening")}</p>;
  return (
    <section aria-labelledby="pl-browse" className="mkt-plugins__browse">
      <h3 id="pl-browse">{t("plugins.browse.title")}</h3>
      {view.trust.catalog_expired && <p role="alert">{t("plugins.browse.expired")}</p>}
      <div className="mkt-plugins__browse-bar">
        <Input
          label={t("plugins.browse.search")}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
        <Select
          label={t("plugins.browse.category")}
          value={category}
          onChange={(e) => setCategory(e.target.value)}
        >
          <option value="">{t("plugins.browse.all_categories")}</option>
          {categories.map((c) => (
            <option key={c} value={c}>
              {t(`plugins.category.${c}`) === `plugins.category.${c}`
                ? c
                : t(`plugins.category.${c}`)}
            </option>
          ))}
        </Select>
        <Select
          label={t("plugins.browse.sort")}
          value={sort}
          onChange={(e) => setSort(e.target.value as Sort)}
        >
          <option value="rating">{t("plugins.browse.sort.rating")}</option>
          <option value="installs">{t("plugins.browse.sort.installs")}</option>
          <option value="newest">{t("plugins.browse.sort.newest")}</option>
        </Select>
        <Toggle
          label={t("plugins.browse.show_newer")}
          checked={all}
          onChange={(e) => setAll(e.target.checked)}
        />
      </div>
      <ul className="mkt-plugins__cards">
        {shown.map((p) => (
          <li key={p.id}>
            <article className="mkt-plugins__card" aria-label={word(p.names, locale)}>
              <h4 className="mkt-plugins__name">{bidiIsolate(word(p.names, locale))}</h4>
              <p>{bidiIsolate(word(p.summaries, locale))}</p>
              <p className="mkt-plugins__hint">
                {bidiIsolate(p.developer)} ·{" "}
                {p.rating_count >= 5
                  ? t("plugins.browse.rating", {
                      avg: n(p.rating_avg_x100 / 100),
                      count: n(p.rating_count),
                    })
                  : t("plugins.browse.few_ratings")}
              </p>
              {!p.newest && <Badge tone="warn">{t("plugins.browse.needs_newer")}</Badge>}
              {p.installed && (
                <Badge tone="neutral">
                  {t("plugins.browse.installed", { version: p.installed })}
                </Badge>
              )}
              <Button variant="ghost" onClick={() => setOpen(p)}>
                {t("plugins.action.open")}
              </Button>
            </article>
          </li>
        ))}
      </ul>
    </section>
  );
}

function StorePlugin({
  entry: p,
  view,
  onBack,
  onInstalled,
}: {
  entry: CatalogEntry;
  view: CatalogView;
  onBack: () => void;
  onInstalled: (id: string) => void;
}) {
  const { t, locale, n } = useI18n();
  const [note, setNote] = useState("");
  const [error, setError] = useState("");
  const [stars, setStars] = useState(0);
  const [text, setText] = useState("");
  const [reason, setReason] = useState("");
  const [details, setDetails] = useState("");
  const v = p.newest;
  const machine = locale === "ar" && (p.machine_translated ?? []).includes("ar");
  const canInstall =
    !!v &&
    view.trust.store_installs_ok &&
    !view.trust.catalog_expired &&
    p.withdrawn?.action !== "disable";
  const run = async (f: () => Promise<unknown>, done: string) => {
    try {
      await f();
      setNote(done);
      setError("");
    } catch (e) {
      setError(refusalWords(t, e));
    }
  };
  return (
    <section className="mkt-plugins__page">
      <Button variant="link" onClick={onBack}>
        {t("plugins.browse.back")}
      </Button>
      <h3>{bidiIsolate(word(p.names, locale))}</h3>
      <p>
        {bidiIsolate(word(p.summaries, locale))}{" "}
        {machine && <Badge tone="neutral">{t("plugins.browse.machine_translated")}</Badge>}
      </p>
      <p className="mkt-plugins__desc">
        {bidiIsolate(word((p.extra?.descriptions as Record<string, string>) ?? {}, locale))}
      </p>
      <p>
        {bidiIsolate(p.developer)}{" "}
        {v && (
          <Badge tone="success">
            {t(
              v.review_level === "reviewed" ? "plugins.badge.reviewed" : "plugins.badge.automated"
            )}
          </Badge>
        )}{" "}
        {p.rating_count >= 5
          ? t("plugins.browse.rating", {
              avg: n(p.rating_avg_x100 / 100),
              count: n(p.rating_count),
            })
          : t("plugins.browse.few_ratings")}
      </p>
      {error && <p role="alert">{error}</p>}
      {note && <p role="status">{note}</p>}
      <h4>{t("plugins.section.permissions")}</h4>
      <ul>
        {(v?.capabilities ?? []).map((c) => (
          <li key={c}>{capabilityTitle(t, c).title}</li>
        ))}
      </ul>
      <h4>{t("plugins.section.versions")}</h4>
      <ul>
        {p.versions.map((x) => (
          <li key={x.version} dir="ltr">
            {x.version} · {x.maktaba} · {n(Math.ceil(x.size / 1024))} KB
          </li>
        ))}
      </ul>
      {view.trust.catalog_expired && <p role="alert">{t("plugins.browse.expired")}</p>}
      <Button
        disabled={!canInstall}
        onClick={() =>
          void run(async () => {
            const r = await pluginsApi.installFromStore(p.id, v!.version);
            onInstalled(r.result.plugin_id);
          }, t("plugins.browse.installed_now"))
        }
      >
        {p.installed
          ? t("plugins.browse.update", { version: v?.version ?? "" })
          : t("plugins.browse.install")}
      </Button>

      <h4>{t("plugins.browse.rate")}</h4>
      <div role="radiogroup" aria-label={t("plugins.browse.rate")}>
        {[1, 2, 3, 4, 5].map((s) => (
          <button
            key={s}
            type="button"
            role="radio"
            aria-checked={stars === s}
            aria-label={t("plugins.browse.stars", { n: s })}
            onClick={() => setStars(s)}
          >
            {s <= stars ? "★" : "☆"}
          </button>
        ))}
      </div>
      <Input
        label={t("plugins.browse.review")}
        value={text}
        onChange={(e) => setText(e.target.value)}
        maxLength={2000}
      />
      <Button
        disabled={!stars}
        onClick={() =>
          void run(() => pluginsApi.rate(p.id, stars, text), t("plugins.browse.rated"))
        }
      >
        {t("plugins.browse.send_rating")}
      </Button>

      <h4>{t("plugins.browse.report")}</h4>
      <Select
        label={t("plugins.browse.report_reason")}
        value={reason}
        onChange={(e) => setReason(e.target.value)}
      >
        <option value="" />
        {[
          "malware",
          "data_misuse",
          "does_not_work",
          "misleading",
          "offensive",
          "copyright",
          "other",
        ].map((r) => (
          <option key={r} value={r}>
            {t(`plugins.report.${r}`)}
          </option>
        ))}
      </Select>
      <Input
        label={t("plugins.browse.report_details")}
        value={details}
        onChange={(e) => setDetails(e.target.value)}
      />
      <Button
        variant="ghost"
        disabled={!reason}
        onClick={() =>
          void run(() => pluginsApi.report(p.id, reason, details), t("plugins.browse.reported"))
        }
      >
        {t("plugins.browse.send_report")}
      </Button>
    </section>
  );
}
