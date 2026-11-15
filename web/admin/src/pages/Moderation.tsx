// /moderation: copyright notices and content reports about published channels (Epic 50 story
// 50.24 §2-§4). The queue: open notices and reports first (urgent ones on top), then the actions
// with each home's answer, the counter-notices with their restore window, the strikes and the
// terminated publishers. Each decision needs its reason (ReasonDialog) and the sensitive ones a
// fresh MFA check, which the API asks for (401 reauth_required, handled by api.ts); every one is
// audited in the cloud. The API is the authority: only a service admin is answered.
import { useState } from "react";
import type { FormEvent } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { Input } from "@ds/components/Input/Input";
import { Modal } from "@ds/components/Modal/Modal";
import { Select } from "@ds/components/Select/Select";
import { Textarea } from "@ds/components/Textarea/Textarea";
import { useToast } from "@ds/components/Toast/Toast";
import { api, fetchApi } from "../api";
import { useI18n } from "@cloud-shared/i18n";
import { Banner, LoadError, Loading, Page, useErrorMessage, useLoad } from "@cloud-shared/ui";
import { ReasonDialog } from "./ReasonDialog";

export interface NoticeRow {
  id: string;
  source: string;
  status: string;
  claimant: string;
  email: string;
  address: string;
  work: string;
  urls: string[];
  programme: string;
  at?: string;
  explanation: string;
  privacy: boolean;
  urgent: boolean;
  channel_id?: string;
  edge_known: boolean;
  received_at: string;
  unfounded: number;
  suspended: boolean;
  decision?: string;
}

export interface ReportRow {
  id: string;
  category: string;
  about: string;
  status: string;
  channel: string;
  channel_id?: string;
  edge_known: boolean;
  programme: string;
  at: string;
  email: string;
  text: string;
  urgent: boolean;
  received_at: string;
}

export interface ActionRow {
  id: string;
  kind: string;
  basis: string;
  scope: string;
  notice_id?: string;
  report_id?: string;
  channel_id?: string;
  publisher_id?: string;
  programme?: string;
  source_ref?: string;
  home_state: string;
  home_detail?: string;
  reason: string;
  actor: string;
  created_at: string;
  restored_at?: string;
}

interface CounterRow {
  id: string;
  action_id: string;
  name: string;
  email: string;
  explanation: string;
  filed_at: string;
  restore_after: string;
  lawsuit_at?: string;
  restored_at?: string;
  restore_open: boolean;
}

interface StrikeRow {
  id: string;
  publisher_id: string;
  notice_id: string;
  at: string;
  expires_at: string;
  withdrawn_at?: string;
  active: boolean;
}

export interface Queue {
  notices: NoticeRow[];
  reports: ReportRow[];
  actions: ActionRow[];
  counter_notices: CounterRow[];
  strikes: StrikeRow[];
  terminated: string[];
  strike_limit: number;
  strike_window_months: number;
}

const BASE = "/v1/admin/moderation";
export const SCOPES = ["item", "source", "channel", "publisher"];
const SOURCE_KINDS = ["channel", "playlist", "speaker", "label"];

/** A decision that needs only its reason: where it posts, and its words. */
interface Decision {
  path: string;
  title: string;
  confirm: string;
  destructive?: boolean;
  extra?: Record<string, unknown>;
  unfounded?: boolean;
}

/** What a takedown answers: a notice, a report, or what staff saw themselves. */
export interface Basis {
  basis: "notice" | "report" | "own";
  id?: string;
  channel?: string;
  at?: string;
  programme?: string;
  edgeKnown: boolean;
}

