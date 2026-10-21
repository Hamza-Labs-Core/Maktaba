// The help area (Epic 41): /help lists every topic by category with a
// search box (?q= keeps the search in the URL), /help/<topic> shows a topic,
// /help/<topic>#<anchor> scrolls to a section and highlights it.
import { useEffect, useMemo, useState } from "react";
import { Link, useLocation, useParams, useSearchParams } from "react-router-dom";
import { useI18n } from "../lib/i18n";
import { loadAll, loadTopic } from "./content";
import { HelpMarkdown } from "./HelpMarkdown";
import "../styles/help.css";
import { headings } from "./parse";
import { helpLink } from "./registry";
import { searchHelp, type HelpHit } from "./search";
import { HELP_CATEGORIES, HELP_TOPICS, isHelpTopic, type HelpTopicId } from "./topics";

function useTopicTitle() {
  const { t } = useI18n();
  return (id: HelpTopicId) => t(`help.topic.${id}.title`);
}

function SearchBox({ value, onChange }: { value: string; onChange: (q: string) => void }) {
  const { t } = useI18n();
  return (
    <form className="mkt-help__search" role="search" onSubmit={(e) => e.preventDefault()}>
      <input
        id="mkt-help-q"
        aria-label={t("help.search")}
        type="search"
        dir="auto"
        value={value}
        placeholder={t("help.searchPlaceholder")}
        onChange={(e) => onChange(e.target.value)}
      />
    </form>
  );
}

function Results({ query }: { query: string }) {
  const { t, locale } = useI18n();
  const title = useTopicTitle();
  const [hits, setHits] = useState<HelpHit[] | null>(null);
  useEffect(() => {
    let live = true;
    setHits(null);
    void loadAll(locale, HELP_TOPICS).then((all) => {
      if (!live) return;
      setHits(
        searchHelp(
          all.map((x) => ({ ...x, title: title(x.id) })),
          query
        )
      );
    });
    return () => {
      live = false;
    };
  }, [locale, query]); // title() follows the locale

  if (hits === null) return <p className="mkt-help__muted">{t("common.loading")}</p>;
  if (hits.length === 0)
    return <p className="mkt-help__muted">{t("help.noResults", { q: query })}</p>;
  return (
    <section aria-label={t("help.results")}>
      <p className="mkt-help__muted" role="status">
        {t("help.resultCount", { n: hits.length })}
      </p>
      <ul className="mkt-help__results">
        {hits.map((h) => (
          <li key={`${h.topic}#${h.anchor}`}>
            <Link to={helpLink(h.topic, h.anchor || undefined)} className="mkt-help__result">
              <span className="mkt-help__result-title" dir="auto">
                {h.heading && h.heading !== h.title ? `${h.title} › ${h.heading}` : h.title}
              </span>
              <span className="mkt-help__result-snippet" dir="auto">
                {h.snippet}
              </span>
            </Link>
          </li>
        ))}
      </ul>
    </section>
  );
}

export function HelpIndex() {
  const { t } = useI18n();
  const title = useTopicTitle();
  const [params, setParams] = useSearchParams();
  const query = params.get("q") ?? "";
  const setQuery = (q: string) =>
    setParams(q ? { q } : {}, { replace: true, preventScrollReset: true });
  return (
    <section className="mkt-page mkt-help">
      <header className="mkt-help__head">
        <h1>{t("help.title")}</h1>
        <p className="mkt-help__muted">{t("help.intro")}</p>
      </header>
      <SearchBox value={query} onChange={setQuery} />
      {query.trim() ? (
        <Results query={query.trim()} />
      ) : (
        <div className="mkt-help__cats">
          {HELP_CATEGORIES.map((c) => (
            <section key={c.id} className="mkt-help__cat" aria-labelledby={`mkt-help-cat-${c.id}`}>
              <h2 id={`mkt-help-cat-${c.id}`}>{t(`help.category.${c.id}`)}</h2>
              <ul>
                {c.topics.map((id) => (
                  <li key={id}>
                    <Link to={helpLink(id)} className="mkt-help__topic">
                      <span className="mkt-help__topic-title">{title(id)}</span>
                      <span className="mkt-help__topic-summary">
                        {t(`help.topic.${id}.summary`)}
                      </span>
                    </Link>
                  </li>
                ))}
              </ul>
            </section>
          ))}
        </div>
      )}
    </section>
  );
}

export function HelpTopic() {
  const { t, locale } = useI18n();
  const title = useTopicTitle();
  const { topic = "" } = useParams();
  const { hash } = useLocation();
  const active = decodeURIComponent(hash.replace(/^#/, ""));
  const [src, setSrc] = useState<string | null | undefined>(undefined);
  const known = isHelpTopic(topic);

  useEffect(() => {
    if (!known) return;
    let live = true;
    setSrc(undefined);
    void loadTopic(locale, topic).then((s) => live && setSrc(s));
    return () => {
      live = false;
    };
  }, [known, locale, topic]);

  // Scroll to the section in the address bar once the topic is on screen.
  useEffect(() => {
    if (!src || !active) return;
    const el = document.getElementById(active);
    if (!el) return;
    el.scrollIntoView?.({ block: "start" });
    el.focus?.({ preventScroll: true });
  }, [src, active]);

  const toc = useMemo(
    () => (src ? headings(src).filter((h) => h.level === 2 && h.anchor) : []),
    [src]
  );

  if (!known) {
    return (
      <section className="mkt-page mkt-help">
        <h1>{t("help.notFound")}</h1>
        <p>
          <Link to="/help">{t("help.allTopics")}</Link>
        </p>
      </section>
    );
  }
  const url = helpLink(topic);
  return (
    <article className="mkt-page mkt-help mkt-help--topic">
      <nav className="mkt-help__crumbs" aria-label={t("help.breadcrumb")}>
        <Link to="/help">{t("help.title")}</Link>
      </nav>
      <h1>{title(topic)}</h1>
      {src === undefined && <p className="mkt-help__muted">{t("common.loading")}</p>}
      {src === null && <p className="mkt-help__muted">{t("help.loadError")}</p>}
      {src && (
        <div className="mkt-help__layout">
          {toc.length > 0 && (
            <nav className="mkt-help__toc" aria-label={t("help.contents")}>
              <ul>
                {toc.map((h) => (
                  <li key={h.anchor}>
                    <Link to={`${url}#${h.anchor}`} preventScrollReset>
                      {h.text}
                    </Link>
                  </li>
                ))}
              </ul>
            </nav>
          )}
          <HelpMarkdown source={src} topicUrl={url} active={active} />
        </div>
      )}
    </article>
  );
}
