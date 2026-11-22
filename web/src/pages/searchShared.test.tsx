// renderSnippet: the server's <mark> wrappers become <mark> elements, every
// other run stays plain text, and the whole snippet is one dir="auto" run so
// an Arabic title keeps its spaces in an LTR page.
import { describe, it, expect } from "vitest";
import { render } from "@testing-library/react";
import { renderSnippet, requestFilters, savedOf, selOfFilters } from "./searchShared";

describe("renderSnippet", () => {
  it("keeps the space between a plain word and a marked one, in one bidi run", () => {
    const { container } = render(<p>{renderSnippet("أقم <mark>الصلاة</mark>")}</p>);
    const runs = container.querySelectorAll("[dir]");
    expect(runs).toHaveLength(1);
    expect(runs[0].getAttribute("dir")).toBe("auto");
    expect(runs[0].textContent).toBe("أقم الصلاة");
    expect(container.querySelectorAll("mark")).toHaveLength(1);
    expect(container.querySelector("mark")?.textContent).toBe("الصلاة");
  });

  it("marks several words and leaves other markup as text", () => {
    const { container } = render(
      <p>{renderSnippet("<mark>الصلاة</mark> و <mark>الزكاة</mark> <b>x</b>")}</p>
    );
    expect(container.querySelectorAll("mark")).toHaveLength(2);
    expect(container.querySelector("b")).toBeNull();
    expect(container.textContent).toBe("الصلاة و الزكاة <b>x</b>");
  });

  it("renders nothing without a snippet", () => {
    expect(renderSnippet(null)).toBeNull();
    expect(renderSnippet(undefined)).toBeNull();
  });
});

// Story 49.10's follow-up: a recent or saved search's filters back into the page's selection.
describe("selOfFilters", () => {
  it("is the inverse of requestFilters for every facet", () => {
    const sel = {
      library: ["lib-1", "lib-2"],
      language: ["ar"],
      speaker: ["Sheikh Ahmad"],
      year: "2024",
      duration: "5m_20m",
    };
    expect(selOfFilters(requestFilters(sel))).toEqual(sel);
    expect(
      selOfFilters(requestFilters({ library: [], language: [], speaker: [], duration: "over_60m" }))
    ).toEqual({
      library: [],
      language: [],
      speaker: [],
      duration: "over_60m",
    });
  });

  it("leaves out what the page cannot show, and takes none", () => {
    const none = { library: [], language: [], speaker: [] };
    expect(selOfFilters(undefined)).toEqual(none);
    expect(selOfFilters(null)).toEqual(none);
    expect(selOfFilters("x")).toEqual(none);
    expect(
      selOfFilters({
        library_id: ["a", 3, "a", ""],
        date: { gte: "2024-03-01T00:00:00Z", lt: "2025-01-01T00:00:00Z" },
        duration_sec: { gte: 10, lt: 300 },
      })
    ).toEqual({ ...none, library: ["a"] });
    // a range with an inclusive end is not a bucket's
    expect(
      selOfFilters({ date: { gte: "2024-01-01T00:00:00Z", lt: "2025-01-01T00:00:00Z", lte: "x" } })
    ).toEqual(none);
  });
});

describe("savedOf", () => {
  it("reads a saved search's query, and the older shape", () => {
    expect(
      savedOf({
        id: "s1",
        name: "fox · Newest",
        query: { q: " fox ", mode: "fts", sort: "newest", filters: { language: ["en"] } },
      })
    ).toEqual({
      q: "fox",
      mode: "fts",
      name: "fox · Newest",
      sort: "newest",
      filters: { language: ["en"] },
    });
    expect(savedOf({ id: "s2", q: "dog", mode: "hybrid" })).toEqual({
      q: "dog",
      mode: "hybrid",
      name: "dog",
      sort: undefined,
      filters: undefined,
    });
    expect(savedOf({ id: "s3", name: "named only", query: null })).toMatchObject({
      q: "named only",
      mode: "hybrid",
    });
  });
});
