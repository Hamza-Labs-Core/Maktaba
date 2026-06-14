// Story 26.6 — Library enrichment review queue.
//
// Server contract:
//   GET /api/enrichment/pending -> { items:[{ video_id, video_title,
//        candidate_title, provider, confidence }] }
//
// Lists videos with a pending high-confidence match. Selecting a row
// shows the full EnrichmentPanel for that video. Keyboard-driven:
// j/k move the selection, a accepts (handled inside the panel via its
// own controls), so the reviewer can clear the queue quickly.
import { useEffect, useRef, useState } from "react";
import { Card } from "@ds/components/Card/Card";
import { Badge } from "@ds/components/Badge/Badge";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { EnrichmentPanel } from "../../components/EnrichmentPanel";
import { api, ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";

interface Pending {
  video_id: string;
  library_id: string;
  video_title: string;
  candidate_title: string;
  provider: string;
  confidence: number;
}

export function AdminEnrichment() {
  const { t } = useI18n();
  const [items, setItems] = useState<Pending[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const listRef = useRef<HTMLUListElement | null>(null);

  function load() {
    api
      .get<{ items: Pending[] }>("/api/enrichment/pending")
      .then((r) => {
        setItems(r.items ?? []);
        setSelected((cur) => cur ?? r.items?.[0]?.video_id ?? null);
      })
      .catch((e) => setErr(e instanceof ApiError ? e.problem.title : t("common.error")));
  }

  useEffect(load, [t]);

  // j/k navigate the queue (keyboard-driven review, Story 26.6 AC).
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (!items || items.length === 0) return;
      if (e.key !== "j" && e.key !== "k") return;
      const idx = items.findIndex((it) => it.video_id === selected);
      const next = e.key === "j" ? Math.min(items.length - 1, idx + 1) : Math.max(0, idx - 1);
      setSelected(items[next].video_id);
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [items, selected]);

  if (err) return <ErrorState kind="server" title={t("common.error")} description={err} />;

  return (
    <section className="mkt-page mkt-enrich-review">
      <header className="mkt-page__header">
        <h1>{t("enrich.review.title")}</h1>
        <p className="mkt-muted">{t("enrich.review.desc")}</p>
      </header>

      {!items && <p className="mkt-loading">{t("common.loading")}</p>}
      {items && items.length === 0 && (
        <EmptyState title={t("enrich.review.empty.title")} description={t("enrich.review.empty.desc")} />
      )}

      {items && items.length > 0 && (
        <div className="mkt-enrich-review__layout">
          <ul ref={listRef} className="mkt-enrich-review__queue" role="listbox" aria-label={t("enrich.review.title")}>
            {items.map((it) => (
              <li key={it.video_id}>
                <button
                  type="button"
                  role="option"
                  aria-selected={selected === it.video_id}
                  className={`mkt-enrich-review__row${selected === it.video_id ? " is-selected" : ""}`}
                  onClick={() => setSelected(it.video_id)}
                >
                  <span className="mkt-enrich-review__vtitle" dir="auto">
                    {it.video_title}
                  </span>
                  <span className="mkt-enrich-review__match" dir="auto">
                    {it.candidate_title}
                  </span>
                  <Badge tone="accent">{Math.round(it.confidence * 100)}%</Badge>
                </button>
              </li>
            ))}
          </ul>
          <div className="mkt-enrich-review__detail">
            {selected ? (
              <EnrichmentPanel key={selected} videoId={selected} />
            ) : (
              <Card elevation={1}>
                <p className="mkt-muted">{t("enrich.review.select")}</p>
              </Card>
            )}
          </div>
        </div>
      )}
    </section>
  );
}
