// Story 49.11 — big screen's search request and URL from its order and facet choices.
import { describe, expect, it } from "vitest";
import { noSel, type Facets } from "../../pages/searchShared";
import { askKey, askOf, bigSearchBody, bigSearchParams, plainAsk } from "./searchAsk";
import { activeChips, facetGroups, suggestItems } from "../../pages/searchHelp";
import { didYouMeanOf, facetsOf } from "../../pages/searchShared";

const FACETS: Facets = {
  year: [
    {
      value: "2023",
      count: 4,
      filter: { date: { gte: "2023-01-01T03:00:00.000Z", lt: "2024-01-01T03:00:00.000Z" } },
    },
  ],
  duration: [{ value: "over_60m", count: 2, filter: { duration_sec: { gte: 3600 } } }],
};

describe("bigSearchBody", () => {
  it("relevance and no filters send the request a home before 49.8 reads", () => {
    expect(bigSearchBody("ab", plainAsk(), null)).toEqual({
      q: "ab",
      mode: "hybrid",
      view: "grid",
    });
  });

  it("sends the order and each facet's filter; lists combine", () => {
    const body = bigSearchBody(
      "ab",
      {
        sort: "matches",
        sel: {
          library: ["L1", "L2"],
          language: ["ar"],
          speaker: ["Ahmad"],
          year: "2023",
          duration: "over_60m",
        },
      },
      FACETS
    );
    expect(body).toEqual({
      q: "ab",
      mode: "hybrid",
      view: "grid",
      sort: "matches",
      filters: {
        library_id: ["L1", "L2"],
        language: ["ar"],
        speaker: ["Ahmad"],
        // the bucket's own filter, as the home gave it
        date: { gte: "2023-01-01T03:00:00.000Z", lt: "2024-01-01T03:00:00.000Z" },
        duration_sec: { gte: 3600 },
      },
    });
  });

  it("a year or band the facets on screen do not list takes the range the server gives it", () => {
    const body = bigSearchBody(
      "ab",
      { sort: "newest", sel: { ...noSel(), year: "2020", duration: "under_5m" } },
      null
    );
    expect(body.filters).toEqual({
      date: { gte: "2020-01-01T00:00:00.000Z", lt: "2021-01-01T00:00:00.000Z" },
      duration_sec: { gte: 0, lt: 300 },
    });
  });
});

describe("the URL", () => {
  it("carries the query, a non-relevance order and the filters, and keeps other parameters", () => {
    const p = bigSearchParams(new URLSearchParams("x=1&q=old&lib=L9"), "ab", {
      sort: "newest",
      sel: { library: ["L1"], language: [], speaker: ["A b"], year: "2024" },
    });
    expect(p.toString()).toBe("x=1&q=ab&sort=newest&lib=L1&spk=A+b&year=2024");
    expect(askOf(p)).toEqual({
      sort: "newest",
      sel: { library: ["L1"], language: [], speaker: ["A b"], year: "2024" },
    });
  });

  it("no query leaves none of the search's parameters", () => {
    expect(
      bigSearchParams(
        new URLSearchParams("q=ab&sort=newest&dur=5m_20m"),
        null,
        plainAsk()
      ).toString()
    ).toBe("");
  });

  it("reads an unknown order as relevance and drops an unknown band", () => {
    expect(askOf(new URLSearchParams("sort=best&dur=forever&year=abc"))).toEqual(plainAsk());
  });

  it("askKey changes with the order and the filters only", () => {
    expect(askKey(plainAsk())).toBe(askKey(plainAsk()));
    expect(askKey({ sort: "newest", sel: noSel() })).not.toBe(askKey(plainAsk()));
    expect(askKey({ sort: "relevance", sel: { ...noSel(), language: ["ar"] } })).not.toBe(
      askKey(plainAsk())
    );
  });
});

describe("the answer's new fields, and their absence", () => {
  it("parses facets and did-you-mean, and tolerates an older home", () => {
    expect(facetsOf({ facets: FACETS })).toBe(FACETS);
    expect(facetsOf({})).toBeNull();
    expect(facetsOf(null)).toBeNull();
    expect(didYouMeanOf({ did_you_mean: " patience " }, "patiense")).toBe("patience");
    expect(didYouMeanOf({ did_you_mean: "ab" }, "ab")).toBe("");
    expect(didYouMeanOf({}, "ab")).toBe("");
    expect(facetGroups(null, noSel())).toEqual([]);
  });

  it("the suggestion kinds: items with their kinds, a title without a video dropped, plain texts", () => {
    expect(
      suggestItems({
        items: [
          { text: "a", kind: "history" },
          { text: "b", kind: "term" },
          { text: "c", kind: "title", video_id: "v1" },
          { text: "d", kind: "title" },
          { text: "e", kind: "speaker" },
          { text: "f", kind: "other" },
        ],
      })
    ).toEqual([
      { text: "a", kind: "history" },
      { text: "b", kind: "term" },
      { text: "c", kind: "title", video_id: "v1" },
      { text: "e", kind: "speaker" },
      { text: "f" },
    ]);
    expect(suggestItems({ suggestions: ["x", ""] })).toEqual([{ text: "x" }]);
  });

  it("a chosen bucket the narrowed answer dropped stays, without a count; the chips in use", () => {
    const sel = { ...noSel(), library: ["L2"], year: "2023" };
    const groups = facetGroups(
      { library: [{ value: "L1", label: "Tafsir", count: 2 }], year: FACETS.year },
      sel
    );
    expect(groups.map((g) => [g.facet, g.buckets.map((b) => `${b.value}:${b.count}`)])).toEqual([
      ["library", ["L2:-1", "L1:2"]],
      ["year", ["2023:4"]],
    ]);
    expect(activeChips(sel)).toEqual([
      { facet: "library", value: "L2" },
      { facet: "year", value: "2023" },
    ]);
  });
});
