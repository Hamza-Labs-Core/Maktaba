// Story 33.13 TC3: the bell - the unread count, the list (localised from key + params),
// opening a notice marks it read and follows its link, mark all read, Escape closes.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { I18nProvider } from "../lib/i18n";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});

import { NotificationsBell, notificationVars } from "./NotificationsBell";
import { notifications } from "../pages/Admin/LiveTV/changeFixtures";

function Where() {
  const loc = useLocation();
  return <p data-testid="where">{loc.pathname}</p>;
}

function renderBell(locale: "en" | "ar" = "en") {
  window.localStorage.setItem("mkt:locale", locale);
  return render(
    <I18nProvider>
      <MemoryRouter initialEntries={["/library"]}>
        <NotificationsBell />
        <Routes>
          <Route path="*" element={<Where />} />
        </Routes>
      </MemoryRouter>
    </I18nProvider>
  );
}

beforeEach(() => {
  window.localStorage.removeItem("mkt:locale");
  get.mockReset();
  post.mockReset();
  get.mockResolvedValue(notifications);
});

describe("NotificationsBell", () => {
  it("shows the unread count on the bell", async () => {
    renderBell();
    const bell = screen.getByTestId("notifications-bell");
    expect(await screen.findByText("2")).toBeInTheDocument();
    expect(bell).toHaveAccessibleName("Notifications, 2 unread");
    expect(get).toHaveBeenCalledWith("/api/notifications?limit=30");
  });

  it("lists the notices localised from their key and params, newest first", async () => {
    renderBell();
    await screen.findByText("2");
    await userEvent.setup().click(screen.getByTestId("notifications-bell"));
    const panel = screen.getByRole("dialog", { name: "Notifications" });
    const items = within(panel)
      .getAllByRole("button")
      .filter((b) => b.className.includes("mkt-bell__item"));
    expect(items).toHaveLength(3);
    expect(items[0]).toHaveTextContent(
      "TV change #42 passed its checks and needs your approval: Ramadan grid and the Seerah channel"
    );
    expect(items[0]).toHaveClass("mkt-bell__item--unread");
    expect(items[1]).toHaveTextContent("failed verification (1 checks failed)");
    expect(items[2]).not.toHaveClass("mkt-bell__item--unread");
  });

  it("opening a notice marks it read and follows its link", async () => {
    renderBell();
    await screen.findByText("2");
    const user = userEvent.setup();
    await user.click(screen.getByTestId("notifications-bell"));
    post.mockResolvedValueOnce({ unread: 1 });
    await user.click(screen.getByText(/needs your approval/));
    expect(post).toHaveBeenCalledWith(`/api/notifications/${notifications.items[0]!.id}/read`);
    // the bell follows the link once the read has answered, and the router's
    // location update is a transition: it can land after the click resolves
    await waitFor(() =>
      expect(screen.getByTestId("where")).toHaveTextContent(notifications.items[0]!.link)
    );
    expect(screen.getByTestId("notifications-bell")).toHaveAccessibleName(
      "Notifications, 1 unread"
    );
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("marks all read", async () => {
    renderBell();
    await screen.findByText("2");
    const user = userEvent.setup();
    await user.click(screen.getByTestId("notifications-bell"));
    post.mockResolvedValueOnce({ unread: 0 });
    await user.click(screen.getByRole("button", { name: "Mark all read" }));
    expect(post).toHaveBeenCalledWith("/api/notifications/read-all");
    expect(screen.getByTestId("notifications-bell")).toHaveAccessibleName("Notifications");
    expect(screen.getByRole("button", { name: "Mark all read" })).toBeDisabled();
  });

  it("closes on Escape and says when there is nothing", async () => {
    get.mockResolvedValue({ unread: 0, items: [] });
    renderBell();
    const user = userEvent.setup();
    await user.click(screen.getByTestId("notifications-bell"));
    expect(await screen.findByText("Nothing new.")).toBeInTheDocument();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("speaks Arabic", async () => {
    renderBell("ar");
    await screen.findByTestId("notifications-count");
    await userEvent.setup().click(screen.getByTestId("notifications-bell"));
    expect(screen.getByRole("dialog", { name: "الإشعارات" })).toHaveTextContent(
      "اجتاز تغيير البث رقم ٤٢ فحوصه وينتظر اعتمادك"
    );
  });

  it("joins list params and keeps numbers", () => {
    expect(notificationVars({ kinds: ["lineup", "art"], number: 3, ok: true, x: null })).toEqual({
      kinds: "lineup, art",
      number: 3,
      ok: "yes",
    });
  });
});
