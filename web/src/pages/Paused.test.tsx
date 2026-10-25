// Epic 35, story 35.10 TC6: the paused page on a node (reasons, the link to
// the home, the reload when the node's version changes; en + ar).
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, render, screen } from "@testing-library/react";
import { I18nProvider } from "../lib/i18n";
import type { ClusterSelf } from "../lib/clusterApi";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});

import { PAUSED_POLL_MS, PAUSED_RELOAD_EVERY_MS, Paused } from "./Paused";

const WHY = "paused: home database at 0130, this machine needs 0134 — update the home";

function self(over: Partial<ClusterSelf> = {}): ClusterSelf {
  return {
    role: "node",
    id: "n1",
    name: "Studio Mac",
    version: "1.5.0",
    schema_rev: 134,
    sso: true,
    paused: WHY,
    home: { name: "Home NAS", web_url: "http://home.local:8780", reachable: true },
    served_at: new Date().toISOString(),
    ...over,
  };
}

function renderPaused(props: Parameters<typeof Paused>[0]) {
  return render(
    <I18nProvider>
      <Paused {...props} />
    </I18nProvider>
  );
}

describe("Paused page", () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    get.mockReset();
    vi.useFakeTimers({ shouldAdvanceTime: true });
  });
  afterEach(() => {
    vi.useRealTimers();
    document.documentElement.dir = "ltr";
  });

  it("names the schema guard's reason and links to the home", async () => {
    get.mockResolvedValue(self());
    renderPaused({ code: "paused", reason: WHY, onReload: vi.fn() });
    expect(screen.getByRole("heading", { name: "This machine is paused" })).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent(
      "paused: home database at 0130, this machine needs 0134 — update the home"
    );
    const link = await screen.findByRole("link", { name: "http://home.local:8780" });
    expect(link).toHaveAttribute("href", "http://home.local:8780");
    expect(screen.getByText(/Use Maktaba on .*Home NAS/)).toBeInTheDocument();
    expect(get).toHaveBeenCalledWith("/api/cluster/self");
    // what it means, in the help on the home (this machine's own pages are paused)
    expect(screen.getByTestId("paused-help")).toHaveAttribute(
      "href",
      "http://home.local:8780/help/machines#paused-page"
    );
  });

  it("says when the machine cannot reach its home", async () => {
    get.mockResolvedValue(
      self({
        paused: "",
        home: { name: "Home NAS", web_url: "http://home.local", reachable: false },
      })
    );
    renderPaused({ code: "home_unreachable", onReload: vi.fn() });
    expect(
      screen.getByRole("heading", { name: "This machine cannot reach its home" })
    ).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent(/This machine cannot reach its home/);
    expect(await screen.findByRole("link", { name: "http://home.local" })).toBeInTheDocument();
    expect(screen.getByTestId("paused-help")).toHaveAttribute(
      "href",
      "http://home.local/help/machines#home-unreachable"
    );
  });

  it("reloads when the node's version changes", async () => {
    const onReload = vi.fn();
    get.mockResolvedValue(self());
    renderPaused({ code: "paused", reason: WHY, onReload });
    await screen.findByRole("link", { name: "http://home.local:8780" });
    await act(() => vi.advanceTimersByTimeAsync(PAUSED_POLL_MS));
    expect(onReload).not.toHaveBeenCalled();

    get.mockResolvedValue(self({ version: "1.6.0" }));
    await act(() => vi.advanceTimersByTimeAsync(PAUSED_POLL_MS));
    expect(onReload).toHaveBeenCalledTimes(1);
  });

  it("reloads when the condition clears", async () => {
    const onReload = vi.fn();
    get.mockResolvedValue(self({ paused: "", home: { reachable: false } }));
    renderPaused({ code: "home_unreachable", onReload });
    await act(() => vi.advanceTimersByTimeAsync(0));
    expect(get).toHaveBeenCalledTimes(1);

    get.mockResolvedValue(self({ paused: "", home: { reachable: true } }));
    await act(() => vi.advanceTimersByTimeAsync(PAUSED_POLL_MS));
    expect(onReload).toHaveBeenCalledTimes(1);
  });

  it("reloads a page a moment's blip sent here, but never in a loop", async () => {
    // the page came of an answer while the home answered all along: two looks that find
    // it answering reload it
    const onReload = vi.fn();
    get.mockResolvedValue(self({ paused: "", home: { reachable: true } }));
    const first = renderPaused({ code: "home_unreachable", onReload });
    await act(() => vi.advanceTimersByTimeAsync(0));
    expect(onReload).not.toHaveBeenCalled();
    await act(() => vi.advanceTimersByTimeAsync(PAUSED_POLL_MS));
    expect(onReload).toHaveBeenCalledTimes(1);
    first.unmount();
    // shown again right after (the app sent it here again): it waits, rather than loop
    const again = vi.fn();
    const second = renderPaused({ code: "home_unreachable", onReload: again });
    await act(() => vi.advanceTimersByTimeAsync(PAUSED_POLL_MS * 3));
    expect(again).not.toHaveBeenCalled();
    second.unmount();
    // a while later it may again
    const later = vi.fn();
    vi.setSystemTime(Date.now() + PAUSED_RELOAD_EVERY_MS);
    renderPaused({ code: "home_unreachable", onReload: later });
    await act(() => vi.advanceTimersByTimeAsync(PAUSED_POLL_MS * 2));
    expect(later).toHaveBeenCalledTimes(1);
  });

  it("keeps waiting while the machine restarts", async () => {
    const onReload = vi.fn();
    get.mockResolvedValueOnce(self()).mockRejectedValue(new Error("restarting"));
    renderPaused({ code: "paused", reason: WHY, onReload });
    await act(() => vi.advanceTimersByTimeAsync(PAUSED_POLL_MS * 2));
    expect(onReload).not.toHaveBeenCalled();
    expect(screen.getByRole("heading", { name: "This machine is paused" })).toBeInTheDocument();
  });

  it("speaks Arabic, right to left", async () => {
    localStorage.setItem("mkt:locale", "ar");
    get.mockResolvedValue(self());
    renderPaused({ code: "paused", reason: WHY, onReload: vi.fn() });
    expect(screen.getByRole("heading", { name: "هذا الجهاز متوقف مؤقتًا" })).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent(
      "متوقف مؤقتًا: قاعدة بيانات الجهاز الرئيسي على الإصدار 0130 وهذا الجهاز يحتاج الإصدار 0134"
    );
    expect(await screen.findByText(/استخدم مكتبة على .*Home NAS/)).toBeInTheDocument();
    expect(document.documentElement.dir).toBe("rtl");
  });

  it("speaks Arabic when the home is out of reach", () => {
    localStorage.setItem("mkt:locale", "ar");
    get.mockResolvedValue(self({ paused: "" }));
    renderPaused({ code: "home_unreachable", onReload: vi.fn() });
    expect(
      screen.getByRole("heading", { name: "لا يستطيع هذا الجهاز الوصول إلى جهازه الرئيسي" })
    ).toBeInTheDocument();
  });
});

