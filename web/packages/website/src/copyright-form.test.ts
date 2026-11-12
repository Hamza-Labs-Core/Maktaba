// @vitest-environment node
// Story 60.07: the copyright notice form's flag, its fields and the JSON it sends (the browser
// half, T03 and T04, is e2e/legal.spec.ts).
import { describe, expect, it } from "vitest";
import {
  FIELDS,
  noticeConfig,
  noticePayload,
  PAYLOAD_KEYS,
  renderNoticeForm,
  TURNSTILE_INPUT,
} from "./copyright-form";

const CFG = { endpoint: "https://api.maktaba.tv/v1/moderation/notices", sitekey: "0x4AAA" };

describe("the flag", () => {
  it("is off with neither variable: no form (the page today)", () => {
    expect(noticeConfig({})).toBeNull();
    expect(
      noticeConfig({ WEBSITE_NOTICE_ENDPOINT: " ", WEBSITE_TURNSTILE_SITEKEY: "" })
    ).toBeNull();
  });
  it("is on with both", () => {
    expect(
      noticeConfig({ WEBSITE_NOTICE_ENDPOINT: CFG.endpoint, WEBSITE_TURNSTILE_SITEKEY: "0x4AAA" })
    ).toEqual(CFG);
  });
  it("refuses one without the other, and an endpoint the page's CSP does not allow", () => {
    expect(() => noticeConfig({ WEBSITE_NOTICE_ENDPOINT: CFG.endpoint })).toThrow(/both/);
    expect(() => noticeConfig({ WEBSITE_TURNSTILE_SITEKEY: "k" })).toThrow(/both/);
    expect(() =>
      noticeConfig({
        WEBSITE_NOTICE_ENDPOINT: "https://evil.example/notices",
        WEBSITE_TURNSTILE_SITEKEY: "k",
      })
    ).toThrow(/api\.maktaba\.tv/);
  });
});

describe("the form", () => {
  it("has every § 512(c)(3) element and DSA Art. 16 field, labelled, in both languages", () => {
    for (const lang of ["en", "ar"] as const) {
      const html = renderNoticeForm(lang, CFG);
      for (const f of FIELDS) {
        expect(html, `${lang} ${f.name}`).toContain(`name="${f.name}"`);
        expect(html, `${lang} ${f.name}`).toContain(`<label for="notice-${f.name}">`);
      }
      expect(html).toContain('class="cf-turnstile" data-sitekey="0x4AAA"');
      expect(html).toContain('<script src="https://challenges.cloudflare.com/turnstile/v0/api.js"');
      // where the notice goes, said before the button
      expect(html.indexOf('id="notice-where"')).toBeLessThan(html.indexOf('type="submit"'));
    }
    expect(renderNoticeForm("en", CFG)).toContain("Your notice goes to Maktaba's moderation team.");
    expect(renderNoticeForm("ar", CFG)).toContain("أرسل الإشعار");
  });

  it("sends exactly its fields, the Turnstile token and the language", () => {
    const values: Record<string, string> = {
      claimant_name: "A Person",
      claimant_email: "a@example.com",
      claimant_address: "1 Street",
      claimant_phone: "+1 555 0100",
      rights_holder: "",
      work: "A film",
      urls: "https://watch.maktaba.tv/c/1\nhttps://watch.maktaba.tv/c/2 ",
      programme_time: "",
      explanation: "It is mine.",
      good_faith: "yes",
      signature: "A Person",
      [TURNSTILE_INPUT]: "tok",
      lang: "ar",
      unrelated: "never sent",
    };
    const body = noticePayload((n) => values[n] ?? null);
    expect(Object.keys(body).sort()).toEqual(PAYLOAD_KEYS);
    expect(body.urls).toEqual(["https://watch.maktaba.tv/c/1", "https://watch.maktaba.tv/c/2"]);
    expect(body.good_faith).toBe(true);
    expect(body.accuracy).toBe(false);
    expect(body.turnstile_token).toBe("tok");
    expect(body.lang).toBe("ar");
  });
});
