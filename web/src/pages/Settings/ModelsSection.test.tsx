// Settings -> Models cases from web/test-cases/settings-models.md.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post, patch, del } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  patch: vi.fn(),
  del: vi.fn(),
}));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, patch, delete: del } };
});

import { ApiError, type Problem } from "../../lib/api";
import { memberUser, quietCrash, renderPage } from "../../test/pageHarness";
import { ModelsSection } from "./ModelsSection";

type M = Record<string, unknown>;
const small: M = {
  id: "whisper-small",
  type: "stt",
  name: "Whisper small",
  size: "466 MB",
  platform: "cpu",
  status: "active",
  progress: 100,
  active: true,
};
const large: M = {
  id: "whisper-large",
  type: "stt",
  name: "Whisper large",
  size: "2.9 GB",
  platform: "cuda",
  status: "available",
  progress: 0,
  active: false,
};
const e5: M = {
  id: "e5",
  type: "embedding",
  name: "E5 base",
  size: "420 MB",
  platform: "cpu",
  status: "downloaded",
  progress: 100,
  active: false,
};
const LIST = () => [small, large, e5].map((m) => ({ ...m }));

function problem(status: number, title: string, detail?: string) {
  return new ApiError({
    type: "about:blank",
    title,
    status,
    ...(detail ? { detail } : {}),
  } as Problem);
}

/** GET /api/models answers in turn (the last one repeats). */
function answers(...lists: unknown[]) {
  let i = 0;
  get.mockImplementation(() => {
    const a = lists[Math.min(i++, lists.length - 1)];
    return a instanceof Error ? Promise.reject(a) : Promise.resolve(structuredClone(a));
  });
}

function renderModels(opts: { user?: typeof memberUser | null } = {}) {
  return renderPage(<ModelsSection />, { path: "/settings/models", ...opts });
}

/** The card of a model, found by its bold name. */
function card(name: string) {
  return screen.getByText(name, { selector: "strong" }).closest(".mkt-models__card") as HTMLElement;
}
const buttons = (el: HTMLElement) =>
  within(el)
    .queryAllByRole("button")
    .map((b) => b.textContent);
const modelCalls = () => get.mock.calls.filter((c) => c[0] === "/api/models").length;

function fakeClock() {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  return userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
}
const tick = (ms: number) => act(async () => void (await vi.advanceTimersByTimeAsync(ms)));

