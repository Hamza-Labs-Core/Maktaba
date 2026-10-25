// Settings → Account (admins): the home's name, which every signed-in user's app
// shows instead of the home's address. Stored as home.name (a runtime setting);
// empty goes back to the computer's name. The API refuses anyone but an admin.
//
//   GET   /api/auth/me                  { home_name, … }  (the name in use)
//   PATCH /api/settings { "home.name": string }
import { useEffect, useState, type FormEvent } from "react";
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { useToast } from "@ds/components/Toast/Toast";
import { api, ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";

export const HOME_NAME = "home.name";
/** The longest name the API takes (settings.MaxHomeName). */
export const MAX_HOME_NAME = 64;

export function HomeNameSettings() {
  const { t } = useI18n();
  const toast = useToast();
  const [saved, setSaved] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);

  /** The name in use (the stored one or the computer's). */
  async function homeName(): Promise<string> {
    const me = await api.get<{ home_name?: string } | undefined>("/api/auth/me");
    return me?.home_name ?? "";
  }

  function show(n: string) {
    setSaved(n);
    setName(n);
  }

  useEffect(() => {
    let alive = true;
    homeName()
      .then((n) => alive && show(n))
      .catch(() => alive && show(""));
    return () => {
      alive = false;
    };
  }, []);

  async function save(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      await api.patch("/api/settings", { [HOME_NAME]: name.trim() });
      // empty is the computer's name again: the home says which
      show(await homeName());
      toast.show({ tone: "success", message: t("settings.homeName.saved") });
    } catch (err) {
      toast.show({
        tone: "error",
        message: err instanceof ApiError ? err.problem.title : t("common.error"),
      });
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="mkt-settings__home-name" onSubmit={save} data-testid="home-name">
      <h2>{t("settings.homeName.title")}</h2>
      <Input
        label={t("settings.homeName.label")}
        description={t("settings.homeName.help")}
        value={name}
        maxLength={MAX_HOME_NAME}
        autoComplete="off"
        disabled={saved === null || busy}
        onChange={(e) => setName(e.currentTarget.value)}
      />
      <Button
        type="submit"
        variant="secondary"
        disabled={saved === null || busy || name.trim() === saved}
      >
        {t("settings.homeName.save")}
      </Button>
    </form>
  );
}
