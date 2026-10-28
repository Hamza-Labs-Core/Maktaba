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
// off) are accepted by the server's sweep and never reach the queue. A
// return shows the last queue at once (lib/usePageData); a decision changes
// the video, so the pages that show videos read them again.
import { useEffect, useRef, useState } from "react";
import { Card } from "@ds/components/Card/Card";
import { Skeleton } from "@ds/components/Skeleton/Skeleton";
import { Badge } from "@ds/components/Badge/Badge";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { Button } from "@ds/components/Button/Button";
import { AdminGate } from "../../components/AdminGate";
import { EnrichmentPanel } from "../../components/EnrichmentPanel";
import { api, ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import { TitleRow } from "../../help/HelpLink";
import { usePageData } from "../../lib/usePageData";
import { forgetAnswers, VIDEO_ANSWERS } from "../../lib/pageCache";
import { Loading } from "../../components/Placeholders";

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

/** The queue's rows and the panel beside them while the first read runs. */
function QueuePlaceholder() {
  return (
    <div className="mkt-enrich-review__layout" aria-hidden="true">
      <ul className="mkt-enrich-review__queue">
        {[0, 1, 2, 3].map((i) => (
          <li key={i}>
            <span className="mkt-enrich-review__row">
              <span className="mkt-enrich-review__vtitle">
                <Skeleton variant="text" className="mkt-ph-line" width="70%" />
              </span>
              <span className="mkt-enrich-review__match">
                <Skeleton variant="text" className="mkt-ph-line" width="50%" />
              </span>
            </span>
          </li>
        ))}
      </ul>
      <div className="mkt-enrich-review__detail">
        <Card elevation={1}>
          <Skeleton variant="text" className="mkt-ph-line" width="60%" />
          <Skeleton variant="text" className="mkt-ph-line" width="90%" />
          <Skeleton variant="text" className="mkt-ph-line" width="40%" />
        </Card>
      </div>
    </div>
  );
}

function EnrichmentInner() {
  const { t } = useI18n();
  const {
    data: items,
    err: loadErr,
    loading,
    reload,
    setData: setItems,
  } = usePageData<Pending[]>(
    "admin:enrichment",
    () =>
      api
        .get<{ items?: Pending[] | null } | null>("/api/enrichment/pending")
        .then((r) => r?.items ?? []),
    [t]
  );
  const err =
    loadErr == null
      ? null
      : loadErr instanceof ApiError
        ? loadErr.problem.title
        : t("common.error");
  const [selected, setSelected] = useState<string | null>(null);
  const listRef = useRef<HTMLUListElement | null>(null);
  const itemsRef = useRef<Pending[] | null>(null);

  // Each new queue keeps the selection; when the selected video left it
  // (just decided), the selection moves to the row that now sits where it was.
  useEffect(() => {
    if (!items) return;
    const prev = itemsRef.current ?? [];
    itemsRef.current = items;
    setSelected((cur) => {
      if (cur && items.some((it) => it.video_id === cur)) return cur;
      const oldIdx = cur ? prev.findIndex((it) => it.video_id === cur) : -1;
      if (oldIdx >= 0 && items.length > 0) {
        return items[Math.min(oldIdx, items.length - 1)]!.video_id;
      }
      return items[0]?.video_id ?? null;
    });
  }, [items]);

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
          <Button variant="secondary" onClick={reload}>
            {t("common.retry")}
          </Button>
        }
      />
    );

  return (
    <section className="mkt-page mkt-enrich-review">
      <header className="mkt-page__header">
        <TitleRow>
          <h1>{t("enrich.review.title")}</h1>
        </TitleRow>
        <p className="mkt-muted">{t("enrich.review.desc")}</p>
      </header>

      {!items && (
        <Loading loading={loading}>
          <QueuePlaceholder />
        </Loading>
      )}
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
                  // the video's title, poster or details may have changed
                  forgetAnswers(...VIDEO_ANSWERS);
                  if (action === "revert") return;
                  // decided: out of the queue now, and in the kept one
                  setItems((prev) => prev && prev.filter((it) => it.video_id !== selected));
                  reload();
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
