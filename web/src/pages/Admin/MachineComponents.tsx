// Epic 51 (story 51.12): the components on one machine. It sits on the machine's card on
// /admin/machines (anchor #components-<node_id>, which the Components page links to) and
// can be dropped anywhere else a machine is shown.
//
// Each row says what the machine does with a component: the home's want or its own
// override ("Off here (home: on)"), stage switches that leave it partly off ("On, 1 stage
// off: Transcribe"), what the component needs against what the machine has, and how long
// the machine has been silent when its phase may be old. Every change asks for a preview
// first and waits in 51.10's confirm dialog when it touches more than its own row.
import { useCallback, useEffect, useState } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Checkbox } from "@ds/components/Choice/Checkbox";
import { useToast } from "@ds/components/Toast/Toast";
import { errorText } from "../../lib/errorText";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import type { Preview } from "../../lib/componentsApi";
import { refusalOf } from "../../lib/componentsApi";
import {
  isMachineList,
  machineComponentsApi,
  type MachineChange,
  type MachineComponent,
  type MachineList,
} from "../../lib/machineComponentsApi";
import { ComponentConfirm, isSimple, type Action, type Plan } from "../Settings/ComponentConfirm";
import { joinNames, phaseTone, phaseWord, titleFinder } from "../Settings/componentWords";

export type MachineAction = "follow" | "off" | "stop" | "start";

/** The anchor the Components page links to for one machine. */
export const componentsAnchor = (node: string) => `components-${node}`;

/** The change a row's action sends, with the override's generation (0: none yet). */
export function machineChange(c: MachineComponent, action: MachineAction): MachineChange {
  const generation = c.here.override?.generation ?? 0;
  switch (action) {
    case "follow":
      return c.here.override?.hold
        ? { id: c.id, want: "follow", hold: false, generation }
        : { id: c.id, want: "follow", generation };
    case "off":
      return { id: c.id, want: "installed", generation };
    case "stop":
      return { id: c.id, hold: true, generation };
    case "start":
      return { id: c.id, hold: false, generation };
  }
}

/** The rows worth showing for a machine: those it runs, was offered, or set apart. */
function relevant(c: MachineComponent): boolean {
  if (!c.switchable || c.kind === "core") return false;
  return (
    c.here.worker_offer ||
    c.here.override !== null ||
    c.here.reported ||
    c.here.stages_off.length > 0
  );
}

/** The confirm dialog's action for a machine action (51.10's words). */
const CONFIRM: Record<MachineAction, Action> = {
  follow: "on",
  off: "off",
  stop: "stop",
  start: "start",
};

