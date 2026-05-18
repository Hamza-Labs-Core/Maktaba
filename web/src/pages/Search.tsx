// Story 11.4 — Search interface.
//
// Real server contract (api/internal/handlers/search):
//   POST   /api/search          { q, mode, limit } -> { hits, total, ... }
//   GET    /api/search/suggest?q=  -> { suggestions: string[] }
//   POST   /api/search/save     { q, mode }
//   GET    /api/search/saved    -> { items:[{ id, q, mode }] }
//   DELETE /api/search/saved/{id}
//
// `?q=` seeds the query from the header search box. Mode (hybrid/fts/
// semantic) persists in localStorage. Suggestions debounce at 200ms.
// Snippet `<mark>` runs are rendered as escaped React nodes (no raw
// HTML). Deep-links use a formatted [mm:ss → mm:ss] range. The hit list
// is window-capped (true virtualization deferred — see report).
import { type FormEvent, type ReactNode, useEffect, useMemo, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { api, ApiError } from "../lib/api";
import { useI18n } from "../lib/i18n";

type Mode = "hybrid" | "fts" | "semantic";
const SEARCH_LIMIT = 50;
const MODE_KEY = "mkt:search:mode";
const RESULT_WINDOW = 100;

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
}
interface SavedSearch {
  id: string;
  q: string;
  mode: string;
}

function readMode(): Mode {
  try {
    const m = localStorage.getItem(MODE_KEY);
    if (m === "hybrid" || m === "fts" || m === "semantic") return m;
  } catch {
    /* ignored */
  }
  return "hybrid";
}

function ts(s: number): string {
  const m = Math.floor(s / 60);
  const sec = Math.floor(s % 60);
  return `${m}:${sec.toString().padStart(2, "0")}`;
}

// Server emits only <mark>…</mark> wrappers; split on the literal tags
// and keep every other run as a plain (escaped) text node.
function renderSnippet(snippet: string): ReactNode[] {
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
      inMark ? (
        <mark key={key++}>{part}</mark>
      ) : (
        <span key={key++} dir="auto">
          {part}
        </span>
      )
    );
  }
  return nodes;
}

