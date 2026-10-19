// /admin/machines — Epic 31: this home and the machines that share its
// library and queue. The home card opens and closes joining; pending
// requests are approved with the 6-digit code the machine shows; each
// joined machine shows its status, capabilities, storage check, path map
// and the stages it serves (with an admin switch per stage).
//
// Contract: GET /api/cluster and the /api/cluster/* mutations (design §8),
// re-read every POLL_MS while the page is open.
import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { Modal } from "@ds/components/Modal/Modal";
import { Select } from "@ds/components/Select/Select";
import { Tabs } from "@ds/components/Tabs/Tabs";
import { Toggle } from "@ds/components/Choice/Toggle";
import { useToast } from "@ds/components/Toast/Toast";
import { AdminGate } from "../../components/AdminGate";
import { useClusterFmt } from "../../components/cluster/format";
import { JobsPanel } from "../../components/cluster/JobsPanel";
import { LibraryPanel } from "../../components/cluster/LibraryPanel";
import { LiveMachines } from "../../components/cluster/LiveMachines";
import { LlmPanel } from "../../components/cluster/LlmPanel";
import type { LogTarget } from "../../components/cluster/LogViewer";
import { LogsPanel } from "../../components/cluster/LogsPanel";
import { queueLabeller } from "../../components/cluster/names";
import { QueuesPanel } from "../../components/cluster/QueuesPanel";
import { RelayPanel } from "../../components/cluster/RelayPanel";
import { useClusterFiles } from "../../components/cluster/useClusterFiles";
import { useFmt } from "../../components/queue/format";
import { useClusterStream } from "../../lib/clusterStream";
import type { SocketStatus } from "../../lib/clusterSocket";
import { ApiError } from "../../lib/api";
import {
  approveFailure,
  clusterApi,
  normalizePairingCode,
  type ClusterHome,
  type ClusterNode,
  type ClusterView,
  type NodeUpdate,
} from "../../lib/clusterApi";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import "../../styles/queue.css";
import "../../styles/machines.css";
import "../../styles/cluster.css";

export const POLL_MS = 5_000;

export function Machines() {
  return (
    <AdminGate>
      <MachinesPage />
    </AdminGate>
  );
}

const STATUS_BADGE: Record<SocketStatus, ["success" | "neutral" | "warn", string]> = {
  idle: ["neutral", "cluster.status.connecting"],
  connecting: ["neutral", "cluster.status.connecting"],
  open: ["success", "cluster.status.open"],
  reconnecting: ["warn", "cluster.status.reconnecting"],
  unavailable: ["neutral", "cluster.status.unavailable"],
};

