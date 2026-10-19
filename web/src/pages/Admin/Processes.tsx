// Process supervision card (Story 22.9) on the System Health page.
//
// Real contract:
//   GET /api/system/processes (admin)
//     { available, reason?, host?, processes: [ProcessStatus], events: [ProcessEvent] }
//
// maktaba-server's supervisor restarts the api, streaming (live TV) and
// pipeline processes on their own; this card shows each one's state,
// how often it has been restarted and why it last went down. Without the
// supervisor (compose, a single --role) the API answers available=false
// and the card says so.
import { useCallback, useEffect, useState } from "react";
import { Card } from "@ds/components/Card/Card";
import { Badge } from "@ds/components/Badge/Badge";
import { useI18n } from "../../lib/i18n";
import { api } from "../../lib/api";

export interface ProcessExit {
  at?: string;
  code: number;
  signal?: string;
  reason: string;
  error?: string;
  uptime_sec: number;
}

export interface ProcessStatus {
  role: string;
  state: string;
  pid?: number;
  restarts: number;
  crash_loop?: boolean;
  uptime_sec?: number;
  last_exit?: ProcessExit;
  last_restart_at?: string;
  next_restart_at?: string;
  last_probe?: { at: string; ok: boolean; error?: string; consecutive_failures: number };
}

export interface ProcessEvent {
  seq: number;
  at: string;
  role: string;
  kind: string;
  reason?: string;
  code?: number;
  signal?: string;
  message?: string;
}

export interface ProcessesDoc {
  available: boolean;
  reason?: string;
  processes: ProcessStatus[];
  events: ProcessEvent[];
}

const REFRESH_MS = 15_000;

type Tone = "neutral" | "accent" | "success" | "warn" | "error";

const STATE_TONE: Record<string, Tone> = {
  running: "success",
  starting: "accent",
  unhealthy: "warn",
  backoff: "warn",
  failed: "error",
  exited: "neutral",
  stopped: "neutral",
};

// Exits worth listing as "recent crashes": not clean exits or shutdowns.
const FAILURE_REASONS = new Set(["crash", "hang", "start_failed"]);

export function ProcessesCard() {
  const { t } = useI18n();
  const [doc, setDoc] = useState<ProcessesDoc | null>(null);
  const [err, setErr] = useState(false);

  const refresh = useCallback(() => {
    api
      .get<ProcessesDoc>("/api/system/processes")
      .then((d) => {
        setDoc(d);
        setErr(false);
      })
      .catch(() => setErr(true));
  }, []);

  useEffect(() => {
    refresh();
    const id = setInterval(refresh, REFRESH_MS);
    return () => clearInterval(id);
  }, [refresh]);

  const crashes = (doc?.events ?? [])
    .filter((e) => e.kind === "exited" && FAILURE_REASONS.has(e.reason ?? ""))
    .slice(-5)
    .reverse();

  return (
    <Card header={<strong>{t("health.processes")}</strong>} className="mkt-processes">
      {err ? (
        <p className="mkt-muted">{t("health.section.error")}</p>
      ) : !doc ? (
        <p className="mkt-loading">{t("common.loading")}</p>
      ) : !doc.available ? (
        <p className="mkt-muted">{t("health.processes.unavailable")}</p>
      ) : (
        <>
          <div className="mkt-stage-cards">
            {doc.processes.map((p) => (
              <ProcessRow key={p.role} p={p} />
            ))}
          </div>
          <h3>{t("health.processes.recent")}</h3>
          {crashes.length === 0 ? (
            <p className="mkt-muted">{t("health.processes.noCrashes")}</p>
          ) : (
            <ul className="mkt-kv">
              {crashes.map((e) => (
                <CrashLine key={e.seq} e={e} />
              ))}
            </ul>
          )}
        </>
      )}
    </Card>
  );
}

function roleName(t: (k: string) => string, role: string): string {
  const key = `health.processes.role.${role}`;
  const v = t(key);
  return v === key ? role : v;
}

function ProcessRow({ p }: { p: ProcessStatus }) {
  const { t, n, formatDate } = useI18n();
  const stateKey = `health.processes.state.${p.state}`;
  const stateLabel = t(stateKey) === stateKey ? p.state : t(stateKey);
  const failed = p.last_exit && FAILURE_REASONS.has(p.last_exit.reason) ? p.last_exit : null;
  return (
    <Card className="mkt-service-card" data-testid={`process-${p.role}`}>
      <div className="mkt-service-card__head">
        <strong>{roleName(t, p.role)}</strong>
        <Badge tone={STATE_TONE[p.state] ?? "neutral"}>{stateLabel}</Badge>
      </div>
      <ul className="mkt-kv">
        <li>
          <span className="mkt-muted">{t("health.processes.restarts")}</span>
          <span>{n(p.restarts)}</span>
        </li>
        {p.pid ? (
          <li>
            <span className="mkt-muted">{t("health.processes.uptime")}</span>
            <span>{humanDuration(p.uptime_sec ?? 0, t, n)}</span>
          </li>
        ) : null}
        {p.next_restart_at && (
          <li>
            <span className="mkt-muted">{t("health.processes.nextRestart")}</span>
            <span>{formatDate(p.next_restart_at)}</span>
          </li>
        )}
        {failed && (
          <li>
            <span className="mkt-muted">{t("health.processes.lastCrash")}</span>
            <span>{exitReason(t, failed)}</span>
          </li>
        )}
        {p.last_probe && !p.last_probe.ok && (
          <li>
            <span className="mkt-muted">{t("health.processes.probe")}</span>
            <span>
              {t("health.processes.probeFailing", { n: n(p.last_probe.consecutive_failures) })}
            </span>
          </li>
        )}
      </ul>
      {p.crash_loop && <p className="mkt-muted">{t("health.processes.crashLoop")}</p>}
    </Card>
  );
}

function CrashLine({ e }: { e: ProcessEvent }) {
  const { t, formatDate } = useI18n();
  return (
    <li>
      <span className="mkt-muted">{formatDate(e.at)}</span>
      <span>
        {roleName(t, e.role)} —{" "}
        {exitReason(t, { reason: e.reason ?? "crash", code: e.code ?? -1, signal: e.signal })}
      </span>
    </li>
  );
}

function exitReason(
  t: (k: string, v?: Record<string, string | number>) => string,
  x: { reason: string; code: number; signal?: string }
): string {
  if (x.reason === "hang") return t("health.processes.reason.hang");
  if (x.reason === "start_failed") return t("health.processes.reason.startFailed");
  if (x.signal) return t("health.processes.reason.signal", { signal: x.signal });
  return t("health.processes.reason.crash", { code: x.code });
}

function humanDuration(
  sec: number,
  t: (k: string, v?: Record<string, string | number>) => string,
  n: (v: number) => string
): string {
  const s = Math.max(0, Math.floor(sec));
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (d > 0) return t("health.processes.dur.days", { d: n(d), h: n(h) });
  if (h > 0) return t("health.processes.dur.hours", { h: n(h), m: n(m) });
  if (m > 0) return t("health.processes.dur.minutes", { m: n(m) });
  return t("health.processes.dur.seconds", { s: n(s) });
}
