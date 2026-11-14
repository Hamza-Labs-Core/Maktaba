// /tv (story 58.03): approve a TV's Maktaba Cloud code in a browser. Signed in, the person reads
// what the TV is, picks a home linked to this cloud account and confirms; the cloud asks that home
// to approve the code. Cancel sends nothing.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { setAccessToken } from "../shared/api";
import { json, renderAt, session, signedOut, stubFetch, user } from "../test/harness";
import { countryName, parseTvCode, tvCodeProblem, tvPlatformName } from "./ApproveTv";
import type { CloudHome } from "./Homes";

const CODE = "ABCD2345";
const LOOKUP = `/v1/tv/codes/${CODE}`;
const APPROVE = `/v1/tv/codes/${CODE}/approve`;

const KITCHEN: CloudHome = {
  server_id: "0a1b2c3d-0000-4000-8000-000000000000",
  name: "Kitchen",
  slug: "kitchen",
  relay_url: "https://kitchen.relay.maktaba.tv",
  online: true,
  linked: true,
  owned: true,
};
const CABIN: CloudHome = {
  ...KITCHEN,
  server_id: "s2",
  name: "Cabin",
  slug: "cabin",
  online: false,
};
const LOFT: CloudHome = { ...KITCHEN, server_id: "s3", name: "Loft", slug: "loft", linked: false };
const STUDIO: CloudHome = { ...KITCHEN, server_id: "s4", name: "Studio", slug: "studio" };

const TV = {
  code: "ABCD-2345",
  platform: "tizen",
  app_version: "1.4.0",
  country: "JO",
  expires_at: "2026-11-14T12:10:00Z",
};

const signedIn = session(user());
const plain = (el: HTMLElement) => (el.textContent ?? "").replace(/[⁨⁩]/g, "");

function routes(extra: Record<string, Parameters<typeof stubFetch>[0][string]> = {}) {
  return stubFetch({
    "POST /v1/auth/refresh": signedIn,
    [`GET ${LOOKUP}`]: [200, TV],
    "GET /v1/homes": [200, { homes: [KITCHEN] }],
    ...extra,
  });
}

beforeEach(() => {
  setAccessToken(null);
  localStorage.clear();
});
afterEach(() => vi.unstubAllGlobals());

describe("the TV code", () => {
  it("takes either shape, in any case, and strips the dash and spaces", () => {
    expect(parseTvCode("ABCD-2345")).toBe(CODE);
    expect(parseTvCode("abcd2345")).toBe(CODE);
    expect(parseTvCode(" ab cd-23 45 ")).toBe(CODE);
    expect(parseTvCode("ABCD-234")).toBeNull();
    expect(parseTvCode("ABCD-2340")).toBeNull(); // 0 is not in the alphabet
    expect(parseTvCode("ABCI-2345")).toBeNull(); // nor I, L, O, U
    expect(parseTvCode(null)).toBeNull();
  });

  it("says what is wrong with one", () => {
    expect(tvCodeProblem("")).toBe("empty");
    expect(tvCodeProblem("ABC")).toBe("length");
    expect(tvCodeProblem("ABCD-1234")).toBe("chars");
    expect(tvCodeProblem("abcd-2345")).toBeNull();
  });

  it("names the platform by its maker and the country in the reader's language", () => {
    expect(tvPlatformName("tizen")).toBe("Samsung");
    expect(tvPlatformName("webos")).toBe("LG");
    expect(tvPlatformName("vidaa")).toBe("Hisense");
    expect(tvPlatformName("newbox")).toBe("newbox");
    expect(countryName("JO", "en")).toBe("Jordan");
    expect(countryName("JO", "ar")).toBe("الأردن");
    expect(countryName("", "en")).toBe("");
  });
});

