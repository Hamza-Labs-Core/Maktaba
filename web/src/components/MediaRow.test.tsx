// Epic 45 — MediaRow / MediaCard: a row of cards that scrolls sideways, its
// back / forward buttons (swapped under RTL), and the card's poster,
// progress and bidi-isolated title.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { I18nProvider } from "../lib/i18n";
import { MediaRow, MediaCard, VideoRow } from "./MediaRow";
import en from "../i18n/en.json";
import ar from "../i18n/ar.json";

const scrollBy = vi.fn();
const original = Element.prototype.scrollBy;

function renderRow(ui: React.ReactElement) {
  return render(
    <MemoryRouter>
      <I18nProvider>{ui}</I18nProvider>
    </MemoryRouter>
  );
}

const videos = [
  { id: "v1", title: "سورة البقرة", duration_sec: 3725, position_sec: 1862 },
  { id: "v2", title: null, filename: "lesson-2.mp4", duration_sec: 60 },
  { id: "v3", title: "Done", duration_sec: 100, position_sec: 99, completed: true },
];

describe("MediaRow", () => {
  beforeEach(() => {
    scrollBy.mockReset();
    Element.prototype.scrollBy = scrollBy as unknown as typeof Element.prototype.scrollBy;
  });
  afterEach(() => {
    Element.prototype.scrollBy = original;
    localStorage.removeItem("mkt:locale");
  });

  it("renders a titled region of cards linking to the videos", () => {
    renderRow(<VideoRow title="Recently Added" seeAllTo="/videos" videos={videos} />);
    const region = screen.getByRole("region", { name: "Recently Added" });
    const links = within(region)
      .getAllByRole("link")
      .map((a) => a.getAttribute("href"));
    expect(links).toEqual(["/videos", "/videos/v1", "/videos/v2", "/videos/v3"]);
    expect(within(region).getByRole("link", { name: en["home.seeAll"] })).toHaveAttribute(
      "href",
      "/videos"
    );
    // the poster is the video's, loaded lazily
    const img = region.querySelector("img.mkt-card__poster")!;
    expect(img).toHaveAttribute("src", "/api/videos/v1/poster");
    expect(img).toHaveAttribute("loading", "lazy");
    // titles are bidi-isolated; a card without a title shows its file name
    expect(screen.getByText("سورة البقرة").tagName).toBe("BDI");
    expect(screen.getByText("lesson-2.mp4").tagName).toBe("BDI");
    expect(within(region).getByText("1:02:05")).toBeInTheDocument();
  });

  it("shows how far into a video you are, not for a finished one", () => {
    renderRow(<VideoRow title="Continue" videos={videos} />);
    const bars = screen.getAllByRole("progressbar", { name: en["home.watched"] });
    expect(bars).toHaveLength(1);
    expect(bars[0]).toHaveAttribute("aria-valuenow", "50");
    expect(bars[0]).toHaveAttribute("aria-valuetext", "50% watched");
  });

  it("renders nothing when the row is empty", () => {
    const { container } = renderRow(<VideoRow title="Empty" videos={[]} />);
    expect(container).toBeEmptyDOMElement();
    renderRow(<MediaRow title="Also empty">{[]}</MediaRow>);
    expect(screen.queryByRole("region")).toBeNull();
  });

  it("links the title when the row has its own page", () => {
    renderRow(<VideoRow title="My library" titleTo="/library/l1" videos={videos} />);
    expect(screen.getByRole("heading", { level: 2 })).toContainElement(
      screen.getByRole("link", { name: "My library" })
    );
    expect(screen.getByRole("link", { name: "My library" })).toHaveAttribute("href", "/library/l1");
  });

  it("back / forward scroll the row; the buttons are keyboard reachable", async () => {
    renderRow(<VideoRow title="Row" videos={videos} />);
    const user = userEvent.setup();
    const back = screen.getByRole("button", { name: en["home.scrollBack"] });
    const fwd = screen.getByRole("button", { name: en["home.scrollForward"] });
    expect(back).toHaveTextContent("‹");
    expect(fwd).toHaveTextContent("›");
    fwd.focus();
    await user.keyboard("{Enter}");
    expect(scrollBy.mock.calls[0][0].left).toBeGreaterThan(0);
    await user.click(back);
    expect(scrollBy.mock.calls[1][0].left).toBeLessThan(0);
    // Tab from forward goes into the cards
    fwd.focus();
    await user.tab();
    expect(screen.getAllByRole("link")[0]).toHaveFocus();
  });

  it("under RTL the arrows swap and forward scrolls left", async () => {
    localStorage.setItem("mkt:locale", "ar");
    renderRow(<VideoRow title="صف" videos={videos} />);
    const user = userEvent.setup();
    const back = screen.getByRole("button", { name: ar["home.scrollBack"] });
    const fwd = screen.getByRole("button", { name: ar["home.scrollForward"] });
    expect(back).toHaveTextContent("›");
    expect(fwd).toHaveTextContent("‹");
    await user.click(fwd);
    expect(scrollBy.mock.calls[0][0].left).toBeLessThan(0);
    await user.click(back);
    expect(scrollBy.mock.calls[1][0].left).toBeGreaterThan(0);
  });

  it("a single card renders on its own", () => {
    renderRow(
      <ul>
        <MediaCard video={{ id: "x", filename: "x.mp4" }} />
      </ul>
    );
    expect(screen.getByRole("link", { name: "x.mp4" })).toHaveAttribute("href", "/videos/x");
    expect(screen.queryByRole("progressbar")).toBeNull();
  });
});
