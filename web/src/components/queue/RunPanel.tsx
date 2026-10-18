// "Start a run": the stand-alone control centre's wizard on top of the queue
// graph's own switches (no parallel mechanism). Its choices map to:
//
//   libraries            the subjects of POST /api/queue/retrigger
//   write to Plex / TV   the plex / dizquetv extensions' enabled flag (PUT /api/extensions/{name})
//   read YouTube         the youtube_sync queue's enabled flag (PATCH /api/queues/{stage})
//   start at <step>      a retrigger of that queue in "from here on" mode (force_cascade);
//                        without it, each library's entry queues in the normal "only what
//                        changed" mode
//   leave out <steps>    those queues' enabled flag (PATCH /api/queues/{stage})
//
// The write and leave-out switches are the queues' saved settings, so they stay as set after
// the run; the panel shows their current state and says so.
import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { queueApi } from "../../lib/queueApi";
import type {
  Extension,
  LibraryQueueRow,
  RetriggerMode,
  RetriggerRequest,
  RetriggerResponse,
  StageInfo,
} from "../../lib/queueApi";
import { summarize } from "./RetriggerDialog";
import { useAction } from "./useAction";

/** The planner's single global subject (engine.graph.CATALOG_ID). */
export const CATALOG_ID = "00000000-0000-0000-0000-00000000ca7a";
const YOUTUBE_STAGE = "youtube_sync";
const WRITE_EXTENSIONS = ["plex", "dizquetv"] as const;
const START_KINDS = ["library", "catalog", "season"] as const;
const RUN_PRIORITY = 50;

type Seasons = (libraryId: string) => Promise<string[]>;

/** A queue the run can start at: one a library, the catalog or a season is the subject of. */
export const canStartAt = (s: StageInfo): boolean =>
  !s.hidden &&
  s.enabled &&
  s.subject_kinds.some((k) => (START_KINDS as readonly string[]).includes(k));

/** Queues the "leave out" list offers: not hidden, not the YouTube read, not an extension's. */
export const leaveOutChoices = (stages: StageInfo[]): StageInfo[] =>
  stages
    .filter((s) => !s.hidden && !s.extension && s.stage !== YOUTUBE_STAGE)
    .sort((a, b) => a.lane_order - b.lane_order);

/**
 * The retrigger request a run maps to. Without `startAt`: each library, its entry queues, only
 * what changed. With it: that queue for each library (or the catalog, or each library's seasons)
 * and everything after it. `seasonsOf` resolves a library's season ids when the queue is a
 * per-season one.
 */
export async function buildRunRequest(p: {
  libraries: string[];
  startAt: string;
  stages: StageInfo[];
  seasonsOf: Seasons;
}): Promise<RetriggerRequest> {
  if (!p.startAt) {
    return {
      items: p.libraries.map((id) => ({ subject_kind: "library", subject_id: id })),
      mode: "queue",
      priority: RUN_PRIORITY,
    };
  }
  const info = p.stages.find((s) => s.stage === p.startAt);
  const kinds = info?.subject_kinds ?? [];
  const mode: RetriggerMode = "force_cascade";
  let items: RetriggerRequest["items"] = [];
  if (kinds.includes("library")) {
    items = p.libraries.map((id) => ({
      subject_kind: "library",
      subject_id: id,
      stage: p.startAt,
    }));
  } else if (kinds.includes("catalog")) {
    items = [{ subject_kind: "catalog", subject_id: CATALOG_ID, stage: p.startAt }];
  } else if (kinds.includes("season")) {
    for (const lib of p.libraries) {
      for (const id of await p.seasonsOf(lib)) {
        items.push({ subject_kind: "season", subject_id: id, stage: p.startAt });
      }
    }
  }
  return { items, mode, priority: RUN_PRIORITY };
}

export interface RunPanelProps {
  stages: StageInfo[];
  libraries: LibraryQueueRow[];
  extensions: Extension[] | null;
  canRun: boolean;
  /** Called after the run was queued (and the switches saved). */
  onDone?: (res: RetriggerResponse) => void;
  /** Open on first render (tests, deep links). */
  defaultOpen?: boolean;
}

