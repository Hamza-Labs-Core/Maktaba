// /queue/reports — the pages the pipeline writes about the library: a review
// page per library (seasons and their episodes by eye), the channel plan, its
// preview (a dry run with the current settings) and the channel audit. The
// list is whatever *.html is in the pipeline's reports folder; a page opens in
// a sandboxed frame (the API serves it under a policy with no script).
import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { reportsApi } from "../../lib/reportsApi";
import type { Report, ReportKind } from "../../lib/reportsApi";
import { PlanPreviewPanel } from "../../components/queue/PlanPreviewPanel";
import { useIsAdmin } from "../../components/queue/useIsAdmin";
import { useFmt } from "../../components/queue/format";
import "../../styles/queue.css";

const KIND_ORDER: ReportKind[] = ["plan", "preview", "audit", "review", "other"];

export function Reports() {
  const { t, n } = useI18n();
  const fmt = useFmt();
  const admin = useIsAdmin();
  const [params, setParams] = useSearchParams();
  const [reports, setReports] = useState<Report[] | null>(null);
  const [error, setError] = useState(false);
  const [filter, setFilter] = useState("");
  const [version, setVersion] = useState(0);
  const open = params.get("open");

  const load = useCallback(() => {
    reportsApi
      .list()
      .then((r) => {
        setReports(r.reports);
        setError(false);
      })
      .catch(() => setError(true));
  }, []);
  useEffect(load, [load]);

  const needle = filter.trim().toLowerCase();
  const groups = useMemo(() => {
    const shown = (reports ?? []).filter(
      (r) => !needle || `${r.title} ${r.name}`.toLowerCase().includes(needle)
    );
    return KIND_ORDER.map((kind) => ({ kind, items: shown.filter((r) => r.kind === kind) })).filter(
      (g) => g.items.length > 0
    );
  }, [reports, needle]);

  const current = reports?.find((r) => r.name === open) ?? null;
  const select = (name: string) => setParams({ open: name }, { replace: true });

  return (
    <section className="mkt-page mkt-q">
      <header className="mkt-q-header">
        <div className="mkt-q-header__title">
          <h1>{t("queue.reports.title")}</h1>
        </div>
        <div className="mkt-q-header__actions">
          <Link className="mkt-q-linkbtn" to="/queue">
            {t("queue.detail.back")}
          </Link>
        </div>
      </header>
      <p className="mkt-q-muted">{t("queue.reports.intro")}</p>

      <PlanPreviewPanel
        canRun={admin}
        onFinished={() => {
          load();
          setVersion((v) => v + 1);
        }}
      />

      {error && (
        <p role="alert" className="mkt-q-banner">
          {t("queue.reports.error")}{" "}
          <Button variant="secondary" size="sm" onClick={load}>
            {t("queue.error.retry")}
          </Button>
        </p>
      )}

      <div className="mkt-q-reports">
        <nav className="mkt-q-reports__list" aria-label={t("queue.reports.list")}>
          <input
            type="search"
            dir="auto"
            value={filter}
            placeholder={t("queue.reports.filter")}
            aria-label={t("queue.reports.filter")}
            onChange={(e) => setFilter(e.target.value)}
          />
          {reports === null && !error && <p className="mkt-loading">{t("common.loading")}</p>}
          {reports !== null && reports.length === 0 && (
            <p className="mkt-q-empty">{t("queue.reports.none")}</p>
          )}
          {groups.map((g) => (
            <section key={g.kind} aria-labelledby={`mkt-q-rep-${g.kind}`}>
              <h2 className="mkt-q-eyebrow" id={`mkt-q-rep-${g.kind}`}>
                {t(`queue.reports.kind.${g.kind}`)}
                {g.kind === "review" && ` (${n(g.items.length)})`}
              </h2>
              <ul>
                {g.items.map((r) => (
                  <li key={r.name}>
                    <button
                      type="button"
                      className="mkt-q-reports__item"
                      aria-current={r.name === open ? "page" : undefined}
                      onClick={() => select(r.name)}
                    >
                      <span dir="auto">{bidiIsolate(r.title)}</span>
                      <span className="mkt-q-muted">{fmt.ago(r.updated_at)}</span>
                    </button>
                  </li>
                ))}
              </ul>
            </section>
          ))}
        </nav>

        <div className="mkt-q-reports__view">
          {current ? (
            <>
              <div className="mkt-q-reports__bar">
                <strong dir="auto">{bidiIsolate(current.title)}</strong>
                <span className="mkt-q-muted">
                  {t("queue.reports.updated", { time: fmt.ago(current.updated_at) })}
                </span>
                <a
                  href={reportsApi.pageUrl(current.name, version)}
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  {t("queue.reports.newTab")}
                </a>
              </div>
              {/* allow-same-origin only: the session cookie reaches the page's images;
                  scripts, forms and navigation stay off (the server also sends a CSP) */}
              <iframe
                key={`${current.name}:${version}`}
                title={current.title}
                className="mkt-q-reports__frame"
                sandbox="allow-same-origin"
                referrerPolicy="no-referrer"
                src={reportsApi.pageUrl(current.name, version)}
              />
            </>
          ) : (
            <p className="mkt-q-empty">
              {open && reports
                ? t("queue.reports.missing", { name: open })
                : t("queue.reports.pick")}
            </p>
          )}
        </div>
      </div>
    </section>
  );
}
