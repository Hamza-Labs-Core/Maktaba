// The sections of a library's admin page (/admin/libraries/:id). Each reads
// what it shows and only offers what the API lets an admin do; the API
// refuses every one of these reads and writes to anyone else.
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Select } from "@ds/components/Select/Select";
import { Toggle } from "@ds/components/Choice/Toggle";
import { useToast } from "@ds/components/Toast/Toast";
import { api, ApiError } from "../../../lib/api";
import { useI18n } from "../../../lib/i18n";
import { libraryAdminApi, problemMessage, type ManagedLibrary } from "../../../lib/libraryAdminApi";
import { forgetAnswers, VIDEO_ANSWERS } from "../../../lib/pageCache";
import type { FailureItem, OfflineSummary, StageInfo } from "../../../lib/queueApi";
import { useFmt } from "../../../components/queue/format";
import { OFFLINE_ACTIONS, useBytes, type OfflineAction } from "../../../components/queue/offline";
import { RingGauge } from "../../../components/queue/RingGauge";
import { ActivityChart, StatusBar, useStatusText } from "../../../components/libdash/charts";
import { StageChart } from "../../../components/libdash/StageChart";
import { RING_STATE } from "../../../components/libdash/LibraryTile";
import type { LibraryHealth } from "../../../components/libdash/health";
import type { Activity } from "../../../components/libdash/useLibraryBoard";

export function Section({
  id,
  title,
  desc,
  actions,
  children,
}: {
  id: string;
  title: string;
  desc?: string;
  actions?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section id={id} className="mkt-lp-section" aria-labelledby={`${id}-h`} tabIndex={-1}>
      <div className="mkt-lp-section__head">
        <h2 id={`${id}-h`}>{title}</h2>
        {actions && <div className="mkt-lp-section__actions">{actions}</div>}
      </div>
      {desc && <p className="mkt-muted mkt-lp-section__desc">{desc}</p>}
      {children}
    </section>
  );
}

/** A label and a figure, for the overview's facts. */
function Fact({
  label,
  value,
  tone,
}: {
  label: string;
  value: ReactNode;
  tone?: "error" | "warn";
}) {
  return (
    <div className="mkt-lp-fact" data-tone={tone}>
      <dt>{label}</dt>
      <dd>{value}</dd>
    </div>
  );
}

export interface LibraryStats {
  total_videos: number;
  total_duration_sec: number;
  source_size_bytes: number;
  derived_size_bytes: number;
}

