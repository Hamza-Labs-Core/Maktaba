// Story 27.10 — filler & bumper pools: every pool with how many clips it
// holds and how long they run; clips are added by searching the library
// (not by typing ids), removed, and previewed. The lineup plan's own pools
// (external_key) are rewritten by the plan writer, which the page says.
import { useCallback, useEffect, useMemo, useState } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { Input } from "@ds/components/Input/Input";
import { Select } from "@ds/components/Select/Select";
import { useToast } from "@ds/components/Toast/Toast";
import { ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import { AdminGate } from "../../components/AdminGate";
import { fillerApi, type FillerKind, type FillerPool } from "../../lib/channels";
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
  // A failed first load is an error, not "No filler pools".
  const [loadError, setLoadError] = useState<string | null>(null);

  const load = useCallback(() => {
    fillerApi
      .pools()
      .then((r) => {
        setLoadError(null);
        setPools(r.items ?? []);
      })
      .catch((e: unknown) => setLoadError(e instanceof ApiError ? e.problem.title : ""));
  }, []);
  useEffect(load, [load]);

  function fail(e: unknown) {
    toast.show({
      tone: "error",
      message: e instanceof ApiError ? e.problem.title : t("common.error"),
    });
  }

  async function create() {
    if (!name.trim()) return;
    try {
      const p = await fillerApi.createPool({ name: name.trim() });
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
      await fillerApi.deletePool(p.id);
      load();
    } catch (e) {
      fail(e);
    }
  }

  async function addVideo(pool: FillerPool, videoId: string) {
    try {
      await fillerApi.addItems(pool.id, [{ video_id: videoId, type: kind }]);
      load();
    } catch (e) {
      fail(e);
    }
  }

  async function removeItem(id: string) {
    try {
      await fillerApi.deleteItem(id);
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
        (p.name ?? "").toLowerCase().includes(needle) ||
        (p.external_key ?? "").toLowerCase().includes(needle)
    );
  }, [pools, q]);

  if (!pools && loadError !== null) {
    return (
      <section className="mkt-page mkt-fillerpools">
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
                            <Badge tone="neutral">
                              {FILLER_KINDS.includes(it.type)
                                ? t(`filler.kind.${it.type}`)
                                : String(it.type ?? "")}
                            </Badge>
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
