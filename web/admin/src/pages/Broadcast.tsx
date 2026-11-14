// /broadcast: the broadcast kill switch (Epic 50 story 50.25 §4). The current revocation list
// with its entries by kind, who added each, when and why; a form to add one and a button to lift
// one (each a fresh MFA check on the API side, and a row in broadcast_revocation_audit); and the
// audit itself. The API is the authority: only a service admin is answered.
import { useState } from "react";
import type { FormEvent } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { Input } from "@ds/components/Input/Input";
import { Select } from "@ds/components/Select/Select";
import { Textarea } from "@ds/components/Textarea/Textarea";
import { useToast } from "@ds/components/Toast/Toast";
import { api } from "../api";
import { useI18n } from "@cloud-shared/i18n";
import { Banner, LoadError, Loading, Page, useErrorMessage, useLoad } from "@cloud-shared/ui";
import { ReasonDialog } from "./ReasonDialog";

export interface Revocation {
  id: string;
  kind: string;
  ref?: string;
  action: string;
  reason: string;
  since: string;
  until?: string;
  note: string;
  added_by: string;
  added_at: string;
  lifted_at?: string;
}

interface AuditRow {
  revocation: string;
  action: string;
  version: number;
  actor: string;
  note: string;
  at: string;
}

interface RevocationsView {
  version: number;
  changed_at: string;
  entries: Revocation[];
  audit: AuditRow[];
  signing: boolean;
}

/** The kinds, and the actions each may take (the API's Entry.Validate). */
export const ACTIONS_BY_KIND: Record<string, string[]> = {
  channel: ["off_air"],
  programme: ["slate"],
  publisher: ["suspend", "terminate", "off_air"],
  home: ["suspend", "terminate", "off_air"],
  global: ["off_air"],
};
const KINDS = Object.keys(ACTIONS_BY_KIND);
const REASONS = ["notice", "report", "incident", "abuse", "repeat_infringer"];

