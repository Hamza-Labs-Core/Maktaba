// Epic 45 Phase 3 (story 45.20) — the grid's Length, Added and Resolution
// filters: offered from the facets' bands, kept in the URL, sent to the server.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useLocation } from "react-router-dom";
import { renderPage, memberUser } from "../test/pageHarness";
import en from "../i18n/en.json";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});

import { LibraryBrowser } from "./LibraryBrowser";

const LIB = "11111111-1111-4111-8111-111111111111";

const FACETS = {
  languages: [],
  tags: [],
  speakers: [],
  letters: [],
  durations: [
    { value: "short", videos: 3 },
    { value: "long", videos: 9 },
  ],
  added: [{ value: "year", videos: 4 }],
  resolutions: [],
};

const VIDEOS = {
  items: [{ id: "v1", title: "Lesson one", filename: "1.mp4", state: "ready", duration_sec: 100 }],
  next: null,
};

function serve() {
  get.mockImplementation((url: string) => {
    if (url.startsWith("/api/videos/facets")) return Promise.resolve(FACETS);
    if (url.startsWith("/api/videos?")) return Promise.resolve(VIDEOS);
    return Promise.reject(new Error(`unmocked GET ${url}`));
  });
}

const lastQuery = () => {
  const calls = get.mock.calls
    .map((c) => c[0] as string)
    .filter((u) => u.startsWith("/api/videos?"));
  return new URLSearchParams(calls.at(-1)!.split("?")[1]);
};

function Loc() {
  const { search } = useLocation();
  return <span data-testid="loc">{search}</span>;
}

function renderGrid(path = `/library/${LIB}`) {
  return renderPage(
    <>
      <LibraryBrowser />
      <Loc />
    </>,
    { path, route: "/library/:libraryId", user: memberUser }
  );
}

describe("LibraryBrowser, Phase 3 filters", () => {
  beforeEach(() => {
    get.mockReset();
    localStorage.clear();
  });

  it("offers only the bands with videos, and filters by them", async () => {
    serve();
    renderGrid();
    await screen.findByText("Lesson one");
    const length = await screen.findByRole("combobox", { name: en["browse.filter.duration"] });
    const options = [...(length as HTMLSelectElement).options].map((o) => o.value);
    expect(options).toEqual(["", "short", "long"]);
    // no resolution has videos: no Resolution filter
    expect(screen.queryByRole("combobox", { name: en["browse.filter.resolution"] })).toBeNull();

    await userEvent.selectOptions(length, "long");
    await waitFor(() => expect(lastQuery().get("duration")).toBe("long"));
    expect(screen.getByTestId("loc").textContent).toContain("duration=long");

    const addedSel = screen.getByRole("combobox", { name: en["browse.filter.added"] });
    await userEvent.selectOptions(addedSel, "year");
    await waitFor(() => expect(lastQuery().get("added")).toBe("year"));
  });

  it("keeps a band from a link, even one the facets do not list, and ignores an unknown one", async () => {
    serve();
    renderGrid(`/library/${LIB}?resolution=uhd&duration=tiny`);
    await screen.findByText("Lesson one");
    expect(lastQuery().get("resolution")).toBe("uhd");
    expect(lastQuery().get("duration")).toBeNull();
    const res = await screen.findByRole("combobox", { name: en["browse.filter.resolution"] });
    expect((res as HTMLSelectElement).value).toBe("uhd");
  });
});
