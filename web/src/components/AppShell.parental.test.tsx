// Epic 57: what the shell links to under parental controls. The home refuses a
// restricted account a personal access token (403 restricted), so its pages carry no
// link to that page; a guardian who is not an admin gets the way to the time rules.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { ToastProvider } from "@ds/components/Toast/Toast";

const { get } = vi.hoisted(() => ({ get: vi.fn() }));
vi.mock("../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../lib/api")>("../lib/api");
  return { ...actual, api: { ...actual.api, get } };
});

import { AppShell, MoreMenu, secondaryNav } from "./AppShell";
import { I18nProvider } from "../lib/i18n";
import { ShortcutProvider } from "../lib/keyboard/shortcuts";
import { ParentalFixture, type MyParental } from "../lib/parental";
import { renderPage, memberUser, adminUser, TestAuth } from "../test/pageHarness";
import type { User } from "../lib/auth";
import { APITokens } from "../pages/Profile/APITokens";
import { errorText, restrictedText } from "../lib/errorText";
import { ApiError } from "../lib/api";
import en from "../i18n/en.json";
import ar from "../i18n/ar.json";

const child = { id: "u1", username: "sara", maxTier: 9 };
const hrefs = (p: Pick<MyParental, "restricted" | "children">, manage: boolean) =>
  secondaryNav(p, manage).map((i) => i.to);

function more(value: Partial<MyParental>, user: User = memberUser, locale: "en" | "ar" = "en") {
  localStorage.setItem("mkt:locale", locale);
  return renderPage(
    <ParentalFixture value={value}>
      <MoreMenu />
    </ParentalFixture>,
    { path: "/more", user }
  );
}

beforeEach(() => {
  get.mockReset();
  get.mockResolvedValue({ items: [] });
  localStorage.clear();
});

describe("secondaryNav", () => {
  it("is everyone's list for an account with no part in parental controls", () => {
    const list = hrefs({ restricted: false, children: [] }, false);
    expect(list).toContain("/account/tokens");
    expect(list).not.toContain("/family");
  });

  it("a restricted account gets no link to API tokens, and none to the time rules", () => {
    const list = hrefs({ restricted: true, children: [] }, false);
    expect(list).not.toContain("/account/tokens");
    expect(list).not.toContain("/family");
    // everything else stays: the account page, history, help
    expect(list).toEqual(expect.arrayContaining(["/account", "/account/history", "/help"]));
  });

  it("a guardian who is not an admin gets Family, before Help", () => {
    const list = hrefs({ restricted: false, children: [child] }, false);
    expect(list.slice(-2)).toEqual(["/family", "/help"]);
  });

  it("an admin has it in the Manage group instead", () => {
    expect(hrefs({ restricted: false, children: [child] }, true)).not.toContain("/family");
  });
});

describe("the More page", () => {
  it("a restricted account sees no API tokens link and no admin link", () => {
    more({ restricted: true, maxTier: 9 });
    expect(screen.queryByRole("link", { name: en["nav.tokens"] })).toBeNull();
    expect(screen.queryByRole("link", { name: en["nav.family"] })).toBeNull();
    expect(document.querySelector('a[href^="/admin/"]')).toBeNull();
    expect(document.querySelector('a[href="/parental/review"]')).toBeNull();
    expect(screen.getByRole("link", { name: en["nav.history"] })).toBeInTheDocument();
  });

  it("a guardian sees Family; a viewer does not", () => {
    const { unmount } = more({ children: [child] });
    expect(screen.getByRole("link", { name: "Family" })).toHaveAttribute("href", "/family");
    unmount();
    more({});
    expect(screen.queryByRole("link", { name: "Family" })).toBeNull();
  });

  it("an admin sees Family and Detected ratings among the admin pages", () => {
    more({}, adminUser);
    expect(screen.getByRole("link", { name: "Family" })).toHaveAttribute("href", "/family");
    expect(screen.getByRole("link", { name: "Detected ratings" })).toHaveAttribute(
      "href",
      "/parental/review"
    );
  });

  it("shows how many requests wait, beside Family", () => {
    more({ children: [child], pendingRequests: 4 });
    const link = screen.getByRole("link", { name: /Family/ });
    expect(within(link).getByLabelText("Requests waiting: 4")).toHaveTextContent("4");
  });

  it("counts them in the language's own digits, to the eye and to a screen reader", () => {
    more({ children: [child], pendingRequests: 4 }, memberUser, "ar");
    const link = screen.getByRole("link", { name: new RegExp(ar["nav.family"]) });
    const count = within(link).getByLabelText("طلبات بالانتظار: ٤");
    expect(count).toHaveTextContent("٤");
    expect(count).not.toHaveTextContent("4");
  });

  it("past ninety-nine says so in the language's own digits", () => {
    const { unmount } = more({ children: [child], pendingRequests: 120 }, memberUser, "ar");
    expect(screen.getByLabelText("طلبات بالانتظار: ١٢٠")).toHaveTextContent("٩٩+");
    unmount();
    more({ children: [child], pendingRequests: 120 });
    expect(screen.getByLabelText("Requests waiting: 120")).toHaveTextContent("99+");
  });

  it("names them in Arabic", () => {
    more({ children: [child] }, memberUser, "ar");
    expect(screen.getByRole("link", { name: ar["nav.family"] })).toBeInTheDocument();
  });
});

