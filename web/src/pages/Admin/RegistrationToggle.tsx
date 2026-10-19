// Admin → Users: open self-service registration (auth.open_registration, a
// runtime setting). Off (the default): only an administrator creates accounts
// and the Register page says registration is closed.
//
//   GET   /api/settings                       { "auth.open_registration": bool, … }
//   PATCH /api/settings { "auth.open_registration": bool }
import { useEffect, useState } from "react";
import { Checkbox } from "@ds/components/Choice/Checkbox";
import { useToast } from "@ds/components/Toast/Toast";
import { api, ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";

export const OPEN_REGISTRATION = "auth.open_registration";

/** The stored value is a JSON bool (or, from older rows, the text "true"). */
export function isOpen(v: unknown): boolean {
  return v === true || (typeof v === "string" && v.trim().toLowerCase() === "true");
}

export function RegistrationToggle() {
  const { t } = useI18n();
  const toast = useToast();
  const [open, setOpen] = useState<boolean | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let alive = true;
    api
      .get<Record<string, unknown>>("/api/settings")
      .then((s) => alive && setOpen(isOpen(s[OPEN_REGISTRATION])))
      .catch(() => alive && setOpen(false));
    return () => {
      alive = false;
    };
  }, []);

  async function change(next: boolean) {
    setBusy(true);
    try {
      const s = await api.patch<Record<string, unknown>>("/api/settings", { [OPEN_REGISTRATION]: next });
      setOpen(isOpen(s?.[OPEN_REGISTRATION] ?? next));
      toast.show({
        tone: "success",
        message: t(next ? "admin.users.registration.opened" : "admin.users.registration.closed"),
      });
    } catch (e) {
      toast.show({
        tone: "error",
        message: e instanceof ApiError ? e.problem.title : t("common.error"),
      });
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="mkt-admin__registration" data-testid="registration-toggle">
      <Checkbox
        label={t("admin.users.registration.label")}
        checked={open === true}
        disabled={open === null || busy}
        onChange={(e) => void change(e.target.checked)}
      />
      <p className="mkt-muted">{t("admin.users.registration.help")}</p>
    </div>
  );
}