export function OverviewSection({
  lib,
  h,
  stats,
  series,
  scan,
}: {
  lib: ManagedLibrary;
  h: LibraryHealth | null;
  stats: LibraryStats | null;
  series: number | null;
  scan: ReactNode;
}) {
  const { t, n, formatDate } = useI18n();
  const bytes = useBytes();
  const c = lib.counts;
  const unreachable = lib.roots.filter((r) => !r.reachable).length;
  const last = lib.last_scan;
  const hours = stats ? Math.round(stats.total_duration_sec / 3600) : null;
  return (
    <Section id="overview" title={t("libpage.overview.title")} actions={scan}>
      <dl className="mkt-lp-facts">
        <Fact label={t("libpage.overview.videos")} value={n(c.total)} />
        <Fact label={t("libpage.overview.ready")} value={n(c.ready)} />
        <Fact label={t("libpage.overview.series")} value={series === null ? "–" : n(series)} />
        <Fact label={t("libpage.overview.seasons")} value={h ? n(h.row.seasons) : "–"} />
        <Fact label={t("libpage.overview.hours")} value={hours === null ? "–" : n(hours)} />
        <Fact
          label={t("libpage.overview.storage")}
          value={stats ? bytes(stats.source_size_bytes) : "–"}
        />
        <Fact
          label={t("libpage.overview.derived")}
          value={stats ? bytes(stats.derived_size_bytes) : "–"}
        />
        <Fact
          label={t("libpage.overview.failed")}
          value={n(c.failed)}
          tone={c.failed > 0 ? "error" : undefined}
        />
        <Fact
          label={t("libpage.overview.missing")}
          value={n(c.missing)}
          tone={c.missing > 0 ? "warn" : undefined}
        />
      </dl>
      <h3 className="mkt-lp-sub">{t("libpage.overview.health")}</h3>
      <ul className="mkt-lp-health">
        <li>
          {unreachable === 0 ? (
            <Badge tone="success">
              {t("libpage.health.folders.ok", { n: n(lib.roots.length) })}
            </Badge>
          ) : (
            <Badge tone="error">{t("libpage.health.folders.bad", { n: n(unreachable) })}</Badge>
          )}
        </li>
        {h && (
          <li>
            <Badge tone={h.failed > 0 ? "error" : h.state === "done" ? "success" : "accent"}>
              {h.failed > 0
                ? t("libdash.state.failing", { n: n(h.failed) })
                : t(`libdash.state.${h.state}`)}
            </Badge>
          </li>
        )}
        {lib.conflicts.length > 0 && (
          <li>
            <Badge tone="warn">
              {t("libpage.health.conflicts", { n: n(lib.conflicts.length) })}
            </Badge>
          </li>
        )}
        <li className="mkt-muted">
          {last
            ? t("libpage.health.lastScan", {
                when: formatDate(last.finished_at || last.created_at),
                state: t(`libraryAdmin.scanState.${last.state}`),
              })
            : t("libraryAdmin.neverScanned")}
        </li>
      </ul>
      <h3 className="mkt-lp-sub">{t("libraryAdmin.folders")}</h3>
      <ul className="mkt-lp-roots">
        {lib.roots.map((r) => (
          <li key={r.path}>
            <bdi dir="ltr" className="mkt-libadmin__path">
              {r.path}
            </bdi>{" "}
            {r.reachable ? (
              <Badge tone="success">{t("libraryAdmin.folder.reachable")}</Badge>
            ) : (
              <Badge tone="error">
                {t("libraryAdmin.folder.unreachable")}
                {r.problem ? `: ${r.problem}` : ""}
              </Badge>
            )}
          </li>
        ))}
      </ul>
    </Section>
  );
}

export function QueueSection({
  h,
  stages,
  activity,
  failures,
  onOffline,
  onRequeue,
}: {
  h: LibraryHealth | null;
  stages: StageInfo[];
  activity: Activity | undefined;
  failures: { items: FailureItem[]; total: number } | null;
  onOffline: (a: OfflineAction) => void;
  onRequeue: () => void;
}) {
  const { t, n } = useI18n();
  const fmt = useFmt();
  const text = useStatusText();
  const libId = h?.id ?? "";
  return (
    <Section
      id="queues"
      title={t("libpage.queues.title")}
      desc={t("libpage.queues.desc")}
      actions={
        <>
          <Button size="sm" variant="secondary" onClick={onRequeue}>
            {t("queue.libs.requeue")}
          </Button>
          {OFFLINE_ACTIONS.map((a) => (
            <Button key={a} size="sm" variant="secondary" onClick={() => onOffline(a)}>
              {t(`queue.offline.action.${a}`)}
            </Button>
          ))}
        </>
      }
    >
      {h === null ? (
        <p className="mkt-muted">{t("libdash.loading")}</p>
      ) : (
        <>
          <div className="mkt-lp-gauge">
            <RingGauge
              done={h.done}
              total={Math.max(h.total, 1)}
              state={RING_STATE[h.state]}
              size={96}
              centre={`${n(h.pct)}%`}
              label={t("libdash.gauge", { pct: n(h.pct), name: h.name })}
            />
            <div className="mkt-lp-gauge__text">
              <p className={`mkt-ld-state mkt-ld-state--${h.state}`}>
                <span className="mkt-ld-state__dot" aria-hidden="true" />
                {h.state === "failing"
                  ? t("libdash.state.failing", { n: n(h.failed) })
                  : t(`libdash.state.${h.state}`)}
              </p>
              <StatusBar
                c={{
                  done: h.done,
                  total: h.total,
                  running: h.running,
                  waiting: h.waiting,
                  failed: h.failed,
                }}
                label={text({
                  done: h.done,
                  total: h.total,
                  running: h.running,
                  waiting: h.waiting,
                  failed: h.failed,
                })}
              />
              <p className="mkt-ld-tile__counts">
                <span>{t("libdash.n.running", { n: n(h.running) })}</span>
                <span>{t("libdash.n.waiting", { n: n(h.waiting) })}</span>
                <span>{t("libdash.changed", { time: fmt.ago(h.row.last_activity_at) })}</span>
              </p>
            </div>
          </div>

          <h3 className="mkt-lp-sub">
            {t("libpage.queues.activity", { n: n(activity?.days.length ?? 0) })}
          </h3>
          {activity ? (
            <ActivityChart name={h.name} {...activity} />
          ) : (
            <p className="mkt-muted">{t("libdash.loading")}</p>
          )}

          <h3 className="mkt-lp-sub">{t("libpage.queues.failing")}</h3>
          {failures === null ? (
            <p className="mkt-muted">{t("libdash.loading")}</p>
          ) : failures.items.length === 0 ? (
            <p className="mkt-muted">{t("libpage.queues.noFailures")}</p>
          ) : (
            <>
              <ul className="mkt-lp-fails">
                {failures.items.map((f) => (
                  <li key={f.job_id}>
                    <Link
                      to={`/queue/items/${encodeURIComponent(f.subject_kind)}/${encodeURIComponent(f.subject_id)}`}
                    >
                      <bdi>{f.label}</bdi>
                    </Link>
                    <span className="mkt-q-chip" dir="auto">
                      {stages.find((s) => s.stage === f.stage)?.label ?? f.stage}
                    </span>
                    <span className="mkt-lp-fails__msg" dir="auto">
                      {f.message}
                    </span>
                  </li>
                ))}
              </ul>
              <Link to={`/queue/failures?library=${encodeURIComponent(libId)}`}>
                {t("libpage.queues.allFailures", {
                  n: n(Math.max(failures.total, failures.items.length)),
                })}
              </Link>
            </>
          )}

          <h3 className="mkt-lp-sub">{t("libpage.queues.stages")}</h3>
          <StageChart row={h.row} stages={stages} />
          <p>
            <Link to={`/queue/items/library/${encodeURIComponent(libId)}`}>
              {t("libpage.queues.items")}
            </Link>
          </p>
        </>
      )}
    </Section>
  );
}

