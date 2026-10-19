// "Queue work…" dialog -> POST /api/queue/retrigger.
// Pick libraries / seasons (or arrive with a prefilled subject), choose
// queues (none = each item's entry queues) and a mode:
//   queue          re-add it; unchanged work is skipped
//   force          redo the chosen queues, pass on only what changed
//   force_cascade  redo it and everything downstream ("from the start")
import { useEffect, useMemo, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Modal } from "@ds/components/Modal/Modal";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { queueApi } from "../../lib/queueApi";
import type {
  LibraryQueueRow,
  RetriggerMode,
  RetriggerRequest,
  RetriggerResponse,
  StageInfo,
  SubjectKind,
} from "../../lib/queueApi";
import { useAction } from "./useAction";

export interface RetriggerSubject {
  kind: SubjectKind;
  id: string;
  label: string;
}

export interface RetriggerDialogProps {
  open: boolean;
  onClose: () => void;
  stages: StageInfo[];
  libraries: LibraryQueueRow[];
  initialSubjects?: RetriggerSubject[];
  initialStages?: string[];
  initialMode?: RetriggerMode;
  onDone?: (res: RetriggerResponse) => void;
}

const keyOf = (s: { kind: string; id: string }) => `${s.kind}:${s.id}`;
const MODES: RetriggerMode[] = ["queue", "force", "force_cascade"];

export function summarize(res: RetriggerResponse) {
  const out = { queued: 0, merged: 0, skipped: 0 };
  for (const r of res?.results ?? []) {
    if (r.outcome === "inserted") out.queued++;
    else if (r.outcome === "coalesced" || r.outcome === "rerun_requested") out.merged++;
    else out.skipped++;
  }
  return out;
}

