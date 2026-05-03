# Story 17.11 — Subtitle and transcript presentation

The transcript sidebar and inline subtitle layer share styles and
behaviors so the user's eye doesn't have to re-learn either.

**Anchors:** [`architecture.md` §4.5](../../architecture.md). Implements
the visual language consumed by
[Story 11.2](../11-web-ui/story-11-02-video-detail-page.md) and the
inline player overlay from
[Story 11.3](../11-web-ui/story-11-03-video-player.md).

## AC

- Transcript sidebar: list of segments with `[mm:ss]` prefix, speaker
  badge if diarized, current segment highlighted in real time.
- Click a segment: player seeks to its `start_sec`.
- Search inside transcript: `Cmd/Ctrl+F` opens an inline find bar.
- Inline subtitles: same font + size as transcript, with stronger
  background contrast.
- Auto-scroll the sidebar to keep the current segment in view; toggle
  off ("Free scroll") in transcript settings.
- "Copy transcript" / "Copy timestamped transcript" actions.

## TC

- Play a video; sidebar's current segment highlight tracks the player
  within 200 ms.
- Click a segment: player seeks; sidebar centers on the clicked item.
- Search transcript for a phrase: matches highlighted; `n` / `Shift+n`
  cycles through them.

## EC

- A segment with no text (silence): rendered with a dim placeholder;
  not skipped (preserves timing context).
- A segment with bidi-mixed text: bidi isolate so neither direction
  bleeds.
- 50,000 segments: virtualized; auto-scroll seeks via virtual list
  index, not pixel offset.
