// /install/synology (Epic 59 story 59.09 T04): the household's answers → the project's YAML,
// built in the page; the Arabic guide at /ar/install/synology.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setAccessToken } from "../shared/api";
import { renderAt, signedOut, stubFetch } from "../test/harness";

beforeEach(() => {
  setAccessToken(null);
  localStorage.clear();
});
afterEach(() => vi.unstubAllGlobals());

const yaml = () => screen.getByTestId("syn-yaml").textContent ?? "";

describe("the Synology install guide", () => {
  it("opens signed out, on a DS425+ with 6 GB: the J4125 file, Light", async () => {
    stubFetch({ "POST /v1/auth/refresh": signedOut });
    renderAt("/install/synology");
    expect(
      await screen.findByRole("heading", { name: "Install Maktaba on a Synology NAS" })
    ).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "The project file (Light)" })).toBeInTheDocument();
    const y = yaml();
    expect(y).toContain('user: "1026:100"');
    expect(y).toContain("mem_limit: 4g");
    expect(y).toContain("      - /dev/dri:/dev/dri");
    expect(y).toContain("      - /volume1/Movies:/volume1/Movies:ro");
    expect(y).toContain("from your answers: DS425+, 6 GB.");
    // the model table says which models fit before the household starts
    const table = screen.getByTestId("syn-models");
    const row = (name: string) => within(table).getByText(name).closest("tr")!;
    expect(row("DS923+")).toHaveTextContent("Fits: Light");
    expect(row("DS425+")).toHaveTextContent("Fits with more memory: Light with 6 GB");
    expect(row("DS223j")).toHaveTextContent("Use as storage");
  });

  it("fills in the answers, and sends none of them anywhere", async () => {
    const f = stubFetch({ "POST /v1/auth/refresh": signedOut });
    const u = userEvent.setup();
    renderAt("/install/synology");
    await screen.findByTestId("syn-yaml");
    await u.selectOptions(screen.getByLabelText("Model"), "DS1522+");
    const ram = screen.getByLabelText("Memory installed (GB)");
    await u.clear(ram);
    await u.type(ram, "8");
    const uid = screen.getByLabelText(/The uid of the DSM user maktaba/);
    await u.clear(uid);
    await u.type(uid, "1031");
    await u.click(screen.getByRole("button", { name: "Add a folder" }));
    const paths = screen.getAllByLabelText(/Folder \(such as/);
    await u.type(paths[2], "Cartoons");
    await u.type(screen.getByLabelText(/This NAS's address/), "192.168.7.77");
    await u.type(screen.getByLabelText(/Time zone/), "Asia/Riyadh");

    expect(
      screen.getByRole("heading", { name: "The project file (Standard)" })
    ).toBeInTheDocument();
    const y = yaml();
    expect(y).toContain('user: "1031:100"');
    expect(y).toContain("mem_limit: 6g");
    expect(y).not.toContain("/dev/dri"); // a Ryzen: no Quick Sync
    expect(y).toContain("      - /volume1/Cartoons:/volume1/Cartoons:ro");
    expect(y).toContain("      MAKTABA_HOST_IP: 192.168.7.77");
    expect(y).toContain("      TZ: Asia/Riyadh");
    for (const c of f.calls) {
      const sent = `${c.path} ${JSON.stringify(c.body ?? "")}`;
      for (const answer of ["1031", "Cartoons", "192.168.7.77", "Riyadh"]) {
        expect(sent).not.toContain(answer);
      }
    }
  });

  it("names a wrong answer and gives no file until it is fixed", async () => {
    stubFetch({ "POST /v1/auth/refresh": signedOut });
    const u = userEvent.setup();
    renderAt("/install/synology");
    await screen.findByTestId("syn-yaml");
    const path = screen.getAllByLabelText(/Folder \(such as/)[0];
    await u.clear(path);
    await u.type(path, "/srv/media");
    expect(
      screen.getByText(/Enter a folder on a volume, such as \/volume1\/Movies/)
    ).toBeInTheDocument();
    expect(screen.queryByTestId("syn-yaml")).toBeNull();
    expect(screen.getByText("Fix the answers above to get the file.")).toBeInTheDocument();
  });

  it("says a 2 GB ARM model is storage only", async () => {
    stubFetch({ "POST /v1/auth/refresh": signedOut });
    const u = userEvent.setup();
    renderAt("/install/synology");
    await screen.findByTestId("syn-yaml");
    await u.selectOptions(screen.getByLabelText("Model"), "DS223");
    const ram = screen.getByLabelText("Memory installed (GB)");
    await u.clear(ram);
    await u.type(ram, "2");
    expect(screen.getByTestId("syn-too-small")).toHaveTextContent(
      "With 2 GB this NAS has too little memory for Maktaba"
    );
    expect(screen.queryByTestId("syn-yaml")).toBeNull();
    // no Quick Sync choice on ARM
    expect(screen.queryByLabelText(/Use Quick Sync/)).toBeNull();
  });

  it("opens in Arabic at /ar/install/synology, right to left", async () => {
    stubFetch({ "POST /v1/auth/refresh": signedOut });
    renderAt("/ar/install/synology");
    expect(
      await screen.findByRole("heading", { name: "تثبيت «مكتبة» على جهاز Synology NAS" })
    ).toBeInTheDocument();
    expect(document.documentElement.dir).toBe("rtl");
    // the file itself stays English: Container Manager's editor reads it
    expect(yaml()).toContain("MAKTABA_PLATFORM: dsm");
  });
});
