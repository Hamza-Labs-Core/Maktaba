// The video's on-air title (Story 27.11): shown with where it came from;
// an admin sets one by hand (it wins) or goes back to the automatic one.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "../lib/i18n";

const { patch } = vi.hoisted(() => ({ patch: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, patch } };
});

import { DisplayTitle, type DisplayTitleVideo } from "./DisplayTitle";

const AUTO = "في الخنادق… لا يوجد ملحدون!";
const auto: DisplayTitleVideo = {
  id: "v1",
  display_title: AUTO,
  display_title_auto: AUTO,
  display_title_source: "rules",
};

describe("DisplayTitle", () => {
  beforeEach(() => patch.mockReset());

  it("shows the automatic title and where it came from, read only for viewers", () => {
    render(
      <I18nProvider>
        <DisplayTitle video={auto} canEdit={false} onSaved={() => {}} />
      </I18nProvider>
    );
    expect(screen.getByText(AUTO)).toBeInTheDocument();
    expect(screen.getByText("by the rules")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Change" })).toBeNull();
  });

  it("sets a title by hand and goes back to the automatic one", async () => {
    const user = userEvent.setup();
    const onSaved = vi.fn();
    const manual = {
      ...auto,
      display_title: "لا ملحدين في الخنادق",
      display_title_manual: "لا ملحدين في الخنادق",
      display_title_source: "manual",
    };
    patch.mockResolvedValueOnce(manual);
    const { rerender } = render(
      <I18nProvider>
        <DisplayTitle video={auto} canEdit onSaved={onSaved} />
      </I18nProvider>
    );
    await user.click(screen.getByRole("button", { name: "Change" }));
    const box = screen.getByLabelText("On-air title");
    await user.clear(box);
    await user.type(box, "لا ملحدين في الخنادق");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(patch).toHaveBeenCalledWith("/api/videos/v1", { display_title: "لا ملحدين في الخنادق" });
    expect(onSaved).toHaveBeenCalledWith(manual);

    rerender(
      <I18nProvider>
        <DisplayTitle video={manual} canEdit onSaved={onSaved} />
      </I18nProvider>
    );
    expect(screen.getByText("set by hand")).toBeInTheDocument();
    expect(screen.getByText(`(automatic: ${AUTO})`)).toBeInTheDocument();
    patch.mockResolvedValueOnce(auto);
    await user.click(screen.getByRole("button", { name: "Back to the automatic title" }));
    expect(patch).toHaveBeenLastCalledWith("/api/videos/v1", { display_title: null });
  });
});