export function Search() {
  const { t } = useI18n();
  const [params, setParams] = useSearchParams();
  const [q, setQ] = useState(params.get("q") ?? "");
  const [mode, setMode] = useState<Mode>(readMode);
  const [hits, setHits] = useState<ServerHit[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const [suggestions, setSuggestions] = useState<string[]>([]);
  const [saved, setSaved] = useState<SavedSearch[]>([]);
  const suggestTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    try {
      localStorage.setItem(MODE_KEY, mode);
    } catch {
      /* ignored */
    }
  }, [mode]);

  const refreshSaved = () =>
    api
      .get<{ items: SavedSearch[] }>("/api/search/saved")
      .then((r) => setSaved(r.items ?? []))
      .catch(() => setSaved([]));

  useEffect(() => {
    void refreshSaved();
  }, []);

  const runSearch = useMemo(
    () => async (term: string, m: Mode) => {
      const query = term.trim();
      if (!query) return;
      setPending(true);
      setErr(null);
      try {
        const res = await api.post<SearchResponse>("/api/search", {
          q: query,
          mode: m,
          limit: SEARCH_LIMIT,
        });
        setHits(res.hits ?? []);
      } catch (e) {
        setErr(e instanceof ApiError ? e.problem.title : t("common.error"));
        setHits([]);
      } finally {
        setPending(false);
      }
    },
    [t]
  );

  // Auto-run once when the header box seeded `?q=` (deep link / `/`
  // focus). Intentionally mount-only: re-running on every param/mode
  // change would fire a search on each keystroke.
  const didSeed = useRef(false);
  useEffect(() => {
    if (didSeed.current) return;
    didSeed.current = true;
    const seeded = params.get("q");
    if (seeded) void runSearch(seeded, mode);
  }, [params, mode, runSearch]);

  function onChangeQuery(value: string) {
    setQ(value);
    if (suggestTimer.current) clearTimeout(suggestTimer.current);
    if (!value.trim()) {
      setSuggestions([]);
      return;
    }
    suggestTimer.current = setTimeout(() => {
      api
        .get<{ suggestions: string[] }>(`/api/search/suggest?q=${encodeURIComponent(value.trim())}`)
        .then((r) => setSuggestions(r.suggestions ?? []))
        .catch(() => setSuggestions([]));
    }, 200);
  }

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    setSuggestions([]);
    const nextParams = new URLSearchParams(params);
    if (q.trim()) nextParams.set("q", q.trim());
    else nextParams.delete("q");
    setParams(nextParams, { replace: true });
    void runSearch(q, mode);
  }

  function saveCurrent() {
    if (!q.trim()) return;
    void api
      .post("/api/search/save", { q: q.trim(), mode })
      .then(refreshSaved)
      .catch(() => {});
  }

  const shown = hits ? hits.slice(0, RESULT_WINDOW) : null;

  return (
    <section className="mkt-page mkt-search">
      <div className="mkt-search__layout">
        <div className="mkt-search__main">
          <form onSubmit={onSubmit} className="mkt-search__form" role="search">
            <input
              type="search"
              value={q}
              onChange={(e) => onChangeQuery(e.target.value)}
              placeholder={t("search.placeholder")}
              aria-label={t("nav.search")}
              list="mkt-suggest"
              autoFocus
            />
            <datalist id="mkt-suggest">
              {suggestions.map((s) => (
                <option key={s} value={s} />
              ))}
            </datalist>
            <select
              value={mode}
              onChange={(e) => setMode(e.target.value as Mode)}
              aria-label={t("search.mode")}
            >
              <option value="hybrid">{t("search.mode.hybrid")}</option>
              <option value="fts">{t("search.mode.fts")}</option>
              <option value="semantic">{t("search.mode.semantic")}</option>
            </select>
            <Button type="submit" loading={pending}>
              {t("nav.search")}
            </Button>
            <Button type="button" variant="secondary" onClick={saveCurrent}>
              {t("search.save")}
            </Button>
          </form>

          {err && <ErrorState kind="server" title={t("common.error")} description={err} />}

          {shown && shown.length === 0 && !err && (
            <EmptyState kind="filtered_out" title={t("search.didYouMean")} />
          )}

          {shown && shown.length > 0 && (
            <ul className="mkt-results">
              {shown.map((h) => (
                <li key={h.segment_id} className="mkt-result">
                  <Link to={`/videos/${h.video_id}/watch?t=${Math.floor(h.start_sec)}`}>
                    <span className="mkt-result__time">
                      [{ts(h.start_sec)} → {ts(h.end_sec)}]
                    </span>
                    <span className="mkt-result__snippet">{renderSnippet(h.snippet)}</span>
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </div>

        <aside className="mkt-search__side" aria-label={t("search.saved")}>
          <h2>{t("search.saved")}</h2>
          {saved.length === 0 ? (
            <p className="mkt-muted">{t("common.empty")}</p>
          ) : (
            <ul className="mkt-list">
              {saved.map((s) => (
                <li key={s.id}>
                  <button
                    type="button"
                    className="mkt-link-btn"
                    onClick={() => {
                      setQ(s.q);
                      setMode(s.mode as Mode);
                      void runSearch(s.q, s.mode as Mode);
                    }}
                  >
                    {s.q}
                  </button>
                  <button
                    type="button"
                    className="mkt-icon-btn"
                    aria-label={t("common.close")}
                    onClick={() =>
                      void api
                        .delete(`/api/search/saved/${encodeURIComponent(s.id)}`)
                        .then(refreshSaved)
                        .catch(() => {})
                    }
                  >
                    ×
                  </button>
                </li>
              ))}
            </ul>
          )}
        </aside>
      </div>
    </section>
  );
}
