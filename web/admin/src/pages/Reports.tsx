// /reports — the problem reports homes chose to send to Maktaba (Epic 53 story 53.16),
// for the service admin only (a home admin's token gets 403: a home has its reports at home).
// The list across homes (home, app, version, category, status, age) with filters; a report:
// its text, device and merged timeline (the app's lines and the home's by time), status (New,
// Triaged, Fixed in {version}, Closed), a link to a GitHub issue pasted by hand, and a reply
// the home's admin and the report's sender see. A report holds no title, path, name or
// address (the home's export scrub) and is kept 90 days. Its Apple crashes read as functions,
// files and lines where the release's symbol tables are uploaded (/symbols, 53.14), its
// Android crashes where the release's R8 mapping is, its Android native crashes where the
// app's own libraries' tables are (keyed by build id), and its desktop crashes as they came
// (the desktop player is not obfuscated); the binaries, mappings and libraries without one
// are named. A native crash kept with its reason alone (Android 11) has no frames.
import { useEffect, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { Input } from "@ds/components/Input/Input";
import { Textarea } from "@ds/components/Textarea/Textarea";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { Select } from "@ds/components/Select/Select";
import { api, fetchApi, withQuery } from "@cloud-shared/api";
import { useI18n } from "@cloud-shared/i18n";
import { LoadError, Loading, Page, useErrorMessage, useLoad } from "@cloud-shared/ui";
import { Pager } from "./shared";

export const REPORT_STATUSES = ["new", "triaged", "fixed", "closed"] as const;
export const REPORT_CATEGORIES = ["playback", "live_tv", "search", "signing_in", "other"] as const;

export interface CloudReport {
  id: number;
  server_id: string;
  home: string;
  home_report_id: number;
  received_at: string;
  happened_at?: string;
  app: string;
  version: string;
  platform: string;
  category: string;
  status: (typeof REPORT_STATUSES)[number];
  fixed_in?: string;
  issue_url?: string;
  reply?: string;
  replied_at?: string;
  screenshot: boolean;
  fingerprints: string[];
}

interface Line {
  ts?: string;
  level?: string;
  service?: string;
  msg?: string;
  [k: string]: unknown;
}

export interface CrashFrame {
  raw: string;
  in_app: boolean;
  symbol?: { function: string; file?: string; line?: number };
  // R8 inlined this frame into the next one (an Android release's mapping, 53.16)
  inlined?: boolean;
}

export interface ReportCrash {
  type: string;
  value?: string;
  frames: CrashFrame[];
  // a binary's missing dSYM, (kind "r8") an Android release's missing mapping, or (kind "elf")
  // an Android app's own native library without a table, with its build id when known
  missing?: {
    code_file: string;
    debug_id: string;
    kind?: string;
    version?: string;
    code_id?: string;
  }[];
  // how the frames read: a dSYM's table, an R8 mapping, a native library's table, or the
  // code's own names (not obfuscated)
  symbols?: "dsym" | "mapping" | "source" | "native";
}

export interface CloudReportDoc extends CloudReport {
  owner_email?: string;
  crashes?: ReportCrash[];
  body: {
    text?: string;
    os_version?: string;
    device_model?: string;
    lines?: Line[];
    server?: Line[];
    crashes?: unknown[];
  };
}

const PAGE = 50;

export function AdminReports() {
  const { t, n, formatDate } = useI18n();
  const [params, setParams] = useSearchParams();
  const f = {
    status: params.get("status") ?? "",
    category: params.get("category") ?? "",
    app: params.get("app") ?? "",
    version: params.get("version") ?? "",
    fp: params.get("fp") ?? "",
  };
  const page = Math.max(1, Number(params.get("page")) || 1);
  const path = withQuery("/v1/admin/reports", { ...f, limit: PAGE, offset: (page - 1) * PAGE });
  const data = useLoad(() => api.get<{ reports: CloudReport[]; total: number }>(path), path);

  function set(next: Record<string, string>) {
    const p = new URLSearchParams(params);
    for (const [k, v] of Object.entries(next)) {
      if (v) p.set(k, v);
      else p.delete(k);
    }
    if (!("page" in next)) p.delete("page");
    setParams(p, { replace: true });
  }

  return (
    <Page title={t("admin.reports.title")}>
      <p className="mkc-muted">{t("admin.reports.about")}</p>
      <div className="mkc-filters">
        <Select
          label={t("admin.col.status")}
          value={f.status}
          onChange={(e) => set({ status: e.target.value })}
          options={[
            { value: "", label: t("admin.filter.any") },
            ...REPORT_STATUSES.map((s) => ({ value: s, label: t(`admin.reports.status.${s}`) })),
          ]}
        />
        <Select
          label={t("admin.reports.category")}
          value={f.category}
          onChange={(e) => set({ category: e.target.value })}
          options={[
            { value: "", label: t("admin.filter.any") },
            ...REPORT_CATEGORIES.map((c) => ({ value: c, label: t(`admin.reports.cat.${c}`) })),
          ]}
        />
        <Input
          label={t("admin.reports.app")}
          value={f.app}
          onChange={(e) => set({ app: e.target.value })}
          dir="ltr"
        />
        <Input
          label={t("admin.col.version")}
          value={f.version}
          onChange={(e) => set({ version: e.target.value })}
          dir="ltr"
        />
        <Input
          label={t("admin.reports.group")}
          value={f.fp}
          onChange={(e) => set({ fp: e.target.value })}
          dir="ltr"
        />
      </div>
      {data.error != null ? (
        <LoadError error={data.error} onRetry={data.reload} />
      ) : !data.data ? (
        <Loading />
      ) : data.data.reports.length === 0 ? (
        <EmptyState kind="filtered_out" title={t("admin.reports.none")} />
      ) : (
        <>
          <p className="mkc-muted">{t("admin.total", { count: n(data.data.total) })}</p>
          <table className="mkc-table">
            <thead>
              <tr>
                <th scope="col">#</th>
                <th scope="col">{t("admin.reports.home")}</th>
                <th scope="col">{t("admin.reports.app")}</th>
                <th scope="col">{t("admin.col.version")}</th>
                <th scope="col">{t("admin.reports.category")}</th>
                <th scope="col">{t("admin.col.status")}</th>
                <th scope="col">{t("admin.reports.received")}</th>
              </tr>
            </thead>
            <tbody>
              {data.data.reports.map((r) => (
                <tr key={r.id} data-testid="cloud-report-row">
                  <td>
                    <Link to={`/reports/${r.id}`}>#{r.id}</Link>
                  </td>
                  <td>
                    <bdi>{r.home || "—"}</bdi>
                  </td>
                  <td className="mkc-mono">{r.app}</td>
                  <td className="mkc-mono">{r.version}</td>
                  <td>{t(`admin.reports.cat.${r.category}`)}</td>
                  <td>
                    <StatusBadge r={r} />
                  </td>
                  <td>{formatDate(r.received_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <Pager
            page={page}
            total={data.data.total}
            size={PAGE}
            onChange={(p) => set({ page: String(p) })}
          />
        </>
      )}
    </Page>
  );
}

function StatusBadge({ r }: { r: CloudReport }) {
  const { t } = useI18n();
  return (
    <Badge tone={r.status === "new" ? "error" : r.status === "fixed" ? "success" : "neutral"}>
      {r.status === "fixed" && r.fixed_in
        ? t("admin.reports.fixedIn", { version: r.fixed_in })
        : t(`admin.reports.status.${r.status}`)}
    </Badge>
  );
}

/** The report's timeline: the app's lines and the home's merged by time, oldest first. */
export function cloudTimeline(d: CloudReportDoc): (Line & { side: "app" | "home" })[] {
  const all = [
    ...(d.body.lines ?? []).map((l) => ({ ...l, side: "app" as const })),
    ...(d.body.server ?? []).map((l) => ({ ...l, side: "home" as const })),
  ];
  return all.sort((a, b) => (Date.parse(a.ts ?? "") || 0) - (Date.parse(b.ts ?? "") || 0));
}

// how a crash's frames read (ReportCrash.symbols)
const SYMBOLS_NOTE = {
  dsym: "admin.reports.symbols.dsym",
  mapping: "admin.reports.symbols.mapping",
  source: "admin.reports.symbols.source",
  native: "admin.reports.symbols.native",
} as const;

// a frame's place in the code: the function, and the file's name and line when known
function frameText(f: CrashFrame): string {
  if (!f.symbol) return f.raw;
  const file = f.symbol.file ? f.symbol.file.split("/").pop() : "";
  return file
    ? `${f.symbol.function} (${file}${f.symbol.line ? `:${f.symbol.line}` : ""})`
    : f.symbol.function;
}

function Crashes({ crashes }: { crashes: ReportCrash[] }) {
  const { t } = useI18n();
  return (
    <>
      <h2>{t("admin.reports.crashes")}</h2>
      {crashes.map((c, i) => (
        <Card
          key={i}
          header={
            <h3 className="mkc-mono" dir="ltr">
              {c.type}
              {c.value && c.value !== c.type ? ` · ${c.value}` : ""}
            </h3>
          }
        >
          {(c.frames ?? []).length === 0 ? (
            <p className="mkc-muted" data-testid="crash-no-frames">
              {t("admin.reports.noFrames")}
            </p>
          ) : (
            <>
              <p className="mkc-muted">{t("admin.reports.crashFrames")}</p>
              {c.symbols && SYMBOLS_NOTE[c.symbols] && (
                <p className="mkc-muted" data-testid="crash-symbols">
                  {t(SYMBOLS_NOTE[c.symbols])}
                </p>
              )}
              <ol className="mkc-list" data-testid="crash-frames" dir="ltr">
                {c.frames.map((f, k) => (
                  <li key={k} className="mkc-list__row mkc-mono">
                    {f.in_app ? <strong>{frameText(f)}</strong> : frameText(f)}
                    {f.inlined && <span className="mkc-muted"> {t("admin.reports.inlined")}</span>}
                    {f.symbol && f.raw && <span className="mkc-muted"> · {f.raw}</span>}
                  </li>
                ))}
              </ol>
            </>
          )}
          {c.missing && c.missing.some((m) => !m.kind) && (
            <p className="mkc-muted" data-testid="crash-missing">
              {t("admin.reports.noSymbols", {
                binaries: c.missing
                  .filter((m) => !m.kind)
                  .map((m) => `${m.code_file} (${m.debug_id})`)
                  .join(", "),
              })}
            </p>
          )}
          {c.missing && c.missing.some((m) => m.kind === "elf") && (
            <p className="mkc-muted" data-testid="crash-missing-native">
              {t("admin.reports.noNativeSymbols", {
                libraries: c.missing
                  .filter((m) => m.kind === "elf")
                  .map((m) =>
                    m.code_id
                      ? t("admin.reports.library", { file: m.code_file, buildId: m.code_id })
                      : t("admin.reports.libraryNoBuildId", { file: m.code_file })
                  )
                  .join(", "),
              })}
            </p>
          )}
          {c.missing
            ?.filter((m) => m.kind === "r8")
            .map((m) => (
              <p key={m.debug_id} className="mkc-muted" data-testid="crash-missing-mapping">
                {t("admin.reports.noMapping", { app: m.code_file, version: m.version ?? "" })}
              </p>
            ))}
        </Card>
      ))}
    </>
  );
}

export function AdminReportDetail() {
  const { id = "" } = useParams();
  const { t, formatDate } = useI18n();
  const message = useErrorMessage();
  const path = `/v1/admin/reports/${encodeURIComponent(id)}`;
  const data = useLoad(() => api.get<CloudReportDoc>(path), path);
  const [status, setStatus] = useState("");
  const [fixedIn, setFixedIn] = useState("");
  const [issue, setIssue] = useState("");
  const [reply, setReply] = useState("");
  const [shot, setShot] = useState<string | null>(null);
  const [err, setErr] = useState("");
  const [saved, setSaved] = useState("");
  const d = data.data;

  useEffect(() => {
    if (!d) return;
    setStatus(d.status);
    setFixedIn(d.fixed_in ?? "");
    setIssue(d.issue_url ?? "");
    setReply(d.reply ?? "");
  }, [d]);
  useEffect(() => () => void (shot && URL.revokeObjectURL(shot)), [shot]);

  async function save(what: string, sub: string, body: unknown) {
    setErr("");
    setSaved("");
    try {
      await api.post(`${path}/${sub}`, body);
      setSaved(what);
      data.reload();
    } catch (e) {
      setErr(message(e));
    }
  }

  async function showShot() {
    try {
      const res = await fetchApi("GET", `${path}/screenshot`);
      setShot(URL.createObjectURL(await res.blob()));
    } catch (e) {
      setErr(message(e));
    }
  }

  if (data.error != null) return <LoadError error={data.error} onRetry={data.reload} />;
  if (!d) return <Loading />;
  return (
    <Page title={t("admin.reports.one", { id: d.id })}>
      <p>
        <Link to="/reports">{t("admin.reports.back")}</Link>
      </p>
      <dl className="mkc-kv" data-testid="cloud-report">
        <dt>{t("admin.reports.home")}</dt>
        <dd>
          <bdi>{d.home}</bdi>{" "}
          {d.owner_email && <span className="mkc-muted">· {d.owner_email}</span>}{" "}
          <span className="mkc-muted">#{d.home_report_id}</span>
        </dd>
        <dt>{t("admin.reports.app")}</dt>
        <dd className="mkc-mono" dir="ltr">
          {d.app} {d.version} · {d.platform} {d.body.device_model ?? ""} {d.body.os_version ?? ""}
        </dd>
        <dt>{t("admin.reports.category")}</dt>
        <dd>{t(`admin.reports.cat.${d.category}`)}</dd>
        <dt>{t("admin.reports.happened")}</dt>
        <dd>{formatDate(d.happened_at ?? d.received_at)}</dd>
        <dt>{t("admin.col.status")}</dt>
        <dd>
          <StatusBadge r={d} />
        </dd>
        {d.fingerprints.length > 0 && (
          <>
            <dt>{t("admin.reports.group")}</dt>
            <dd className="mkc-mono">
              {d.fingerprints.map((fp) => (
                <Link key={fp} to={`/reports?fp=${encodeURIComponent(fp)}`}>
                  {fp}{" "}
                </Link>
              ))}
            </dd>
          </>
        )}
      </dl>
      {d.body.text && <blockquote className="mkc-detail">{d.body.text}</blockquote>}
      {d.screenshot &&
        (shot ? (
          <img className="mkc-shot" src={shot} alt={t("admin.reports.screenshot")} />
        ) : (
          <Button variant="secondary" onClick={() => void showShot()}>
            {t("admin.reports.showScreenshot")}
          </Button>
        ))}

      {d.crashes && d.crashes.length > 0 && <Crashes crashes={d.crashes} />}

      <Card header={<h2>{t("admin.reports.triage")}</h2>}>
        <div className="mkc-stack">
          <div className="mkc-filters">
            <Select
              label={t("admin.col.status")}
              value={status}
              onChange={(e) => setStatus(e.target.value)}
              options={REPORT_STATUSES.map((s) => ({
                value: s,
                label: t(`admin.reports.status.${s}`),
              }))}
            />
            {status === "fixed" && (
              <Input
                label={t("admin.reports.fixedInLabel")}
                value={fixedIn}
                onChange={(e) => setFixedIn(e.target.value)}
                dir="ltr"
              />
            )}
            <Button
              onClick={() =>
                void save(t("admin.reports.saved.status"), "status", { status, fixed_in: fixedIn })
              }
            >
              {t("admin.reports.saveStatus")}
            </Button>
          </div>
          <Input
            label={t("admin.reports.issue")}
            value={issue}
            onChange={(e) => setIssue(e.target.value)}
            dir="ltr"
            placeholder="https://github.com/…/issues/…"
          />
          <Button
            variant="secondary"
            onClick={() => void save(t("admin.reports.saved.issue"), "issue", { url: issue })}
          >
            {t("admin.reports.saveIssue")}
          </Button>
          {d.issue_url && (
            <p>
              <a href={d.issue_url} target="_blank" rel="noreferrer noopener" dir="ltr">
                {d.issue_url}
              </a>
            </p>
          )}
          <Textarea
            label={t("admin.reports.reply")}
            rows={3}
            maxLength={2000}
            value={reply}
            onChange={(e) => setReply(e.target.value)}
          />
          <p className="mkc-muted">{t("admin.reports.replyNote")}</p>
          <Button
            disabled={!reply.trim()}
            onClick={() => void save(t("admin.reports.saved.reply"), "reply", { reply })}
          >
            {t("admin.reports.sendReply")}
          </Button>
          {d.replied_at && (
            <p className="mkc-muted">
              {t("admin.reports.repliedAt", { when: formatDate(d.replied_at) })}
            </p>
          )}
          {saved && (
            <p role="status" className="mkc-muted">
              {saved}
            </p>
          )}
          {err && <p role="alert">{err}</p>}
        </div>
      </Card>

      <h2>{t("admin.reports.timeline")}</h2>
      <ul className="mkc-list" data-testid="cloud-report-timeline">
        {cloudTimeline(d).map((l, i) => (
          <li key={i} className="mkc-list__row">
            <span className="mkc-muted">{l.ts ? formatDate(l.ts) : "—"}</span>{" "}
            <span>
              {l.side === "app"
                ? t("admin.reports.side.app")
                : (l.service ?? t("admin.reports.side.home"))}
            </span>{" "}
            <span className="mkc-mono" dir="ltr">
              {l.level ? `${l.level} ` : ""}
              {l.msg}
            </span>
          </li>
        ))}
      </ul>
    </Page>
  );
}
