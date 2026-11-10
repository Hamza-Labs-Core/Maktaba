// Epic 59 story 59.04: Settings → This machine (admins). What the server found about the
// computer it runs on (memory with the ZFS cache counted as available, a container's
// limits, the processor and its features, the GPUs, the data disk, the platform) and the
// hardware tier it set its defaults from, with why ("Standard: 8 GB, no GPU for video").
// An admin can pick another tier, or go back to detecting it; the pick applies when the
// server starts again, which the admin can do here. Story 59.02 adds the quiet hours
// (heavy work waits for them) and, when the data disk cannot be read, whether it is an SSD.
//
//   GET /api/server/machine   the document ({available: false} without a supervisor)
//   PUT /api/server/machine   {tier: "light"|"standard"|"strong"|"auto"}
import { useCallback, useEffect, useRef, useState } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { Input } from "@ds/components/Input/Input";
import { Select } from "@ds/components/Select/Select";
import { useToast } from "@ds/components/Toast/Toast";
import { ApiError } from "../../lib/api";
import { useAuth } from "../../lib/auth";
import { useI18n } from "../../lib/i18n";
import { serverSettingsApi, type MachineDoc, type Tier } from "../../lib/serverSettingsApi";
import { LinesPlaceholder } from "../../components/Placeholders";
import "../../styles/libraryAdmin.css";
import "../../styles/updates.css";

const GIB = 1024 ** 3;
/** GB as people read them: one decimal under 10 GB (a "4 GB" NAS shows 3.7). */
export function gb(bytes: number): number {
  const g = bytes / GIB;
  return g < 10 ? Math.round(g * 10) / 10 : Math.round(g);
}

/** How often the page looks whether the restarted server answers, and for how long. */
export const RESTART_POLL_MS = 2000;
const RESTART_POLL_MAX = 90;

type Available = Extract<MachineDoc, { available: true }>;

export function ThisMachineSection({ pollMs = RESTART_POLL_MS }: { pollMs?: number } = {}) {
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
  return <MachinePanel pollMs={pollMs} />;
}

