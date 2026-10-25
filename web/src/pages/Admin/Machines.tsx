// /admin/machines — Epic 31: this home and the machines that share its
// library and queue. The home card opens and closes joining; pending
// requests are approved with the 6-digit code the machine shows; each
// joined machine shows its status, capabilities, storage check, path map
// and the stages it serves (with an admin switch per stage).
//
// Contract: GET /api/cluster and the /api/cluster/* mutations (design §8),
// re-read every POLL_MS while the page is open.
//
// The page holds still while it updates: machines keep a fixed order (state,
// then name), a poll that brings the same data re-renders nothing (unchanged
// nodes keep their object, the cards are memoised), the skeleton shows on the
// first load only, status lines keep their space whether or not they say
// anything, and the hub's frames reach React at most once a second
// (clusterStream). Per-job progress is not drawn here: each machine says how
// many jobs it runs and links to the Processing page filtered by it.
//
// A full machine that cannot reach the home's database says so on its card, with why and
// a link to its own log files on the admin log page (update-attempts.md).
import {
  memo,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
  type ReactNode,
} from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { Modal } from "@ds/components/Modal/Modal";
import { Select } from "@ds/components/Select/Select";
import { Tabs } from "@ds/components/Tabs/Tabs";
import { Toggle } from "@ds/components/Choice/Toggle";
import { useToast } from "@ds/components/Toast/Toast";
import { AdminGate } from "../../components/AdminGate";
import { CanonicalizePanel } from "../../components/cluster/CanonicalizePanel";
import { EmergencyCard } from "../../components/cluster/EmergencyCard";
import { pausedOf, useClusterFmt } from "../../components/cluster/format";
import { JobsPanel } from "../../components/cluster/JobsPanel";
import { JobsSummary } from "../../components/cluster/JobsSummary";
import { LibraryPanel } from "../../components/cluster/LibraryPanel";
import { LiveMachines } from "../../components/cluster/LiveMachines";
import { LlmPanel } from "../../components/cluster/LlmPanel";
import type { LogTarget } from "../../components/cluster/LogViewer";
import { LogsPanel } from "../../components/cluster/LogsPanel";
import {
  HOME_HOST,
  countsKey,
  jobCounts,
  reconcileView,
} from "../../components/cluster/machineSummary";
import { queueLabeller } from "../../components/cluster/names";
import { QueuesPanel } from "../../components/cluster/QueuesPanel";
import { RelayDoorCard } from "../../components/cluster/RelayDoorCard";
import { RelayPanel } from "../../components/cluster/RelayPanel";
import { SwitchHomeCard } from "../../components/cluster/SwitchHomeCard";
import { useClusterFiles } from "../../components/cluster/useClusterFiles";
import { useFmt } from "../../components/queue/format";
import { useClusterStream, type ClusterStream } from "../../lib/clusterStream";
import type { SocketStatus } from "../../lib/clusterSocket";
import { ApiError } from "../../lib/api";
import {
  approveFailure,
  clusterApi,
  normalizePairingCode,
  type ClusterHome,
  type ClusterNode,
  type ClusterView,
  type NodeCopy,
  type NodeStorage,
  type NodeUpdate,
  type StorageServer,
} from "../../lib/clusterApi";
import { bidiIsolate, lookupMessage, useI18n } from "../../lib/i18n";
import { safeHttpUrl } from "../../lib/navigation";
import { logWindow, nodeLogsLink } from "../../lib/logLinks";
import { homeDbOf, type HomeDb } from "../../lib/homeDb";
import { TitleRow } from "../../help/HelpLink";
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
  // the labels only change when the stages do, not on every queue sample
  const labelsKey = (stream.latestQueues?.stages ?? [])
    .map((s) => `${s.stage}\u0001${s.label}`)
    .join("\u0002");
  const queueLabel = useMemo(() => queueLabeller(stream.latestQueues), [labelsKey]);
  const [tone, statusKey] = STATUS_BADGE[stream.status];
  const openLogs = useCallback((target: LogTarget) => {
    setLogTarget(target);
    setTab("logs");
  }, []);

  return (
    <section className="mkt-page mkt-mc" aria-labelledby="mkt-mc-h">
      <header className="mkt-mc__head">
        <div className="mkt-mc-row">
          <TitleRow>
            <h1 id="mkt-mc-h">{t("machines.title")}</h1>
          </TitleRow>
          <span className="mkt-mc-live">
            <Badge tone={tone} role="status">
              {t(statusKey)}
            </Badge>
          </span>
        </div>
        <p className="mkt-mc-muted">{t("machines.intro")}</p>
        {/* one line that is always there, so a note never pushes the page down */}
        <p className="mkt-mc-statusline">
          {stream.dropped > 0 && (
            <span className="mkt-mc-muted">
              {t("cluster.status.dropped", { n: n(stream.dropped) })}
            </span>
          )}
          {stream.lastRefused && (
            <span className="mkt-mc-warn" role="note">
              {t("cluster.status.refused", { reason: stream.lastRefused.reason ?? "–" })}
            </span>
          )}
        </p>
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
              <MachinesOverview
                stream={stream}
                queueLabel={queueLabel}
                onOpenLogs={openLogs}
                extra={
                  <>
                    <EmergencyCard />
                    <SwitchHomeCard />
                    <RelayDoorCard />
                  </>
                }
              />
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

/**
 * The Machines tab: this home, the requests to join and the joined machines
 * (Approvals), then what each connected machine is doing now (LiveMachines).
 * Each section is its own component; a new one (say, the relay door's status)
 * goes in `extra` or as one more child here, without touching the others.
 */
function MachinesOverview({
  stream,
  queueLabel,
  onOpenLogs,
  extra,
}: {
  stream: ClusterStream;
  queueLabel: (stage: string) => string;
  onOpenLogs: (target: LogTarget) => void;
  extra?: ReactNode;
}) {
  // jobs per machine from the hub; the same counts keep the same object
  const counts = jobCounts(stream.processes);
  const key = countsKey(counts);
  const live = stream.ready && stream.status === "open";
  const jobs = useMemo(() => (live ? counts : null), [key, live]);
  // the machines that cannot reach the home's database (from the cluster view): their live
  // card is not green either
  const [dbDown, setDbDown] = useState("");
  const noHomeDb = useMemo(() => new Set(dbDown ? dbDown.split("\n") : []), [dbDown]);
  return (
    <div className="mkt-mc-tab">
      <Approvals queueLabel={queueLabel} jobs={jobs} onHomeDbDown={setDbDown} />
      {extra}
      <LiveMachines
        stream={stream}
        queueLabel={queueLabel}
        onOpenLogs={onOpenLogs}
        jobs={jobs}
        noHomeDb={noHomeDb}
      />
    </div>
  );
}

// safeView makes a /api/cluster answer safe to sort and render: a null or
// missing node list is empty, and a node with no name sorts as "".
function safeView(v: ClusterView | null | undefined): ClusterView {
  const nodes = Array.isArray(v?.nodes) ? v.nodes : [];
  return {
    ...(v ?? ({} as ClusterView)),
    home: v?.home ?? (null as unknown as ClusterHome),
    nodes: nodes.map((n) => (typeof n.name === "string" ? n : { ...n, name: "" })),
  };
}

// asciiDigits turns Arabic-Indic (٠-٩) and Persian (۰-۹) digits into 0-9,
// so a code typed on an Arabic keyboard is accepted.
function asciiDigits(s: string): string {
  return s.replace(/[\u0660-\u0669\u06F0-\u06F9]/g, (d) => String(d.charCodeAt(0) & 0xf));
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

type JobCounts = Record<string, number> | null;

const Approvals = memo(function Approvals({
  queueLabel,
  jobs,
  onHomeDbDown,
}: {
  queueLabel: (stage: string) => string;
  /** jobs running per machine (node id / "home"); null while the hub is not connected */
  jobs: JobCounts;
  /** the machines that cannot reach the home's database, as their node ids one per line */
  onHomeDbDown?: (ids: string) => void;
}) {
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
          const safe = safeView(v);
          setView((prev) => reconcileView(prev, safe));
          setError(false);
          onHomeDbDown?.(
            safe.nodes
              .filter((n) => n.online && homeDbOf(n.update_state)?.ok === false)
              .map((n) => n.id)
              .join("\n")
          );
        })
        .catch(() => {
          if (alive.current) setError(true);
        }),
    [onHomeDbDown]
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

  const reload = useCallback(() => void load(), [load]);

  if (!view) {
    return error ? (
      <div role="alert" className="mkt-mc-banner">
        <p>{t("machines.error.load")}</p>
        <Button variant="secondary" onClick={() => void load()}>
          {t("common.retry")}
        </Button>
      </div>
    ) : (
      // the first load only: the shapes of what is coming, at their size
      <div className="mkt-mc-skeleton" aria-busy="true">
        <p className="mkt-visually-hidden" role="status">
          {t("common.loading")}
        </p>
        <div className="mkt-mc-card mkt-mc-skel mkt-mc-skel--home" aria-hidden="true" />
        <div className="mkt-mc-card mkt-mc-skel mkt-mc-skel--node" aria-hidden="true" />
      </div>
    );
  }

  const pending = view.nodes.filter((n) => n.state === "pending");
  const machines = view.nodes.filter((n) => n.state !== "pending");

  return (
    <>
      {/* always there: a failed refresh says so without moving anything */}
      <p className="mkt-mc-statusline" role="status">
        {error && <span className="mkt-mc-muted">{t("machines.stale")}</span>}
      </p>
      {view.home ? (
        <HomeCard home={view.home} onChanged={reload} jobs={jobs ? (jobs[HOME_HOST] ?? 0) : null} />
      ) : (
        // no home in the answer: say so here and keep the rest of the page
        <section className="mkt-mc-card mkt-mc-home" aria-labelledby="mkt-mc-home-h">
          <h2 id="mkt-mc-home-h" className="mkt-mc-card__eyebrow">
            {t("machines.home.title")}
          </h2>
          <p className="mkt-mc-muted">{t("common.error")}</p>
        </section>
      )}
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
              <MachineCard
                key={n.id}
                node={n}
                onChanged={reload}
                queueLabel={queueLabel}
                jobs={jobs ? (jobs[n.id] ?? 0) : null}
              />
            ))}
          </ul>
        )}
      </section>
    </>
  );
});

