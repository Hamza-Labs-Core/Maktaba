// Admin log viewer (troubleshooting / diagnostics surface).
//
// Real contracts:
//   GET /api/admin/logs/stream?level=&services=&q=&limit=  (admin)
//        → { entries: <raw JSON log line>[], count }
//   GET /api/admin/logs/export?format=&since=&services=&level=  (admin)
//        → .tar.gz diagnostics bundle (Content-Disposition download)
//
// The viewer polls /stream on an interval (pausable), colour-codes by
// level, and filters client- + server-side. Each entry is the verbatim
// structured JSON line from the service ring buffers, so we parse the
// common contract fields (ts/level/service/msg) for display and keep
// the rest as an expandable detail blob.
//
// Its filters can come in the address (a failure elsewhere links here: update-attempts.md):
// ?q=, ?services=, ?level=, and ?since= / ?until= (ISO times: lines outside are hidden).
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Card } from "@ds/components/Card/Card";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { Select } from "@ds/components/Select/Select";
import { useI18n } from "../../lib/i18n";
import { api, downloadBlob, ApiError } from "../../lib/api";
import { useToast } from "@ds/components/Toast/Toast";
import { AdminGate } from "../../components/AdminGate";
import { TitleRow } from "../../help/HelpLink";
import { inWindow, readLogFilters, type LogFilters } from "../../lib/logLinks";

interface StreamResponse {
  entries?: unknown[] | null;
  count: number;
}

// LogLine is the parsed view of one structured entry. Unknown extra
// fields are retained in `attrs` for the expandable detail row.
interface LogLine {
  ts: string;
  level: string;
  service: string;
  msg: string;
  attrs: Record<string, unknown>;
  raw: Record<string, unknown>;
}

const LEVELS = ["debug", "info", "warn", "error"] as const;
const POLL_MS = 3000;
const POLL_LIMIT = 1000;

export function AdminLogs() {
  return (
    <AdminGate>
      <LogsInner />
    </AdminGate>
  );
}

/** The filters the page opened with (its address). */
function initialFilters(): LogFilters {
  return readLogFilters(typeof window === "undefined" ? "" : window.location.search);
}

