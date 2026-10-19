// The log viewer for one machine or process: a live tail over the hub's
// `logs:<target>:<level>` topic (follow, pause, level, search), or an
// earlier day read from the files on the share (date, level, search on the
// server, "Load older", filter by process). At most MAX_ROWS rows are in
// the DOM.
import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { Select } from "@ds/components/Select/Select";
import { Toggle } from "@ds/components/Choice/Toggle";
import {
  clusterChannelApi,
  LOG_LEVELS,
  tsMs,
  type ClusterLogRecord,
  type FileNode,
  type LogLevel,
} from "../../lib/clusterApi";
import { atLeast, type ClusterStream } from "../../lib/clusterStream";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { useClusterFmt } from "./format";
import { nodeDays } from "./useClusterFiles";

export const MAX_ROWS = 2_000;
export const PAGE_SIZE = 500;

export interface LogTarget {
  /** the machine's IP (its folder on the share) */
  ip: string;
  /** one process (live tail by session); absent: the whole machine */
  session?: string | null;
  /** the process's worker id (filters the files) */
  workerId?: string | null;
  label: string;
  /** open on this day of the files instead of the live tail */
  date?: string;
}

type Row = ClusterLogRecord & { key: string };

function matches(r: ClusterLogRecord, q: string): boolean {
  if (!q) return true;
  const hay = `${r.event} ${r.logger ?? ""} ${r.worker_id ?? ""} ${r.fields ? JSON.stringify(r.fields) : ""}`;
  return hay.toLowerCase().includes(q.toLowerCase());
}

function fieldsText(fields: Record<string, unknown> | undefined): string {
  if (!fields) return "";
  return Object.entries(fields)
    .map(([k, v]) => `${k}=${typeof v === "string" ? v : JSON.stringify(v)}`)
    .join(" ");
}

let rowSeq = 0;

