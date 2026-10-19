// The channel editor: Basics, Art, Schedule (the grid, or the rule of a
// non-grid channel), Filler, Live broadcasts and Preview, in tabs, with one
// Save. A channel the lineup plan wrote says so up front: saving a change
// makes it stop following the plan, and an edited one can go "back to plan".
//
// Only what really changed is sent (PATCH), so opening and saving a plan
// channel, or changing only its art or its live broadcasts, is not an edit.
import { useEffect, useMemo, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { Modal } from "@ds/components/Modal/Modal";
import { Select } from "@ds/components/Select/Select";
import { Tabs } from "@ds/components/Tabs/Tabs";
import { Toggle } from "@ds/components/Choice/Toggle";
import { useToast } from "@ds/components/Toast/Toast";
import { ApiError } from "../../../lib/api";
import { useI18n } from "../../../lib/i18n";
import { rulesApi, rulesError } from "../../../lib/rulesApi";
import {
  channelArtApi,
  channelOrigin,
  channelsApi,
  fillerApi,
  type ArtKind,
  type Channel,
  type ChannelMode,
  type FillerPool,
} from "../../../lib/channels";
import { ArtSection, PendingArtSection, type PendingArt } from "./ArtSection";
import { FillerSection } from "./FillerSection";
import { LiveSection } from "./LiveSection";
import { PreferHost } from "./PreferHost";
import { PreviewSection } from "./PreviewSection";
import { RuleSection, ruleProblem } from "./RuleSection";
import { ScheduleSection } from "./ScheduleSection";
import { liveProblems, slotProblems, type LiveRow, type Slot } from "./schedule";

const MODES: ChannelMode[] = ["schedule", "shuffle", "marathon", "smart_mix"];
const DEFINITION_FIELDS = [
  "name",
  "number",
  "category",
  "mode",
  "mode_config",
  "transition",
  "enabled",
] as const;

/** The definition fields that differ between the saved channel and the form. */
export function changedFields(before: Partial<Channel>, after: Partial<Channel>): Partial<Channel> {
  const out: Record<string, unknown> = {};
  for (const f of DEFINITION_FIELDS) {
    if (JSON.stringify(before[f] ?? null) !== JSON.stringify(after[f] ?? null)) out[f] = after[f];
  }
  return out as Partial<Channel>;
}

/** Slot problems that block saving (an overlap only warns: the first slot wins). */
function blockingSlotProblems(config: Record<string, unknown>): boolean {
  const grids: Slot[][] = [
    (config.slots as Slot[] | undefined) ?? [],
    ((config.ramadan as { slots?: Slot[] } | undefined)?.slots as Slot[] | undefined) ?? [],
  ];
  return grids.some((g) =>
    g.some((s, i) => slotProblems(s, g, i).some((p) => p !== "channels.slot.err.overlap"))
  );
}

interface LiveState {
  rows: LiveRow[];
  inherited: boolean;
  dirty: boolean;
  loaded: boolean;
}

export function ChannelEditor({
  draft,
  existing,
  onClose,
  onSaved,
  onArtChanged,
  initialTab,
}: {
  draft: Partial<Channel>;
  initialTab?: string;
  existing: Channel[];
  onClose: () => void;
  onSaved: () => void;
  onArtChanged?: () => void;
}) {
  const { t } = useI18n();
  const toast = useToast();
  const isEdit = Boolean(draft.id);
  const [form, setForm] = useState<Partial<Channel>>(draft);
  const [tab, setTab] = useState(initialTab ?? "basics");
  const [saving, setSaving] = useState(false);
  const [pending, setPending] = useState<PendingArt>({});
  const [pools, setPools] = useState<FillerPool[]>([]);
  const [live, setLive] = useState<LiveState>({
    rows: [],
    inherited: false,
    dirty: false,
    loaded: false,
  });
  const [liveFocus, setLiveFocus] = useState<number | null>(null);
  const origin = channelOrigin(draft);

  useEffect(() => {
    fillerApi
      .pools()
      .then((r) => setPools(r.items ?? []))
      .catch(() => setPools([]));
    rulesApi
      .list()
      .then((r) => {
        const field = (sec: string) =>
          r.sections.find((s) => s.key === sec)?.fields.find((f) => f.name === "live")?.value;
        const own = Array.isArray(field("tv")) ? (field("tv") as LiveRow[]) : [];
        const dz = Array.isArray(field("dizquetv")) ? (field("dizquetv") as LiveRow[]) : [];
        // an empty tv.live means "the dizqueTV export's list": editing starts from it,
        // so every other channel keeps its broadcasts
        setLive({
          rows: own.length ? own : dz,
          inherited: !own.length && dz.length > 0,
          dirty: false,
          loaded: true,
        });
      })
      .catch(() => setLive((l) => ({ ...l, loaded: false })));
  }, []);

  const config = useMemo(() => form.mode_config ?? {}, [form.mode_config]);
  function set<K extends keyof Channel>(key: K, value: Channel[K]) {
    setForm((f) => ({ ...f, [key]: value }));
  }
  function setConfig(key: string, value: unknown) {
    setForm((f) => ({ ...f, mode_config: { ...(f.mode_config ?? {}), [key]: value } }));
  }
  function setLiveRows(rows: LiveRow[]) {
    setLive((l) => ({ ...l, rows, dirty: true }));
  }

  const categories = useMemo(
    () =>
      [...new Set(existing.map((c) => c.category).filter((c): c is string => Boolean(c)))].sort(),
    [existing]
  );
  const numberCollision = existing.find((c) => c.number === form.number && c.id !== draft.id);
  const nameError = !form.name?.trim() ? t("channels.err.nameRequired") : undefined;
  const numberError =
    !form.number || form.number <= 0
      ? t("channels.err.numberPositive")
      : numberCollision
        ? t("channels.err.numberTakenBy", { name: numberCollision.name })
        : undefined;
  const mode = (form.mode ?? "schedule") as ChannelMode;
  const rule = ruleProblem(mode, config);
  const slotsBad = mode === "schedule" && blockingSlotProblems(config);
  const number = form.number ?? 0;
  const liveBad = live.rows.some((r) => Number(r.channel) === number && liveProblems(r).length > 0);
  const changes = isEdit ? changedFields(draft, form) : form;
  const channelDirty = Object.keys(changes).length > 0;
  const stopsFollowing = origin === "plan" && channelDirty;
  const blocked = Boolean(nameError || numberError || rule || slotsBad || liveBad);

  async function uploadPending(id: string) {
    for (const kind of Object.keys(pending) as ArtKind[]) {
      const f = pending[kind];
      if (!f) continue;
      try {
        await channelArtApi.upload(id, kind, f);
      } catch (e) {
        toast.show({
          tone: "error",
          message: t("channels.art.uploadFailed", {
            kind: t(`channels.art.kind.${kind}`),
            why: e instanceof ApiError ? e.problem.title : t("common.error"),
          }),
        });
      }
    }
  }

  async function save() {
    if (blocked) return;
    setSaving(true);
    try {
      if (isEdit && draft.id) {
        if (channelDirty) await channelsApi.update(draft.id, changes);
      } else {
        const created = await channelsApi.create(form);
        await uploadPending(created.id);
      }
      if (live.dirty) {
        const old = draft.number;
        const rows =
          isEdit && old !== undefined && old !== form.number
            ? live.rows.map((r) => (Number(r.channel) === old ? { ...r, channel: form.number } : r))
            : live.rows;
        try {
          await rulesApi.patch("tv", { live: rows }, []);
        } catch (e) {
          toast.show({
            tone: "error",
            message: t("channels.live.saveFailed", { why: rulesError(e).message }),
          });
          return;
        }
      }
      onSaved();
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        toast.show({ tone: "error", message: t("channels.err.stale") });
      } else {
        toast.show({
          tone: "error",
          message: e instanceof ApiError ? e.problem.detail || e.problem.title : t("common.error"),
        });
      }
    } finally {
      setSaving(false);
    }
  }

  async function backToPlan() {
    if (!draft.id || !window.confirm(t("channels.plan.backConfirm"))) return;
    try {
      await channelsApi.followPlan(draft.id);
      toast.show({ tone: "success", message: t("channels.plan.backDone") });
      onSaved();
    } catch (e) {
      toast.show({
        tone: "error",
        message: e instanceof ApiError ? e.problem.title : t("common.error"),
      });
    }
  }

  const basics = (
    <div className="mkt-form">
      <Input
        label={t("channels.field.name")}
        value={form.name ?? ""}
        error={nameError}
        onChange={(e) => set("name", e.target.value)}
      />
      <div className="mkt-cb-row">
        <Input
          label={t("channels.field.number")}
          type="number"
          min={1}
          value={String(form.number ?? "")}
          error={numberError}
          onChange={(e) => set("number", Number(e.target.value))}
        />
        <Input
          label={t("channels.field.category")}
          value={form.category ?? ""}
          list="mkt-cb-categories"
          description={t("channels.field.categoryHint")}
          onChange={(e) => set("category", e.target.value)}
        />
        <datalist id="mkt-cb-categories">
          {categories.map((c) => (
            <option key={c} value={c} />
          ))}
        </datalist>
      </div>
      <div className="mkt-cb-row">
        <Select
          label={t("channels.field.mode")}
          value={mode}
          options={MODES.map((m) => ({ value: m, label: t(`channels.mode.${m}`) }))}
          description={t(`channels.mode.help.${mode}`)}
          onChange={(e) => set("mode", e.target.value as ChannelMode)}
        />
        <Select
          label={t("channels.field.transition")}
          value={form.transition ?? "cut"}
          options={[
            { value: "cut", label: t("channels.transition.cut") },
            { value: "crossfade", label: t("channels.transition.crossfade") },
          ]}
          onChange={(e) => set("transition", e.target.value as Channel["transition"])}
        />
      </div>
      <Toggle
        checked={form.enabled ?? false}
        onChange={(e) => set("enabled", e.target.checked)}
        label={t("channels.field.enabled")}
      />
      {isEdit && draft.slug && <PreferHost slug={draft.slug} />}
      {isEdit && <p className="mkt-muted">{t("channels.nextBoundaryNote")}</p>}
    </div>
  );

  const art =
    isEdit && draft.id ? (
      <ArtSection
        channelId={draft.id}
        channelName={form.name ?? ""}
        art={form.art}
        onChange={(c: Channel) => {
          setForm((f) => ({ ...f, art: c.art, logo_path: c.logo_path ?? null }));
          onArtChanged?.();
        }}
      />
    ) : (
      <PendingArtSection pending={pending} onChange={setPending} />
    );

  const schedule =
    mode === "schedule" ? (
      <ScheduleSection
        config={config}
        setConfig={setConfig}
        live={live.rows}
        number={number}
        pools={pools}
        onAddLive={(row) => {
          setLiveRows([...live.rows, row]);
          setLiveFocus(live.rows.length);
        }}
        onOpenLive={(i) => {
          setLiveFocus(i);
          setTab("live");
        }}
      />
    ) : (
      <RuleSection mode={mode} config={config} setConfig={setConfig} />
    );

  const dot = (bad: boolean) => (bad ? " •" : "");
  const items = [
    {
      id: "basics",
      label: t("channels.tab.basics") + dot(Boolean(nameError || numberError)),
      content: basics,
    },
    { id: "art", label: t("channels.tab.art"), content: art },
    {
      id: "schedule",
      label: t("channels.tab.schedule") + dot(Boolean(rule || slotsBad)),
      content: schedule,
    },
    {
      id: "filler",
      label: t("channels.tab.filler"),
      content: <FillerSection config={config} setConfig={setConfig} pools={pools} />,
    },
    {
      id: "live",
      label: t("channels.tab.live") + dot(liveBad),
      content: live.loaded ? (
        <LiveSection
          rows={live.rows}
          number={number}
          focus={liveFocus}
          inherited={live.inherited}
          onChange={setLiveRows}
        />
      ) : (
        <p className="mkt-muted">{t("channels.live.unavailable")}</p>
      ),
    },
    ...(isEdit && draft.id
      ? [
          {
            id: "preview",
            label: t("channels.tab.preview"),
            content: <PreviewSection channelId={draft.id} />,
          },
        ]
      : []),
  ];

  return (
    <Modal
      open
      onClose={onClose}
      className="mkt-cb-modal"
      title={isEdit ? t("channels.editNamed", { name: draft.name ?? "" }) : t("channels.new")}
    >
      {draft.draft && (
        <div className="mkt-cb-banner" role="status">
          {t("channels.tvDraft", { n: draft.draft.number })}
        </div>
      )}
      {origin === "plan" && (
        <div
          className={`mkt-cb-banner${stopsFollowing ? " mkt-cb-banner--warn" : ""}`}
          role="status"
        >
          {stopsFollowing ? t("channels.plan.willStop") : t("channels.plan.follows")}
        </div>
      )}
      {origin === "edited" && (
        <div className="mkt-cb-banner mkt-cb-banner--warn" role="status">
          <span>{t("channels.plan.edited")}</span>
          <Button size="sm" variant="secondary" onClick={backToPlan}>
            {t("channels.plan.back")}
          </Button>
        </div>
      )}
      <Tabs label={t("channels.tabs")} items={items} value={tab} onChange={setTab} />
      {(rule || slotsBad) && tab !== "schedule" && (
        <p className="mkt-form__warn">{t(rule ?? "channels.slot.err.fix")}</p>
      )}
      <div className="mkt-form__actions">
        <Button variant="ghost" onClick={onClose}>
          {t("common.cancel")}
        </Button>
        <Button
          onClick={save}
          disabled={saving || blocked || (isEdit && !channelDirty && !live.dirty)}
        >
          {stopsFollowing ? t("channels.plan.saveAnyway") : t("common.save")}
        </Button>
      </div>
    </Modal>
  );
}