/** settings.convert: allow_reencode and auto, both off by default (library_mgmt/convert.py). */
export function convertSettings(s: Record<string, unknown> | undefined): {
  allow_reencode: boolean;
  auto: boolean;
} {
  const c = s?.convert;
  const o = c && typeof c === "object" && !Array.isArray(c) ? (c as Record<string, unknown>) : {};
  return { allow_reencode: o.allow_reencode === true, auto: o.auto === true };
}

export function ConvertSection({
  lib,
  summary,
  onSaved,
  onConvert,
}: {
  lib: ManagedLibrary;
  summary: OfflineSummary | null;
  onSaved: () => void;
  onConvert: () => void;
}) {
  const { t, n } = useI18n();
  const toast = useToast();
  const saved = convertSettings(lib.settings);
  const [draft, setDraft] = useState(saved);
  const [busy, setBusy] = useState(false);
  useEffect(() => setDraft(convertSettings(lib.settings)), [lib.settings]);
  const changed = draft.allow_reencode !== saved.allow_reencode || draft.auto !== saved.auto;
  const save = async () => {
    setBusy(true);
    try {
      const prev = lib.settings?.convert;
      const base = prev && typeof prev === "object" && !Array.isArray(prev) ? prev : {};
      await libraryAdminApi.update(lib.id, {
        settings: { ...lib.settings, convert: { ...base, ...draft } },
      });
      toast.show({ tone: "success", message: t("libpage.convert.saved") });
      forgetAnswers("admin:libraries");
      onSaved();
    } catch (e) {
      toast.show({ tone: "error", message: problemMessage(e, t("common.error")) });
    } finally {
      setBusy(false);
    }
  };
  return (
    <Section
      id="convert"
      title={t("libpage.convert.title")}
      desc={t("libpage.convert.desc")}
      actions={
        <Button size="sm" variant="secondary" onClick={onConvert}>
          {t("queue.offline.action.convert")}
        </Button>
      }
    >
      <dl className="mkt-lp-facts">
        <Fact
          label={t("libpage.convert.left")}
          value={summary ? n(summary.not_mp4) : "–"}
          tone={summary && summary.not_mp4 > 0 ? "warn" : undefined}
        />
        <Fact label={t("libpage.convert.running")} value={summary ? n(summary.converting) : "–"} />
      </dl>
      <form
        className="mkt-lp-form"
        aria-label={t("libpage.convert.settings")}
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <Toggle
          checked={draft.allow_reencode}
          onChange={(e) => setDraft((d) => ({ ...d, allow_reencode: e.target.checked }))}
          label={t("libpage.convert.reencode")}
        />
        <p className="mkt-muted mkt-lp-hint">{t("libpage.convert.reencodeHint")}</p>
        <Toggle
          checked={draft.auto}
          onChange={(e) => setDraft((d) => ({ ...d, auto: e.target.checked }))}
          label={t("libpage.convert.auto")}
        />
        <p className="mkt-muted mkt-lp-hint">{t("libpage.convert.autoHint")}</p>
        <div>
          <Button type="submit" size="sm" disabled={!changed} loading={busy}>
            {t("libpage.convert.save")}
          </Button>
        </div>
      </form>
    </Section>
  );
}

