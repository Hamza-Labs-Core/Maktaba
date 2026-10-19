// Story 26.6 — Enrichment review panel (per video); stub audit #4 — the
// suggestions come from Maktaba's metadata provider (its own intel, and a
// web search a language model read), each field with its confidence and
// the sources it rests on.
//
// Server contract (api/internal/handlers/enrichment):
//   GET  /api/videos/{id}/enrichment            -> { candidates:[Candidate], job?, revertable:[field] }
//   POST /api/videos/{id}/enrichment/accept      { external_id, version? }
//   POST /api/videos/{id}/enrichment/dismiss     { external_id? }
//   POST /api/videos/{id}/enrichment/search      { query, year? } -> 202 { candidates, job }
//   POST /api/videos/{id}/enrichment/reenrich    -> 202 { job }
//   POST /api/videos/{id}/enrichment/revert      { field? }
//
// Surfaces the top candidate as "We found this might be X (year) — NN%
// match" with a side-by-side current-vs-proposed diff; protected
// (user-owned) fields are badged "won't change". Every field shows its
// confidence and its sources (web pages link out; Maktaba's own intel is
// named). A search runs as the engine's `enrich` job: the panel shows it
// and polls until it is done. Accept / Dismiss / Search controls, and
// "Revert to original" once an accept has written fields. With
// `shortcuts`, the a / d keys accept / dismiss the top candidate (the
// review queue turns this on).
import { useCallback, useEffect, useRef, useState } from "react";
import { Card } from "@ds/components/Card/Card";
import { Button } from "@ds/components/Button/Button";
import { Badge } from "@ds/components/Badge/Badge";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { useToast } from "@ds/components/Toast/Toast";
import { api, ApiError } from "../lib/api";
import { lookupMessage, useI18n } from "../lib/i18n";

interface FieldDiff {
  field: string;
  current: string;
  proposed: string;
  would_change: boolean;
  protected: boolean;
  confidence?: number;
  sources?: string[];
}
interface Source {
  id: string;
  label: string;
  url?: string;
  kind?: string;
}
interface Fact {
  field: string;
  value: unknown;
  confidence?: number;
  sources?: string[];
}
interface Candidate {
  id: string;
  provider: string;
  external_id: string;
  confidence: number;
  accepted: boolean;
  title?: string;
  year?: number;
  fields?: FieldDiff[];
  sources?: Source[];
  facts?: Fact[];
}
interface Job {
  status: "pending" | "running" | "done" | "deferred" | "failed";
  origin: string;
  query?: string;
  last_error?: string;
  not_before?: string;
}

const POLL_MS = 3000;

function isBusy(job: Job | null | undefined): boolean {
  return job?.status === "pending" || job?.status === "running";
}

// A YouTube row ties a video to its listing (accepted when imported): it is
// not a suggestion to review.
function suggestions(cands: Candidate[] | undefined): Candidate[] {
  return (cands ?? []).filter((c) => !(c.provider === "youtube" && c.accepted));
}

// isTypingTarget is true for a key press meant for a text field, so
// single-letter shortcuts never fire while someone types.
export function isTypingTarget(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null;
  if (!el || typeof el.tagName !== "string") return false;
  const tag = el.tagName.toLowerCase();
  return tag === "input" || tag === "textarea" || tag === "select" || el.isContentEditable;
}

export interface EnrichmentPanelProps {
  videoId: string;
  /** a / d accept / dismiss the top candidate. */
  shortcuts?: boolean;
  /** Called after an accept, dismiss or revert succeeded. */
  onDecided?: (action: "accept" | "dismiss" | "revert") => void;
}

