// Registered devices table (Story 7.22 / 12.10 device surface).
//
// Real contracts:
//   GET    /api/devices             { items: [redactedDevice] }
//   POST   /api/devices/register    { platform, token, bundle_id, categories?, locale?, … }
//   PATCH  /api/devices/{id}        { categories?, locale? }
//   DELETE /api/devices/{id}        (soft-revoke)
//
// NOTE: GET /api/devices and GET /api/me/devices hit the SAME handler,
// both scoped to the signed-in principal (WHERE user_id = current). There
// is no cross-user "all devices" query, so this admin view is honestly
// account-scoped — surfaced via the note copy. The push token is redacted
// at the response boundary; PATCH can only change categories/locale (no
// device name field exists).
import { useEffect, useRef, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Modal } from "@ds/components/Modal/Modal";
import { Input } from "@ds/components/Input/Input";
import { Select } from "@ds/components/Select/Select";
import { Checkbox } from "@ds/components/Choice/Checkbox";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { useToast } from "@ds/components/Toast/Toast";
import { api, ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import { AdminGate } from "../../components/AdminGate";
import { TitleRow } from "../../help/HelpLink";

export interface Device {
  id: string;
  user_id: string;
  platform: string;
  bundle_id: string;
  app_version?: string;
  os_version?: string;
  locale?: string;
  categories?: string[];
  registered_at: string;
  last_seen_at: string;
}

export const CATEGORIES = ["job", "library", "subscription", "system"] as const;

export function CloudDevices() {
  return (
    <AdminGate>
      <DevicesInner />
    </AdminGate>
  );
}

function DevicesInner() {
  const { t, formatDate } = useI18n();
  const toast = useToast();
  const [devices, setDevices] = useState<Device[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [registerOpen, setRegisterOpen] = useState(false);
  const [editing, setEditing] = useState<Device | null>(null);
  const [deleting, setDeleting] = useState<Device | null>(null);
  const [removing, setRemoving] = useState(false);

  function load() {
    setError(null);
    setDevices(null);
    api
      .get<{ items?: Device[] | null }>("/api/devices")
      .then((r) => setDevices(Array.isArray(r?.items) ? r.items : []))
      .catch((e) => {
        setError(e instanceof ApiError ? e.problem.detail || e.problem.title : t("common.error"));
        setDevices([]);
      });
  }

  useEffect(load, []);

  async function remove(d: Device) {
    if (removing) return;
    setRemoving(true);
    try {
      await api.delete(`/api/devices/${encodeURIComponent(d.id)}`);
      toast.show({ tone: "success", message: t("devices.removed") });
      setDeleting(null);
      load();
    } catch (e) {
      toast.show({
        tone: "error",
        message: e instanceof ApiError ? e.problem.detail || e.problem.title : t("common.error"),
      });
    } finally {
      setRemoving(false);
    }
  }

  const dateText = (v: unknown) => (typeof v === "string" && v ? formatDate(v) : "—");

  return (
    <section className="mkt-page mkt-devices">
      <header className="mkt-page__header">
        <TitleRow>
          <h1>{t("devices.adminTitle")}</h1>
        </TitleRow>
        <Button onClick={() => setRegisterOpen(true)}>{t("devices.register")}</Button>
      </header>
      <p className="mkt-muted mkt-admin__note">{t("devices.note")}</p>

      {error ? (
        <ErrorState
          kind="server"
          title={t("common.error")}
          description={error}
          action={
            <Button variant="secondary" onClick={load}>
              {t("common.retry")}
            </Button>
          }
        />
      ) : devices === null ? (
        <p className="mkt-loading">{t("common.loading")}</p>
      ) : devices.length === 0 ? (
        <EmptyState title={t("devices.empty.title")} description={t("devices.empty.desc")} />
      ) : (
        <table className="mkt-table" aria-label={t("devices.adminTitle")}>
          <thead>
            <tr>
              <th>{t("devices.col.platform")}</th>
              <th>{t("devices.col.bundle")}</th>
              <th>{t("devices.col.appVersion")}</th>
              <th>{t("devices.col.lastSeen")}</th>
              <th>{t("devices.col.registered")}</th>
              <th>{t("common.actions")}</th>
            </tr>
          </thead>
          <tbody>
            {devices.map((d) => (
              <tr key={d.id}>
                <td>
                  {platformIcon(d.platform)} {platformLabel(t, d.platform)}
                </td>
                <td className="mkt-mono mkt-truncate">{d.bundle_id}</td>
                <td>{d.app_version ?? "—"}</td>
                <td>{dateText(d.last_seen_at)}</td>
                <td>{dateText(d.registered_at)}</td>
                <td className="mkt-row-actions">
                  <Button size="sm" variant="ghost" onClick={() => setEditing(d)}>
                    {t("common.edit")}
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => setDeleting(d)}>
                    {t("devices.remove")}
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {registerOpen && (
        <RegisterModal
          onClose={() => setRegisterOpen(false)}
          onDone={() => {
            setRegisterOpen(false);
            load();
          }}
        />
      )}
      {editing && (
        <EditModal
          device={editing}
          onClose={() => setEditing(null)}
          onDone={() => {
            setEditing(null);
            load();
          }}
        />
      )}
      {deleting && (
        <Modal
          open
          onClose={() => setDeleting(null)}
          dismissable={false}
          title={t("devices.remove.title")}
          footer={
            <>
              <Button variant="ghost" onClick={() => setDeleting(null)}>
                {t("common.cancel")}
              </Button>
              <Button variant="destructive" loading={removing} onClick={() => remove(deleting)}>
                {t("devices.remove")}
              </Button>
            </>
          }
        >
          <p>{t("devices.remove.confirm")}</p>
        </Modal>
      )}
    </section>
  );
}

// platformLabel names a platform; an unknown one shows as sent, not as
// a raw i18n key.
function platformLabel(t: (k: string) => string, platform: string): string {
  const key = `devices.platform.${platform}`;
  const v = t(key);
  return v === key ? String(platform ?? "") : v;
}

export function platformIcon(platform: string): string {
  switch (platform) {
    case "ios":
      return "";
    case "android":
      return "🤖";
    case "web":
      return "🌐";
    default:
      return "📱";
  }
}

function CategoryPicker({
  value,
  onChange,
  t,
}: {
  value: string[];
  onChange: (next: string[]) => void;
  t: (k: string) => string;
}) {
  return (
    <fieldset className="mkt-field">
      <legend className="mkt-field__label">{t("devices.field.categories")}</legend>
      {CATEGORIES.map((c) => (
        <Checkbox
          key={c}
          label={t(`devices.category.${c}`)}
          checked={value.includes(c)}
          onChange={(e) =>
            onChange(e.target.checked ? [...value, c] : value.filter((x) => x !== c))
          }
        />
      ))}
    </fieldset>
  );
}

function RegisterModal({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const { t } = useI18n();
  const toast = useToast();
  const [platform, setPlatform] = useState("web");
  const [token, setToken] = useState("");
  const [bundleId, setBundleId] = useState("");
  const [categories, setCategories] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  // guards Enter-key submits too (the button's busy state only covers clicks)
  const inflight = useRef(false);

  async function submit() {
    if (inflight.current) return;
    inflight.current = true;
    setBusy(true);
    try {
      await api.post("/api/devices/register", {
        platform,
        token: token.trim(),
        bundle_id: bundleId.trim(),
        categories,
      });
      toast.show({ tone: "success", message: t("devices.registered") });
      onDone();
    } catch (e) {
      toast.show({
        tone: "error",
        message: e instanceof ApiError ? e.problem.detail || e.problem.title : t("common.error"),
      });
    } finally {
      inflight.current = false;
      setBusy(false);
    }
  }

  const valid = token.trim() !== "" && bundleId.trim() !== "";

  return (
    <Modal
      open
      onClose={onClose}
      title={t("devices.register.title")}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button onClick={submit} loading={busy} disabled={!valid}>
            {t("devices.register")}
          </Button>
        </>
      }
    >
      <form
        className="mkt-form"
        onSubmit={(e) => {
          e.preventDefault();
          if (valid) void submit();
        }}
      >
        <Select
          label={t("devices.field.platform")}
          value={platform}
          onChange={(e) => setPlatform(e.target.value)}
          options={[
            { value: "web", label: t("devices.platform.web") },
            { value: "ios", label: t("devices.platform.ios") },
            { value: "android", label: t("devices.platform.android") },
          ]}
        />
        <Input
          label={t("devices.field.token")}
          value={token}
          onChange={(e) => setToken(e.target.value)}
          className="mkt-mono"
          required
        />
        <Input
          label={t("devices.field.bundle")}
          value={bundleId}
          onChange={(e) => setBundleId(e.target.value)}
          className="mkt-mono"
          required
        />
        <CategoryPicker value={categories} onChange={setCategories} t={t} />
        {/* two text fields and no submit button block Enter-to-submit */}
        <button type="submit" hidden tabIndex={-1} />
      </form>
    </Modal>
  );
}

function EditModal({
  device,
  onClose,
  onDone,
}: {
  device: Device;
  onClose: () => void;
  onDone: () => void;
}) {
  const { t } = useI18n();
  const toast = useToast();
  const [categories, setCategories] = useState<string[]>(device.categories ?? []);
  const [locale, setLocale] = useState(device.locale ?? "");
  const [busy, setBusy] = useState(false);

  async function submit() {
    setBusy(true);
    try {
      await api.patch(`/api/devices/${encodeURIComponent(device.id)}`, {
        categories,
        // "" clears the locale on the server (it stores NULL for blank)
        locale: locale.trim(),
      });
      toast.show({ tone: "success", message: t("devices.updated") });
      onDone();
    } catch (e) {
      toast.show({
        tone: "error",
        message: e instanceof ApiError ? e.problem.detail || e.problem.title : t("common.error"),
      });
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      open
      onClose={onClose}
      title={t("devices.edit.title")}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button onClick={submit} loading={busy}>
            {t("common.save")}
          </Button>
        </>
      }
    >
      <form
        className="mkt-form"
        onSubmit={(e) => {
          e.preventDefault();
          void submit();
        }}
      >
        <CategoryPicker value={categories} onChange={setCategories} t={t} />
        <Input
          label={t("devices.field.locale")}
          value={locale}
          onChange={(e) => setLocale(e.target.value)}
        />
      </form>
    </Modal>
  );
}
