// Story 26.6 AC test_ui_diff_renders — the enrichment panel renders the
// "We found this might be X" headline, a current-vs-proposed diff, the
// protected ("won't change") marker, and Accept/Dismiss controls.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

import { EnrichmentPanel } from "./EnrichmentPanel";
import { I18nProvider } from "../lib/i18n";
import { ToastProvider } from "@ds/components/Toast/Toast";

const VID = "11111111-1111-1111-1111-111111111111";

function renderPanel(props: { shortcuts?: boolean; onDecided?: (a: string) => void } = {}) {
  return render(
    <I18nProvider>
      <ToastProvider>
        <EnrichmentPanel videoId={VID} {...props} />
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

  const oneCandidate = {
    candidates: [
      {
        id: "c1",
        provider: "tmdb",
        external_id: "tmdb:movie:603",
        confidence: 0.92,
        accepted: false,
        title: "The Matrix",
        fields: [],
      },
    ],
  };

  it("hides Revert to original when no accept wrote fields", async () => {
    get.mockResolvedValue({ ...oneCandidate, revertable: [] });
    renderPanel();
    await waitFor(() => expect(screen.getByText(/We found this might be/)).toBeInTheDocument());
    expect(screen.queryByRole("button", { name: "Revert to original" })).not.toBeInTheDocument();
  });

  it("reverts to the original through the revert endpoint", async () => {
    get.mockResolvedValueOnce({ ...oneCandidate, revertable: ["title", "description"] });
    get.mockResolvedValue({ ...oneCandidate, revertable: [] });
    post.mockResolvedValue({ reverted: ["title", "description"] });
    const onDecided = vi.fn();
    renderPanel({ onDecided });
    const btn = await screen.findByRole("button", { name: "Revert to original" });
    expect(screen.getByText(/Title, Description/)).toBeInTheDocument();
    fireEvent.click(btn);
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(`/api/videos/${VID}/enrichment/revert`, {})
    );
    await waitFor(() => expect(onDecided).toHaveBeenCalledWith("revert"));
    expect(await screen.findByText("Restored the original values.")).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: "Revert to original" })).not.toBeInTheDocument()
    );
  });

  it("accepts with a and dismisses with d when shortcuts are on", async () => {
    get.mockResolvedValue(oneCandidate);
    post.mockResolvedValue({});
    const onDecided = vi.fn();
    renderPanel({ shortcuts: true, onDecided });
    await waitFor(() => expect(screen.getByText(/We found this might be/)).toBeInTheDocument());
    fireEvent.keyDown(window, { key: "a" });
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(`/api/videos/${VID}/enrichment/accept`, {
        external_id: "tmdb:movie:603",
      })
    );
    await waitFor(() => expect(onDecided).toHaveBeenCalledWith("accept"));
    await waitFor(() => expect(screen.getByRole("button", { name: "Accept" })).not.toBeDisabled());
    fireEvent.keyDown(window, { key: "d" });
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(`/api/videos/${VID}/enrichment/dismiss`, {
        external_id: "tmdb:movie:603",
      })
    );
  });

  it("ignores the shortcut keys while typing in the search box", async () => {
    get.mockResolvedValue(oneCandidate);
    renderPanel({ shortcuts: true });
    await waitFor(() => expect(screen.getByText(/We found this might be/)).toBeInTheDocument());
    fireEvent.keyDown(screen.getByRole("searchbox"), { key: "a" });
    expect(post).not.toHaveBeenCalled();
  });

  it("does not react to a / d without shortcuts", async () => {
    get.mockResolvedValue(oneCandidate);
    renderPanel();
    await waitFor(() => expect(screen.getByText(/We found this might be/)).toBeInTheDocument());
    fireEvent.keyDown(window, { key: "a" });
    expect(post).not.toHaveBeenCalled();
  });
});
