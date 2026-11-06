// The audio around the word (Epic 52, story 52.11): plays as the word opens, at 1× or
// 0.75×, replays from its start. A clip that cannot load says so; the actions still work.
import { useEffect, useImperativeHandle, useRef, useState, type MutableRefObject } from "react";
import { useI18n } from "../../lib/i18n";
import type { Clip } from "../../lib/reviewApi";

export interface ClipControls {
  toggle: (rate: number) => void;
  replay: () => void;
}

export function ClipPlayer({
  clip,
  controls,
}: {
  clip: Clip | null;
  controls: MutableRefObject<ClipControls | null>;
}) {
  const { t } = useI18n();
  const audio = useRef<HTMLAudioElement | null>(null);
  const [failed, setFailed] = useState(false);
  const [rate, setRate] = useState(1);

  useEffect(() => {
    setFailed(false);
    const a = audio.current;
    if (!a || !clip) return;
    a.playbackRate = rate;
    void a.play?.()?.catch?.(() => undefined);
  }, [clip?.url]);

  useImperativeHandle(controls, () => ({
    toggle: (r: number) => {
      const a = audio.current;
      if (!a) return;
      a.playbackRate = r;
      setRate(r);
      if (a.paused) void a.play?.()?.catch?.(() => undefined);
      else a.pause();
    },
    replay: () => {
      const a = audio.current;
      if (!a) return;
      a.currentTime = 0;
      void a.play?.()?.catch?.(() => undefined);
    },
  }));

  if (!clip || failed) return <p className="mkt-review-noaudio">{t("review.noAudio")}</p>;
  return (
    <div className="mkt-review-player">
      <audio
        ref={audio}
        src={clip.url}
        preload="auto"
        onError={() => setFailed(true)}
        data-testid="review-audio"
      />
      <button type="button" onClick={() => controls.current?.toggle(1)}>
        {t("review.play")}
      </button>
      <button type="button" onClick={() => controls.current?.toggle(0.75)}>
        {t("review.slow")}
      </button>
      <button type="button" onClick={() => controls.current?.replay()}>
        {t("review.replay")}
      </button>
    </div>
  );
}
