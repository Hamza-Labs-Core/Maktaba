import { clsx } from "../internal/clsx";
import "./pagination.css";

// Story 17.2 — Pagination. A <nav aria-label> of page buttons with
// prev/next, ellipsis truncation, and aria-current on the active page.
// 1-indexed `page`.

export interface PaginationProps {
  page: number;
  pageCount: number;
  onChange: (page: number) => void;
  /** Accessible name for the nav landmark. */
  label?: string;
  /** Pages shown around the current one before collapsing to "…". */
  siblings?: number;
  className?: string;
}

function range(start: number, end: number): number[] {
  return Array.from({ length: end - start + 1 }, (_, i) => start + i);
}

function pages(page: number, count: number, siblings: number): Array<number | "…"> {
  if (count <= siblings * 2 + 5) return range(1, count);
  const left = Math.max(2, page - siblings);
  const right = Math.min(count - 1, page + siblings);
  const out: Array<number | "…"> = [1];
  if (left > 2) out.push("…");
  out.push(...range(left, right));
  if (right < count - 1) out.push("…");
  out.push(count);
  return out;
}

export function Pagination({
  page,
  pageCount,
  onChange,
  label = "Pagination",
  siblings = 1,
  className,
}: PaginationProps) {
  if (pageCount <= 1) return null;
  const items = pages(page, pageCount, siblings);
  return (
    <nav className={clsx("mk-pagination", className)} aria-label={label}>
      <button
        type="button"
        className="mk-pagination__btn"
        aria-label="Previous page"
        disabled={page <= 1}
        onClick={() => onChange(page - 1)}
      >
        ‹
      </button>
      {items.map((it, i) =>
        it === "…" ? (
          <span key={`gap-${i}`} className="mk-pagination__gap" aria-hidden="true">
            …
          </span>
        ) : (
          <button
            key={it}
            type="button"
            className={clsx("mk-pagination__btn", it === page && "mk-pagination__btn--active")}
            aria-current={it === page ? "page" : undefined}
            aria-label={`Page ${it}`}
            onClick={() => onChange(it)}
          >
            {it}
          </button>
        )
      )}
      <button
        type="button"
        className="mk-pagination__btn"
        aria-label="Next page"
        disabled={page >= pageCount}
        onClick={() => onChange(page + 1)}
      >
        ›
      </button>
    </nav>
  );
}