export function RunPanel({
  stages,
  libraries,
  extensions,
  canRun,
  onDone,
  defaultOpen = false,
}: RunPanelProps) {
  const { t, n } = useI18n();
  const { busy, error, run, clear } = useAction();
  const [scope, setScope] = useState<"all" | "selected">("all");
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [startAt, setStartAt] = useState("");
  const [leaveOut, setLeaveOut] = useState<Set<string>>(new Set());
  const [write, setWrite] = useState<Record<string, boolean>>({});
  const [query, setQuery] = useState("");
  const [result, setResult] = useState<RetriggerResponse | null>(null);
  const [localError, setLocalError] = useState<string | null>(null);

  const choices = useMemo(() => leaveOutChoices(stages), [stages]);
  const byName = useMemo(() => new Map(stages.map((s) => [s.stage, s])), [stages]);
  const yt = byName.get(YOUTUBE_STAGE);
  const ext = useMemo(() => new Map((extensions ?? []).map((e) => [e.name, e])), [extensions]);

  // The switches start from what is saved, and follow it when it changes underneath.
  const savedKey = JSON.stringify([
    choices.map((s) => [s.stage, s.enabled]),
    [yt?.enabled],
    (extensions ?? []).map((e) => [e.name, e.enabled]),
  ]);
  useEffect(() => {
    setLeaveOut(
      new Set(
        choices
          .filter((s) => !s.enabled && s.disabled_reason !== "not_configured")
          .map((s) => s.stage)
      )
    );
    const w: Record<string, boolean> = {};
    for (const name of WRITE_EXTENSIONS) w[name] = ext.get(name)?.enabled ?? false;
    w[YOUTUBE_STAGE] = yt?.enabled ?? false;
    setWrite(w);
  }, [savedKey]);

  const startChoices = useMemo(
    () =>
      stages
        .filter(canStartAt)
        .filter((s) => !leaveOut.has(s.stage))
        .sort((a, b) => a.lane_order - b.lane_order),
    [stages, leaveOut]
  );
  useEffect(() => {
    if (startAt && !startChoices.some((s) => s.stage === startAt)) setStartAt("");
  }, [startChoices, startAt]);

  const q = query.trim().toLowerCase();
  const shown = libraries.filter((l) => l.name.toLowerCase().includes(q));
  const chosen = scope === "all" ? libraries.map((l) => l.library_id) : [...picked];

  const stageLabel = (s: string) => byName.get(s)?.label ?? s;
  const writingTo = WRITE_EXTENSIONS.filter((e) => write[e]).map((e) => ext.get(e)?.label ?? e);
  const left = [...leaveOut].map(stageLabel);

  async function start() {
    setLocalError(null);
    setResult(null);
    if (chosen.length === 0) {
      setLocalError(t("queue.run.noLibraries"));
      return;
    }
    const res = await run("run", async () => {
      // 1. the saved switches that differ from what was chosen
      for (const s of choices) {
        const want = !leaveOut.has(s.stage);
        if (s.enabled !== want && s.disabled_reason !== "not_configured") {
          await queueApi.patchStage(s.stage, { enabled: want });
        }
      }
      if (yt && write[YOUTUBE_STAGE] !== undefined && yt.enabled !== write[YOUTUBE_STAGE]) {
        await queueApi.patchStage(YOUTUBE_STAGE, { enabled: write[YOUTUBE_STAGE] });
      }
      for (const name of WRITE_EXTENSIONS) {
        const e = ext.get(name);
        if (e && e.configured && e.enabled !== write[name]) {
          await queueApi.saveExtension(name, { enabled: write[name] });
        }
      }
      // 2. the work
      const req = await buildRunRequest({
        libraries: chosen,
        startAt,
        stages,
        seasonsOf: async (id) =>
          ((await queueApi.history("library", id)).seasons ?? []).map((s) => s.id),
      });
      if (req.items.length === 0) throw new Error(t("queue.run.nothing"));
      return queueApi.retrigger(req);
    });
    if (res) {
      setResult(res);
      onDone?.(res);
    }
  }

  const sum = result ? summarize(result) : null;
  const toggle = <T,>(set: Set<T>, v: T): Set<T> => {
    const next = new Set(set);
    if (next.has(v)) next.delete(v);
    else next.add(v);
    return next;
  };

  return (
    <details className="mkt-q-run mkt-q-panel" open={defaultOpen || undefined}>
      <summary className="mkt-q-section-title">{t("queue.run.title")}</summary>
      <p className="mkt-q-muted">{t("queue.run.intro")}</p>
      {!canRun && <p className="mkt-q-note">{t("queue.run.adminOnly")}</p>}
      <form className="mkt-q-run__form" onSubmit={(e) => e.preventDefault()}>
        <fieldset className="mkt-q-fieldset" disabled={!canRun}>
          <legend>{t("queue.run.libraries")}</legend>
          <label className="mkt-q-radio">
            <input
              type="radio"
              name="mkt-q-run-scope"
              checked={scope === "all"}
              onChange={() => setScope("all")}
            />
            <span>
              {t("queue.run.allLibraries")}{" "}
              <span className="mkt-q-muted">({n(libraries.length)})</span>
            </span>
          </label>
          <label className="mkt-q-radio">
            <input
              type="radio"
              name="mkt-q-run-scope"
              checked={scope === "selected"}
              onChange={() => setScope("selected")}
            />
            <span>
              {t("queue.run.someLibraries")} <span className="mkt-q-chip">{n(picked.size)}</span>
            </span>
          </label>
          {scope === "selected" && (
            <>
              <input
                type="search"
                dir="auto"
                value={query}
                placeholder={t("queue.retrigger.search")}
                aria-label={t("queue.retrigger.search")}
                onChange={(e) => setQuery(e.target.value)}
              />
              <ul className="mkt-q-picklist mkt-q-picklist--cols">
                {shown.map((l) => (
                  <li key={l.library_id}>
                    <label className="mkt-q-check">
                      <input
                        type="checkbox"
                        checked={picked.has(l.library_id)}
                        onChange={() => setPicked((p) => toggle(p, l.library_id))}
                      />
                      <span dir="auto">{bidiIsolate(l.name)}</span>
                    </label>
                  </li>
                ))}
              </ul>
            </>
          )}
        </fieldset>

        <fieldset className="mkt-q-fieldset" disabled={!canRun}>
          <legend>{t("queue.run.write")}</legend>
          <p className="mkt-q-muted">{t("queue.run.writeSaved")}</p>
          {WRITE_EXTENSIONS.map((name) => {
            const e = ext.get(name);
            return (
              <label className="mkt-q-check" key={name}>
                <input
                  type="checkbox"
                  checked={!!write[name]}
                  disabled={!e || !e.configured}
                  onChange={(ev) => setWrite((w) => ({ ...w, [name]: ev.target.checked }))}
                />
                <span>
                  {t(`queue.run.write.${name}`)}
                  {e && !e.configured && (
                    <>
                      {" "}
                      <Link to="/admin/extensions">{t("queue.run.notSetUp")}</Link>
                    </>
                  )}
                </span>
              </label>
            );
          })}
          <label className="mkt-q-check">
            <input
              type="checkbox"
              checked={!!write[YOUTUBE_STAGE]}
              disabled={!yt || yt.disabled_reason === "not_configured"}
              onChange={(ev) => setWrite((w) => ({ ...w, [YOUTUBE_STAGE]: ev.target.checked }))}
            />
            <span>{t("queue.run.write.youtube")}</span>
          </label>
          <label className="mkt-q-check">
            <input type="checkbox" checked={false} disabled />
            <span>
              {t("queue.run.exact")} <span className="mkt-q-muted">{t("queue.run.exactNote")}</span>
            </span>
          </label>
        </fieldset>

        <fieldset className="mkt-q-fieldset" disabled={!canRun}>
          <legend>{t("queue.run.steps")}</legend>
          <label className="mkt-q-field">
            <span>{t("queue.run.startAt")}</span>
            <select value={startAt} onChange={(e) => setStartAt(e.target.value)}>
              <option value="">{t("queue.run.firstStep")}</option>
              {startChoices.map((s) => (
                <option key={s.stage} value={s.stage}>
                  {s.label}
                </option>
              ))}
            </select>
          </label>
          <p className="mkt-q-muted">
            {startAt ? t("queue.run.fromHere") : t("queue.run.fromStart")}
          </p>
          <details>
            <summary>
              {t("queue.run.leaveOut")} <span className="mkt-q-chip">{n(leaveOut.size)}</span>
            </summary>
            <ul className="mkt-q-picklist mkt-q-picklist--cols">
              {choices.map((s) => (
                <li key={s.stage}>
                  <label className="mkt-q-check">
                    <input
                      type="checkbox"
                      checked={leaveOut.has(s.stage)}
                      disabled={s.disabled_reason === "not_configured"}
                      onChange={() => setLeaveOut((p) => toggle(p, s.stage))}
                    />
                    <span>{s.label}</span>
                  </label>
                </li>
              ))}
            </ul>
          </details>
        </fieldset>

        <p className="mkt-q-muted" data-testid="run-review">
          {t("queue.run.review", {
            libraries: n(chosen.length),
            start: startAt ? stageLabel(startAt) : t("queue.run.firstStep"),
            write: writingTo.length ? writingTo.join(", ") : t("queue.run.writeNothing"),
            left: left.length ? left.join(", ") : t("queue.run.leftNothing"),
          })}
        </p>
        {(error || localError) && (
          <p role="alert" className="mkt-q-error">
            {localError ?? error}
          </p>
        )}
        {result && sum && (
          <p role="status">
            {t("queue.retrigger.result", {
              queued: n(sum.queued),
              merged: n(sum.merged),
              skipped: n(sum.skipped),
            })}
          </p>
        )}
        <div>
          <Button
            variant="primary"
            loading={busy === "run"}
            disabled={!canRun}
            onClick={() => {
              clear();
              void start();
            }}
          >
            {t("queue.run.start")}
          </Button>
        </div>
      </form>
    </details>
  );
}
