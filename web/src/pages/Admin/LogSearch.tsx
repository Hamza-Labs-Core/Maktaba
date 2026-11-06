// The Logs page over the log index (Epic 53 story 53.11): search (words, "phrases", -word;
// a reference or a request id opens Follow a request), filters in the address (services,
// components, level, a time window, a machine), Live (the index's tail, paused while the
// admin reads further down, "N new lines" to go back up), each line with its ids as chips
// and "Lines around this", and Follow a request: every line of one request, from every
// service, in time order, with gaps over a second marked.
//
// lib/logIndex.ts has the routes. When the index does not answer (503: the logs role is
// off or restarting), Logs.tsx shows its files view instead, with a notice.
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Card } from "@ds/components/Card/Card";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { Select } from "@ds/components/Select/Select";
import { useI18n } from "../../lib/i18n";
import { downloadBlob } from "../../lib/api";
import { logsExportURL } from "../../lib/logLinks";
import {
  aroundLine,
  gapsOver,
  logUsers,
  looksLikeRequestId,
  searchLogs,
  tailURL,
  WINDOWS,
  windowSince,
  type IndexLine,
  type IndexQuery,
  type SearchResult,
  type WindowKey,
} from "../../lib/logIndex";

const LEVELS = ["debug", "info", "warn", "error"] as const;
const SERVICES = ["api", "streaming", "pipeline", "supervisor", "logs", "postgres", "web"] as const;
/** At most this many lines are kept on the page (the live tail drops the oldest). */
const KEEP = 2000;

function tone(level?: string): "neutral" | "accent" | "warn" | "error" {
  switch ((level ?? "").toLowerCase()) {
    case "error":
    case "fatal":
      return "error";
    case "warn":
      return "warn";
    case "debug":
      return "neutral";
    default:
      return "accent";
  }
}

/** Writes the filters into the address (other pages link here with them). */
function syncAddress(f: IndexQuery) {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(f)) if (v) p.set(k, String(v));
  const s = p.toString();
  window.history.replaceState(window.history.state, "", s ? `?${s}` : window.location.pathname);
}

