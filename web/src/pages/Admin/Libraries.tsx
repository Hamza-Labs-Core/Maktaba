// Epic 45 (story 45.5): Manage libraries (admin).
//
//   GET /api/libraries/manage   {items, conflict_groups}: every library with its folders (reachable or why
//                               not), counts, last scan, a running scan, and the
//                               folders two libraries claim (conflict_groups)
//
// Each library has Scan (with progress, followed after a reload too), Edit,
// Empty trash, Merge and Remove; "Folders claimed twice" offers a one-click
// Merge into the library that holds the videos. The dialogs live in
// ./libraries/. The API refuses all of it to anyone but an admin.
import { useCallback, useEffect, useState } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import {
  libraryAdminApi,
  problemMessage,
  type ConflictGroup,
  type ManageResponse,
  type ManagedLibrary,
} from "../../lib/libraryAdminApi";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { AdminGate } from "../../components/AdminGate";
import { TitleRow } from "../../help/HelpLink";
import { ScanNow } from "../LibraryBrowser";
import { LibraryForm } from "./libraries/LibraryForm";
import { EmptyTrashDialog, MergeDialog, RemoveDialog } from "./libraries/ConfirmDialogs";
import "../../styles/libraryAdmin.css";

type Dialog =
  | { kind: "add" }
  | { kind: "edit"; library: ManagedLibrary }
  | { kind: "trash"; library: ManagedLibrary }
  | { kind: "merge"; library: ManagedLibrary; target?: string }
  | { kind: "remove"; library: ManagedLibrary };

const SCAN_STATES = new Set(["pending", "running", "done", "failed", "cancelled", "superseded"]);

export function AdminLibraries() {
  return (
    <AdminGate>
      <LibrariesInner />
    </AdminGate>
  );
}

function LibrariesInner() {
  const { t } = useI18n();
  const [data, setData] = useState<ManageResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [dialog, setDialog] = useState<Dialog | null>(null);

  const load = useCallback(() => {
    setError(null);
    libraryAdminApi
      .manage()
      .then(setData)
      .catch((e) => setError(problemMessage(e, t("common.error"))));
  }, [t]);

  useEffect(load, [load]);

  const done = () => {
    setDialog(null);
    load();
  };
  const libraries = data?.items ?? [];

  return (
    <section className="mkt-page mkt-libadmin">
      <header className="mkt-page__header">
        <TitleRow>
          <h1>{t("libraryAdmin.title")}</h1>
        </TitleRow>
        <Button type="button" onClick={() => setDialog({ kind: "add" })}>
          {t("libraryAdmin.add")}
        </Button>
      </header>
      <p className="mkt-muted">{t("libraryAdmin.desc")}</p>

      {error ? (
        <ErrorState
          kind="server"
          title={t("common.error")}
          description={error}
          action={
            <Button type="button" variant="secondary" onClick={load}>
              {t("common.retry")}
            </Button>
          }
        />
      ) : data === null ? (
        <p className="mkt-loading">{t("common.loading")}</p>
      ) : libraries.length === 0 ? (
        <EmptyState
          title={t("libraryAdmin.empty.title")}
          description={t("libraryAdmin.empty.desc")}
          action={
            <Button type="button" onClick={() => setDialog({ kind: "add" })}>
              {t("libraryAdmin.add")}
            </Button>
          }
        />
      ) : (
        <>
          <Conflicts
            groups={data.conflict_groups}
            libraries={libraries}
            onMerge={(library, target) => setDialog({ kind: "merge", library, target })}
          />
          <ul className="mkt-libadmin__list">
            {libraries.map((lib) => (
              <LibraryCard
                key={lib.id}
                library={lib}
                onReload={load}
                canMerge={libraries.length > 1}
                onAction={(kind) => setDialog({ kind, library: lib })}
              />
            ))}
          </ul>
        </>
      )}

      {dialog?.kind === "add" && <LibraryForm onClose={() => setDialog(null)} onSaved={done} />}
      {dialog?.kind === "edit" && (
        <LibraryForm library={dialog.library} onClose={() => setDialog(null)} onSaved={done} />
      )}
      {dialog?.kind === "trash" && (
        <EmptyTrashDialog library={dialog.library} onClose={() => setDialog(null)} onDone={done} />
      )}
      {dialog?.kind === "merge" && (
        <MergeDialog
          library={dialog.library}
          libraries={libraries}
          target={dialog.target}
          onClose={() => setDialog(null)}
          onDone={done}
        />
      )}
      {dialog?.kind === "remove" && (
        <RemoveDialog library={dialog.library} onClose={() => setDialog(null)} onDone={done} />
      )}
    </section>
  );
}

