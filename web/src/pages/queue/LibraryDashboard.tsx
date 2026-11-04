// /queue/libraries — the library dashboard (admins): one tile per library in a
// responsive grid, with the filters (needs attention, failing, files to convert,
// YouTube off) as counted toggles, a sort and a search. Tiles can be selected
// for the bulk actions (Re-queue, Refresh from YouTube, Convert to MP4, Find
// duplicates); a tile's name opens the library's own admin page and its YouTube
// badge opens its sources. The API refuses all of it to anyone but an admin.
import { useEffect, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { Menu } from "@ds/components/Menu/Menu";
import { Select } from "@ds/components/Select/Select";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { useI18n } from "../../lib/i18n";
import { AdminGate } from "../../components/AdminGate";
import { useJobStream } from "../../lib/useQueueStream";
import { useThrottledReload } from "../../components/queue/useThrottledReload";
import { LibraryTile } from "../../components/libdash/LibraryTile";
import { StatusLegend } from "../../components/libdash/charts";
import {
  FILTERS,
  SORTS,
  filterCounts,
  filterLibraries,
  libraryHealth,
  sortLibraries,
  type Filter,
  type Sort,
} from "../../components/libdash/health";
import { activityOf, useLibraryBoard } from "../../components/libdash/useLibraryBoard";
import { OFFLINE_ACTIONS } from "../../components/queue/offline";
import {
  OfflineActionDialog,
  type OfflineActionTarget,
} from "../../components/queue/OfflineActionDialog";
import { YouTubeSourcesDialog } from "../../components/queue/YouTubeSourcesDialog";
import { RetriggerDialog, type RetriggerSubject } from "../../components/queue/RetriggerDialog";
import { FeatureOffNote, useFeatureGate } from "../../lib/capabilities";
import "../../styles/queue.css";
import "../../styles/libdash.css";

export function LibraryDashboard() {
  return (
    <AdminGate>
      <Dashboard />
    </AdminGate>
  );
}

const isFilter = (v: string | null): v is Filter =>
  !!v && (FILTERS as readonly string[]).includes(v);
const isSort = (v: string | null): v is Sort => !!v && (SORTS as readonly string[]).includes(v);

function Dashboard() {
  const { t, n, locale } = useI18n();
  const board = useLibraryBoard();
  const ytGate = useFeatureGate("youtube");
  const stream = useJobStream();
  useThrottledReload(board.reload, stream.finishedTick, 15_000);
  const [params, setParams] = useSearchParams();
  const filter: Filter = isFilter(params.get("show")) ? (params.get("show") as Filter) : "all";
  const sort: Sort = isSort(params.get("sort")) ? (params.get("sort") as Sort) : "attention";
  const [query, setQuery] = useState("");
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [sourcesOf, setSourcesOf] = useState<{ id: string; name: string } | null>(null);
  const [offlineAction, setOfflineAction] = useState<OfflineActionTarget | null>(null);
  const [requeue, setRequeue] = useState<RetriggerSubject[] | null>(null);

  const all = useMemo(
    () => (board.rows ?? []).map((r) => libraryHealth(r, board.offline?.[r.library_id])),
    [board.rows, board.offline]
  );
  const counts = useMemo(() => filterCounts(all), [all]);
  const shown = useMemo(
    () => sortLibraries(filterLibraries(all, filter, query), sort, locale),
    [all, filter, query, sort, locale]
  );
  // a library that left the list (removed) leaves the selection too
  useEffect(() => {
    setPicked((prev) => {
      const ids = new Set(all.map((h) => h.id));
      const next = new Set([...prev].filter((id) => ids.has(id)));
      return next.size === prev.size ? prev : next;
    });
  }, [all]);

  const set = (key: string, value: string, fallback: string) =>
    setParams(
      (p) => {
        const next = new URLSearchParams(p);
        if (value === fallback) next.delete(key);
        else next.set(key, value);
        return next;
      },
      { replace: true }
    );
  const selected = all.filter((h) => picked.has(h.id));
  const allShownPicked = shown.length > 0 && shown.every((h) => picked.has(h.id));
  const toggle = (id: string, on: boolean) =>
    setPicked((prev) => {
      const next = new Set(prev);
      if (on) next.add(id);
      else next.delete(id);
      return next;
    });
  const totals = all.reduce(
    (a, h) => ({
      videos: a.videos + h.row.videos,
      failed: a.failed + h.failed,
      convert: a.convert + h.toConvert,
    }),
    { videos: 0, failed: 0, convert: 0 }
  );

  return (
    <section className="mkt-page mkt-q mkt-ld" aria-labelledby="mkt-ld-h">
      <header className="mkt-ld__head">
        <div>
          <p className="mkt-ld__crumb">
            <Link to="/queue">{t("libdash.back")}</Link>
          </p>
          <h1 id="mkt-ld-h">{t("libdash.title")}</h1>
          {board.rows && (
            <p className="mkt-ld__summary">
              {t("libdash.summary", {
                libraries: n(all.length),
                videos: n(totals.videos),
                failed: n(totals.failed),
                convert: n(totals.convert),
              })}
            </p>
          )}
        </div>
        <Button variant="secondary" size="sm" onClick={board.reload}>
          {t("libdash.refresh")}
        </Button>
      </header>

      <div className="mkt-ld__tools">
        <div className="mkt-ld__filters" role="group" aria-label={t("libdash.filter.label")}>
          {FILTERS.filter((f) => board.offline || (f !== "convert" && f !== "youtubeOff")).map(
            (f) => (
              <button
                key={f}
                type="button"
                className="mkt-ld-filter"
                data-filter={f}
                aria-pressed={filter === f}
                onClick={() => set("show", f, "all")}
              >
                <span>{t(`libdash.filter.${f}`)}</span>
                <span className="mkt-ld-filter__n">{n(counts[f])}</span>
              </button>
            )
          )}
        </div>
        <div className="mkt-ld__find">
          <Input
            className="mkt-ld__search"
            type="search"
            label={t("libdash.search")}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
          <Select
            className="mkt-ld__sort"
            label={t("libdash.sort.label")}
            value={sort}
            onChange={(e) => set("sort", e.target.value, "attention")}
            options={SORTS.map((s) => ({ value: s, label: t(`libdash.sort.${s}`) }))}
          />
        </div>
      </div>

      <div className="mkt-ld__bulk" role="region" aria-label={t("libdash.bulk.label")}>
        <Button
          size="sm"
          variant="ghost"
          disabled={shown.length === 0}
          onClick={() =>
            setPicked((prev) => {
              const next = new Set(prev);
              for (const h of shown) {
                if (allShownPicked) next.delete(h.id);
                else next.add(h.id);
              }
              return next;
            })
          }
        >
          {allShownPicked
            ? t("libdash.bulk.unselectShown")
            : t("libdash.bulk.selectShown", { n: n(shown.length) })}
        </Button>
        <span className="mkt-ld__picked" role="status">
          {t("libdash.bulk.selected", { n: n(selected.length) })}
        </span>
        <Button
          size="sm"
          variant="secondary"
          disabled={selected.length === 0}
          onClick={() =>
            setRequeue(selected.map((h) => ({ kind: "library", id: h.id, label: h.name })))
          }
        >
          {t("libdash.bulk.requeue")}
        </Button>
        {selected.length > 0 && (
          <>
            <Menu
              className="mkt-q-libs__bulk"
              trigger={t("queue.libs.withSelected", { n: n(selected.length) })}
              // YouTube off on the home (Epic 51): no refresh to offer; the note says where to
              // turn it on (the route answers 409 component_off)
              items={OFFLINE_ACTIONS.filter(
                (a) => a !== "youtube_refresh" || ytGate === "show"
              ).map((a) => ({
                id: a,
                label: t(`queue.offline.action.${a}`),
                onSelect: () =>
                  setOfflineAction({
                    action: a,
                    libraries: selected.map((h) => ({ id: h.id, name: h.name })),
                  }),
              }))}
            />
            <Button size="sm" variant="ghost" onClick={() => setPicked(new Set())}>
              {t("libdash.bulk.clear")}
            </Button>
            {ytGate === "explain" && <FeatureOffNote id="youtube" />}
          </>
        )}
      </div>

      <StatusLegend />

      {board.error && board.rows && (
        <p className="mkt-ld__stale" role="alert">
          {t("libdash.staleError")}{" "}
          <Button size="sm" variant="secondary" onClick={board.reload}>
            {t("common.retry")}
          </Button>
        </p>
      )}

      {board.error && !board.rows ? (
        <ErrorState
          kind="server"
          title={t("common.error")}
          description={t("libdash.loadError")}
          action={
            <Button variant="secondary" onClick={board.reload}>
              {t("common.retry")}
            </Button>
          }
        />
      ) : board.rows === null ? (
        <div className="mkt-ld__wait" role="status">
          <p className="mkt-muted">{t("libdash.loading")}</p>
          {board.slow && (
            <p className="mkt-muted">
              {t("libdash.slow")}{" "}
              <Button size="sm" variant="secondary" onClick={board.reload}>
                {t("common.retry")}
              </Button>
            </p>
          )}
        </div>
      ) : shown.length === 0 ? (
        <EmptyState
          title={t("libdash.empty.title")}
          description={all.length === 0 ? t("libdash.empty.none") : t("libdash.empty.filtered")}
          action={
            all.length > 0 ? (
              <Button
                variant="secondary"
                onClick={() => {
                  setQuery("");
                  set("show", "all", "all");
                }}
              >
                {t("libdash.empty.showAll")}
              </Button>
            ) : undefined
          }
        />
      ) : (
        <>
          <p className="mkt-visually-hidden" role="status">
            {t("libdash.showing", { n: n(shown.length), total: n(all.length) })}
          </p>
          <ul className="mkt-ld-grid">
            {shown.map((h) => (
              <LibraryTile
                key={h.id}
                h={h}
                activity={activityOf(board.activity, h.id)}
                selected={picked.has(h.id)}
                onSelect={(on) => toggle(h.id, on)}
                onSources={() => setSourcesOf({ id: h.id, name: h.name })}
              />
            ))}
          </ul>
        </>
      )}

      <YouTubeSourcesDialog
        library={sourcesOf}
        onClose={() => setSourcesOf(null)}
        onChanged={board.reloadAfterChange}
      />
      <OfflineActionDialog
        target={offlineAction}
        onClose={() => setOfflineAction(null)}
        onDone={board.reloadAfterChange}
      />
      <RetriggerDialog
        open={requeue !== null}
        onClose={() => setRequeue(null)}
        stages={board.stages}
        libraries={board.rows ?? []}
        initialSubjects={requeue ?? undefined}
        onDone={board.reloadAfterChange}
      />
    </section>
  );
}
