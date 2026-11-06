// The notice the app shows while something is wrong (Epic 53 story 53.06: the status
// strip): the browser has no network (pages already loaded stay readable, and a page that
// failed to load tries again once the network is back: LoadError), or a part of the home
// is down, restarting or in safe mode.
import { StatusStrip } from "./StatusStrip";

export function OfflineBanner() {
  return <StatusStrip />;
}
