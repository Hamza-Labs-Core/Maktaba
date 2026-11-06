// Epic 53 story 53.06: the strip shows the highest line, "N more" for the rest, Details
// for admins; closed, it comes back when the state changes; in Arabic, Arabic.
import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { authState } = vi.hoisted(() => ({
  authState: { user: null as null | { is_admin: boolean } },
}));
vi.mock("../lib/auth", () => ({ useAuth: () => authState }));

import { I18nProvider } from "../lib/i18n";
import { setHomeStatus, type HomeStatus } from "../lib/homeStatus";
import { StatusStrip } from "./StatusStrip";

const base: HomeStatus = {
  v: 1,
  at: "2026-11-06T10:00:00Z",
  mode: "normal",
  overall: "ok",
  components: {
    database: "ok",
    api: "ok",
    streaming: "ok",
    processing: "ok",
    search: "ok",
    logs: "off",
  },
  features_down: [],
};

beforeEach(() => {
  vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("", { status: 404 }));
  authState.user = { is_admin: false };
});
afterEach(() => {
  setHomeStatus(null);
  vi.restoreAllMocks();
});

function show(s: HomeStatus | null) {
  setHomeStatus(s);
  return render(
    <I18nProvider>
      <StatusStrip />
    </I18nProvider>
  );
}

describe("StatusStrip", () => {
  it("shows nothing when all is well", () => {
    show(base);
    expect(screen.queryByTestId("status-strip")).toBeNull();
  });

  it("says playback is down, with one more line behind a tap", async () => {
    show({
      ...base,
      overall: "degraded",
      components: { ...base.components, streaming: "down", search: "down" },
    });
    expect(screen.getByText("Playback and Live TV are unavailable right now.")).toBeInTheDocument();
    expect(screen.queryByText("Search is unavailable right now.")).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "1 more" }));
    expect(screen.getByText("Search is unavailable right now.")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "Details" })).toBeNull();
  });

  it("gives an admin Details to the recovery page when the API is down", () => {
    authState.user = { is_admin: true };
    show({ ...base, overall: "down", components: { ...base.components, api: "restarting" } });
    expect(screen.getByText(/Maktaba is restarting/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Details" })).toHaveAttribute("href", "/recovery");
  });

  it("closed, comes back when the state changes", async () => {
    show({ ...base, mode: "safe" });
    await userEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(screen.queryByTestId("status-strip")).toBeNull();
    act(() =>
      setHomeStatus({ ...base, mode: "safe", components: { ...base.components, search: "down" } })
    );
    expect(screen.getByTestId("status-strip")).toBeInTheDocument();
  });

  it("is Arabic in Arabic", () => {
    localStorage.setItem("mkt:locale", "ar");
    show({ ...base, overall: "degraded", components: { ...base.components, streaming: "down" } });
    const strip = screen.getByTestId("status-strip");
    localStorage.removeItem("mkt:locale");
    expect(strip.textContent).toMatch(/التشغيل والبث المباشر غير متاحين الآن/);
  });
});