// The page: the approvals (phase 1) and the live views of the cluster hub
// (phase 2, design-agent.md §5) in tabs, all fed by one socket.
function MachinesPage() {
  const { t, n } = useI18n();
  const stream = useClusterStream();
  const files = useClusterFiles();
  const [tab, setTab] = useState("machines");
  const [logTarget, setLogTarget] = useState<LogTarget | null>(null);
  const queueLabel = useMemo(() => queueLabeller(stream.latestQueues), [stream.latestQueues]);
  const [tone, statusKey] = STATUS_BADGE[stream.status];
  const openLogs = (target: LogTarget) => {
    setLogTarget(target);
    setTab("logs");
  };

  return (
    <section className="mkt-page mkt-mc" aria-labelledby="mkt-mc-h">
      <header className="mkt-mc__head">
        <div className="mkt-mc-row">
          <h1 id="mkt-mc-h">{t("machines.title")}</h1>
          <Badge tone={tone} role="status">
            {t(statusKey)}
          </Badge>
        </div>
        <p className="mkt-mc-muted">{t("machines.intro")}</p>
        {stream.dropped > 0 && (
          <p className="mkt-mc-muted">{t("cluster.status.dropped", { n: n(stream.dropped) })}</p>
        )}
        {stream.lastRefused && (
          <p className="mkt-mc-note" role="note">
            {t("cluster.status.refused", { reason: stream.lastRefused.reason ?? "–" })}
          </p>
        )}
      </header>
      <Tabs
        label={t("cluster.tabs.label")}
        value={tab}
        onChange={setTab}
        className="mkt-cl-tabs"
        items={[
          {
            id: "machines",
            label: t("cluster.tabs.machines"),
            content: (
              <div className="mkt-mc-tab">
                <Approvals queueLabel={queueLabel} />
                <LiveMachines stream={stream} queueLabel={queueLabel} onOpenLogs={openLogs} />
              </div>
            ),
          },
          {
            id: "jobs",
            label: t("cluster.tabs.jobs"),
            content: <JobsPanel stream={stream} queueLabel={queueLabel} />,
          },
          {
            id: "queues",
            label: t("cluster.tabs.queues"),
            content: <QueuesPanel stream={stream} files={files} queueLabel={queueLabel} />,
          },
          {
            id: "library",
            label: t("cluster.tabs.library"),
            content: <LibraryPanel stream={stream} files={files} />,
          },
          {
            id: "logs",
            label: t("cluster.tabs.logs"),
            content: (
              <LogsPanel stream={stream} files={files} target={logTarget} onTarget={setLogTarget} />
            ),
          },
          {
            id: "llm",
            label: t("cluster.tabs.llm"),
            content: <LlmPanel stream={stream} />,
          },
          {
            id: "relay",
            label: t("cluster.tabs.relay"),
            content: <RelayPanel />,
          },
        ]}
      />
    </section>
  );
}

function secondsUntil(iso: string | null | undefined): number | null {
  if (!iso) return null;
  const at = Date.parse(iso);
  if (Number.isNaN(at)) return null;
  return Math.max(0, Math.round((at - Date.now()) / 1000));
}

function useErrorToast() {
  const toast = useToast();
  const { t } = useI18n();
  return useCallback(
    (e: unknown) =>
      toast.show({
        tone: "error",
        message: e instanceof ApiError ? e.problem.detail || e.problem.title : t("common.error"),
      }),
    [toast, t]
  );
}

function Approvals({ queueLabel }: { queueLabel: (stage: string) => string }) {
  const { t } = useI18n();
  const [view, setView] = useState<ClusterView | null>(null);
  const [error, setError] = useState(false);
  const alive = useRef(true);

  const load = useCallback(
    () =>
      clusterApi
        .getCluster()
        .then((v) => {
          if (!alive.current) return;
          setView(v);
          setError(false);
        })
        .catch(() => {
          if (alive.current) setError(true);
        }),
    []
  );

  useEffect(() => {
    alive.current = true;
    void load();
    const timer = setInterval(() => void load(), POLL_MS);
    return () => {
      alive.current = false;
      clearInterval(timer);
    };
  }, [load]);

  if (!view) {
    return error ? (
      <div role="alert" className="mkt-mc-banner">
        <p>{t("machines.error.load")}</p>
        <Button variant="secondary" onClick={() => void load()}>
          {t("common.retry")}
        </Button>
      </div>
    ) : (
      <p className="mkt-loading" role="status">
        {t("common.loading")}
      </p>
    );
  }

  const pending = view.nodes.filter((n) => n.state === "pending");
  const machines = view.nodes.filter((n) => n.state !== "pending");
  const reload = () => void load();

  return (
    <>
      {error && (
        <p className="mkt-mc-muted" role="status">
          {t("machines.stale")}
        </p>
      )}
      <HomeCard home={view.home} onChanged={reload} />
      <section className="mkt-mc-section" aria-labelledby="mkt-mc-pending-h">
        <h2 id="mkt-mc-pending-h" className="mkt-mc-section__title">
          {t("machines.pending.title")}
        </h2>
        {pending.length === 0 ? (
          <p className="mkt-mc-muted">{t("machines.pending.none")}</p>
        ) : (
          <ul className="mkt-mc-list">
            {pending.map((n) => (
              <PendingRow key={n.id} node={n} onChanged={reload} />
            ))}
          </ul>
        )}
      </section>
      <section className="mkt-mc-section" aria-labelledby="mkt-mc-nodes-h">
        <h2 id="mkt-mc-nodes-h" className="mkt-mc-section__title">
          {t("machines.nodes.title")}
        </h2>
        {machines.length === 0 ? (
          <p className="mkt-mc-muted">{t("machines.nodes.none")}</p>
        ) : (
          <ul className="mkt-mc-list">
            {machines.map((n) => (
              <MachineCard key={n.id} node={n} onChanged={reload} queueLabel={queueLabel} />
            ))}
          </ul>
        )}
      </section>
    </>
  );
}

