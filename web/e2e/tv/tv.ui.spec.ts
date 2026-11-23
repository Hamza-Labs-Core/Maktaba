// Epic 58 story 58.08 — the TV build (/tv/) in Chromium at 1920×1080 against the mock
// home, driven with D-pad keys only (arrows, Enter, Back):
//
//   T03: each screen, dark, in English and Arabic (screenshots in test-results/tv/shots);
//        the console and the network log are attached to the report.
//   T04: Home → Library → Series with the CPU throttled ×6, each step under the
//        3-second budget between screens.
//   58.15: an Arabic title with a number and an English word reads in the order Roku draws it
//        (shared/i18n/visual-vectors.json), in the bundled Arabic font, in both languages.
//   49.11: Search with the D-pad: suggestions with their kinds, Did you mean, the order and
//        the Filters panel, in both languages.
//   49.14: Search's AI mode tab with the D-pad: the words typed asked, the sources and the
//        cited sentences, a follow-up with its history, a citation playing its moment.
import { expect, test, type Page, type TestInfo } from "@playwright/test";
import { mkdirSync } from "node:fs";
import path from "node:path";
import {
  arabicFontLoaded,
  expectedOrder,
  vectorInput,
  visualOrder,
} from "../../../shared/i18n/visual_order.mjs";
import { BLOCKED_VIDEO, mockHome, type MockHome } from "./mockHome";

const SHOTS = path.resolve(process.cwd(), "test-results/tv/shots");

/** The console and the network, kept for the report. */
function record(page: Page) {
  const consoleLines: string[] = [];
  const network: string[] = [];
  page.on("console", (m) => consoleLines.push(`[${m.type()}] ${m.text()}`));
  page.on("pageerror", (e) => consoleLines.push(`[pageerror] ${e.message}`));
  page.on("requestfinished", async (r) => {
    const res = await r.response();
    network.push(`${res?.status() ?? "-"} ${r.method()} ${r.url()}`);
  });
  page.on("requestfailed", (r) =>
    network.push(`FAILED ${r.method()} ${r.url()} ${r.failure()?.errorText ?? ""}`)
  );
  return {
    consoleLines,
    async attach(info: TestInfo, home?: MockHome) {
      await info.attach("console.log", {
        body: consoleLines.join("\n"),
        contentType: "text/plain",
      });
      await info.attach("network.log", { body: network.join("\n"), contentType: "text/plain" });
      if (home)
        await info.attach("home-calls.log", {
          body: home.calls.join("\n"),
          contentType: "text/plain",
        });
    },
  };
}

async function start(page: Page, locale: "en" | "ar") {
  await page.addInitScript((l) => {
    try {
      localStorage.setItem("mkt:locale", l);
    } catch {
      /* private mode: English */
    }
  }, locale);
}

const focused = (page: Page) =>
  page.evaluate(() => document.activeElement?.getAttribute("data-bs-focus") ?? "");

const OPPOSITE: Record<string, string> = {
  ArrowLeft: "ArrowRight",
  ArrowRight: "ArrowLeft",
  ArrowUp: "ArrowDown",
  ArrowDown: "ArrowUp",
};

/**
 * Presses `key` until the focus is on `id` (D-pad only); at the row's end (the
 * focus stops moving) it turns round, as a person would.
 */
async function pressTo(page: Page, key: string, id: string | RegExp, max = 16) {
  const hit = (f: string) => (typeof id === "string" ? f === id : id.test(f));
  let k = key;
  let turned = false;
  let last = await focused(page);
  const path = [last];
  for (let i = 0; i < max; i++) {
    if (hit(last)) return;
    await page.keyboard.press(k);
    await page.waitForTimeout(80);
    const now = await focused(page);
    if (now === last && !turned && OPPOSITE[k]) {
      k = OPPOSITE[k]!;
      turned = true;
    }
    last = now;
    path.push(now);
  }
  throw new Error(`focus never reached ${id} with ${key} (on ${last}) PATH ${path.join(" > ")}`);
}

async function shot(page: Page, name: string) {
  mkdirSync(SHOTS, { recursive: true });
  // the fonts and the posters first: a screenshot of what a person sees
  await page.evaluate(() => document.fonts.ready);
  await page.screenshot({ path: path.join(SHOTS, `${name}.png`) });
}