export function AdminModeration() {
  const { t, formatDate } = useI18n();
  const toast = useToast();
  const message = useErrorMessage();
  const data = useLoad(() => api.get<Queue>(BASE), "moderation");
  const [decision, setDecision] = useState<Decision | null>(null);
  const [unfounded, setUnfounded] = useState(false);
  const [taking, setTaking] = useState<Basis | null>(null);
  const [exportError, setExportError] = useState<unknown>(null);

  async function decide(reason: string) {
    if (!decision) return;
    const body: Record<string, unknown> = { reason, ...decision.extra };
    if (decision.unfounded) body.unfounded = unfounded;
    await api.post(decision.path, body);
    setDecision(null);
    toast.show({ tone: "success", message: t("admin.moderation.done") });
    data.reload();
  }

  async function download(format: "csv" | "json") {
    setExportError(null);
    try {
      const res = await fetchApi("GET", `${BASE}/export?format=${format}`);
      const url = URL.createObjectURL(await res.blob());
      const a = document.createElement("a");
      a.href = url;
      a.download = `maktaba-moderation.${format}`;
      a.click();
      URL.revokeObjectURL(url);
    } catch (err) {
      setExportError(err);
    }
  }

  const open = (d: Decision) => {
    setUnfounded(false);
    setDecision(d);
  };

  if (data.error != null) {
    return (
      <Page title={t("admin.moderation.title")}>
        <LoadError error={data.error} onRetry={data.reload} />
      </Page>
    );
  }
  const q = data.data;
  const openNotices = q?.notices.filter((n) => n.status === "open") ?? [];
  const decidedNotices = q?.notices.filter((n) => n.status !== "open") ?? [];
  const openReports = q?.reports.filter((r) => r.status === "open") ?? [];

  return (
    <Page title={t("admin.moderation.title")}>
      {!q ? (
        <Loading />
      ) : (
        <div className="mkc-stack">
          <p className="mkc-muted">
            {t("admin.moderation.policy", {
              limit: q.strike_limit,
              months: q.strike_window_months,
            })}
          </p>
          <div className="mkc-row">
            <Button
              size="sm"
              variant="secondary"
              onClick={() => setTaking({ basis: "own", edgeKnown: false })}
            >
              {t("admin.moderation.actOwn")}
            </Button>
            <Button size="sm" variant="secondary" onClick={() => void download("csv")}>
              {t("admin.moderation.exportCsv")}
            </Button>
            <Button size="sm" variant="secondary" onClick={() => void download("json")}>
              {t("admin.moderation.exportJson")}
            </Button>
          </div>
          {exportError != null && <Banner tone="error">{message(exportError)}</Banner>}

          <Card header={<h2>{t("admin.moderation.notices")}</h2>}>
            {openNotices.length === 0 ? (
              <p className="mkc-muted">{t("admin.moderation.noNotices")}</p>
            ) : (
              <table className="mkc-table">
                <thead>
                  <tr>
                    <th scope="col">{t("admin.col.created")}</th>
                    <th scope="col">{t("admin.moderation.claimant")}</th>
                    <th scope="col">{t("admin.moderation.work")}</th>
                    <th scope="col">{t("admin.moderation.where")}</th>
                    <th scope="col">
                      <span className="mkc-visually-hidden">{t("common.actions")}</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {openNotices.map((n) => (
                    <tr key={n.id}>
                      <td>
                        {formatDate(n.received_at)}
                        {n.urgent && <Badge>{t("admin.moderation.urgent")}</Badge>}
                      </td>
                      <td>
                        {n.claimant}
                        <div className="mkc-muted">{n.email}</div>
                        {n.privacy && <Badge>{t("admin.moderation.privacy")}</Badge>}
                        {n.unfounded > 0 && (
                          <div className="mkc-muted">
                            {t("admin.moderation.unfoundedCount", { count: n.unfounded })}
                          </div>
                        )}
                        {n.suspended && <Badge>{t("admin.moderation.suspended")}</Badge>}
                      </td>
                      <td>
                        {n.work}
                        <div className="mkc-muted">{n.explanation}</div>
                      </td>
                      <td>
                        {n.urls.map((u) => (
                          <div key={u} className="mkc-mono">
                            {u}
                          </div>
                        ))}
                        {n.at && <div className="mkc-muted">{formatDate(n.at)}</div>}
                        {!n.channel_id && (
                          <div className="mkc-muted">{t("admin.moderation.unresolved")}</div>
                        )}
                      </td>
                      <td>
                        <div className="mkc-row">
                          <Button
                            size="sm"
                            variant="destructive"
                            aria-label={t("admin.moderation.takeDownOne", { id: n.id })}
                            onClick={() =>
                              setTaking({
                                basis: "notice",
                                id: n.id,
                                channel: n.channel_id,
                                at: n.at,
                                programme: n.programme,
                                edgeKnown: n.edge_known,
                              })
                            }
                          >
                            {t("admin.moderation.takeDown")}
                          </Button>
                          <Button
                            size="sm"
                            variant="secondary"
                            aria-label={t("admin.moderation.rejectOne", { id: n.id })}
                            onClick={() =>
                              open({
                                path: `${BASE}/notices/${n.id}/reject`,
                                title: t("admin.moderation.rejectTitle"),
                                confirm: t("admin.moderation.reject"),
                                unfounded: true,
                              })
                            }
                          >
                            {t("admin.moderation.reject")}
                          </Button>
                          {!n.suspended && (
                            <Button
                              size="sm"
                              variant="secondary"
                              onClick={() =>
                                open({
                                  path: `${BASE}/notifiers/suspend`,
                                  title: t("admin.moderation.suspendTitle", { email: n.email }),
                                  confirm: t("admin.moderation.suspend"),
                                  destructive: true,
                                  extra: { email: n.email },
                                })
                              }
                            >
                              {t("admin.moderation.suspend")}
                            </Button>
                          )}
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </Card>

          <Card header={<h2>{t("admin.moderation.reports")}</h2>}>
            {openReports.length === 0 ? (
              <p className="mkc-muted">{t("admin.moderation.noReports")}</p>
            ) : (
              <table className="mkc-table">
                <thead>
                  <tr>
                    <th scope="col">{t("admin.col.created")}</th>
                    <th scope="col">{t("admin.moderation.category")}</th>
                    <th scope="col">{t("admin.moderation.where")}</th>
                    <th scope="col">{t("admin.moderation.text")}</th>
                    <th scope="col">
                      <span className="mkc-visually-hidden">{t("common.actions")}</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {openReports.map((r) => (
                    <tr key={r.id}>
                      <td>
                        {formatDate(r.received_at)}
                        {r.urgent && <Badge>{t("admin.moderation.urgent")}</Badge>}
                      </td>
                      <td>{t(`admin.moderation.category.${r.category}`)}</td>
                      <td>
                        <div className="mkc-mono">{r.channel}</div>
                        <div className="mkc-muted">
                          {r.programme} {formatDate(r.at)}
                        </div>
                      </td>
                      <td>
                        {r.text}
                        {r.email && <div className="mkc-muted">{r.email}</div>}
                      </td>
                      <td>
                        <div className="mkc-row">
                          <Button
                            size="sm"
                            variant="destructive"
                            aria-label={t("admin.moderation.actOne", { id: r.id })}
                            onClick={() =>
                              setTaking({
                                basis: "report",
                                id: r.id,
                                channel: r.channel_id,
                                at: r.at,
                                programme: r.programme,
                                edgeKnown: r.edge_known,
                              })
                            }
                          >
                            {t("admin.moderation.act")}
                          </Button>
                          <Button
                            size="sm"
                            variant="secondary"
                            aria-label={t("admin.moderation.dismissOne", { id: r.id })}
                            onClick={() =>
                              open({
                                path: `${BASE}/reports/${r.id}/dismiss`,
                                title: t("admin.moderation.dismissTitle"),
                                confirm: t("admin.moderation.dismiss"),
                              })
                            }
                          >
                            {t("admin.moderation.dismiss")}
                          </Button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </Card>

          <Card header={<h2>{t("admin.moderation.actions")}</h2>}>
            {q.actions.length === 0 ? (
              <p className="mkc-muted">{t("admin.moderation.noActions")}</p>
            ) : (
              <table className="mkc-table">
                <thead>
                  <tr>
                    <th scope="col">{t("admin.col.created")}</th>
                    <th scope="col">{t("admin.moderation.action")}</th>
                    <th scope="col">{t("admin.moderation.home")}</th>
                    <th scope="col">{t("admin.reason")}</th>
                    <th scope="col">
                      <span className="mkc-visually-hidden">{t("common.actions")}</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {q.actions.map((a) => (
                    <tr key={a.id}>
                      <td>
                        {formatDate(a.created_at)}
                        <div className="mkc-muted">{a.actor}</div>
                      </td>
                      <td>
                        {t(`admin.moderation.kind.${a.kind}`)} ·{" "}
                        {t(`admin.moderation.scope.${a.scope}`)}
                        <div className="mkc-muted">
                          {a.programme || a.source_ref || a.channel_id}
                        </div>
                      </td>
                      <td>
                        <Badge>{t(`admin.moderation.homeState.${a.home_state}`)}</Badge>
                        {a.home_detail && <div className="mkc-muted">{a.home_detail}</div>}
                      </td>
                      <td>{a.reason}</td>
                      <td>
                        {a.kind === "takedown" && !a.restored_at && (
                          <Button
                            size="sm"
                            variant="secondary"
                            aria-label={t("admin.moderation.restoreOne", { id: a.id })}
                            onClick={() =>
                              open({
                                path: `${BASE}/actions/${a.id}/restore`,
                                title: t("admin.moderation.restoreTitle"),
                                confirm: t("admin.moderation.restore"),
                              })
                            }
                          >
                            {t("admin.moderation.restore")}
                          </Button>
                        )}
                        {a.restored_at && (
                          <span className="mkc-muted">
                            {t("admin.moderation.restoredAt", { at: formatDate(a.restored_at) })}
                          </span>
                        )}
                        {a.notice_id && a.kind === "takedown" && !a.restored_at && (
                          <Button
                            size="sm"
                            variant="secondary"
                            onClick={() =>
                              open({
                                path: `${BASE}/notices/${a.notice_id}/withdraw`,
                                title: t("admin.moderation.withdrawTitle"),
                                confirm: t("admin.moderation.withdraw"),
                              })
                            }
                          >
                            {t("admin.moderation.withdraw")}
                          </Button>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </Card>

          {q.counter_notices.length > 0 && (
            <Card header={<h2>{t("admin.moderation.counters")}</h2>}>
              <table className="mkc-table">
                <thead>
                  <tr>
                    <th scope="col">{t("admin.col.created")}</th>
                    <th scope="col">{t("admin.moderation.claimant")}</th>
                    <th scope="col">{t("admin.moderation.window")}</th>
                    <th scope="col">
                      <span className="mkc-visually-hidden">{t("common.actions")}</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {q.counter_notices.map((c) => (
                    <tr key={c.id}>
                      <td>{formatDate(c.filed_at)}</td>
                      <td>
                        {c.name}
                        <div className="mkc-muted">{c.explanation}</div>
                      </td>
                      <td>
                        {c.lawsuit_at
                          ? t("admin.moderation.lawsuitAt", { at: formatDate(c.lawsuit_at) })
                          : c.restored_at
                            ? t("admin.moderation.restoredAt", { at: formatDate(c.restored_at) })
                            : c.restore_open
                              ? t("admin.moderation.restoreOpen")
                              : t("admin.moderation.restoreFrom", {
                                  at: formatDate(c.restore_after),
                                })}
                      </td>
                      <td>
                        {!c.lawsuit_at && !c.restored_at && (
                          <Button
                            size="sm"
                            variant="secondary"
                            onClick={() =>
                              open({
                                path: `${BASE}/counter-notices/${c.id}/lawsuit`,
                                title: t("admin.moderation.lawsuitTitle"),
                                confirm: t("admin.moderation.lawsuit"),
                              })
                            }
                          >
                            {t("admin.moderation.lawsuit")}
                          </Button>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </Card>
          )}

          {(q.strikes.length > 0 || q.terminated.length > 0) && (
            <Card header={<h2>{t("admin.moderation.strikes")}</h2>}>
              <ul>
                {q.strikes.map((s) => (
                  <li key={s.id}>
                    <span className="mkc-mono">{s.publisher_id}</span> · {formatDate(s.at)} ·{" "}
                    {s.active
                      ? t("admin.moderation.strikeUntil", { at: formatDate(s.expires_at) })
                      : t("admin.moderation.strikeEnded")}{" "}
                    {s.active && (
                      <Button
                        size="sm"
                        variant="secondary"
                        onClick={() =>
                          open({
                            path: `${BASE}/strikes/${s.id}/withdraw`,
                            title: t("admin.moderation.withdrawStrikeTitle"),
                            confirm: t("admin.moderation.withdrawStrike"),
                          })
                        }
                      >
                        {t("admin.moderation.withdrawStrike")}
                      </Button>
                    )}
                  </li>
                ))}
                {q.terminated.map((p) => (
                  <li key={p}>
                    <Badge>{t("admin.moderation.terminated")}</Badge>{" "}
                    <span className="mkc-mono">{p}</span>{" "}
                    <Button
                      size="sm"
                      variant="secondary"
                      aria-label={t("admin.moderation.reinstateOne", { id: p })}
                      onClick={() =>
                        open({
                          path: `${BASE}/publishers/${p}/reinstate`,
                          title: t("admin.moderation.reinstateTitle"),
                          confirm: t("admin.moderation.reinstate"),
                        })
                      }
                    >
                      {t("admin.moderation.reinstate")}
                    </Button>
                  </li>
                ))}
              </ul>
            </Card>
          )}

          <EnterNotice onAdded={data.reload} />

          {decidedNotices.length > 0 && (
            <Card header={<h2>{t("admin.moderation.decided")}</h2>}>
              <ul>
                {decidedNotices.map((n) => (
                  <li key={n.id}>
                    {formatDate(n.received_at)} · {n.claimant} · {n.work} ·{" "}
                    <Badge>{t(`admin.moderation.status.${n.status}`)}</Badge>
                    {n.decision && <span className="mkc-muted"> {n.decision}</span>}
                  </li>
                ))}
              </ul>
            </Card>
          )}
        </div>
      )}
      <ReasonDialog
        open={decision !== null}
        title={decision?.title ?? ""}
        confirmLabel={decision?.confirm ?? ""}
        destructive={decision?.destructive}
        onClose={() => setDecision(null)}
        onConfirm={decide}
      >
        {decision?.unfounded && (
          <label>
            <input
              type="checkbox"
              checked={unfounded}
              onChange={(e) => setUnfounded(e.target.checked)}
            />{" "}
            {t("admin.moderation.unfounded")}
          </label>
        )}
      </ReasonDialog>
      {taking && (
        <TakedownDialog
          basis={taking}
          onClose={() => setTaking(null)}
          onDone={() => {
            setTaking(null);
            toast.show({ tone: "success", message: t("admin.moderation.takenDown") });
            data.reload();
          }}
        />
      )}
    </Page>
  );
}

/** datetime-local's value for an ISO time, in the browser's zone. */
function localValue(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/** The takedown: its scope, the item or source, whether it is on air now, and its reason. */
function TakedownDialog({
  basis,
  onClose,
  onDone,
}: {
  basis: Basis;
  onClose: () => void;
  onDone: () => void;
}) {
  const { t } = useI18n();
  const message = useErrorMessage();
  const [scope, setScope] = useState(basis.basis === "own" ? "channel" : "item");
  const [channel, setChannel] = useState(basis.channel ?? "");
  const [publisher, setPublisher] = useState("");
  const [at, setAt] = useState(localValue(basis.at));
  const [itemRef, setItemRef] = useState("");
  const [programme, setProgramme] = useState(basis.programme ?? "");
  const [sourceKind, setSourceKind] = useState("channel");
  const [sourceRef, setSourceRef] = useState("");
  const [airing, setAiring] = useState(false);
  const [until, setUntil] = useState("");
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const needsTarget =
    scope === "publisher"
      ? !!(publisher.trim() || channel.trim())
      : !!channel.trim() || basis.basis !== "own";
  const ready =
    reason.trim() &&
    needsTarget &&
    (scope !== "item" || at || itemRef.trim()) &&
    (scope !== "source" || sourceRef.trim());

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!ready) return;
    setBusy(true);
    setError(null);
    try {
      const body: Record<string, unknown> = { basis: basis.basis, scope, reason: reason.trim() };
      if (basis.basis === "notice") body.notice_id = basis.id;
      if (basis.basis === "report") body.report_id = basis.id;
      if (channel.trim()) body.channel = channel.trim();
      if (scope === "publisher" && publisher.trim()) body.publisher_id = publisher.trim();
      if (scope === "item") {
        if (at) body.at = new Date(at).toISOString();
        if (itemRef.trim()) body.item_ref = itemRef.trim();
        if (programme.trim()) body.programme = programme.trim();
      }
      if (scope === "source") {
        body.source_kind = sourceKind;
        body.source_ref = sourceRef.trim();
      }
      if (airing && (scope === "item" || scope === "source")) {
        body.airing_now = true;
        if (until) body.airing_until = new Date(until).toISOString();
      }
      await api.post(`${BASE}/actions`, body);
      onDone();
    } catch (err) {
      setError(err);
      setBusy(false);
    }
  }

  return (
    <Modal
      open
      onClose={onClose}
      dismissable={false}
      title={t(`admin.moderation.takedownTitle.${basis.basis}`)}
    >
      {error != null && <Banner tone="error">{message(error)}</Banner>}
      <form className="mkc-form" onSubmit={submit} noValidate>
        <Select
          label={t("admin.moderation.scope")}
          value={scope}
          onChange={(e) => setScope(e.target.value)}
          options={SCOPES.map((s) => ({ value: s, label: t(`admin.moderation.scope.${s}`) }))}
        />
        <Input
          label={t("admin.moderation.channel")}
          description={t("admin.moderation.channelHint")}
          value={channel}
          onChange={(e) => setChannel(e.target.value)}
        />
        {scope === "publisher" && (
          <Input
            label={t("admin.moderation.publisher")}
            value={publisher}
            onChange={(e) => setPublisher(e.target.value)}
          />
        )}
        {scope === "item" && (
          <>
            <Input
              label={t("admin.moderation.at")}
              type="datetime-local"
              value={at}
              onChange={(e) => setAt(e.target.value)}
            />
            <Input
              label={t("admin.moderation.itemRef")}
              value={itemRef}
              onChange={(e) => setItemRef(e.target.value)}
            />
            <Input
              label={t("admin.moderation.programme")}
              value={programme}
              onChange={(e) => setProgramme(e.target.value)}
            />
          </>
        )}
        {scope === "source" && (
          <>
            <Select
              label={t("admin.moderation.sourceKind")}
              value={sourceKind}
              onChange={(e) => setSourceKind(e.target.value)}
              options={SOURCE_KINDS.map((k) => ({
                value: k,
                label: t(`admin.moderation.sourceKind.${k}`),
              }))}
            />
            <Input
              label={t("admin.moderation.sourceRef")}
              value={sourceRef}
              onChange={(e) => setSourceRef(e.target.value)}
            />
          </>
        )}
        {(scope === "item" || scope === "source") && (
          <>
            <label>
              <input
                type="checkbox"
                checked={airing}
                onChange={(e) => setAiring(e.target.checked)}
              />{" "}
              {t("admin.moderation.airingNow")}
            </label>
            {airing && !basis.edgeKnown && !channel && (
              <p className="mkc-muted">{t("admin.moderation.edgeUnknown")}</p>
            )}
            {airing && (
              <Input
                label={t("admin.moderation.airingUntil")}
                type="datetime-local"
                value={until}
                onChange={(e) => setUntil(e.target.value)}
              />
            )}
          </>
        )}
        <Textarea
          label={t("admin.reason")}
          required
          rows={2}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
        />
        <div className="mkc-row">
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button type="submit" variant="destructive" loading={busy} disabled={!ready}>
            {t("admin.moderation.takeDown")}
          </Button>
        </div>
      </form>
    </Modal>
  );
}

/** A notice that reached the agent by email or post, entered by staff. */
function EnterNotice({ onAdded }: { onAdded: () => void }) {
  const { t } = useI18n();
  const message = useErrorMessage();
  const toast = useToast();
  const empty = {
    claimant_name: "",
    claimant_email: "",
    claimant_address: "",
    work: "",
    urls: "",
    at: "",
    explanation: "",
    signature: "",
  };
  const [n, setN] = useState(empty);
  const [statements, setStatements] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const ready =
    n.claimant_name.trim() &&
    n.claimant_email.includes("@") &&
    n.work.trim() &&
    n.urls.trim() &&
    n.signature.trim() &&
    statements;

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!ready) return;
    setBusy(true);
    setError(null);
    try {
      await api.post(`${BASE}/notices`, {
        ...n,
        urls: n.urls
          .split(/\s+/)
          .map((u) => u.trim())
          .filter(Boolean),
        at: n.at ? new Date(n.at).toISOString() : undefined,
        good_faith: true,
        accuracy: true,
      });
      toast.show({ tone: "success", message: t("admin.moderation.entered") });
      setN(empty);
      setStatements(false);
      onAdded();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  }

  const field = (key: keyof typeof empty, type = "text") => (
    <Input
      label={t(`admin.moderation.field.${key}`)}
      type={type}
      value={n[key]}
      onChange={(e) => setN({ ...n, [key]: e.target.value })}
    />
  );
  return (
    <Card header={<h2>{t("admin.moderation.enterTitle")}</h2>}>
      {error != null && <Banner tone="error">{message(error)}</Banner>}
      <form className="mkc-form" onSubmit={submit} noValidate>
        {field("claimant_name")}
        {field("claimant_email", "email")}
        {field("claimant_address")}
        {field("work")}
        <Textarea
          label={t("admin.moderation.field.urls")}
          rows={2}
          value={n.urls}
          onChange={(e) => setN({ ...n, urls: e.target.value })}
        />
        {field("at", "datetime-local")}
        {field("explanation")}
        {field("signature")}
        <label>
          <input
            type="checkbox"
            checked={statements}
            onChange={(e) => setStatements(e.target.checked)}
          />{" "}
          {t("admin.moderation.field.statements")}
        </label>
        <Button type="submit" loading={busy} disabled={!ready}>
          {t("admin.moderation.enter")}
        </Button>
      </form>
    </Card>
  );
}