export function EnrichmentPanel({ videoId, shortcuts = false, onDecided }: EnrichmentPanelProps) {
  const { t, n, locale, formatDate } = useI18n();
  const toast = useToast();
  const [candidates, setCandidates] = useState<Candidate[] | null>(null);
  const [revertable, setRevertable] = useState<string[]>([]);
  const [job, setJob] = useState<Job | null>(null);
  const [busy, setBusy] = useState(false);
  const [searching, setSearching] = useState(false);
  const [query, setQuery] = useState("");

  const load = useCallback(() => {
    api
      .get<{ candidates: Candidate[]; job?: Job | null; revertable?: string[] | null }>(
        `/api/videos/${encodeURIComponent(videoId)}/enrichment`
      )
      .then((r) => {
        setCandidates(suggestions(r.candidates));
        setJob(r.job ?? null);
        setRevertable(r.revertable ?? []);
      })
      .catch(() => setCandidates([]));
  }, [videoId]);

  useEffect(() => {
    setCandidates(null);
    setJob(null);
    load();
  }, [load]);

  // poll while the search runs (the engine's enrich job)
  useEffect(() => {
    if (!isBusy(job)) return;
    const id = window.setTimeout(load, POLL_MS);
    return () => window.clearTimeout(id);
  }, [job, load]);

  const fail = (e: unknown) =>
    toast.show({
      message: e instanceof ApiError ? e.problem.title : t("common.error"),
      tone: "error",
    });

  async function accept(c: Candidate) {
    setBusy(true);
    try {
      await api.post(`/api/videos/${encodeURIComponent(videoId)}/enrichment/accept`, {
        external_id: c.external_id,
      });
      toast.show({ message: t("enrich.accepted"), tone: "success" });
      load();
      onDecided?.("accept");
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }

  async function dismiss(c: Candidate) {
    setBusy(true);
    try {
      await api.post(`/api/videos/${encodeURIComponent(videoId)}/enrichment/dismiss`, {
        external_id: c.external_id,
      });
      toast.show({ message: t("enrich.dismissed"), tone: "info" });
      load();
      onDecided?.("dismiss");
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }

  async function revert() {
    setBusy(true);
    try {
      const r = await api.post<{ reverted?: string[] | null }>(
        `/api/videos/${encodeURIComponent(videoId)}/enrichment/revert`,
        {}
      );
      toast.show({
        message: (r?.reverted ?? []).length > 0 ? t("enrich.reverted") : t("enrich.revertNothing"),
        tone: "success",
      });
      load();
      onDecided?.("revert");
    } catch (e) {
      fail(e);
    } finally {
      setBusy(false);
    }
  }

  // The latest top candidate and busy flag, for the key handler below.
  const live = useRef<{ top?: Candidate; busy: boolean }>({ busy: false });
  live.current = { top: candidates?.[0], busy };
  const actions = useRef({ accept, dismiss });
  actions.current = { accept, dismiss };

  useEffect(() => {
    if (!shortcuts) return;
    function onKey(e: KeyboardEvent) {
      if (e.ctrlKey || e.metaKey || e.altKey || isTypingTarget(e.target)) return;
      const { top, busy: isBusyNow } = live.current;
      if (!top || isBusyNow) return;
      if (e.key === "a" && !top.accepted) {
        e.preventDefault();
        void actions.current.accept(top);
      } else if (e.key === "d") {
        e.preventDefault();
        void actions.current.dismiss(top);
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [shortcuts]);

  async function manualSearch() {
    setSearching(true);
    try {
      const r = await api.post<{ candidates: Candidate[]; job?: Job | null }>(
        `/api/videos/${encodeURIComponent(videoId)}/enrichment/search`,
        { query: query.trim() }
      );
      setCandidates(suggestions(r.candidates));
      setJob(r.job ?? null);
      toast.show({ message: t("enrich.queued"), tone: "info" });
    } catch (e) {
      fail(e);
    } finally {
      setSearching(false);
    }
  }

  async function reenrich() {
    setSearching(true);
    try {
      const r = await api.post<{ job?: Job | null }>(
        `/api/videos/${encodeURIComponent(videoId)}/enrichment/reenrich`,
        {}
      );
      setJob(r.job ?? null);
      toast.show({ message: t("enrich.queued"), tone: "info" });
    } catch (e) {
      fail(e);
    } finally {
      setSearching(false);
    }
  }

  if (candidates == null) return <p className="mkt-loading">{t("common.loading")}</p>;

  const reason = (raw?: string) => {
    const r = (raw ?? "").trim();
    if (!r) return "";
    const key = r.startsWith("budget:")
      ? r
      : r.toLowerCase().includes("rate")
        ? "rate_limited"
        : "";
    return (key && lookupMessage(locale, `enrich.reason.${key}`)) || r;
  };
  const labels: Labels = {
    pct: (c) => (c == null ? "" : t("enrich.confidencePct", { pct: n(Math.round(c * 100)) })),
    fieldName: (f) => lookupMessage(locale, `enrich.fieldName.${f}`) ?? f,
    providerName: (p) => lookupMessage(locale, `enrich.provider.${p}`) ?? p,
    value: (v) =>
      Array.isArray(v)
        ? v.map(String).join(locale === "ar" ? "، " : ", ")
        : typeof v === "number"
          ? n(v, { useGrouping: false })
          : String(v ?? ""),
  };

  const top = candidates[0];
  const rest = candidates.slice(1);

  return (
    <Card className="mkt-enrich-panel" elevation={1}>
      <div className="mkt-enrich-panel__search">
        <input
          type="search"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && query.trim() && !isBusy(job)) void manualSearch();
          }}
          placeholder={t("enrich.searchPlaceholder")}
          aria-label={t("enrich.searchManually")}
          maxLength={200}
          dir="auto"
        />
        <Button
          variant="secondary"
          onClick={manualSearch}
          loading={searching}
          disabled={!query.trim() || isBusy(job)}
        >
          {t("enrich.searchManually")}
        </Button>
        <Button variant="ghost" onClick={reenrich} disabled={searching || isBusy(job)}>
          {t("enrich.reenrich")}
        </Button>
      </div>

      {job && job.status !== "done" && (
        <p
          className={`mkt-enrich-panel__job mkt-enrich-panel__job--${job.status}`}
          role="status"
          aria-live="polite"
          dir="auto"
        >
          {job.status === "deferred"
            ? t("enrich.job.deferred", {
                when: job.not_before ? formatDate(job.not_before) : "",
                reason: reason(job.last_error),
              })
            : job.status === "failed"
              ? t("enrich.job.failed", { reason: reason(job.last_error) })
              : t(`enrich.job.${job.status}`)}
        </p>
      )}

      {revertable.length > 0 && (
        <div className="mkt-enrich-panel__revert">
          <p className="mkt-muted">
            {t("enrich.revertHint", {
              fields: revertable
                .map((f) => labels.fieldName(f))
                .join(locale === "ar" ? "، " : ", "),
            })}
          </p>
          <Button variant="secondary" onClick={revert} disabled={busy}>
            {t("enrich.revert")}
          </Button>
        </div>
      )}

      {candidates.length === 0 ? (
        <EmptyState title={t("enrich.empty.title")} description={t("enrich.empty.desc")} />
      ) : (
        <>
          <CandidateView
            c={top}
            headline
            shortcuts={shortcuts}
            busy={busy}
            onAccept={accept}
            onDismiss={dismiss}
            labels={labels}
          />
          {shortcuts && <p className="mkt-muted mkt-enrich-panel__keys">{t("enrich.keys")}</p>}
          {rest.length > 0 && (
            <section aria-labelledby="mkt-enrich-more">
              <h3 id="mkt-enrich-more" className="mkt-enrich-panel__more">
                {t("enrich.otherSuggestions")}
              </h3>
              {rest.map((c) => (
                <CandidateView
                  key={c.id}
                  c={c}
                  busy={busy}
                  onAccept={accept}
                  onDismiss={dismiss}
                  labels={labels}
                />
              ))}
            </section>
          )}
        </>
      )}
    </Card>
  );
}

interface Labels {
  pct: (c?: number) => string;
  fieldName: (f: string) => string;
  providerName: (p: string) => string;
  value: (v: unknown) => string;
}

function CandidateView({
  c,
  headline,
  shortcuts,
  busy,
  onAccept,
  onDismiss,
  labels,
}: {
  c: Candidate;
  headline?: boolean;
  /** this card's Accept / Dismiss answer the a / d keys */
  shortcuts?: boolean;
  busy: boolean;
  onAccept: (c: Candidate) => void;
  onDismiss: (c: Candidate) => void;
  labels: Labels;
}) {
  const { t, n, locale } = useI18n();
  const { pct, fieldName, providerName, value } = labels;
  const sources = c.sources ?? [];
  const known = new Set(sources.map((s) => s.id));
  const anchor = (id: string) => `mkt-src-${c.id}-${id}`;
  const sourceLabel = (s: Source) =>
    (s.kind && lookupMessage(locale, `enrich.sourceKind.${s.kind}`)) || s.label || s.url || s.id;
  const cites = (ids?: string[]) =>
    (ids ?? [])
      .filter((id) => known.has(id))
      .map((id) => (
        <a key={id} href={`#${anchor(id)}`} className="mkt-enrich-cite" dir="ltr">
          [{id}]
        </a>
      ));
  const sep = locale === "ar" ? "، " : ", ";
  const usedFor = (id: string) =>
    [...(c.fields ?? []), ...(c.facts ?? [])]
      .filter((f) => f.sources?.includes(id))
      .map((f) => fieldName(f.field))
      .join(sep);

  return (
    <article className="mkt-enrich-cand" aria-label={providerName(c.provider)}>
      <p className={headline ? "mkt-enrich-panel__headline" : "mkt-enrich-cand__title"} dir="auto">
        {t("enrich.foundMaybe", {
          title: c.title || c.external_id,
          year: c.year ? ` (${n(c.year, { useGrouping: false })})` : "",
        })}{" "}
        <Badge tone="accent">
          {t("enrich.matchPct", { pct: String(Math.round(c.confidence * 100)) })}
        </Badge>{" "}
        <Badge tone="neutral">{providerName(c.provider)}</Badge>
      </p>

      {c.fields && c.fields.length > 0 && (
        <div className="mkt-enrich-scroll">
          <table className="mkt-enrich-diff" aria-label={t("enrich.diff")}>
            <thead>
              <tr>
                <th>{t("enrich.field")}</th>
                <th>{t("enrich.current")}</th>
                <th>{t("enrich.proposed")}</th>
                <th>{t("enrich.confidence")}</th>
              </tr>
            </thead>
            <tbody>
              {c.fields.map((f) => (
                <tr key={f.field} className={f.protected ? "mkt-enrich-diff__protected" : ""}>
                  <td>{fieldName(f.field)}</td>
                  <td dir="auto">{f.current || "—"}</td>
                  <td dir="auto">
                    {f.proposed || "—"}
                    {f.protected && <Badge tone="neutral">{t("enrich.wontChange")}</Badge>}
                  </td>
                  <td className="mkt-enrich-conf">
                    {pct(f.confidence)} {cites(f.sources)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {c.facts && c.facts.length > 0 && (
        <div className="mkt-enrich-scroll">
          <table className="mkt-enrich-diff mkt-enrich-facts">
            <caption>{t("enrich.facts")}</caption>
            <tbody>
              {c.facts.map((f) => (
                <tr key={f.field}>
                  <th scope="row">{fieldName(f.field)}</th>
                  <td dir="auto">{value(f.value)}</td>
                  <td className="mkt-enrich-conf">
                    {pct(f.confidence)} {cites(f.sources)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {sources.length > 0 && (
        <details className="mkt-enrich-sources" open={headline}>
          <summary>{t("enrich.sources")}</summary>
          <ol>
            {sources.map((s) => (
              <li key={s.id} id={anchor(s.id)}>
                <code dir="ltr">[{s.id}]</code>{" "}
                {s.url ? (
                  <a href={s.url} target="_blank" rel="noopener noreferrer nofollow" dir="auto">
                    {sourceLabel(s)}
                  </a>
                ) : (
                  <span dir="auto">{sourceLabel(s)}</span>
                )}
                {usedFor(s.id) && (
                  <span className="mkt-enrich-sources__used" dir="auto">
                    {" "}
                    — {t("enrich.citedBy", { fields: usedFor(s.id) })}
                  </span>
                )}
              </li>
            ))}
          </ol>
        </details>
      )}

      <div className="mkt-enrich-panel__actions">
        <Button
          onClick={() => onAccept(c)}
          loading={busy}
          disabled={c.accepted}
          aria-keyshortcuts={shortcuts ? "a" : undefined}
        >
          {t("enrich.accept")}
        </Button>
        <Button
          variant="ghost"
          onClick={() => onDismiss(c)}
          disabled={busy}
          aria-keyshortcuts={shortcuts ? "d" : undefined}
        >
          {t("enrich.dismiss")}
        </Button>
      </div>
    </article>
  );
}