test("signed out: the sign-in page sends the TV to the home's sign-in", async ({ page }, info) => {
  const log = record(page);
  const home = await mockHome(page, { signedIn: false });
  await start(page, "en");
  await page.goto("/tv/");
  await expect(page.getByTestId("tv-signin")).toBeVisible();
  await shot(page, "signin-en");
  await page.keyboard.press("Enter");
  await page.waitForURL(/\/login\?next=%2Ftv%2F/);
  await log.attach(info, home);
});

for (const locale of ["en", "ar"] as const) {
  test(`every screen with the D-pad only, dark, ${locale}`, async ({ page }, info) => {
    const log = record(page);
    const home = await mockHome(page);
    await start(page, locale);
    // the start: Ink & Brass, the language's direction
    await page.goto("/tv/");
    await expect(page).toHaveURL(/\/tv\/big$/);
    await expect(page.getByTestId("big-hero")).toBeVisible();
    await expect(page.locator("html")).toHaveAttribute("dir", locale === "ar" ? "rtl" : "ltr");
    // the TV build has no Leave big screen button
    await expect(page.getByTestId("big-leave")).toHaveCount(0);
    await shot(page, `home-${locale}`);

    const forward = locale === "ar" ? "ArrowLeft" : "ArrowRight";
    const backward = locale === "ar" ? "ArrowRight" : "ArrowLeft";

    // Libraries: up to the tab row, along it in reading order (mirrored in Arabic)
    await pressTo(page, "ArrowUp", /^tab:/);
    await pressTo(page, forward, "tab:/big/libraries");
    await page.keyboard.press("Enter");
    await expect(page.getByTestId("big-libraries")).toBeVisible();
    await pressTo(page, "ArrowDown", /^lib:/);
    await pressTo(page, forward, "lib:L1");
    await shot(page, `libraries-${locale}`);

    // a library: Recommended first
    await page.keyboard.press("Enter");
    await expect(page).toHaveURL(/\/tv\/big\/library\/L1/);
    await expect(page.locator('[data-bs-focus="ltab:recommended"]')).toBeVisible();
    await shot(page, `library-recommended-${locale}`);

    // the Library tab: the grid and the letter rail (ا…ي first in Arabic)
    await pressTo(page, "ArrowUp", /^ltab:/);
    await pressTo(page, forward, "ltab:library");
    await expect(page.getByTestId("big-letter-rail")).toBeVisible();
    const letters = await page
      .getByTestId("big-letter-rail")
      .locator("[data-bs-focus^='letter:']:not([data-bs-focus='letter:all'])")
      .evaluateAll((els) =>
        els.map((e) => e.getAttribute("data-bs-focus")!.slice("letter:".length))
      );
    expect(letters[0]).toBe(locale === "ar" ? "ا" : "A");
    await shot(page, `library-grid-${locale}`);

    // Collections and Playlists
    await pressTo(page, forward, "ltab:collections");
    await expect(page.getByText("Ramadan")).toBeVisible();
    await shot(page, `library-collections-${locale}`);
    await pressTo(page, forward, "ltab:playlists");
    await expect(page.getByText("Before sleep")).toBeVisible();
    await shot(page, `library-playlists-${locale}`);
    await pressTo(page, backward, "ltab:recommended");

    // Back: to the libraries, the one opened keeps the focus
    await page.keyboard.press("Escape");
    await expect(page.getByTestId("big-libraries")).toBeVisible();

    // Series: the list, then a series with its season pills
    await pressTo(page, "ArrowUp", /^tab:/);
    await pressTo(page, forward, "tab:/big/series");
    await page.keyboard.press("Enter");
    await expect(page.locator('[data-bs-focus="series:S1"]')).toBeVisible();
    await pressTo(page, "ArrowDown", "series:S1");
    await page.keyboard.press("Enter");
    await expect(page.locator('[data-bs-focus="season:2"]')).toBeVisible();
    await shot(page, `series-${locale}`);

    // Back, step by step, to Home; then once more: Leave Maktaba? (Stay has the focus)
    for (let i = 0; i < 4 && !(await page.getByTestId("big-hero").isVisible()); i++) {
      await page.keyboard.press("Escape");
      await page.waitForTimeout(300);
    }
    await expect(page.getByTestId("big-hero")).toBeVisible();
    await page.keyboard.press("Escape");
    const dialog = page.getByTestId("big-exit");
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText(locale === "ar" ? "الخروج من Maktaba؟" : "Leave Maktaba?");
    expect(await focused(page)).toBe("exit-stay");
    await shot(page, `exit-${locale}`);
    await page.keyboard.press("Enter");
    await expect(dialog).toHaveCount(0);

    await log.attach(info, home);
    expect(home.unknown, "every call the screens made has an answer in the mock home").toEqual([]);
    expect(log.consoleLines.filter((l) => l.startsWith("[pageerror]"))).toEqual([]);
  });
}

