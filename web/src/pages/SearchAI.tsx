// Story 49.14 — the Search page's AI mode tab: a question asked of the home's language model
// (POST /api/search/ai, story 49.12, through lib/searchAI). Each turn shows its sources at
// once, then the answer sentence by sentence, each citation a link that opens the player at
// its moment. A follow-up asks with the earlier turns as history. A home without a model, AI
// mode turned off, or a full line are messages, not errors. The conversation itself lives in
// the page (useAIConversation), so leaving the tab and coming back keeps it.
import { type FormEvent, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { useI18n } from "../lib/i18n";
import { errorText } from "../lib/errorText";
import {
  AI_HELP_PATH,
  type AIConversation,
  type AIPassage,
  type AITurnState,
  turnLive,
} from "../lib/searchAI";
import { ts, watchHref } from "./searchShared";

export function SearchAI({ conv, isAdmin }: { conv: AIConversation; isAdmin: boolean }) {
  const { t } = useI18n();
  // the words of the search in the URL (the Results' submitted words) are asked as the tab
  // opens (useAITab, given Search's urlQ); this box starts empty
  const [q, setQ] = useState("");
  const input = useRef<HTMLInputElement>(null);
  const last = conv.turns[conv.turns.length - 1];
  const live = turnLive(last);

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    const question = q.trim();
    if (!question) return;
    conv.ask(question);
    setQ("");
  }

  const followUp = conv.turns.length > 0;
  return (
    <div className="mkt-ai">
      <form className="mkt-search__form mkt-ai__form" onSubmit={onSubmit}>
        <label htmlFor="mkt-ai-q" className="mkt-ai__label">
          {followUp ? t("search.ai.followUp") : t("search.ai.question")}
        </label>
        <input
          id="mkt-ai-q"
          ref={input}
          type="search"
          dir="auto"
          value={q}
          maxLength={500}
          onChange={(e) => setQ(e.target.value)}
          placeholder={t("search.ai.placeholder")}
          autoComplete="off"
        />
        <Button type="submit" loading={live} disabled={!q.trim()}>
          {t("search.ai.ask")}
        </Button>
        {followUp && (
          <Button
            type="button"
            variant="secondary"
            onClick={() => {
              conv.reset();
              input.current?.focus();
            }}
          >
            {t("search.ai.new")}
          </Button>
        )}
      </form>
      {!followUp && <p className="mkt-muted mkt-ai__intro">{t("search.ai.intro")}</p>}
      <div className="mkt-ai__turns">
        {conv.turns.map((turn) => (
          <AITurn key={turn.id} turn={turn} isAdmin={isAdmin} onAgain={() => conv.ask(turn.q)} />
        ))}
      </div>
    </div>
  );
}