describe("approving a TV", () => {
  it("asks, then posts exactly the chosen home to the code's approve address", async () => {
    const f = routes({
      [`POST ${APPROVE}`]: [
        200,
        { status: "approved", platform: "tizen", country: "JO", home_name: "Kitchen" },
      ],
    });
    renderAt(`/tv?c=${CODE}`);
    const q = await screen.findByRole("heading", { level: 2 });
    expect(plain(q)).toBe("Is this your TV? Samsung, in Jordan.");
    // the only linked home is chosen; nothing is approved before the person confirms
    const radio = await screen.findByRole("radio", { name: /Kitchen/ });
    expect(radio).toBeChecked();
    expect(f.find("POST", APPROVE)).toHaveLength(0);
    await userEvent.setup().click(screen.getByRole("button", { name: "Approve this TV" }));
    expect(
      await screen.findByRole("heading", { name: "Approved. The TV is signing in." })
    ).toBeInTheDocument();
    expect(screen.getByText(/It signs in to .*Kitchen/)).toBeInTheDocument();
    const reqs = f.find("POST", APPROVE);
    expect(reqs).toHaveLength(1);
    expect(reqs[0].body).toEqual({ server_id: KITCHEN.server_id });
    expect(reqs[0].headers.Authorization).toBe("Bearer tok");
  });

  it("asks without a country when the TV sent none", async () => {
    routes({ [`GET ${LOOKUP}`]: [200, { ...TV, country: "", platform: "roku" }] });
    renderAt(`/tv?c=abcd-2345`);
    expect(plain(await screen.findByRole("heading", { level: 2 }))).toBe("Is this your TV? Roku.");
  });

  it("asks in Arabic, with and without a country", async () => {
    localStorage.setItem("mkt:cloud:locale", "ar");
    routes();
    const { unmount } = renderAt(`/tv?c=${CODE}`);
    expect(plain(await screen.findByRole("heading", { level: 2 }))).toBe(
      "هل هذا تلفازك؟ Samsung، في الأردن."
    );
    expect(screen.getByRole("button", { name: "الموافقة على هذا التلفاز" })).toBeInTheDocument();
    unmount();
    vi.unstubAllGlobals();
    routes({ [`GET ${LOOKUP}`]: [200, { ...TV, country: "" }] });
    renderAt(`/tv?c=${CODE}`);
    expect(plain(await screen.findByRole("heading", { level: 2 }))).toBe("هل هذا تلفازك؟ Samsung.");
  });

  it("offers only linked homes, an offline one disabled, and none preselected among several", async () => {
    routes({ "GET /v1/homes": [200, { homes: [KITCHEN, CABIN, LOFT, STUDIO] }] });
    renderAt(`/tv?c=${CODE}`);
    const group = await screen.findByRole("radiogroup", { name: "Sign it in to" });
    const radios = within(group).getAllByRole("radio");
    expect(radios).toHaveLength(3);
    expect(within(group).queryByText("Loft")).not.toBeInTheDocument();
    expect(within(group).getByRole("radio", { name: /Cabin/ })).toBeDisabled();
    expect(within(group).getByText("Offline")).toBeInTheDocument();
    radios.forEach((r) => expect(r).not.toBeChecked());
    const approve = screen.getByRole("button", { name: "Approve this TV" });
    expect(approve).toBeDisabled();
    await userEvent.setup().click(within(group).getByRole("radio", { name: /Studio/ }));
    expect(approve).toBeEnabled();
  });

  it("explains how to link a home when none is linked", async () => {
    const f = routes({ "GET /v1/homes": [200, { homes: [LOFT] }] });
    renderAt(`/tv?c=${CODE}`);
    expect(await screen.findByText(/None of your homes is linked/)).toBeInTheDocument();
    expect(screen.getByText(/Profile → Approve a TV/)).toBeInTheDocument();
    expect(screen.queryByRole("radio")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Approve this TV" })).toBeDisabled();
    expect(f.find("POST", APPROVE)).toHaveLength(0);
  });

  it("approves nothing on Cancel", async () => {
    const f = routes();
    renderAt(`/tv?c=${CODE}`);
    await screen.findByRole("radio", { name: /Kitchen/ });
    await userEvent.setup().click(screen.getByRole("button", { name: "Cancel, not my TV" }));
    expect(
      await screen.findByRole("heading", { name: "Nothing was approved." })
    ).toBeInTheDocument();
    expect(f.calls.filter((c) => c.method === "POST" && c.path.startsWith("/v1/tv"))).toHaveLength(
      0
    );
  });

  it("signs in first, then comes back to the same code", async () => {
    const f = stubFetch({
      "POST /v1/auth/refresh": signedOut,
      "GET /v1/auth/providers": [200, { google: false, apple: false }],
      "POST /v1/auth/login": [200, { access_token: "tok", expires_in: 900, user: user() }],
      [`GET ${LOOKUP}`]: [200, TV],
      "GET /v1/homes": [200, { homes: [KITCHEN] }],
    });
    renderAt(`/tv?c=${CODE}`);
    const ue = userEvent.setup();
    await ue.type(await screen.findByLabelText(/^Email/), "viewer@example.com");
    await ue.type(screen.getByLabelText(/^Password/), "correct horse");
    expect(f.find("GET", LOOKUP)).toHaveLength(0);
    await ue.click(screen.getByRole("button", { name: "Sign in" }));
    expect(plain(await screen.findByRole("heading", { level: 2 }))).toBe(
      "Is this your TV? Samsung, in Jordan."
    );
    expect(f.find("GET", LOOKUP)).toHaveLength(1);
  });
});

describe("typing a code", () => {
  it("asks for one when the link has none, and goes on with a typed one", async () => {
    const f = routes();
    renderAt("/tv");
    const field = await screen.findByLabelText("Code on the TV");
    const ue = userEvent.setup();
    await ue.type(field, "abcd-2345");
    await ue.click(screen.getByRole("button", { name: "Continue" }));
    expect(plain(await screen.findByRole("heading", { level: 2 }))).toBe(
      "Is this your TV? Samsung, in Jordan."
    );
    expect(f.find("GET", LOOKUP)).toHaveLength(1);
  });

  it("refuses characters outside the alphabet, and a code from the link that is not one", async () => {
    const f = routes();
    renderAt("/tv?c=ABCD-1234");
    expect(await screen.findByText(/That is not a TV code/)).toBeInTheDocument();
    expect(screen.getByLabelText("Code on the TV")).toHaveValue("ABCD-1234");
    expect(f.find("GET", "/v1/tv/codes/ABCD1234")).toHaveLength(0);
    const ue = userEvent.setup();
    await ue.clear(screen.getByLabelText("Code on the TV"));
    await ue.type(screen.getByLabelText("Code on the TV"), "ABC");
    await ue.click(screen.getByRole("button", { name: "Continue" }));
    expect(await screen.findByText("The code has 8 letters and digits.")).toBeInTheDocument();
  });
});

describe("what the cloud refuses", () => {
  it("says a code that is not open is wrong, expired or used", async () => {
    routes({ [`GET ${LOOKUP}`]: [404, { error: "not_found", message: "no such code" }] });
    renderAt(`/tv?c=${CODE}`);
    expect(await screen.findByRole("alert")).toHaveTextContent(/wrong, expired or already used/);
    expect(screen.getByRole("button", { name: "Enter another code" })).toBeInTheDocument();
  });

  it("asks to wait when rate limited, for as long as the cloud says", async () => {
    routes({
      [`GET ${LOOKUP}`]: () =>
        json(429, { error: "rate_limited", message: "slow down" }, { "Retry-After": "30" }),
    });
    renderAt(`/tv?c=${CODE}`);
    expect(await screen.findByRole("alert")).toHaveTextContent("Try again in 30 seconds.");
  });

  const cases: [number, string, RegExp][] = [
    [404, "not_found", /no longer open/],
    [403, "not_linked", /link was removed on the home/],
    [409, "home_offline", /home is offline, so nothing was approved/],
    [503, "home_unreachable", /could not reach that home, so nothing was approved/],
    [409, "already_approved", /Someone already approved this code/],
    [429, "rate_limited", /Too many tries/],
  ];
  it.each(cases)("says why on %i %s", async (status, error, message) => {
    routes({ [`POST ${APPROVE}`]: [status, { error, message: "server words" }] });
    renderAt(`/tv?c=${CODE}`);
    await screen.findByRole("radio", { name: /Kitchen/ });
    await userEvent.setup().click(screen.getByRole("button", { name: "Approve this TV" }));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(message);
    expect(screen.queryByText("Approved. The TV is signing in.")).not.toBeInTheDocument();
    if (error === "not_found" || error === "already_approved") {
      expect(screen.getByRole("button", { name: "Enter another code" })).toBeInTheDocument();
      expect(screen.queryByRole("button", { name: "Approve this TV" })).not.toBeInTheDocument();
    } else {
      await waitFor(() =>
        expect(screen.getByRole("button", { name: "Approve this TV" })).toBeInTheDocument()
      );
    }
  });

  it("says each refusal in Arabic too", async () => {
    localStorage.setItem("mkt:cloud:locale", "ar");
    routes({ [`POST ${APPROVE}`]: [409, { error: "home_offline", message: "offline" }] });
    renderAt(`/tv?c=${CODE}`);
    await screen.findByRole("radio", { name: /Kitchen/ });
    await userEvent.setup().click(screen.getByRole("button", { name: "الموافقة على هذا التلفاز" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "ذلك المنزل غير متصل، لذا لم تتم الموافقة على شيء."
    );
  });
});
