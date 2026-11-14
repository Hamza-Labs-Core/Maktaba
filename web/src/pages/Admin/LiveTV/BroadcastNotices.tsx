// The Broadcast panel's notices (Epic 50 story 50.24): what Maktaba Cloud told this home about
// its published channels. Each statement of reasons (a programme taken down after a copyright
// notice, the strike warning, the end of broadcasting, a restore), the reports viewers sent about
// its content, the strikes against the limit, and the takedowns the home applied. A takedown
// that can still be answered offers the counter-notice form; the cloud checks every element and
// the API passes its refusal on. Shown only when there is something to show, or the cloud could
// not be reached.
import { useCallback, useEffect, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { Modal } from "@ds/components/Modal/Modal";
import { useI18n } from "../../../lib/i18n";
import {
  broadcastRefusal,
  tvBroadcastApi,
  type CounterNoticeRequest,
  type ModerationStatement,
  type ModerationView,
} from "../../../lib/tvBroadcastApi";

const REPORT_CATEGORIES = ["copyright", "illegal", "abuse", "other"];

export function BroadcastNotices() {
  const { t, formatDate } = useI18n();
  const [view, setView] = useState<ModerationView | null>(null);
  const [countering, setCountering] = useState<ModerationStatement | null>(null);

  const reload = useCallback(async () => {
    try {
      setView(await tvBroadcastApi.moderation());
    } catch {
      setView(null); // the panel works without its notices
    }
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);

  if (!view) return null;
  const rec = view.records ?? {};
  const statements = Array.isArray(rec.statements) ? rec.statements : [];
  const reports = Array.isArray(rec.reports) ? rec.reports : [];
  const takedowns = Array.isArray(view.takedowns) ? view.takedowns : [];
  const strikes = rec.strikes ?? 0;
  const limit = rec.strike_limit ?? 3;
  const empty =
    statements.length === 0 && reports.length === 0 && takedowns.length === 0 && !rec.terminated;
  if (view.cloud !== "unreachable" && empty && strikes === 0) return null;

  const statementText = (s: ModerationStatement) => {
    const programme = String(s.params.programme ?? "");
    switch (s.kind) {
      case "takedown":
        return t("broadcast.takedown.notice", { programme });
      case "restored":
        return t("broadcast.notices.restored", { programme });
      case "strike_warning":
        return t("broadcast.strikes.warning", {
          count: Number(s.params.count ?? strikes),
          limit: Number(s.params.limit ?? limit),
        });
      case "terminated":
        return t("broadcast.notices.terminated");
      default:
        return t("broadcast.notices.report");
    }
  };

  return (
    <section aria-labelledby="broadcast-notices" data-testid="broadcast-notices">
      <h3 id="broadcast-notices">{t("broadcast.notices.title")}</h3>
      {view.cloud === "unreachable" && <p role="status">{t("broadcast.notices.unreachable")}</p>}
      {rec.terminated && <p role="alert">{t("broadcast.notices.terminated")}</p>}
      {!rec.terminated && strikes > 0 && (
        <p role={strikes >= limit - 1 ? "alert" : "status"}>
          {t("broadcast.strikes.warning", { count: strikes, limit })}
        </p>
      )}
      {statements.length > 0 && (
        <ul>
          {statements.map((s) => (
            <li key={s.id}>
              <span>{statementText(s)}</span> <small>{formatDate(s.created_at)}</small>
              {s.kind === "takedown" && s.params.claimant ? (
                <div>
                  {t("broadcast.notices.claimant", { claimant: String(s.params.claimant) })}
                </div>
              ) : null}
              {s.counter_notice && (
                <div>
                  {t("broadcast.notices.counterFiled", {
                    at: formatDate(s.counter_notice.filed_at),
                    after: formatDate(s.counter_notice.restore_after),
                  })}
                </div>
              )}
              {s.can_counter && (
                <Button size="sm" variant="secondary" onClick={() => setCountering(s)}>
                  {t("broadcast.counter.open")}
                </Button>
              )}
            </li>
          ))}
        </ul>
      )}
      {reports.length > 0 && (
        <>
          <h4>{t("broadcast.notices.reports")}</h4>
          <ul>
            {reports.map((r) => (
              <li key={r.id}>
                {t(
                  `broadcast.report.category.${REPORT_CATEGORIES.includes(r.category) ? r.category : "other"}`
                )}
                {r.programme ? ` · ${r.programme}` : ""} <small>{formatDate(r.received_at)}</small>
                {r.text && <div>{r.text}</div>}
              </li>
            ))}
          </ul>
        </>
      )}
      {takedowns.length > 0 && (
        <>
          <h4>{t("broadcast.notices.applied")}</h4>
          <ul>
            {takedowns.map((d) => (
              <li key={d.id}>
                {d.programme || d.source_ref || d.video_id}{" "}
                <small>
                  {d.restored_at
                    ? t("broadcast.notices.restoredAt", { at: formatDate(d.restored_at) })
                    : t("broadcast.notices.appliedAt", { at: formatDate(d.applied_at) })}
                </small>
              </li>
            ))}
          </ul>
        </>
      )}
      {countering && countering.action_id && (
        <CounterNoticeDialog
          actionId={countering.action_id}
          programme={String(countering.params.programme ?? "")}
          onClose={() => setCountering(null)}
          onFiled={() => {
            setCountering(null);
            void reload();
          }}
        />
      )}
    </section>
  );
}

/** The counter-notice: the elements the law requires, each named when missing. */
export function CounterNoticeDialog(props: {
  actionId: string;
  programme: string;
  onClose: () => void;
  onFiled: () => void;
}) {
  const { t } = useI18n();
  const [c, setC] = useState<CounterNoticeRequest>({
    action_id: props.actionId,
    name: "",
    address: "",
    phone: "",
    email: "",
    explanation: "",
    mistake: false,
    consent: false,
    signature: "",
  });
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const ready =
    c.name.trim() &&
    c.address.trim() &&
    c.email.includes("@") &&
    c.explanation.trim() &&
    c.signature.trim() &&
    c.mistake &&
    c.consent;
  const file = async () => {
    setBusy(true);
    setError(null);
    try {
      await tvBroadcastApi.counterNotice(c);
      props.onFiled();
    } catch (e) {
      const r = broadcastRefusal(e);
      setError(r ? t(r.code, r.params) : e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };
  const field = (key: "name" | "address" | "phone" | "email" | "explanation" | "signature") => (
    <Input
      label={t(`broadcast.counter.${key}`)}
      type={key === "email" ? "email" : "text"}
      value={c[key]}
      onChange={(e) => setC({ ...c, [key]: e.target.value })}
    />
  );
  return (
    <Modal open onClose={props.onClose} title={t("broadcast.counter.title")}>
      <p>{t("broadcast.counter.intro", { programme: props.programme })}</p>
      {field("name")}
      {field("address")}
      {field("phone")}
      {field("email")}
      {field("explanation")}
      <label>
        <input
          type="checkbox"
          checked={c.mistake}
          onChange={(e) => setC({ ...c, mistake: e.target.checked })}
        />{" "}
        {t("broadcast.counter.mistake")}
      </label>
      <label>
        <input
          type="checkbox"
          checked={c.consent}
          onChange={(e) => setC({ ...c, consent: e.target.checked })}
        />{" "}
        {t("broadcast.counter.consent")}
      </label>
      {field("signature")}
      {error && <p role="alert">{error}</p>}
      <div>
        <Button variant="secondary" onClick={props.onClose}>
          {t("common.cancel")}
        </Button>
        <Button onClick={() => void file()} disabled={!ready || busy}>
          {t("broadcast.counter.submit")}
        </Button>
      </div>
    </Modal>
  );
}
