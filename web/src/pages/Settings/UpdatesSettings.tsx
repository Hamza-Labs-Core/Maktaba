// Settings -> About -> Automatic updates (Epic 35, story 35.3): the home installs new
// signed releases by itself in the quiet window. The card shows the next planned update
// and edits the main rules.updates settings (the full list is also on the rules page):
// install automatically, channel, quiet window, release age, pin. The window and the pin
// are checked here first (the API refuses them too: 400 bad_window / pin_below_installed).
import { useCallback, useEffect, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { Select } from "@ds/components/Select/Select";
import { Toggle } from "@ds/components/Choice/Toggle";
import { useToast } from "@ds/components/Toast/Toast";
import { ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import {
  pinProblem,
  updatesApi,
  validWindow,
  type UpdatesOverview,
  type UpdatesPlan,
} from "../../lib/updatesApi";

interface Draft {
  auto_install: boolean;
  channel: "stable" | "beta";
  window: string;
  min_release_age_hours: string;
  pinned_version: string;
}

function draftOf(o: UpdatesOverview): Draft {
  return {
    auto_install: o.policy.auto_install,
    channel: o.policy.channel,
    window: o.policy.window ?? "",
    min_release_age_hours: String(o.policy.min_release_age_hours),
    pinned_version: o.policy.pinned_version ?? "",
  };
}

export function UpdatesSettings() {
  const { t } = useI18n();
  const toast = useToast();
  const [ov, setOv] = useState<UpdatesOverview | null>(null);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [busy, setBusy] = useState(false);
  const [unavailable, setUnavailable] = useState(false);

  const load = useCallback(async () => {
    try {
      const o = await updatesApi.overview();
      setOv(o);
      setDraft(draftOf(o));
    } catch {
      setUnavailable(true);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  if (unavailable || !ov || !draft) return null;

  const installed = ov.watermark || ov.current_version;
  const windowError = validWindow(draft.window) ? "" : t("updates.settings.badWindow");
  const pin = pinProblem(draft.pinned_version, installed);
  const pinError =
    pin === "bad_pin"
      ? t("updates.settings.badPin")
      : pin === "pin_below_installed"
        ? t("updates.settings.pinBelow", { version: installed })
        : "";
  const age = Number(draft.min_release_age_hours);
  const ageError =
    draft.min_release_age_hours.trim() === "" || !Number.isInteger(age) || age < 0
      ? t("updates.settings.badAge")
      : "";
  const invalid = Boolean(windowError || pinError || ageError);

  const save = async () => {
    if (invalid) return;
    setBusy(true);
    try {
      await updatesApi.saveSettings({
        auto_install: draft.auto_install,
        channel: draft.channel,
        window: draft.window.trim(),
        min_release_age_hours: age,
        pinned_version: draft.pinned_version.trim(),
      });
      toast.show({ tone: "success", message: t("updates.settings.saved") });
      await load();
    } catch (e) {
      const msg = e instanceof ApiError ? e.message : t("common.error");
      toast.show({ tone: "error", message: msg });
    } finally {
      setBusy(false);
    }
  };

  // plan null or missing: nothing planned (it must not take the About tab down)
  const plan: Partial<UpdatesPlan> = ov.plan ?? {};
  const target = plan.target;
  let next = t("updates.plan.none");
  if (target && plan.held) next = t("updates.plan.held", { version: target.version });
  else if (target && plan.next_run_at)
    next = t("updates.plan.at", {
      version: target.version,
      at: new Date(plan.next_run_at).toLocaleString(),
    });
  else if (target) {
    const key = `updates.plan.waiting.${plan.waiting ?? "auto_install_off"}`;
    next = t(key, { version: target.version });
    // a reason this build has no text for: say the version is there, not the raw key
    if (next === key)
      next = t("updates.plan.waiting.automatic_installs_not_built", { version: target.version });
  }

  return (
    <section className="mkt-about__updates" aria-labelledby="updates-settings-title" id="updates">
      <h2 id="updates-settings-title">{t("updates.settings.title")}</h2>
      <p className="mkt-muted" data-testid="updates-next">
        {next}
      </p>
      {!ov.auto_install_available && (
        <p className="mkt-muted" data-testid="updates-phase-a">
          {t("updates.settings.notBuilt")}
        </p>
      )}
      {ov.check_stale && (
        <p className="mkt-error" role="alert">
          {t("updates.settings.stale")}
        </p>
      )}
      <Toggle
        label={t("updates.settings.autoInstall")}
        checked={draft.auto_install}
        onChange={(e) => setDraft({ ...draft, auto_install: e.currentTarget.checked })}
      />
      <Select
        label={t("updates.settings.channel")}
        value={draft.channel}
        onChange={(e) => setDraft({ ...draft, channel: e.currentTarget.value as Draft["channel"] })}
        options={[
          { value: "stable", label: t("update.channel.stable") },
          { value: "beta", label: t("update.channel.beta") },
        ]}
      />
      <Input
        label={t("updates.settings.window", { tz: ov.time_zone })}
        value={draft.window}
        error={windowError || undefined}
        onChange={(e) => setDraft({ ...draft, window: e.currentTarget.value })}
      />
      <Input
        label={t("updates.settings.age")}
        inputMode="numeric"
        value={draft.min_release_age_hours}
        error={ageError || undefined}
        onChange={(e) => setDraft({ ...draft, min_release_age_hours: e.currentTarget.value })}
      />
      <Input
        label={t("updates.settings.pin")}
        placeholder="1.5.9"
        value={draft.pinned_version}
        error={pinError || undefined}
        onChange={(e) => setDraft({ ...draft, pinned_version: e.currentTarget.value })}
      />
      <div className="mkt-about__actions">
        <Button variant="primary" disabled={busy || invalid} onClick={save}>
          {t("updates.settings.save")}
        </Button>
      </div>
    </section>
  );
}
