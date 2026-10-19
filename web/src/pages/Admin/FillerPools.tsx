// Story 27.10 — filler & bumper pools: every pool with how many clips it
// holds and how long they run; clips are added by searching the library
// (not by typing ids), removed, and previewed. The lineup plan's own pools
// (external_key) are rewritten by the plan writer, which the page says.
// With TV staging on an edit is saved into the editor's draft change (story
// 33.12 AC6): the page says so, and marks the pools and clips it holds.
import { useCallback, useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { Input } from "@ds/components/Input/Input";
import { Select } from "@ds/components/Select/Select";
import { useToast } from "@ds/components/Toast/Toast";
import { ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import { AdminGate } from "../../components/AdminGate";
import {
  fillerApi,
  type FillerDraftRef,
  type FillerKind,
  type FillerPool,
} from "../../lib/channels";
import { SourceSearch, useDuration } from "./channels/pickers";
import "./channels/channels.css";

const FILLER_KINDS: FillerKind[] = ["filler", "bumper", "station_id"];

export function FillerPools() {
  return (
    <AdminGate>
      <FillerPoolsInner />
    </AdminGate>
  );
}

function FillerPoolsInner() {
  const { t, n } = useI18n();
  const toast = useToast();
  const dur = useDuration();
  const [pools, setPools] = useState<FillerPool[] | null>(null);
  const [name, setName] = useState("");
  const [q, setQ] = useState("");
  const [open, setOpen] = useState<string | null>(null);
  const [kind, setKind] = useState<FillerKind>("filler");

  const load = useCallback(() => {
    fillerApi
      .pools()
      .then((r) => setPools(r.items ?? []))
      .catch(() => setPools([]));
  }, []);
  useEffect(load, [load]);

  function fail(e: unknown) {
    toast.show({
      tone: "error",
      message: e instanceof ApiError ? e.problem.title : t("common.error"),
    });
  }

  /** A save that went into a TV draft change says so. */
  function drafted(number: number | undefined | null) {
    if (number == null) return;
    toast.show({ tone: "success", message: t("fillerPools.savedToDraft", { n: number }) });
  }

  function draftOf(r: unknown) {
    return (r as FillerDraftRef | undefined)?.draft_change?.number;
  }

  async function create() {
    if (!name.trim()) return;
    try {
      const p = await fillerApi.createPool({ name: name.trim() });
      drafted(p.draft?.number);
      setName("");
      setOpen(p.id);
      load();
    } catch (e) {
      fail(e);
    }
  }

  async function removePool(p: FillerPool) {
    if (!window.confirm(t("fillerPools.confirmDelete", { name: p.name }))) return;
    try {
      drafted(draftOf(await fillerApi.deletePool(p.id)));
      load();
    } catch (e) {
      fail(e);
    }
  }

  async function addVideo(pool: FillerPool, videoId: string) {
    try {
      drafted(draftOf(await fillerApi.addItems(pool.id, [{ video_id: videoId, type: kind }])));
      load();
    } catch (e) {
      fail(e);
    }
  }

  async function removeItem(id: string) {
    try {
      drafted(draftOf(await fillerApi.deleteItem(id)));
      load();
    } catch (e) {
      fail(e);
    }
  }

  const shown = useMemo(() => {
    const needle = q.trim().toLowerCase();
    return (pools ?? []).filter(
      (p) =>
        !needle ||
        p.name.toLowerCase().includes(needle) ||
        (p.external_key ?? "").toLowerCase().includes(needle)
    );
  }, [pools, q]);

  if (!pools) return <p className="mkt-loading">{t("common.loading")}</p>;

  return (
    <section className="mkt-page mkt-fillerpools">
      <header className="mkt-page__header">
        <h1>{t("fillerPools.title")}</h1>
      </header>
      <p className="mkt-muted">{t("fillerPools.help")}</p>
      <div className="mkt-cb-filters">
        <Input
          label={t("fillerPools.search")}
          type="search"
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
        <Input
          label={t("filler.poolName")}
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
        <Button onClick={create} disabled={!name.trim()}>
          {t("filler.addPool")}
        </Button>
      </div>

      {shown.length === 0 ? (
        <EmptyState title={t("filler.empty.title")} description={t("fillerPools.emptyDesc")} />
      ) : (
        <div className="mkt-fillerpools__list">
          {shown.map((p) => {
            const items = p.items ?? [];
            const ms = items.reduce((a, it) => a + (it.duration_ms ?? 0), 0);
            const isOpen = open === p.id;
            return (
              <Card key={p.id} className="mkt-fillerpools__pool">
                <header className="mkt-fillerpools__head">
                  <button
                    type="button"
                    className="mkt-fillerpools__toggle"
                    aria-expanded={isOpen}
                    onClick={() => setOpen(isOpen ? null : p.id)}
                  >
                    <strong>{p.name}</strong>
                    <span className="mkt-muted">
                      {t("channels.filler.poolMeta", { n: n(items.length), dur: dur(ms) })}
                    </span>
                  </button>
                  {p.external_key && <Badge tone="accent">{t("fillerPools.fromPlan")}</Badge>}
                  {p.channel_id && <Badge tone="neutral">{t("fillerPools.channelOnly")}</Badge>}
                  {p.draft && (
                    <Link
                      to={`/admin/live-tv/changes/${encodeURIComponent(p.draft.change_id)}`}
                      data-testid={`pool-draft-${p.id}`}
                    >
                      <Badge tone="warn">
                        {t("fillerPools.inDraft", { n: p.draft.number })}
                      </Badge>
                    </Link>
                  )}
                  <Button size="sm" variant="ghost" onClick={() => removePool(p)}>
                    {t("common.delete")}
                  </Button>
                </header>
                {isOpen && (
                  <div className="mkt-fillerpools__body">
                    {p.external_key && (
                      <p className="mkt-form__warn">{t("fillerPools.planNote")}</p>
                    )}
                    {items.length === 0 ? (
                      <p className="mkt-muted">{t("filler.noItems")}</p>
                    ) : (
                      <ul className="mkt-filler__items">
                        {items.map((it) => (
                          <li key={it.id}>
                            <Badge tone="neutral">{t(`filler.kind.${it.type}`)}</Badge>
                            {it.pending && (
                              <Badge tone="warn">{t("fillerPools.pendingItem")}</Badge>
                            )}
                            <a href={`/videos/${encodeURIComponent(it.video_id)}/watch?t=0`}>
                              {it.title || it.video_id}
                            </a>
                            <span className="mkt-muted">{dur(it.duration_ms ?? 0)}</span>
                            <button
                              type="button"
                              aria-label={t("fillerPools.removeItem", {
                                name: it.title || it.video_id,
                              })}
                              onClick={() => removeItem(it.id)}
                            >
                              ✕
                            </button>
                          </li>
                        ))}
                      </ul>
                    )}
                    <div className="mkt-fillerpools__add">
                      <Select
                        label={t("filler.kindLabel")}
                        value={kind}
                        options={FILLER_KINDS.map((k) => ({
                          value: k,
                          label: t(`filler.kind.${k}`),
                        }))}
                        onChange={(e) => setKind(e.target.value as FillerKind)}
                      />
                      <SourceSearch
                        kind="video"
                        label={t("fillerPools.addVideo")}
                        onPick={(h) => void addVideo(p, h.id)}
                      />
                    </div>
                  </div>
                )}
              </Card>
            );
          })}
        </div>
      )}
    </section>
  );
}
