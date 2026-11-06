// Dashboards (Epic 53 story 53.12, /admin/dashboards): Overview, Queue, Processing,
// Streaming, Storage and Use, each a set of panels over one shared time range (1 h to 90 d).
// Each panel is one metric (a line per label set), with its value under the pointer, a
// sentence for screen readers, and Open in Logs for the same window. Use shows counts and
// titles only, never one person's history.
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Card } from "@ds/components/Card/Card";
import { Button } from "@ds/components/Button/Button";
import { useI18n } from "../../lib/i18n";
import { AdminGate } from "../../components/AdminGate";
import { TitleRow } from "../../help/HelpLink";
import { LineChart } from "../../components/charts/LineChart";
import { logsPageLink } from "../../lib/logIndex";
import {
  BOARDS,
  RANGES,
  RANGE_MS,
  getBoard,
  panels,
  seriesLabel,
  type Board,
  type BoardDoc,
  type Range,
} from "../../lib/dashboards";

export function AdminDashboards() {
  return (
    <AdminGate>
      <DashboardsInner />
    </AdminGate>
  );
}

/** The panel's title: its words when the app has them, else the metric's name. */
function useSeriesTitle() {
  const { t } = useI18n();
  return (name: string) => {
    const key = `dash.series.${name}`;
    const v = t(key);
    return v === key ? name : v;
  };
}

function DashboardsInner() {
  const { t, n, formatDate } = useI18n();
  const title = useSeriesTitle();
  const [board, setBoard] = useState<Board>(() => {
    const b = new URLSearchParams(typeof window === "undefined" ? "" : window.location.search).get(
      "board"
    );
    return (BOARDS as readonly string[]).includes(b ?? "") ? (b as Board) : "overview";
  });
  const [range, setRange] = useState<Range>("24h");
  const [doc, setDoc] = useState<BoardDoc | null>(null);
  const [err, setErr] = useState(false);

  useEffect(() => {
    let alive = true;
    setDoc(null);
    getBoard(board, range)
      .then((d) => {
        if (!alive) return;
        setDoc(d);
        setErr(false);
      })
      .catch(() => alive && setErr(true));
    return () => {
      alive = false;
    };
  }, [board, range]);

  const since = new Date(Date.now() - RANGE_MS[range]).toISOString();
  const fmtTime = (s: number) => formatDate(new Date(s * 1000).toISOString());
  return (
    <section className="mkt-dash">
      <header>
        <TitleRow>
          <h1>{t("dash.title")}</h1>
        </TitleRow>
      </header>
      <div className="mkt-dash__bar" role="tablist" aria-label={t("dash.title")}>
        {BOARDS.map((b) => (
          <Button
            key={b}
            role="tab"
            aria-selected={b === board}
            variant={b === board ? "primary" : "secondary"}
            size="sm"
            onClick={() => setBoard(b)}
          >
            {t(`dash.${b}`)}
          </Button>
        ))}
      </div>
      <div className="mkt-dash__bar" role="group" aria-label={t("dash.range")}>
        {RANGES.map((r) => (
          <Button
            key={r}
            aria-pressed={r === range}
            variant={r === range ? "primary" : "secondary"}
            size="sm"
            onClick={() => setRange(r)}
          >
            {t(`dash.range.${r}`)}
          </Button>
        ))}
      </div>
      {err && <p className="mkt-muted">{t("dash.loadError")}</p>}
      {doc && doc.series.length === 0 && (doc.top ?? []).length === 0 && (
        <p className="mkt-muted" data-testid="dash-empty">
          {t("dash.noData")}
        </p>
      )}
      <div className="mkt-dash__grid">
        {doc &&
          panels(doc.series).map((p) => {
            const lines = p.series.map((s) => ({
              label: seriesLabel(s) || title(p.name),
              points: s.points,
            }));
            const last = lines.map((l) => {
              const v = l.points.at(-1)?.[1] ?? 0;
              return t("dash.summaryLine", { name: l.label, value: n(Math.round(v * 100) / 100) });
            });
            const max = Math.max(0, ...p.series.flatMap((s) => s.points.map((q) => q[1])));
            const comp = p.series[0]?.labels?.match(/(?:^|,)(?:id|component)=([^,]+)/)?.[1];
            return (
              <Card key={p.name} className="mkt-dash__panel" data-testid="dash-panel">
                <h2 className="mkt-dash__ptitle">{title(p.name)}</h2>
                <LineChart
                  series={lines}
                  summary={t("dash.summary", {
                    title: title(p.name),
                    latest: last.join(", "),
                    max: n(Math.round(max * 100) / 100),
                  })}
                  format={(v) => n(Math.round(v * 100) / 100)}
                  formatTime={fmtTime}
                />
                <Link to={logsPageLink({ since, components: comp })}>{t("dash.openLogs")}</Link>
              </Card>
            );
          })}
      </div>
      {doc && board === "use" && (doc.top ?? []).length > 0 && (
        <Card>
          <h2>{t("dash.topVideos")}</h2>
          <ol data-testid="dash-top">
            {(doc.top ?? []).map((v) => (
              <li key={v.title}>
                <bdi>{v.title}</bdi> — {t("dash.plays", { count: n(v.plays) })}
              </li>
            ))}
          </ol>
        </Card>
      )}
    </section>
  );
}