// ---------------------------------------------------------------- home

function HomeCard({ home, onChanged }: { home: ClusterHome; onChanged: () => void }) {
  const { t } = useI18n();
  const fmt = useFmt();
  const showError = useErrorToast();
  const [busy, setBusy] = useState(false);
  const left = home.accept_joins ? secondsUntil(home.accept_joins_until) : null;

  const toggle = (on: boolean) => {
    setBusy(true);
    clusterApi
      .putSettings({ accept_joins: on })
      .then(onChanged)
      .catch(showError)
      .finally(() => setBusy(false));
  };

  return (
    <section className="mkt-mc-card mkt-mc-home" aria-labelledby="mkt-mc-home-h">
      <h2 id="mkt-mc-home-h" className="mkt-mc-card__eyebrow">
        {t("machines.home.title")}
      </h2>
      <p className="mkt-mc-home__name">{bidiIsolate(home.name)}</p>
      <Toggle
        label={t("machines.home.accept")}
        checked={home.accept_joins}
        disabled={busy || !home.roles_supported}
        onChange={(e) => toggle(e.currentTarget.checked)}
      />
      {home.roles_supported && home.accept_joins && left !== null && (
        <p className="mkt-mc-muted">{t("machines.home.acceptLeft", { time: fmt.dur(left) })}</p>
      )}
      {home.roles_supported && !home.accept_joins && (
        <p className="mkt-mc-muted">{t("machines.home.closed")}</p>
      )}
      {!home.roles_supported && (
        <p className="mkt-mc-note" role="note">
          {home.roles_reason || t("machines.home.unsupported")}
        </p>
      )}
    </section>
  );
}

// ---------------------------------------------------------------- pending

function platform(node: ClusterNode): string {
  return [node.os, node.arch].filter(Boolean).join(" · ");
}