beforeEach(() => {
  for (const f of [get, post, patch, del]) f.mockReset();
  answers(LIST());
  post.mockResolvedValue({});
  patch.mockResolvedValue({});
  del.mockResolvedValue(undefined);
  localStorage.clear();
});
afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe("Settings models (test-cases/settings-models.md)", () => {
  it("SMD-01: groups models into STT / Embedding / Diarization cards", async () => {
    renderModels();
    expect(screen.getByRole("heading", { level: 2, name: "Model Management" })).toBeInTheDocument();
    await screen.findByText("Whisper small", { selector: "strong" });
    const group = (h: string) =>
      screen.getByRole("heading", { level: 3, name: h }).closest("section") as HTMLElement;
    expect(group("Speech-to-Text").querySelectorAll(".mkt-models__card")).toHaveLength(2);
    expect(group("Embedding").querySelectorAll(".mkt-models__card")).toHaveLength(1);
    expect(
      within(group("Diarization")).getByText("No models in this category.")
    ).toBeInTheDocument();
    const c = card("Whisper small");
    expect(within(c).getByText("Active")).toBeInTheDocument();
    expect(within(c).getByText("466 MB")).toBeInTheDocument();
    expect(within(c).getByText("cpu")).toBeInTheDocument();
    expect(within(card("Whisper large")).getByText("Available")).toBeInTheDocument();
    expect(within(card("E5 base")).getByText("Downloaded")).toBeInTheDocument();
  });

  it("SMD-02: the buttons follow the status (available / downloaded / active / cloud / downloading)", async () => {
    answers([
      ...LIST(),
      { ...e5, id: "openai", type: "stt", name: "OpenAI Whisper", size: "", platform: "cloud" },
      { ...large, id: "dl", name: "Busy one", status: "downloading", progress: 40 },
    ]);
    renderModels();
    await screen.findByText("Whisper small", { selector: "strong" });
    expect(buttons(card("Whisper large"))).toEqual(["Download"]);
    expect(buttons(card("E5 base"))).toEqual(["Activate", "Test", "Delete"]);
    expect(buttons(card("Whisper small"))).toEqual(["Test", "Delete"]);
    const cloud = card("OpenAI Whisper");
    expect(buttons(cloud)).toEqual(["Activate", "Test"]);
    expect(cloud.querySelector(".mkt-models__size")).toBeNull();
    const busy = card("Busy one");
    expect(buttons(busy)).toEqual([]);
    expect(within(busy).getByRole("progressbar")).toBeInTheDocument();
  });

  it("SMD-03: Download starts polling and shows the progress", async () => {
    const user = fakeClock();
    const at = (status: string, progress: number) => [small, { ...large, status, progress }, e5];
    answers(LIST(), at("downloading", 10), at("downloading", 55), at("downloaded", 100));
    renderModels();
    await screen.findByText("Whisper large", { selector: "strong" });
    await user.click(within(card("Whisper large")).getByRole("button", { name: "Download" }));
    expect(post).toHaveBeenCalledTimes(1);
    expect(post).toHaveBeenCalledWith("/api/models/whisper-large/download");
    const bar = await within(card("Whisper large")).findByRole("progressbar", {
      name: "Downloading",
    });
    expect(bar).toHaveAttribute("aria-valuenow", "10");
    expect(within(card("Whisper large")).getByText("Downloading")).toBeInTheDocument();
    await tick(600);
    await waitFor(() =>
      expect(within(card("Whisper large")).getByRole("progressbar")).toHaveAttribute(
        "aria-valuenow",
        "55"
      )
    );
    await tick(600);
    await waitFor(() =>
      expect(within(card("Whisper large")).queryByRole("progressbar")).not.toBeInTheDocument()
    );
    expect(within(card("Whisper large")).getByText("Downloaded")).toBeInTheDocument();
    expect(buttons(card("Whisper large"))).toEqual(["Activate", "Test", "Delete"]);
  });

  it("SMD-04: polling stops when the download finishes and on unmount", async () => {
    fakeClock();
    const busy = [small, { ...large, status: "downloading", progress: 20 }, e5];
    answers(busy, busy, LIST());
    const { unmount } = renderModels();
    await screen.findByRole("progressbar");
    await tick(600);
    await tick(600);
    await waitFor(() => expect(screen.queryByRole("progressbar")).not.toBeInTheDocument());
    const settled = modelCalls();
    await tick(3000);
    expect(modelCalls()).toBe(settled);
    unmount();

    get.mockClear();
    answers(busy);
    const errors = vi.spyOn(console, "error");
    const second = renderModels();
    await screen.findByRole("progressbar");
    second.unmount();
    const left = modelCalls();
    await tick(3000);
    expect(modelCalls()).toBe(left);
    expect(errors).not.toHaveBeenCalledWith(expect.stringMatching(/unmounted component/));
  });

  it("SMD-05: Activate from the card and from the backend select", async () => {
    const user = userEvent.setup();
    const medium = { ...e5, id: "whisper-medium", type: "stt", name: "Whisper medium" };
    const before = [small, large, medium];
    const after = [
      { ...small, status: "downloaded", active: false },
      large,
      { ...medium, status: "active", active: true },
    ];
    for (const via of ["select", "card"]) {
      patch.mockClear();
      answers(before, after);
      const { unmount } = renderModels();
      const select = await screen.findByRole("combobox", { name: "Active STT backend" });
      await screen.findByText("Whisper medium", { selector: "strong" });
      expect(
        within(select)
          .getAllByRole("option")
          .map((o) => o.textContent)
      ).toEqual(["Whisper small", "Whisper medium"]);
      expect(select).toHaveValue("whisper-small");
      if (via === "select") await user.selectOptions(select, "whisper-medium");
      else
        await user.click(within(card("Whisper medium")).getByRole("button", { name: "Activate" }));
      expect(patch).toHaveBeenCalledTimes(1);
      expect(patch).toHaveBeenCalledWith("/api/models/whisper-medium/activate");
      await waitFor(() => expect(select).toHaveValue("whisper-medium"));
      expect(within(card("Whisper medium")).getByText("Active")).toBeInTheDocument();
      unmount();
    }
  });

  it("SMD-06: the backend select with no STT model downloaded is empty", async () => {
    answers([large, e5]);
    renderModels();
    const select = await screen.findByRole("combobox", { name: "Active STT backend" });
    await screen.findByText("Whisper large", { selector: "strong" });
    expect(within(select).queryAllByRole("option")).toHaveLength(0);
  });

  // Bug: with one downloaded STT model and none active, value "" matches no option, so the
  // browser shows that model as if active. Wanted a "None" placeholder (needs an i18n key).
  it.fails("SMD-06: no active STT model shows a placeholder, not a model", async () => {
    answers([{ ...small, status: "downloaded", active: false }, e5]);
    renderModels();
    const select = await screen.findByRole("combobox", { name: "Active STT backend" });
    await screen.findByText("Whisper small", { selector: "strong" });
    expect(within(select).getAllByRole("option")[0]).toHaveValue("");
  });

  it("SMD-07: Delete a model", async () => {
    const user = userEvent.setup();
    answers(LIST(), [small, large, { ...e5, status: "available", progress: 0 }]);
    renderModels();
    await screen.findByText("E5 base", { selector: "strong" });
    await user.click(within(card("E5 base")).getByRole("button", { name: "Delete" }));
    expect(del).toHaveBeenCalledTimes(1);
    expect(del).toHaveBeenCalledWith("/api/models/e5");
    await waitFor(() => expect(within(card("E5 base")).getByText("Available")).toBeInTheDocument());
    expect(buttons(card("E5 base"))).toEqual(["Download"]);
  });

  it("SMD-08: Test a model shows pass / fail", async () => {
    const user = userEvent.setup();
    for (const [last, text, cls] of [
      [{ ok: true, latency_ms: 412 }, "Test passed (412 ms)", "mkt-models__test-ok"],
      [
        { ok: false, latency_ms: 0, detail: "CUDA missing" },
        "Test failed",
        "mkt-models__test-fail",
      ],
    ] as const) {
      post.mockClear();
      answers(LIST(), [{ ...small, last_test: last }, large, e5]);
      const { unmount } = renderModels();
      await screen.findByText("Whisper small", { selector: "strong" });
      await user.click(within(card("Whisper small")).getByRole("button", { name: "Test" }));
      expect(post).toHaveBeenCalledWith("/api/models/whisper-small/test");
      expect(await screen.findByText(text)).toHaveClass(cls);
      unmount();
    }
  });

  // Bug: the list starts as [], so every group says "No models in this category." until the
  // list arrives. Wanted a loading state (ModelsSection.tsx; small, but needs a design call).
  it.fails("SMD-09: no empty text flashes before the list arrives", () => {
    get.mockReturnValue(new Promise(() => {}));
    renderModels();
    expect(screen.queryAllByText("No models in this category.")).toHaveLength(0);
  });

  it("SMD-10: an empty list shows three empty groups and no polling", async () => {
    fakeClock();
    answers([]);
    renderModels();
    await waitFor(() => expect(screen.getAllByText("No models in this category.")).toHaveLength(3));
    const select = screen.getByRole("combobox", { name: "Active STT backend" });
    expect(within(select).queryAllByRole("option")).toHaveLength(0);
    expect(screen.getByLabelText("OpenAI API key")).toBeEnabled();
    await tick(3000);
    expect(modelCalls()).toBe(1);
  });

  it("SMD-11: GET /api/models returning null shows the empty groups, no crash", async () => {
    answers(null);
    renderModels();
    await waitFor(() => expect(get).toHaveBeenCalled());
    expect(await screen.findAllByText("No models in this category.")).toHaveLength(3);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("SMD-12: an object instead of an array shows the empty groups, no crash", async () => {
    for (const body of [{ items: LIST() }, { models: null }]) {
      answers(body);
      const { unmount } = renderModels();
      await waitFor(() => expect(get).toHaveBeenCalled());
      expect(await screen.findAllByText("No models in this category.")).toHaveLength(3);
      expect(screen.queryByRole("alert")).not.toBeInTheDocument();
      unmount();
    }
  });

  it("SMD-13: missing or null optional fields on a model do not crash", async () => {
    answers([
      { id: "x", type: "stt", name: "X", status: "downloading" },
      {
        id: "y",
        type: "diarization",
        name: null,
        size: null,
        platform: null,
        status: "weird",
        progress: null,
        active: false,
        last_test: null,
      },
      { id: "z", type: "ocr", name: "Z", status: "available" },
    ]);
    renderModels();
    const x = card(
      await screen.findByText("X", { selector: "strong" }).then((e) => e.textContent!)
    );
    expect(within(x).getByRole("progressbar")).toBeInTheDocument();
    expect(document.querySelectorAll(".mkt-models__card")).toHaveLength(2); // the ocr model is dropped
    expect(screen.queryByText("Z", { selector: "strong" })).not.toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("SMD-14: a failed load shows the error and the empty groups", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    const fails: [unknown, string][] = [
      [
        problem(500, "Internal Server Error", "pipeline offline"),
        "Internal Server Error: pipeline offline",
      ],
      [problem(403, "Forbidden"), "Forbidden"],
      [new TypeError("Failed to fetch"), "Could not load models."],
      [new SyntaxError("Unexpected token"), "Could not load models."],
    ];
    for (const [err, text] of fails) {
      answers(err as Error);
      const { unmount } = renderModels();
      expect(await screen.findByRole("alert")).toHaveTextContent(text);
      expect(screen.getAllByText("No models in this category.")).toHaveLength(3);
      unmount();
    }
  });

  it("SMD-15: a failed action shows the message and keeps the cards", async () => {
    const user = userEvent.setup();
    renderModels();
    await screen.findByText("Whisper large", { selector: "strong" });
    const loads = modelCalls();
    post.mockRejectedValueOnce(problem(503, "Service Unavailable", "pipeline offline"));
    await user.click(within(card("Whisper large")).getByRole("button", { name: "Download" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Service Unavailable: pipeline offline"
    );
    del.mockRejectedValueOnce(problem(404, "Not Found"));
    await user.click(within(card("E5 base")).getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("Not Found"));
    patch.mockRejectedValueOnce(new TypeError("Failed to fetch"));
    await user.click(within(card("E5 base")).getByRole("button", { name: "Activate" }));
    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent("Could not load models.")
    );
    expect(modelCalls()).toBe(loads); // no reload after a failed action
    expect(within(card("Whisper large")).getByText("Available")).toBeInTheDocument();
    expect(within(card("E5 base")).getByText("Downloaded")).toBeInTheDocument();
  });

  it("SMD-16: a polling error is shown (and polling keeps going while downloading)", async () => {
    fakeClock();
    answers(
      [{ ...large, status: "downloading", progress: 30 }],
      problem(500, "Internal Server Error")
    );
    renderModels();
    await screen.findByRole("progressbar");
    await tick(6000);
    expect(screen.getByRole("alert")).toHaveTextContent("Internal Server Error");
    // recorded behaviour: no back-off, one call per 600 ms (wanted: back off)
    expect(modelCalls()).toBeGreaterThanOrEqual(10);
  });

  it("SMD-17: the OpenAI API key is saved to storage and prefilled", async () => {
    const user = userEvent.setup();
    const { unmount } = renderModels();
    const input = screen.getByLabelText("OpenAI API key");
    expect(input).toHaveAttribute("type", "password");
    expect(input).toHaveAttribute("autocomplete", "off");
    expect(input).toHaveAttribute("placeholder", "sk-…");
    await user.type(input, "sk-test-123");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(localStorage.getItem("mkt:openai_api_key")).toBe("sk-test-123");
    unmount();
    renderModels();
    expect(screen.getByLabelText("OpenAI API key")).toHaveValue("sk-test-123");
    // recorded: the key never goes to the server
    expect([...post.mock.calls, ...patch.mock.calls]).toEqual([]);
  });

  // Security gap: every user sees Download / Delete / Activate / Test, and the server
  // handler (api/internal/handlers/models) shows no admin check. Needs a server decision.
  it("SMD-18: a non-admin sees no model mutations", async () => {
    renderModels({ user: memberUser });
    await screen.findByText("Whisper large", { selector: "strong" });
    for (const name of ["Download", "Delete", "Activate", "Test"]) {
      expect(screen.queryByRole("button", { name })).not.toBeInTheDocument();
    }
    expect(screen.getByLabelText("Active STT backend")).toBeDisabled();
  });

  it("SMD-19: a signed-out deep link redirects to /login", () => {
    renderModels({ user: null });
    expect(screen.getByText("login page (from /settings/models)")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("SMD-20: a render crash shows the route error fallback; Back works", async () => {
    const user = userEvent.setup();
    quietCrash();
    answers([{ ...small, name: { bad: true } }]);
    renderPage(<ModelsSection />, { path: "/settings/models", history: ["/elsewhere"] });
    expect(await screen.findByRole("alert")).toHaveTextContent("Objects are not valid");
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(screen.getByText("elsewhere")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    answers(LIST());
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(screen.getByText("elsewhere")).toBeInTheDocument();
  });

  it("SMD-21: Arabic: RTL and Arabic strings", async () => {
    localStorage.setItem("mkt:locale", "ar");
    answers([{ ...small, last_test: { ok: true, latency_ms: 412 } }, large, e5]);
    renderModels();
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
    expect(screen.getByRole("heading", { name: "إدارة النماذج" })).toBeInTheDocument();
    for (const h of ["تحويل الكلام إلى نص", "التضمين", "فصل المتحدثين"])
      expect(screen.getByRole("heading", { name: h })).toBeInTheDocument();
    await screen.findByText("Whisper small", { selector: "strong" });
    for (const b of ["نشط", "متاح", "مُنزَّل"]) expect(screen.getByText(b)).toBeInTheDocument();
    for (const b of ["تنزيل", "تفعيل", "اختبار", "حذف"])
      expect(screen.getAllByRole("button", { name: b }).length).toBeGreaterThan(0);
    expect(document.body.textContent).not.toMatch(/settings\.models\./);
  });

  it("SMD-23: the select, key field, progress bar and alert are labelled", async () => {
    answers([{ ...large, status: "downloading", progress: 40 }]);
    renderModels();
    expect(screen.getByRole("combobox", { name: "Active STT backend" })).toBeInTheDocument();
    expect(screen.getByLabelText("OpenAI API key")).toBeInTheDocument();
    const bar = await screen.findByRole("progressbar", { name: "Downloading" });
    expect(bar).toHaveAttribute("aria-valuemin", "0");
    expect(bar).toHaveAttribute("aria-valuemax", "100");
    expect(bar).toHaveAttribute("aria-valuenow", "40");
  });
});
