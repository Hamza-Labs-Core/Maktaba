// Login cases from web/test-cases/login.md, with the real AuthProvider (the
// existing Login.test.tsx mocks useAuth, so these live in their own file).
import { describe, it, expect, vi, beforeEach } from "vitest";
import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get, post } };
});
const { assignLocation } = vi.hoisted(() => ({ assignLocation: vi.fn() }));
vi.mock("../lib/navigation", async () => {
  const actual = await vi.importActual<typeof import("../lib/navigation")>("../lib/navigation");
  return { ...actual, assignLocation };
});

import { amal, deferred, htmlError, problem, publicGet, renderAuth } from "./Auth/authTestHarness";
import en from "../i18n/en.json";
import ar from "../i18n/ar.json";

async function signIn(username = "amal", password = "secret") {
  const user = userEvent.setup();
  await user.type(await screen.findByLabelText(en["login.username"]), username);
  await user.type(screen.getByLabelText(en["login.password"]), password);
  await user.click(screen.getByRole("button", { name: en["login.submit"] }));
  return user;
}

const where = () => screen.findByTestId("where");

const node = (home: unknown) => ({
  role: "node",
  id: "n1",
  name: "Studio Mac",
  version: "1.5.0",
  schema_rev: 134,
  sso: true,
  home,
});

beforeEach(() => {
  get.mockReset();
  post.mockReset();
  assignLocation.mockReset();
  get.mockImplementation(publicGet(null));
  post.mockImplementation((path: string) =>
    path === "/api/auth/login"
      ? Promise.resolve({ user: amal })
      : Promise.reject(new Error(`no mock for POST ${path}`))
  );
});