// ---------------------------------------------------------------- home

const HomeCard = memo(function HomeCard({
  home,
  onChanged,
  jobs,
}: {
  home: ClusterHome;
  onChanged: () => void;
  jobs: number | null;
}) {
  const { t } = useI18n();
  const fmt = useFmt();
  const showError = useErrorToast();
  const [busy, setBusy] = useState(false);
  const left = home.accept_joins ? secondsUntil(home.accept_joins_until) : null;
  const webUrl = safeHttpUrl(home.web_url);

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
      <div className="mkt-mc-row mkt-mc-row--head">
        <p className="mkt-mc-home__name">{bidiIsolate(home.name)}</p>
        <JobsSummary host={HOME_HOST} count={jobs} name={home.name} />
      </div>
      {(webUrl || home.version) && (
        <p className="mkt-mc-muted mkt-mc-line mkt-mc-where">
          {webUrl && (
            <span>
              {t("machines.home.web")}{" "}
              <WebLink href={webUrl}>
                <bdi dir="ltr">{webUrl}</bdi>
              </WebLink>
            </span>
          )}
          {home.version && (
            <span className="mkt-mc-chip">{t("machines.version", { version: home.version })}</span>
          )}
        </p>
      )}
      <Toggle
        label={t("machines.home.accept")}
        checked={home.accept_joins}
        disabled={busy || !home.roles_supported}
        onChange={(e) => toggle(e.currentTarget.checked)}
      />
      {!home.roles_supported ? (
        <p className="mkt-mc-note" role="note">
          {home.roles_reason || t("machines.home.unsupported")}
        </p>
      ) : (
        // one line whether open or closed
        <p className="mkt-mc-muted mkt-mc-line">
          {home.accept_joins
            ? left !== null
              ? t("machines.home.acceptLeft", { time: fmt.dur(left) })
              : "\u00a0"
            : t("machines.home.closed")}
        </p>
      )}
      {home.storage !== undefined && (
        <>
          <StorageCheck storage={home.storage} />
          <CanonicalizePanel status={home.canonicalize ?? null} onChanged={onChanged} />
          <PathMapEditor
            entries={home.path_map ?? home.storage?.path_map ?? null}
            source={home.path_map ? "admin" : home.storage?.path_map ? "inferred" : null}
            busy={busy}
            onSave={(pm) => {
              setBusy(true);
              clusterApi
                .setHomePathMap(pm)
                .then(onChanged)
                .catch(showError)
                .finally(() => setBusy(false));
            }}
          />
          <StorageServers />
        </>
      )}
    </section>
  );
});

