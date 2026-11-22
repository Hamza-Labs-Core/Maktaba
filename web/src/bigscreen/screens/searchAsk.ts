// Story 49.11 — what big screen's Search asks the home beyond its words (pure): the order
// and the facet filters, in the request body and in the URL. The filters, their URL
// parameters and the request filters are the desk Search's (pages/searchShared.ts).
import {
  type FacetSel,
  type Facets,
  type Sort,
  noSel,
  requestFilters,
  searchParamsOf,
  selOf,
  urlSort,
} from "../../pages/searchShared";

/** The order and the filters of a search. */
export interface BigAsk {
  sort: Sort;
  sel: FacetSel;
}

export const plainAsk = (): BigAsk => ({ sort: "relevance", sel: noSel() });

/** The order and filters in the screen's URL (the desk's parameters: sort, lib, lang, spk, year, dur). */
export const askOf = (p: URLSearchParams): BigAsk => ({
  sort: urlSort(p.get("sort")),
  sel: selOf(p),
});

// the parameters a search owns in the URL; any other stays as it was
const OWN = ["q", "sort", "lib", "lang", "spk", "year", "dur"];

/**
 * The URL parameters of a search: `params` with the query, the order (left out when
 * relevance) and the filters; no query leaves none of them.
 */
export function bigSearchParams(
  params: URLSearchParams,
  q: string | null,
  ask: BigAsk
): URLSearchParams {
  const out = new URLSearchParams();
  for (const [k, v] of params) if (!OWN.includes(k)) out.append(k, v);
  if (!q) return out;
  for (const [k, v] of searchParamsOf({ q, mode: "hybrid", sort: ask.sort, sel: ask.sel }))
    if (k !== "mode") out.append(k, v);
  return out;
}

/**
 * The POST /api/search body. Relevance and no filters leave their fields out, so a home
 * before story 49.8 reads the request it always did. A year or duration band sends its
 * bucket's own filter from the facets on screen, else the range the server gives it.
 */
export function bigSearchBody(
  q: string,
  ask: BigAsk,
  facets: Facets | null | undefined
): Record<string, unknown> {
  const body: Record<string, unknown> = { q, mode: "hybrid", view: "grid" };
  if (ask.sort !== "relevance") body.sort = ask.sort;
  const filters = requestFilters(ask.sel, facets);
  if (filters) body.filters = filters;
  return body;
}

/** A key that changes when the order or the filters do. */
export const askKey = (ask: BigAsk): string =>
  bigSearchParams(new URLSearchParams(), "-", ask).toString();
