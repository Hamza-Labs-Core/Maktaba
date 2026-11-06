// Epic 53 stories 53.10 and 53.11: the Errors list (new and seen by default, a component
// from the address, sort), a group's page (Resolve in its last version, mute for a time; the
// navigation's count is asked to read again), a viewer's "admins only", and Retention
// (defaults, a save, a value the server refuses, detailed logs for an hour).
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { Route } from "react-router-dom";
import { ApiError } from "../../lib/api";
import { renderPage, memberUser } from "../../test/pageHarness";
import { ERRORS_CHANGED_EVENT } from "../../lib/newErrors";
import en from "../../i18n/en.json";

const { get, post, put } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, put } };
});

import { AdminErrorGroup, AdminErrors } from "./Errors";
import { AdminLogSettings } from "./LogSettings";

const GROUP = {
  fp: "ab12",
  service: "api",
  component: "core.api",
  code: "db.timeout",
  title: "db.timeout: query took 5003ms",
  first_seen: "2026-11-05T10:00:00Z",
  last_seen: "2026-11-06T10:00:00Z",
  count: 40,
  count_24h: 3,
  users: 2,
  status: "new",
  last_version: "0.3.84",
  days: [0, 0, 1, 2, 3],
};

const RETENTION = {
  logs_days: 7,
  errors_days: 30,
  reports_days: 90,
  total_bytes: 1 << 30,
  used: { logs: 50 << 20, index: 10 << 20, reports: 0 },
  ranges: {
    logs_days: [1, 90],
    errors_days: [1, 365],
    reports_days: [1, 365],
    total_bytes: [256 << 20, 20 << 30],
  },
  detailed: {},
};