export function MachineComponents({
  node,
  name,
  stageLabel = (s) => s,
  onChanged,
}: {
  node: string;
  name: string;
  /** a stage's name in words (the Machines page's queue labels) */
  stageLabel?: (stage: string) => string;
  /** the machine's own card changed too (its stage switches) */
  onChanged?: () => void;
}) {
  const { t, n, locale } = useI18n();
  const toast = useToast();
  const [list, setList] = useState<MachineList | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [plan, setPlan] = useState<(Plan & { change: MachineChange }) | null>(null);

  const reload = useCallback(async () => {
    try {
      const l = await machineComponentsApi.list(node);
      setList(isMachineList(l) ? l : null);
    } catch {
      // an older home has no per-machine list: the section stays out of the card
      setList(null);
    }
  }, [node]);

  useEffect(() => {
    void reload();
  }, [reload]);

  // the Components page links here: the card loads after the page, so scroll once it has
  const loaded = list !== null;
  useEffect(() => {
    if (loaded && window.location.hash === `#${componentsAnchor(node)}`) {
      document.getElementById(componentsAnchor(node))?.scrollIntoView?.({ block: "start" });
    }
  }, [loaded, node]);

  const titleOf = titleFinder(t, list?.components ?? []);

  const fail = useCallback(
    (e: unknown) => {
      const r = refusalOf(e);
      toast.show({ tone: "error", message: r?.detail || errorText(e, t) });
    },
    [t, toast]
  );

  const commit = useCallback(
    async (change: MachineChange) => {
      setBusy(change.id);
      try {
        await machineComponentsApi.apply(node, change);
        setPlan(null);
        await reload();
        onChanged?.();
      } catch (e) {
        fail(e);
      } finally {
        setBusy(null);
      }
    },
    [fail, node, onChanged, reload]
  );

  const act = useCallback(
    async (c: MachineComponent, action: MachineAction) => {
      if (busy) return;
      const change = machineChange(c, action);
      setBusy(c.id);
      let preview: Preview;
      try {
        preview = await machineComponentsApi.preview(node, change);
      } catch (e) {
        setBusy(null);
        fail(e);
        return;
      }
      setBusy(null);
      if (preview.refused.some((r) => r.code === "stale_generation")) {
        await reload();
        toast.show({ tone: "warn", message: t("components.stale") });
        return;
      }
      if (isSimple(preview, c.id)) {
        await commit(change);
        return;
      }
      setPlan({ action: CONFIRM[action], view: c, preview, change });
    },
    [busy, commit, fail, node, reload, t, toast]
  );

  if (!list) return null;
  const rows = list.components.filter(relevant);
  const who = bidiIsolate(name);
  const heard = list.machine.heard_at ? Date.parse(list.machine.heard_at) : NaN;
  const silentMin = Number.isNaN(heard)
    ? null
    : Math.max(1, Math.round((Date.now() - heard) / 60_000));

  return (
    <section
      id={componentsAnchor(node)}
      className="mkt-mc-sub mkt-mc-components"
      aria-label={t("machines.components.title", { name: who })}
    >
      <h4 className="mkt-mc-sub__title">{t("machines.components.title", { name: who })}</h4>
      {list.worker_offered > 0 && (
        <p className="mkt-mc-muted">
          {t("machines.components.worker", {
            on: n(list.worker_on),
            of: n(list.worker_offered),
          })}
        </p>
      )}
      {list.machine.stale && silentMin !== null && (
        <p className="mkt-mc-note mkt-mc-note--warn" role="note">
          {t("machines.components.stale", { minutes: n(silentMin) })}
        </p>
      )}
      {rows.length === 0 ? (
        <p className="mkt-mc-muted">{t("machines.components.none")}</p>
      ) : (
        <ul className="mkt-mc-comp-list">
          {rows.map((c) => (
            <Row
              key={c.id}
              c={c}
              title={titleOf(c.id)}
              stageNames={joinNames(locale, c.here.stages_off.map(stageLabel))}
              busy={busy !== null}
              onAction={(a) => void act(c, a)}
            />
          ))}
        </ul>
      )}
      <ComponentConfirm
        plan={plan}
        busy={busy !== null}
        titleOf={titleOf}
        onCancel={() => setPlan(null)}
        onConfirm={() => {
          if (plan) void commit(plan.change);
        }}
        onShow={() => setPlan(null)}
      />
    </section>
  );
}

function Row({
  c,
  title,
  stageNames,
  busy,
  onAction,
}: {
  c: MachineComponent;
  title: string;
  stageNames: string;
  busy: boolean;
  onAction: (a: MachineAction) => void;
}) {
  const { t, n } = useI18n();
  const here = c.here;
  const homeOn = c.want === "enabled";
  const offHere = here.want !== "enabled";
  const held = here.override?.hold != null;
  const ownWant = here.override?.want !== undefined;

  let state: string;
  if (ownWant && offHere && homeOn) state = t("machines.components.offHere");
  else if (ownWant && !offHere && !homeOn) state = t("machines.components.onHere");
  else if (held) state = t("machines.components.stoppedHere");
  else if (here.stages_off.length > 0 && !offHere)
    state =
      here.stages_off.length === 1
        ? t("machines.components.stagesOff.one", { stages: stageNames })
        : t("machines.components.stagesOff", {
            count: n(here.stages_off.length),
            stages: stageNames,
          });
  else state = phaseWord(t, here.phase, c.kind);

  const need = needWords(t, n, c);

  return (
    <li className="mkt-mc-comp" data-component={c.id}>
      <div className="mkt-mc-row">
        <strong>{title}</strong>
        <Badge tone={here.cannot_run ? "warn" : phaseTone(here.phase, c.kind)}>{state}</Badge>
        {!ownWant && !held && (
          <span className="mkt-mc-muted">{t("machines.components.follows")}</span>
        )}
        {need.length > 0 && <span className="mkt-mc-muted">{need.join(" · ")}</span>}
      </div>
      {here.cannot_run && (
        <p className="mkt-mc-note mkt-mc-note--warn" role="note">
          {t(`machines.components.cannotRun.${here.cannot_run}`)}
        </p>
      )}
      <div className="mkt-mc-actions">
        {(ownWant || held) && (
          <Button
            variant="secondary"
            disabled={busy}
            aria-label={t("machines.components.followNamed", { name: title })}
            onClick={() => onAction("follow")}
          >
            {t("machines.components.follow")}
          </Button>
        )}
        {!offHere && (
          <Button
            variant="secondary"
            disabled={busy}
            aria-label={t("machines.components.turnOffNamed", { name: title })}
            onClick={() => onAction("off")}
          >
            {t("machines.components.turnOff")}
          </Button>
        )}
        {!offHere && !held && (
          <Button
            variant="secondary"
            disabled={busy}
            aria-label={t("machines.components.stopNamed", { name: title })}
            onClick={() => onAction("stop")}
          >
            {t("machines.components.stop")}
          </Button>
        )}
        {held && (
          <Button
            variant="secondary"
            disabled={busy}
            aria-label={t("machines.components.startNamed", { name: title })}
            onClick={() => onAction("start")}
          >
            {t("machines.components.start")}
          </Button>
        )}
      </div>
    </li>
  );
}

