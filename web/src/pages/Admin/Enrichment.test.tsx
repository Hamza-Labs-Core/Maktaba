// Story 26.6 — the review queue: j/k move, a/d accept/dismiss the selected
// match, and the queue moves on to the next video after a decision.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

import { AdminEnrichment } from "./Enrichment";
import { I18nProvider } from "../../lib/i18n";
import { ToastProvider } from "@ds/components/Toast/Toast";

const A = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa";
const B = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb";

const row = (id: string, title: string) => ({
  video_id: id,
  library_id: "lib",
  video_title: title,
  candidate_title: `Match ${title}`,
  provider: "tmdb",
  confidence: 0.8,
});
const candidates = (id: string) => ({
  candidates: [
    { id: `c-${id}`, provider: "tmdb", external_id: `ext-${id}`, confidence: 0.8, accepted: false },
  ],
  revertable: [],
});

function selectedTitle(): string | null {
  const sel = screen.getAllByRole("option").find((o) => o.getAttribute("aria-selected") === "true");
  return sel?.textContent ?? null;
}

describe("AdminEnrichment", () => {
  let pending: ReturnType<typeof row>[];
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
    pending = [row(A, "Alpha"), row(B, "Beta")];
    get.mockImplementation((url: string) => {
      if (url === "/api/enrichment/pending") return Promise.resolve({ items: pending });
      if (url.includes(A)) return Promise.resolve(candidates(A));
      return Promise.resolve(candidates(B));
    });
    post.mockImplementation((url: string) => {
      if (url.includes("/dismiss") || url.includes("/accept")) {
        pending = pending.filter((p) => !url.includes(p.video_id));
      }
      return Promise.resolve({});
    });
  });

  function renderPage() {
    return render(
      <I18nProvider>
        <ToastProvider>
          <AdminEnrichment />
        </ToastProvider>
      </I18nProvider>
    );
  }

  it("d dismisses the selected match and selects the next video", async () => {
    renderPage();
    await waitFor(() => expect(selectedTitle()).toContain("Alpha"));
    await screen.findByText(/We found this might be/);
    fireEvent.keyDown(window, { key: "d" });
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(`/api/videos/${A}/enrichment/dismiss`, {
        external_id: `ext-${A}`,
      })
    );
    await waitFor(() => expect(screen.getAllByRole("option")).toHaveLength(1));
    expect(selectedTitle()).toContain("Beta");
  });

  it("a accepts the selected match", async () => {
    renderPage();
    await screen.findByText(/We found this might be/);
    fireEvent.keyDown(window, { key: "j" });
    await waitFor(() => expect(selectedTitle()).toContain("Beta"));
    await waitFor(() => expect(get).toHaveBeenCalledWith(`/api/videos/${B}/enrichment`));
    await screen.findByText(/We found this might be/);
    fireEvent.keyDown(window, { key: "a" });
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(`/api/videos/${B}/enrichment/accept`, {
        external_id: `ext-${B}`,
      })
    );
    await waitFor(() => expect(screen.getAllByRole("option")).toHaveLength(1));
    expect(selectedTitle()).toContain("Alpha");
  });

  it("j / k do nothing while typing in the search box", async () => {
    renderPage();
    await screen.findByText(/We found this might be/);
    fireEvent.keyDown(screen.getByRole("searchbox"), { key: "j" });
    expect(selectedTitle()).toContain("Alpha");
  });
});
