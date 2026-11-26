// Subtitles clear of the picture's bottom edge on the big screen: a TV may crop it, and the
// browser draws a cue's last line against it, cutting Arabic's descenders. Only a cue the
// author left to the player (`line` "auto", horizontal, in no region) is moved: one that says
// where it goes (at the top, over burned-in text) stays there.
//
// A snapped line counts from the bottom (-1 is the last), and the cue's first line is put there,
// so a cue of n lines goes to -(n + 1): its last line one line up. Cues on screen together keep
// the browser's own stacking.

/** A line's letters are this much of the video's height (the browser's 5%). */
const CUE_EM = 0.05;

/** A letter's width in ems: a capital is wide, a Chinese, Japanese or Korean one a whole em. */
function letterEm(c: string): number {
  if (c >= "\u2e80" && c <= "\uffdc") return c >= "\ufb50" && c <= "\ufefc" ? 0.5 : 1;
  return c !== c.toLowerCase() ? 0.7 : 0.5;
}

type CueLike = Pick<VTTCue, "text" | "vertical" | "size" | "snapToLines"> & {
  line: number | "auto";
  region?: unknown;
};

/** How wide `line` shows, in ems: what is inside <...> is the cue's markup (a voice, a colour). */
function shown(line: string): number {
  let em = 0;
  let markup = false;
  for (const c of line) {
    if (c === "<") markup = true;
    else if (c === ">") markup = false;
    else if (!markup) em += letterEm(c);
  }
  return em;
}

/**
 * How many lines `text` takes in a picture `aspect` wide for its height, at `scale`, in a cue
 * `size` per cent of the width.
 */
export function cueLines(text: string, aspect: number, scale: number, size = 100): number {
  const room = Math.max(1, (aspect * (size / 100)) / (CUE_EM * scale));
  return text.split("\n").reduce((n, line) => n + Math.max(1, Math.ceil(shown(line) / room)), 0);
}

/** The cue one line up, when the author left its place to the player; whether it was moved. */
export function raiseCue(cue: CueLike, aspect: number, scale: number): boolean {
  if (cue.line !== "auto" || !cue.snapToLines || cue.vertical || cue.region) return false;
  cue.line = -(cueLines(cue.text, aspect, scale, cue.size) + 1);
  return true;
}

/**
 * Keeps `el`'s subtitles clear of the bottom edge: the cues of every track that is on, as they
 * load and as more come (HLS adds them while it plays). Returns what stops it.
 */
export function raiseCues(el: HTMLVideoElement, scale: number): () => void {
  // the cues left to the player, kept so a new subtitle size counts their lines again
  const ours = new Set<CueLike>();
  const seen = new WeakSet<object>();
  let aspect = 0;
  const run = () => {
    if (el.videoWidth > 0 && el.videoHeight > 0) aspect = el.videoWidth / el.videoHeight;
    const wide = aspect || 16 / 9;
    for (const track of Array.from(el.textTracks ?? [])) {
      const cues = track.mode === "disabled" ? null : track.cues;
      if (!cues) continue;
      const moved = new Set<unknown>();
      for (const cue of Array.from(cues) as unknown as CueLike[]) {
        if (seen.has(cue)) continue;
        seen.add(cue);
        if (!raiseCue(cue, wide, scale)) continue;
        ours.add(cue);
        moved.add(cue);
      }
      // a cue already on screen (the subtitles just turned on) is not laid out again by itself
      if (
        track.mode === "showing" &&
        Array.from(track.activeCues ?? []).some((c) => moved.has(c))
      ) {
        track.mode = "hidden";
        track.mode = "showing";
      }
    }
  };
  const events = ["loadedmetadata", "loadeddata", "playing", "timeupdate"];
  for (const e of events) el.addEventListener(e, run);
  // a <track>'s file arriving (its event does not bubble)
  el.addEventListener("load", run, true);
  const list = el.textTracks;
  list?.addEventListener?.("change", run);
  list?.addEventListener?.("addtrack", run);
  run();
  return () => {
    for (const e of events) el.removeEventListener(e, run);
    el.removeEventListener("load", run, true);
    list?.removeEventListener?.("change", run);
    list?.removeEventListener?.("addtrack", run);
    for (const cue of ours) cue.line = "auto";
  };
}