describe("Login (test-cases/login.md)", () => {
  it("LGN-01: sign in posts the credentials and goes to /library", async () => {
    renderAuth(["/login"]);
    await signIn();
    expect(post).toHaveBeenCalledWith("/api/auth/login", { username: "amal", password: "secret" });
    expect(await where()).toHaveTextContent("/library");
    expect(screen.queryByLabelText(en["login.username"])).not.toBeInTheDocument();
    expect(screen.getByText("signed in as amal")).toBeInTheDocument();
  });

  it("LGN-02: sign in returns to state.from", async () => {
    renderAuth([{ pathname: "/login", state: { from: "/videos/42" } }]);
    await signIn();
    expect(await where()).toHaveTextContent(/^\/videos\/42$/);
    expect(assignLocation).not.toHaveBeenCalled();
  });

  it("LGN-03: a signed-out deep link with no query comes back after sign-in", async () => {
    renderAuth(["/videos/42"]);
    await signIn();
    expect(await where()).toHaveTextContent(/^\/videos\/42$/);
  });

  it("LGN-03: a signed-out deep link keeps its query after sign-in", async () => {
    renderAuth(["/search?q=quran"]);
    await signIn();
    expect(await where()).toHaveTextContent(/^\/search\?q=quran$/);
  });

  it("LGN-04: an app path in ?next= wins over state.from", async () => {
    renderAuth([{ pathname: "/login", search: "?next=%2Fqueue", state: { from: "/videos/42" } }]);
    await signIn();
    expect(await where()).toHaveTextContent(/^\/queue$/);
  });

  it.each([["//evil.example/x"], ["https://evil.example"], [42]])(
    "LGN-07: unsafe state.from %s falls back to /library",
    async (from) => {
      renderAuth([{ pathname: "/login", state: { from } }]);
      await signIn();
      expect(await where()).toHaveTextContent(/^\/library$/);
      expect(assignLocation).not.toHaveBeenCalled();
    }
  );

  it("LGN-08: already signed in, /login goes to /library and Back does not return", async () => {
    get.mockImplementation(publicGet(amal));
    renderAuth(["/register", "/login"]);
    expect(await where()).toHaveTextContent(/^\/library$/);
    expect(screen.queryByLabelText(en["login.username"])).not.toBeInTheDocument();
    await userEvent.setup().click(screen.getByRole("button", { name: "go back" }));
    expect(await where()).toHaveTextContent(/^\/library$/);
  });

  it("LGN-09: already signed in with state.from goes there", async () => {
    get.mockImplementation(publicGet(amal));
    renderAuth([{ pathname: "/login", state: { from: "/series/7" } }]);
    expect(await where()).toHaveTextContent(/^\/series\/7$/);
  });

  it("LGN-10: the session probe finishing after render moves the page on", async () => {
    const me = deferred<unknown>();
    get.mockImplementation(publicGet(null, { "/api/auth/me": () => me.promise }));
    renderAuth(["/login"]);
    expect(await screen.findByLabelText(en["login.username"])).toBeInTheDocument();
    await act(async () => me.resolve({ user: amal }));
    expect(await where()).toHaveTextContent(/^\/library$/);
    expect(screen.getAllByTestId("where")).toHaveLength(1);
  });

  it("LGN-11: a wrong password shows the invalid-credentials alert", async () => {
    post.mockRejectedValue(problem(401, { title: "Unauthorized" }));
    renderAuth(["/login"]);
    await signIn("amal", "wrong");
    expect(await screen.findByRole("alert")).toHaveTextContent(en["login.error"]);
    expect(screen.getByLabelText(en["login.username"])).toHaveValue("amal");
    expect(screen.getByLabelText(en["login.password"])).toHaveValue("wrong");
    expect(screen.getByRole("button", { name: en["login.submit"] })).toBeEnabled();
    expect(screen.queryByTestId("where")).not.toBeInTheDocument();
  });

  it.each([
    ["500", problem(500, { detail: "db down" })],
    ["403", problem(403, { detail: "csrf" })],
    ["HTML 502", htmlError()],
  ])("LGN-12: a %s answer shows the generic alert", async (_name, err) => {
    post.mockRejectedValue(err);
    renderAuth(["/login"]);
    await signIn();
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(en["common.error"]);
    expect(alert).not.toHaveTextContent(/db down|csrf|Bad Gateway/);
  });

  it("LGN-13: a network failure shows the generic alert", async () => {
    post.mockRejectedValue(new TypeError("Failed to fetch"));
    renderAuth(["/login"]);
    await signIn();
    expect(await screen.findByRole("alert")).toHaveTextContent(en["common.error"]);
    expect(screen.getByRole("button", { name: en["login.submit"] })).toBeEnabled();
  });

  it("LGN-14: a 200 answer without user shows an error on /login", async () => {
    post.mockResolvedValue({});
    renderAuth(["/login"]);
    await signIn();
    expect(await screen.findByRole("alert", {}, { timeout: 300 })).toBeInTheDocument();
  });

  it("LGN-15: while submitting the button is disabled and one request is sent", async () => {
    const held = deferred<unknown>();
    post.mockReturnValue(held.promise);
    renderAuth(["/login"]);
    const user = await signIn();
    const busy = screen.getByRole("button", { name: en["common.loading"] });
    expect(busy).toBeDisabled();
    await user.click(busy);
    await user.type(screen.getByLabelText(en["login.password"]), "{Enter}");
    expect(post).toHaveBeenCalledTimes(1);
    await act(async () => held.resolve({ user: amal }));
    expect(await where()).toHaveTextContent("/library");
  });

  it("LGN-17: empty username or password does not submit", async () => {
    renderAuth(["/login"]);
    const user = userEvent.setup();
    const submit = await screen.findByRole("button", { name: en["login.submit"] });
    await user.click(submit);
    await user.type(screen.getByLabelText(en["login.username"]), "amal");
    await user.click(submit);
    expect(post).not.toHaveBeenCalled();
    expect(screen.getByLabelText(en["login.username"])).toBeRequired();
    expect(screen.getByLabelText(en["login.password"])).toBeRequired();
  });

  it("LGN-18: a temporary password signs in to the required change form only", async () => {
    post.mockResolvedValue({ user: { ...amal, must_change_password: true } });
    renderAuth([{ pathname: "/login", state: { from: "/series/7" } }]);
    await signIn("amal", "abcd-EFGH-jkmn-PQRS");
    expect(
      await screen.findByRole("heading", { name: en["mustChange.title"] })
    ).toBeInTheDocument();
    expect(screen.queryByTestId("where")).not.toBeInTheDocument();
  });

  it("LGN-19: on a node the cluster prompt starts SSO back to state.from", async () => {
    get.mockImplementation(
      publicGet(null, {
        "/api/cluster/self": async () =>
          node({ name: "Home NAS", web_url: "http://home.local:8780", reachable: true }),
      })
    );
    renderAuth([{ pathname: "/login", state: { from: "/videos/42" } }]);
    const cont = await screen.findByRole("button", { name: /Home NAS/ });
    expect(screen.getByText(en["clusterSignIn.or"])).toBeInTheDocument();
    await userEvent.setup().click(cont);
    expect(assignLocation).toHaveBeenCalledWith("/api/cluster/sso/start?return=%2Fvideos%2F42");
  });

  it("LGN-19: the cluster prompt is hidden while continuing an SSO authorize", async () => {
    get.mockImplementation(
      publicGet(null, {
        "/api/cluster/self": async () =>
          node({ name: "Home NAS", web_url: "http://home.local:8780", reachable: true }),
      })
    );
    const next = encodeURIComponent("/api/cluster/sso/authorize?node=n1&challenge=abc");
    renderAuth([`/login?next=${next}`]);
    await screen.findByLabelText(en["login.username"]);
    await waitFor(() => expect(get).toHaveBeenCalledWith("/api/auth/me"));
    expect(get).not.toHaveBeenCalledWith("/api/cluster/self");
    expect(screen.queryByRole("button", { name: /Home NAS/ })).not.toBeInTheDocument();
  });

  it.each([
    ["home: null", async () => node(null)],
    ["no home key", async () => ({ ...node(null), home: undefined })],
    ["500", () => Promise.reject(problem(500))],
    ["HTML body", async () => "<html>oops</html>"],
  ])("LGN-20: /api/cluster/self with %s: no prompt, sign-in works", async (_name, answer) => {
    get.mockImplementation(publicGet(null, { "/api/cluster/self": answer }));
    renderAuth(["/login"]);
    await waitFor(() => expect(get).toHaveBeenCalledWith("/api/cluster/self"));
    expect(screen.queryByText(en["clusterSignIn.or"])).not.toBeInTheDocument();
    await signIn();
    expect(await where()).toHaveTextContent("/library");
  });

  it("LGN-21: no forgot-password link; the register link opens its page; Back returns", async () => {
    renderAuth(["/login"]);
    const user = userEvent.setup();
    expect(await screen.findByRole("link", { name: en["login.register"] })).toHaveAttribute(
      "href",
      "/register"
    );
    expect(screen.queryByRole("link", { name: /forgot/i })).not.toBeInTheDocument();
    await user.click(await screen.findByRole("link", { name: en["login.register"] }));
    expect(await screen.findByRole("heading", { name: en["register.title"] })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "go back" }));
    expect(await screen.findByLabelText(en["login.username"])).toBeInTheDocument();
  });

  it("LGN-22: Back after sign-in does not return to /login", async () => {
    renderAuth(["/register", "/library"]);
    const user = await signIn();
    expect(await where()).toHaveTextContent(/^\/library$/);
    await user.click(screen.getByRole("button", { name: "go back" }));
    // /register sends a signed-in user on to /library: never the login form
    expect(await where()).toHaveTextContent(/^\/library$/);
    expect(screen.queryByLabelText(en["login.username"])).not.toBeInTheDocument();
  });

  it("LGN-24: Arabic: right-to-left with translated labels and errors", async () => {
    localStorage.setItem("mkt:locale", "ar");
    post.mockRejectedValue(problem(401));
    renderAuth(["/login"]);
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText(ar["login.username"]), "amal");
    await user.type(screen.getByLabelText(ar["login.password"]), "wrong");
    expect(document.documentElement).toHaveAttribute("dir", "rtl");
    expect(document.documentElement).toHaveAttribute("lang", "ar");
    expect(screen.getByRole("heading", { name: ar["app.title"] })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: ar["login.register"] })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: ar["login.submit"] }));
    expect(await screen.findByRole("alert")).toHaveTextContent(ar["login.error"]);
  });

  it("LGN-27: keyboard order, labels, autocomplete and Enter to submit", async () => {
    renderAuth(["/login"]);
    const username = await screen.findByLabelText(en["login.username"]);
    const password = screen.getByLabelText(en["login.password"]);
    expect(username).toHaveFocus();
    expect(username).toHaveAttribute("autocomplete", "username");
    expect(password).toHaveAttribute("autocomplete", "current-password");
    expect(screen.getByRole("form", { name: en["login.submit"] })).toBeInTheDocument();
    const user = userEvent.setup();
    await user.tab();
    expect(password).toHaveFocus();
    await user.tab();
    expect(screen.getByRole("button", { name: en["login.submit"] })).toHaveFocus();
    await user.tab();
    expect(screen.getByRole("link", { name: en["login.register"] })).toHaveFocus();
    await user.click(username);
    await user.type(username, "amal");
    await user.type(password, "secret{Enter}");
    expect(post).toHaveBeenCalledWith("/api/auth/login", { username: "amal", password: "secret" });
  });
});
