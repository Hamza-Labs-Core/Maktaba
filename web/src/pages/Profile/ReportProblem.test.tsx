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

  // Epic 53 story 53.16: the Maktaba box shows only while the home allows it, starts
  // unticked, and a ticked box says what Maktaba gets and is sent with the report
  it("offers Also send to Maktaba only when the home allows it", async () => {
    get.mockResolvedValue({ available: true, enabled: false });
    const { unmount } = renderPage(<ReportProblem />, { path: "/account/report" });
    await waitFor(() => expect(get).toHaveBeenCalledWith("/api/reports/maktaba"));
    expect(screen.queryByTestId("report-to-maktaba")).toBeNull();
    unmount();
    get.mockResolvedValue({ available: true, enabled: true });
    renderPage(<ReportProblem />, { path: "/account/report" });
    const box = await screen.findByRole("checkbox", { name: en["report.to.maktaba"] });
    expect(box).not.toBeChecked();
    expect(screen.queryByText(en["report.to.maktaba.note"])).toBeNull();
    fireEvent.click(box);
    expect(screen.getByText(en["report.to.maktaba.note"])).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: en["report.send"] }));
    await screen.findByTestId("report-sent");
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(JSON.parse(String((init.body as FormData).get("report"))).share_with_maktaba).toBe(true);
  });

  it("sends without Maktaba while the box is unticked", async () => {
    get.mockResolvedValue({ available: true, enabled: true });
    renderPage(<ReportProblem />, { path: "/account/report" });
    await screen.findByRole("checkbox", { name: en["report.to.maktaba"] });
    fireEvent.click(screen.getByRole("button", { name: en["report.send"] }));
    await screen.findByTestId("report-sent");
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(JSON.parse(String((init.body as FormData).get("report"))).share_with_maktaba).toBe(
      false
    );
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

  it("My reports says a report also went to Maktaba, and Maktaba's reply", async () => {
    get.mockResolvedValue({
      reports: [
        {
          id: 28,
          at: "2026-11-07T10:00:00Z",
          app: "web",
          category: "playback",
          status: "new",
          shared_with_maktaba: true,
          maktaba: { state: "sent", reply: "Thank you, it is fixed", fixed_in: "0.3.96" },
        },
        {
          id: 29,
          at: "2026-11-07T11:00:00Z",
          app: "web",
          category: "search",
          status: "new",
          shared_with_maktaba: true,
          maktaba: { state: "queued" },
        },
      ],
    });
    renderPage(<MyReports />, { path: "/account/reports" });
    const rows = await screen.findAllByTestId("report-maktaba");
    expect(rows[0]).toHaveTextContent(en["report.sentToMaktaba"]);
    expect(screen.getByTestId("report-maktaba-reply")).toHaveTextContent(
      `${en["report.maktabaReplied"]}: Thank you, it is fixed`
    );
    expect(screen.getByTestId("report-maktaba-reply")).toHaveTextContent("0.3.96");
    expect(rows[1]).toHaveTextContent(en["report.maktaba.state.queued"]);
  });
});
