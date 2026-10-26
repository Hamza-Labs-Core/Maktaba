// /admin/abuse — recent abuse signals and the blocklist, with block / unblock.
import { useState } from "react";
import type { FormEvent } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { Input } from "@ds/components/Input/Input";
import { Select } from "@ds/components/Select/Select";
import { Textarea } from "@ds/components/Textarea/Textarea";
import { useToast } from "@ds/components/Toast/Toast";
import { api } from "../../api";
import { useI18n } from "../../i18n";
import { Banner, LoadError, Loading, Page, useErrorMessage, useLoad } from "../../ui";
import { ReasonDialog } from "./ReasonDialog";

export interface AbuseSignal {
  id: string;
  subject_kind: string;
  subject: string;
  kind: string;
  severity: string | number;
  created_at: string;
}

interface BlockEntry {
  subject_kind: string;
  subject: string;
  reason: string;
  blocked_at: string;
  expires_at?: string;
}

// The blocklist's subject kinds (abuse.Subject.Kind on the API side).
const SUBJECT_KINDS = ["ip", "user", "server"];

export function AdminAbuse() {
  const { t, formatDate } = useI18n();
  const toast = useToast();
  const data = useLoad(
    () => api.get<{ signals: AbuseSignal[]; blocklist: BlockEntry[] }>("/v1/admin/abuse?limit=100"),
    "abuse"
  );
  const [unblock, setUnblock] = useState<BlockEntry | null>(null);

  async function doUnblock(reason: string) {
    if (!unblock) return;
    await api.post("/v1/admin/abuse/unblock", {
      subject_kind: unblock.subject_kind,
      subject: unblock.subject,
      reason,
    });
    setUnblock(null);
    toast.show({ tone: "success", message: t("admin.abuse.unblocked") });
    data.reload();
  }

  if (data.error != null) {
    return (
      <Page title={t("admin.abuse.title")}>
        <LoadError error={data.error} onRetry={data.reload} />
      </Page>
    );
  }

  return (
    <Page title={t("admin.abuse.title")}>
      {!data.data ? (
        <Loading />
      ) : (
        <div className="mkc-stack">
          <BlockForm onBlocked={data.reload} />
          <Card header={<h2>{t("admin.abuse.blocklist")}</h2>}>
            {data.data.blocklist.length === 0 ? (
              <p className="mkc-muted">{t("admin.abuse.noBlocks")}</p>
            ) : (
              <table className="mkc-table">
                <thead>
                  <tr>
                    <th scope="col">{t("admin.abuse.subject")}</th>
                    <th scope="col">{t("admin.reason")}</th>
                    <th scope="col">{t("admin.abuse.blockedAt")}</th>
                    <th scope="col">{t("admin.abuse.expires")}</th>
                    <th scope="col">
                      <span className="mkc-visually-hidden">{t("common.actions")}</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {data.data.blocklist.map((b) => (
                    <tr key={`${b.subject_kind}:${b.subject}`}>
                      <td>
                        <Badge>{b.subject_kind}</Badge>{" "}
                        <span className="mkc-mono">{b.subject}</span>
                      </td>
                      <td>{b.reason}</td>
                      <td>{formatDate(b.blocked_at)}</td>
                      <td>{b.expires_at ? formatDate(b.expires_at) : t("admin.abuse.never")}</td>
                      <td>
                        <Button
                          size="sm"
                          variant="secondary"
                          onClick={() => setUnblock(b)}
                          aria-label={t("admin.abuse.unblockOne", { subject: b.subject })}
                        >
                          {t("admin.abuse.unblock")}
                        </Button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </Card>
          <Card header={<h2>{t("admin.abuse.signals")}</h2>}>
            {data.data.signals.length === 0 ? (
              <p className="mkc-muted">{t("admin.abuse.noSignals")}</p>
            ) : (
              <table className="mkc-table">
                <thead>
                  <tr>
                    <th scope="col">{t("admin.abuse.subject")}</th>
                    <th scope="col">{t("admin.abuse.kind")}</th>
                    <th scope="col">{t("admin.abuse.severity")}</th>
                    <th scope="col">{t("admin.col.created")}</th>
                  </tr>
                </thead>
                <tbody>
                  {data.data.signals.map((s) => (
                    <tr key={s.id}>
                      <td>
                        <Badge>{s.subject_kind}</Badge>{" "}
                        <span className="mkc-mono">{s.subject}</span>
                      </td>
                      <td className="mkc-mono">{s.kind}</td>
                      <td>{String(s.severity)}</td>
                      <td>{formatDate(s.created_at)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </Card>
        </div>
      )}
      <ReasonDialog
        open={unblock !== null}
        title={t("admin.abuse.unblockTitle", { subject: unblock?.subject ?? "" })}
        confirmLabel={t("admin.abuse.unblock")}
        onClose={() => setUnblock(null)}
        onConfirm={doUnblock}
      />
    </Page>
  );
}

function BlockForm({ onBlocked }: { onBlocked: () => void }) {
  const { t } = useI18n();
  const message = useErrorMessage();
  const toast = useToast();
  const [kind, setKind] = useState(SUBJECT_KINDS[0]);
  const [subject, setSubject] = useState("");
  const [reason, setReason] = useState("");
  const [hours, setHours] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const hoursValid = hours === "" || (/^\d+$/.test(hours) && Number(hours) > 0);

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!subject.trim() || !reason.trim() || !hoursValid) return;
    setBusy(true);
    setError(null);
    try {
      const body: Record<string, string | number> = {
        subject_kind: kind,
        subject: subject.trim(),
        reason: reason.trim(),
      };
      if (hours) body.expires_in_hours = Number(hours);
      await api.post("/v1/admin/abuse/block", body);
      toast.show({ tone: "success", message: t("admin.abuse.blocked") });
      setSubject("");
      setReason("");
      setHours("");
      onBlocked();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card header={<h2>{t("admin.abuse.blockTitle")}</h2>}>
      {error != null && <Banner tone="error">{message(error)}</Banner>}
      <form className="mkc-form" onSubmit={submit} noValidate>
        <Select
          label={t("admin.abuse.subjectKind")}
          value={kind}
          onChange={(e) => setKind(e.target.value)}
          options={SUBJECT_KINDS.map((k) => ({ value: k, label: t(`admin.abuse.kind.${k}`) }))}
        />
        <Input
          label={t("admin.abuse.subject")}
          required
          value={subject}
          onChange={(e) => setSubject(e.target.value)}
        />
        <Textarea
          label={t("admin.reason")}
          required
          rows={2}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
        />
        <Input
          label={t("admin.abuse.expiresIn")}
          description={t("admin.abuse.expiresHint")}
          inputMode="numeric"
          value={hours}
          error={hoursValid ? undefined : t("admin.abuse.hoursInvalid")}
          onChange={(e) => setHours(e.target.value)}
        />
        <Button
          type="submit"
          variant="destructive"
          loading={busy}
          disabled={!subject.trim() || !reason.trim() || !hoursValid}
        >
          {t("admin.abuse.block")}
        </Button>
      </form>
    </Card>
  );
}
