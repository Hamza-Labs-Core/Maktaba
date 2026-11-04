// The order of a page's reads and changes. A read that began before a change
// was answered before it (its answer still has the removed row, the old
// toggle), and an answer that arrives after a newer one is older than what is
// on screen: neither may be applied, or the old state comes back on screen
// while the home has the new one. lib/usePageData does this for the pages it
// reads; a page that reads on its own (a poll, a stream, Load more) uses this.
import { useRef } from "react";

export interface ReadOrder {
  /** A read begins: its ticket, for `fresh` once it is answered. */
  begin(): number;
  /**
   * The read with `ticket` was answered: true when it may be applied, that is
   * it began after every change and is no older than any answer applied. Then
   * reads that began before it are older and are dropped.
   */
  fresh(ticket: number): boolean;
  /** A change is on screen or saved: every read under way is older than it. */
  changed(): void;
  /**
   * Runs `save` (a removal, a toggle): no read is applied while it is under
   * way, and every read that began before it ends is older than it.
   */
  saving<T>(save: () => Promise<T>): Promise<T>;
}

export function readOrder(): ReadOrder {
  let started = 0;
  // the oldest ticket that may still be applied
  let floor = 0;
  let pending = 0;
  return {
    begin: () => ++started,
    fresh(ticket) {
      if (pending > 0 || ticket < floor) return false;
      floor = ticket;
      return true;
    },
    changed() {
      floor = started + 1;
    },
    async saving(save) {
      pending += 1;
      try {
        return await save();
      } finally {
        pending -= 1;
        floor = started + 1;
      }
    },
  };
}

/** The page's own `ReadOrder`, kept for as long as it is shown. */
export function useReadOrder(): ReadOrder {
  const order = useRef<ReadOrder | null>(null);
  order.current ??= readOrder();
  return order.current;
}
