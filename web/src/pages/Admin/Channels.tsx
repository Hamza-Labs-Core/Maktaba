// Story 27.8 — Channel management admin UI.
//
// The channel list (search, filters, drag-to-reorder, on/off, what is on
// now and how far the guide reaches) and the channel editor (Basics, Art,
// the Schedule grid, Filler, Live broadcasts, Preview). Most channels are
// written by the lineup plan (pipeline plan_channels → write_channels); a
// channel a person edits stops following the plan until it is handed back.
// The pieces live in ./channels/.
//
// ACL: only admins reach this surface; the server re-checks the principal on
// every mutation, so the AdminGate here is purely a UX guard.
import { useCallback, useEffect, useMemo, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { Input } from "@ds/components/Input/Input";
import { Select } from "@ds/components/Select/Select";
import { useToast } from "@ds/components/Toast/Toast";
import { Link } from "react-router-dom";
import { ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import { AdminGate } from "../../components/AdminGate";
import { CopyField } from "../../components/CopyField";
import {
  channelOrigin,
  channelsApi,
  tvApi,
  type Channel,
  type ChannelOrigin,
} from "../../lib/channels";
import { ChannelEditor } from "./channels/ChannelEditor";
import { ChannelList, renumber } from "./channels/ChannelList";
import { TitleRow } from "../../help/HelpLink";
import "./channels/channels.css";

// A new channel starts as a schedule channel, off until it has a slot.
function blankChannel(nextNumber: number): Partial<Channel> {
  return {
    name: "",
    number: nextNumber,
    category: "",
    mode: "schedule",
    transition: "cut",
    enabled: false,
    mode_config: { slots: [] },
  };
}

export function Channels() {
  return (
    <AdminGate>
      <ChannelsInner />
    </AdminGate>
  );
}

type OriginFilter = "all" | ChannelOrigin;

function ChannelsInner() {
  const { t } = useI18n();
  const toast = useToast();
  const [channels, setChannels] = useState<Channel[] | null>(null);
  const [editing, setEditing] = useState<{ draft: Partial<Channel>; tab?: string } | null>(null);
  const [q, setQ] = useState("");
  const [category, setCategory] = useState("");
  const [origin, setOrigin] = useState<OriginFilter>("all");
  const [nowMs, setNowMs] = useState(() => Date.now());
  // A failed first load is an error, not an empty lineup (an admin told
  // "No channels yet" would create duplicates).
  const [loadError, setLoadError] = useState<string | null>(null);

  const load = useCallback(() => {
    channelsApi
      .list()
      .then((r) => {
        setLoadError(null);
        setChannels([...(r.items ?? [])].sort((a, b) => a.number - b.number));
      })
      .catch((e: unknown) => setLoadError(e instanceof ApiError ? e.problem.title : ""));
  }, []);
  useEffect(load, [load]);
  useEffect(() => {
    const id = setInterval(() => setNowMs(Date.now()), 30_000);
    return () => clearInterval(id);
  }, []);

  const nextNumber = useMemo(
    () => (channels && channels.length ? Math.max(...channels.map((c) => c.number)) + 1 : 1),
    [channels]
  );
  const categories = useMemo(
    () =>
      [
        ...new Set((channels ?? []).map((c) => c.category).filter((c): c is string => Boolean(c))),
      ].sort(),
    [channels]
  );
  const filtering = Boolean(q.trim() || category || origin !== "all");
  const shown = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return (channels ?? []).filter(
      (c) =>
        (!needle ||
          (c.name ?? "").toLowerCase().includes(needle) ||
          String(c.number).includes(needle) ||
          (c.now_playing?.title ?? "").toLowerCase().includes(needle)) &&
        (!category || c.category === category) &&
        (origin === "all" || channelOrigin(c) === origin)
    );
  }, [channels, q, category, origin]);

  function showError(e: unknown, fallback: string) {
    toast.show({ tone: "error", message: e instanceof ApiError ? e.problem.title : fallback });
  }

  // A plan channel that is changed here stops following the plan: say so first.
  function confirmLeavesPlan(cs: Channel[]): boolean {
    const planned = cs.filter((c) => channelOrigin(c) === "plan");
    if (planned.length === 0) return true;
    return window.confirm(
      t("channels.plan.confirmLeave", { names: planned.map((c) => c.name).join(", ") })
    );
  }

  async function toggleEnabled(c: Channel) {
    if (!confirmLeavesPlan([c])) return;
    setChannels((cs) => cs && cs.map((x) => (x.id === c.id ? { ...x, enabled: !c.enabled } : x)));
    try {
      await channelsApi.update(c.id, { enabled: !c.enabled });
    } catch (e) {
      showError(e, t("common.error"));
    }
    load();
  }

  async function reorder(next: Channel[]) {
    if (!channels) return;
    const changes = renumber(channels, next);
    if (changes.length === 0) return;
    const moved = channels.filter((c) => changes.some((x) => x.id === c.id));
    if (!confirmLeavesPlan(moved)) return;
    const numberOf = new Map(changes.map((x) => [x.id, x.number]));
    setChannels(next.map((c) => ({ ...c, number: numberOf.get(c.id) ?? c.number })));
    try {
      await channelsApi.reorder(changes);
    } catch (e) {
      showError(e, t("channels.reorderError"));
    }
    load();
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

  if (!channels && loadError !== null) {
    return (
      <section className="mkt-page mkt-channels">
        <ErrorState
          kind="server"
          title={loadError || t("common.error")}
          action={
            <Button
              onClick={() => {
                setLoadError(null);
                load();
              }}
            >
              {t("common.retry")}
            </Button>
          }
        />
      </section>
    );
  }
  if (!channels) return <p className="mkt-loading">{t("common.loading")}</p>;

  return (
    <section className="mkt-page mkt-channels">
      <header className="mkt-page__header">
        <TitleRow>
          <h1>{t("channels.title")}</h1>
        </TitleRow>
        <div className="mkt-cb-toolbar">
          <Link className="mkt-cb-link" to="/admin/filler">
            {t("channels.fillerPools")}
          </Link>
          <Button onClick={() => setEditing({ draft: blankChannel(nextNumber) })}>
            {t("channels.new")}
          </Button>
        </div>
      </header>
      <GuideUrl />

      {channels.length === 0 ? (
        <EmptyState title={t("channels.empty.title")} description={t("channels.empty.desc")} />
      ) : (
        <>
          <div className="mkt-cb-filters" role="search">
            <Input
              label={t("channels.list.search")}
              type="search"
              value={q}
              onChange={(e) => setQ(e.target.value)}
            />
            <Select
              label={t("channels.col.category")}
              value={category}
              options={[
                { value: "", label: t("channels.list.allCategories") },
                ...categories.map((c) => ({ value: c, label: c })),
              ]}
              onChange={(e) => setCategory(e.target.value)}
            />
            <Select
              label={t("channels.list.origin")}
              value={origin}
              options={(["all", "plan", "edited", "manual"] as const).map((o) => ({
                value: o,
                label: o === "all" ? t("channels.list.allOrigins") : t(`channels.origin.${o}`),
              }))}
              onChange={(e) => setOrigin(e.target.value as OriginFilter)}
            />
          </div>
          <p className="mkt-muted">
            {filtering
              ? t("channels.list.filtered", { n: shown.length, total: channels.length })
              : t("channels.list.reorderHint")}
          </p>
          {shown.length === 0 ? (
            <p className="mkt-muted">{t("channels.list.noMatch")}</p>
          ) : (
            <ChannelList
              channels={shown}
              nowMs={nowMs}
              canReorder={!filtering}
              onReorder={reorder}
              onToggle={toggleEnabled}
              onEdit={(c, tab) => setEditing({ draft: c, tab })}
              onDelete={remove}
            />
          )}
        </>
      )}

      {editing && (
        <ChannelEditor
          key={editing.draft.id ?? "new"}
          draft={editing.draft}
          initialTab={editing.tab}
          existing={channels}
          onClose={() => setEditing(null)}
          onArtChanged={load}
          onSaved={() => {
            setEditing(null);
            load();
          }}
        />
      )}
    </section>
  );
}

/** The XMLTV guide address (what Plex and other players read), copyable. */
function GuideUrl() {
  const { t } = useI18n();
  const [url, setUrl] = useState<string | null>(null);
  useEffect(() => {
    let alive = true;
    tvApi
      .info()
      .then((i) => {
        if (alive && typeof i?.xmltv_url === "string" && i.xmltv_url) setUrl(i.xmltv_url);
      })
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, []);
  if (!url) return null;
  return (
    <CopyField
      className="mkt-copy--compact mkt-channels__guide"
      label={t("channels.guideUrl")}
      value={url}
    />
  );
}