// Story 49.11: Search with story 49.8's help, D-pad only: two letters on the screen keyboard,
// the suggestions under it with their kinds, Did you mean, the order, the Filters panel (a
// focus layer whose chips combine), the filters in use as chips; each request body checked.
for (const locale of ["en", "ar"] as const) {
  test(`Search: suggestions, did-you-mean, the order and the filters, ${locale} (49.11)`, async ({
    page,
  }, info) => {
    const log = record(page);
    const home = await mockHome(page);
    await start(page, locale);
    await page.goto("/tv/");
    await expect(page.getByTestId("big-hero")).toBeVisible();
    const forward = locale === "ar" ? "ArrowLeft" : "ArrowRight";
    await pressTo(page, "ArrowUp", /^tab:/);
    await pressTo(page, forward, "tab:/big/search");
    await page.keyboard.press("Enter");
    const s = page.getByTestId("big-search");
    await expect(s).toBeVisible();

    // two letters with the screen keyboard: ص then ب, or p then a
    const [first, second] = locale === "ar" ? ["ص", "ب"] : ["p", "a"];
    await expect.poll(() => focused(page)).toMatch(/^key:/);
    await pressTo(page, "ArrowDown", locale === "ar" ? "key:ar:ز" : "key:en:k");
    await pressTo(page, forward, `key:${locale}:${first}`);
    await page.keyboard.press("Enter");
    // up to the first row (the alphabet's start)
    await pressTo(page, "ArrowUp", locale === "ar" ? /^key:ar:[ابتثجحخدذر]$/ : /^key:en:[a-j]$/);
    await pressTo(page, locale === "ar" ? "ArrowRight" : "ArrowLeft", `key:${locale}:${second}`);
    await page.keyboard.press("Enter");
    const typed = `${first}${second}`;
    await expect(s.getByRole("searchbox")).toHaveValue(typed);

    // the suggestions under the keyboard, each with its kind; Down reaches them
    const kinds = s.locator(".bs-suggest__item");
    await expect(kinds).toHaveCount(4);
    await expect(s.locator(".bs-suggest__kind")).toHaveText(
      locale === "ar"
        ? ["بحثت عنه", "كلمة", "مقطع", "متحدث"]
        : ["Recent", "Word", "Video", "Speaker"]
    );
    await expect(page.getByTestId("big-did-you-mean")).toBeVisible();
    expect(home.searches.at(-1)).toEqual({ q: typed, mode: "hybrid", view: "grid" });
    await pressTo(page, "ArrowDown", "sg:0");
    await shot(page, `search-${locale}`);

    // across to the results' column: the order, then Filters
    // across from a suggestion is the result beside it; up from there, the order
    await pressTo(page, forward, /^(st|sm|sort|search):/);
    await pressTo(page, "ArrowUp", /^sort:/);
    await pressTo(page, forward, "sort:newest");
    await page.keyboard.press("Enter");
    await expect.poll(() => home.searches.at(-1)?.sort).toBe("newest");
    await pressTo(page, forward, "search:filters");
    await page.keyboard.press("Enter");
    const panel = page.getByTestId("big-filters");
    await expect(panel).toBeVisible();
    // the panel's first chip has the focus: a library, then a year further down
    await expect.poll(() => focused(page)).toBe("f:library:L1");
    await page.keyboard.press("Enter");
    await expect.poll(() => home.searches.at(-1)?.filters).toEqual({ library_id: ["L1"] });
    await pressTo(page, "ArrowDown", /^f:year:/);
    await pressTo(page, forward, "f:year:2024");
    await page.keyboard.press("Enter");
    await expect
      .poll(() => home.searches.at(-1)?.filters)
      .toEqual({
        library_id: ["L1"],
        date: { gte: "2024-01-01T00:00:00.000Z", lt: "2025-01-01T00:00:00.000Z" },
      });
    await expect(panel.locator('[aria-pressed="true"]')).toHaveCount(2);
    await shot(page, `search-filters-${locale}`);

    // Back closes the panel; the filters in use are chips, and Clear all takes them off
    await page.keyboard.press("Escape");
    await expect(panel).toHaveCount(0);
    await expect(s.locator(".bs-refine__active .bs-chip--remove")).toHaveCount(2);
    await expect.poll(() => focused(page)).toBe("search:filters");
    await pressTo(page, "ArrowDown", /^(rm:|search:clear)/);
    await pressTo(page, forward, "search:clear");
    await page.keyboard.press("Enter");
    await expect.poll(() => home.searches.at(-1)?.filters).toBeUndefined();
    await expect(s.locator(".bs-refine__active")).toHaveCount(0);

    await log.attach(info, home);
    expect(home.unknown, "every call the screen made has an answer in the mock home").toEqual([]);
    expect(log.consoleLines.filter((l) => l.startsWith("[pageerror]"))).toEqual([]);
  });
}

