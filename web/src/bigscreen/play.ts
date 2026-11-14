// Epic 58 story 58.08 — playing from the big screen on the server queue
// (Epic 56 story 56.01): every Play, Shuffle and Play/Pause on a card makes a
// queue from its source and opens the player on the queue's first video.
import { useCallback } from "react";
import { playSource, type QueueSourceRef } from "../lib/playQueue";
import { bigPath, useBigNav } from "./nav";

export interface PlayOpts {
  shuffle?: boolean;
  /** where the first video starts (a started video resumes) */
  at?: number | null;
  /** a series' id, named in the player's URL for its Next lesson */
  series?: string | null;
  startVideoId?: string;
}

/**
 * Plays a source: true when the player opened, false when the source had
 * nothing to play. A failure leaves the screen as it was (the key can be
 * pressed again).
 */
export function usePlay() {
  const { go } = useBigNav();
  return useCallback(
    async (source: QueueSourceRef, opts: PlayOpts = {}): Promise<boolean> => {
      try {
        const first = await playSource(source, {
          shuffle: opts.shuffle,
          startVideoId: opts.startVideoId,
        });
        if (!first) return false;
        const at = first === (opts.startVideoId ?? source.id) ? (opts.at ?? 0) : 0;
        go(bigPath.watch(first, { t: at ?? 0, series: opts.series ?? null }));
        return true;
      } catch {
        return false;
      }
    },
    [go]
  );
}
