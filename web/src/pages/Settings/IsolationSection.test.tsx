// Epic 59 story 59.07: Settings → Plugins shows the home's plugin isolation level; on the
// Container level, the switch Allow plugins with container-only isolation, which asks for
// the admin's password before it turns on, and the hold's reason while it is off.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, put } = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, put } };
});

import en from "../../i18n/en.json";
import ar from "../../i18n/ar.json";
import { renderPage } from "../../test/pageHarness";
import { routeGet } from "../../test/queueHarness";
import { IsolationSection } from "./PluginsSection";

const EN = en as Record<string, string>;
const AR = ar as Record<string, string>;

const CONTAINER = {
  level: "container",
  machines: [
    { machine: "home", level: "container", missing: ["landlock", "namespaces"], landlock: 0 },
  ],
  allow_container_isolation: false,
  held: true,
};

function open(doc: unknown) {
  routeGet(get, { "/api/plugins/isolation": () => doc });
  return renderPage(<IsolationSection />, { path: "/settings/plugins" });
}

describe("IsolationSection", () => {
  beforeEach(() => {
    get.mockReset();
    put.mockReset();
    localStorage.removeItem("mkt:locale");
  });
  afterEach(() => localStorage.removeItem("mkt:locale"));

  it("says the Container level, holds with the reason, and asks for the password", async () => {
    open(CONTAINER);
    expect(await screen.findByText(EN["plugins.isolation.container"])).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent(EN["plugins.held_isolation"]);
    expect(screen.getByText(EN["plugins.allow_container.explain"])).toBeInTheDocument();
    put.mockResolvedValue({ ...CONTAINER, allow_container_isolation: true, held: false });
    const user = userEvent.setup();
    await user.click(screen.getByRole("switch", { name: EN["plugins.allow_container"] }));
    const pw = await screen.findByLabelText(EN["plugins.policy.password"]);
    expect(put).not.toHaveBeenCalled();
    await user.type(pw, "secret");
    await user.click(screen.getByRole("button", { name: EN["plugins.policy.confirm"] }));
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/api/plugins/isolation", {
        allow: true,
        password: "secret",
      })
    );
    await waitFor(() => expect(screen.queryByRole("status")).not.toBeInTheDocument());
  });

  it("turning it off sends no password", async () => {
    open({ ...CONTAINER, allow_container_isolation: true, held: false });
    put.mockResolvedValue(CONTAINER);
    const user = userEvent.setup();
    await user.click(await screen.findByRole("switch", { name: EN["plugins.allow_container"] }));
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith("/api/plugins/isolation", { allow: false })
    );
    expect(await screen.findByRole("status")).toHaveTextContent(EN["plugins.held_isolation"]);
  });

  it("on the Full level says so and shows no switch", async () => {
    open({ level: "full", machines: [], allow_container_isolation: false, held: false });
    expect(await screen.findByText(EN["plugins.isolation.full"])).toBeInTheDocument();
    expect(screen.queryByRole("switch")).not.toBeInTheDocument();
  });

  it("says it in Arabic", async () => {
    localStorage.setItem("mkt:locale", "ar");
    open(CONTAINER);
    expect(await screen.findByText(AR["plugins.isolation.container"])).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent(AR["plugins.held_isolation"]);
    expect(screen.getByRole("switch", { name: AR["plugins.allow_container"] })).toBeInTheDocument();
  });

  it("shows nothing before any server wrote its level", async () => {
    open({ level: "", machines: [], allow_container_isolation: false, held: false });
    await waitFor(() => expect(get).toHaveBeenCalled());
    expect(screen.queryByTestId("plugin-isolation")).not.toBeInTheDocument();
  });
});
