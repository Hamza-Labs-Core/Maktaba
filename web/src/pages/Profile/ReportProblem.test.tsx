// Epic 53 story 53.15: the form shows what will be sent before Send, takes the error
// screen's reference, and says the report's number; My reports shows status and reply.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { renderPage } from "../../test/pageHarness";
import { appLog, clearLogLines } from "../../lib/errorLog";
import en from "../../i18n/en.json";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});

import { ReportProblem } from "./ReportProblem";
import { MyReports } from "./MyReports";

describe("Report a problem", () => {
  const fetchMock = vi.fn();
  beforeEach(() => {
    clearLogLines();
    appLog("warn", "playback_stalled", { at: 12 });
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ id: 27 }), { status: 201 }));
    vi.stubGlobal("fetch", fetchMock);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    fetchMock.mockReset();
    get.mockReset();
  });

  it("previews what is sent, sends it with the reference, and says its number", async () => {
    renderPage(<ReportProblem />, {
      path: "/account/report?ref=01932f6e",
      route: "/account/report",
    });
    const preview = screen.getByTestId("report-preview");
    expect(preview).toHaveTextContent("playback_stalled");
    expect(preview).toHaveTextContent("01932f6e");
    fireEvent.change(screen.getByRole("combobox"), { target: { value: "playback" } });
    fireEvent.change(screen.getByRole("textbox"), { target: { value: "it stopped" } });
    fireEvent.click(screen.getByRole("button", { name: en["report.send"] }));
    expect(await screen.findByTestId("report-sent")).toHaveTextContent("#27");
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toContain("/api/reports");
    const sent = JSON.parse(String((init.body as FormData).get("report")));
    expect(sent.category).toBe("playback");
    expect(sent.text).toBe("it stopped");
    expect(sent.request_ids).toContain("01932f6e");
    expect(sent.lines.some((l: { msg: string }) => l.msg === "playback_stalled")).toBe(true);
  });

  it("says when it could not be sent", async () => {
    fetchMock.mockResolvedValue(new Response("{}", { status: 429 }));
    renderPage(<ReportProblem />, { path: "/account/report" });
    fireEvent.click(screen.getByRole("button", { name: en["report.send"] }));
    expect(await screen.findByRole("alert")).toHaveTextContent(en["report.error"]);
  });

  it("My reports lists the person's reports with the admin's reply", async () => {
    get.mockResolvedValue({
      reports: [
        {
          id: 27,
          at: "2026-11-06T10:00:00Z",
          app: "web",
          category: "playback",
          status: "fixed",
          reply: "Fixed in 0.3.85",
          shared_with_maktaba: false,
        },
      ],
    });
    renderPage(<MyReports />, { path: "/account/reports" });
    expect(await screen.findByTestId("my-report")).toHaveTextContent(en["report.status.fixed"]);
    expect(screen.getByTestId("my-report-reply")).toHaveTextContent("Fixed in 0.3.85");
    await waitFor(() => expect(get).toHaveBeenCalledWith("/api/reports"));
  });
});
