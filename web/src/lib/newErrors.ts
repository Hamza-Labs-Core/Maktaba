// How many error groups are new (Epic 53 story 53.11): the count on the navigation's Errors
// entry, read for admins at start, every minute, and at once when a group's status changes
// (ERRORS_CHANGED_EVENT, fired by the Errors page). Nothing is shown when it cannot be read.
import { useEffect, useState } from "react";
import { listErrors } from "./logIndex";

export const ERRORS_CHANGED_EVENT = "maktaba:errors-changed";
const EVERY_MS = 60_000;

export function useNewErrors(enabled: boolean): number {
  const [count, setCount] = useState(0);
  useEffect(() => {
    if (!enabled) {
      setCount(0);
      return;
    }
    let alive = true;
    const read = () => {
      Promise.resolve()
        .then(() => listErrors({ status: "new" }))
        .then((g) => alive && setCount(Array.isArray(g) ? g.length : 0))
        .catch(() => {});
    };
    read();
    const id = window.setInterval(read, EVERY_MS);
    window.addEventListener(ERRORS_CHANGED_EVENT, read);
    return () => {
      alive = false;
      window.clearInterval(id);
      window.removeEventListener(ERRORS_CHANGED_EVENT, read);
    };
  }, [enabled]);
  return count;
}
