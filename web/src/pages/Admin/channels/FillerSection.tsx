// The channel's filler: the weighted lists its gaps are filled from
// (`mode_config.filler.lists`: a pool — by the plan's key or its id — with a
// weight and a cooldown), how soon one clip may repeat, the longest gap a
// slot fills with filler before it takes the next episode, and whether the
// spare time is shared out between episodes.
import { useMemo, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { Toggle } from "@ds/components/Choice/Toggle";
import { useI18n } from "../../../lib/i18n";
import type { FillerPool } from "../../../lib/channels";
import { useDebounced, useDuration } from "./pickers";

export interface FillerList {
  pool: string;
  weight: number;
  cooldown_ms: number;
}

export interface FillerSpec {
  lists: FillerList[];
  repeat_cooldown_ms?: number;
  [key: string]: unknown;
}

export const WEIGHT_MAX = 1000;

export function fillerSpec(config: Record<string, unknown>): FillerSpec {
  const raw = config.filler;
  if (!raw || typeof raw !== "object") return { lists: [] };
  const spec = raw as FillerSpec;
  return {
    ...spec,
    lists: (Array.isArray(spec.lists) ? spec.lists : [])
      .filter((x) => x && typeof x.pool === "string")
      .map((x) => ({
        pool: x.pool,
        weight: Number(x.weight ?? 1),
        cooldown_ms: Number(x.cooldown_ms ?? 0),
      })),
  };
}

/** Each list's share of the picks, in percent (weights are relative). */
export function shares(lists: FillerList[]): number[] {
  const total = lists.reduce((a, l) => a + Math.max(0, l.weight), 0);
  return lists.map((l) => (total > 0 ? Math.round((Math.max(0, l.weight) / total) * 100) : 0));
}

export function poolFor(pools: FillerPool[], ref: string): FillerPool | undefined {
  return pools.find((p) => p.id === ref || (p.external_key && p.external_key === ref));
}

export function FillerSection({
  config,
  setConfig,
  pools,
}: {
  config: Record<string, unknown>;
  setConfig: (key: string, value: unknown) => void;
  pools: FillerPool[];
}) {
  const { t, n } = useI18n();
  const dur = useDuration();
  const spec = fillerSpec(config);
  const [q, setQ] = useState("");
  const query = useDebounced(q, 150).trim().toLowerCase();
  const pct = shares(spec.lists);

  const offered = useMemo(
    () =>
      pools.filter(
        (p) =>
          !spec.lists.some((l) => poolFor([p], l.pool)) &&
          (!query ||
            p.name.toLowerCase().includes(query) ||
            (p.external_key ?? "").toLowerCase().includes(query))
      ),
    [pools, spec.lists, query]
  );

  function write(lists: FillerList[], extra: Partial<FillerSpec> = {}) {
    setConfig("filler", { ...spec, ...extra, lists });
  }
  function update(i: number, patch: Partial<FillerList>) {
    write(spec.lists.map((l, j) => (j === i ? { ...l, ...patch } : l)));
  }

  const repeatMin = Math.round((spec.repeat_cooldown_ms ?? 1_800_000) / 60_000);
  const maxGap = Number(config.slot_max_filler_minutes ?? 20);
  const maxGapError =
    !Number.isFinite(maxGap) || maxGap < 0 || maxGap > 240
      ? t("channels.filler.err.gap")
      : undefined;

  return (
    <div className="mkt-cb-filler">
      <p className="mkt-muted">{t("channels.filler.help")}</p>
      {spec.lists.length === 0 ? (
        <p className="mkt-muted">{t("channels.filler.noLists")}</p>
      ) : (
        <ul className="mkt-cb-filler__lists">
          {spec.lists.map((l, i) => {
            const pool = poolFor(pools, l.pool);
            const items = pool?.items ?? [];
            const ms = items.reduce((a, it) => a + (it.duration_ms ?? 0), 0);
            return (
              <li
                key={`${l.pool}-${i}`}
                className="mkt-cb-filler__list"
                data-testid={`filler-list-${i}`}
              >
                <div className="mkt-cb-filler__name">
                  <strong>{pool?.name ?? l.pool}</strong>
                  <span className="mkt-muted">
                    {pool
                      ? t("channels.filler.poolMeta", { n: n(items.length), dur: dur(ms) })
                      : t("channels.filler.missingPool")}
                  </span>
                </div>
                <label className="mkt-cb-slider">
                  <span>{t("channels.filler.weight")}</span>
                  <input
                    type="range"
                    min={0}
                    max={WEIGHT_MAX}
                    step={5}
                    value={Math.min(WEIGHT_MAX, l.weight)}
                    aria-valuetext={t("channels.filler.share", { w: n(l.weight), pct: n(pct[i]) })}
                    onChange={(e) => update(i, { weight: Number(e.target.value) })}
                  />
                  <input
                    type="number"
                    className="mkt-cb-num"
                    min={0}
                    max={WEIGHT_MAX}
                    aria-label={t("channels.filler.weightFor", { name: pool?.name ?? l.pool })}
                    value={l.weight}
                    onChange={(e) =>
                      update(i, {
                        weight: Math.max(0, Math.min(WEIGHT_MAX, Number(e.target.value) || 0)),
                      })
                    }
                  />
                  <span className="mkt-cb-share">
                    {t("channels.filler.pct", { pct: n(pct[i]) })}
                  </span>
                </label>
                <Input
                  label={t("channels.filler.cooldown")}
                  type="number"
                  min={0}
                  value={String(Math.round(l.cooldown_ms / 60_000))}
                  description={t("channels.filler.cooldownHint")}
                  onChange={(e) =>
                    update(i, { cooldown_ms: Math.max(0, Number(e.target.value) || 0) * 60_000 })
                  }
                />
                <Button
                  size="sm"
                  variant="ghost"
                  aria-label={t("channels.filler.removeList", { name: pool?.name ?? l.pool })}
                  onClick={() => write(spec.lists.filter((_, j) => j !== i))}
                >
                  {t("common.delete")}
                </Button>
              </li>
            );
          })}
        </ul>
      )}

      <fieldset className="mkt-cb-filler__add">
        <legend>{t("channels.filler.addList")}</legend>
        <Input
          label={t("channels.filler.searchPools")}
          type="search"
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
        {offered.length === 0 ? (
          <p className="mkt-muted">{t("channels.filler.noPools")}</p>
        ) : (
          <ul className="mkt-cb-hits">
            {offered.slice(0, 20).map((p) => {
              const ms = (p.items ?? []).reduce((a, it) => a + (it.duration_ms ?? 0), 0);
              return (
                <li key={p.id}>
                  <button
                    type="button"
                    className="mkt-cb-hit"
                    onClick={() =>
                      write([
                        ...spec.lists,
                        { pool: p.external_key || p.id, weight: 100, cooldown_ms: 0 },
                      ])
                    }
                  >
                    <span className="mkt-cb-hit__name">{p.name}</span>
                    <span className="mkt-cb-hit__meta">
                      {t("channels.filler.poolMeta", {
                        n: n((p.items ?? []).length),
                        dur: dur(ms),
                      })}
                    </span>
                  </button>
                </li>
              );
            })}
          </ul>
        )}
      </fieldset>

      <div className="mkt-cb-row">
        <Input
          label={t("channels.filler.repeat")}
          type="number"
          min={0}
          value={String(repeatMin)}
          description={t("channels.filler.repeatHint")}
          onChange={(e) =>
            write(spec.lists, {
              repeat_cooldown_ms: Math.max(0, Number(e.target.value) || 0) * 60_000,
            })
          }
        />
        <Input
          label={t("channels.filler.maxGap")}
          type="number"
          min={0}
          max={240}
          value={String(maxGap)}
          error={maxGapError}
          description={t("channels.filler.maxGapHint")}
          onChange={(e) => setConfig("slot_max_filler_minutes", Number(e.target.value))}
        />
      </div>
      <Toggle
        checked={Boolean(config.filler_between_episodes)}
        onChange={(e) => setConfig("filler_between_episodes", e.target.checked)}
        label={t("channels.filler.between")}
      />
    </div>
  );
}
