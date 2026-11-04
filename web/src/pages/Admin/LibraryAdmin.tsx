// /admin/libraries/:id — one library, everything an admin manages about it on
// one page: overview (size, counts, storage, folders, health), queue status
// (gauge, activity, failing items, every queue, the actions), YouTube sources
// with their modes, conversion (what is left, the convert settings),
// duplicates, trash, settings and access. Opened from Manage libraries, the
// library dashboard and the Queues page; a #section in the address opens at
// that section. The viewer's page of the library (/library/:id) is separate.
// The API refuses every read and write here to anyone but an admin.
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useLocation, useNavigate, useParams } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { api } from "../../lib/api";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { libraryAdminApi, problemMessage, type ManageResponse } from "../../lib/libraryAdminApi";
import { forgetAnswers, VIDEO_ANSWERS } from "../../lib/pageCache";
import { queueApi } from "../../lib/queueApi";
import { useTitledStages } from "../../lib/stageWords";
import type { FailureItem, LibraryQueueRow, OfflineSummary, StageInfo } from "../../lib/queueApi";
import { AdminGate } from "../../components/AdminGate";
import { ScanNow } from "../LibraryBrowser";
import { libraryHealth } from "../../components/libdash/health";
import {
  activityById,
  activityOf,
  LIBRARIES_SLOW_MS,
  LIBRARIES_TIMEOUT_MS,
  withTimeout,
  type Activity,
} from "../../components/libdash/useLibraryBoard";
import { YouTubeSourcesPanel } from "../../components/queue/YouTubeSourcesDialog";
import {
  OfflineActionDialog,
  type OfflineActionTarget,
} from "../../components/queue/OfflineActionDialog";
import { RetriggerDialog } from "../../components/queue/RetriggerDialog";
import type { OfflineAction } from "../../components/queue/offline";
import { safeLibraries, safeStage } from "../queue/safeData";
import { LibraryForm } from "./libraries/LibraryForm";
import { EmptyTrashDialog, MergeDialog, RemoveDialog } from "./libraries/ConfirmDialogs";
import {
  AccessSection,
  ConvertSection,
  DuplicatesSection,
  OverviewSection,
  QueueSection,
  type QueueReadState,
  Section,
  SettingsSection,
  TrashSection,
  type LibraryStats,
} from "./library/sections";
import "../../styles/queue.css";
import "../../styles/libraryAdmin.css";
import "../../styles/libdash.css";

export const LIBRARY_SECTIONS = [
  "overview",
  "queues",
  "youtube",
  "convert",
  "duplicates",
  "trash",
  "settings",
  "access",
] as const;

export function LibraryAdmin() {
  return (
    <AdminGate>
      <LibraryAdminInner />
    </AdminGate>
  );
}

type Dialog = "edit" | "trash" | "merge" | "remove" | "requeue" | null;

