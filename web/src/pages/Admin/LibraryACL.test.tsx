import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post, put, patch, del } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  patch: vi.fn(),
  del: vi.fn(),
}));
vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { ...actual.api, get, post, put, patch, delete: del } };
});

import { ToastProvider } from "@ds/components/Toast/Toast";
import { ApiError } from "../../lib/api";
import { LibraryACL } from "./LibraryACL";
import { renderPage, memberUser, quietCrash, adminUser } from "../../test/pageHarness";
import { routeGet } from "../../test/queueHarness";
import type { User } from "../../lib/auth";

const URL = "/api/admin/library-acl";
const DATA = {
  users: [
    { id: "u1", username: "alice" },
    { id: "u2", username: "bob" },
  ],
  libraries: [
    { id: "l1", name: "Lectures" },
    { id: "l2", name: "Kids" },
  ],
  grants: [
    { user_id: "u1", library_id: "l1", role: "admin" },
    { user_id: "u2", library_id: "l2", role: "read" },
  ],
};

function open(data: unknown = DATA, user: User | null = adminUser) {
  routeGet(get, { [URL]: () => data });
  return renderPage(
    <ToastProvider>
      <LibraryACL />
    </ToastProvider>,
    { path: "/admin/library-acl", user }
  );
}
const cell = (name: string) => screen.getByRole("combobox", { name });
const findCell = (name: string) => screen.findByRole("combobox", { name });
const saveBtn = () => screen.getByRole("button", { name: /^Save/ });
function problem(status: number, title: string, detail?: string) {
  return new ApiError({ type: "about:blank", title, status, detail });
}

