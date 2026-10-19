// Admin audit log (Story 10.16 security feed + Story 9.17 library feed).
//
// Real contracts:
//   GET /api/security/audit?limit=&cursor=    { items: [Entry] }
//       cursor is an RFC3339Nano timestamp (the boundary row's ts).
//   GET /api/libraries/{id}/audit?limit=&cursor=
//       { items: [Entry], next_cursor? }   cursor is the integer id.
//
// Both feeds are newest-first and expose NO server-side filtering — only
// limit + cursor. Date-range / actor / event filters are therefore
// applied client-side over the rows already loaded, and "load more"
// pages with the source-appropriate cursor. CSV export serialises the
// currently filtered rows.
import { useEffect, useMemo, useRef, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Select } from "@ds/components/Select/Select";
import { Input } from "@ds/components/Input/Input";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { api, ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import { AdminGate } from "../../components/AdminGate";

interface AuditEntry {
  id: number;
  event: string;
  actor_user_id?: string;
  target_id?: string;
  payload?: unknown;
  ts: string;
}

type Source = "security" | "library";
const PAGE = 50;

export function AdminAuditLog() {
  return (
    <AdminGate>
      <AuditInner />
    </AdminGate>
  );
}

function AuditInner() {
  const { t, formatDate } = useI18n();
  const [source, setSource] = useState<Source>("security");
  const [libraryId, setLibraryId] = useState("");
  const [rows, setRows] = useState<AuditEntry[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);
  // a failed "Load more" keeps the loaded rows and shows its error inline
  const [moreError, setMoreError] = useState<string | null>(null);
  const [exhausted, setExhausted] = useState(false);

  // client-side filters
  const [eventFilter, setEventFilter] = useState("");
  const [actorFilter, setActorFilter] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");

  function endpoint(cursor?: string | number): string | null {
    if (source === "security") {
      const c = cursor ? `&cursor=${encodeURIComponent(String(cursor))}` : "";
      return `/api/security/audit?limit=${PAGE}${c}`;
    }
    const id = libraryId.trim();
    if (!id) return null;
    const c = cursor ? `&cursor=${encodeURIComponent(String(cursor))}` : "";
    return `/api/libraries/${encodeURIComponent(id)}/audit?limit=${PAGE}${c}`;
  }

  // Each request takes a sequence number; a response that is no longer the
  // latest (the source or library id changed meanwhile) is dropped.
  const seq = useRef(0);

  async function load(reset: boolean) {
    const cursor = reset ? undefined : nextCursor(rows, source);
    const url = endpoint(cursor);
    const mine = ++seq.current;
    setMoreError(null);
    if (!url) {
      setRows(null);
      setError(null);
      setLoadingMore(false);
      return;
    }
    if (reset) {
      setRows(null);
      setExhausted(false);
    } else {
      setLoadingMore(true);
    }
    try {
      const res = await api.get<{ items?: AuditEntry[] | null }>(url);
      if (mine !== seq.current) return;
      const items = Array.isArray(res?.items) ? res.items : [];
      setError(null);
      setExhausted(items.length < PAGE);
      setRows((prev) => (reset || !prev ? items : [...prev, ...items]));
    } catch (e) {
      if (mine !== seq.current) return;
      const msg = e instanceof ApiError ? e.problem.detail || e.problem.title : t("common.error");
      if (reset) {
        setError(msg);
        setRows([]);
      } else {
        setMoreError(msg);
      }
    } finally {
      if (mine === seq.current) setLoadingMore(false);
    }
  }

  // Reload whenever the source (or the library id for the library feed)
  // changes. The library feed waits for a non-empty id.
  useEffect(() => {
    void load(true);
  }, [source, libraryId]);

  const events = useMemo(() => {
    const set = new Set<string>();
    (rows ?? []).forEach((r) => {
      if (typeof r.event === "string" && r.event) set.add(r.event);
    });
    return [...set].sort();
  }, [rows]);

  const filtered = useMemo(() => {
    const fromTs = from ? new Date(from).getTime() : null;
    // "To" is minute-precise, so it includes the whole selected minute.
    const toTs = to ? new Date(to).getTime() + 59_999 : null;
    return (rows ?? []).filter((r) => {
      if (eventFilter && r.event !== eventFilter) return false;
      if (actorFilter && !(r.actor_user_id ?? "").includes(actorFilter)) return false;
      const ts = new Date(r.ts).getTime();
      if (fromTs !== null && ts < fromTs) return false;
      if (toTs !== null && ts > toTs) return false;
      return true;
    });
  }, [rows, eventFilter, actorFilter, from, to]);

  function exportCsv() {
    const header = ["id", "ts", "event", "actor_user_id", "target_id", "payload"];
    const lines = [header.join(",")];
    for (const r of filtered) {
      lines.push(
        [
          r.id,
          r.ts,
          r.event,
          r.actor_user_id ?? "",
          r.target_id ?? "",
          JSON.stringify(r.payload ?? {}),
        ]
          .map(csvCell)
          .join(",")
      );
    }
    // BOM first so Excel reads the file as UTF-8 (Arabic text survives).
    const blob = new Blob(["\uFEFF" + lines.join("\n")], { type: "text/csv;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `audit-${source}.csv`;
    a.click();
    URL.revokeObjectURL(url);
  }

  const needLibraryId = source === "library" && !libraryId.trim();

  const loadMore = exhausted ? null : (
    <div className="mkt-loadmore">
      {moreError && (
        <p className="mkt-form__warn" role="alert">
          {moreError}
        </p>
      )}
      <Button variant="secondary" loading={loadingMore} onClick={() => load(false)}>
        {moreError ? t("common.retry") : t("common.loadMore")}
      </Button>
    </div>
  );

  return (
    <section className="mkt-page mkt-admin">
      <header className="mkt-page__header">
        <h1>{t("audit.title")}</h1>
        <div className="mkt-toolbar" role="toolbar">
          <Button variant="secondary" onClick={() => load(true)}>
            {t("common.refresh")}
          </Button>
          <Button variant="secondary" onClick={exportCsv} disabled={filtered.length === 0}>
            {t("common.export")}
          </Button>
        </div>
      </header>

      <div className="mkt-filters">
        <Select
          label={t("audit.source")}
          value={source}
          onChange={(e) => {
            setSource(e.target.value as Source);
            setEventFilter("");
          }}
          options={[
            { value: "security", label: t("audit.source.security") },
            { value: "library", label: t("audit.source.library") },
          ]}
        />
        {source === "library" && (
          <Input
            label={t("audit.libraryId")}
            value={libraryId}
            onChange={(e) => setLibraryId(e.target.value)}
            className="mkt-mono"
          />
        )}
        <Select
          label={t("audit.filter.event")}
          value={eventFilter}
          onChange={(e) => setEventFilter(e.target.value)}
          options={[
            { value: "", label: t("audit.filter.allEvents") },
            ...events.map((ev) => ({ value: ev, label: ev })),
          ]}
        />
        <Input
          label={t("audit.filter.actor")}
          value={actorFilter}
          onChange={(e) => setActorFilter(e.target.value)}
        />
        <Input
          type="datetime-local"
          label={t("audit.filter.from")}
          value={from}
          onChange={(e) => setFrom(e.target.value)}
        />
        <Input
          type="datetime-local"
          label={t("audit.filter.to")}
          value={to}
          onChange={(e) => setTo(e.target.value)}
        />
      </div>

      {needLibraryId ? (
        <EmptyState kind="filtered_out" title={t("audit.needLibraryId")} />
      ) : error ? (
        <ErrorState
          kind="server"
          title={t("common.error")}
          description={error}
          action={
            <Button variant="secondary" onClick={() => load(true)}>
              {t("common.retry")}
            </Button>
          }
        />
      ) : rows === null ? (
        <p className="mkt-loading">{t("common.loading")}</p>
      ) : filtered.length === 0 ? (
        <>
          <EmptyState
            kind="filtered_out"
            title={t("audit.empty.title")}
            description={t("audit.empty.desc")}
          />
          {/* filters can hide every loaded row; older pages may still match */}
          {rows.length > 0 && loadMore}
        </>
      ) : (
        <>
          <table className="mkt-table" aria-label={t("audit.title")}>
            <thead>
              <tr>
                <th>{t("audit.col.ts")}</th>
                <th>{t("audit.col.event")}</th>
                <th>{t("audit.col.actor")}</th>
                <th>{t("audit.col.target")}</th>
                <th>{t("audit.col.payload")}</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((r) => (
                <tr key={r.id}>
                  <td>{validTs(r.ts) ? formatDate(r.ts) : "—"}</td>
                  <td>{r.event}</td>
                  <td className="mkt-mono mkt-truncate">{r.actor_user_id ?? "—"}</td>
                  <td className="mkt-mono mkt-truncate">{r.target_id ?? "—"}</td>
                  <td className="mkt-mono mkt-audit__payload">{payloadText(r.payload)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {loadMore}
        </>
      )}
    </section>
  );
}

// nextCursor derives the pagination cursor from the last loaded row:
// the security feed pages by the boundary timestamp, the library feed by
// the integer id.
function nextCursor(rows: AuditEntry[] | null, source: Source): string | number | undefined {
  if (!rows || rows.length === 0) return undefined;
  const last = rows[rows.length - 1];
  return source === "security" ? last.ts : last.id;
}

function validTs(ts: unknown): ts is string {
  return typeof ts === "string" && ts !== "" && !Number.isNaN(Date.parse(ts));
}

function payloadText(p: unknown): string {
  if (p == null) return "";
  if (typeof p === "string") return p;
  try {
    return JSON.stringify(p);
  } catch {
    return String(p);
  }
}

// csvCell quotes a CSV cell and neutralises spreadsheet formulas: a cell
// starting with =, +, - or @ gets a leading ' so Excel / Sheets show it as
// text instead of running it (audit rows carry user-chosen values).
function csvCell(v: unknown): string {
  let s = String(v ?? "");
  if (/^[=+\-@]/.test(s)) s = `'${s}`;
  return /[",\n\r]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
}
