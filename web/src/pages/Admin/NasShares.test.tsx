// Epic 59 story 59.13: a NAS home's own folders and their shares on /admin/machines.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "../../lib/i18n";
import { ToastProvider } from "@ds/components/Toast/Toast";

const { put } = vi.hoisted(() => ({ put: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, put } };
});

import type { HomeShares } from "../../lib/clusterApi";
import { NasShares } from "./NasShares";
import { axeViolations } from "../../test/axe";

const NAS: HomeShares = {
  nas: true,
  platform: "dsm",
  server: "diskstation",
  addresses: ["diskstation.local", "192.168.1.20"],
  shares: [
    { path: "/media/TV", share: "TV", local_only: false, host_path: false },
    {
      path: "/volume1/Movies",
      share: "Movies",
      local_only: false,
      host_path: true,
      rule: "Movies",
    },
    { path: "/volume1/Private", share: "", local_only: true, host_path: true, rule: "Private" },
  ],
  unnamed: ["/mnt/usb/clips"],
  other_path: true,
  writable_share: false,
  notices: ["no_writable_share"],
};

function renderCard(hs: HomeShares) {
  return render(
    <I18nProvider>
      <ToastProvider>
        <NasShares load={() => Promise.resolve(hs)} />
      </ToastProvider>
    </I18nProvider>
  );
}

describe("NasShares (story 59.13)", () => {
  beforeEach(() => {
    localStorage.clear();
    put.mockReset();
  });

  it("shows each folder's share, the notices, and saves the named folder", async () => {
    put.mockResolvedValue({ ...NAS, unnamed: [], notices: [] });
    const { container } = renderCard(NAS);
    expect(await screen.findByRole("heading", { name: "This NAS's shares" })).toBeInTheDocument();
    expect(
      screen.getByText(/No shared folder on this NAS is writable for Maktaba/)
    ).toBeInTheDocument();
    expect(
      screen.getByText(/nodes will match them by the share name you gave/)
    ).toBeInTheDocument();
    expect(screen.getByText("smb://diskstation/Movies")).toBeInTheDocument();
    const rows = screen.getAllByTestId("nas-share");
    expect(rows).toHaveLength(4);
    expect(within(rows[2]).getByLabelText("Not shared on the network")).toBeChecked();
    const save = screen.getByRole("button", { name: "Save" });
    expect(save).toBeDisabled(); // the USB folder has no share yet
    await userEvent.type(screen.getByLabelText("Share name for /mnt/usb/clips"), "Clips");
    expect(await axeViolations(container)).toEqual([]);
    await userEvent.click(save);
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/api/cluster/storage/home-shares", {
        addresses: ["diskstation.local", "192.168.1.20"],
        shares: [
          { path: "/media/TV", share: "TV" },
          { path: "/volume1/Movies", share: "Movies" },
          { path: "/volume1/Private", share: "" },
          { path: "/mnt/usb/clips", share: "Clips" },
        ],
      })
    );
    await waitFor(() =>
      expect(screen.queryByText(/No shared folder on this NAS is writable/)).not.toBeInTheDocument()
    );
  });

  it("shows nothing on a home that is not a NAS", async () => {
    const load = vi.fn(() => Promise.resolve({ ...NAS, nas: false }));
    const { container } = render(
      <I18nProvider>
        <ToastProvider>
          <NasShares load={load} />
        </ToastProvider>
      </I18nProvider>
    );
    await waitFor(() => expect(load).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });

  it("in Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    renderCard(NAS);
    expect(
      await screen.findByRole("heading", { name: "مشاركات خادم التخزين هذا" })
    ).toBeInTheDocument();
    expect(screen.getAllByLabelText("غير مشارك على الشبكة")).toHaveLength(4);
  });
});
