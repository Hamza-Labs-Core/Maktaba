// The player's overlays (Epic 34.6 AC1/AC3): the tuning card, the number
// overlay and the mini-guide. They carry no styling of their own beyond
// class names and take every word from the host app (i18n), so the public
// site and the Maktaba app dress them their own way.
import type { ReactNode } from "react";

export interface CardChannel {
  id: string;
  number: number;
  name: string;
  logo?: string | null;
}

export interface TuningCardProps {
  channel: CardChannel;
  /** What is on now / next (already formatted). */
  now?: ReactNode;
  next?: ReactNode;
  /** The switch's state line ("Tuning…", "Starting remote stream…", an error). */
  status?: ReactNode;
  error?: boolean;
  className?: string;
}

/** The card shown over the old picture from the input until the new first frame. */
export function TuningCard({ channel, now, next, status, error, className }: TuningCardProps) {
  return (
    <div
      className={["tvp-card", error ? "tvp-card--error" : "", className ?? ""].join(" ").trim()}
      data-testid="tuning-card"
      role="status"
      aria-live="polite"
    >
      <div className="tvp-card__id">
        {channel.logo && <img className="tvp-card__logo" src={channel.logo} alt="" />}
        <span className="tvp-card__num">{channel.number}</span>
        <strong className="tvp-card__name">{channel.name}</strong>
      </div>
      {now && <div className="tvp-card__now">{now}</div>}
      {next && <div className="tvp-card__next">{next}</div>}
      {status && <div className="tvp-card__status">{status}</div>}
    </div>
  );
}

export function NumberOverlay({ text, className }: { text: ReactNode; className?: string }) {
  return (
    <div className={["tvp-number", className ?? ""].join(" ").trim()} data-testid="number-entry">
      {text}
    </div>
  );
}

export interface MiniGuideRow {
  channel: CardChannel;
  now?: ReactNode;
  next?: ReactNode;
}

export interface MiniGuideProps {
  rows: MiniGuideRow[];
  currentId: string | null;
  title: ReactNode;
  closeLabel: ReactNode;
  onPick: (id: string) => void;
  onClose: () => void;
  className?: string;
  nextLabel?: ReactNode;
}

/** Now/next for every channel over the playing picture; a tap tunes. */
export function MiniGuide(p: MiniGuideProps) {
  return (
    <div
      className={["tvp-miniguide", p.className ?? ""].join(" ").trim()}
      data-testid="mini-guide"
      role="dialog"
      aria-label={typeof p.title === "string" ? p.title : undefined}
    >
      <header>
        <strong>{p.title}</strong>
        <button type="button" onClick={p.onClose}>
          {p.closeLabel}
        </button>
      </header>
      <ul>
        {p.rows.map((r) => (
          <li key={r.channel.id}>
            <button
              type="button"
              className={r.channel.id === p.currentId ? "is-current" : ""}
              aria-current={r.channel.id === p.currentId ? "true" : undefined}
              onClick={() => p.onPick(r.channel.id)}
            >
              <span className="tvp-miniguide__num">{r.channel.number}</span>
              <span className="tvp-miniguide__name">{r.channel.name}</span>
              <span className="tvp-miniguide__now">{r.now}</span>
              {r.next && (
                <span className="tvp-miniguide__next">
                  {p.nextLabel}
                  {p.nextLabel ? ": " : ""}
                  {r.next}
                </span>
              )}
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}
