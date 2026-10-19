// Channel art on the channel form: logo / corner watermark / loading
// screen tiles (upload with progress, replace, back to plan) and the
// watermark corner + size settings.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "../../lib/i18n";
import { ToastProvider } from "@ds/components/Toast/Toast";

const { get, post, patch, del, upload } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  patch: vi.fn(),
  del: vi.fn(),
  upload: vi.fn(),
}));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return {
    ...actual,
    api: { ...actual.api, get, post, patch, delete: del },
    uploadWithProgress: upload,
  };
});
vi.mock("../../lib/auth", () => ({
  useAuth: () => ({ user: { username: "admin", is_admin: true } }),
}));

import { Channels } from "./Channels";

const art = (over: Partial<Record<string, unknown>> = {}) => ({
  logo: { url: "/api/channels/c1/art/logo?v=1", path: "/plan/logo.png", overridden: false },
  watermark: {
    url: "/api/channels/c1/art/watermark?v=2",
    path: "/data/channel-art/c1/watermark-abc.gif",
    overridden: true,
    position: "top-right",
    width_pct: 12,
    margin_pct: 2,
  },
  loading: { overridden: false },
  ...over,
});

const channel = (a = art()) => ({
  id: "c1",
  number: 1,
  name: "Kids",
  mode: "shuffle",
  enabled: true,
  sort_order: 0,
  logo_path: "/plan/logo.png",
  art: a,
});

async function openForm() {
  const user = userEvent.setup();
  get.mockResolvedValue({ items: [channel()] });
  render(
    <I18nProvider>
      <ToastProvider>
        <Channels />
      </ToastProvider>
    </I18nProvider>
  );
  await screen.findByText("Kids");
  await user.click(screen.getByRole("button", { name: /^edit$/i }));
  await screen.findByText(/corner watermark/i);
  return user;
}

describe("Channel art", () => {
  beforeEach(() => {
    for (const f of [get, post, patch, del, upload]) f.mockReset();
  });

  it("shows the current pictures and where they come from", async () => {
    await openForm();
    const logo = screen.getByTestId("art-logo");
    expect(within(logo).getByRole("img")).toHaveAttribute("src", "/api/channels/c1/art/logo?v=1");
    expect(within(logo).getByText(/from the plan/i)).toBeInTheDocument();
    // only hand-set art can go back to the plan
    expect(within(logo).queryByRole("button", { name: /back to plan/i })).toBeNull();
    const wm = screen.getByTestId("art-watermark");
    // an animated GIF is shown as is (an <img> plays it)
    expect(within(wm).getByRole("img").getAttribute("src")).toContain("/art/watermark");
    expect(within(wm).getByText(/uploaded/i)).toBeInTheDocument();
    expect(within(screen.getByTestId("art-loading")).getByText(/no image/i)).toBeInTheDocument();
  });

  it("uploads a chosen file to the art endpoint with progress", async () => {
    const user = await openForm();
    let report: ((f: number) => void) | undefined;
    let finish: (c: unknown) => void = () => {};
    upload.mockImplementation(
      (_path: string, _form: FormData, onProgress?: (f: number) => void) => {
        report = onProgress;
        return new Promise((resolve) => (finish = resolve));
      }
    );
    const tile = screen.getByTestId("art-loading");
    const file = new File([new Uint8Array([0x47, 0x49, 0x46])], "intro.gif", { type: "image/gif" });
    await user.upload(within(tile).getByLabelText(/choose an image for loading screen/i), file);
    expect(upload).toHaveBeenCalledTimes(1);
    const [path, form] = upload.mock.calls[0] as [string, FormData];
    expect(path).toBe("/api/channels/c1/art/loading");
    expect((form.get("file") as File).name).toBe("intro.gif");
    report?.(0.5);
    expect(await within(tile).findByRole("progressbar")).toBeInTheDocument();
    finish(
      channel(
        art({
          loading: {
            url: "/api/channels/c1/art/loading?v=9",
            path: "/data/channel-art/c1/loading-1.gif",
            overridden: true,
          },
        })
      )
    );
    expect(await within(tile).findByRole("img")).toHaveAttribute(
      "src",
      "/api/channels/c1/art/loading?v=9"
    );
    expect(within(tile).getByRole("button", { name: /replace/i })).toBeInTheDocument();
  });

  it("refuses a file that is too big or the wrong type before uploading", async () => {
    const user = userEvent.setup({ applyAccept: false });
    await openForm();
    const tile = screen.getByTestId("art-watermark");
    const svg = new File(["<svg/>"], "w.svg", { type: "image/svg+xml" });
    await user.upload(within(tile).getByLabelText(/choose an image for corner watermark/i), svg);
    expect(await within(tile).findByRole("alert")).toHaveTextContent(/not accepted/i);
    const big = new File([new Uint8Array(5 * 1024 * 1024 + 1)], "big.png", { type: "image/png" });
    await user.upload(within(tile).getByLabelText(/choose an image for corner watermark/i), big);
    expect(await within(tile).findByRole("alert")).toHaveTextContent(/5 MB/);
    expect(upload).not.toHaveBeenCalled();
  });

  it("goes back to the plan's picture", async () => {
    const user = await openForm();
    del.mockResolvedValue(channel(art({ watermark: { ...art().watermark, overridden: false } })));
    const tile = screen.getByTestId("art-watermark");
    await user.click(within(tile).getByRole("button", { name: /back to plan/i }));
    expect(del).toHaveBeenCalledWith("/api/channels/c1/art/watermark");
    expect(await within(tile).findByText(/from the plan/i)).toBeInTheDocument();
  });

  it("saves the watermark corner, width and margin", async () => {
    const user = await openForm();
    patch.mockResolvedValue(channel());
    const apply = screen.getByRole("button", { name: /^apply$/i });
    expect(apply).toBeDisabled();
    await user.click(screen.getByRole("radio", { name: /bottom left/i }));
    const width = screen.getByLabelText(/watermark width/i);
    await user.clear(width);
    await user.type(width, "20");
    await user.click(apply);
    expect(patch).toHaveBeenCalledWith("/api/channels/c1/art/watermark", {
      position: "bottom-left",
      width_pct: 20,
      margin_pct: 2,
    });
  });

  it("asks to save a new channel before adding art", async () => {
    const user = userEvent.setup();
    get.mockResolvedValue({ items: [] });
    render(
      <I18nProvider>
        <ToastProvider>
          <Channels />
        </ToastProvider>
      </I18nProvider>
    );
    await user.click(await screen.findByRole("button", { name: /new channel/i }));
    expect(await screen.findByText(/save the channel first/i)).toBeInTheDocument();
    expect(screen.queryByTestId("art-logo")).toBeNull();
  });
});