export function DuplicatesSection({
  libId,
  summary,
  onScan,
}: {
  libId: string;
  summary: OfflineSummary | null;
  onScan: () => void;
}) {
  const { t, n } = useI18n();
  const d = summary?.duplicates ?? null;
  return (
    <Section
      id="duplicates"
      title={t("libpage.dups.title")}
      desc={t("libpage.dups.desc")}
      actions={
        <Button size="sm" variant="secondary" onClick={onScan}>
          {t("queue.offline.action.dedup")}
        </Button>
      }
    >
      {d === null ? (
        <p className="mkt-muted">{t("libdash.loading")}</p>
      ) : d === 0 ? (
        <p className="mkt-muted">{t("libpage.dups.none")}</p>
      ) : (
        <p>
          <Link to={`/queue/duplicates?library=${encodeURIComponent(libId)}`}>
            {t("libdash.duplicates", { n: n(d) })}
          </Link>
        </p>
      )}
    </Section>
  );
}

export function TrashSection({
  lib,
  summary,
  onEmptyMissing,
}: {
  lib: ManagedLibrary;
  summary: OfflineSummary | null;
  onEmptyMissing: () => void;
}) {
  const { t, n, formatDate } = useI18n();
  const bytes = useBytes();
  const tr = summary?.trash;
  return (
    <Section id="trash" title={t("libpage.trash.title")} desc={t("libpage.trash.desc")}>
      <dl className="mkt-lp-facts">
        <Fact label={t("libpage.trash.files")} value={tr ? n(tr.entries) : "–"} />
        <Fact label={t("libpage.trash.size")} value={tr ? bytes(tr.bytes) : "–"} />
        <Fact
          label={t("libpage.trash.purge")}
          value={tr?.next_purge_at ? formatDate(tr.next_purge_at, { dateStyle: "medium" }) : "–"}
        />
        <Fact
          label={t("libpage.trash.missing")}
          value={n(lib.counts.missing)}
          tone={lib.counts.missing > 0 ? "warn" : undefined}
        />
      </dl>
      <div className="mkt-lp-row">
        <Link to={`/queue/file-actions?library=${encodeURIComponent(lib.id)}`}>
          {t("libpage.trash.open")}
        </Link>
        <Button
          size="sm"
          variant="secondary"
          disabled={lib.counts.missing === 0}
          onClick={onEmptyMissing}
        >
          {t("libraryAdmin.trash.button", { count: n(lib.counts.missing) })}
        </Button>
      </div>
    </Section>
  );
}

