// Story 27.8 — Channel management admin UI.
//
// The visual counterpart to the channel CRUD API (27.1), the scheduler
// (27.2) and filler management (27.10). An editor can create a channel,
// configure its programming rule (the builder adapts to the selected
// mode), preview the next 48h of what will actually play, reorder
// channels, enable/disable them, and manage the filler/bumper pools used
// for padding.
//
// ACL: only library editors reach this surface; the server re-checks the
// principal on every mutation, so the AdminGate here is purely a UX guard
// (consistent with the other admin pages).
import { useCallback, useEffect, useMemo, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { Modal } from "@ds/components/Modal/Modal";
import { Input } from "@ds/components/Input/Input";
import { Select } from "@ds/components/Select/Select";
import { Toggle } from "@ds/components/Choice/Toggle";
import { Badge } from "@ds/components/Badge/Badge";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { useToast } from "@ds/components/Toast/Toast";
import { ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import { AdminGate } from "../../components/AdminGate";
import {
  channelsApi,
  fillerApi,
  isFiller,
  type Channel,
  type ChannelMode,
  type FillerKind,
  type FillerPool,
  type GuideResponse,
} from "../../lib/channels";

const MODES: ChannelMode[] = ["shuffle", "marathon", "schedule", "smart_mix"];
const FILLER_KINDS: FillerKind[] = ["bumper", "filler", "station_id"];

// blank returns the default draft channel — saved disabled until it has a
// valid rule (AC9, the "forgiving" draft path).
function blankChannel(nextNumber: number): Partial<Channel> {
  return {
    name: "",
    number: nextNumber,
    category: "",
    mode: "shuffle",
    transition: "cut",
    enabled: false,
    mode_config: {},
  };
}

export function Channels() {
  return (
    <AdminGate>
      <ChannelsInner />
    </AdminGate>
  );
}

function ChannelsInner() {
  const { t, formatDate } = useI18n();
  const toast = useToast();
  const [channels, setChannels] = useState<Channel[] | null>(null);
  const [editing, setEditing] = useState<Partial<Channel> | null>(null);
  const [preview, setPreview] = useState<{ channel: Channel; data: GuideResponse | null } | null>(
    null
  );
  const [fillerFor, setFillerFor] = useState<Channel | null>(null);

  const load = useCallback(() => {
    channelsApi
      .list()
      .then((r) => setChannels([...r.items].sort((a, b) => a.number - b.number)))
      .catch(() => setChannels([]));
  }, []);

  useEffect(load, [load]);

  const nextNumber = useMemo(
    () => (channels && channels.length ? Math.max(...channels.map((c) => c.number)) + 1 : 1),
    [channels]
  );

  function showError(e: unknown, fallback: string) {
    toast.show({ tone: "error", message: e instanceof ApiError ? e.problem.title : fallback });
  }

  async function toggleEnabled(c: Channel) {
    try {
      await channelsApi.update(c.id, { enabled: !c.enabled });
      load();
    } catch (e) {
      showError(e, t("common.error"));
    }
  }

  async function remove(c: Channel) {
    if (!window.confirm(t("channels.confirmDelete", { name: c.name }))) return;
    try {
      await channelsApi.remove(c.id);
      load();
    } catch (e) {
      showError(e, t("common.error"));
    }
  }

  // Reorder by swapping numbers with the neighbour, then committing the
  // pair through the bulk renumber endpoint (all-or-nothing, AC4).
  async function move(idx: number, dir: number) {
    if (!channels) return;
    const j = idx + dir;
    if (j < 0 || j >= channels.length) return;
    const a = channels[idx];
    const b = channels[j];
    try {
      await channelsApi.reorder([
        { id: a.id, number: b.number },
        { id: b.id, number: a.number },
      ]);
      load();
    } catch (e) {
      showError(e, t("channels.reorderError"));
    }
  }

  async function openPreview(c: Channel) {
    setPreview({ channel: c, data: null });
    try {
      const data = await channelsApi.schedulePreview(c.id, 48);
      setPreview({ channel: c, data });
    } catch {
      setPreview({ channel: c, data: { server_time: "", channels: [] } });
    }
  }

  if (!channels) return <p className="mkt-loading">{t("common.loading")}</p>;

  return (
    <section className="mkt-page mkt-channels">
      <header className="mkt-page__header">
        <h1>{t("channels.title")}</h1>
        <Button onClick={() => setEditing(blankChannel(nextNumber))}>
          {t("channels.new")}
        </Button>
      </header>

      {channels.length === 0 ? (
        <EmptyState title={t("channels.empty.title")} description={t("channels.empty.desc")} />
      ) : (
        <table className="mkt-table" aria-label={t("channels.title")}>
          <thead>
            <tr>
              <th>{t("channels.col.number")}</th>
              <th>{t("channels.col.name")}</th>
              <th>{t("channels.col.category")}</th>
              <th>{t("channels.col.mode")}</th>
              <th>{t("channels.col.enabled")}</th>
              <th>{t("channels.col.actions")}</th>
            </tr>
          </thead>
          <tbody>
            {channels.map((c, idx) => (
              <tr key={c.id}>
                <td>
                  <div className="mkt-channels__reorder">
                    <button
                      type="button"
                      aria-label={t("channels.moveUp")}
                      disabled={idx === 0}
                      onClick={() => move(idx, -1)}
                    >
                      ▲
                    </button>
                    <span>{c.number}</span>
                    <button
                      type="button"
                      aria-label={t("channels.moveDown")}
                      disabled={idx === channels.length - 1}
                      onClick={() => move(idx, 1)}
                    >
                      ▼
                    </button>
                  </div>
                </td>
                <td>{c.name}</td>
                <td>{c.category || "—"}</td>
                <td>
                  <Badge tone="neutral">{t(`channels.mode.${c.mode}`)}</Badge>
                </td>
                <td>
                  <Toggle
                    checked={c.enabled}
                    onChange={() => toggleEnabled(c)}
                    label={c.enabled ? t("channels.on") : t("channels.off")}
                  />
                </td>
                <td className="mkt-row-actions">
                  <Button size="sm" variant="ghost" onClick={() => setEditing(c)}>
                    {t("common.edit")}
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => openPreview(c)}>
                    {t("channels.preview")}
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => setFillerFor(c)}>
                    {t("channels.filler")}
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => remove(c)}>
                    {t("common.delete")}
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {editing && (
        <ChannelForm
          draft={editing}
          existing={channels}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            load();
          }}
        />
      )}

      {preview && (
        <Modal
          open
          onClose={() => setPreview(null)}
          title={t("channels.previewTitle", { name: preview.channel.name })}
        >
          <p className="mkt-muted">{t("channels.previewNote")}</p>
          {!preview.data ? (
            <p className="mkt-loading">{t("common.loading")}</p>
          ) : (
            <SchedulePreview data={preview.data} formatDate={formatDate} t={t} />
          )}
        </Modal>
      )}

      {fillerFor && (
        <FillerManager channel={fillerFor} onClose={() => setFillerFor(null)} />
      )}
    </section>
  );
}

// ─── Channel form ─────────────────────────────────────────────────────
function ChannelForm({
  draft,
  existing,
  onClose,
  onSaved,
}: {
  draft: Partial<Channel>;
  existing: Channel[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = useI18n();
  const toast = useToast();
  const [form, setForm] = useState<Partial<Channel>>(draft);
  const [saving, setSaving] = useState(false);
  const isEdit = Boolean(draft.id);

  function set<K extends keyof Channel>(key: K, value: Channel[K]) {
    setForm((f) => ({ ...f, [key]: value }));
  }
  function setConfig(key: string, value: unknown) {
    setForm((f) => ({ ...f, mode_config: { ...(f.mode_config ?? {}), [key]: value } }));
  }

  // Inline validation mirrors the API (AC1): name required, number unique.
  const numberCollision = existing.some(
    (c) => c.number === form.number && c.id !== draft.id
  );
  const nameError = !form.name?.trim() ? t("channels.err.nameRequired") : undefined;
  const numberError = numberCollision ? t("channels.err.numberTaken") : undefined;
  const enablingDraft = form.enabled && !form.name?.trim();

  async function save() {
    if (nameError || numberError) return;
    setSaving(true);
    try {
      if (isEdit && draft.id) {
        await channelsApi.update(draft.id, form);
      } else {
        await channelsApi.create(form);
      }
      onSaved();
    } catch (e) {
      // EC5: optimistic-concurrency 409 → ask the editor to reload.
      if (e instanceof ApiError && e.status === 409) {
        toast.show({ tone: "error", message: t("channels.err.stale") });
      } else {
        toast.show({
          tone: "error",
          message: e instanceof ApiError ? e.problem.title : t("common.error"),
        });
      }
    } finally {
      setSaving(false);
    }
  }

  return (
    <Modal open onClose={onClose} title={isEdit ? t("channels.edit") : t("channels.new")}>
      <div className="mkt-form">
        <Input
          label={t("channels.field.name")}
          value={form.name ?? ""}
          error={nameError}
          onChange={(e) => set("name", e.target.value)}
        />
        <Input
          label={t("channels.field.number")}
          type="number"
          value={String(form.number ?? "")}
          error={numberError}
          onChange={(e) => set("number", Number(e.target.value))}
        />
        <Input
          label={t("channels.field.category")}
          value={form.category ?? ""}
          onChange={(e) => set("category", e.target.value)}
        />
        <Input
          label={t("channels.field.logo")}
          value={form.logo_path ?? ""}
          description={t("channels.field.logoHint")}
          onChange={(e) => set("logo_path", e.target.value)}
        />
        <Select
          label={t("channels.field.mode")}
          value={form.mode}
          options={MODES.map((m) => ({ value: m, label: t(`channels.mode.${m}`) }))}
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

        <RuleBuilder mode={form.mode ?? "shuffle"} config={form.mode_config ?? {}} setConfig={setConfig} />

        <Toggle
          checked={form.enabled ?? false}
          onChange={(e) => set("enabled", e.target.checked)}
          label={t("channels.field.enabled")}
        />
        {enablingDraft && <p className="mkt-form__warn">{t("channels.err.enableNeedsRule")}</p>}
        {isEdit && <p className="mkt-muted">{t("channels.nextBoundaryNote")}</p>}

        <div className="mkt-form__actions">
          <Button variant="ghost" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button
            onClick={save}
            disabled={saving || Boolean(nameError) || Boolean(numberError) || enablingDraft}
          >
            {t("common.save")}
          </Button>
        </div>
      </div>
    </Modal>
  );
}

// RuleBuilder adapts to the channel mode (AC2). Each mode writes into the
// opaque `mode_config` the scheduler consumes.
function RuleBuilder({
  mode,
  config,
  setConfig,
}: {
  mode: ChannelMode;
  config: Record<string, unknown>;
  setConfig: (key: string, value: unknown) => void;
}) {
  const { t } = useI18n();
  const str = (k: string) => (config[k] as string) ?? "";

  if (mode === "shuffle") {
    return (
      <fieldset className="mkt-rule">
        <legend>{t("channels.rule.shuffle")}</legend>
        <Input
          label={t("channels.rule.filter")}
          description={t("channels.rule.filterHint")}
          value={str("filter")}
          onChange={(e) => setConfig("filter", e.target.value)}
        />
      </fieldset>
    );
  }
  if (mode === "marathon") {
    return (
      <fieldset className="mkt-rule">
        <legend>{t("channels.rule.marathon")}</legend>
        <Input
          label={t("channels.rule.series")}
          value={str("series_id")}
          onChange={(e) => setConfig("series_id", e.target.value)}
        />
        <Select
          label={t("channels.rule.order")}
          value={str("order") || "aired"}
          options={[
            { value: "aired", label: t("channels.rule.order.aired") },
            { value: "dvd", label: t("channels.rule.order.dvd") },
            { value: "filename", label: t("channels.rule.order.filename") },
          ]}
          onChange={(e) => setConfig("order", e.target.value)}
        />
        <Toggle
          checked={Boolean(config.loop)}
          onChange={(e) => setConfig("loop", e.target.checked)}
          label={t("channels.rule.loop")}
        />
      </fieldset>
    );
  }
  if (mode === "schedule") {
    return (
      <fieldset className="mkt-rule">
        <legend>{t("channels.rule.schedule")}</legend>
        <Input
          label={t("channels.rule.slots")}
          description={t("channels.rule.slotsHint")}
          value={str("slots")}
          onChange={(e) => setConfig("slots", e.target.value)}
        />
      </fieldset>
    );
  }
  // smart_mix — note that it degrades to weighted shuffle when Epic 26
  // classification is unavailable (AC2 / README "smart-mix degrades").
  return (
    <fieldset className="mkt-rule">
      <legend>{t("channels.rule.smartMix")}</legend>
      <Input
        label={t("channels.rule.daypart")}
        value={str("daypart_profile")}
        onChange={(e) => setConfig("daypart_profile", e.target.value)}
      />
      <p className="mkt-muted">{t("channels.rule.smartMixFallback")}</p>
    </fieldset>
  );
}

// SchedulePreview renders the dry-run 48h timeline; filler is collapsed so
// the preview reads as program boundaries + padding (AC3).
function SchedulePreview({
  data,
  formatDate,
  t,
}: {
  data: GuideResponse;
  formatDate: (v: string) => string;
  t: (k: string, v?: Record<string, string | number>) => string;
}) {
  const programs = data.channels[0]?.programs ?? [];
  if (programs.length === 0) {
    return <p className="mkt-muted">{t("channels.previewEmpty")}</p>;
  }
  return (
    <ol className="mkt-preview" aria-label={t("channels.previewTitle", { name: "" })}>
      {programs.map((p) => (
        <li
          key={`${p.seq}-${p.start_at}`}
          className={isFiller(p.kind) ? "mkt-preview__filler" : ""}
        >
          <span className="mkt-preview__time">{formatDate(p.start_at)}</span>
          <span className="mkt-preview__title">
            {isFiller(p.kind) ? t("channels.previewFiller") : p.title}
          </span>
        </li>
      ))}
    </ol>
  );
}

// ─── Filler manager (27.10 admin hooks) ───────────────────────────────
function FillerManager({ channel, onClose }: { channel: Channel; onClose: () => void }) {
  const { t } = useI18n();
  const toast = useToast();
  const [pools, setPools] = useState<FillerPool[] | null>(null);
  const [poolName, setPoolName] = useState("");
  const [videoId, setVideoId] = useState<Record<string, string>>({});
  const [kind, setKind] = useState<Record<string, FillerKind>>({});

  const load = useCallback(() => {
    fillerApi
      .pools(channel.id)
      .then((r) => setPools(r.items))
      .catch(() => setPools([]));
  }, [channel.id]);

  useEffect(load, [load]);

  function fail(e: unknown) {
    toast.show({ tone: "error", message: e instanceof ApiError ? e.problem.title : t("common.error") });
  }

  async function createPool() {
    if (!poolName.trim()) return;
    try {
      await fillerApi.createPool({ name: poolName.trim(), channel_id: channel.id });
      setPoolName("");
      load();
    } catch (e) {
      fail(e);
    }
  }

  async function addItem(poolId: string) {
    const vid = (videoId[poolId] ?? "").trim();
    if (!vid) return;
    try {
      await fillerApi.addItems(poolId, [{ video_id: vid, type: kind[poolId] ?? "bumper" }]);
      setVideoId((m) => ({ ...m, [poolId]: "" }));
      load();
    } catch (e) {
      fail(e);
    }
  }

  return (
    <Modal open onClose={onClose} title={t("filler.title", { name: channel.name })}>
      <div className="mkt-filler">
        <div className="mkt-filler__create">
          <Input
            label={t("filler.poolName")}
            value={poolName}
            onChange={(e) => setPoolName(e.target.value)}
          />
          <Button onClick={createPool} disabled={!poolName.trim()}>
            {t("filler.addPool")}
          </Button>
        </div>

        {!pools ? (
          <p className="mkt-loading">{t("common.loading")}</p>
        ) : pools.length === 0 ? (
          <EmptyState title={t("filler.empty.title")} description={t("filler.empty.desc")} />
        ) : (
          pools.map((pool) => (
            <Card key={pool.id} className="mkt-filler__pool">
              <header>
                <strong>{pool.name}</strong>
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => fillerApi.deletePool(pool.id).then(load).catch(fail)}
                >
                  {t("common.delete")}
                </Button>
              </header>
              <ul className="mkt-filler__items">
                {(pool.items ?? []).map((it) => (
                  <li key={it.id}>
                    <Badge tone="neutral">{t(`filler.kind.${it.type}`)}</Badge>
                    <span>{it.title || it.video_id}</span>
                    <button
                      type="button"
                      aria-label={t("common.delete")}
                      onClick={() => fillerApi.deleteItem(it.id).then(load).catch(fail)}
                    >
                      ✕
                    </button>
                  </li>
                ))}
                {(pool.items ?? []).length === 0 && (
                  <li className="mkt-muted">{t("filler.noItems")}</li>
                )}
              </ul>
              <div className="mkt-filler__additem">
                <Input
                  label={t("filler.videoId")}
                  value={videoId[pool.id] ?? ""}
                  onChange={(e) => setVideoId((m) => ({ ...m, [pool.id]: e.target.value }))}
                />
                <Select
                  label={t("filler.kindLabel")}
                  value={kind[pool.id] ?? "bumper"}
                  options={FILLER_KINDS.map((k) => ({ value: k, label: t(`filler.kind.${k}`) }))}
                  onChange={(e) =>
                    setKind((m) => ({ ...m, [pool.id]: e.target.value as FillerKind }))
                  }
                />
                <Button size="sm" onClick={() => addItem(pool.id)}>
                  {t("filler.addItem")}
                </Button>
              </div>
            </Card>
          ))
        )}
      </div>
    </Modal>
  );
}
