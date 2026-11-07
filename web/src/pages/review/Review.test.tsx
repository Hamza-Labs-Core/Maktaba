import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, act } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { ToastProvider } from "@ds/components/Toast/Toast";

const m = vi.hoisted(() => ({
  flags: vi.fn(),
  flag: vi.fn(),
  clip: vi.fn(),
  resolve: vi.fn(),
  undoEdit: vi.fn(),
  undoBatch: vi.fn(),
  summary: vi.fn(),
}));
vi.mock("../../lib/reviewApi", async () => {
  const actual = await vi.importActual<typeof import("../../lib/reviewApi")>("../../lib/reviewApi");
  return { ...actual, reviewApi: { ...actual.reviewApi, ...m } };
});

import { I18nProvider } from "../../lib/i18n";
import { ApiError } from "../../lib/api";
import { Review } from "./Review";
import type { FlagDetail } from "../../lib/reviewApi";

function flag(id: string, extra: Partial<FlagDetail> = {}): FlagDetail {
  return {
    id,
    kind: "word",
    transcript_id: "t",
    video_id: "v",
    video_title: "درس",
    library_id: "l",
    segment_seq: 0,
    word_index: 1,
    word_count: 1,
    char_start: 4,
    char_len: 6,
    start_sec: 1,
    end_sec: 1.5,
    original: "الصلوة",
    context: "قال الصلوة خير",
    language: "ar",
    direction: "rtl",
    reasons: ["not_in_dictionary"],
    scores: {},
    priority: 0.7,
    candidates: [
      { text: "الصلوات", source: "dictionary", score: 0.5 },
      { text: "الصلاة", source: "dictionary", score: 0.5 },
    ],
    key: "الصلوه",
    status: "open",
    resolution: null,
    locked_by: null,
    locked_by_name: null,
    locked_until: null,
    created_at: "2026-11-06T00:00:00Z",
    before: "",
    after: "",
    same_key_open: 0,
    same_key_preview: [],
    verse: null,
    reviewers_note: "",
    ...extra,
  };
}

function page() {
  return render(
    <I18nProvider>
      <ToastProvider>
        <MemoryRouter initialEntries={["/review"]}>
          <Review />
        </MemoryRouter>
      </ToastProvider>
    </I18nProvider>
  );
}

beforeEach(() => {
  for (const f of Object.values(m)) f.mockReset();
  HTMLMediaElement.prototype.play = vi.fn(() => Promise.resolve());
  HTMLMediaElement.prototype.pause = vi.fn();
  m.clip.mockResolvedValue({ url: "/stream/clip/v?sig=x", start: 0, end: 3, expires_at: "" });
  m.summary.mockResolvedValue({
    open: 0,
    libraries: [],
    languages: {},
    reasons: {},
    last_check: null,
  });
});

describe("Review", () => {
  it("corrects with the second candidate on 2, then undoes with U", async () => {
    m.flags.mockResolvedValue({
      items: [flag("a"), flag("b", { original: "teh", language: "en", direction: "ltr" })],
      next_cursor: null,
    });
    m.flag.mockImplementation((id: string) =>
      Promise.resolve(id === "a" ? flag("a") : flag("b", { original: "teh" }))
    );
    m.resolve.mockResolvedValue({ status: "corrected", changed: 1, edit_id: "e1", batch_id: null });
    m.undoEdit.mockResolvedValue({ undone: 1 });
    page();
    expect(await screen.findByLabelText("Spelling")).toHaveValue("الصلوة");
    // the sentence reads right to left; the word is marked
    expect(screen.getByLabelText(/الصلوة/, { selector: "mark" })).toBeInTheDocument();
    await act(async () => {
      window.dispatchEvent(new KeyboardEvent("keydown", { code: "Digit2", key: "2" }));
    });
    expect(m.resolve).toHaveBeenCalledWith(
      "a",
      expect.objectContaining({ action: "correct", text: "الصلاة" })
    );
    expect(await screen.findByLabelText("Spelling")).toHaveValue("teh");
    await act(async () => {
      window.dispatchEvent(new KeyboardEvent("keydown", { code: "KeyU", key: "u" }));
    });
    expect(m.undoEdit).toHaveBeenCalledWith("e1");
  });

  it("marks a name with the physical N key on an Arabic layout", async () => {
    m.flags.mockResolvedValue({ items: [flag("a")], next_cursor: null });
    m.flag.mockResolvedValue(flag("a"));
    m.resolve.mockResolvedValue({ status: "name", changed: 1, edit_id: null, batch_id: null });
    page();
    await screen.findByLabelText("Spelling");
    // no flush before the key: the page's listener reads the word from the render on
    // screen (#697), and the API call is made in the key's handler, so it is checked at once
    await act(async () => {
      window.dispatchEvent(new KeyboardEvent("keydown", { code: "KeyN", key: "ى" }));
    });
    expect(m.resolve).toHaveBeenCalledWith("a", expect.objectContaining({ action: "name" }));
  });

  it("shows the verse, its word and the Tanzil credit, and no dictionary button", async () => {
    const q = flag("q", {
      reasons: ["quran_mismatch"],
      verse: {
        surah: 1,
        aya: 2,
        label_ar: "الفاتحة ٢",
        label_en: "Al-Faatiha 1:2",
        expected: "العالمين",
        kind: "sub",
      },
    });
    m.flags.mockResolvedValue({ items: [q], next_cursor: null });
    m.flag.mockResolvedValue(q);
    page();
    expect(await screen.findByText("Al-Faatiha 1:2")).toBeInTheDocument();
    expect(screen.getByText(/The verse has: العالمين/)).toBeInTheDocument();
    expect(screen.getByText(/Tanzil Project/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Add to dictionary/ })).toBeNull();
  });

  it("says only home admins review, when the API refuses", async () => {
    m.flags.mockRejectedValue(new ApiError({ type: "x", title: "forbidden", status: 403 }));
    page();
    expect(await screen.findByRole("alert")).toHaveTextContent("Only home admins can review words");
  });

  it("says there is nothing to review", async () => {
    m.flags.mockResolvedValue({ items: [], next_cursor: null });
    page();
    expect(await screen.findByText(/No words to review/)).toBeInTheDocument();
  });
});