export function SettingsSection({
  lib,
  onEdit,
  onMerge,
  onRemove,
  canMerge,
}: {
  lib: ManagedLibrary;
  onEdit: () => void;
  onMerge: () => void;
  onRemove: () => void;
  canMerge: boolean;
}) {
  const { t, n } = useI18n();
  const keys = Object.keys(lib.settings ?? {});
  return (
    <Section id="settings" title={t("libpage.settings.title")} desc={t("libpage.settings.desc")}>
      <dl className="mkt-lp-facts">
        <Fact label={t("libpage.settings.name")} value={<bdi>{lib.name}</bdi>} />
        <Fact label={t("libpage.settings.folders")} value={n(lib.roots.length)} />
        <Fact
          label={t("libpage.settings.advanced")}
          value={keys.length ? <bdi dir="ltr">{keys.join(", ")}</bdi> : t("libpage.settings.none")}
        />
      </dl>
      <div className="mkt-lp-row">
        <Button size="sm" onClick={onEdit}>
          {t("common.edit")}
        </Button>
        <Button size="sm" variant="secondary" onClick={onMerge} disabled={!canMerge}>
          {t("libraryAdmin.merge.button")}
        </Button>
        <Button size="sm" variant="destructive" onClick={onRemove}>
          {t("libraryAdmin.remove.button")}
        </Button>
      </div>
    </Section>
  );
}

interface Matrix {
  users: { id: string; username: string }[];
  libraries: { id: string; name: string }[];
  grants: { user_id: string; library_id: string; role: string }[];
}
const ROLES = ["none", "read", "write", "admin"] as const;

/** Who may open this library: the library ACL's column for it (GET/PUT /api/admin/library-acl). */
export function AccessSection({ lib }: { lib: ManagedLibrary }) {
  const { t, n } = useI18n();
  const toast = useToast();
  const [m, setM] = useState<Matrix | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [edits, setEdits] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const load = () =>
    api
      .get<Partial<Matrix> | null>("/api/admin/library-acl")
      .then((r) => {
        setM({ users: r?.users ?? [], libraries: r?.libraries ?? [], grants: r?.grants ?? [] });
        setErr(null);
      })
      .catch((e) =>
        setErr(e instanceof ApiError ? e.problem.detail || e.problem.title : t("common.error"))
      );
  useEffect(() => {
    void load();
    // once per library
  }, [lib.id]);
  const roles = useMemo(() => {
    const r: Record<string, string> = {};
    for (const g of m?.grants ?? []) if (g.library_id === lib.id) r[g.user_id] = g.role;
    return r;
  }, [m, lib.id]);
  const changes = Object.entries(edits).filter(([u, role]) => role !== (roles[u] ?? "none"));
  const save = async () => {
    setBusy(true);
    try {
      await api.put("/api/admin/library-acl", {
        grants: changes.map(([user_id, role]) => ({ user_id, library_id: lib.id, role })),
      });
      toast.show({ tone: "success", message: t("acl.saved") });
      forgetAnswers("admin:library-acl", "libraries", ...VIDEO_ANSWERS);
      setEdits({});
      await load();
    } catch (e) {
      toast.show({
        tone: "error",
        message: e instanceof ApiError ? e.problem.detail || e.problem.title : t("common.error"),
      });
    } finally {
      setBusy(false);
    }
  };
  return (
    <Section
      id="access"
      title={t("libpage.access.title")}
      desc={t("libpage.access.desc")}
      actions={<Link to="/admin/library-acl">{t("libpage.access.all")}</Link>}
    >
      {err ? (
        <p className="mkt-q-error">{err}</p>
      ) : m === null ? (
        <p className="mkt-muted">{t("libdash.loading")}</p>
      ) : m.users.length === 0 ? (
        <p className="mkt-muted">{t("acl.empty.desc")}</p>
      ) : (
        <form
          className="mkt-lp-acl"
          onSubmit={(e) => {
            e.preventDefault();
            void save();
          }}
        >
          <ul>
            {m.users.map((u) => (
              <li key={u.id}>
                <Select
                  label={<bdi>{u.username}</bdi>}
                  value={edits[u.id] ?? roles[u.id] ?? "none"}
                  onChange={(e) => setEdits((x) => ({ ...x, [u.id]: e.target.value }))}
                  aria-label={t("acl.cellLabel", { user: u.username, library: lib.name })}
                  options={ROLES.map((r) => ({ value: r, label: t(`acl.role.${r}`) }))}
                />
              </li>
            ))}
          </ul>
          <div>
            <Button type="submit" size="sm" disabled={changes.length === 0} loading={busy}>
              {t("acl.saveCount", { count: n(changes.length) })}
            </Button>
          </div>
        </form>
      )}
    </Section>
  );
}
