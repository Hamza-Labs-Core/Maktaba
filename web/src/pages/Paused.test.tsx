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

import { PAUSED_POLL_MS, Paused } from "./Paused";

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