function LogsInner() {
  const { t, formatDate } = useI18n();
  const toast = useToast();

  const [initial] = useState(initialFilters);
  const [lines, setLines] = useState<LogLine[]>([]);
  const [level, setLevel] = useState<string>(
    LEVELS.includes(initial.level as (typeof LEVELS)[number]) ? (initial.level as string) : "debug"
  );
  const [service, setService] = useState<string>(initial.services ?? "");
  const [search, setSearch] = useState<string>(initial.q ?? "");
  // a failure's time window (from the address); "Show all" lets it go
  const [range, setRange] = useState<Pick<LogFilters, "since" | "until">>({
    since: initial.since,
    until: initial.until,
  });
  const windowed = Boolean(range.since || range.until);
  const [paused, setPaused] = useState(false);
  const [autoScroll, setAutoScroll] = useState(true);
  const [exporting, setExporting] = useState(false);
  const [err, setErr] = useState(false);

  const scrollRef = useRef<HTMLDivElement | null>(null);

  const query = useMemo(() => {
    const p = new URLSearchParams();
    if (level) p.set("level", level);
    if (service) p.set("services", service);
    if (search.trim()) p.set("q", search.trim());
    p.set("limit", String(POLL_LIMIT));
    return p.toString();
  }, [level, service, search]);

  const refresh = useCallback(() => {
    api
      .get<StreamResponse>(`/api/admin/logs/stream?${query}`)
      .then((r) => {
        // a null body or list is empty; one bad entry never drops the batch
        const entries = Array.isArray(r?.entries) ? r.entries : [];
        setLines(entries.flatMap((e) => parseLine(e) ?? []));
        setErr(false);
      })
      .catch(() => setErr(true));
  }, [query]);

  // Poll on an interval unless paused. A filter change refreshes
  // immediately (refresh identity changes with the query).
  useEffect(() => {
    refresh();
    if (paused) return;
    const id = setInterval(refresh, POLL_MS);
    return () => clearInterval(id);
  }, [refresh, paused]);

  // Keep the view pinned to the newest line while auto-scroll is on.
  useEffect(() => {
    if (autoScroll && scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [lines, autoScroll]);

  const onExport = useCallback(async () => {
    setExporting(true);
    try {
      const p = new URLSearchParams();
      if (level) p.set("level", level);
      if (service) p.set("services", service);
      if (range.since) p.set("since", range.since);
      await downloadBlob(`/api/admin/logs/export?${p.toString()}`, "maktaba-diagnostics.tar.gz");
      toast.show({ tone: "success", message: t("logs.export.success") });
    } catch (e) {
      const detail = e instanceof ApiError ? e.message : t("common.error");
      toast.show({ tone: "error", message: `${t("logs.export.error")}: ${detail}` });
    } finally {
      setExporting(false);
    }
  }, [level, service, range.since, toast, t]);

  // Service options are derived from whatever services appear in the
  // current window, so a single-service deploy shows just its own.
  const services = useMemo(() => {
    const set = new Set<string>();
    lines.forEach((l) => l.service && set.add(l.service));
    if (service) set.add(service); // one the address asked for, before its lines come
    return Array.from(set).sort();
  }, [lines, service]);

  // the lines of the window (all of them without one)
  const shown = useMemo(
    () => (windowed ? lines.filter((l) => inWindow(l.ts, range)) : lines),
    [lines, range, windowed]
  );

  return (
    <section className="mkt-page mkt-logs">
      <header className="mkt-page__header">
        <TitleRow>
          <h1>{t("logs.title")}</h1>
        </TitleRow>
        <span className="mkt-muted">{t("logs.subtitle")}</span>
      </header>

      <div className="mkt-logs__toolbar">
        <label className="mkt-logs__filter">
          <span className="mkt-muted">{t("logs.filter.level")}</span>
          <Select value={level} onChange={(e) => setLevel(e.target.value)}>
            {LEVELS.map((lv) => (
              <option key={lv} value={lv}>
                {t(`logs.level.${lv}`)}
              </option>
            ))}
          </Select>
        </label>

        <label className="mkt-logs__filter">
          <span className="mkt-muted">{t("logs.filter.service")}</span>
          <Select value={service} onChange={(e) => setService(e.target.value)}>
            <option value="">{t("logs.filter.allServices")}</option>
            {services.map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </Select>
        </label>

        <label className="mkt-logs__filter mkt-logs__filter--grow">
          <span className="mkt-muted">{t("logs.filter.search")}</span>
          <Input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder={t("logs.filter.searchPlaceholder")}
          />
        </label>

        <div className="mkt-logs__actions">
          <Button variant="secondary" onClick={() => setPaused((p) => !p)}>
            {paused ? t("logs.resume") : t("logs.pause")}
          </Button>
          <label className="mkt-logs__toggle">
            <input
              type="checkbox"
              checked={autoScroll}
              onChange={(e) => setAutoScroll(e.target.checked)}
            />
            {t("logs.autoScroll")}
          </label>
          <Button onClick={onExport} disabled={exporting}>
            {exporting ? t("logs.export.running") : t("logs.export")}
          </Button>
        </div>
      </div>

      {err && <p className="mkt-muted">{t("logs.error")}</p>}

      {windowed && (
        <p className="mkt-logs__window" role="status" data-testid="logs-window">
          {range.since && range.until
            ? t("logs.window.between", {
                since: formatDate(range.since),
                until: formatDate(range.until),
              })
            : range.since
              ? t("logs.window.since", { since: formatDate(range.since) })
              : t("logs.window.until", { until: formatDate(range.until as string) })}{" "}
          <Button variant="secondary" size="sm" onClick={() => setRange({})}>
            {t("logs.window.all")}
          </Button>
        </p>
      )}

      <Card className="mkt-logs__viewport">
        <div className="mkt-logs__scroll" ref={scrollRef}>
          {shown.length === 0 ? (
            <p className="mkt-muted mkt-logs__empty">
              {windowed && lines.length > 0 ? t("logs.window.empty") : t("logs.empty")}
            </p>
          ) : (
            <ul className="mkt-logs__list">
              {shown.map((l, i) => (
                <LogRow key={i} line={l} formatDate={formatDate} />
              ))}
            </ul>
          )}
        </div>
      </Card>
    </section>
  );
}

function LogRow({
  line,
  formatDate,
}: {
  line: LogLine;
  formatDate: (v: string | number | Date) => string;
}) {
  const tone = levelTone(line.level);
  const hasAttrs = Object.keys(line.attrs).length > 0;
  return (
    <li className={`mkt-logs__row mkt-logs__row--${tone}`}>
      <span className="mkt-logs__ts">{line.ts ? formatDate(line.ts) : "—"}</span>
      <Badge tone={tone} className="mkt-badge--sm">
        {line.level || "?"}
      </Badge>
      {line.service && <span className="mkt-logs__service">{line.service}</span>}
      <span className="mkt-logs__msg">{line.msg}</span>
      {hasAttrs && (
        <details className="mkt-logs__attrs">
          <summary aria-label="details" />
          <pre>{JSON.stringify(line.attrs, null, 2)}</pre>
        </details>
      )}
    </li>
  );
}

// levelTone maps a level string to a design-system Badge tone (and the
// row colour class): debug=gray/neutral, info=blue/accent, warn=yellow,
// error=red. (The Badge palette has no "info" tone, so info maps to
// "accent" — the blue brand colour.)
function levelTone(level: string): "neutral" | "accent" | "warn" | "error" {
  switch (level.toLowerCase()) {
    case "error":
      return "error";
    case "warn":
    case "warning":
      return "warn";
    case "debug":
      return "neutral";
    default:
      return "accent";
  }
}

// parseLine splits the structured record into the display fields and the
// remaining attributes (everything that isn't a base-contract field).
// A null entry is skipped; a bare string or number is shown as the message.
function parseLine(entry: unknown): LogLine | null {
  if (entry == null) return null;
  if (typeof entry !== "object" || Array.isArray(entry)) {
    const msg = typeof entry === "string" ? entry : JSON.stringify(entry);
    return { ts: "", level: "", service: "", msg, attrs: {}, raw: {} };
  }
  const rec = entry as Record<string, unknown>;
  const base = new Set(["ts", "level", "service", "msg", "version", "env"]);
  const attrs: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(rec)) {
    if (!base.has(k)) attrs[k] = v;
  }
  return {
    ts: tsText(rec.ts),
    level: String(rec.level ?? ""),
    service: String(rec.service ?? ""),
    msg: rec.msg == null ? "" : typeof rec.msg === "string" ? rec.msg : JSON.stringify(rec.msg),
    attrs,
    raw: rec,
  };
}

// tsText reads a numeric ts as epoch seconds (or milliseconds when it is
// that large) so it formats as a date instead of a bare number.
function tsText(ts: unknown): string {
  if (typeof ts === "number" && Number.isFinite(ts)) {
    return new Date(ts < 1e12 ? ts * 1000 : ts).toISOString();
  }
  return String(ts ?? "");
}
