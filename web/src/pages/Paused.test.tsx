// Epic 35, story 35.10 TC6: the paused page on a node (reasons, the link to
// the home, the reload when the node's version changes; en + ar). Since
// 2026-10-28 a whole page: why (each reason, with this machine's release and
// the main machine's), what is happening now, and how to keep going.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "../lib/i18n";
import type { ClusterSelf } from "../lib/clusterApi";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});

import { PAUSED_POLL_MS, PAUSED_RELOAD_EVERY_MS, Paused, pauseKind } from "./Paused";

const WHY = "paused: home database at 0130, this machine needs 0134 — update the home";
const BEHIND =
  "paused: home database at 0134 needs this machine at 0134 or newer (it has 0130) — update this machine";

function self(over: Partial<ClusterSelf> = {}): ClusterSelf {
  return {
    role: "node",
    id: "n1",
    name: "Studio Mac",
    version: "1.5.0",
    schema_rev: 134,
    paused: WHY,
    home: {
      name: "Home NAS",
      web_url: "http://home.local:8780",
      version: "v1.4.0",
      reachable: true,
    },
    served_at: new Date().toISOString(),
    ...over,
  };
}

/** An element's words without the bidi isolates around versions and names. */
function plain(el: HTMLElement): string {
  return (el.textContent ?? "").replace(/[⁨⁩]/g, "");
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

  it("tells the reasons apart", () => {
    expect(pauseKind(WHY)).toBe("home_behind");
    expect(pauseKind(BEHIND)).toBe("node_behind");
    expect(pauseKind("paused: something else")).toBe("unknown");
    expect(pauseKind(undefined)).toBe("unknown");
  });

  it("the main machine behind: both releases, it resumes by itself, the library on the home", async () => {
    get.mockResolvedValue(self());
    renderPaused({ code: "paused", reason: WHY, onReload: vi.fn() });
    expect(
      await screen.findByRole("heading", { name: "Paused until the main machine updates" })
    ).toBeInTheDocument();
    await vi.waitFor(() =>
      expect(plain(screen.getByRole("status"))).toBe(
        "This machine is on v1.5.0; the main machine's library (Home NAS) is on v1.4.0. It resumes by itself when the main machine updates."
      )
    );
    // the schema guard's own words stay, small, for whoever needs them
    expect(screen.getByTestId("paused-detail")).toHaveTextContent(
      "paused: home database at 0130, this machine needs 0134 — update the home"
    );
    // a working link to the library on the main machine
    const use = screen.getByTestId("paused-use-home");
    expect(plain(use)).toBe("Use the library on Home NAS");
    expect(use).toHaveAttribute("href", "http://home.local:8780");
    expect(screen.getByTestId("paused-home-url")).toHaveAttribute("href", "http://home.local:8780");
    expect(get).toHaveBeenCalledWith("/api/cluster/self");
    // what is happening now: no update anywhere, nothing to do here, when it last asked
    expect(plain(screen.getByTestId("paused-now-home"))).toContain(
      "On v1.4.0. No update in progress."
    );
    expect(plain(screen.getByTestId("paused-now-own"))).toContain(
      "On v1.5.0. Nothing to do here: it waits for the main machine."
    );
    expect(screen.getByTestId("paused-now-checked")).toHaveTextContent(/At .+\./);
    expect(screen.getByTestId("paused-help")).toHaveAttribute(
      "href",
      "http://home.local:8780/help/machines#paused-page"
    );
    expect(screen.getByRole("button", { name: "Check again" })).toBeEnabled();
  });

  it("the main machine updating: says so, with its release and where it is", async () => {
    get.mockResolvedValue(
      self({
        home: {
          name: "Home NAS",
          web_url: "http://home.local:8780",
          version: "1.4.0",
          reachable: true,
          update: { version: "1.6.0", state: "home_applying", step: "watching" },
        },
      })
    );
    renderPaused({ code: "paused", reason: WHY, onReload: vi.fn() });
    await vi.waitFor(() =>
      expect(plain(screen.getByTestId("paused-now-home"))).toContain(
        "Updating to v1.6.0: checking that it runs well."
      )
    );
  });

  it("this machine behind: it updates by itself to the main machine's release", async () => {
    get.mockResolvedValue(self({ version: "1.3.0", paused: BEHIND }));
    renderPaused({ code: "paused", reason: BEHIND, onReload: vi.fn() });
    expect(
      await screen.findByRole("heading", { name: "This machine is catching up" })
    ).toBeInTheDocument();
    await vi.waitFor(() =>
      expect(plain(screen.getByRole("status"))).toBe(
        "This machine updates by itself to v1.4.0; nothing to do."
      )
    );
    expect(plain(screen.getByTestId("paused-now-own"))).toContain(
      "On v1.3.0. Its update comes from the main machine, by itself."
    );
  });

  it("this machine behind and updating now: its own update, in plain words", async () => {
    get.mockResolvedValue(
      self({
        version: "1.3.0",
        paused: BEHIND,
        update: { version: "1.4.0", state: "preparing" },
      })
    );
    renderPaused({ code: "paused", reason: BEHIND, onReload: vi.fn() });
    await vi.waitFor(() =>
      expect(plain(screen.getByTestId("paused-now-own"))).toContain(
        "Updating to v1.4.0: downloading it."
      )
    );
  });

  it("an unknown reason still makes a whole page", async () => {
    get.mockResolvedValue(self({ paused: "paused: x" }));
    renderPaused({ code: "paused", reason: "paused: x", onReload: vi.fn() });
    expect(screen.getByRole("heading", { name: "This machine is paused" })).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("It is waiting for an update.");
    expect(await screen.findByTestId("paused-use-home")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "What is happening now" })).toBeInTheDocument();
  });

  it("an unknown home address: no dead link, Check again still there", async () => {
    get.mockResolvedValue(self({ home: { reachable: true } }));
    renderPaused({ code: "paused", reason: WHY, onReload: vi.fn() });
    expect(await screen.findByTestId("paused-home-unknown")).toHaveTextContent(
      "The home's address is not known yet."
    );
    expect(screen.queryByTestId("paused-use-home")).toBeNull();
    expect(screen.queryByRole("link")).toBeNull();
    expect(screen.getByRole("button", { name: "Check again" })).toBeInTheDocument();
    // no versions known for the home: the lead says it without them
    expect(plain(screen.getByRole("status"))).toBe(
      "This machine runs a newer release than the main machine's library. It resumes by itself when the main machine updates."
    );
  });

  it("Check again asks at once, and reloads once the pause is over", async () => {
    const onReload = vi.fn();
    get.mockResolvedValue(self());
    renderPaused({ code: "paused", reason: WHY, onReload });
    await vi.waitFor(() => expect(get).toHaveBeenCalledTimes(1));
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    await user.click(screen.getByRole("button", { name: "Check again" }));
    await vi.waitFor(() => expect(get).toHaveBeenCalledTimes(2));
    expect(onReload).not.toHaveBeenCalled();
    get.mockResolvedValue(self({ paused: "" }));
    await user.click(screen.getByRole("button", { name: "Check again" }));
    await vi.waitFor(() => expect(onReload).toHaveBeenCalledTimes(1));
  });

  it("says when this machine does not answer (restarting)", async () => {
    get.mockRejectedValue(new Error("restarting"));
    renderPaused({ code: "paused", reason: WHY, onReload: vi.fn() });
    await vi.waitFor(() =>
      expect(screen.getByTestId("paused-now-checked")).toHaveTextContent(
        "This machine did not answer (it may be restarting). Trying again."
      )
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
    expect(screen.getByTestId("paused-now-home")).toHaveTextContent("Not answering");
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
    expect(
      screen.getByRole("heading", { name: "Paused until the main machine updates" })
    ).toBeInTheDocument();
  });

  it("speaks Arabic, right to left", async () => {
    localStorage.setItem("mkt:locale", "ar");
    get.mockResolvedValue(self());
    renderPaused({ code: "paused", reason: WHY, onReload: vi.fn() });
    expect(
      screen.getByRole("heading", { name: "متوقف مؤقتًا حتى يُحدَّث الجهاز الرئيسي" })
    ).toBeInTheDocument();
    await vi.waitFor(() =>
      expect(plain(screen.getByRole("status"))).toBe(
        "هذا الجهاز على v1.5.0، ومكتبة الجهاز الرئيسي (Home NAS) على v1.4.0. سيستأنف عمله تلقائيًا عندما يُحدَّث الجهاز الرئيسي."
      )
    );
    expect(screen.getByTestId("paused-detail")).toHaveTextContent(
      "متوقف مؤقتًا: قاعدة بيانات الجهاز الرئيسي على الإصدار 0130 وهذا الجهاز يحتاج الإصدار 0134"
    );
    expect(plain(screen.getByTestId("paused-use-home"))).toBe("استخدم المكتبة على Home NAS");
    expect(screen.getByRole("button", { name: "افحص مجددًا" })).toBeInTheDocument();
    expect(document.documentElement.dir).toBe("rtl");
  });

  it("speaks Arabic when this machine is behind", async () => {
    localStorage.setItem("mkt:locale", "ar");
    get.mockResolvedValue(self({ version: "1.3.0", paused: BEHIND }));
    renderPaused({ code: "paused", reason: BEHIND, onReload: vi.fn() });
    expect(
      screen.getByRole("heading", { name: "هذا الجهاز يلحق بالجهاز الرئيسي" })
    ).toBeInTheDocument();
    await vi.waitFor(() =>
      expect(plain(screen.getByRole("status"))).toBe(
        "سيُحدَّث هذا الجهاز تلقائيًا إلى v1.4.0، ولا شيء عليك فعله."
      )
    );
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