export function RetriggerDialog({
  open,
  onClose,
  stages,
  libraries,
  initialSubjects,
  initialStages,
  initialMode = "queue",
  onDone,
}: RetriggerDialogProps) {
  const { t, n } = useI18n();
  const { busy, error, run, clear } = useAction();
  const [subjects, setSubjects] = useState<Map<string, RetriggerSubject>>(new Map());
  const [chosenStages, setChosenStages] = useState<Set<string>>(new Set());
  const [mode, setMode] = useState<RetriggerMode>(initialMode);
  const [query, setQuery] = useState("");
  const [seasons, setSeasons] = useState<
    Record<string, { id: string; label: string }[] | "loading" | "error">
  >({});
  const [result, setResult] = useState<RetriggerResponse | null>(null);
  const [localError, setLocalError] = useState<string | null>(null);

  // Re-seed whenever the dialog opens (it is mounted once per page).
  useEffect(() => {
    if (!open) return;
    setSubjects(new Map((initialSubjects ?? []).map((s) => [keyOf(s), s])));
    setChosenStages(new Set(initialStages ?? []));
    setMode(initialMode);
    setQuery("");
    setSeasons({});
    setResult(null);
    setLocalError(null);
    clear();
  }, [open]);

  const offered = useMemo(
    () => stages.filter((s) => !s.hidden && s.enabled).sort((a, b) => a.lane_order - b.lane_order),
    [stages]
  );
  const stageByName = useMemo(() => new Map(stages.map((s) => [s.stage, s])), [stages]);

  const toggleSubject = (s: RetriggerSubject) =>
    setSubjects((prev) => {
      const next = new Map(prev);
      if (next.has(keyOf(s))) next.delete(keyOf(s));
      else next.set(keyOf(s), s);
      return next;
    });

  async function loadSeasons(libId: string) {
    if (seasons[libId] && seasons[libId] !== "error") return;
    setSeasons((p) => ({ ...p, [libId]: "loading" }));
    try {
      const h = await queueApi.history("library", libId);
      setSeasons((p) => ({
        ...p,
        [libId]: (h.seasons ?? []).map((s) => ({ id: s.id, label: s.label })),
      }));
    } catch {
      setSeasons((p) => ({ ...p, [libId]: "error" }));
    }
  }

  const q = query.trim().toLowerCase();
  const shownLibs = libraries.filter((l) => l.name.toLowerCase().includes(q));
  // Subjects that came in prefilled but are not libraries (season / video / catalog).
  const libKeys = new Set(libraries.map((l) => `library:${l.library_id}`));
  const extras = [...subjects.values()].filter((s) => !libKeys.has(keyOf(s)) && !isListedSeason(s));
  function isListedSeason(s: RetriggerSubject) {
    return (
      s.kind === "season" &&
      Object.values(seasons).some((v) => Array.isArray(v) && v.some((x) => x.id === s.id))
    );
  }

  async function submit() {
    setLocalError(null);
    const items: RetriggerRequest["items"] = [];
    for (const s of subjects.values()) {
      if (chosenStages.size === 0) {
        items.push({ subject_kind: s.kind, subject_id: s.id });
        continue;
      }
      for (const st of chosenStages) {
        if (stageByName.get(st)?.subject_kinds.includes(s.kind)) {
          items.push({ subject_kind: s.kind, subject_id: s.id, stage: st });
        }
      }
    }
    if (items.length === 0) {
      setLocalError(t("queue.retrigger.noMatch"));
      return;
    }
    const res = await run("retrigger", () => queueApi.retrigger({ items, mode, priority: 50 }));
    if (res) {
      setResult(res);
      onDone?.(res);
    }
  }

  const sum = result ? summarize(result) : null;

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t("queue.retrigger.title")}
      footer={
        result ? (
          <Button onClick={onClose}>{t("queue.retrigger.close")}</Button>
        ) : (
          <>
            <Button variant="ghost" onClick={onClose}>
              {t("queue.retrigger.cancel")}
            </Button>
            <Button
              loading={busy === "retrigger"}
              disabled={subjects.size === 0}
              onClick={() => void submit()}
            >
              {t("queue.retrigger.submit", { n: n(subjects.size) })}
            </Button>
          </>
        )
      }
    >
      {result && sum ? (
        <div role="status" className="mkt-q-retrigger__done">
          <p>
            {t("queue.retrigger.result", {
              queued: n(sum.queued),
              merged: n(sum.merged),
              skipped: n(sum.skipped),
            })}
          </p>
        </div>
      ) : (
        <form className="mkt-q-retrigger" onSubmit={(e) => e.preventDefault()}>
          <fieldset className="mkt-q-fieldset">
            <legend>{t("queue.retrigger.what")}</legend>
            <label className="mkt-q-field">
              <span className="mkt-visually-hidden">{t("queue.retrigger.search")}</span>
              <input
                type="search"
                value={query}
                placeholder={t("queue.retrigger.search")}
                onChange={(e) => setQuery(e.target.value)}
              />
            </label>
            <ul className="mkt-q-picklist">
              {extras.map((s) => (
                <li key={keyOf(s)}>
                  <label className="mkt-q-check">
                    <input type="checkbox" checked onChange={() => toggleSubject(s)} />
                    <span>
                      {bidiIsolate(s.label)}{" "}
                      <span className="mkt-q-muted">{t(`queue.subject.${s.kind}`)}</span>
                    </span>
                  </label>
                </li>
              ))}
              {shownLibs.map((l) => {
                const sub: RetriggerSubject = { kind: "library", id: l.library_id, label: l.name };
                const ss = seasons[l.library_id];
                return (
                  <li key={l.library_id}>
                    <div className="mkt-q-pick__row">
                      <label className="mkt-q-check">
                        <input
                          type="checkbox"
                          checked={subjects.has(keyOf(sub))}
                          onChange={() => toggleSubject(sub)}
                        />
                        <span>
                          {bidiIsolate(l.name)}{" "}
                          <span className="mkt-q-muted">{t("queue.subject.library")}</span>
                        </span>
                      </label>
                      <Button
                        size="sm"
                        variant="link"
                        type="button"
                        aria-expanded={Array.isArray(ss)}
                        onClick={() => void loadSeasons(l.library_id)}
                      >
                        {t("queue.retrigger.seasons")}
                        <span className="mkt-visually-hidden"> {l.name}</span>
                      </Button>
                    </div>
                    {ss === "loading" && <p className="mkt-q-muted">{t("common.loading")}</p>}
                    {ss === "error" && <p role="alert">{t("queue.retrigger.seasonsError")}</p>}
                    {Array.isArray(ss) && (
                      <ul className="mkt-q-picklist mkt-q-picklist--nested">
                        {ss.length === 0 && (
                          <li className="mkt-q-muted">{t("queue.retrigger.noSeasons")}</li>
                        )}
                        {ss.map((s) => {
                          const sSub: RetriggerSubject = {
                            kind: "season",
                            id: s.id,
                            label: s.label,
                          };
                          return (
                            <li key={s.id}>
                              <label className="mkt-q-check">
                                <input
                                  type="checkbox"
                                  checked={subjects.has(keyOf(sSub))}
                                  onChange={() => toggleSubject(sSub)}
                                />
                                <span>{bidiIsolate(s.label)}</span>
                              </label>
                            </li>
                          );
                        })}
                      </ul>
                    )}
                  </li>
                );
              })}
            </ul>
          </fieldset>

          <fieldset className="mkt-q-fieldset">
            <legend>{t("queue.retrigger.queues")}</legend>
            <p className="mkt-q-muted">{t("queue.retrigger.queuesHint")}</p>
            <ul className="mkt-q-picklist mkt-q-picklist--cols">
              {offered.map((s) => (
                <li key={s.stage}>
                  <label className="mkt-q-check">
                    <input
                      type="checkbox"
                      checked={chosenStages.has(s.stage)}
                      onChange={() =>
                        setChosenStages((prev) => {
                          const next = new Set(prev);
                          if (next.has(s.stage)) next.delete(s.stage);
                          else next.add(s.stage);
                          return next;
                        })
                      }
                    />
                    <span>{s.label}</span>
                  </label>
                </li>
              ))}
            </ul>
          </fieldset>

          <fieldset className="mkt-q-fieldset">
            <legend>{t("queue.retrigger.mode")}</legend>
            {MODES.map((m) => (
              <label className="mkt-q-radio" key={m}>
                <input
                  type="radio"
                  name="mkt-q-mode"
                  value={m}
                  checked={mode === m}
                  onChange={() => setMode(m)}
                />
                <span>
                  <strong>{t(`queue.mode.${m}`)}</strong>
                  <span className="mkt-q-muted"> {t(`queue.mode.${m}.help`)}</span>
                </span>
              </label>
            ))}
          </fieldset>
          {(error || localError) && (
            <p role="alert" className="mkt-q-error">
              {localError ?? error}
            </p>
          )}
        </form>
      )}
    </Modal>
  );
}
