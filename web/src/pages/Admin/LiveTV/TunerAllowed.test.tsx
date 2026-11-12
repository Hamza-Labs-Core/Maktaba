// Epic 57 story 57.10: the addresses allowed to use the tuner (admin).
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, put } = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }));
vi.mock("../../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../../lib/api")>("../../../lib/api");
  return { ...actual, api: { ...actual.api, get, put } };
});

import { ApiError } from "../../../lib/api";
import { I18nProvider } from "../../../lib/i18n";
import { TunerAllowed, parseAddresses } from "./TunerAllowed";

function show(locale: "en" | "ar" = "en") {
  localStorage.setItem("mkt:locale", locale);
  return render(
    <I18nProvider>
      <TunerAllowed />
    </I18nProvider>
  );
}

const box = () => screen.findByRole("textbox", { name: "Addresses allowed to use the tuner" });

beforeEach(() => {
  get.mockReset();
  put.mockReset();
  localStorage.clear();
  get.mockResolvedValue({ addresses: ["192.168.1.20"] });
});

describe("TunerAllowed (57.10)", () => {
  it("reads lines: trimmed, no blanks, no repeats", () => {
    expect(parseAddresses(" 192.168.1.20 \n\n10.0.0.0/8, 192.168.1.20\n")).toEqual([
      "192.168.1.20",
      "10.0.0.0/8",
    ]);
    expect(parseAddresses("")).toEqual([]);
  });

  it("shows the stored list and saves a changed one", async () => {
    put.mockResolvedValue({ addresses: ["192.168.1.20", "10.0.0.0/8"] });
    show();
    const area = await box();
    expect(area).toHaveValue("192.168.1.20");
    expect(get).toHaveBeenCalledWith("/api/tv/hdhr/allowed");
    const save = screen.getByRole("button", { name: "Save" });
    expect(save).toBeDisabled();
    await userEvent.type(area, "\n10.0.0.0/8");
    await userEvent.click(save);
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/api/tv/hdhr/allowed", {
        addresses: ["192.168.1.20", "10.0.0.0/8"],
      })
    );
    expect(await screen.findByRole("status")).toHaveTextContent("Addresses saved.");
    expect(save).toBeDisabled();
  });

  it("an empty list allows no other address", async () => {
    put.mockResolvedValue({ addresses: [] });
    show();
    const area = await box();
    await userEvent.clear(area);
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/api/tv/hdhr/allowed", { addresses: [] })
    );
  });

  it("says when the home does not take an entry, and keeps what was typed", async () => {
    put.mockRejectedValue(
      new ApiError({ type: "about:blank", title: "unprocessable", status: 422 })
    );
    show();
    const area = await box();
    await userEvent.type(area, "\nnot-an-address");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "One of these is not an address or a range. Check each line."
    );
    expect(area).toHaveValue("192.168.1.20\nnot-an-address");
  });

  it("will not send more than 64", async () => {
    get.mockResolvedValue({ addresses: [] });
    show();
    const area = await box();
    await userEvent.click(area);
    await userEvent.paste(Array.from({ length: 65 }, (_, i) => `10.0.0.${i}`).join("\n"));
    expect(screen.getByRole("alert")).toHaveTextContent("That is more than 64 addresses.");
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });

  it("says so when the list cannot be read (the home refuses anyone but an admin)", async () => {
    get.mockRejectedValue(new ApiError({ type: "about:blank", title: "forbidden", status: 403 }));
    show();
    expect(await screen.findByRole("alert")).toHaveTextContent("Could not load the addresses.");
    expect(screen.queryByRole("textbox")).toBeNull();
  });

  it("is Arabic in Arabic; the addresses stay left to right", async () => {
    show("ar");
    const area = await screen.findByRole("textbox", {
      name: "العناوين المسموح لها باستخدام الموالف",
    });
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
    expect(area).toHaveValue("192.168.1.20");
    expect(screen.getByRole("button", { name: "حفظ" })).toBeInTheDocument();
  });
});
