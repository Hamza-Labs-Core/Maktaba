// Epic 51 (story 51.10): Settings → Components (admins). Every part of the home in its
// catalog group, with its phase and why in words, an On/Off switch (want enabled or
// installed), one Stop button that holds it until an admin starts it again, the ⋯ drawer,
// and a Machines link when a machine of the home differs.
//
// Every change asks POST /api/components/preview first. A change that touches only its own
// row and parks nothing applies at once with a 5 s undo toast; anything else waits in the
// confirm dialog (ComponentConfirm). Writes send the generation the page read; a 409
// stale_generation reloads the list and says someone else changed it.
//
// Live: the list is read every POLL_MS while the tab is visible, and at once after a write.
// When /ws/home (story 51.09) sends the components change signal, call `reload` from it and
// keep the poll as the fallback.
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { IconButton } from "@ds/components/IconButton/IconButton";
import { Toggle } from "@ds/components/Choice/Toggle";
import { useToast } from "@ds/components/Toast/Toast";
import { useAuth } from "../../lib/auth";
import {
  componentsApi,
  refusalOf,
  type ComponentView,
  type ListView,
  type Preview,
} from "../../lib/componentsApi";
import { errorText } from "../../lib/errorText";
import { useI18n } from "../../lib/i18n";
import { HelpLink } from "../../help/HelpLink";
import { LinesPlaceholder } from "../../components/Placeholders";
import { useBytes } from "../../components/queue/offline";
import { ComponentConfirm, isSimple, type Action, type Plan } from "./ComponentConfirm";
import { ComponentDrawer } from "./ComponentDrawer";
import { BundleDialog } from "./BundleDialog";
import { ComponentSettingsLink } from "./ComponentSettingsLink";
import {
  BUSY_PHASES,
  LOCKED_PHASES,
  groupRows,
  joinNames,
  machinesApart,
  phaseTone,
  phaseWord,
  reasonText,
  titleFinder,
} from "./componentWords";
import "./components.css";

/** How often the open page reads the list. */
export const POLL_MS = 5_000;
/** How long the undo toast of a change that applied at once stays. */
export const UNDO_MS = 5_000;
/** More model.llm.* rows than this fold under one line. */
const FOLD_AFTER = 3;

export function ComponentsSection({ pollMs = POLL_MS }: { pollMs?: number } = {}) {
  const { user } = useAuth();
  const { t } = useI18n();
  if (!user?.is_admin) {
    return (
      <ErrorState
        kind="permission"
        title={t("common.forbidden.title")}
        description={t("common.forbidden.desc")}
      />
    );
  }
  return <ComponentsInner pollMs={pollMs} />;
}