describe("Errors", () => {
  beforeEach(() => {
    get.mockImplementation((path: string) => {
      if (path.startsWith("/api/admin/errors/ab12"))
        return Promise.resolve({
          group: GROUP,
          hours: { "2026-11-06T09:00:00Z": 2, "2026-11-06T10:00:00Z": 1 },
          samples: {
            lines: [
              {
                ts: "2026-11-06T10:00:00Z",
                level: "error",
                service: "api",
                msg: "video_load_failed",
                request_id: "01932f6e-7c1a-7b2e-9a51-2f5c3d4e5f60",
              },
            ],
            ids: [9],
          },
        });
      if (path.startsWith("/api/admin/errors")) return Promise.resolve({ groups: [GROUP] });
      if (path.startsWith("/api/admin/logs/retention")) return Promise.resolve(RETENTION);
      return Promise.resolve({});
    });
    post.mockResolvedValue(undefined);
  });
  afterEach(() => {
    get.mockReset();
    post.mockReset();
    put.mockReset();
    window.history.replaceState({}, "", "/");
  });

  it("lists new and seen groups by default, with a component from the address", async () => {
    window.history.replaceState({}, "", "/admin/errors?component=core.api");
    renderPage(<AdminErrors />, { path: "/admin/errors" });
    expect(await screen.findByText(GROUP.title)).toBeInTheDocument();
    const asked = String(get.mock.calls[0][0]);
    expect(asked).toContain("status=new%2Cseen");
    expect(asked).toContain("component=core.api");
    expect(asked).toContain("sort=last_seen");
    expect(
      screen.getByText(en["errors.counts"].replace("{day}", "3").replace("{all}", "40"))
    ).toBeInTheDocument();
    fireEvent.change(screen.getByRole("combobox", { name: en["errors.filter.sort"] }), {
      target: { value: "users" },
    });
    await waitFor(() => expect(String(get.mock.calls.at(-1)?.[0])).toContain("sort=users"));
  });

  it("a group's page resolves it in its last version and mutes it for 24 hours", async () => {
    const changed = vi.fn();
    window.addEventListener(ERRORS_CHANGED_EVENT, changed);
    renderPage(<AdminErrorGroup />, { path: "/admin/errors/ab12", route: "/admin/errors/:fp" });
    expect(await screen.findByTestId("error-group-page")).toHaveTextContent(GROUP.title);
    expect(screen.getByText("video_load_failed")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: en["errors.resolve"] }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/admin/errors/ab12/status", {
        status: "resolved",
        version: "0.3.84",
      })
    );
    await waitFor(() => expect(changed).toHaveBeenCalled());
    fireEvent.click(screen.getByRole("button", { name: en["errors.mute24h"] }));
    await waitFor(() => expect(post).toHaveBeenCalledTimes(2));
    const body = post.mock.calls[1][1] as { status: string; muted_until: string };
    expect(body.status).toBe("muted");
    const hours = (Date.parse(body.muted_until) - Date.now()) / 3600_000;
    expect(hours).toBeGreaterThan(23.9);
    expect(hours).toBeLessThan(24.1);
    window.removeEventListener(ERRORS_CHANGED_EVENT, changed);
  });

  it("a sample's request opens Follow a request in Logs", async () => {
    renderPage(<AdminErrorGroup />, {
      path: "/admin/errors/ab12",
      route: "/admin/errors/:fp",
      extraRoutes: <Route path="/admin/logs" element={<p>the logs page</p>} />,
    });
    await screen.findByText("video_load_failed");
    fireEvent.click(screen.getByTitle(en["logs.follow"]));
    expect(await screen.findByText("the logs page")).toBeInTheDocument();
  });

  it("a viewer who opens Errors by its address sees admins only, and nothing is asked", async () => {
    renderPage(<AdminErrors />, { path: "/admin/errors", user: memberUser });
    expect(await screen.findByText(en["common.forbidden.title"])).toBeInTheDocument();
    expect(get.mock.calls.some(([p]) => String(p).startsWith("/api/admin/errors"))).toBe(false);
  });

  it("Retention shows 7, 30 and 90 days and 1 GB with what is used, and saves a change", async () => {
    put.mockResolvedValue({ ...RETENTION, logs_days: 14 });
    renderPage(<AdminLogSettings />, { path: "/admin/logs/settings" });
    const logs = await screen.findByRole("spinbutton", {
      name: en["logs.settings.keepLogs"].replace("{days}", "7"),
    });
    expect(logs).toHaveValue(7);
    expect(
      screen.getByRole("spinbutton", {
        name: en["logs.settings.keepErrors"].replace("{days}", "30"),
      })
    ).toHaveValue(30);
    expect(
      screen.getByRole("spinbutton", {
        name: en["logs.settings.keepReports"].replace("{days}", "90"),
      })
    ).toHaveValue(90);
    expect(screen.getByTestId("logset-used")).toHaveTextContent("60");
    fireEvent.change(logs, { target: { value: "14" } });
    fireEvent.click(screen.getByRole("button", { name: en["common.save"] }));
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/api/admin/logs/retention", {
        logs_days: 14,
        errors_days: 30,
        reports_days: 90,
        total_bytes: 1 << 30,
      })
    );
  });

  it("Retention says when the server refuses a value; detailed logs for an hour", async () => {
    put.mockRejectedValue(new ApiError({ type: "about:blank", title: "bad", status: 400 }));
    post.mockResolvedValue({ ...RETENTION, detailed: { pipeline: "2026-11-06T11:00:00Z" } });
    renderPage(<AdminLogSettings />, { path: "/admin/logs/settings" });
    await screen.findByRole("spinbutton", {
      name: en["logs.settings.keepLogs"].replace("{days}", "7"),
    });
    fireEvent.click(screen.getByRole("button", { name: en["common.save"] }));
    expect(await screen.findByRole("alert")).toHaveTextContent(en["logs.settings.outOfRange"]);
    fireEvent.click(screen.getAllByRole("button", { name: en["logs.settings.debug"] })[2]);
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/admin/logs/level", {
        service: "pipeline",
        level: "debug",
        minutes: 60,
      })
    );
    expect(
      await screen.findByRole("button", { name: en["logs.settings.debugStop"] })
    ).toBeInTheDocument();
  });
});