function Conflicts({
  groups,
  libraries,
  onMerge,
}: {
  groups: ConflictGroup[];
  libraries: ManagedLibrary[];
  onMerge: (library: ManagedLibrary, target: string) => void;
}) {
  const { t, n } = useI18n();
  const byId = new Map(libraries.map((l) => [l.id, l]));
  if (groups.length === 0) return null;
  return (
    <section
      className="mkt-libadmin__conflicts"
      aria-labelledby="mkt-libadmin-conflicts"
      role="region"
    >
      <h2 id="mkt-libadmin-conflicts">{t("libraryAdmin.conflicts.title")}</h2>
      <p className="mkt-muted">{t("libraryAdmin.conflicts.desc")}</p>
      <ul>
        {groups.map((g) => {
          const ids = new Set(g.libraries.map((m) => m.id));
          const target = g.libraries.find((m) => m.id === g.suggested_target);
          // the folders the group's libraries claim of each other
          const folders = [
            ...new Set(
              g.libraries.flatMap((m) =>
                (byId.get(m.id)?.conflicts ?? [])
                  .filter((c) => ids.has(c.library_id))
                  .map((c) => c.root)
              )
            ),
          ];
          return (
            <li key={g.libraries.map((m) => m.id).join("|")} className="mkt-libadmin__conflict">
              <p>
                <Badge tone="warn">{t(`libraryAdmin.conflicts.kind.${g.kind}`)}</Badge>{" "}
                {folders.map((f) => (
                  <bdi key={f} dir="ltr" className="mkt-libadmin__path mkt-libadmin__local">
                    {f}
                  </bdi>
                ))}
              </p>
              <ul>
                {g.libraries.map((m) => {
                  const lib = byId.get(m.id);
                  return (
                    <li key={m.id} className="mkt-libadmin__conflict-lib">
                      <span>
                        <bdi>{m.name}</bdi>{" "}
                        <span className="mkt-muted">
                          ({t("libraryAdmin.counts.videos", { count: n(m.videos) })})
                        </span>
                      </span>
                      {target && lib && m.id !== target.id && (
                        <Button type="button" size="sm" onClick={() => onMerge(lib, target.id)}>
                          {t("libraryAdmin.conflicts.mergeInto", {
                            name: bidiIsolate(target.name),
                          })}
                        </Button>
                      )}
                      {m.id === g.suggested_target && (
                        <Badge tone="success">{t("libraryAdmin.conflicts.keeps")}</Badge>
                      )}
                    </li>
                  );
                })}
              </ul>
            </li>
          );
        })}
      </ul>
    </section>
  );
}

function LibraryCard({
  library: lib,
  onReload,
  canMerge,
  onAction,
}: {
  library: ManagedLibrary;
  onReload: () => void;
  canMerge: boolean;
  onAction: (kind: "edit" | "trash" | "merge" | "remove") => void;
}) {
  const { t, n, formatDate } = useI18n();
  const c = lib.counts;
  const scan = lib.last_scan;
  const when = scan?.finished_at || scan?.created_at;
  const headingId = `mkt-libadmin-${lib.id}`;
  return (
    <li className="mkt-libadmin__card" aria-labelledby={headingId}>
      <div className="mkt-libadmin__card-head">
        <h2 id={headingId}>
          <bdi>{lib.name}</bdi>
        </h2>
        <p className="mkt-libadmin__counts">
          {t("libraryAdmin.counts.videos", { count: n(c.total) })}
          {" · "}
          {t("libraryAdmin.counts.ready", { count: n(c.ready) })}
          {c.processing > 0 &&
            ` · ${t("libraryAdmin.counts.processing", { count: n(c.processing) })}`}
          {c.failed > 0 && ` · ${t("libraryAdmin.counts.failed", { count: n(c.failed) })}`}
          {c.missing > 0 && ` · ${t("libraryAdmin.counts.missing", { count: n(c.missing) })}`}
        </p>
      </div>

      <h3 className="mkt-libadmin__label">{t("libraryAdmin.folders")}</h3>
      <ul className="mkt-libadmin__roots">
        {lib.roots.map((r) => {
          const conflicts = lib.conflicts.filter((x) => x.root === r.path);
          return (
            <li key={r.path}>
              <bdi dir="ltr" className="mkt-libadmin__path">
                {r.path}
              </bdi>{" "}
              {r.reachable ? (
                <Badge tone="success">{t("libraryAdmin.folder.reachable")}</Badge>
              ) : (
                <Badge tone="error">
                  {t("libraryAdmin.folder.unreachable")}
                  {r.problem ? `: ${r.problem}` : ""}
                </Badge>
              )}
              {r.local_path && r.local_path !== r.path && (
                <span className="mkt-muted mkt-libadmin__small mkt-libadmin__local">
                  {t("libraryAdmin.folder.localPath")}{" "}
                  <bdi dir="ltr" className="mkt-libadmin__path">
                    {r.local_path}
                  </bdi>
                </span>
              )}
              {conflicts.map((x) => (
                <span key={`${x.library_id}-${x.other_root}`} className="mkt-libadmin__problem">
                  <Badge tone="warn">{t(`libraryAdmin.conflicts.kind.${x.kind}`)}</Badge>{" "}
                  {t("libraryAdmin.folder.alsoIn", {
                    name: bidiIsolate(x.library_name),
                    count: n(x.other_count),
                  })}
                </span>
              ))}
            </li>
          );
        })}
      </ul>

      <p className="mkt-muted mkt-libadmin__small">
        {scan
          ? t("libraryAdmin.lastScan", {
              when: when ? formatDate(when) : "—",
              state: SCAN_STATES.has(scan.state)
                ? t(`libraryAdmin.scanState.${scan.state}`)
                : scan.state,
            })
          : t("libraryAdmin.neverScanned")}
        {scan?.error ? ` — ${scan.error}` : ""}
      </p>

      <div className="mkt-libadmin__actions">
        <ScanNow libraryId={lib.id} jobId={lib.scanning ? scan?.job_id : null} onDone={onReload} />
        <Button type="button" variant="secondary" onClick={() => onAction("edit")}>
          {t("common.edit")}
        </Button>
        <Button
          type="button"
          variant="secondary"
          onClick={() => onAction("trash")}
          disabled={c.missing === 0}
        >
          {t("libraryAdmin.trash.button", { count: n(c.missing) })}
        </Button>
        <Button
          type="button"
          variant="secondary"
          onClick={() => onAction("merge")}
          disabled={!canMerge}
        >
          {t("libraryAdmin.merge.button")}
        </Button>
        <Button type="button" variant="destructive" onClick={() => onAction("remove")}>
          {t("libraryAdmin.remove.button")}
        </Button>
      </div>
    </li>
  );
}
