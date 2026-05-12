// Story 11.4 — Search interface.
//
// Submits queries to `/api/search?q=...&type=video|segment`. The
// p50/p95 targets (Story 11/§performance) are an Epic 7 / 18 concern;
// the page just wires the surface.
import { type FormEvent, useState } from "react";
import { Link } from "react-router-dom";
import { api, ApiError } from "../lib/api";
import { useI18n } from "../lib/i18n";

type Scope = "video" | "segment";

interface SearchHit {
  id: string;
  video_id: string;
  title: string;
  snippet?: string;
  start_sec?: number;
}

interface SearchResponse {
  hits: SearchHit[];
  total: number;
  took_ms?: number;
}

export function Search() {
  const { t } = useI18n();
  const [q, setQ] = useState("");
  const [scope, setScope] = useState<Scope>("segment");
  const [hits, setHits] = useState<SearchHit[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [pending, setPending] = useState(false);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (!q.trim()) return;
    setPending(true);
    setErr(null);
    try {
      const params = new URLSearchParams({ q, type: scope });
      const res = await api.get<SearchResponse>(`/api/search?${params.toString()}`);
      setHits(res.hits ?? []);
    } catch (e) {
      if (e instanceof ApiError) setErr(e.problem.title);
      else setErr(t("common.error"));
      setHits([]);
    } finally {
      setPending(false);
    }
  }

  return (
    <section className="mkt-page mkt-search">
      <form onSubmit={onSubmit} className="mkt-search__form" role="search">
        <input
          type="search"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder={t("nav.search")}
          aria-label={t("nav.search")}
          autoFocus
        />
        <select
          value={scope}
          onChange={(e) => setScope(e.target.value as Scope)}
          aria-label="Scope"
        >
          <option value="segment">Transcript</option>
          <option value="video">Title</option>
        </select>
        <button type="submit" className="mkt-btn mkt-btn--primary" disabled={pending}>
          {pending ? t("common.loading") : t("nav.search")}
        </button>
      </form>
      {err && (
        <div className="mkt-alert" role="alert">
          {err}
        </div>
      )}
      {hits && hits.length === 0 && !err && <p className="mkt-empty">{t("common.empty")}</p>}
      {hits && hits.length > 0 && (
        <ul className="mkt-results">
          {hits.map((h) => (
            <li key={h.id} className="mkt-result">
              <Link
                to={
                  typeof h.start_sec === "number"
                    ? `/videos/${h.video_id}/watch?t=${h.start_sec}`
                    : `/videos/${h.video_id}`
                }
              >
                <div className="mkt-result__title">{h.title}</div>
                {h.snippet && <div className="mkt-result__snippet">{h.snippet}</div>}
              </Link>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