describe("Paused (test-cases/app-shell.md)", () => {
  beforeEach(() => {
    get.mockReset();
    vi.useFakeTimers({ shouldAdvanceTime: true });
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("SHL-21: home null shows the unknown-address note and never reloads", async () => {
    const onReload = vi.fn();
    get.mockResolvedValue(self({ paused: "", home: null as never }));
    renderPaused({ code: "home_unreachable", onReload });
    await act(() => vi.advanceTimersByTimeAsync(PAUSED_POLL_MS * 3));
    expect(get).toHaveBeenCalledTimes(4);
    expect(screen.getByText("The home's address is not known yet.")).toBeInTheDocument();
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
    expect(onReload).not.toHaveBeenCalled();
  });

  it("SHL-21: an unsafe home web_url is not linked", async () => {
    get.mockResolvedValue(
      self({ paused: "", home: { web_url: "javascript:alert(1)", reachable: false } })
    );
    renderPaused({ code: "home_unreachable", onReload: vi.fn() });
    await act(() => vi.advanceTimersByTimeAsync(0));
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
    expect(screen.getByText("The home's address is not known yet.")).toBeInTheDocument();
  });

  it("SHL-21: no machine name means no 'This is …' line", async () => {
    get.mockResolvedValue(self({ name: undefined as never }));
    renderPaused({ code: "paused", reason: WHY, onReload: vi.fn() });
    await screen.findByRole("link", { name: "http://home.local:8780" });
    expect(screen.queryByText(/^This is /)).not.toBeInTheDocument();
  });
});