/** What a component needs, in words, and why the machine cannot run it. */
function needWords(
  t: ReturnType<typeof useI18n>["t"],
  n: ReturnType<typeof useI18n>["n"],
  c: MachineComponent
): string[] {
  const bits: string[] = [];
  if (c.here.ram_mb)
    bits.push(t("machines.components.needRam", { gb: n(Math.ceil(c.here.ram_mb / 1024)) }));
  if (c.here.gpu) bits.push(t("machines.components.needGpu"));
  return bits;
}

/**
 * The worker components offered when a machine is approved (story 51.12): each ticked,
 * with what it needs and whether the machine can run it. Unticked ones are set off on
 * that machine by the approve call.
 */
export function WorkerChoice({
  node,
  disabled,
  onChange,
}: {
  node: string;
  disabled?: boolean;
  /** the ids to keep, or null while there is nothing to choose */
  onChange: (keep: string[] | null) => void;
}) {
  const { t, n } = useI18n();
  const [offered, setOffered] = useState<MachineComponent[]>([]);
  const [keep, setKeep] = useState<Set<string>>(new Set());

  useEffect(() => {
    let live = true;
    machineComponentsApi
      .list(node)
      .then((l) => {
        if (!live || !isMachineList(l)) return;
        const o = l.components.filter((c) => c.here.worker_offer);
        const ids = o.filter((c) => !c.here.cannot_run).map((c) => c.id);
        setOffered(o);
        setKeep(new Set(ids));
        onChange(o.length ? ids : null);
      })
      .catch(() => {
        // an older home offers nothing: approve sends no components
      });
    return () => {
      live = false;
    };
    // onChange is the parent's state setter: the offer is read once per machine
  }, [node]);

  if (offered.length === 0) return null;
  const titleOf = titleFinder(t, offered);
  const toggle = (id: string, on: boolean) => {
    const next = new Set(keep);
    if (on) next.add(id);
    else next.delete(id);
    setKeep(next);
    onChange(offered.filter((c) => next.has(c.id)).map((c) => c.id));
  };

  return (
    <fieldset className="mkt-mc-worker-choice">
      <legend>{t("machines.pending.components")}</legend>
      <p className="mkt-mc-muted">{t("machines.pending.componentsHelp")}</p>
      {offered.map((c) => {
        const bits = needWords(t, n, c);
        if (c.here.cannot_run) bits.push(t(`machines.components.cannotRun.${c.here.cannot_run}`));
        return (
          <div key={c.id} className="mkt-mc-worker-choice__item">
            <Checkbox
              label={titleOf(c.id)}
              checked={keep.has(c.id)}
              disabled={disabled}
              onChange={(e) => toggle(c.id, e.currentTarget.checked)}
            />
            {bits.length > 0 && <span className="mkt-mc-muted">{bits.join(" · ")}</span>}
          </div>
        );
      })}
    </fieldset>
  );
}
