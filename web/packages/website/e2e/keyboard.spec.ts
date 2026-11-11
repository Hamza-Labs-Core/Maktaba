// Story 60.04 T04, the keyboard: on every page in both languages (both themes for the focus
// ring), the first Tab reaches a skip link to the main content; Tab then reaches every link and
// control in reading order; each focused control shows an outline of 2 px or more at 3:1
// against the page (2.4.7, 2.4.11), not hidden under a sticky header; and the phone menu, on a
// page whose shell declares one (<meta name="maktaba-menu" content="phone">), opens with Enter,
// closes with Escape and gives focus back to its button.
import { expect, test, type Page } from "@playwright/test";
import { label, settle, sitePages } from "./pages";

/** The page's tab stops in reading order, as a browser should visit them: links, controls and
 * tabindex ≥ 0, visible, one radio per group (the checked one, else the first). */
async function expectedStops(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const sel =
      "a[href], button:not([disabled]), input:not([disabled]):not([type=hidden]), select:not([disabled]), textarea:not([disabled]), summary, [tabindex]";
    const seen = new Set<string>();
    const out: string[] = [];
    for (const el of document.querySelectorAll<HTMLElement>(sel)) {
      if (el.tabIndex < 0) continue;
      if (!el.checkVisibility({ visibilityProperty: true })) {
        // the skip link is off screen, not hidden
        if (!el.classList.contains("skip-link")) continue;
      }
      if (el instanceof HTMLInputElement && el.type === "radio" && el.name) {
        const group = [
          ...document.querySelectorAll<HTMLInputElement>(`input[type=radio][name="${el.name}"]`),
        ];
        const pick = group.find((r) => r.checked) ?? group[0];
        if (seen.has(el.name) || pick !== el) continue;
        seen.add(el.name);
      }
      el.dataset.stop = String(out.length);
      out.push(el.dataset.stop);
    }
    return out;
  });
}

/** What the focused element's ring looks like, and whether anything covers it. */
async function ringOf(page: Page) {
  return page.evaluate(() => {
    const el = document.activeElement as HTMLElement | null;
    if (!el || el === document.body) return null;
    const s = getComputedStyle(el);
    const rgb = (c: string) => {
      const m = c.match(/rgba?\(([^)]+)\)/);
      if (!m) return null;
      const [r, g, b, a = 1] = m[1]
        .split(/[\s,/]+/)
        .filter(Boolean)
        .map(Number);
      return { r, g, b, a };
    };
    const lum = (c: { r: number; g: number; b: number }) => {
      const f = (v: number) => {
        const x = v / 255;
        return x <= 0.04045 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4;
      };
      return 0.2126 * f(c.r) + 0.7152 * f(c.g) + 0.0722 * f(c.b);
    };
    // the colour behind the ring: the nearest ancestor that paints a background
    let bg = null;
    for (let n: HTMLElement | null = el.parentElement; n; n = n.parentElement) {
      const c = rgb(getComputedStyle(n).backgroundColor);
      if (c && c.a > 0.5) {
        bg = c;
        break;
      }
    }
    bg ??= rgb(getComputedStyle(document.documentElement).backgroundColor) ?? {
      r: 255,
      g: 255,
      b: 255,
      a: 1,
    };
    const ring = rgb(s.outlineColor);
    const l1 = ring ? lum(ring) : 0;
    const l2 = lum(bg);
    const contrast = (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05);
    // 2.4.11: the focused control is not entirely hidden (here: its centre is its own)
    const r = el.getBoundingClientRect();
    const at = document.elementFromPoint(
      Math.min(Math.max(r.left + r.width / 2, 0), innerWidth - 1),
      Math.min(Math.max(r.top + r.height / 2, 0), innerHeight - 1)
    );
    const covered = !!at && at !== el && !el.contains(at) && !at.contains(el);
    return {
      stop: el.dataset.stop ?? null,
      what: `${el.tagName.toLowerCase()} "${(el.textContent ?? "").trim().slice(0, 40)}"`,
      style: s.outlineStyle,
      width: parseFloat(s.outlineWidth),
      contrast,
      covered: covered ? `${at!.tagName.toLowerCase()}.${at!.className}` : null,
    };
  });
}

