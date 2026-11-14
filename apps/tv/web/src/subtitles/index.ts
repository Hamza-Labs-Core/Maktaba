// Epic 58 — the shared subtitle layer (stories 58.10 and 58.11): the WebVTT reader and the
// layer that draws Maktaba's own subtitles over the video on Samsung and LG.
import { parseVtt, type Cue } from "./vtt";

export { parseVtt, parseTime, cueText, cuesAt, type Cue } from "./vtt";
export { SubtitleLayer, SUBTITLE_SCALES, BASE_FONT_PX, textDir, validScale } from "./layer";

/** Fetches and reads a WebVTT file ([] on any failure, with the reason in onError). */
export async function loadVtt(
  url: string,
  fetchFn: (url: string) => Promise<Response>,
  onError?: (reason: string) => void
): Promise<Cue[]> {
  try {
    const res = await fetchFn(url);
    if (!res.ok) {
      onError?.(`HTTP ${res.status}`);
      return [];
    }
    const cues = parseVtt(await res.text());
    if (cues.length === 0) onError?.("no cues");
    return cues;
  } catch (e) {
    onError?.(e instanceof Error ? e.message : String(e));
    return [];
  }
}