// The storage servers (story 31.3): the name a stored smb://<name>/... path
// uses and the addresses it is reached by (an IP change is one edit here).
function StorageServers() {
  const { t } = useI18n();
  const showError = useErrorToast();
  const [servers, setServers] = useState<StorageServer[] | null>(null);
  const [draft, setDraft] = useState<{ name: string; addresses: string; char_map: string } | null>(
    null
  );
  const [busy, setBusy] = useState(false);
  const load = useCallback(
    () =>
      clusterApi
        .listServers()
        .then((r) => setServers(Array.isArray(r?.servers) ? r.servers : []))
        .catch(() => setServers([])),
    []
  );
  useEffect(() => {
    void load();
  }, [load]);

  const save = (e: FormEvent) => {
    e.preventDefault();
    if (!draft || !draft.name.trim()) return;
    setBusy(true);
    clusterApi
      .putServer(draft.name.trim(), {
        addresses: draft.addresses
          .split(/[\s,]+/)
          .map((a) => a.trim())
          .filter(Boolean),
        char_map: draft.char_map,
      })
      .then(() => {
        setDraft(null);
        return load();
      })
      .catch(showError)
      .finally(() => setBusy(false));
  };
  const remove = (name: string) => {
    setBusy(true);
    clusterApi
      .deleteServer(name)
      .then(load)
      .catch(showError)
      .finally(() => setBusy(false));
  };

  return (
    <section className="mkt-mc-sub" aria-label={t("machines.servers.title")}>
      <h4 className="mkt-mc-sub__title">{t("machines.servers.title")}</h4>
      {servers === null ? null : servers.length === 0 ? (
        <p className="mkt-mc-muted">{t("machines.servers.none")}</p>
      ) : (
        <ul className="mkt-mc-map">
          {servers.map((s) => (
            <li key={s.name} dir="ltr">
              <code>{`smb://${s.name}/`}</code> <span>{(s.addresses ?? []).join(", ") || "-"}</span>
              {s.char_map !== "none" && <span className="mkt-mc-muted"> ({s.char_map})</span>}{" "}
              <Button
                size="sm"
                variant="ghost"
                disabled={busy}
                onClick={() =>
                  setDraft({
                    name: s.name,
                    addresses: (s.addresses ?? []).join(", "),
                    char_map: s.char_map,
                  })
                }
              >
                {t("machines.servers.edit", { name: s.name })}
              </Button>
              <Button size="sm" variant="ghost" disabled={busy} onClick={() => remove(s.name)}>
                {t("machines.servers.remove", { name: s.name })}
              </Button>
            </li>
          ))}
        </ul>
      )}
      {draft === null ? (
        <div className="mkt-mc-actions">
          <Button
            size="sm"
            variant="secondary"
            disabled={busy}
            onClick={() => setDraft({ name: "", addresses: "", char_map: "none" })}
          >
            {t("machines.servers.add")}
          </Button>
        </div>
      ) : (
        <form onSubmit={save} className="mkt-mc-map-form">
          <div className="mkt-mc-map-row" dir="ltr">
            <Input
              label={t("machines.servers.name")}
              value={draft.name}
              onChange={(e) => setDraft({ ...draft, name: e.currentTarget.value })}
            />
            <Input
              label={t("machines.servers.addresses")}
              value={draft.addresses}
              onChange={(e) => setDraft({ ...draft, addresses: e.currentTarget.value })}
            />
            <Select
              label={t("machines.servers.charMap")}
              value={draft.char_map}
              onChange={(e) => setDraft({ ...draft, char_map: e.currentTarget.value })}
              options={[
                { value: "none", label: t("machines.servers.charMap.none") },
                { value: "fullwidth", label: t("machines.servers.charMap.fullwidth") },
                { value: "sfm", label: t("machines.servers.charMap.sfm") },
              ]}
            />
          </div>
          <div className="mkt-mc-actions">
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

/** A link to a machine's own web address, in a new tab. */
function WebLink({ href, children }: { href: string; children: ReactNode }) {
  const { t } = useI18n();
  return (
    <a href={href} target="_blank" rel="noopener noreferrer" className="mkt-mc-weblink">
      {children}
      <span className="mkt-visually-hidden"> {t("machines.newTab")}</span>
    </a>
  );
}

/** "Version 1.5.0 · windows-amd64" (Epic 35); null when it reports neither. */
function versionChip(
  t: (key: string, vars?: Record<string, string | number>) => string,
  node: ClusterNode
): string | null {
  const parts = [
    node.version ? t("machines.version", { version: node.version }) : "",
    node.platform ?? "",
  ];
  const text = parts.filter(Boolean).join(" · ");
  return text || null;
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
    const digits = normalizePairingCode(asciiDigits(code));
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

type Status = "online" | "offline" | "draining" | "paused" | "revoked" | "rejected" | "no_home_db";

function nodeStatus(node: ClusterNode): Status {
  if (node.state === "revoked") return "revoked";
  if (node.state === "rejected") return "rejected";
  // it answers, but cannot reach the home's database: not healthy, whatever else it says
  if (node.online && homeDbOf(node.update_state)?.ok === false) return "no_home_db";
  if (node.drain) return "draining";
  // its code needs a database schema the home does not have (or the other way round)
  if (node.online && pausedOf(node.workers)) return "paused";
  return node.online ? "online" : "offline";
}

const STATUS_TONE: Record<Status, "success" | "neutral" | "warn" | "error"> = {
  online: "success",
  offline: "neutral",
  draining: "warn",
  paused: "warn",
  revoked: "error",
  rejected: "error",
  no_home_db: "error",
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

/** A full machine that cannot reach the home's database: why (in the reader's words), the
 * home re-provisioning its access when it does, what it said, and its own log files around
 * the last try (the admin log page, over its channel). */
function HomeDbNotice({ homeDb, nodeId }: { homeDb: HomeDb; nodeId: string }) {
  const { t, has } = useHomeDbWords();
  const code = homeDb.code ?? "other";
  const reason = has(`machines.homeDb.code.${code}`)
    ? t(`machines.homeDb.code.${code}`)
    : t("machines.homeDb.code.other");
  return (
    <div className="mkt-mc-note mkt-mc-note--warn" role="alert" data-testid="machine-home-db">
      <p>{t("machines.homeDb.unreachable", { reason })}</p>
      {homeDb.healing && <p>{t("machines.homeDb.healing")}</p>}
      {homeDb.detail && (
        <details>
          <summary>{t("machines.homeDb.detail")}</summary>
          <p className="mkt-mc-muted" dir="ltr">
            {homeDb.detail}
          </p>
        </details>
      )}
      <a href={nodeLogsLink(nodeId, logWindow(homeDb.at))} data-testid="machine-home-db-logs">
        {t("machines.homeDb.logs")}
      </a>
    </div>
  );
}

function useHomeDbWords() {
  const { t, locale } = useI18n();
  return { t, has: (key: string) => lookupMessage(locale, key) !== undefined };
}

/** Why a machine claims nothing: its code and the home's database schema do not fit. */
function PausedNotice({ reason }: { reason: string }) {
  const cf = useClusterFmt();
  return (
    <p className="mkt-mc-muted" role="status">
      {cf.reason(reason)}
    </p>
  );
}

const COPY_STATES: ReadonlySet<string> = new Set([
  "preparing",
  "waiting",
  "copying",
  "rebuilding",
  "following",
  "behind",
  "starting",
  "incompatible",
  "unavailable",
  "error",
]);
const COPY_WARN: ReadonlySet<string> = new Set(["behind", "incompatible", "unavailable", "error"]);

/** The line a full machine's copy of this home's database (Epic 37) gets on its card. */
function useCopyNote(copy: NodeCopy | undefined): { text: string; warn: boolean } | null {
  const { t } = useI18n();
  const fmt = useFmt();
  if (!copy?.state || !COPY_STATES.has(copy.state)) return null;
  const ago = copy.applied_at ? fmt.ago(copy.applied_at) : t("machines.copy.never");
  return {
    text: t(`machines.copy.${copy.state}`, { ago, detail: copy.detail ?? "" }),
    warn: COPY_WARN.has(copy.state),
  };
}

const MachineCard = memo(function MachineCard({
  node,
  onChanged,
  queueLabel,
  jobs,
}: {
  node: ClusterNode;
  onChanged: () => void;
  queueLabel: (stage: string) => string;
  jobs: number | null;
}) {
  const { t } = useI18n();
  const fmt = useFmt();
  const showError = useErrorToast();
  const [busy, setBusy] = useState(false);
  const [confirm, setConfirm] = useState<"revoke" | "remove" | null>(null);
  const status = nodeStatus(node);
  const paused = status === "paused" ? pausedOf(node.workers) : null;
  const chips = useCapabilityChips(node);
  const active = node.state === "approved";
  const name = bidiIsolate(node.name);
  const version = versionChip(t, node);
  const vod = node.vod?.fallback ? node.vod : null;
  const copyNote = useCopyNote(node.copy);
  const webUrl = safeHttpUrl(node.web_url);
  const homeDb = homeDbOf(node.update_state);

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
        <header className="mkt-mc-row mkt-mc-row--head">
          <h3 className="mkt-mc-name">{webUrl ? <WebLink href={webUrl}>{name}</WebLink> : name}</h3>
          <Badge tone={STATUS_TONE[status]}>{t(`machines.status.${status}`)}</Badge>
          <span className="mkt-mc-muted">{t("machines.role.node")}</span>
          {version && <span className="mkt-mc-chip">{version}</span>}
          {active && node.full_node === true && <Badge tone="success">{t("machines.full")}</Badge>}
          {active && node.full_node === false && (
            <Badge tone="neutral">
              {t(
                node.worker_convert === "installer"
                  ? "machines.workerOnly"
                  : "machines.workerOnlyConverts"
              )}
            </Badge>
          )}
          {/* "last seen" only while it is away: on a live machine it changed every poll */}
          {status !== "online" && node.last_seen_at && (
            <span className="mkt-mc-muted">
              {t("machines.lastSeen", { time: fmt.ago(node.last_seen_at) })}
            </span>
          )}
          {active && <JobsSummary host={node.id} count={jobs} name={node.name} />}
        </header>
        {paused && <PausedNotice reason={paused} />}
        {active && homeDb && homeDb.ok === false && (
          <HomeDbNotice homeDb={homeDb} nodeId={node.id} />
        )}
        {active && copyNote && (
          <p className={`mkt-mc-note${copyNote.warn ? " mkt-mc-note--warn" : ""}`} role="note">
            {copyNote.text}
          </p>
        )}
        {vod && (
          <p className="mkt-mc-note" role="note">
            {t("machines.vod.fallback", {
              machine: vod.served_by ? bidiIsolate(vod.served_by) : t("machines.vod.another"),
            })}
            {vod.detail && <span className="mkt-mc-muted"> {vod.detail}</span>}
          </p>
        )}
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
            <StorageCheck storage={node.storage} />
            <PathMapEditor
              entries={node.path_map}
              source={node.path_map_source}
              busy={busy}
              onSave={(path_map) => update({ path_map })}
            />
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
});

function StorageCheck({ storage }: { storage: NodeStorage | null | undefined }) {
  const { t } = useI18n();
  const cf = useClusterFmt();
  const s = storage && Object.keys(storage).length > 0 ? storage : null;
  const word = (v: string) =>
    v === "ok" || v === "error" || v === "skipped" ? t(`machines.storage.${v}`) : v;
  return (
    <section className="mkt-mc-sub" aria-label={t("machines.storage.title")}>
      <h4 className="mkt-mc-sub__title">{t("machines.storage.title")}</h4>
      {!s ? (
        <p className="mkt-mc-muted">{t("machines.storage.unknown")}</p>
      ) : (
        <>
          <p className="mkt-mc-storage">
            <span className={`mkt-mc-check mkt-mc-check--${s.read}`}>
              {t("machines.storage.read", { v: word(s.read) })}
            </span>
            <span className={`mkt-mc-check mkt-mc-check--${s.write}`}>
              {t("machines.storage.write", { v: word(s.write) })}
            </span>
            {s.checked_at && (
              <span className="mkt-mc-muted">
                {t("machines.storage.checkedAt", { time: cf.time(Date.parse(s.checked_at)) })}
              </span>
            )}
            {s.detail && <span className="mkt-mc-muted">{s.detail}</span>}
          </p>
          {s.shares && s.shares.length > 0 && (
            <ul className="mkt-mc-shares" aria-label={t("machines.storage.shares")}>
              {s.shares.map((sh) => (
                <li key={sh.share} className="mkt-mc-share">
                  <code dir="ltr">{sh.share}</code>{" "}
                  {sh.local ? (
                    <>
                      <span aria-hidden="true">→</span>
                      <span className="mkt-visually-hidden">{t("machines.pathMap.to")}</span>{" "}
                      <code dir="ltr">{sh.local}</code>
                    </>
                  ) : (
                    <span className="mkt-mc-muted">{t("machines.storage.notMounted")}</span>
                  )}{" "}
                  <span className={`mkt-mc-check mkt-mc-check--${sh.read}`}>
                    {t("machines.storage.read", { v: word(sh.read) })}
                  </span>{" "}
                  <span className={`mkt-mc-check mkt-mc-check--${sh.write}`}>
                    {t("machines.storage.write", { v: word(sh.write) })}
                  </span>
                  {sh.detail && sh.local && <span className="mkt-mc-muted"> {sh.detail}</span>}
                </li>
              ))}
            </ul>
          )}
        </>
      )}
    </section>
  );
}

interface MapRow {
  from: string;
  to: string;
}

// "share -> local mount" (story 31.3): the shares a machine resolves the
// stored smb://server/share/... paths through; the admin's rows override
// what it works out from its own mounts.
function PathMapEditor({
  entries: map,
  source: from,
  busy,
  onSave,
}: {
  entries: Record<string, string> | null | undefined;
  source: "inferred" | "admin" | null | undefined;
  busy: boolean;
  onSave: (pathMap: Record<string, string> | null) => void;
}) {
  const { t } = useI18n();
  const [draft, setDraft] = useState<MapRow[] | null>(null);
  const entries = Object.entries(map ?? {});
  const source =
    from === "admin"
      ? t("machines.pathMap.admin")
      : from === "inferred"
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
    onSave(map);
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
            {from === "admin" && (
              <Button size="sm" variant="ghost" disabled={busy} onClick={() => onSave(null)}>
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
    (w.stages ?? []).forEach((s) => set.add(s));
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
              const served = (node.workers ?? []).some((w) => (w.stages ?? []).includes(s));
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