export function LogSearch({ initial, first }: { initial: IndexQuery; first: SearchResult }) {
  const { t } = useI18n();
  const [f, setF] = useState<IndexQuery>(initial);
  const [words, setWords] = useState(initial.q ?? "");
  const [res, setRes] = useState<SearchResult>(first);
  const [err, setErr] = useState(false);
  const [live, setLive] = useState(false);
  const [around, setAround] = useState<{ id: number; res: SearchResult } | null>(null);
  const [users, setUsers] = useState<Record<string, string>>({});
  const loaded = useRef(true); // the first page came with the probe

  useEffect(() => {
    Promise.resolve()
      .then(() => logUsers())
      .then((u) => setUsers(u ?? {}))
      .catch(() => {});
  }, []);

  useEffect(() => syncAddress(f), [f]);

  const query = useMemo(() => ({ ...f }), [f]);
  useEffect(() => {
    if (loaded.current) {
      loaded.current = false;
      return;
    }
    let alive = true;
    searchLogs(query)
      .then((r) => {
        if (!alive) return;
        setRes({ ...r, lines: r?.lines ?? [], ids: r?.ids ?? [] });
        setErr(false);
      })
      .catch(() => alive && setErr(true));
    return () => {
      alive = false;
    };
  }, [query]);

  const onSearch = () => {
    const w = words.trim();
    if (looksLikeRequestId(w)) {
      setF((p) => ({ ...p, q: undefined, request: w.toLowerCase() }));
      return;
    }
    setF((p) => ({ ...p, q: w || undefined, request: undefined }));
  };

  const more = () => {
    if (!res.cursor) return;
    searchLogs(query, res.cursor)
      .then((r) =>
        setRes((p) => ({
          lines: [...p.lines, ...(r?.lines ?? [])],
          ids: [...p.ids, ...(r?.ids ?? [])],
          cursor: r?.cursor,
          partial: r?.partial,
        }))
      )
      .catch(() => setErr(true));
  };

  const follow = (rid: string) => {
    setLive(false);
    setWords(rid);
    setF((p) => ({ ...p, q: undefined, request: rid }));
  };

  const openAround = (id: number) => {
    aroundLine(id)
      .then((r) => setAround({ id, res: { ...r, lines: r?.lines ?? [], ids: r?.ids ?? [] } }))
      .catch(() => setErr(true));
  };

  const download = () =>
    downloadBlob(
      logsExportURL({
        services: f.services,
        level: f.level,
        since: f.since,
        until: f.until,
        node: f.node,
      }),
      "maktaba-logs.tar.gz"
    ).catch(() => setErr(true));

  if (f.request) {
    return (
      <FollowRequest
        rid={f.request}
        lines={res.lines}
        users={users}
        onBack={() => {
          setWords("");
          setF((p) => ({ ...p, request: undefined }));
        }}
      />
    );
  }

  return (
    <div data-testid="log-search">
      <div className="mkt-logs__toolbar">
        <label className="mkt-logs__filter mkt-logs__filter--grow">
          <span className="mkt-muted">{t("logs.search")}</span>
          <Input
            value={words}
            onChange={(e) => setWords(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") onSearch();
            }}
            placeholder={t("logs.filter.searchPlaceholder")}
            aria-label={t("logs.search")}
          />
        </label>
        <Button onClick={onSearch}>{t("logs.search")}</Button>
        <label className="mkt-logs__filter">
          <span className="mkt-muted">{t("logs.filter.level")}</span>
          <Select
            value={f.level ?? ""}
            onChange={(e) => setF((p) => ({ ...p, level: e.target.value || undefined }))}
          >
            <option value="">{t("logs.level.info")}</option>
            {LEVELS.map((lv) => (
              <option key={lv} value={lv}>
                {t(`logs.level.${lv}`)}
              </option>
            ))}
          </Select>
        </label>
        <label className="mkt-logs__filter">
          <span className="mkt-muted">{t("logs.filter.service")}</span>
          <Select
            value={f.services ?? ""}
            onChange={(e) => setF((p) => ({ ...p, services: e.target.value || undefined }))}
          >
            <option value="">{t("logs.filter.allServices")}</option>
            {SERVICES.map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </Select>
        </label>
        <label className="mkt-logs__filter">
          <span className="mkt-muted">{t("logs.filter.time")}</span>
          <Select
            value=""
            onChange={(e) => {
              const w = e.target.value as WindowKey | "";
              setF((p) => ({ ...p, since: w ? windowSince(w) : undefined, until: undefined }));
            }}
          >
            <option value="">{t("logs.filter.anyTime")}</option>
            {(Object.keys(WINDOWS) as WindowKey[]).map((w) => (
              <option key={w} value={w}>
                {t(`logs.window.${w}`)}
              </option>
            ))}
          </Select>
        </label>
        <div className="mkt-logs__actions">
          <label className="mkt-logs__toggle">
            <input type="checkbox" checked={live} onChange={(e) => setLive(e.target.checked)} />
            {t("logs.live")}
          </label>
          <Button variant="secondary" onClick={download}>
            {t("logs.export")}
          </Button>
        </div>
      </div>
      {f.components && (
        <p className="mkt-muted" data-testid="logs-component">
          {t("logs.filter.component")}: <bdi>{f.components}</bdi>{" "}
          <Button
            variant="secondary"
            size="sm"
            onClick={() => setF((p) => ({ ...p, components: undefined }))}
          >
            {t("logs.window.all")}
          </Button>
        </p>
      )}
      {err && <p className="mkt-muted">{t("logs.error")}</p>}
      {res.partial && <p className="mkt-muted">{t("logs.partial")}</p>}
      {live ? (
        <LiveTail query={query} users={users} onFollow={follow} onAround={openAround} />
      ) : (
        <Card className="mkt-logs__viewport">
          <div className="mkt-logs__scroll">
            {res.lines.length === 0 ? (
              <p className="mkt-muted mkt-logs__empty">{t("logs.empty")}</p>
            ) : (
              <ul className="mkt-logs__list">
                {res.lines.map((l, i) => (
                  <IndexRow
                    key={res.ids[i] ?? i}
                    line={l}
                    id={res.ids[i]}
                    users={users}
                    onFollow={follow}
                    onAround={openAround}
                  />
                ))}
              </ul>
            )}
            {res.cursor && (
              <Button variant="secondary" size="sm" onClick={more}>
                {t("logs.more")}
              </Button>
            )}
          </div>
        </Card>
      )}
      {around && (
        <Card className="mkt-logs__around" data-testid="logs-around">
          <header className="mkt-logs__around-head">
            <strong>{t("logs.around")}</strong>{" "}
            <Button variant="secondary" size="sm" onClick={() => setAround(null)}>
              {t("common.close")}
            </Button>
          </header>
          <ul className="mkt-logs__list">
            {around.res.lines.map((l, i) => (
              <IndexRow
                key={around.res.ids[i] ?? i}
                line={l}
                id={around.res.ids[i]}
                users={users}
                onFollow={follow}
                mark={around.res.ids[i] === around.id}
              />
            ))}
          </ul>
        </Card>
      )}
    </div>
  );
}

/** The live tail: new lines on top while the admin is at the top; reading further down
 * pauses it and counts what came, "N new lines" goes back up. */
function LiveTail({
  query,
  users,
  onFollow,
  onAround,
}: {
  query: IndexQuery;
  users: Record<string, string>;
  onFollow: (rid: string) => void;
  onAround: (id: number) => void;
}) {
  const { t } = useI18n();
  const [lines, setLines] = useState<{ id: number; line: IndexLine }[]>([]);
  const [waiting, setWaiting] = useState<{ id: number; line: IndexLine }[]>([]);
  const paused = useRef(false);
  const box = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (typeof EventSource === "undefined") return;
    const es = new EventSource(tailURL(query), { withCredentials: true });
    es.onmessage = (ev) => {
      let line: IndexLine;
      try {
        line = JSON.parse(ev.data) as IndexLine;
      } catch {
        return;
      }
      const item = { id: Number(ev.lastEventId) || 0, line };
      if (paused.current) setWaiting((w) => [item, ...w].slice(0, KEEP));
      else setLines((l) => [item, ...l].slice(0, KEEP));
    };
    return () => es.close();
  }, [query]);

  const onScroll = useCallback(() => {
    const top = (box.current?.scrollTop ?? 0) <= 4;
    paused.current = !top;
  }, []);

  const jump = () => {
    setLines((l) => [...waiting, ...l].slice(0, KEEP));
    setWaiting([]);
    paused.current = false;
    if (box.current) box.current.scrollTop = 0;
  };

  return (
    <Card className="mkt-logs__viewport" data-testid="logs-live">
      {waiting.length > 0 && (
        <Button size="sm" onClick={jump} data-testid="logs-new-lines">
          {t("logs.newLines", { count: waiting.length })}
        </Button>
      )}
      <div
        className="mkt-logs__scroll"
        ref={box}
        onScroll={onScroll}
        data-testid="logs-live-scroll"
      >
        {lines.length === 0 ? (
          <p className="mkt-muted mkt-logs__empty">{t("logs.liveWaiting")}</p>
        ) : (
          <ul className="mkt-logs__list">
            {lines.map((l, i) => (
              <IndexRow
                key={`${l.id}-${i}`}
                line={l.line}
                id={l.id || undefined}
                users={users}
                onFollow={onFollow}
                onAround={onAround}
              />
            ))}
          </ul>
        )}
      </div>
    </Card>
  );
}