// Story 49.14: AI mode, D-pad only: two letters on the screen keyboard, across to the results'
// column and up to the tabs, AI mode chosen (the words typed are asked), the sources and the
// sentences with their citations; a follow-up typed and asked with Ask carries the first
// turn; a citation plays its video at its moment.
for (const locale of ["en", "ar"] as const) {
  test(`Search: AI mode, a cited answer, a follow-up, a citation played, ${locale} (49.14)`, async ({
    page,
  }, info) => {
    const log = record(page);
    const home = await mockHome(page);
    await start(page, locale);
    await page.goto("/tv/");
    await expect(page.getByTestId("big-hero")).toBeVisible();
    const forward = locale === "ar" ? "ArrowLeft" : "ArrowRight";
    await pressTo(page, "ArrowUp", /^tab:/);
    await pressTo(page, forward, "tab:/big/search");
    await page.keyboard.press("Enter");
    const s = page.getByTestId("big-search");
    await expect(s).toBeVisible();

    // two letters with the screen keyboard, as 49.11 types them
    const [first, second] = locale === "ar" ? ["ص", "ب"] : ["p", "a"];
    await expect.poll(() => focused(page)).toMatch(/^key:/);
    await pressTo(page, "ArrowDown", locale === "ar" ? "key:ar:ز" : "key:en:k");
    await pressTo(page, forward, `key:${locale}:${first}`);
    await page.keyboard.press("Enter");
    await pressTo(page, "ArrowUp", locale === "ar" ? /^key:ar:[ابتثجحخدذر]$/ : /^key:en:[a-j]$/);
    await pressTo(page, locale === "ar" ? "ArrowRight" : "ArrowLeft", `key:${locale}:${second}`);
    await page.keyboard.press("Enter");
    const typed = `${first}${second}`;
    await expect(s.getByRole("searchbox")).toHaveValue(typed);
    await expect.poll(() => home.searches.length).toBeGreaterThan(0);

    // across to the results' column, up to the tabs, AI mode
    await pressTo(page, forward, /^(st|sm|sort|search|sg|tab):/);
    await pressTo(page, "ArrowUp", /^stab:/);
    await pressTo(page, forward, "stab:ai");
    await page.keyboard.press("Enter");
    await expect(
      s.getByRole("tab", { name: locale === "ar" ? "وضع الذكاء الاصطناعي" : "AI mode" })
    ).toHaveAttribute("aria-selected", "true");
    await expect.poll(() => home.asks).toEqual([{ q: typed }]);
    const turn = s.getByTestId("big-ai-turn").first();
    await expect(turn.locator(".bs-match")).toHaveCount(2);
    await expect(turn.locator(".bs-ai__sentence")).toHaveCount(2);
    await expect(turn.locator(".bs-ai__cite")).toHaveCount(2);
    await shot(page, `search-ai-${locale}`);
    const searches = home.searches.length;

    // a follow-up: two more letters after the words asked, then Ask
    // back across to the keyboard (the field keeps left and right: down leaves it)
    await pressTo(page, locale === "ar" ? "ArrowRight" : "ArrowLeft", /^(key:|search:field$)/, 24);
    await pressTo(page, "ArrowDown", /^key:/);
    await page.keyboard.press("Enter");
    await page.keyboard.press("Enter");
    const follow = await s.getByRole("searchbox").inputValue();
    expect(follow).toHaveLength(typed.length + 2);
    await pressTo(page, forward, /^(ai|tab):/, 24);
    await pressTo(page, "ArrowUp", /^stab:/);
    await pressTo(page, "ArrowDown", "ai:ask");
    await page.keyboard.press("Enter");
    await expect.poll(() => home.asks.length).toBe(2);
    expect(home.asks[1]).toEqual({
      q: follow,
      history: [
        {
          q: typed,
          answer:
            locale === "ar"
              ? "الصبر يكون عند الشدة. وصبر الأنبياء على قومهم."
              : "Patience is shown in hardship. The prophets were patient with their people.",
        },
      ],
    });
    await expect(s.getByTestId("big-ai-turn")).toHaveCount(2);
    await expect(s.getByTestId("big-ai-turn").first().locator(".bs-ai__sentence")).toHaveCount(1);
    // the search waited while AI mode was shown
    expect(home.searches.length).toBe(searches);
    await shot(page, `search-ai-followup-${locale}`);

    // down the column to a citation of the newest answer, and it plays its moment
    await pressTo(page, forward, /^(ai|tab):/, 24);
    await pressTo(page, "ArrowDown", /^ai:c:/, 24);
    const cite = await focused(page);
    expect(cite).toMatch(/^ai:c:2:0:1$/);
    expect(home.unknown, "every call the screen made has an answer in the mock home").toEqual([]);
    await page.keyboard.press("Enter");
    await expect(page).toHaveURL(/\/tv\/big\/watch\/v3\?t=312/);

    await log.attach(info, home);
    expect(log.consoleLines.filter((l) => l.startsWith("[pageerror]"))).toEqual([]);
  });
}