function MachinePanel({ pollMs }: { pollMs: number }) {
  const { t, n } = useI18n();
  const toast = useToast();
  const [doc, setDoc] = useState<MachineDoc | null>(null);
  const [failed, setFailed] = useState(false);
  const [pick, setPick] = useState<Tier | "auto">("auto");
  const [quiet, setQuiet] = useState("");
  const [busy, setBusy] = useState(false);
  const [restarting, setRestarting] = useState(false);
  const alive = useRef(true);

  const load = useCallback(async () => {
    try {
      const d = await serverSettingsApi.machine();
      if (!alive.current) return;
      setDoc(d);
      setFailed(false);
      if (d.available) {
        setPick(d.saved);
        setQuiet(d.quiet_hours);
      }
    } catch {
      if (alive.current) setFailed(true);
    }
  }, []);

  useEffect(() => {
    alive.current = true;
    void load();
    return () => {
      alive.current = false;
    };
  }, [load]);

  const num = (v: number) => n(v);
  const tierName = (tier: Tier) => t(`machine.tier.${tier}`);

  if (failed) {
    return (
      <ErrorState
        kind="server"
        title={t("common.error")}
        action={
          <Button variant="secondary" onClick={() => void load()}>
            {t("common.retry")}
          </Button>
        }
      />
    );
  }
  if (!doc) {
    return (
      <div className="mkt-settings__panel">
        <LinesPlaceholder lines={6} />
      </div>
    );
  }
  if (!doc.available) {
    return (
      <div className="mkt-settings__panel" data-testid="this-machine">
        <h2>{t("settings.section.machine")}</h2>
        <p className="mkt-muted">{t("machine.unavailable")}</p>
      </div>
    );
  }

  async function save(change?: Parameters<typeof serverSettingsApi.saveMachine>[0]) {
    setBusy(true);
    try {
      const d = change
        ? await serverSettingsApi.saveMachine(change)
        : await serverSettingsApi.saveTier(pick);
      setDoc(d);
      if (d.available) setQuiet(d.quiet_hours);
      toast.show({ tone: "success", message: t("machine.tier.saved") });
    } catch (err) {
      toast.show({
        tone: "error",
        message: err instanceof ApiError ? err.problem.title : t("common.error"),
      });
    } finally {
      setBusy(false);
    }
  }

  async function restart() {
    setRestarting(true);
    try {
      await serverSettingsApi.restart();
    } catch (err) {
      setRestarting(false);
      toast.show({
        tone: "error",
        message: err instanceof ApiError ? err.problem.title : t("common.error"),
      });
      return;
    }
    // the server goes away and comes back with the new tier
    for (let i = 0; i < RESTART_POLL_MAX && alive.current; i++) {
      await new Promise((r) => setTimeout(r, pollMs));
      try {
        const d = await serverSettingsApi.machine();
        if (d.available && !d.restart_needed) {
          if (alive.current) {
            setDoc(d);
            setPick(d.saved);
          }
          break;
        }
      } catch {
        // not back yet
      }
    }
    if (alive.current) setRestarting(false);
  }

  return view(doc);

  function view(d: Available) {
    const m = d.machine;
    const gpuLabel = d.reason.gpu || t("machine.gpu.none");
    const reason = t("machine.tier.reason", {
      tier: tierName(d.reason.tier),
      memory: num(d.reason.memory_gb),
      gpu: gpuLabel,
    });
    const limited = m.memory.total_bytes > 0 && m.memory.limit_bytes < m.memory.total_bytes;
    const disk = d.data_disk;
    const rows: [string, React.ReactNode][] = [
      [
        t("machine.platform"),
        t(`machine.platform.${m.platform}`) === `machine.platform.${m.platform}`
          ? m.platform
          : t(`machine.platform.${m.platform}`),
      ],
      [
        t("machine.memory"),
        <>
          {t("machine.memory.value", {
            limit: num(gb(m.memory.limit_bytes)),
            available: num(gb(m.memory.available_bytes)),
          })}
          {(m.memory.arc_reclaimable_bytes ?? 0) > 0 && (
            <> ({t("machine.memory.zfs", { n: num(gb(m.memory.arc_reclaimable_bytes ?? 0)) })})</>
          )}
          {limited && (
            <span className="mkt-muted mkt-libadmin__small" style={{ display: "block" }}>
              {t("machine.memory.ofTotal", {
                limit: num(gb(m.memory.limit_bytes)),
                total: num(gb(m.memory.total_bytes)),
              })}
            </span>
          )}
          {m.memory.arc_unread && (
            <span className="mkt-muted mkt-libadmin__small" style={{ display: "block" }}>
              {t("machine.memory.zfs_unread")}
            </span>
          )}
        </>,
      ],
      [
        t("machine.cpu"),
        <>
          {m.cpu.model && (
            <bdi dir="ltr" style={{ display: "block" }}>
              {m.cpu.model}
            </bdi>
          )}
          {t("machine.cpu.cores", { cores: num(m.cpu.cores), arch: m.arch })}
          {m.cpu.cores < m.cpu.online && (
            <span className="mkt-muted mkt-libadmin__small" style={{ display: "block" }}>
              {t("machine.cpu.quota", { cores: num(m.cpu.cores), online: num(m.cpu.online) })}
            </span>
          )}
          {m.arch === "amd64" && (
            <span className="mkt-muted mkt-libadmin__small" style={{ display: "block" }}>
              {m.cpu.avx2 ? t("machine.cpu.avx2") : t("machine.cpu.noAvx2")}
            </span>
          )}
        </>,
      ],
      [
        t("machine.gpu"),
        m.gpu.nvidia.length === 0 && m.gpu.render.length === 0 ? (
          t("machine.gpu.none")
        ) : (
          <>
            {m.gpu.nvidia.map((g) => (
              <bdi key={g.name} dir="ltr" style={{ display: "block" }}>
                {g.name}
              </bdi>
            ))}
            {m.gpu.render.map((r) => (
              <span key={r.path} style={{ display: "block" }}>
                {t("machine.gpu.render", { device: r.driver || "?", path: r.path })}
              </span>
            ))}
          </>
        ),
      ],
      [
        t("machine.disk"),
        <>
          {d.disk_how === "asked"
            ? t("machine.disk.asked", { fs: disk.fs_type || "?" })
            : t(`machine.disk.${disk.kind}`, { fs: disk.fs_type || "?" })}
          {d.rotational && (
            <span className="mkt-muted mkt-libadmin__small" style={{ display: "block" }}>
              {t("machine.disk.tunedRotational")}
            </span>
          )}
          {(disk.kind === "unknown" || d.disk_how === "asked") && (
            <Select
              label={t("machine.disk.ask")}
              value={d.disk_how === "asked" ? (d.rotational ? "rotational" : "ssd") : "auto"}
              disabled={busy || restarting}
              onChange={(e) =>
                void save({ data_disk: e.currentTarget.value as "ssd" | "rotational" | "auto" })
              }
              options={(["auto", "ssd", "rotational"] as const).map((v) => ({
                value: v,
                label: t(`machine.disk.answer.${v}`),
              }))}
            />
          )}
        </>,
      ],
    ];
    const window = (w: string) => w || t("machine.quiet.none");

    return (
      <div className="mkt-settings__panel" data-testid="this-machine">
        <h2>{t("settings.section.machine")}</h2>
        <p className="mkt-muted">{t("machine.intro")}</p>
        {d.moved_up && (
          <p role="status">{t("machine.tier.moved_up", { tier: tierName(d.tier) })}</p>
        )}
        <section className="mkt-storage__reach" aria-labelledby="mkt-machine-tier">
          <h3 id="mkt-machine-tier">
            {t("machine.tier")} <Badge>{tierName(d.tier)}</Badge>
          </h3>
          <p data-testid="tier-reason">{reason}</p>
          <p className="mkt-muted mkt-libadmin__small">
            {d.chosen ? t("machine.tier.chosen") : t("machine.tier.detected")}
          </p>
          <ul className="mkt-libadmin__small" aria-label={t("machine.defaults")}>
            <li>
              {d.defaults.pipeline_workers > 0
                ? t("machine.defaults.workers", { n: num(d.defaults.pipeline_workers) })
                : t("machine.defaults.workersAny")}
            </li>
            <li>{t("machine.defaults.postgres", { value: d.defaults.shared_buffers })}</li>
            <li>{t("machine.defaults.quiet", { window: window(d.defaults.quiet_hours) })}</li>
          </ul>
          <Select
            label={t("machine.tier.pick")}
            value={pick}
            onChange={(e) => setPick(e.currentTarget.value as Tier | "auto")}
            disabled={busy || restarting}
            options={[
              { value: "auto", label: t("machine.tier.auto", { tier: tierName(d.detected) }) },
              ...(["light", "standard", "strong"] as Tier[]).map((tier) => ({
                value: tier,
                label: tierName(tier),
              })),
            ]}
          />
          <Button
            variant="secondary"
            onClick={() => void save()}
            disabled={busy || restarting || pick === d.saved}
          >
            {t("machine.tier.save")}
          </Button>
          {d.restart_needed && (
            <p role="status" className="mkt-muted">
              {restarting ? t("machine.tier.restarting") : t("machine.tier.restartNeeded")}{" "}
              {d.can_restart && !restarting && (
                <Button variant="primary" onClick={() => void restart()}>
                  {t("machine.tier.restart")}
                </Button>
              )}
            </p>
          )}
        </section>
        <section className="mkt-storage__reach" aria-labelledby="mkt-machine-quiet">
          <h3 id="mkt-machine-quiet">{t("settings.quiet_hours")}</h3>
          <Input
            label={t("settings.quiet_hours")}
            description={t("machine.quiet.help")}
            value={quiet}
            dir="ltr"
            placeholder="01:00-06:00"
            disabled={busy || restarting}
            onChange={(e) => setQuiet(e.currentTarget.value)}
          />
          <Button
            variant="secondary"
            disabled={busy || restarting || (d.quiet_set && quiet.trim() === d.quiet_hours)}
            onClick={() => void save({ quiet_hours: quiet.trim() })}
          >
            {t("machine.quiet.save")}
          </Button>
          {d.quiet_set && (
            <Button
              variant="ghost"
              disabled={busy || restarting}
              onClick={() => void save({ quiet_default: true })}
            >
              {t("machine.quiet.default", { window: window(d.quiet_default) })}
            </Button>
          )}
        </section>
        <dl className="mkt-about__facts" aria-label={t("settings.section.machine")}>
          {rows.map(([label, value]) => (
            <div key={label}>
              <dt>{label}</dt>
              <dd>{value}</dd>
            </div>
          ))}
        </dl>
      </div>
    );
  }
}