function PendingRow({ node, onChanged }: { node: ClusterNode; onChanged: () => void }) {
  const { t, n } = useI18n();
  const fmt = useFmt();
  const showError = useErrorToast();
  const [code, setCode] = useState("");
  const [message, setMessage] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const left = secondsUntil(node.expires_at);
  const expired = left === 0;

  const approve = (e: FormEvent) => {
    e.preventDefault();
    const digits = normalizePairingCode(code);
    if (!digits) {
      setMessage(t("machines.pending.codeFormat"));
      return;
    }
    setBusy(true);
    setMessage(null);
    clusterApi
      .approve(node.id, digits)
      .then(() => {
        setCode("");
        onChanged();
      })
      .catch((err: unknown) => {
        const f = approveFailure(err);
        if (f.kind === "wrong_code") {
          setMessage(
            f.attemptsLeft === null
              ? t("machines.pending.wrongCodeNoCount")
              : t("machines.pending.wrongCode", { n: n(f.attemptsLeft) })
          );
          if (f.attemptsLeft === 0) onChanged();
        } else if (f.kind === "expired") {
          setMessage(t("machines.pending.expired"));
          onChanged();
        } else {
          setMessage(f.message);
        }
      })
      .finally(() => setBusy(false));
  };

  const reject = () => {
    setBusy(true);
    clusterApi
      .reject(node.id)
      .then(onChanged)
      .catch(showError)
      .finally(() => setBusy(false));
  };

  const name = bidiIsolate(node.name);
  return (
    <li className="mkt-mc-card mkt-mc-pending">
      <div className="mkt-mc-row">
        <strong className="mkt-mc-name">{name}</strong>
        <span className="mkt-mc-muted">{platform(node)}</span>
        {expired ? (
          <Badge tone="error">{t("machines.pending.expiredBadge")}</Badge>
        ) : (
          left !== null && (
            <span className="mkt-mc-muted">
              {t("machines.pending.timeLeft", { time: fmt.dur(left) })}
            </span>
          )
        )}
      </div>
      <form className="mkt-mc-pending__form" onSubmit={approve}>
        <Input
          label={t("machines.pending.code", { name })}
          description={t("machines.pending.codeHelp")}
          value={code}
          onChange={(e) => setCode(e.currentTarget.value)}
          inputMode="numeric"
          autoComplete="one-time-code"
          maxLength={9}
          dir="ltr"
          disabled={busy || expired}
          className="mkt-mc-code"
        />
        <div className="mkt-mc-actions">
          <Button type="submit" disabled={busy || expired || code.trim() === ""}>
            {t("machines.pending.approve")}
          </Button>
          <Button type="button" variant="secondary" disabled={busy} onClick={reject}>
            {t("machines.pending.reject")}
          </Button>
        </div>
      </form>
      {message && (
        <p className="mkt-mc-error" role="alert">
          {message}
        </p>
      )}
    </li>
  );
}

// ---------------------------------------------------------------- machines

type Status = "online" | "offline" | "draining" | "revoked" | "rejected";

function nodeStatus(node: ClusterNode): Status {
  if (node.state === "revoked") return "revoked";
  if (node.state === "rejected") return "rejected";
  if (node.drain) return "draining";
  return node.online ? "online" : "offline";
}

const STATUS_TONE: Record<Status, "success" | "neutral" | "warn" | "error"> = {
  online: "success",
  offline: "neutral",
  draining: "warn",
  revoked: "error",
  rejected: "error",
};

function num(v: unknown): number | null {
  return typeof v === "number" && Number.isFinite(v) ? v : null;
}

/** OS / CPU / memory / GPU / Apple Silicon chips from the reported capabilities. */
function useCapabilityChips(node: ClusterNode): string[] {
  const { t, n } = useI18n();
  const c = node.capabilities ?? {};
  const chips: string[] = [];
  const osVersion = typeof c.os_version === "string" ? c.os_version : "";
  if (node.os) chips.push([node.os, osVersion].filter(Boolean).join(" "));
  if (node.arch) chips.push(node.arch);
  const cpus = num(c.cpu_count) ?? num(c.cpus);
  if (cpus !== null) chips.push(t("machines.cap.cpus", { n: n(cpus) }));
  const memGb =
    num(c.memory_gb) ??
    (num(c.memory_bytes) !== null ? (num(c.memory_bytes) as number) / 1024 ** 3 : null);
  if (memGb !== null)
    chips.push(t("machines.cap.memory", { n: n(memGb, { maximumFractionDigits: 0 }) }));
  const gpus = Array.isArray(c.gpus) ? c.gpus : Array.isArray(c.nvidia_gpus) ? c.nvidia_gpus : [];
  for (const g of gpus) {
    const label =
      typeof g === "string"
        ? g
        : g && typeof g === "object" && typeof (g as { name?: unknown }).name === "string"
          ? (g as { name: string }).name
          : null;
    chips.push(label ? t("machines.cap.gpuNamed", { name: label }) : t("machines.cap.gpu"));
  }
  if (c.apple_silicon === true) chips.push(t("machines.cap.appleSilicon"));
  return chips;
}