/** One line: time, level, service and component, message, the ids as chips; expanded,
 * every field and "Lines around this". Its words stay as written (left to right). */
export function IndexRow({
  line,
  id,
  users,
  onFollow,
  onAround,
  mark,
  gap,
}: {
  line: IndexLine;
  id?: number;
  users: Record<string, string>;
  onFollow?: (rid: string) => void;
  onAround?: (id: number) => void;
  mark?: boolean;
  gap?: boolean;
}) {
  const { t, formatDate } = useI18n();
  const tn = tone(line.level);
  const chip = (label: string, value?: string) =>
    value ? (
      <span className="mkt-logs__chip" key={label}>
        {label}: <bdi dir="ltr">{value.length > 12 ? value.slice(0, 8) : value}</bdi>
      </span>
    ) : null;
  const user = line.user_h ? (users[line.user_h] ?? t("logs.unknownUser")) : "";
  return (
    <li
      className={`mkt-logs__row mkt-logs__row--${tn}${mark ? " mkt-logs__row--mark" : ""}${gap ? " mkt-logs__row--gap" : ""}`}
      data-testid="index-row"
    >
      <span className="mkt-logs__ts">{line.ts ? formatDate(line.ts) : "—"}</span>
      <Badge tone={tn} className="mkt-badge--sm">
        {line.level || "?"}
      </Badge>
      <span className="mkt-logs__service">
        {line.service}
        {line.component && <span className="mkt-logs__file"> · {line.component}</span>}
      </span>
      <span className="mkt-logs__msg" dir="ltr">
        {line.msg}
      </span>
      {line.request_id &&
        (onFollow ? (
          <button
            type="button"
            className="mkt-logs__chip mkt-logs__chip--link"
            onClick={() => onFollow(line.request_id as string)}
            title={t("logs.follow")}
          >
            {t("logs.chip.request")}: <bdi dir="ltr">{line.request_id.slice(0, 8)}</bdi>
          </button>
        ) : (
          chip(t("logs.chip.request"), line.request_id)
        ))}
      {chip(t("logs.chip.job"), line.job_id)}
      {chip(t("logs.chip.video"), line.video_id)}
      {chip(t("logs.chip.session"), line.session_id)}
      {user && (
        <span className="mkt-logs__chip">
          {t("logs.chip.user")}: <bdi>{user}</bdi>
        </span>
      )}
      <details className="mkt-logs__attrs">
        <summary aria-label={t("logs.details")} />
        <pre dir="ltr">{JSON.stringify(line, null, 2)}</pre>
        {id !== undefined && onAround && (
          <Button variant="secondary" size="sm" onClick={() => onAround(id)}>
            {t("logs.around")}
          </Button>
        )}
      </details>
    </li>
  );
}

/** Every line of one request, every service, in time order; a gap over a second is marked. */
function FollowRequest({
  rid,
  lines,
  users,
  onBack,
}: {
  rid: string;
  lines: IndexLine[];
  users: Record<string, string>;
  onBack: () => void;
}) {
  const { t } = useI18n();
  const gaps = gapsOver(lines);
  return (
    <section data-testid="follow-request">
      <p>
        <strong>{t("logs.follow")}</strong> <bdi dir="ltr">{rid}</bdi>{" "}
        <Button variant="secondary" size="sm" onClick={onBack}>
          {t("logs.back")}
        </Button>
      </p>
      <Card className="mkt-logs__viewport">
        {lines.length === 0 ? (
          <p className="mkt-muted mkt-logs__empty">{t("logs.followEmpty")}</p>
        ) : (
          <ol className="mkt-logs__list mkt-logs__timeline">
            {lines.map((l, i) => (
              <IndexRow key={i} line={l} users={users} gap={gaps[i]} />
            ))}
          </ol>
        )}
      </Card>
    </section>
  );
}