describe("the sidebar's Family count", () => {
  function shell(pendingRequests: number, locale: "en" | "ar") {
    localStorage.setItem("mkt:locale", locale);
    return render(
      <MemoryRouter>
        <I18nProvider>
          <ToastProvider>
            <TestAuth user={memberUser}>
              <ShortcutProvider>
                <ParentalFixture value={{ children: [child], pendingRequests }}>
                  <AppShell />
                </ParentalFixture>
              </ShortcutProvider>
            </TestAuth>
          </ToastProvider>
        </I18nProvider>
      </MemoryRouter>
    );
  }

  it("is in the language's own digits, to the eye and to a screen reader", async () => {
    shell(4, "ar");
    const count = await screen.findByTestId("family-count");
    expect(count).toHaveTextContent("٤");
    expect(count).toHaveAttribute("aria-label", "طلبات بالانتظار: ٤");
  });

  it("is Western digits in English", async () => {
    shell(4, "en");
    const count = await screen.findByTestId("family-count");
    expect(count).toHaveTextContent("4");
    expect(count).toHaveAttribute("aria-label", "Requests waiting: 4");
  });
});

describe("the API tokens page for a restricted account", () => {
  it("offers no way to make a token, and says why", async () => {
    renderPage(
      <ParentalFixture value={{ restricted: true, maxTier: 9 }}>
        <APITokens />
      </ParentalFixture>,
      { path: "/account/tokens", user: memberUser }
    );
    expect(await screen.findByTestId("tokens-restricted")).toHaveTextContent(
      "This isn't available for your account."
    );
    expect(screen.queryByRole("button", { name: en["tokens.create"] })).toBeNull();
  });

  it("is unchanged for anyone else", async () => {
    renderPage(<APITokens />, { path: "/account/tokens", user: memberUser });
    expect(await screen.findByRole("button", { name: en["tokens.create"] })).toBeInTheDocument();
    expect(screen.queryByTestId("tokens-restricted")).toBeNull();
  });
});

describe("the home's 403 restricted, in the app's words", () => {
  const t = (k: string) => (en as Record<string, string>)[k] ?? k;
  const refusal = new ApiError({
    type: "https://maktaba.tv/problems/restricted",
    title: "a restricted account cannot do this",
    status: 403,
    code: "restricted",
  });

  it("says it is not available for the account, never the home's own title", () => {
    expect(restrictedText(refusal, t)).toBe("This isn't available for your account.");
    expect(errorText(refusal, t)).toBe("This isn't available for your account.");
  });

  it("leaves every other refusal as it was", () => {
    const plain = new ApiError({ type: "about:blank", title: "forbidden", status: 403 });
    expect(restrictedText(plain, t)).toBeNull();
    expect(errorText(plain, t)).toBe(en["error.kind.noAccess"]);
  });
});
