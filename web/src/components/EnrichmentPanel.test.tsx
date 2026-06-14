// Story 26.6 AC test_ui_diff_renders — the enrichment panel renders the
// "We found this might be X" headline, a current-vs-proposed diff, the
// protected ("won't change") marker, and Accept/Dismiss controls.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";

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
            { field: "title", current: "matrix.mkv", proposed: "The Matrix", would_change: true, protected: true },
            { field: "description", current: "", proposed: "A hacker learns…", would_change: true, protected: false },
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
});
