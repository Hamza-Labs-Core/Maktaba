// Story 26.6 — Enrichment review panel (per video).
//
// Server contract (api/internal/handlers/enrichment):
//   GET  /api/videos/{id}/enrichment            -> { candidates:[Candidate] }
//   POST /api/videos/{id}/enrichment/accept      { external_id, version? }
//   POST /api/videos/{id}/enrichment/dismiss     { external_id? }
//   POST /api/videos/{id}/enrichment/search      { query, year? }
//   POST /api/videos/{id}/enrichment/revert      { field? }
//
// Surfaces the top candidate as "We found this might be X (year) — NN%
// match" with a side-by-side current-vs-proposed diff; protected
// (user-owned) fields are badged "won't change". Accept / Dismiss /
// Search-manually controls. Empty state offers a manual search CTA.
import { useCallback, useEffect, useState } from "react";
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

export function EnrichmentPanel({ videoId }: { videoId: string }) {
  const { t, n } = useI18n();
  const toast = useToast();
  const [candidates, setCandidates] = useState<Candidate[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [searching, setSearching] = useState(false);
  const [query, setQuery] = useState("");

  const load = useCallback(() => {
    api
      .get<{ candidates: Candidate[] }>(`/api/videos/${encodeURIComponent(videoId)}/enrichment`)
      .then((r) => setCandidates(r.candidates ?? []))
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
    } catch (e) {
      toast.show({
        message: e instanceof ApiError ? e.problem.title : t("common.error"),
        tone: "error",
      });
    } finally {
      setBusy(false);
    }
  }

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

      {candidates.length === 0 ? (
        <EmptyState title={t("enrich.empty.title")} description={t("enrich.empty.desc")} />
      ) : (
        <>
          <p className="mkt-enrich-panel__headline" dir="auto">
            {t("enrich.foundMaybe", {
              title: top.title || top.external_id,
              year: top.year ? ` (${n(top.year)})` : "",
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
            <Button onClick={() => accept(top)} loading={busy}>
              {t("enrich.accept")}
            </Button>
            <Button variant="ghost" onClick={() => dismiss(top)} disabled={busy}>
              {t("enrich.dismiss")}
            </Button>
          </div>
        </>
      )}
    </Card>
  );
}
