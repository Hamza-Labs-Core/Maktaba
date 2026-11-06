import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { fixWord } = vi.hoisted(() => ({ fixWord: vi.fn() }));
vi.mock("../../lib/reviewApi", async () => {
  const actual = await vi.importActual<typeof import("../../lib/reviewApi")>("../../lib/reviewApi");
  return { ...actual, reviewApi: { ...actual.reviewApi, fixWord } };
});

import { I18nProvider } from "../../lib/i18n";
import { WordPicker, segmentWords, spanToCodePoints } from "./WordPicker";

beforeEach(() => fixWord.mockReset());

// Epic 52 (story 52.12): words of Arabic, English and mixed sentences, selected one at a
// time or as a span, sent as code points.
describe("WordPicker", () => {
  it("segments Arabic with harakat and a Latin word", () => {
    const words = segmentWords("قالَ النبيُّ hadith صحيح", "ar").filter((w) => w.word);
    expect(words.map((w) => w.text)).toEqual(["قالَ", "النبيُّ", "hadith", "صحيح"]);
  });
  it("sends code points past an emoji", () => {
    const text = "😀 the teh";
    expect(spanToCodePoints(text, text.indexOf("teh"), text.length)).toEqual({
      char_start: 6,
      char_len: 3,
    });
  });
  it("fixes a selected span and shows the new sentence", async () => {
    fixWord.mockResolvedValue({ segment_seq: 3, text: "the sky", flag_id: "f", edit_id: "e" });
    const user = userEvent.setup();
    const saved = vi.fn();
    const undo = vi.fn();
    render(
      <I18nProvider>
        <WordPicker
          videoId="v"
          seq={3}
          text="teh sk y"
          lang="en"
          onSaved={saved}
          onStale={vi.fn()}
          onUndoable={undo}
        />
      </I18nProvider>
    );
    await user.click(screen.getByText("sk"));
    await user.keyboard("{Shift>}");
    await user.click(screen.getByText("y"));
    await user.keyboard("{/Shift}");
    const field = screen.getByRole("textbox");
    expect(field).toHaveValue("sk y");
    await user.clear(field);
    await user.type(field, "sky");
    await user.click(screen.getByRole("button", { name: /save/i }));
    expect(fixWord).toHaveBeenCalledWith(
      "v",
      expect.objectContaining({
        segment_seq: 3,
        char_start: 4,
        char_len: 4,
        before: "sk y",
        text: "sky",
      })
    );
    expect(saved).toHaveBeenCalledWith("the sky");
    expect(undo).toHaveBeenCalledWith("e", expect.any(String));
  });
});