function MachineCard({
  node,
  onChanged,
  queueLabel,
}: {
  node: ClusterNode;
  onChanged: () => void;
  queueLabel: (stage: string) => string;
}) {
  const { t } = useI18n();
  const fmt = useFmt();
  const showError = useErrorToast();
  const [busy, setBusy] = useState(false);
  const [confirm, setConfirm] = useState<"revoke" | "remove" | null>(null);
  const status = nodeStatus(node);
  const chips = useCapabilityChips(node);
  const active = node.state === "approved";
  const name = bidiIsolate(node.name);

  const run = (p: Promise<unknown>) => {
    setBusy(true);
    p.then(onChanged)
      .catch(showError)
      .finally(() => setBusy(false));
  };
  const update = (body: NodeUpdate) => run(clusterApi.updateNode(node.id, body));

  return (
    <li className={`mkt-mc-card mkt-mc-node mkt-mc-node--${status}`}>
      <article aria-label={node.name}>
        <header className="mkt-mc-row">
          <h3 className="mkt-mc-name">{name}</h3>
          <Badge tone={STATUS_TONE[status]}>{t(`machines.status.${status}`)}</Badge>
          {node.last_seen_at && (
            <span className="mkt-mc-muted">
              {t("machines.lastSeen", { time: fmt.ago(node.last_seen_at) })}
            </span>
          )}
        </header>
        {chips.length > 0 && (
          <ul className="mkt-mc-chips" aria-label={t("machines.cap.title")}>
            {chips.map((c, i) => (
              <li key={i} className="mkt-mc-chip">
                {c}
              </li>
            ))}
          </ul>
        )}
        {active && (
          <>
            <StorageCheck node={node} />
            <PathMapEditor node={node} busy={busy} onSave={update} />
            <StageTable node={node} busy={busy} onSet={update} queueLabel={queueLabel} />
          </>
        )}
        <div className="mkt-mc-actions mkt-mc-node__actions">
          {active && (
            <>
              <Toggle
                label={t("machines.drain")}
                checked={node.drain}
                disabled={busy}
                onChange={(e) => update({ drain: e.currentTarget.checked })}
              />
              <Button variant="destructive" disabled={busy} onClick={() => setConfirm("revoke")}>
                {t("machines.revoke")}
              </Button>
            </>
          )}
          {(node.state === "revoked" || node.state === "rejected") && (
            <Button variant="secondary" disabled={busy} onClick={() => setConfirm("remove")}>
              {t("machines.remove")}
            </Button>
          )}
        </div>
      </article>
      {confirm && (
        <Modal
          open
          dismissable={false}
          onClose={() => setConfirm(null)}
          title={t(`machines.${confirm}.title`, { name })}
          footer={
            <>
              <Button variant="secondary" onClick={() => setConfirm(null)}>
                {t("common.cancel")}
              </Button>
              <Button
                variant="destructive"
                onClick={() => {
                  const which = confirm;
                  setConfirm(null);
                  run(
                    which === "revoke" ? clusterApi.revoke(node.id) : clusterApi.removeNode(node.id)
                  );
                }}
              >
                {t(`machines.${confirm}.confirm`)}
              </Button>
            </>
          }
        >
          <p>{t(`machines.${confirm}.body`, { name })}</p>
        </Modal>
      )}
    </li>
  );
}

function StorageCheck({ node }: { node: ClusterNode }) {
  const { t } = useI18n();
  const fmt = useFmt();
  const s = node.storage;
  const word = (v: string) =>
    v === "ok" || v === "error" || v === "skipped" ? t(`machines.storage.${v}`) : v;
  return (
    <section className="mkt-mc-sub" aria-label={t("machines.storage.title")}>
      <h4 className="mkt-mc-sub__title">{t("machines.storage.title")}</h4>
      {!s ? (
        <p className="mkt-mc-muted">{t("machines.storage.unknown")}</p>
      ) : (
        <p className="mkt-mc-storage">
          <span className={`mkt-mc-check mkt-mc-check--${s.read}`}>
            {t("machines.storage.read", { v: word(s.read) })}
          </span>
          <span className={`mkt-mc-check mkt-mc-check--${s.write}`}>
            {t("machines.storage.write", { v: word(s.write) })}
          </span>
          {s.checked_at && (
            <span className="mkt-mc-muted">
              {t("machines.storage.checked", { time: fmt.ago(s.checked_at) })}
            </span>
          )}
          {s.detail && <span className="mkt-mc-muted">{s.detail}</span>}
        </p>
      )}
    </section>
  );
}

