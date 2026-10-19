// Story 26.6 — Library enrichment review queue.
//
// Server contract:
//   GET /api/enrichment/pending -> { items:[{ video_id, video_title,
//        candidate_title, provider, confidence }] }
//
// Lists videos with a pending high-confidence match. Selecting a row
// shows the full EnrichmentPanel for that video. Keyboard-driven:
// j/k move the selection, a accepts and d dismisses the selected match
// (the panel's shortcuts), then the queue reloads and the next video is
// selected, so the reviewer can clear the queue quickly. Matches at or
// above rules.enrich.auto_accept_threshold (Settings > Rules; 0 =
// off) are accepted by the server's sweep and never reach the queue.
import { useEffect, useRef, useState } from "react";
import { Card } from "@ds/components/Card/Card";
import { Badge } from "@ds/components/Badge/Badge";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { Button } from "@ds/components/Button/Button";
import { AdminGate } from "../../components/AdminGate";
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

// Typing in a field (the panel's manual search) must not move the queue.
function isTyping(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  return (
    target.isContentEditable ||
    target instanceof HTMLInputElement ||
    target instanceof HTMLTextAreaElement ||
    target instanceof HTMLSelectElement
  );
}

// 0..1 → whole percent; a missing/odd value shows 0 %, never NaN.
function pct(confidence: unknown): number {
  const c = typeof confidence === "number" && Number.isFinite(confidence) ? confidence : 0;
  return Math.round(Math.min(1, Math.max(0, c)) * 100);
}

export function AdminEnrichment() {
  return (
    <AdminGate>
      <EnrichmentInner />
    </AdminGate>
  );
}

function EnrichmentInner() {
  const { t } = useI18n();
  const [items, setItems] = useState<Pending[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const listRef = useRef<HTMLUListElement | null>(null);
  const itemsRef = useRef<Pending[] | null>(null);

  // load refreshes the queue. `after` is the video just decided: the
  // selection moves to the row that now sits where it was.
  function load(after?: string) {
    setErr(null);
    api
      .get<{ items?: Pending[] | null } | null>("/api/enrichment/pending")
      .then((r) => {
        const next = r?.items ?? [];
        const oldIdx = after
          ? (itemsRef.current ?? []).findIndex((it) => it.video_id === after)
          : -1;
        itemsRef.current = next;
        setItems(next);
        setSelected((cur) => {
          if (cur && next.some((it) => it.video_id === cur)) return cur;
          if (oldIdx >= 0 && next.length > 0) {
            return next[Math.min(oldIdx, next.length - 1)]!.video_id;
          }
          return next[0]?.video_id ?? null;
        });
      })
      .catch((e) => setErr(e instanceof ApiError ? e.problem.title : t("common.error")));
  }

  useEffect(() => load(), [t]);

  // j/k navigate the queue (keyboard-driven review, Story 26.6 AC).
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (!items || items.length === 0) return;
      if (e.key !== "j" && e.key !== "k") return;
      if (e.ctrlKey || e.metaKey || e.altKey || isTyping(e.target)) return;
      const idx = items.findIndex((it) => it.video_id === selected);
      const next = e.key === "j" ? Math.min(items.length - 1, idx + 1) : Math.max(0, idx - 1);
      setSelected(items[next].video_id);
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [items, selected]);

  if (err)
    return (
      <ErrorState
        kind="server"
        title={t("common.error")}
        description={err}
        action={
          <Button variant="secondary" onClick={() => load()}>
            {t("common.retry")}
          </Button>
        }
      />
    );

  return (
    <section className="mkt-page mkt-enrich-review">
      <header className="mkt-page__header">
        <h1>{t("enrich.review.title")}</h1>
        <p className="mkt-muted">{t("enrich.review.desc")}</p>
      </header>

      {!items && <p className="mkt-loading">{t("common.loading")}</p>}
      {items && items.length === 0 && (
        <EmptyState
          title={t("enrich.review.empty.title")}
          description={t("enrich.review.empty.desc")}
        />
      )}

      {items && items.length > 0 && (
        <div className="mkt-enrich-review__layout">
          <ul
            ref={listRef}
            className="mkt-enrich-review__queue"
            role="listbox"
            aria-label={t("enrich.review.title")}
          >
            {items.map((it) => (
              <li key={it.video_id} role="presentation">
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
                  <Badge tone="accent">{pct(it.confidence)}%</Badge>
                </button>
              </li>
            ))}
          </ul>
          <div className="mkt-enrich-review__detail">
            {selected ? (
              <EnrichmentPanel
                key={selected}
                videoId={selected}
                shortcuts
                onDecided={(action) => {
                  if (action !== "revert") load(selected);
                }}
              />
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