// Story 58.15: "الحلقة 3 — Intro" reads Arabic first (on the right), then 3, the dash and Intro,
// as Roku's VisualText draws it (vector episode.06), whatever the screen's language: the title
// is isolated (<bdi>) and takes its direction from its first letter. Joined letters come from
// the bundled Arabic font (Amiri for headings), loaded.
for (const locale of ["en", "ar"] as const) {
  test(`an Arabic title reads in Roku's order, joined, ${locale} (58.15)`, async ({
    page,
  }, info) => {
    const log = record(page);
    const home = await mockHome(page, {
      rename: { "Opening the Book": vectorInput("episode.06") },
    });
    await start(page, locale);
    await page.goto("/tv/");
    const title = page.locator(".bs-hero__title");
    await expect(title).toHaveText(vectorInput("episode.06"));
    await page.evaluate(() => document.fonts.ready);
    expect(await title.evaluate(visualOrder)).toBe(expectedOrder("episode.06"));
    // a face loads when text first needs it, which can be after document.fonts.ready
    await expect
      .poll(() => title.evaluate(arabicFontLoaded), { message: "the title's bundled Arabic font" })
      .toBe(true);
    await shot(page, `arabic-title-${locale}`);
    await log.attach(info, home);
  });
}

test("Home → Library → Series within the budget, CPU throttled ×6 (T04)", async ({
  page,
}, info) => {
  const log = record(page);
  const home = await mockHome(page);
  await start(page, "en");
  const cdp = await page.context().newCDPSession(page);
  await cdp.send("Emulation.setCPUThrottlingRate", { rate: 6 });

  const t0 = Date.now();
  await page.goto("/tv/");
  await expect(page.getByTestId("big-hero")).toBeVisible({ timeout: 15_000 });
  const first = Date.now() - t0;

  await pressTo(page, "ArrowUp", /^tab:/);
  await pressTo(page, "ArrowRight", "tab:/big/libraries");
  await page.keyboard.press("Enter");
  await expect(page.getByTestId("big-libraries")).toBeVisible();
  await pressTo(page, "ArrowDown", /^lib:/);
  await pressTo(page, "ArrowRight", "lib:L1");
  let t = Date.now();
  await page.keyboard.press("Enter");
  await expect(page.locator('[data-bs-focus="ltab:recommended"]')).toBeVisible();
  const toLibrary = Date.now() - t;

  // to a series by the Series tab, timed from OK on it to its seasons
  await page.keyboard.press("Escape");
  await expect(page.getByTestId("big-libraries")).toBeVisible();
  await pressTo(page, "ArrowUp", /^tab:/);
  await pressTo(page, "ArrowRight", "tab:/big/series");
  await page.keyboard.press("Enter");
  await expect(page.locator('[data-bs-focus="series:S1"]')).toBeVisible();
  await pressTo(page, "ArrowDown", "series:S1");
  t = Date.now();
  await page.keyboard.press("Enter");
  await expect(page.locator('[data-bs-focus="season:1"]')).toBeVisible();
  const toSeries = Date.now() - t;

  const timing = { firstScreenMs: first, libraryMs: toLibrary, seriesMs: toSeries };
  await info.attach("timing.json", {
    body: JSON.stringify(timing, null, 2),
    contentType: "application/json",
  });
  await log.attach(info, home);
  // the budget: the first screen under 4 s, each next one under 3 s
  expect(first).toBeLessThan(4_000);
  expect(toLibrary).toBeLessThan(3_000);
  expect(toSeries).toBeLessThan(3_000);
});

