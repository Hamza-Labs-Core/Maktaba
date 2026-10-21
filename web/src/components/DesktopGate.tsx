// DesktopGate (Epic 36): inside Maktaba.app's bundled pages, ask the
// shell what the window should show — the first-run setup screens, the
// "starting" screen while this Mac's server comes up, or the app as
// before (client mode). Everywhere else (the browser, the local server's
// own web app inside the shell) it renders its children untouched.
import { useEffect, useState, type ReactNode } from "react";
import { isBundledDesktop, tauriSetupApi, type AppState, type SetupApi } from "../lib/desktopSetup";
import { SetupWizard } from "../pages/Setup/SetupWizard";
import { ServerStarting } from "../pages/Setup/ServerStarting";

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
  useEffect(() => {
    if (!bundled) return;
    api
      .appState()
      .then(setState)
      .catch(() => setState("failed"));
  }, [api, bundled]);

  if (state === null) return null; // one quick IPC round trip
  if (state === "failed") return <>{children}</>;
  switch (state.target) {
    case "setup":
      return <SetupWizard api={api} />;
    case "starting":
      return <ServerStarting api={api} />;
    case "remote":
      return null; // the shell is loading that Maktaba in the window
    case "bundled":
      return <>{children}</>;
  }
}
