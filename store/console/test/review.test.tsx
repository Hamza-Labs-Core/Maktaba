// Story 54.16 §3 (the page): the queue in English and Arabic, and a decision that asks for a
// passkey on 401 reauth_required.
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { REVIEW_WORDS, Review } from "../src/admin/Review";

afterEach(cleanup);

const ITEMS = [
  {
    id: "s1",
    plugin: "dev.example.a",
    version: "1.0.0",
    status: "in_review",
    first_version: true,
    reasons: ["first_version"],
    flags: [],
    business_days: 6,
    over_sla: true,
  },
  {
    id: "s2",
    plugin: "dev.example.b",
    version: "1.1.0",
    status: "asked",
    first_version: false,
    reasons: ["new_capability"],
    flags: [],
    business_days: 1,
    over_sla: false,
  },
];

function server(routes: Record<string, () => [number, unknown]>) {
  return vi.fn(async (path: string, init?: RequestInit) => {
    const h = routes[`${init?.method ?? "GET"} ${path}`];
    const [status, body] = h ? h() : [404, { error: "not_found" }];
    return new Response(JSON.stringify(body), { status });
  });
}

describe("the review pages", () => {
  it("every word in both languages", () => {
    expect(Object.keys(REVIEW_WORDS.ar).sort()).toEqual(Object.keys(REVIEW_WORDS.en).sort());
  });

  for (const lang of ["en", "ar"] as const) {
    it(`${lang}: the queue marks first submissions, the target and the asked ones`, async () => {
      vi.stubGlobal("fetch", server({ "GET /v1/admin/queue": () => [200, { items: ITEMS }] }));
      render(<Review initialLang={lang} />);
      expect(await screen.findByText(REVIEW_WORDS[lang]["review.first"])).toBeTruthy();
      expect(screen.getByText(REVIEW_WORDS[lang]["review.over_sla"])).toBeTruthy();
      expect(screen.getByText(REVIEW_WORDS[lang]["review.asked"])).toBeTruthy();
      expect(document.documentElement.dir).toBe(lang === "ar" ? "rtl" : "ltr");
    });
  }

  it("approve answered reauth_required asks for the passkey, then decides", async () => {
    let fresh = false;
    const getCred = vi.fn(async () => ({
      id: "x",
      rawId: new ArrayBuffer(1),
      type: "public-key",
      response: { clientDataJSON: new ArrayBuffer(1), authenticatorData: new ArrayBuffer(1), signature: new ArrayBuffer(1) },
    }));
    vi.stubGlobal("navigator", { ...navigator, credentials: { get: getCred } });
    vi.stubGlobal(
      "fetch",
      server({
        "GET /v1/admin/queue": () => [200, { items: ITEMS }],
        "GET /v1/admin/submissions/s1": () => [
          200,
          {
            submission: ITEMS[0],
            version: { notes_en: "", notes_ar: "", manifest: "{}" },
            report: { checks: [], capabilities: { required: [], optional: [] } },
            developer: "Dev",
            source: null,
          },
        ],
        "POST /v1/admin/submissions/s1/approve": () => (fresh ? [200, { status: "approved" }] : [401, { error: "reauth_required" }]),
        "POST /v1/reauth/passkey/options": () => [200, { challenge_id: "c", publicKey: { challenge: "AA" } }],
        "POST /v1/reauth": () => ((fresh = true), [200, {}]),
      }),
    );
    render(<Review />);
    const open = await screen.findAllByRole("button", { name: REVIEW_WORDS.en["review.open"] });
    await act(async () => fireEvent.click(open[0]!));
    const approve = await screen.findByRole("button", { name: REVIEW_WORDS.en["review.approve"] });
    await act(async () => fireEvent.click(approve));
    expect(await screen.findByRole("status")).toHaveProperty("textContent", REVIEW_WORDS.en["review.done"]);
    expect(getCred).toHaveBeenCalled();
  });
});