// Epic 58 story 58.14 T03: a child's account at 9+ on a Samsung or LG TV. A hidden video
// opened by a link shows the blocked page with Ask a parent focused (D-pad only); OK asks
// the home with the id the page had; Settings says a parent manages the account.
for (const locale of ["en", "ar"] as const) {
  test(`a restricted child: the blocked page and the managed line, ${locale}`, async ({
    page,
  }, info) => {
    const log = record(page);
    const home = await mockHome(page, { restricted: true });
    await start(page, locale);
    await page.goto(`/tv/big/video/${BLOCKED_VIDEO}`);
    const blocked = page.getByTestId("parental-blocked");
    await expect(blocked).toBeVisible();
    await expect(page.locator("html")).toHaveAttribute("dir", locale === "ar" ? "rtl" : "ltr");
    await expect(blocked).toContainText(locale === "ar" ? "هذا غير متاح" : "This isn't available");
    await expect(page.getByTestId("parental-ask-on-phone")).toContainText(
      locale === "ar"
        ? "يمكن لأحد الوالدين الرد من Maktaba على هاتفه."
        : "A parent can answer from Maktaba on their phone."
    );
    // nothing of the video: no title, no picture
    await expect(blocked.locator("img")).toHaveCount(0);
    await expect.poll(() => focused(page)).toBe("parental:ask");
    await shot(page, `parental-blocked-${locale}`);

    await page.keyboard.press("Enter");
    await expect(page.getByTestId("parental-ask-waiting")).toBeVisible();
    expect(home.asked).toEqual([{ subject_kind: "video", subject_id: BLOCKED_VIDEO }]);
    await expect.poll(() => focused(page)).toBe("parental:check");
    await shot(page, `parental-asked-${locale}`);

    await page.goto("/tv/big/settings");
    await expect(page.getByTestId("big-parental-managed-line")).toContainText(
      locale === "ar" ? "يدير أحد الوالدين هذا الحساب." : "A parent manages this account."
    );
    // the catalog's own words for the tier (Arabic writes it +9 behind a mark)
    await expect(page.getByTestId("big-parental-limit")).toContainText(
      locale === "ar" ? "+9" : "Content limit: 9+"
    );
    await shot(page, `parental-settings-${locale}`);

    await log.attach(info, home);
    expect(log.consoleLines.filter((l) => l.startsWith("[pageerror]"))).toEqual([]);
  });
}

