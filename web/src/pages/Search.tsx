// Story 11.4 — Search interface.
//
// Speaks the real server contract (api/internal/handlers/search):
//   POST /api/search  { q, mode, limit }
//   -> { hits: [{ segment_id, video_id, start_sec, end_sec, snippet,
//        score }], total, took_ms, mode, filters }
// The p50/p95 targets (Story 11/§performance) are an Epic 7 / 18
// concern; the page just wires the surface.
import { type FormEvent, type ReactNode, useState } from "react";
import { Link } from "react-router-dom";
import { api, ApiError } from "../lib/api";
import { useI18n } from "../lib/i18n";

type Mode = "hybrid" | "fts" | "semantic";

const SEARCH_LIMIT = 50;

// Server-side hit shape (matches search.Hit JSON tags).
interface ServerHit {
  segment_id: number;
  video_id: string;
  start_sec: number;
  end_sec: number;
  snippet: string;
  score: number;
}

interface SearchResponse {
  hits: ServerHit[];
  total: number;
  took_ms?: { fts: number; semantic: number; fusion: number };
  mode?: string;
  filters?: unknown;
}

// renderSnippet turns the server-highlighted snippet into safe React
// nodes. search.highlightSnippet only ever emits <mark>…</mark> wrappers
// (and a leading/trailing "…"); we split on those literal tags and keep
// every other run as a plain text node, so React escapes it — no raw
// HTML injection, no sanitiser dependency.
function renderSnippet(snippet: string) {
  const parts = snippet.split(/(<mark>|<\/mark>)/);
  const nodes: ReactNode[] = [];
  let inMark = false;
  let key = 0;
  for (const part of parts) {
    if (part === "<mark>") {
      inMark = true;
      continue;
    }
    if (part === "</mark>") {
      inMark = false;
      continue;
    }
    if (part === "") continue;
    nodes.push(
      inMark ? <mark key={key++}>{part}</mark> : <span key={key++}>{part}</span>
    );
  }
  return nodes;
}

export function Search() {
  const { t } = useI18n();
  const [q, setQ] = useState("");
  const [mode, setMode] = useState<Mode>("hybrid");
  const [hits, setHits] = useState<ServerHit[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [pending, setPending] = useState(false);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (!q.trim()) return;
    setPending(true);
    setErr(null);
    try {
      const res = await api.post<SearchResponse>("/api/search", {
        q: q.trim(),
        mode,
        limit: SEARCH_LIMIT,
      });
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
          value={mode}
          onChange={(e) => setMode(e.target.value as Mode)}
          aria-label="Mode"
        >
          <option value="hybrid">Hybrid</option>
          <option value="fts">Keyword</option>
          <option value="semantic">Semantic</option>
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
            <li key={h.segment_id} className="mkt-result">
              <Link to={`/videos/${h.video_id}/watch?t=${Math.floor(h.start_sec)}`}>
                {h.snippet && (
                  <div className="mkt-result__snippet">{renderSnippet(h.snippet)}</div>
                )}
              </Link>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
