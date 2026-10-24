// DesktopGate (Epic 36): inside Maktaba.app's bundled pages, ask the
// shell what the window should show — the first-run setup screens, the
// "starting" screen while this Mac's server comes up, or the app as
// before (client mode). Everywhere else (the browser, the local server's
// own web app inside the shell) it renders its children untouched.
import { useEffect, useState, type ReactNode } from "react";
import { useI18n } from "../lib/i18n";
import { isBundledDesktop, tauriSetupApi, type AppState, type SetupApi } from "../lib/desktopSetup";
import { SetupWizard } from "../pages/Setup/SetupWizard";
import { ServerStarting } from "../pages/Setup/ServerStarting";

interface StatusStep {
  configured: boolean;
  web_url?: string;
  supervisor_url?: string;
}

/** The `status` step: when this Mac is already configured, finish again. */
async function adoptConfigured(api: SetupApi): Promise<string | null> {
  const st = await api.runStep<StatusStep>("status", {});
  if (!st.configured || !st.web_url) return null;
  await api.finishSetup(st.web_url, st.supervisor_url);
  return st.web_url;
}

/** The shell's error, whatever shape it came in (a command's string, an Error, a step
 * error with a message). */
function errorText(e: unknown): string {
  if (typeof e === "string") return e;
  if (e instanceof Error) return e.message;
  if (e && typeof e === "object" && typeof (e as { message?: unknown }).message === "string") {
    return (e as { message: string }).message;
  }
  return String(e);
}

/** How the shell words macOS refusing the server's login item from an app that is not
 * code-signed (update-attempts.md, login_item_unsigned). */
const UNSIGNED = "login_item_unsigned: ";

/** Why this computer's set-up server could not be started again: the setup screens follow,
 * but the reason is said (an unsigned app's login item showed none before). */
function AdoptError({ text }: { text: string }) {
  const { t } = useI18n();
  const unsigned = text.startsWith(UNSIGNED);
  return (
    <div className="mkt-error" role="alert" data-testid="desktop-adopt-error">
      <p>{t(unsigned ? "setup.adopt.unsigned" : "setup.adopt.failed")}</p>
      <p className="mkt-muted" dir="ltr">
        {unsigned ? text.slice(UNSIGNED.length) : text}
      </p>
    </div>
  );
}

export function DesktopGate({
  children,
  api = tauriSetupApi,
  bundled = isBundledDesktop(),
}: {
  children: ReactNode;
  api?: SetupApi;
  /** for tests: pretend to run on the app's bundled pages */
  bundled?: boolean;
}) {
  const [state, setState] = useState<AppState | null | "failed">(bundled ? null : "failed");
  const [adoptError, setAdoptError] = useState<string | null>(null);
  useEffect(() => {
    if (!bundled) return;
    api
      .appState()
      .then(async (st) => {
        if (st.target === "setup" && st.server_available && !st.reset) {
          // the app forgot its mode (a reset config dir) but this Mac's
          // server is set up: register it again instead of a second setup
          const adopted = await adoptConfigured(api).catch((e: unknown) => {
            setAdoptError(errorText(e));
            return null;
          });
          if (adopted) return setState({ ...st, target: "starting", url: adopted });
        }
        setState(st);
      })
      .catch(() => setState("failed"));
  }, [api, bundled]);

  if (state === null) return null; // the shell answers (plus the status step on first run)
  if (state === "failed") return <>{children}</>;
  switch (state.target) {
    case "setup":
      return (
        <>
          {adoptError && <AdoptError text={adoptError} />}
          <SetupWizard api={api} />
        </>
      );
    case "starting":
      return <ServerStarting api={api} />;
    case "remote":
      return null; // the shell is loading that Maktaba in the window
    case "bundled":
      return <>{children}</>;
  }
}
