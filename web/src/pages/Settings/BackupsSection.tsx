// Settings → Storage & discovery: the backups of the library database. Where
// the dumps go, when the nightly one runs and how many are kept, the dump
// before each update, and how much of the disk they may take. Save sends only
// what changed; the server checks it (422 names the field) and it is used from
// the next restart (the page's restart banner). A new folder whose old one
// holds dumps asks first: move them along, or leave them there (no longer
// listed, pruned or restored from here). Below, the dumps there are, each one
// deletable.
import { useCallback, useEffect, useState } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { Modal } from "@ds/components/Modal/Modal";
import { Select } from "@ds/components/Select/Select";
import { Toggle } from "@ds/components/Choice/Toggle";
import { ApiError } from "../../lib/api";
import { formatBytes } from "../../lib/desktopSetup";
import { useI18n } from "../../lib/i18n";
import { fieldErrors, problemMessage } from "../../lib/libraryAdminApi";
import {
  HOURS,
  hourLabel,
  serverSettingsApi,
  type BackupDump,
  type BackupSettings,
  type BackupsChange,
  type BackupsDoc,
  type MoveDumpsAsk,
} from "../../lib/serverSettingsApi";
import { HelpLink } from "../../help/HelpLink";

type Ready = Extract<BackupsDoc, { available: true }>;
type NumKey = "keep" | "update_keep" | "reserve_percent" | "reserve_min_gb" | "max_share_percent";
type Freq = "1" | "7" | "n";

/** The form: numbers as typed (checked on save), the frequency as a choice. */
interface Form {
  dir: string;
  nightly: boolean;
  pre_update: boolean;
  freq: Freq;
  every_days: string;
  hour: number;
  keep: string;
  update_keep: string;
  reserve_percent: string;
  reserve_min_gb: string;
  max_share_percent: string;
}

/** The number fields, with whether they take whole numbers only. */
const NUMBERS: { key: NumKey; whole: boolean }[] = [
  { key: "keep", whole: true },
  { key: "update_keep", whole: true },
  { key: "reserve_percent", whole: true },
  { key: "reserve_min_gb", whole: true },
  { key: "max_share_percent", whole: true },
];

function toForm(s: BackupSettings): Form {
  const freq: Freq = s.every_days === 1 ? "1" : s.every_days === 7 ? "7" : "n";
  return {
    dir: s.dir,
    nightly: s.nightly,
    pre_update: s.pre_update,
    freq,
    every_days: String(freq === "n" ? s.every_days : 2),
    hour: s.hour,
    keep: String(s.keep),
    update_keep: String(s.update_keep),
    reserve_percent: String(s.reserve_percent),
    reserve_min_gb: String(s.reserve_min_gb),
    max_share_percent: String(s.max_share_percent),
  };
}

/** A typed number, or null when it is not one (or not whole where it must be). */
function num(v: string, whole: boolean): number | null {
  const s = v.trim();
  if (s === "") return null;
  const n = Number(s);
  if (!Number.isFinite(n) || (whole && !Number.isInteger(n))) return null;
  return n;
}

/** What the form changes from the saved settings, and the numbers it cannot read. */
function changes(form: Form, saved: BackupSettings) {
  const body: BackupsChange = {};
  const bad: string[] = [];
  if (form.dir.trim() !== saved.dir) body.dir = form.dir.trim();
  if (form.nightly !== saved.nightly) body.nightly = form.nightly;
  if (form.pre_update !== saved.pre_update) body.pre_update = form.pre_update;
  if (form.hour !== saved.hour) body.hour = form.hour;
  const every = form.freq === "n" ? num(form.every_days, true) : Number(form.freq);
  if (every === null) bad.push("every_days");
  else if (every !== saved.every_days) body.every_days = every;
  for (const { key, whole } of NUMBERS) {
    const n = num(form[key], whole);
    if (n === null) bad.push(key);
    else if (n !== saved[key]) body[key] = n;
  }
  return { body, bad };
}