describe("LibraryACL (test-cases/admin-library-acl.md)", () => {
  beforeEach(() => {
    for (const f of [get, post, put, patch, del]) f.mockReset();
  });
  afterEach(() => vi.restoreAllMocks());

  it("ACL-01: matrix renders users x libraries with current roles", async () => {
    open();
    expect(
      screen.getByRole("heading", { level: 1, name: "Library access control" })
    ).toBeInTheDocument();
    expect(screen.getByText(/Set each user's permission level per library/)).toBeInTheDocument();
    const table = await screen.findByRole("table", { name: "Library access control" });
    expect(Array.from(table.querySelectorAll("thead th")).map((th) => th.textContent)).toEqual([
      "User",
      "Lectures",
      "Kids",
    ]);
    expect(screen.getByRole("rowheader", { name: "alice" })).toBeInTheDocument();
    expect(screen.getByRole("rowheader", { name: "bob" })).toBeInTheDocument();
    expect(cell("alice access to Lectures")).toHaveValue("admin");
    expect(cell("bob access to Kids")).toHaveValue("read");
    expect(cell("alice access to Kids")).toHaveValue("none");
    expect(cell("bob access to Lectures")).toHaveValue("none");
    expect(saveBtn()).toHaveTextContent("Save");
    expect(saveBtn()).toBeDisabled();
  });

  it("ACL-03: changing a cell and saving sends only the diff", async () => {
    let resolve!: (v: unknown) => void;
    put.mockReturnValue(new Promise((r) => (resolve = r)));
    open();
    const user = userEvent.setup();
    await user.selectOptions(await findCell("bob access to Lectures"), "write");
    expect(saveBtn()).toHaveTextContent("Save 1 change(s)");
    await user.click(saveBtn());
    expect(put).toHaveBeenCalledWith(URL, {
      grants: [{ user_id: "u2", library_id: "l1", role: "write" }],
    });
    expect(saveBtn()).toHaveAttribute("aria-busy");
    resolve({
      grants: [...DATA.grants, { user_id: "u2", library_id: "l1", role: "write" }],
    });
    expect(await screen.findByText("Access permissions saved.")).toBeInTheDocument();
    expect(cell("bob access to Lectures")).toHaveValue("write");
    expect(saveBtn()).toHaveTextContent(/^Save$/);
    expect(saveBtn()).toBeDisabled();
  });

  it("ACL-04: setting a cell to None revokes it", async () => {
    put.mockResolvedValue({ grants: [DATA.grants[1]] });
    open();
    const user = userEvent.setup();
    await user.selectOptions(await findCell("alice access to Lectures"), "none");
    await user.click(saveBtn());
    expect(put).toHaveBeenCalledWith(URL, {
      grants: [{ user_id: "u1", library_id: "l1", role: "none" }],
    });
    await screen.findByText("Access permissions saved.");
    expect(cell("alice access to Lectures")).toHaveValue("none");
  });

  it("ACL-05: changing a cell back removes it from the diff", async () => {
    open();
    const user = userEvent.setup();
    const c = await findCell("bob access to Lectures");
    await user.selectOptions(c, "read");
    expect(saveBtn()).toHaveTextContent("Save 1 change(s)");
    await user.selectOptions(c, "none");
    expect(saveBtn()).toHaveTextContent(/^Save$/);
    expect(saveBtn()).toBeDisabled();
    expect(put).not.toHaveBeenCalled();
  });

  it("ACL-06: count label and one PUT on a double click", async () => {
    put.mockReturnValue(new Promise(() => {}));
    open();
    const user = userEvent.setup();
    await user.selectOptions(await findCell("bob access to Lectures"), "read");
    await user.selectOptions(cell("alice access to Kids"), "write");
    await user.selectOptions(cell("alice access to Lectures"), "read");
    expect(saveBtn()).toHaveTextContent("Save 3 change(s)");
    await user.dblClick(saveBtn());
    expect(put).toHaveBeenCalledTimes(1);
  });

  it.each([
    ["400 problem", problem(400, "bad request", "unknown role"), "unknown role"],
    ["403 problem", problem(403, "forbidden"), "forbidden"],
    ["500 text", problem(500, "Internal Server Error", "upstream exploded"), "upstream exploded"],
    ["network error", new TypeError("Failed to fetch"), "Something went wrong."],
  ])("ACL-07: save failure (%s) keeps edits and shows an error toast", async (_n, err, text) => {
    put.mockRejectedValue(err);
    open();
    const user = userEvent.setup();
    await user.selectOptions(await findCell("bob access to Lectures"), "write");
    await user.click(saveBtn());
    expect(await screen.findByText(text)).toBeInTheDocument();
    expect(cell("bob access to Lectures")).toHaveValue("write");
    expect(saveBtn()).toHaveTextContent("Save 1 change(s)");
    expect(saveBtn()).toBeEnabled();
  });

  it.each([
    ["grants: null", { grants: null }],
    ["{}", {}],
  ])("ACL-08: save response %s shows every cell None", async (_n, res) => {
    put.mockResolvedValue(res);
    open();
    const user = userEvent.setup();
    await user.selectOptions(await findCell("alice access to Lectures"), "none");
    await user.selectOptions(cell("bob access to Kids"), "none");
    await user.click(saveBtn());
    expect(await screen.findByText("Access permissions saved.")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
    for (const c of screen.getAllByRole("combobox")) expect(c).toHaveValue("none");
    expect(saveBtn()).toBeDisabled();
  });

  it.each([
    ["no users", { users: [], libraries: DATA.libraries, grants: [] }],
    ["no libraries", { users: DATA.users, libraries: [], grants: [] }],
  ])("ACL-09: %s shows Nothing to map", async (_n, data) => {
    open(data);
    expect(await screen.findByText("Nothing to map")).toBeInTheDocument();
    expect(
      screen.getByText("Add at least one user and one library to manage access.")
    ).toBeInTheDocument();
    expect(screen.queryByRole("table")).toBeNull();
  });

  const { users, libraries } = DATA;
  it.each([
    ["users: null", { users: null, libraries, grants: [] }],
    ["users missing", { libraries, grants: [] }],
    ["libraries: null", { users, libraries: null, grants: [] }],
    ["libraries missing", { users, grants: [] }],
    ["whole body {}", {}],
    ["body null", null],
  ])("ACL-10: %s shows Nothing to map, no crash", async (_n, data) => {
    quietCrash();
    open(data);
    expect(await screen.findByText("Nothing to map")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it.each([
    ["grants: null", { users, libraries, grants: null }],
    ["grants missing", { users, libraries }],
  ])("ACL-10: %s shows the full matrix with every cell None", async (_n, data) => {
    open(data);
    await screen.findByRole("table", { name: "Library access control" });
    const cells = screen.getAllByRole("combobox");
    expect(cells).toHaveLength(4);
    for (const c of cells) expect(c).toHaveValue("none");
  });

  it.each([
    ["403 problem", problem(403, "forbidden", "admin only"), "admin only"],
    ["404 text", problem(404, "Not Found", "404 page not found"), "404 page not found"],
    ["500 no detail", problem(500, "internal"), "internal"],
    ["network", new TypeError("Failed to fetch"), "Something went wrong."],
    ["bad JSON", new SyntaxError("Unexpected token b in JSON"), "Something went wrong."],
  ])("ACL-11: GET error (%s) shows ErrorState and Try again recovers", async (_n, err, text) => {
    let fail = true;
    let resolveRetry!: (v: unknown) => void;
    get.mockImplementation(() =>
      fail ? Promise.reject(err) : new Promise((r) => (resolveRetry = r))
    );
    renderPage(
      <ToastProvider>
        <LibraryACL />
      </ToastProvider>,
      { path: "/admin/library-acl" }
    );
    expect((await screen.findAllByText(text)).length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText("Something went wrong.").length).toBeGreaterThanOrEqual(1);
    expect(saveBtn()).toBeDisabled();
    fail = false;
    await userEvent.setup().click(screen.getByRole("button", { name: "Try again" }));
    // While retrying: Loading…, not "Nothing to map"
    expect(screen.getByText("Loading…")).toBeInTheDocument();
    expect(screen.queryByText("Nothing to map")).toBeNull();
    resolveRetry(DATA);
    expect(
      await screen.findByRole("table", { name: "Library access control" })
    ).toBeInTheDocument();
  });

  // Wanted: a "you have unsaved changes" prompt when leaving. Needs a data
  // router (useBlocker) and a new i18n string; see the report.
  it.todo("ACL-14: leaving with unsaved changes asks first");

  it("ACL-15: a page crash shows the route error fallback and Back works", async () => {
    quietCrash();
    let data: unknown = { ...DATA, users: [{ id: "u1", username: { bad: 1 } }] };
    get.mockImplementation(() => Promise.resolve(data));
    renderPage(
      <ToastProvider>
        <LibraryACL />
      </ToastProvider>,
      { path: "/admin/library-acl" }
    );
    expect(await screen.findByRole("alert")).toBeInTheDocument();
    const user = userEvent.setup();
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(screen.getByText("elsewhere")).toBeInTheDocument();
    data = DATA;
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(
      await screen.findByRole("table", { name: "Library access control" })
    ).toBeInTheDocument();
  });

  it("ACL-16: non-admin sees Admins only and no request", () => {
    open(DATA, memberUser);
    expect(screen.getByText("Admins only")).toBeInTheDocument();
    expect(
      screen.getByText("You need administrator access to view this page.")
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Save/ })).toBeNull();
    expect(get).not.toHaveBeenCalled();
  });

  it("ACL-17: signed-out user is redirected to /login", () => {
    open(DATA, null);
    expect(screen.getByText("login page (from /admin/library-acl)")).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("ACL-18: Back to the page refetches and renders the matrix", async () => {
    open();
    await screen.findByRole("table", { name: "Library access control" });
    const user = userEvent.setup();
    await user.click(screen.getByRole("link", { name: "go elsewhere" }));
    expect(screen.getByText("elsewhere")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(
      await screen.findByRole("table", { name: "Library access control" })
    ).toBeInTheDocument();
    expect(get).toHaveBeenCalledTimes(2);
  });

  it("ACL-22: every cell is labelled and keyboard-changeable; rows use th scope=row", async () => {
    put.mockResolvedValue({ grants: DATA.grants });
    open();
    const table = await screen.findByRole("table", { name: "Library access control" });
    expect(table.querySelectorAll('tbody th[scope="row"]')).toHaveLength(2);
    const names = screen.getAllByRole("combobox").map((c) => c.getAttribute("aria-label"));
    expect(names).toEqual([
      "alice access to Lectures",
      "alice access to Kids",
      "bob access to Lectures",
      "bob access to Kids",
    ]);
    // Tab order runs row by row through native selects (jsdom can't open a
    // native select with the keyboard, so the value is picked directly).
    const user = userEvent.setup();
    cell("alice access to Lectures").focus();
    await user.tab();
    expect(cell("alice access to Kids")).toHaveFocus();
    await user.tab();
    expect(cell("bob access to Lectures")).toHaveFocus();
    await user.selectOptions(cell("bob access to Lectures"), "read");
    await waitFor(() => expect(saveBtn()).toBeEnabled());
  });
});
