// Story 49.14 — big screen's AI mode (and so the TV launchers' /tv/ build): the tab beside
// Results on the Search screen. The question is typed on the screen keyboard and asked with
// Ask; the turns show newest first (on a TV the latest answer is where the eye already is),
// each with its place in line, its sources at once, then its sentences, every citation a
// D-pad stop that plays the video at its moment. The stream and the conversation are the
// web page's (lib/searchAI); only the look and the focus ids are the big screen's.
import { useI18n } from "../../lib/i18n";
import { errorText } from "../../lib/errorText";
import {
  type AIConversation,
  type AIPassage,
  type AITurnState,
  turnLive,
} from "../../lib/searchAI";
import { bigPath, useBigNav } from "../nav";
import { Empty, Ltr, clock } from "../ui";

export function BigSearchAI({
  conv,
  question,
  onAsk,
}: {
  conv: AIConversation;
  /** the words on the keyboard, the next question */
  question: string;
  /** asks `question` (the screen clears the keyboard for the next one) */
  onAsk: () => void;
}) {
  const { t } = useI18n();
  const shown = [...conv.turns].reverse();
  return (
    <div className="bs-ai" data-testid="big-ai">
      <div className="bs-chips bs-ai__bar" data-bs-group="ai-ask">
        {question && (
          <button
            type="button"
            className="bs-chip bs-ai__ask"
            data-bs-focus="ai:ask"
            onClick={onAsk}
          >
            <bdi>{t("big.search.ai.ask", { q: question })}</bdi>
          </button>
        )}
        {conv.turns.length > 0 && (
          <button type="button" className="bs-chip" data-bs-focus="ai:new" onClick={conv.reset}>
            {t("search.ai.new")}
          </button>
        )}
      </div>
      {conv.turns.length === 0 && (
        <Empty>
          <p>{t("search.ai.intro")}</p>
          <p>{t("big.search.ai.hint")}</p>
        </Empty>
      )}
      {shown.map((turn) => (
        <BigTurn key={turn.id} turn={turn} onAgain={() => conv.ask(turn.q)} />
      ))}
    </div>
  );
}

function BigTurn({ turn, onAgain }: { turn: AITurnState; onAgain: () => void }) {
  const { t, n } = useI18n();
  const { go } = useBigNav();
  const byN = new Map<number, AIPassage>(turn.passages.map((p) => [p.n, p]));
  const headingId = `bs-ai-turn-${turn.id}`;
  const play = (videoId: string, sec: number) => go(bigPath.watch(videoId, { t: sec }));
  return (
    <section
      className="bs-ai__turn"
      aria-labelledby={headingId}
      aria-busy={turnLive(turn)}
      data-testid="big-ai-turn"
      data-status={turn.status}
    >
      <h2 id={headingId} className="bs-row__title" dir="auto">
        {turn.q}
      </h2>
      <p className="bs-ai__status" role="status">
        {statusText(turn, t, n)}
      </p>
      {turn.refusal && <BigRefusal turn={turn} onAgain={onAgain} />}
      {turn.sentences.length > 0 && (
        <div
          className="bs-ai__answer"
          data-bs-group={`ai-answer:${turn.id}`}
          data-testid="big-ai-answer"
        >
          {turn.sentences.map((s, i) => (
            <p key={i} className="bs-ai__sentence" dir="auto">
              <span>{s.text}</span>
              {s.citations.map((c) => {
                const p = byN.get(c.n);
                return (
                  <button
                    key={c.n}
                    type="button"
                    className="bs-chip bs-ai__cite"
                    data-bs-focus={`ai:c:${turn.id}:${i}:${c.n}`}
                    aria-label={t("big.search.ai.cite", {
                      n: n(c.n),
                      title: p?.title || c.video_id,
                      time: clock(c.start_sec),
                    })}
                    onClick={() => play(c.video_id, c.start_sec)}
                  >
                    {n(c.n)}
                  </button>
                );
              })}
            </p>
          ))}
        </div>
      )}
      {turn.passages.length > 0 && (
        <div className="bs-ai__sources">
          <h3 className="bs-panel__heading" id={`${headingId}-sources`}>
            {t("search.ai.sources")}
          </h3>
          <div
            className="bs-matches"
            data-bs-group={`ai-sources:${turn.id}`}
            role="list"
            aria-labelledby={`${headingId}-sources`}
          >
            {turn.passages.map((p) => (
              <div role="listitem" key={p.n}>
                <button
                  type="button"
                  className="bs-match"
                  data-bs-focus={`ai:s:${turn.id}:${p.n}`}
                  onClick={() => play(p.video_id, p.start_sec)}
                >
                  <span className="bs-match__title">
                    <span className="bs-ai__n">{n(p.n)}</span> <bdi>{p.title || p.video_id}</bdi>
                  </span>
                  <span className="bs-match__line">
                    <Ltr className="bs-line__at">{clock(p.start_sec)}</Ltr>
                    <span dir="auto">{p.text}</span>
                  </span>
                </button>
              </div>
            ))}
          </div>
        </div>
      )}
    </section>
  );
}

function statusText(
  turn: AITurnState,
  t: (k: string, v?: Record<string, string | number>) => string,
  n: (v: number) => string
): string {
  switch (turn.status) {
    case "asking":
      return t("search.ai.asking");
    case "waiting":
      return t("search.ai.queue", { position: n(turn.position ?? 1) });
    case "writing":
      return turn.sentences.length ? "" : t("search.ai.writing");
    case "done":
      if (turn.reason === "no-passages") return t("search.ai.noPassages");
      if (!turn.answered) return t("search.ai.notAnswered");
      if (turn.reason === "timeout") return t("search.ai.timeout");
      return "";
    case "failed":
      return t("search.ai.failed");
    case "stopped":
      return t("search.ai.stopped");
    case "refused":
      return "";
  }
}

function BigRefusal({ turn, onAgain }: { turn: AITurnState; onAgain: () => void }) {
  const { t, n } = useI18n();
  const r = turn.refusal!;
  if (r.code === "ai-unavailable")
    return (
      <div className="bs-state" role="note" data-testid="big-ai-unavailable">
        <p>{t("search.ai.unavailable")}</p>
        <p>{t("big.search.ai.unavailable")}</p>
      </div>
    );
  if (r.code === "ai-off")
    return (
      <div className="bs-state" role="note" data-testid="big-ai-off">
        <p>{t("search.ai.off")}</p>
      </div>
    );
  const busy = r.code === "ai-busy";
  return (
    <div
      className={busy ? "bs-state" : "bs-state bs-state--error"}
      role={busy ? "note" : "alert"}
      data-testid={busy ? "big-ai-busy" : "big-ai-failed"}
    >
      <p>
        {busy ? t("search.ai.busy", { seconds: n(r.retryAfter ?? 30) }) : errorText(r.error, t)}
      </p>
      <button
        type="button"
        className="bs-btn"
        data-bs-focus={`ai:again:${turn.id}`}
        onClick={onAgain}
      >
        {t("search.ai.again")}
      </button>
    </div>
  );
}