export function BackupsSection({
  onRestartNeeded,
}: {
  /** told whether saved backup settings wait for a restart (the page's banner) */
  onRestartNeeded?: (needed: boolean) => void;
} = {}) {
  const { t, formatDate } = useI18n();
  const [doc, setDoc] = useState<BackupsDoc | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [form, setForm] = useState<Form | null>(null);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [moveAsk, setMoveAsk] = useState<(MoveDumpsAsk & { body: BackupsChange }) | null>(null);
  const [deleting, setDeleting] = useState<BackupDump | null>(null);
  const [deleteBusy, setDeleteBusy] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const show = useCallback(
    (d: BackupsDoc, resetForm: boolean) => {
      setDoc(d);
      if (d.available) {
        if (resetForm) setForm(toForm(d.saved));
        onRestartNeeded?.(d.restart_needed);
      }
    },
    [onRestartNeeded]
  );

  const load = useCallback(() => {
    setError(null);
    serverSettingsApi
      .backups()
      .then((d) => show(d, true))
      .catch((e) => setError(problemMessage(e, t("common.error"))));
  }, [show, t]);
  useEffect(load, [load]);

  if (error) {
    return (
      <section className="mkt-storage__reach" aria-labelledby="mkt-storage-backups">
        <h3 id="mkt-storage-backups">{t("storage.backups.title")}</h3>
        <p className="mkt-error" role="alert">
          {error}
        </p>
        <Button type="button" variant="secondary" onClick={load}>
          {t("common.retry")}
        </Button>
      </section>
    );
  }
  if (!doc) return <p className="mkt-loading">{t("common.loading")}</p>;
  if (!doc.available || !form) {
    return (
      <section className="mkt-storage__reach" aria-labelledby="mkt-storage-backups">
        <h3 id="mkt-storage-backups">{t("storage.backups.title")}</h3>
        <p>{t("storage.backups.unavailable")}</p>
        {!doc.available && doc.reason && (
          <p className="mkt-muted mkt-libadmin__small">{doc.reason}</p>
        )}
      </section>
    );
  }

  const { body: changed, bad } = changes(form, doc.saved);
  const dirty = Object.keys(changed).length > 0 || bad.length > 0;

  function update(patch: Partial<Form>) {
    setSaved(false);
    setForm((prev) => (prev ? { ...prev, ...patch } : prev));
  }

  async function save(body: BackupsChange) {
    setSaving(true);
    setSaved(false);
    setErrors({});
    setFormError(null);
    try {
      show(await serverSettingsApi.saveBackups(body), true);
      setSaved(true);
    } catch (e) {
      const next: Record<string, string> = {};
      const loose: string[] = [];
      let ask = false;
      for (const fe of fieldErrors(e)) {
        const m = /^backups\.(\w+)$/.exec(fe.field);
        if (m?.[1] === "move_dumps") ask = true;
        else if (m && m[1] in form!) next[m[1]] = fe.message;
        else loose.push(fe.field ? `${fe.field}: ${fe.message}` : fe.message);
      }
      if (ask && e instanceof ApiError) {
        const p = e.problem as unknown as MoveDumpsAsk;
        setMoveAsk({ dumps: p.dumps, dumps_bytes: p.dumps_bytes, from: p.from, body });
      }
      setErrors(next);
      if (loose.length > 0) setFormError(loose.join(" "));
      else if (!ask && Object.keys(next).length === 0)
        setFormError(problemMessage(e, t("common.error")));
    } finally {
      setSaving(false);
    }
  }

  function submit() {
    const { body, bad: unreadable } = changes(form!, (doc as Ready).saved);
    if (unreadable.length > 0) {
      setErrors(Object.fromEntries(unreadable.map((k) => [k, t("storage.backups.notNumber")])));
      return;
    }
    if (Object.keys(body).length > 0) void save(body);
  }

  function answerMove(move: boolean) {
    const ask = moveAsk;
    setMoveAsk(null);
    if (ask) void save({ ...ask.body, move_dumps: move });
  }

  async function deleteDump() {
    if (!deleting) return;
    setDeleteBusy(true);
    setDeleteError(null);
    try {
      show(await serverSettingsApi.deleteDump(deleting.name), false);
      setDeleting(null);
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) {
        // gone already: show the dumps there are now
        setDeleting(null);
        serverSettingsApi
          .backups()
          .then((d) => show(d, false))
          .catch(() => undefined);
      } else if (e instanceof ApiError && e.status === 409) {
        setDeleteError(t("storage.backups.deleteInUse"));
      } else {
        setDeleteError(problemMessage(e, t("common.error")));
      }
    } finally {
      setDeleteBusy(false);
    }
  }

  const numberInput = (key: NumKey | "every_days", label: string, description?: string) => (
    <Input
      label={label}
      description={description}
      type="number"
      inputMode="numeric"
      dir="ltr"
      value={form[key]}
      error={errors[key]}
      onChange={(e) => update({ [key]: e.target.value })}
    />
  );

  return (
    <section className="mkt-storage__reach" aria-labelledby="mkt-storage-backups">
      <h3 id="mkt-storage-backups">{t("storage.backups.title")}</h3>
      <p className="mkt-muted mkt-libadmin__small">
        {t("storage.backups.desc")} <HelpLink to={{ topic: "storage", anchor: "backups" }} />
      </p>

      <form
        className="mkt-form mkt-storage__form"
        aria-label={t("storage.backups.title")}
        onSubmit={(e) => {
          e.preventDefault();
          submit();
        }}
      >
        {formError && (
          <p className="mkt-error" role="alert">
            {formError}
          </p>
        )}

        <Input
          label={t("storage.backups.dir")}
          description={t("storage.backups.dirHelp", { dir: doc.default_dir })}
          value={form.dir}
          dir="ltr"
          spellCheck={false}
          autoComplete="off"
          placeholder={doc.default_dir}
          error={errors.dir}
          onChange={(e) => update({ dir: e.target.value })}
        />
        <p className="mkt-muted mkt-libadmin__small">
          {t("storage.backups.dirNow")}{" "}
          <bdi dir="ltr" className="mkt-libadmin__path">
            {doc.dir}
          </bdi>
        </p>

        <fieldset>
          <legend>{t("storage.backups.schedule")}</legend>
          <Toggle
            label={t("storage.backups.nightly")}
            checked={form.nightly}
            onChange={(e) => update({ nightly: e.target.checked })}
          />
          {errors.nightly && <p className="mkt-error">{errors.nightly}</p>}
          <Select
            label={t("storage.backups.frequency")}
            value={form.freq}
            disabled={!form.nightly}
            onChange={(e) => update({ freq: e.target.value as Freq })}
            options={[
              { value: "1", label: t("storage.backups.daily") },
              { value: "7", label: t("storage.backups.weekly") },
              { value: "n", label: t("storage.backups.everyN") },
            ]}
          />
          {form.freq === "n"
            ? numberInput("every_days", t("storage.backups.everyDays"))
            : errors.every_days && <p className="mkt-error">{errors.every_days}</p>}
          <Select
            label={t("storage.backups.hour")}
            description={t("storage.backups.hourHelp")}
            value={String(form.hour)}
            disabled={!form.nightly}
            dir="ltr"
            error={errors.hour}
            onChange={(e) => update({ hour: Number(e.target.value) })}
            options={HOURS.map((h) => ({ value: String(h), label: hourLabel(h) }))}
          />
          {numberInput("keep", t("storage.backups.keep"), t("storage.backups.keepHelp"))}
        </fieldset>

        <fieldset>
          <legend>{t("storage.backups.updates")}</legend>
          <Toggle
            label={t("storage.backups.preUpdate")}
            checked={form.pre_update}
            onChange={(e) => update({ pre_update: e.target.checked })}
          />
          {errors.pre_update && <p className="mkt-error">{errors.pre_update}</p>}
          {!form.pre_update && (
            <p className="mkt-storage__warn" role="alert">
              {t("storage.backups.preUpdateOff")}
            </p>
          )}
          {numberInput(
            "update_keep",
            t("storage.backups.updateKeep"),
            t("storage.backups.updateKeepHelp", { n: doc.update_keep })
          )}
        </fieldset>

        <details className="mkt-storage__advanced">
          <summary>{t("storage.backups.advanced")}</summary>
          <p className="mkt-muted mkt-libadmin__small">{t("storage.backups.advancedHelp")}</p>
          {numberInput(
            "reserve_percent",
            t("storage.backups.reservePercent"),
            t("storage.backups.default", { n: 5 })
          )}
          {numberInput(
            "reserve_min_gb",
            t("storage.backups.reserveMinGb"),
            t("storage.backups.default", { n: 2 })
          )}
          {numberInput(
            "max_share_percent",
            t("storage.backups.maxShare"),
            t("storage.backups.default", { n: 20 })
          )}
        </details>

        <div className="mkt-storage__actions">
          <Button type="submit" loading={saving} disabled={!dirty || saving}>
            {t("common.save")}
          </Button>
          {dirty && <span className="mkt-muted">{t("storage.saveHint")}</span>}
          {saved && !dirty && <span role="status">{t("storage.backups.saved")}</span>}
        </div>
      </form>

      <h4>{t("storage.backups.dumps")}</h4>
      {doc.disk && (
        <p className="mkt-muted mkt-libadmin__small">
          {t("storage.backups.diskFree")}{" "}
          <bdi dir="ltr" className="mkt-libadmin__path">
            {doc.disk.mount}
          </bdi>{" "}
          <span dir="ltr">
            {t("storage.location.space", {
              free: formatBytes(doc.disk.free_bytes),
              total: formatBytes(doc.disk.total_bytes),
            })}
          </span>
        </p>
      )}
      {doc.dumps.length === 0 ? (
        <p className="mkt-muted">{t("storage.backups.none")}</p>
      ) : (
        <>
          <div className="mkt-acl__scroll">
            <table className="mkt-table" aria-label={t("storage.backups.dumps")}>
              <thead>
                <tr>
                  <th>{t("storage.backups.name")}</th>
                  <th>{t("storage.backups.kind")}</th>
                  <th>{t("storage.location.size")}</th>
                  <th>{t("storage.backups.at")}</th>
                  <th>{t("storage.location.disk")}</th>
                  <th aria-label={t("storage.backups.actions")} />
                </tr>
              </thead>
              <tbody>
                {doc.dumps.map((d) => (
                  <tr key={d.name}>
                    <td>
                      <bdi dir="ltr" className="mkt-libadmin__path">
                        {d.name}
                      </bdi>
                    </td>
                    <td>
                      <Badge tone={d.kind === "failed" ? "error" : "neutral"}>
                        {t(`storage.backups.kind.${d.kind}`)}
                      </Badge>
                    </td>
                    <td dir="ltr">{formatBytes(d.bytes)}</td>
                    <td>{formatDate(d.at)}</td>
                    <td>
                      {doc.disk ? (
                        <bdi dir="ltr" className="mkt-libadmin__path">
                          {doc.disk.mount}
                        </bdi>
                      ) : (
                        "—"
                      )}
                    </td>
                    <td>
                      <Button
                        type="button"
                        size="sm"
                        variant="ghost"
                        aria-label={t("storage.backups.deleteOne", { name: d.name })}
                        onClick={() => {
                          setDeleteError(null);
                          setDeleting(d);
                        }}
                      >
                        {t("common.delete")}
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <p className="mkt-muted mkt-libadmin__small">
            {t("storage.backups.total")} <span dir="ltr">{formatBytes(doc.dumps_bytes)}</span>
          </p>
        </>
      )}

      <Modal
        open={moveAsk !== null}
        onClose={() => setMoveAsk(null)}
        title={t("storage.backups.moveTitle")}
        footer={
          <>
            <Button type="button" variant="ghost" onClick={() => setMoveAsk(null)}>
              {t("common.cancel")}
            </Button>
            <Button type="button" variant="secondary" onClick={() => answerMove(false)}>
              {t("storage.backups.leaveDumps")}
            </Button>
            <Button type="button" onClick={() => answerMove(true)}>
              {t("storage.backups.moveDumps")}
            </Button>
          </>
        }
      >
        <p>
          {t("storage.backups.moveBody", {
            n: moveAsk?.dumps ?? 0,
            size: formatBytes(moveAsk?.dumps_bytes ?? 0),
            from: moveAsk?.from || doc.dir,
          })}
        </p>
        <p className="mkt-muted">{t("storage.backups.leaveNote")}</p>
      </Modal>

      <Modal
        open={deleting !== null}
        onClose={() => setDeleting(null)}
        title={t("storage.backups.deleteTitle")}
        footer={
          <>
            <Button type="button" variant="ghost" onClick={() => setDeleting(null)}>
              {t("common.cancel")}
            </Button>
            <Button
              type="button"
              variant="destructive"
              loading={deleteBusy}
              onClick={() => void deleteDump()}
            >
              {t("common.delete")}
            </Button>
          </>
        }
      >
        <p>{t("storage.backups.deleteBody", { name: deleting?.name ?? "" })}</p>
        {deleteError && (
          <p className="mkt-error" role="alert">
            {deleteError}
          </p>
        )}
      </Modal>
    </section>
  );
}