interface MapRow {
  from: string;
  to: string;
}

function PathMapEditor({
  node,
  busy,
  onSave,
}: {
  node: ClusterNode;
  busy: boolean;
  onSave: (body: NodeUpdate) => void;
}) {
  const { t } = useI18n();
  const [draft, setDraft] = useState<MapRow[] | null>(null);
  const entries = Object.entries(node.path_map ?? {});
  const source =
    node.path_map_source === "admin"
      ? t("machines.pathMap.admin")
      : node.path_map_source === "inferred"
        ? t("machines.pathMap.inferred")
        : null;

  const setRow = (i: number, patch: Partial<MapRow>) =>
    setDraft((d) => (d ? d.map((r, j) => (j === i ? { ...r, ...patch } : r)) : d));

  const save = (e: FormEvent) => {
    e.preventDefault();
    if (!draft) return;
    const map: Record<string, string> = {};
    for (const r of draft) {
      if (r.from.trim() && r.to.trim()) map[r.from.trim()] = r.to.trim();
    }
    onSave({ path_map: map });
    setDraft(null);
  };

  return (
    <section className="mkt-mc-sub" aria-label={t("machines.pathMap.title")}>
      <div className="mkt-mc-row">
        <h4 className="mkt-mc-sub__title">{t("machines.pathMap.title")}</h4>
        {source && <span className="mkt-mc-muted">{source}</span>}
      </div>
      {draft === null ? (
        <>
          {entries.length === 0 ? (
            <p className="mkt-mc-muted">{t("machines.pathMap.none")}</p>
          ) : (
            <ul className="mkt-mc-map">
              {entries.map(([from, to]) => (
                <li key={from} dir="ltr">
                  <code>{from}</code> <span aria-hidden="true">→</span>
                  <span className="mkt-visually-hidden">{t("machines.pathMap.to")}</span>{" "}
                  <code>{to}</code>
                </li>
              ))}
            </ul>
          )}
          <div className="mkt-mc-actions">
            <Button
              size="sm"
              variant="secondary"
              disabled={busy}
              onClick={() =>
                setDraft(
                  entries.length
                    ? entries.map(([from, to]) => ({ from, to }))
                    : [{ from: "", to: "" }]
                )
              }
            >
              {t("machines.pathMap.edit")}
            </Button>
            {node.path_map_source === "admin" && (
              <Button
                size="sm"
                variant="ghost"
                disabled={busy}
                onClick={() => onSave({ path_map: null })}
              >
                {t("machines.pathMap.reset")}
              </Button>
            )}
          </div>
        </>
      ) : (
        <form onSubmit={save} className="mkt-mc-map-form">
          {draft.map((r, i) => (
            <div key={i} className="mkt-mc-map-row" dir="ltr">
              <Input
                label={t("machines.pathMap.from", { n: i + 1 })}
                value={r.from}
                onChange={(e) => setRow(i, { from: e.currentTarget.value })}
              />
              <Input
                label={t("machines.pathMap.toLabel", { n: i + 1 })}
                value={r.to}
                onChange={(e) => setRow(i, { to: e.currentTarget.value })}
              />
              <Button
                size="sm"
                variant="ghost"
                type="button"
                aria-label={t("machines.pathMap.removeRow", { n: i + 1 })}
                onClick={() => setDraft((d) => (d ? d.filter((_, j) => j !== i) : d))}
              >
                ×
              </Button>
            </div>
          ))}
          <div className="mkt-mc-actions">
            <Button
              size="sm"
              variant="ghost"
              type="button"
              onClick={() => setDraft((d) => [...(d ?? []), { from: "", to: "" }])}
            >
              {t("machines.pathMap.addRow")}
            </Button>
            <Button size="sm" type="submit" disabled={busy}>
              {t("common.save")}
            </Button>
            <Button size="sm" variant="secondary" type="button" onClick={() => setDraft(null)}>
              {t("common.cancel")}
            </Button>
          </div>
        </form>
      )}
    </section>
  );
}