// Story 58.09's second part: /tv/ in a launcher's frame takes a cloud code's grant over the
// bridge (hello says sign_in, then maktaba:signIn) and redeems it at the home's token route,
// in its own origin; a refused grant falls back to the home's own code, saying why. The
// launcher here is a page of the same origin (a launcher's origin /tv/ hears), answering
// hello as apps/tv/web does.
const GRANT = "Gr4nt_tv_e2e_" + "q".repeat(30);
const LAUNCHER_HOST = `<!doctype html><html><body style="margin:0;background:#000">
<iframe id="f" src="/tv/?bridge=1&lang=LANG" style="border:0;width:1920px;height:1080px"></iframe>
<script>
  const f = document.getElementById("f");
  window.addEventListener("message", (e) => {
    if (e.source !== f.contentWindow || !e.data || e.data.type !== "maktaba:hello") return;
    const takes = Array.isArray(e.data.takes) && e.data.takes.includes("signIn");
    f.contentWindow.postMessage({ type: "maktaba:hello", re: e.data.id, bridge_level: 1,
      platform: "webos", model: "OLED55CX", year: 2020, engine: "Chromium 79", version: "1.0.0",
      device_caps: { containers: ["mp4"], video: [{ codec: "h264" }], hdr: [], audio: ["aac"],
        passthrough: [], hls_segments: ["ts"] }, keys: [], messages: ["openHomePicker"],
      sign_in: takes }, location.origin);
    if (takes) f.contentWindow.postMessage({ type: "maktaba:signIn", grant: "GRANT" }, location.origin);
  });
</script></body></html>`;

for (const locale of ["en", "ar"] as const) {
  for (const accepted of [true, false]) {
    test(`in a launcher: a cloud code's grant ${accepted ? "signs in" : "refused falls back to the home's code"}, ${locale}`, async ({
      page,
    }, info) => {
      const log = record(page);
      const home = await mockHome(page);
      const redeemed: unknown[] = [];
      let signedIn = false;
      await page.route("**/api/pairing/device/token", async (route) => {
        redeemed.push(route.request().postDataJSON());
        if (!accepted)
          return route.fulfill({
            status: 410,
            contentType: "application/problem+json",
            body: JSON.stringify({ title: "Gone", status: 410 }),
          });
        signedIn = true;
        return route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            access_token: "tv-a",
            access_expires_in: 900,
            refresh_token: "tv-r",
            refresh_expires_in: 2592000,
          }),
        });
      });
      await page.route("**/api/pairing/device", (route) =>
        route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            code: "KQ7M-4R2P",
            device_code: "dc-e2e",
            expires_at: new Date(Date.now() + 600_000).toISOString(),
            verify_path: "/pair",
            verify_url: "",
            interval: 60,
          }),
        })
      );
      await page.route("**/api/auth/me", async (route) =>
        signedIn && (await route.request().headerValue("authorization")) === "Bearer tv-a"
          ? route.fallback()
          : route.fulfill({
              status: 401,
              contentType: "application/json",
              body: JSON.stringify({ title: "Unauthorized", status: 401 }),
            })
      );
      await page.route("**/launcher-host.html", (route) =>
        route.fulfill({
          contentType: "text/html",
          body: LAUNCHER_HOST.replace("LANG", locale).replace("GRANT", GRANT),
        })
      );
      try {
        await page.goto("/launcher-host.html");
        const tv = page.frameLocator("#f");
        if (accepted) {
          await expect(tv.locator('[data-testid="big-home"], [data-bs-focus]').first()).toBeVisible(
            { timeout: 20_000 }
          );
          expect(home.calls).not.toContain("POST /api/pairing/device");
        } else {
          await expect(tv.getByTestId("tv-code-notice")).toBeVisible();
          await expect(tv.getByTestId("tv-code")).toHaveText("KQ7M-4R2P");
          await expect(tv.getByTestId("tv-code-signin")).toHaveAttribute(
            "dir",
            locale === "ar" ? "rtl" : "ltr"
          );
        }
        expect(redeemed).toEqual([{ device_code: GRANT, device_label: "OLED55CX" }]);
        expect(log.consoleLines.some((l) => l.includes(GRANT))).toBe(false);
        await shot(page, `launcher-grant-${accepted ? "signed-in" : "refused"}-${locale}`);
      } finally {
        await log.attach(info, home);
      }
    });
  }
}
