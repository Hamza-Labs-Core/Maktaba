// Epic 58 story 58.08 — which build the big screen runs in.
//
//   desktop: /big in the web app and the desktop apps (the default)
//   tv:      /tv/, the TV build a Samsung or LG launcher (story 58.09) loads
//
// The TV build leaves out what a TV has no use for: leaving big screen (there
// is nothing to go back to) and the desktop's Start in big screen. Its Back on
// Home asks Leave Maktaba? and then hands leaving to the launcher.
import { createContext, useContext } from "react";

export type BigMode = "desktop" | "tv";

export const BigModeContext = createContext<BigMode>("desktop");

export function useBigMode(): BigMode {
  return useContext(BigModeContext);
}