type SwitchValue = "auto" | "on" | "off";

// The queues this machine pulls from: what it reports (pulls from / does
// not, and why) next to the admin's switch per queue.
function StageTable({
  node,
  busy,
  onSet,
  queueLabel,
}: {
  node: ClusterNode;
  busy: boolean;
  onSet: (body: NodeUpdate) => void;
  queueLabel: (stage: string) => string;
}) {
  const { t } = useI18n();
  const cf = useClusterFmt();
  const name = bidiIsolate(node.name);
  const set = new Set<string>(Object.keys(node.stage_overrides ?? {}));
  for (const w of node.workers ?? []) {
    w.stages.forEach((s) => set.add(s));
    Object.keys(w.stages_off ?? {}).forEach((s) => set.add(s));
  }
  const stages = [...set].sort();
  if (stages.length === 0) {
    return (
      <section className="mkt-mc-sub" aria-label={t("machines.stages.title")}>
        <h4 className="mkt-mc-sub__title">{t("machines.stages.title")}</h4>
        <p className="mkt-mc-muted">{t("machines.stages.none")}</p>
      </section>
    );
  }
  const options = [
    { value: "auto", label: t("machines.switch.auto") },
    { value: "on", label: t("machines.switch.on") },
    { value: "off", label: t("machines.switch.off") },
  ];
  return (
    <section className="mkt-mc-sub">
      <h4 className="mkt-mc-sub__title" id={`mkt-mc-st-${node.id}`}>
        {t("machines.stages.title")}
      </h4>
      <p className="mkt-mc-muted">{t("machines.stages.help")}</p>
      <div className="mkt-mc-table-wrap">
        <table className="mkt-mc-table" aria-labelledby={`mkt-mc-st-${node.id}`}>
          <thead>
            <tr>
              <th scope="col">{t("machines.stages.stage")}</th>
              <th scope="col">{t("machines.stages.state")}</th>
              <th scope="col">{t("machines.stages.switch")}</th>
            </tr>
          </thead>
          <tbody>
            {stages.map((s) => {
              const served = (node.workers ?? []).some((w) => w.stages.includes(s));
              const reason = (node.workers ?? [])
                .map((w) => w.stages_off?.[s])
                .find((r): r is string => typeof r === "string" && r !== "");
              const o = node.stage_overrides?.[s];
              const value: SwitchValue = o === true ? "on" : o === false ? "off" : "auto";
              return (
                <tr key={s}>
                  <th scope="row">{queueLabel(s)}</th>
                  <td>
                    {served ? (
                      <span className="mkt-mc-served">
                        <span aria-hidden="true">✓ </span>
                        {t("machines.stages.served")}
                      </span>
                    ) : (
                      <span className="mkt-mc-off">
                        <span aria-hidden="true">– </span>
                        {reason
                          ? t("machines.stages.offWhy", { reason: cf.reason(reason) })
                          : t("machines.stages.off")}
                      </span>
                    )}
                  </td>
                  <td>
                    <Select
                      aria-label={t("machines.stages.switchFor", { stage: queueLabel(s), name })}
                      value={value}
                      disabled={busy}
                      options={options}
                      onChange={(e) => {
                        const v = e.currentTarget.value as SwitchValue;
                        onSet({ stages: { [s]: v === "auto" ? null : v === "on" } });
                      }}
                    />
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </section>
  );
}