/** The list, read now and every pollMs while the tab is visible; the last answer stays on error. */
function useComponentList(pollMs: number) {
  const [list, setList] = useState<ListView | null>(null);
  const [error, setError] = useState<unknown>(null);
  const alive = useRef(true);
  const last = useRef("");

  const reload = useCallback(async () => {
    try {
      const v = await componentsApi.list();
      if (!alive.current) return;
      const key = JSON.stringify(v);
      if (key !== last.current) {
        last.current = key;
        setList(v);
      }
      setError(null);
    } catch (e) {
      if (alive.current) setError(e);
    }
  }, []);

  useEffect(() => {
    alive.current = true;
    let timer: number | null = null;
    const start = () => {
      if (timer === null) timer = window.setInterval(() => void reload(), pollMs);
    };
    const stop = () => {
      if (timer !== null) window.clearInterval(timer);
      timer = null;
    };
    const onVisibility = () => {
      if (document.visibilityState === "visible") {
        void reload();
        start();
      } else {
        stop();
      }
    };
    void reload();
    if (document.visibilityState === "visible") start();
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      alive.current = false;
      stop();
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [reload, pollMs]);

  return { list, error, reload };
}

function ComponentsInner({ pollMs }: { pollMs: number }) {
  const { t, locale } = useI18n();
  const toast = useToast();
  const { list, error, reload } = useComponentList(pollMs);
  const [plan, setPlan] = useState<Plan | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [drawerId, setDrawerId] = useState<string | null>(null);
  const [flash, setFlash] = useState<string | null>(null);
  const [bundleOpen, setBundleOpen] = useState(false);

  const views = useMemo(() => list?.components ?? [], [list]);
  const titleOf = useMemo(() => titleFinder(t, views), [t, views]);
  const groups = useMemo(() => groupRows(views), [views]);
  const offline = error !== null && list !== null;
  const byId = useMemo(() => new Map(views.map((v) => [v.id, v])), [views]);

  const fail = useCallback(
    async (e: unknown, id: string) => {
      const r = refusalOf(e);
      if (r?.code === "stale_generation") {
        await reload();
        setFlash(id);
        toast.show({ tone: "warn", message: t("components.stale") });
        return;
      }
      if (r && r.code !== "stale_generation") {
        const blockers = r.required_by ?? r.conflicts ?? [];
        const key = `components.refused.${r.code}`;
        const text = t(key, {
          name: titleOf(r.id ?? id),
          components: joinNames(locale, blockers.map(titleOf)),
        });
        toast.show({ tone: "error", message: text === key ? errorText(e, t) : text });
        void reload();
        return;
      }
      toast.show({ tone: "error", message: errorText(e, t) });
    },
    [reload, t, titleOf, toast, locale]
  );

  /** Writes the change; answers the generation after it, for an undo. */
  const write = useCallback(async (v: ComponentView, action: Action, withConflicts: boolean) => {
    switch (action) {
      case "on":
        return componentsApi.setWant(v.id, "enabled", v.generation, withConflicts);
      case "off":
        return componentsApi.setWant(v.id, "installed", v.generation);
      case "remove":
        return componentsApi.setWant(v.id, "absent", v.generation);
      case "install":
        return componentsApi.install(v.id);
      case "stop":
        return componentsApi.stop(v.id);
      case "start":
        return componentsApi.start(v.id);
    }
  }, []);

  const undoOf = useCallback((before: ComponentView, action: Action, generation: number) => {
    switch (action) {
      case "on":
      case "off":
        return () => componentsApi.setWant(before.id, before.want, generation);
      case "stop":
        return () => componentsApi.start(before.id);
      case "start":
        return () => componentsApi.stop(before.id);
      default:
        return null;
    }
  }, []);

  const commit = useCallback(
    async (v: ComponentView, action: Action, withConflicts: boolean, offerUndo: boolean) => {
      setBusy(v.id);
      try {
        const after = await write(v, action, withConflicts);
        setPlan(null);
        await reload();
        const undo = offerUndo ? undoOf(v, action, after.generation) : null;
        const name = titleOf(v.id);
        const id = toast.show({
          tone: "success",
          durationMs: UNDO_MS,
          message: (
            <span className="mkt-comp-toast">
              {t(`components.done.${action}`, { name })}
              {undo && (
                <Button
                  variant="link"
                  onClick={() => {
                    toast.dismiss(id);
                    undo()
                      .then(() => reload())
                      .catch((e: unknown) => fail(e, v.id));
                  }}
                >
                  {t("components.undo")}
                </Button>
              )}
            </span>
          ),
        });
      } catch (e) {
        setPlan(null);
        await fail(e, v.id);
      } finally {
        setBusy(null);
      }
    },
    [write, reload, undoOf, titleOf, toast, t, fail]
  );

  const act = useCallback(
    async (v: ComponentView, action: Action) => {
      if (busy) return;
      setFlash(null);
      setBusy(v.id);
      let preview: Preview;
      try {
        preview = await componentsApi.preview({
          changes: [
            action === "stop" || action === "start"
              ? { id: v.id, hold: action === "stop", generation: v.generation }
              : {
                  id: v.id,
                  want:
                    action === "on"
                      ? "enabled"
                      : action === "off"
                        ? "installed"
                        : action === "remove"
                          ? "absent"
                          : "installed",
                  generation: v.generation,
                },
          ],
          with_conflicts: action === "on",
        });
      } catch (e) {
        setBusy(null);
        await fail(e, v.id);
        return;
      }
      setBusy(null);
      const stale = preview.refused.find((r) => r.code === "stale_generation");
      if (stale) {
        await reload();
        setFlash(v.id);
        toast.show({ tone: "warn", message: t("components.stale") });
        return;
      }
      // removing deletes files: it always asks, and has no undo
      if (action !== "remove" && isSimple(preview, v.id)) {
        await commit(v, action, false, true);
        return;
      }
      setPlan({ action, view: v, preview });
    },
    [busy, commit, fail, reload, toast, t]
  );

  const show = useCallback((id: string) => {
    setPlan(null);
    setDrawerId(null);
    document.getElementById(rowId(id))?.scrollIntoView?.({ block: "center" });
    setFlash(id);
  }, []);

  if (!list) {
    return (
      <div className="mkt-settings__panel mkt-comp">
        <Head />
        {error !== null ? (
          <div role="alert" className="mkt-comp__error">
            <p>{errorText(error, t)}</p>
            <Button variant="secondary" onClick={() => void reload()}>
              {t("common.retry")}
            </Button>
          </div>
        ) : (
          <LinesPlaceholder lines={8} />
        )}
      </div>
    );
  }

  return (
    <div className={`mkt-settings__panel mkt-comp${offline ? " is-offline" : ""}`}>
      <Head
        list={list}
        titleOf={titleOf}
        onChangeBundle={offline ? undefined : () => setBundleOpen(true)}
      />
      {bundleOpen && (
        <BundleDialog
          basedOn={list.bundle?.base ?? ""}
          titleOf={titleOf}
          onClose={() => setBundleOpen(false)}
          onApplied={(b) => {
            setBundleOpen(false);
            void reload();
            toast.show({
              tone: "success",
              message: t("components.bundle.done", { bundle: t(`components.bundle.${b}`) }),
            });
          }}
        />
      )}
      {offline && (
        <p role="alert" className="mkt-comp__offline">
          {errorText(error, t)}
        </p>
      )}
      {groups.map(([group, rows]) => (
        <Group
          key={group}
          group={group}
          rows={rows}
          titleOf={titleOf}
          disabled={offline || busy !== null}
          busy={busy}
          flash={flash}
          onAction={(v, a) => void act(v, a)}
          onMore={setDrawerId}
        />
      ))}
      <ComponentConfirm
        plan={plan}
        busy={busy !== null}
        titleOf={titleOf}
        onCancel={() => setPlan(null)}
        onConfirm={() => {
          if (plan) void commit(plan.view, plan.action, plan.preview.conflicts.length > 0, false);
        }}
        onShow={show}
      />
      <ComponentDrawer
        view={drawerId ? (byId.get(drawerId) ?? null) : null}
        revision={list.revision}
        titleOf={titleOf}
        disabled={offline || busy !== null}
        onClose={() => setDrawerId(null)}
        onAction={(v, a) => void act(v, a)}
      />
    </div>
  );
}

function Head({
  list,
  titleOf,
  onChangeBundle,
}: {
  list?: ListView;
  titleOf?: (id: string) => string;
  /** opens the Change bundle dialog (story 51.17) */
  onChangeBundle?: () => void;
}) {
  const { t, locale } = useI18n();
  const b = list?.bundle;
  const parts: string[] = [];
  if (b?.base && titleOf) {
    parts.push(t("components.bundle.based", { bundle: t(`components.bundle.${b.base}`) }));
    if (b.plus.length)
      parts.push(
        t("components.bundle.plus", { components: joinNames(locale, b.plus.map(titleOf)) })
      );
    if (b.minus.length)
      parts.push(
        t("components.bundle.minus", { components: joinNames(locale, b.minus.map(titleOf)) })
      );
  }
  return (
    <header className="mkt-comp__head">
      <h2>{t("components.page.title")}</h2>
      {parts.length > 0 && (
        <p className="mkt-comp__bundle" data-testid="comp-bundle">
          {parts.join(t("components.bundle.sep"))}
        </p>
      )}
      {onChangeBundle && (
        <Button variant="secondary" onClick={onChangeBundle} data-testid="comp-change-bundle">
          {t("components.bundle.change")}
        </Button>
      )}
      <p className="mkt-comp__intro">
        {t("components.page.intro")} <HelpLink to={{ topic: "components" }} />
      </p>
    </header>
  );
}

function Group({
  group,
  rows,
  titleOf,
  disabled,
  busy,
  flash,
  onAction,
  onMore,
}: {
  group: string;
  rows: ComponentView[];
  titleOf: (id: string) => string;
  disabled: boolean;
  busy: string | null;
  flash: string | null;
  onAction: (v: ComponentView, a: Action) => void;
  onMore: (id: string) => void;
}) {
  const { t, n } = useI18n();
  const headingId = `mkt-comp-group-${group}`;
  const llm = rows.filter((r) => r.id.startsWith("model.llm."));
  const fold = llm.length > FOLD_AFTER;
  const shown = fold ? rows.filter((r) => !r.id.startsWith("model.llm.")) : rows;
  const row = (v: ComponentView) => (
    <Row
      key={v.id}
      v={v}
      titleOf={titleOf}
      disabled={disabled}
      busy={busy === v.id}
      flash={flash === v.id}
      onAction={onAction}
      onMore={onMore}
    />
  );
  const title = t(`components.group.${group}`);
  return (
    <section className="mkt-comp__group" aria-labelledby={headingId} data-group={group}>
      <h3 id={headingId}>{title === `components.group.${group}` ? group : title}</h3>
      <ul className="mkt-comp__rows">
        {shown.map(row)}
        {fold && (
          <li className="mkt-comp__fold">
            <details>
              <summary>{t("components.page.llmModels", { count: n(llm.length) })}</summary>
              <ul className="mkt-comp__rows">{llm.map(row)}</ul>
            </details>
          </li>
        )}
      </ul>
    </section>
  );
}

const rowId = (id: string) => `component-${id.replace(/[^a-z0-9_-]/gi, "-")}`;

function Row({
  v,
  titleOf,
  disabled,
  busy,
  flash,
  onAction,
  onMore,
}: {
  v: ComponentView;
  titleOf: (id: string) => string;
  disabled: boolean;
  busy: boolean;
  flash: boolean;
  onAction: (v: ComponentView, a: Action) => void;
  onMore: (id: string) => void;
}) {
  const { t, n, locale } = useI18n();
  const bytes = useBytes();
  const name = titleOf(v.id);
  const asset = v.kind === "asset";
  const held = v.hold !== null;
  const locked = LOCKED_PHASES.has(v.phase);
  const off = disabled || locked || busy;
  const apart = machinesApart(v);
  const total = v.machines.length;
  // the phase word says "Stopped until you turn it on" already; "an admin stopped it" adds nothing
  const why = v.reason === "held" ? "" : reasonText(t, locale, v.reason, titleOf);
  const chip = [
    phaseWord(t, v.phase, v.kind),
    why,
    total > 1
      ? apart.length
        ? t("components.page.machinesOf", { count: n(total - apart.length), total: n(total) })
        : t("components.page.machines", { count: n(total) })
      : "",
    asset && v.install_size_mb > 0 && v.want !== "absent"
      ? bytes(v.install_size_mb * 1024 * 1024)
      : "",
  ].filter(Boolean);

  return (
    <li
      id={rowId(v.id)}
      className={`mkt-comp-row${flash ? " is-flash" : ""}`}
      data-component={v.id}
    >
      <div className="mkt-comp-row__name">
        <span className="mkt-comp-row__title">{name}</span>
        <span className="mkt-comp-row__desc">{t(v.desc_key)}</span>
      </div>
      <div className="mkt-comp-row__state">
        <Badge tone={phaseTone(v.phase, v.kind)} data-testid="comp-chip">
          {BUSY_PHASES.has(v.phase) && <span className="mkt-comp-spin" aria-hidden="true" />}
          {chip.join(" · ")}
        </Badge>
        {apart.length > 0 && (
          <Link
            className="mkt-comp-row__machines"
            to={`/admin/machines#components-${apart[0]}`}
            aria-label={t("components.page.machinesLinkOf", { name })}
          >
            {t("components.page.machinesLink")}
          </Link>
        )}
      </div>
      <div className="mkt-comp-row__actions">
        {v.switchable && !asset && (
          <Toggle
            checked={v.want === "enabled"}
            disabled={off}
            aria-label={name}
            label={v.want === "enabled" ? t("components.page.on") : t("components.page.off")}
            onChange={() => onAction(v, v.want === "enabled" ? "off" : "on")}
          />
        )}
        {v.switchable && !asset && v.want === "enabled" && !held && (
          <Button
            size="sm"
            variant="secondary"
            disabled={off}
            title={t("components.page.stopUntil")}
            aria-label={t("components.page.stopOf", { name })}
            onClick={() => onAction(v, "stop")}
          >
            {t("components.page.stop")}
          </Button>
        )}
        {v.switchable && held && (
          <Button
            size="sm"
            variant="primary"
            disabled={off}
            aria-label={t("components.page.startOf", { name })}
            onClick={() => onAction(v, "start")}
          >
            {t("components.page.start")}
          </Button>
        )}
        <ComponentSettingsLink view={v} name={name} />
        <IconButton
          size="sm"
          variant="ghost"
          label={t("components.page.more", { name })}
          icon={<span aria-hidden="true">⋯</span>}
          onClick={() => onMore(v.id)}
        />
      </div>
    </li>
  );
}
