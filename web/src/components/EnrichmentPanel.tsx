// Story 26.6 — Enrichment review panel (per video).
//
// Server contract (api/internal/handlers/enrichment):
//   GET  /api/videos/{id}/enrichment            -> { candidates:[Candidate], revertable:[field] }
//   POST /api/videos/{id}/enrichment/accept      { external_id, version? }
//   POST /api/videos/{id}/enrichment/dismiss     { external_id? }
//   POST /api/videos/{id}/enrichment/search      { query, year? }
//   POST /api/videos/{id}/enrichment/revert      { field? }
//
// Surfaces the top candidate as "We found this might be X (year) — NN%
// match" with a side-by-side current-vs-proposed diff; protected
// (user-owned) fields are badged "won't change". Accept / Dismiss /
// Search-manually controls, and "Revert to original" once an accept has
// written fields. Empty state offers a manual search CTA. With
// `shortcuts`, the a / d keys accept / dismiss the top candidate (the
// review queue turns this on).
import { useCallback, useEffect, useRef, useState } from "react";
import { Card } from "@ds/components/Card/Card";
import { Button } from "@ds/components/Button/Button";
import { Badge } from "@ds/components/Badge/Badge";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { useToast } from "@ds/components/Toast/Toast";
import { api, ApiError } from "../lib/api";
import { useI18n } from "../lib/i18n";

interface FieldDiff {
  field: string;
  current: string;
  proposed: string;
  would_change: boolean;
  protected: boolean;
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
  const { t, n } = useI18n();
  const toast = useToast();
  const [candidates, setCandidates] = useState<Candidate[] | null>(null);
  const [revertable, setRevertable] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [searching, setSearching] = useState(false);
  const [query, setQuery] = useState("");

  const load = useCallback(() => {
    api
      .get<{ candidates: Candidate[]; revertable?: string[] }>(
        `/api/videos/${encodeURIComponent(videoId)}/enrichment`
      )
      .then((r) => {
        setCandidates(r.candidates ?? []);
        setRevertable(r.revertable ?? []);
      })
      .catch(() => setCandidates([]));
  }, [videoId]);

  useEffect(() => {
    setCandidates(null);
    load();
  }, [load]);

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
      toast.show({
        message: e instanceof ApiError ? e.problem.title : t("common.error"),
        tone: "error",
      });
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
      toast.show({
        message: e instanceof ApiError ? e.problem.title : t("common.error"),
        tone: "error",
      });
    } finally {
      setBusy(false);
    }
  }

  async function revert() {
    setBusy(true);
    try {
      const r = await api.post<{ reverted: string[] }>(
        `/api/videos/${encodeURIComponent(videoId)}/enrichment/revert`,
        {}
      );
      toast.show({
        message: (r.reverted ?? []).length > 0 ? t("enrich.reverted") : t("enrich.revertNothing"),
        tone: "success",
      });
      load();
      onDecided?.("revert");
    } catch (e) {
      toast.show({
        message: e instanceof ApiError ? e.problem.title : t("common.error"),
        tone: "error",
      });
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
      const { top, busy: isBusy } = live.current;
      if (!top || isBusy) return;
      if (e.key === "a") {
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
      const r = await api.post<{ candidates: Candidate[] }>(
        `/api/videos/${encodeURIComponent(videoId)}/enrichment/search`,
        { query: query.trim() }
      );
      setCandidates(r.candidates ?? []);
    } catch (e) {
      toast.show({
        message: e instanceof ApiError ? e.problem.title : t("common.error"),
        tone: "error",
      });
    } finally {
      setSearching(false);
    }
  }

  if (candidates == null) return <p className="mkt-loading">{t("common.loading")}</p>;

  const top = candidates[0];
  const revertControl = revertable.length > 0 && (
    <div className="mkt-enrich-panel__revert">
      <p className="mkt-muted">
        {t("enrich.revertHint", {
          fields: revertable.map((f) => t(`enrich.fieldName.${f}`)).join(", "),
        })}
      </p>
      <Button variant="secondary" onClick={revert} disabled={busy}>
        {t("enrich.revert")}
      </Button>
    </div>
  );

  return (
    <Card className="mkt-enrich-panel" elevation={1}>
      <div className="mkt-enrich-panel__search">
        <input
          type="search"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={t("enrich.searchPlaceholder")}
          aria-label={t("enrich.searchManually")}
          dir="auto"
        />
        <Button
          variant="secondary"
          onClick={manualSearch}
          loading={searching}
          disabled={!query.trim()}
        >
          {t("enrich.searchManually")}
        </Button>
      </div>

      {revertControl}

      {candidates.length === 0 ? (
        <EmptyState title={t("enrich.empty.title")} description={t("enrich.empty.desc")} />
      ) : (
        <>
          <p className="mkt-enrich-panel__headline" dir="auto">
            {t("enrich.foundMaybe", {
              title: top.title || top.external_id,
              year: top.year ? ` (${n(top.year, { useGrouping: false })})` : "",
            })}{" "}
            <Badge tone="accent">
              {t("enrich.matchPct", { pct: String(Math.round(top.confidence * 100)) })}
            </Badge>
          </p>

          {top.fields && top.fields.length > 0 && (
            <table className="mkt-enrich-diff" aria-label={t("enrich.diff")}>
              <thead>
                <tr>
                  <th>{t("enrich.field")}</th>
                  <th>{t("enrich.current")}</th>
                  <th>{t("enrich.proposed")}</th>
                </tr>
              </thead>
              <tbody>
                {top.fields.map((f) => (
                  <tr key={f.field} className={f.protected ? "mkt-enrich-diff__protected" : ""}>
                    <td>{t(`enrich.fieldName.${f.field}`)}</td>
                    <td dir="auto">{f.current || "—"}</td>
                    <td dir="auto">
                      {f.proposed || "—"}
                      {f.protected && <Badge tone="neutral">{t("enrich.wontChange")}</Badge>}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}

          <div className="mkt-enrich-panel__actions">
            <Button
              onClick={() => accept(top)}
              loading={busy}
              aria-keyshortcuts={shortcuts ? "a" : undefined}
            >
              {t("enrich.accept")}
            </Button>
            <Button
              variant="ghost"
              onClick={() => dismiss(top)}
              disabled={busy}
              aria-keyshortcuts={shortcuts ? "d" : undefined}
            >
              {t("enrich.dismiss")}
            </Button>
          </div>
          {shortcuts && <p className="mkt-muted mkt-enrich-panel__keys">{t("enrich.keys")}</p>}
        </>
      )}
    </Card>
  );
}
