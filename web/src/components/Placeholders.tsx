// Loading placeholders in the shape of what loads: Home's hero and rows of
// poster cards, the library / collection / playlist cards, a list's items and
// the grid's cards, built on the same classes as the real ones (MediaRow,
// FeaturedHero, Libraries), so nothing moves when the content comes. They
// show only once a read has taken PLACEHOLDER_DELAY_MS: a quick one never
// flashes one. The region is aria-busy from the start; the shapes themselves
// are hidden from assistive tech.
import type { ReactNode } from "react";
import { Skeleton } from "@ds/components/Skeleton/Skeleton";
import { useDelayedFlag } from "../lib/useDelayedFlag";

/** The loading region: busy while `loading`, its placeholder after the delay. */
export function Loading({ loading, children }: { loading: boolean; children: ReactNode }) {
  const late = useDelayedFlag(loading);
  if (!loading) return null;
  return (
    <div className="mkt-page-loading" aria-busy="true">
      {late && children}
    </div>
  );
}

// beside an inline bar, a heading keeps its real line height
const NBSP = " ";

/** A poster-shaped block (16:9, as VideoPoster). */
function PosterBlock() {
  return <Skeleton variant="rect" className="mkt-ph-fill" />;
}

/** A row of poster cards with its title, as MediaRow lays out VideoRow / AddedRow. */
export function RowPlaceholder({ cards = 6 }: { cards?: number }) {
  return (
    <section className="mkt-row" aria-hidden="true">
      <header className="mkt-row__head mkt-ph-head">
        <span className="mkt-row__title">
          <Skeleton variant="text" className="mkt-ph-title" />
        </span>
      </header>
      <ul className="mkt-row__scroller">
        {Array.from({ length: cards }, (_, i) => (
          <li key={i} className="mkt-row__item">
            <span className="mkt-media-card">
              <span className="mkt-media-card__art">
                <PosterBlock />
              </span>
              <span className="mkt-media-card__title">
                <Skeleton variant="text" className="mkt-ph-line" width="80%" />
              </span>
            </span>
          </li>
        ))}
      </ul>
    </section>
  );
}

/** Home: the featured hero's area, then rows of poster cards. */
export function HomePlaceholder({ rows = 3 }: { rows?: number }) {
  return (
    <>
      <div className="mkt-hero mkt-ph-hero" aria-hidden="true">
        <div className="mkt-hero__body">
          <Skeleton variant="text" width="7rem" />
          <Skeleton variant="text" className="mkt-ph-hero-title" width="18rem" />
          <Skeleton variant="text" width="5rem" />
          <span className="mkt-hero__actions">
            <Skeleton variant="rect" className="mkt-ph-button" width="11rem" />
            <Skeleton variant="rect" className="mkt-ph-button" width="8rem" />
          </span>
        </div>
      </div>
      {Array.from({ length: rows }, (_, i) => (
        <RowPlaceholder key={i} />
      ))}
    </>
  );
}

/** Library, collection and playlist cards (Libraries' grid): mosaic, name,
 *  count; `heading` is the class of the grid's own heading, when it has one. */
export function CardGridPlaceholder({ cards = 4, heading }: { cards?: number; heading?: string }) {
  return (
    <>
      {heading && (
        <h2 className={heading} aria-hidden="true">
          <Skeleton variant="text" className="mkt-ph-title mkt-ph-inline" />
          {NBSP}
        </h2>
      )}
      <ul className="mkt-libgrid" aria-hidden="true">
        {Array.from({ length: cards }, (_, i) => (
          <li key={i} className="mkt-libcard">
            <span className="mkt-libcard__link">
              <Skeleton variant="rect" className="mkt-ph-fill mkt-ph-mosaic" />
              <span className="mkt-libcard__name">
                <Skeleton variant="text" className="mkt-ph-line" width="60%" />
              </span>
              <span className="mkt-libcard__meta">
                <Skeleton variant="text" className="mkt-ph-line" width="35%" />
              </span>
            </span>
          </li>
        ))}
      </ul>
    </>
  );
}

/** A page's heading: the title and the line under it. */
export function HeadingPlaceholder() {
  return (
    <div className="mkt-ph-heading" aria-hidden="true">
      <h1>
        <Skeleton variant="text" className="mkt-ph-inline" width="14rem" />
        {NBSP}
      </h1>
      <Skeleton variant="text" width="6rem" />
    </div>
  );
}

/** A library's page: its heading, then rows of poster cards. */
export function LibraryPagePlaceholder() {
  return (
    <>
      <HeadingPlaceholder />
      <RowPlaceholder />
      <RowPlaceholder />
    </>
  );
}

/** A playlist's or collection's page: its heading, then its numbered items. */
export function ItemListPlaceholder({ items = 5 }: { items?: number }) {
  return (
    <>
      <HeadingPlaceholder />
      <ol className="mkt-plitems" aria-hidden="true">
        {Array.from({ length: items }, (_, i) => (
          <li key={i} className="mkt-plitems__item">
            <span className="mkt-plitems__num" />
            <span className="mkt-plitems__art">
              <PosterBlock />
            </span>
            <span className="mkt-plitems__text">
              <Skeleton variant="text" className="mkt-ph-line" width="60%" />
              <Skeleton variant="text" className="mkt-ph-line" width="20%" />
            </span>
            <span />
          </li>
        ))}
      </ol>
    </>
  );
}

/** The grid's video cards (LibraryBrowser): poster, title, meta. */
export function VideoGridPlaceholder({ cards = 6 }: { cards?: number }) {
  return (
    <ul className="mkt-grid" aria-hidden="true">
      {Array.from({ length: cards }, (_, i) => (
        <li key={i} className="mkt-card">
          <span className="mkt-ph-card">
            <PosterBlock />
            <span className="mkt-card__title">
              <Skeleton variant="text" className="mkt-ph-line" width="75%" />
            </span>
            <span className="mkt-card__meta">
              <Skeleton variant="text" className="mkt-ph-line" width="40%" />
            </span>
          </span>
        </li>
      ))}
    </ul>
  );
}

/**
 * A table's rows (a `.mkt-table` page, the grid's list view): the first
 * column wide, the others short; `head` draws the header row too.
 */
export function TablePlaceholder({
  rows = 6,
  columns = 5,
  head = false,
  className = "mkt-table",
}: {
  rows?: number;
  columns?: number;
  head?: boolean;
  className?: string;
}) {
  const cells = (tag: "th" | "td", row: number) =>
    Array.from({ length: columns }, (_, c) => {
      const Cell = tag;
      return (
        <Cell key={`${row}:${c}`}>
          <Skeleton variant="text" className="mkt-ph-line" width={c === 0 ? "70%" : "3rem"} />
        </Cell>
      );
    });
  return (
    <table className={className} aria-hidden="true">
      {head && (
        <thead>
          <tr>{cells("th", -1)}</tr>
        </thead>
      )}
      <tbody>
        {Array.from({ length: rows }, (_, i) => (
          <tr key={i}>{cells("td", i)}</tr>
        ))}
      </tbody>
    </table>
  );
}

/** Stacked text lines (a settings panel, a short list): `lines` bars of the text's height. */
export function LinesPlaceholder({ lines = 4 }: { lines?: number }) {
  return (
    <div className="mkt-ph-lines" aria-hidden="true">
      {Array.from({ length: lines }, (_, i) => (
        <Skeleton
          key={i}
          variant="text"
          className="mkt-ph-line"
          width={i === lines - 1 ? "40%" : "90%"}
        />
      ))}
    </div>
  );
}