function LibraryAdminInner() {
  const { id = "" } = useParams();
  const { hash } = useLocation();
  const navigate = useNavigate();
  const { t, n } = useI18n();
  const [manage, setManage] = useState<ManageResponse | null>(null);
  const [manageErr, setManageErr] = useState<string | null>(null);
  const [row, setRow] = useState<LibraryQueueRow | null>(null);
  const [rows, setRows] = useState<LibraryQueueRow[]>([]);
  const [queuesState, setQueuesState] = useState<QueueReadState>("loading");
  const queuesSeq = useRef(0);
  const [rawStages, setStages] = useState<StageInfo[]>([]);
  // the stages' labels in the reader's language (lib/stageWords)
  const stages = useTitledStages(rawStages);
  const [summary, setSummary] = useState<OfflineSummary | null>(null);
  const [activity, setActivity] = useState<Activity | undefined>(undefined);
  const [failures, setFailures] = useState<{ items: FailureItem[]; total: number } | null>(null);
  const [stats, setStats] = useState<LibraryStats | null>(null);
  const [series, setSeries] = useState<number | null>(null);
  const [dialog, setDialog] = useState<Dialog>(null);
  const [offline, setOffline] = useState<OfflineActionTarget | null>(null);

  const loadManage = useCallback(() => {
    libraryAdminApi
      .manage()
      .then((m) => {
        setManage(m);
        setManageErr(null);
      })
      .catch((e) => setManageErr(problemMessage(e, t("common.error"))));
  }, [t]);

  const loadQueues = useCallback(() => {
    const q = encodeURIComponent(id);
    const mine = ++queuesSeq.current;
    const current = () => queuesSeq.current === mine;
    setQueuesState((s) => (s === "ready" ? s : "loading"));
    const slow = setTimeout(
      () => current() && setQueuesState((s) => (s === "loading" ? "slow" : s)),
      LIBRARIES_SLOW_MS
    );
    withTimeout(queueApi.libraries(), LIBRARIES_TIMEOUT_MS)
      .then((r) => {
        if (!current()) return;
        const all = safeLibraries(r?.items);
        setRows(all);
        setRow(all.find((x) => x.library_id === id) ?? null);
        setQueuesState("ready");
      })
      .catch(() => current() && setQueuesState("error"))
      .finally(() => clearTimeout(slow));
    queueApi
      .offlineSummary(id)
      .then((s) => setSummary(s ?? null))
      .catch(() => {});
    queueApi
      .libraryActivity(30)
      .then((r) => setActivity(activityOf(activityById(r), id)))
      .catch(() => {});
    queueApi
      .failureItems({ library: id }, null, 10)
      .then((r) => {
        const items = Array.isArray(r?.items) ? r.items : [];
        const facet = (r?.facets?.library ?? []).find((f) => f.value === id);
        setFailures({ items, total: facet?.count ?? items.length });
      })
      .catch(() => setFailures({ items: [], total: 0 }));
    queueApi
      .snapshot()
      .then((s) => Array.isArray(s?.stages) && setStages(s.stages.map(safeStage)))
      .catch(() => {});
    api
      .get<LibraryStats>(`/api/libraries/${q}/stats`)
      .then((s) => setStats(s ?? null))
      .catch(() => {});
    api
      .get<{ total?: number }>(`/api/libraries/${q}/series?limit=1`)
      .then((s) => setSeries(typeof s?.total === "number" ? s.total : null))
      .catch(() => {});
  }, [id]);

  useEffect(() => {
    loadManage();
    loadQueues();
  }, [loadManage, loadQueues]);

  const lib = manage?.items.find((l) => l.id === id) ?? null;
  const h = useMemo(() => (row ? libraryHealth(row, summary ?? undefined) : null), [row, summary]);

  // open at the #section the address names, once the page has drawn it
  useEffect(() => {
    if (!lib || !hash) return;
    const el = document.getElementById(decodeURIComponent(hash.slice(1)));
    if (el) {
      el.scrollIntoView?.({ block: "start" });
      el.focus?.({ preventScroll: true });
    }
  }, [lib, hash]);

  const changed = () => {
    forgetAnswers("libraries", "admin:libraries", "admin:library-acl", ...VIDEO_ANSWERS);
    loadManage();
    loadQueues();
  };
  const closeAndReload = () => {
    setDialog(null);
    changed();
  };
  const runOffline = (a: OfflineAction) =>
    lib && setOffline({ action: a, libraries: [{ id: lib.id, name: lib.name }] });

  if (manageErr) {
    return (
      <section className="mkt-page">
        <ErrorState
          kind="server"
          title={t("common.error")}
          description={manageErr}
          action={
            <Button variant="secondary" onClick={loadManage}>
              {t("common.retry")}
            </Button>
          }
        />
      </section>
    );
  }
  if (!manage) {
    return (
      <section className="mkt-page">
        <p className="mkt-muted" role="status">
          {t("libdash.loading")}
        </p>
      </section>
    );
  }
  if (!lib) {
    return (
      <section className="mkt-page">
        <ErrorState
          kind="not_found"
          title={t("libpage.notFound.title")}
          description={t("libpage.notFound.desc")}
          action={<Link to="/admin/libraries">{t("libpage.back")}</Link>}
        />
      </section>
    );
  }

  return (
    <section className="mkt-page mkt-q mkt-lp" aria-labelledby="mkt-lp-h">
      <header className="mkt-lp__head">
        <p className="mkt-ld__crumb">
          <Link to="/admin/libraries">{t("libpage.back")}</Link>
          <Link to="/queue/libraries">{t("libpage.toDashboard")}</Link>
          <Link to={`/library/${encodeURIComponent(lib.id)}`}>{t("libpage.toViewer")}</Link>
        </p>
        <h1 id="mkt-lp-h" className="mkt-lp__name">
          {bidiIsolate(lib.name)}
        </h1>
        <p className="mkt-ld__summary">
          {t("libdash.meta", { videos: n(lib.counts.total), seasons: n(row?.seasons ?? 0) })}
        </p>
      </header>

      <nav className="mkt-lp-nav" aria-label={t("libpage.nav")}>
        <ul>
          {LIBRARY_SECTIONS.map((s) => (
            <li key={s}>
              <a href={`#${s}`} aria-current={hash === `#${s}` ? "location" : undefined}>
                {t(`libpage.nav.${s}`)}
              </a>
            </li>
          ))}
        </ul>
      </nav>

      <OverviewSection
        lib={lib}
        h={h}
        stats={stats}
        series={series}
        scan={
          <ScanNow
            libraryId={lib.id}
            jobId={lib.scanning ? lib.last_scan?.job_id : null}
            onDone={changed}
          />
        }
      />
      <QueueSection
        h={h}
        status={queuesState}
        onRetry={loadQueues}
        stages={stages}
        activity={activity}
        failures={failures}
        onOffline={runOffline}
        onRequeue={() => setDialog("requeue")}
      />
      <Section id="youtube" title={t("libpage.youtube.title")} desc={t("libpage.youtube.desc")}>
        <YouTubeSourcesPanel library={{ id: lib.id, name: lib.name }} onChanged={loadQueues} />
      </Section>
      <ConvertSection
        lib={lib}
        summary={summary}
        onSaved={loadManage}
        onConvert={() => runOffline("convert")}
      />
      <DuplicatesSection libId={lib.id} summary={summary} onScan={() => runOffline("dedup")} />
      <TrashSection lib={lib} summary={summary} onEmptyMissing={() => setDialog("trash")} />
      <SettingsSection
        lib={lib}
        canMerge={manage.items.length > 1}
        onEdit={() => setDialog("edit")}
        onMerge={() => setDialog("merge")}
        onRemove={() => setDialog("remove")}
      />
      <AccessSection lib={lib} />

      {dialog === "edit" && (
        <LibraryForm library={lib} onClose={() => setDialog(null)} onSaved={closeAndReload} />
      )}
      {dialog === "trash" && (
        <EmptyTrashDialog library={lib} onClose={() => setDialog(null)} onDone={closeAndReload} />
      )}
      {dialog === "merge" && (
        <MergeDialog
          library={lib}
          libraries={manage.items}
          onClose={() => setDialog(null)}
          onDone={closeAndReload}
        />
      )}
      {dialog === "remove" && (
        <RemoveDialog
          library={lib}
          onClose={() => setDialog(null)}
          onDone={() => {
            forgetAnswers("libraries", "admin:libraries", "admin:library-acl", ...VIDEO_ANSWERS);
            navigate("/admin/libraries");
          }}
        />
      )}
      <RetriggerDialog
        open={dialog === "requeue"}
        onClose={() => setDialog(null)}
        stages={stages}
        libraries={rows}
        initialSubjects={[{ kind: "library", id: lib.id, label: lib.name }]}
        onDone={loadQueues}
      />
      <OfflineActionDialog target={offline} onClose={() => setOffline(null)} onDone={loadQueues} />
    </section>
  );
}