export function LogViewer({
  stream,
  target,
  node,
}: {
  stream: ClusterStream;
  target: LogTarget;
  /** the machine's folder in the files listing (days, processes) */
  node: FileNode | null;
}) {
  const { t, n } = useI18n();
  const f = useClusterFmt();
  // the share's days and the archive's: the history reaches back as far as the archive
  const days = useMemo(() => nodeDays(node, "logs"), [node]);
  const [mode, setMode] = useState<"live" | "history">(target.date ? "history" : "live");
  const [date, setDate] = useState(target.date ?? days[0] ?? "");
  const [level, setLevel] = useState<LogLevel>("info");
  const [search, setSearch] = useState("");
  const [serverQ, setServerQ] = useState("");
  const [worker, setWorker] = useState(target.workerId ?? "");
  const [follow, setFollow] = useState(true);
  const [frozen, setFrozen] = useState<Row[] | null>(null);
  const [records, setRecords] = useState<Row[]>([]);
  const [nextBefore, setNextBefore] = useState<string | number | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  const { subscribeLogs } = stream;
  const liveTarget = target.session || target.ip;

  // a new target opens as asked
  useEffect(() => {
    setMode(target.date ? "history" : "live");
    if (target.date) setDate(target.date);
    setWorker(target.workerId ?? "");
    setFrozen(null);
  }, [target.ip, target.session, target.workerId, target.date]);

  useEffect(() => {
    if (!date && days[0]) setDate(days[0]);
  }, [days, date]);

  // live: the hub's logs topic for this target and level
  useEffect(() => {
    if (mode !== "live") return;
    subscribeLogs({ target: liveTarget, level });
    return () => subscribeLogs(null);
  }, [mode, liveTarget, level, subscribeLogs]);

  // history: the day's files
  useEffect(() => {
    if (mode !== "history" || !date) return;
    let alive = true;
    setLoading(true);
    setError(false);
    clusterChannelApi
      .logs(target.ip, { date, level, q: serverQ || undefined, limit: PAGE_SIZE })
      .then((page) => {
        if (!alive) return;
        const list = Array.isArray(page?.records) ? page.records : [];
        setRecords(list.map((r) => ({ ...r, key: `h${++rowSeq}` })));
        setNextBefore(page?.next_before ?? null);
      })
      .catch(() => alive && setError(true))
      .finally(() => alive && setLoading(false));
    return () => {
      alive = false;
    };
  }, [mode, target.ip, date, level, serverQ]);

  const loadOlder = () => {
    if (nextBefore === null) return;
    setLoading(true);
    clusterChannelApi
      .logs(target.ip, {
        date,
        level,
        q: serverQ || undefined,
        limit: PAGE_SIZE,
        before: nextBefore,
      })
      .then((page) => {
        const list = Array.isArray(page?.records) ? page.records : [];
        setRecords((cur) => [...list.map((r) => ({ ...r, key: `h${++rowSeq}` })), ...cur]);
        setNextBefore(page?.next_before ?? null);
      })
      .catch(() => setError(true))
      .finally(() => setLoading(false));
  };

  const source: Row[] = mode === "live" ? (frozen ?? stream.logs) : records;
  const rows = useMemo(() => {
    const list = source.filter(
      (r) =>
        atLeast(r.level, level) &&
        (mode === "live" || !worker || r.worker_id === worker) &&
        matches(r, search)
    );
    if (mode === "history") list.sort((a, b) => (tsMs(a.ts) || 0) - (tsMs(b.ts) || 0));
    return list.slice(-MAX_ROWS);
  }, [source, level, worker, search, mode]);

  useEffect(() => {
    if (follow && mode === "live" && box.current) box.current.scrollTop = box.current.scrollHeight;
  }, [rows.length, follow, mode]);

  const onSearch = (e: FormEvent) => {
    e.preventDefault();
    if (mode === "history") setServerQ(search.trim());
  };

  const processes = node?.processes ?? [];
  const newWhilePaused = frozen ? Math.max(0, stream.logs.length - frozen.length) : 0;

  return (
    <section className="mkt-mc-card mkt-cl-logs" aria-labelledby="mkt-cl-logview-h">
      <h2 id="mkt-cl-logview-h" className="mkt-mc-name">
        {t("cluster.logs.of", { name: bidiIsolate(target.label) })}
      </h2>
      <div className="mkt-cl-seg" role="group" aria-label={t("cluster.logs.mode")}>
        <Button
          size="sm"
          variant={mode === "live" ? "primary" : "secondary"}
          aria-pressed={mode === "live"}
          onClick={() => setMode("live")}
        >
          {t("cluster.logs.live")}
        </Button>
        <Button
          size="sm"
          variant={mode === "history" ? "primary" : "secondary"}
          aria-pressed={mode === "history"}
          disabled={days.length === 0}
          onClick={() => setMode("history")}
        >
          {t("cluster.logs.history")}
        </Button>
      </div>
      <form className="mkt-cl-filters" onSubmit={onSearch} role="search">
        <Select
          label={t("cluster.logs.level")}
          value={level}
          options={LOG_LEVELS.map((l) => ({ value: l, label: t(`cluster.level.${l}`) }))}
          onChange={(e) => setLevel(e.currentTarget.value as LogLevel)}
        />
        <Input
          label={t("cluster.logs.search")}
          type="search"
          value={search}
          onChange={(e) => setSearch(e.currentTarget.value)}
        />
        {mode === "history" && (
          <>
            <Select
              label={t("cluster.logs.date")}
              value={date}
              options={days.map((d) => ({ value: d, label: d }))}
              onChange={(e) => setDate(e.currentTarget.value)}
            />
            {processes.length > 0 && (
              <Select
                label={t("cluster.logs.process")}
                value={worker}
                options={[
                  { value: "", label: t("cluster.logs.allProcesses") },
                  ...processes.map((p) => ({
                    value: p.worker_id,
                    label: `${p.worker_id}${p.role ? ` · ${p.role}` : ""}`,
                  })),
                ]}
                onChange={(e) => setWorker(e.currentTarget.value)}
              />
            )}
            <Button size="sm" type="submit" variant="secondary">
              {t("cluster.logs.searchFiles")}
            </Button>
          </>
        )}
        {mode === "live" && (
          <>
            <Toggle
              label={t("cluster.logs.follow")}
              checked={follow}
              onChange={(e) => setFollow(e.currentTarget.checked)}
            />
            <Button
              size="sm"
              type="button"
              variant="secondary"
              aria-pressed={frozen !== null}
              onClick={() => setFrozen(frozen ? null : stream.logs)}
            >
              {frozen ? t("cluster.logs.resume") : t("cluster.logs.pause")}
            </Button>
            <Button size="sm" type="button" variant="ghost" onClick={() => stream.clearLogs()}>
              {t("cluster.logs.clear")}
            </Button>
          </>
        )}
      </form>
      <p className="mkt-mc-muted" role="status">
        {mode === "live"
          ? frozen
            ? t("cluster.logs.paused", { n: n(newWhilePaused) })
            : t("cluster.logs.tailing", { n: n(rows.length) })
          : loading
            ? t("common.loading")
            : error
              ? t("cluster.logs.error")
              : t("cluster.logs.shown", { n: n(rows.length), date })}
      </p>
      {mode === "history" && nextBefore !== null && (
        <Button size="sm" variant="ghost" disabled={loading} onClick={loadOlder}>
          {t("cluster.logs.older")}
        </Button>
      )}
      <div
        ref={box}
        className="mkt-cl-logbox"
        role="log"
        aria-label={t("cluster.logs.records")}
        tabIndex={0}
        dir="ltr"
      >
        {rows.length === 0 ? (
          <p className="mkt-mc-muted">{t("cluster.logs.empty")}</p>
        ) : (
          <ol className="mkt-cl-loglist">
            {rows.map((r) => {
              const ms = tsMs(r.ts);
              return (
                <li
                  key={r.key}
                  className={`mkt-cl-logrow mkt-cl-logrow--${String(r.level).toLowerCase()}`}
                >
                  <time className="mkt-cl-logrow__ts">
                    {Number.isFinite(ms) ? f.time(ms) : String(r.ts)}
                  </time>{" "}
                  <span className="mkt-cl-logrow__level">{String(r.level).toUpperCase()}</span>{" "}
                  {r.worker_id && <span className="mkt-cl-logrow__worker">{r.worker_id}</span>}{" "}
                  <span className="mkt-cl-logrow__event">{r.event}</span>{" "}
                  {r.logger && <span className="mkt-cl-logrow__logger">{r.logger}</span>}{" "}
                  <span className="mkt-cl-logrow__fields">{fieldsText(r.fields)}</span>
                </li>
              );
            })}
          </ol>
        )}
      </div>
    </section>
  );
}
