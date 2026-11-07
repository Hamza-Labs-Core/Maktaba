// Epic 50 story 50.11 (T05, T08 for the cloud web app): the strip says when Maktaba Cloud's
// relay or sign-in is in trouble, links to the status page, closes per incident, is Arabic
// in Arabic, and shows nothing when the feed can't be read.
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setCloudStatus, type CloudSummary } from "../../src/lib/cloudStatus";
import { I18nProvider } from "./i18n";
import { StatusStrip } from "./StatusStrip";

const apiDown: CloudSummary = {
  updated_at: "2026-11-03T10:15:00Z",
  overall: "outage",
  components: { relay: "operational", api: "outage", push: "operational" },
  incident: {
    id: "2026-11-03-api-down",
    state: "identified",
    components: ["api"],
    title: { en: "Sign-in is down", ar: "تسجيل الدخول متوقف" },
    url: "https://status.maktaba.tv/i/2026-11-03-api-down",
  },
  maintenance: [],
};

function show() {
  return render(
    <I18nProvider>
      <StatusStrip />
    </I18nProvider>
  );
}

afterEach(() => {
  setCloudStatus(null);
  vi.unstubAllGlobals();
  localStorage.clear();
});

describe("cloud StatusStrip", () => {
  it("says sign-in is down, with Details to the incident", () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("", { status: 404 })));
    setCloudStatus(apiDown);
    show();
    expect(
      screen.getByText(/Signing in with Maktaba Cloud isn't working right now/)
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Details" })).toHaveAttribute(
      "href",
      "https://status.maktaba.tv/i/2026-11-03-api-down"
    );
  });

  it("closed, stays closed for that incident and comes back for the next", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("", { status: 404 })));
    setCloudStatus(apiDown);
    show();
    await userEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(screen.queryByTestId("cloud-status-strip")).toBeNull();
    act(() =>
      setCloudStatus({
        ...apiDown,
        incident: null,
        components: { relay: "outage", api: "operational" },
      })
    );
    expect(screen.getByText(/Remote access is down/)).toBeInTheDocument();
  });

  it("shows nothing when summary.json can't be read", async () => {
    const f = vi.fn().mockRejectedValue(new TypeError("Failed to fetch"));
    vi.stubGlobal("fetch", f);
    show();
    await act(async () => {});
    expect(f).toHaveBeenCalled();
    expect(String(f.mock.calls[0][0])).toMatch(
      /^https:\/\/status\.maktaba\.tv\/summary\.json\?t=\d+$/
    );
    expect(screen.queryByTestId("cloud-status-strip")).toBeNull();
  });

  it("is Arabic in Arabic", () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("", { status: 404 })));
    localStorage.setItem("mkt:cloud:locale", "ar");
    setCloudStatus(apiDown);
    show();
    expect(screen.getByTestId("cloud-status-strip").textContent).toMatch(
      /تسجيل الدخول عبر سحابة مكتبة لا يعمل الآن/
    );
    expect(screen.getByRole("link", { name: "التفاصيل" })).toBeInTheDocument();
  });
});