function AITurn({
  turn,
  isAdmin,
  onAgain,
}: {
  turn: AITurnState;
  isAdmin: boolean;
  onAgain: () => void;
}) {
  const { t, n } = useI18n();
  const live = turnLive(turn);
  const headingId = `mkt-ai-turn-${turn.id}`;
  const byN = new Map<number, AIPassage>(turn.passages.map((p) => [p.n, p]));
  return (
    <article
      className="mkt-ai__turn"
      aria-labelledby={headingId}
      aria-busy={live}
      data-testid="ai-turn"
      data-status={turn.status}
    >
      <h2 id={headingId} className="mkt-ai__q" dir="auto">
        {turn.q}
      </h2>
      <p className="mkt-ai__status" role="status">
        <StatusLine turn={turn} />
      </p>
      {turn.refusal && <Refusal turn={turn} isAdmin={isAdmin} onAgain={onAgain} />}
      {turn.sentences.length > 0 && (
        <section className="mkt-ai__answer" aria-label={t("search.ai.answer")}>
          <p dir="auto" data-testid="ai-answer">
            {turn.sentences.map((s, i) => (
              <span key={i} className="mkt-ai__sentence" data-testid="ai-sentence">
                {s.text}
                {s.citations.map((c) => {
                  const p = byN.get(c.n);
                  const name = t("search.ai.cite", {
                    n: n(c.n),
                    title: p?.title || c.video_id,
                    time: ts(c.start_sec),
                  });
                  return (
                    <Link
                      key={c.n}
                      className="mkt-ai__cite"
                      to={watchHref(c.video_id, c.start_sec)}
                      aria-label={name}
                      title={c.quote || name}
                    >
                      [{n(c.n)}]
                    </Link>
                  );
                })}{" "}
              </span>
            ))}
          </p>
        </section>
      )}
      {turn.passages.length > 0 && (
        <section className="mkt-ai__sources" aria-labelledby={`${headingId}-sources`}>
          <h3 id={`${headingId}-sources`}>{t("search.ai.sources")}</h3>
          <ol className="mkt-ai__source-list" data-testid="ai-sources">
            {turn.passages.map((p) => (
              <li key={p.n} className="mkt-ai__source">
                <span className="mkt-ai__source-n" aria-hidden="true">
                  {n(p.n)}
                </span>
                <div className="mkt-ai__source-body">
                  <Link
                    to={watchHref(p.video_id, p.start_sec)}
                    className="mkt-ai__source-title"
                    aria-label={t("search.ai.cite", {
                      n: n(p.n),
                      title: p.title || p.video_id,
                      time: ts(p.start_sec),
                    })}
                  >
                    <bdi>{p.title || p.video_id}</bdi>
                    <span className="mkt-ai__at" dir="ltr">
                      {ts(p.start_sec)}
                    </span>
                  </Link>
                  {p.library_name && (
                    <span className="mkt-muted mkt-ai__lib">
                      <bdi>{p.library_name}</bdi>
                    </span>
                  )}
                  <p className="mkt-ai__passage" dir="auto">
                    {p.text}
                  </p>
                </div>
              </li>
            ))}
          </ol>
        </section>
      )}
    </article>
  );
}

/** What the turn is doing, or how it ended (the polite status line). */
function StatusLine({ turn }: { turn: AITurnState }) {
  const { t, n } = useI18n();
  switch (turn.status) {
    case "asking":
      return <>{t("search.ai.asking")}</>;
    case "waiting":
      return <>{t("search.ai.queue", { position: n(turn.position ?? 1) })}</>;
    case "writing":
      return turn.sentences.length ? null : <>{t("search.ai.writing")}</>;
    case "done":
      if (turn.reason === "no-passages") return <>{t("search.ai.noPassages")}</>;
      // a timeout says so, with or without sentences: no sentence is no proof the passages
      // do not answer
      if (turn.reason === "timeout") return <>{t("search.ai.timeout")}</>;
      if (!turn.answered) return <>{t("search.ai.notAnswered")}</>;
      return null;
    case "failed":
      return <>{t("search.ai.failed")}</>;
    case "stopped":
      return <>{t("search.ai.stopped")}</>;
    case "refused":
      return null;
  }
}

function Refusal({
  turn,
  isAdmin,
  onAgain,
}: {
  turn: AITurnState;
  isAdmin: boolean;
  onAgain: () => void;
}) {
  const { t, n } = useI18n();
  const r = turn.refusal!;
  if (r.code === "ai-unavailable") {
    return (
      <div className="mkt-ai__note" role="note" data-testid="ai-unavailable">
        <p>{t("search.ai.unavailable")}</p>
        <p>
          {isAdmin && r.help?.settings ? (
            <Link to={r.help.settings}>{t("search.ai.unavailable.settings")}</Link>
          ) : (
            t("search.ai.unavailable.member")
          )}
          {" · "}
          <Link to={r.help?.help || AI_HELP_PATH}>{t("search.ai.unavailable.help")}</Link>
        </p>
      </div>
    );
  }
  if (r.code === "ai-off") {
    return (
      <p className="mkt-ai__note" role="note" data-testid="ai-off">
        {t("search.ai.off")}
      </p>
    );
  }
  if (r.code === "ai-busy") {
    return (
      <div className="mkt-ai__note" role="note" data-testid="ai-busy">
        <p>{t("search.ai.busy", { seconds: n(r.retryAfter ?? 30) })}</p>
        <Button type="button" variant="secondary" onClick={onAgain}>
          {t("search.ai.again")}
        </Button>
      </div>
    );
  }
  return (
    <div className="mkt-ai__note mkt-ai__note--error" role="alert">
      <p>{errorText(r.error, t)}</p>
      <Button type="button" variant="secondary" onClick={onAgain}>
        {t("search.ai.again")}
      </Button>
    </div>
  );
}