for (const p of sitePages()) {
  test.describe(label(p), () => {
    test("T04: a skip link first, then every link and control in reading order", async ({
      page,
    }) => {
      await page.goto(p.url);
      await settle(page);
      const stops = await expectedStops(page);
      await page.keyboard.press("Tab");
      const skip = page.locator(":focus");
      await expect(skip, "the first Tab stop is the skip link").toHaveClass(/skip-link/);
      const target = (await skip.getAttribute("href")) ?? "";
      expect(target).toMatch(/^#./);
      await expect(page.locator(`main${target}`), "the skip link goes to <main>").toHaveCount(1);
      await expect(skip, "the skip link shows when focused").toBeInViewport();

      const visited: string[] = [String(await skip.getAttribute("data-stop"))];
      for (let i = 1; i < stops.length; i++) {
        await page.keyboard.press("Tab");
        visited.push(
          String(await page.evaluate(() => (document.activeElement as HTMLElement)?.dataset.stop))
        );
      }
      expect(visited).toEqual(stops);

      // Enter on the skip link moves the keyboard past the header, into <main>
      await page.goto(p.url);
      await settle(page);
      await page.keyboard.press("Tab");
      await page.keyboard.press("Enter");
      await expect(page).toHaveURL(new RegExp(`${target}$`));
      await page.keyboard.press("Tab");
      const inMain = await page.evaluate(
        (sel) => !!document.activeElement?.closest(sel) || document.activeElement === document.body,
        `main${target}`
      );
      const mainHasStops = await page
        .locator(`main${target} a[href], main${target} button, main${target} input`)
        .count();
      if (mainHasStops) expect(inMain, "after the skip link, Tab lands in <main>").toBe(true);
    });

    for (const scheme of ["dark", "light"] as const) {
      test(`2.4.7 / 2.4.11: each focused control shows a 2 px ring at 3:1, not covered (${scheme})`, async ({
        page,
      }) => {
        await page.emulateMedia({ colorScheme: scheme });
        for (const width of [360, 1280]) {
          await page.setViewportSize({ width, height: 800 });
          await page.goto(p.url);
          await settle(page);
          const stops = await expectedStops(page);
          const bad: string[] = [];
          for (let i = 0; i < stops.length; i++) {
            await page.keyboard.press("Tab");
            const ring = await ringOf(page);
            if (!ring) continue;
            if (ring.style === "none" || ring.width < 2)
              bad.push(`${ring.what}: outline ${ring.style} ${ring.width}px`);
            else if (ring.contrast < 3)
              bad.push(`${ring.what}: outline contrast ${ring.contrast.toFixed(2)}:1`);
            if (ring.covered) bad.push(`${ring.what}: covered by ${ring.covered}`);
          }
          expect(bad, `at ${width} px`).toEqual([]);
        }
      });
    }

    test("T04: the phone menu opens with Enter, closes with Escape, focus back on its button", async ({
      page,
    }) => {
      await page.setViewportSize({ width: 360, height: 800 });
      await page.goto(p.url);
      await settle(page);
      const declared = await page
        .locator('meta[name="maktaba-menu"]')
        .getAttribute("content")
        .catch(() => null);
      expect(
        declared,
        'the page declares whether its shell has a phone menu (<meta name="maktaba-menu" content="none|phone">)'
      ).toMatch(/^(none|phone)$/);
      const buttons = page.locator("header [aria-expanded][aria-controls]");
      if (declared === "none") {
        // a menu the page does not declare would go untested: refuse it
        await expect(buttons, 'a menu button on a page that declares content="none"').toHaveCount(
          0
        );
        test.skip(
          true,
          'this page\'s shell declares no phone menu (maktaba-menu="none", the placeholder shell); the test runs once the design\'s shell declares "phone"'
        );
        return;
      }
      await expect(buttons, "the declared phone menu's button").toHaveCount(1);
      const button = buttons.first();
      const menu = page.locator(`#${await button.getAttribute("aria-controls")}`);
      await expect(button).toHaveAttribute("aria-expanded", "false");
      await button.focus();
      await page.keyboard.press("Enter");
      await expect(button).toHaveAttribute("aria-expanded", "true");
      await expect(menu).toBeVisible();
      await page.keyboard.press("Escape");
      await expect(button).toHaveAttribute("aria-expanded", "false");
      await expect(menu).toBeHidden();
      await expect(button).toBeFocused();
    });
  });
}
