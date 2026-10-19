// Story 26.6 AC test_ui_diff_renders — the enrichment panel renders the
// "We found this might be X" headline, a current-vs-proposed diff, the
// protected ("won't change") marker, and Accept/Dismiss controls.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

import { EnrichmentPanel } from "./EnrichmentPanel";
import { I18nProvider } from "../lib/i18n";
import { ToastProvider } from "@ds/components/Toast/Toast";

function renderPanel() {
  return render(
    <I18nProvider>
      <ToastProvider>
        <EnrichmentPanel videoId="11111111-1111-1111-1111-111111111111" />
      </ToastProvider>
    </I18nProvider>
  );
}

describe("EnrichmentPanel", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
  });

  it("renders the headline, diff, and protected marker", async () => {
    get.mockResolvedValue({
      candidates: [
        {
          id: "c1",
          provider: "tmdb",
          external_id: "tmdb:movie:603",
          confidence: 0.92,
          accepted: false,
          title: "The Matrix",
          year: 1999,
          fields: [
            {
              field: "title",
              current: "matrix.mkv",
              proposed: "The Matrix",
              would_change: true,
              protected: true,
            },
            {
              field: "description",
              current: "",
              proposed: "A hacker learns…",
              would_change: true,
              protected: false,
            },
          ],
        },
      ],
    });
    renderPanel();
    await waitFor(() => expect(screen.getByText(/We found this might be/)).toBeInTheDocument());
    // Confidence shown as a percentage.
    expect(screen.getByText(/92% match/)).toBeInTheDocument();
    // Proposed value present.
    expect(screen.getByText("A hacker learns…")).toBeInTheDocument();
    // Protected field marked "won't change".
    expect(screen.getByText(/Won't change/)).toBeInTheDocument();
  });

  it("shows an empty state with a manual-search CTA when there are no candidates", async () => {
    get.mockResolvedValue({ candidates: [] });
    renderPanel();
    await waitFor(() => expect(screen.getByText(/No suggestions/)).toBeInTheDocument());
    expect(screen.getByRole("button", { name: /Search manually/ })).toBeInTheDocument();
  });

  it("shows each field's confidence and sources, and the facts", async () => {
    get.mockResolvedValue({
      job: { status: "done", origin: "manual" },
      candidates: [
        {
          id: "w1",
          provider: "web",
          external_id: "web:abc",
          confidence: 0.9,
          accepted: false,
          title: "شرح بلوغ المرام 3",
          fields: [
            {
              field: "title",
              current: "bulugh_03",
              proposed: "شرح بلوغ المرام 3",
              would_change: true,
              protected: false,
              confidence: 0.9,
              sources: ["S1", "I1"],
            },
          ],
          facts: [{ field: "book_author", value: "ابن حجر", confidence: 0.8, sources: ["S1"] }],
          sources: [
            { id: "S1", label: "Wikipedia", url: "https://ar.wikipedia.org/x" },
            { id: "I1", label: "File and folder names", kind: "file" },
          ],
        },
        {
          id: "c0",
          provider: "youtube",
          external_id: "yt1",
          confidence: 1,
          accepted: true,
          fields: [],
        },
      ],
    });
    renderPanel();
    await waitFor(() => expect(screen.getByText("ابن حجر")).toBeInTheDocument());
    expect(screen.getByText("Book author")).toBeInTheDocument();
    expect(screen.getByText("Web search")).toBeInTheDocument();
    const link = screen.getByRole("link", { name: "Wikipedia" });
    expect(link).toHaveAttribute("href", "https://ar.wikipedia.org/x");
    expect(link).toHaveAttribute("rel", expect.stringContaining("noopener"));
    expect(screen.getByText("File and folder names")).toBeInTheDocument();
    expect(screen.getAllByText("[S1]").length).toBeGreaterThan(1);
    // the YouTube link row is not a suggestion
    expect(screen.queryByText(/yt1/)).not.toBeInTheDocument();
  });

  it("queues a manual search and shows the job until it is done", async () => {
    get.mockResolvedValueOnce({ candidates: [] });
    post.mockResolvedValue({
      queued: true,
      candidates: [],
      job: { status: "pending", origin: "manual" },
    });
    renderPanel();
    await waitFor(() => expect(screen.getByText(/No suggestions/)).toBeInTheDocument());
    fireEvent.change(screen.getByRole("searchbox"), { target: { value: "بلوغ المرام" } });
    fireEvent.click(screen.getByRole("button", { name: /Search manually/ }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(
        "/api/videos/11111111-1111-1111-1111-111111111111/enrichment/search",
        { query: "بلوغ المرام" }
      )
    );
    await waitFor(() =>
      expect(document.querySelector(".mkt-enrich-panel__job")).toHaveTextContent(/Searching/)
    );
  });

  it("explains a search waiting for tomorrow's budget", async () => {
    get.mockResolvedValue({
      candidates: [],
      job: {
        status: "deferred",
        origin: "reenrich",
        last_error: "budget:search",
        not_before: "2026-10-20T00:00:05Z",
      },
    });
    renderPanel();
    await waitFor(() =>
      expect(document.querySelector(".mkt-enrich-panel__job")).toHaveTextContent(
        /today's web searches are used up/
      )
    );
  });

  it("searches again from what Maktaba knows", async () => {
    get.mockResolvedValue({ candidates: [] });
    post.mockResolvedValue({ enqueued: true, job: { status: "pending", origin: "reenrich" } });
    renderPanel();
    await waitFor(() => expect(screen.getByText(/No suggestions/)).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /Search again from what Maktaba knows/ }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(
        "/api/videos/11111111-1111-1111-1111-111111111111/enrichment/reenrich",
        {}
      )
    );
  });
});