export function AdminBroadcast() {
  const { t, formatDate } = useI18n();
  const toast = useToast();
  const data = useLoad(
    () => api.get<RevocationsView>("/v1/admin/broadcast/revocations"),
    "broadcast-revocations"
  );
  const [lifting, setLifting] = useState<Revocation | null>(null);

  async function doLift(note: string) {
    if (!lifting) return;
    await api.post(`/v1/admin/broadcast/revocations/${lifting.id}/lift`, { note });
    setLifting(null);
    toast.show({ tone: "success", message: t("admin.broadcast.lifted") });
    data.reload();
  }

  if (data.error != null) {
    return (
      <Page title={t("admin.broadcast.title")}>
        <LoadError error={data.error} onRetry={data.reload} />
      </Page>
    );
  }

  const live = (data.data?.entries ?? [])
    .filter((e) => !e.lifted_at)
    .sort((a, b) => KINDS.indexOf(a.kind) - KINDS.indexOf(b.kind));
  const lifted = (data.data?.entries ?? []).filter((e) => e.lifted_at);

  return (
    <Page title={t("admin.broadcast.title")}>
      {!data.data ? (
        <Loading />
      ) : (
        <div className="mkc-stack">
          <p className="mkc-muted">
            {t("admin.broadcast.version", {
              version: data.data.version,
              at: formatDate(data.data.changed_at),
            })}
          </p>
          {!data.data.signing && <Banner tone="warn">{t("admin.broadcast.unsigned")}</Banner>}
          <AddForm onAdded={data.reload} />
          <Card header={<h2>{t("admin.broadcast.current")}</h2>}>
            {live.length === 0 ? (
              <p className="mkc-muted">{t("admin.broadcast.none")}</p>
            ) : (
              <EntryTable entries={live} onLift={setLifting} />
            )}
          </Card>
          {lifted.length > 0 && (
            <Card header={<h2>{t("admin.broadcast.liftedRecently")}</h2>}>
              <EntryTable entries={lifted} />
            </Card>
          )}
          <Card header={<h2>{t("admin.broadcast.audit")}</h2>}>
            {data.data.audit.length === 0 ? (
              <p className="mkc-muted">{t("admin.broadcast.noAudit")}</p>
            ) : (
              <table className="mkc-table">
                <thead>
                  <tr>
                    <th scope="col">{t("admin.col.created")}</th>
                    <th scope="col">{t("admin.broadcast.entry")}</th>
                    <th scope="col">{t("admin.broadcast.change")}</th>
                    <th scope="col">{t("admin.broadcast.listVersion")}</th>
                    <th scope="col">{t("admin.broadcast.by")}</th>
                    <th scope="col">{t("admin.reason")}</th>
                  </tr>
                </thead>
                <tbody>
                  {data.data.audit.map((a, i) => (
                    <tr key={`${a.revocation}-${a.action}-${i}`}>
                      <td>{formatDate(a.at)}</td>
                      <td className="mkc-mono">{a.revocation}</td>
                      <td>{t(`admin.broadcast.change.${a.action}`)}</td>
                      <td>{a.version}</td>
                      <td>{a.actor}</td>
                      <td>{a.note}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </Card>
        </div>
      )}
      <ReasonDialog
        open={lifting !== null}
        title={t("admin.broadcast.liftTitle", { id: lifting?.id ?? "" })}
        body={t("admin.broadcast.liftBody")}
        confirmLabel={t("admin.broadcast.lift")}
        onClose={() => setLifting(null)}
        onConfirm={doLift}
      />
    </Page>
  );
}

function EntryTable({
  entries,
  onLift,
}: {
  entries: Revocation[];
  onLift?: (e: Revocation) => void;
}) {
  const { t, formatDate } = useI18n();
  return (
    <table className="mkc-table">
      <thead>
        <tr>
          <th scope="col">{t("admin.broadcast.kind")}</th>
          <th scope="col">{t("admin.broadcast.ref")}</th>
          <th scope="col">{t("admin.broadcast.action")}</th>
          <th scope="col">{t("admin.reason")}</th>
          <th scope="col">{t("admin.broadcast.by")}</th>
          <th scope="col">{t("admin.col.created")}</th>
          {onLift && (
            <th scope="col">
              <span className="mkc-visually-hidden">{t("common.actions")}</span>
            </th>
          )}
        </tr>
      </thead>
      <tbody>
        {entries.map((e) => (
          <tr key={e.id}>
            <td>
              <Badge>{t(`admin.broadcast.kind.${e.kind}`)}</Badge>
            </td>
            <td className="mkc-mono">{e.ref || "—"}</td>
            <td>
              {t(`admin.broadcast.action.${e.action}`)}
              {e.until && (
                <div className="mkc-muted">
                  {t("admin.broadcast.untilAt", { at: formatDate(e.until) })}
                </div>
              )}
            </td>
            <td>
              {t(`admin.broadcast.reason.${e.reason}`)}
              {e.note && <div className="mkc-muted">{e.note}</div>}
            </td>
            <td>{e.added_by}</td>
            <td>
              {formatDate(e.added_at)}
              {e.lifted_at && (
                <div className="mkc-muted">
                  {t("admin.broadcast.liftedAt", { at: formatDate(e.lifted_at) })}
                </div>
              )}
            </td>
            {onLift && (
              <td>
                <Button
                  size="sm"
                  variant="secondary"
                  onClick={() => onLift(e)}
                  aria-label={t("admin.broadcast.liftOne", { id: e.id })}
                >
                  {t("admin.broadcast.lift")}
                </Button>
              </td>
            )}
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function AddForm({ onAdded }: { onAdded: () => void }) {
  const { t } = useI18n();
  const message = useErrorMessage();
  const toast = useToast();
  const [kind, setKind] = useState("channel");
  const [action, setAction] = useState("off_air");
  const [ref, setRef] = useState("");
  const [reason, setReason] = useState("notice");
  const [until, setUntil] = useState("");
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const needsRef = kind !== "global";
  const needsUntil = kind === "programme";
  const ready = (!needsRef || ref.trim()) && (!needsUntil || until) && note.trim();

  function pickKind(k: string) {
    setKind(k);
    setAction(ACTIONS_BY_KIND[k]![0]!);
  }

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!ready) return;
    setBusy(true);
    setError(null);
    try {
      const body: Record<string, string> = { kind, action, reason, note: note.trim() };
      if (needsRef) body.ref = ref.trim();
      if (needsUntil) body.until = new Date(until).toISOString();
      await api.post("/v1/admin/broadcast/revocations", body);
      toast.show({ tone: "success", message: t("admin.broadcast.added") });
      setRef("");
      setUntil("");
      setNote("");
      onAdded();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card header={<h2>{t("admin.broadcast.addTitle")}</h2>}>
      {error != null && <Banner tone="error">{message(error)}</Banner>}
      <form className="mkc-form" onSubmit={submit} noValidate>
        <Select
          label={t("admin.broadcast.kind")}
          value={kind}
          onChange={(e) => pickKind(e.target.value)}
          options={KINDS.map((k) => ({ value: k, label: t(`admin.broadcast.kind.${k}`) }))}
        />
        {needsRef && (
          <Input
            label={t("admin.broadcast.ref")}
            description={t(`admin.broadcast.refHint.${kind}`)}
            required
            value={ref}
            onChange={(e) => setRef(e.target.value)}
          />
        )}
        <Select
          label={t("admin.broadcast.action")}
          value={action}
          onChange={(e) => setAction(e.target.value)}
          options={ACTIONS_BY_KIND[kind]!.map((a) => ({
            value: a,
            label: t(`admin.broadcast.action.${a}`),
          }))}
        />
        {needsUntil && (
          <Input
            label={t("admin.broadcast.until")}
            type="datetime-local"
            required
            value={until}
            onChange={(e) => setUntil(e.target.value)}
          />
        )}
        <Select
          label={t("admin.reason")}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          options={REASONS.map((r) => ({ value: r, label: t(`admin.broadcast.reason.${r}`) }))}
        />
        <Textarea
          label={t("admin.broadcast.note")}
          required
          rows={2}
          value={note}
          onChange={(e) => setNote(e.target.value)}
        />
        <Button type="submit" variant="destructive" loading={busy} disabled={!ready}>
          {t("admin.broadcast.add")}
        </Button>
      </form>
    </Card>
  );
}
