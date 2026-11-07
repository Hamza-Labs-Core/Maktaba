// Epic 50 story 50.13 (T11): the cloud web app when Maktaba Cloud's API is down, in a real
// browser. The API is the test's own (page.route): a session, then /v1/homes failing.
import { expect, test, type Page } from "@playwright/test";

const USER = {
  id: "u1",
  email: "sara@example.com",
  email_verified: true,
  display_name: "Sara",
  plan: "free",
  role: "user",
};
const KITCHEN = {
  server_id: "0a1b2c3d-0000-4000-8000-000000000000",
  name: "Kitchen",
  slug: "kitchen",
  connections: [
    { url: "http://203.0.113.20:32400", kind: "direct" },
    { url: "https://kitchen.relay.maktaba.tv", kind: "relay" },
  ],
};
const json = (status: number, body: unknown) => ({
  status,
  contentType: "application/json",
  body: JSON.stringify(body),
});

/** The cloud answers a session; /v1/homes answers `homes` (a function of the call's number). */
async function cloud(
  page: Page,
  homes: (n: number) => { status: number; contentType?: string; body: string }
) {
  let n = 0;
  await page.route("**/v1/**", (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/v1/auth/refresh")
      return route.fulfill(json(200, { access_token: "t", expires_in: 900, user: USER }));
    if (path === "/v1/homes") return route.fulfill(homes(++n));
    if (path === "/v1/auth/logout") return route.fulfill({ status: 204, body: "" });
    return route.fulfill(json(404, { error: "not_found", message: "" }));
  });
  // the status page's feed: none (the strip shows nothing)
  await page.route("https://status.maktaba.tv/**", (route) =>
    route.fulfill({ status: 404, body: "" })
  );
}

test("a good list is saved, and shown with its time when the API is down", async ({ page }) => {
  await cloud(page, (n) =>
    n === 1
      ? json(200, {
          homes: [
            {
              ...KITCHEN,
              relay_url: "https://kitchen.relay.maktaba.tv",
              online: true,
              linked: true,
              owned: true,
            },
          ],
        })
      : json(503, { error: "unavailable", message: "" })
  );
  await page.goto("/homes");
  await expect(page.getByRole("heading", { name: "Kitchen" })).toBeVisible();

  await page.reload(); // the API is down now
  await expect(
    page.getByText(/Maktaba Cloud can't be reached\. Showing your homes from/)
  ).toBeVisible();
  await expect(page.getByRole("link", { name: "Open directly" })).toHaveAttribute(
    "href",
    "http://203.0.113.20:32400"
  );
  await expect(page.getByRole("link", { name: "Open through Maktaba Cloud" })).toHaveAttribute(
    "href",
    "https://kitchen.relay.maktaba.tv"
  );
  await expect(page.getByText(/more than 30 days old/)).toHaveCount(0);
});

test("an answer that isn't the API's (a captive portal) shows the saved list too; an old list is marked", async ({
  page,
}) => {
  await page.addInitScript(
    (list) => localStorage.setItem("mkt:cloud:homes:u1", JSON.stringify(list)),
    {
      saved_at: new Date(Date.now() - 31 * 24 * 3600_000).toISOString(),
      homes: [KITCHEN],
    }
  );
  await cloud(page, () => ({
    status: 200,
    contentType: "text/html",
    body: "<html>Hotel Wi-Fi</html>",
  }));
  await page.goto("/homes");
  await expect(page.getByText(/Showing your homes from/)).toBeVisible();
  await expect(page.getByText(/more than 30 days old/)).toBeVisible();
});

test("in Arabic, right to left", async ({ page }) => {
  await page.addInitScript(
    (list) => {
      localStorage.setItem("mkt:cloud:locale", "ar");
      localStorage.setItem("mkt:cloud:homes:u1", JSON.stringify(list));
    },
    { saved_at: new Date().toISOString(), homes: [KITCHEN] }
  );
  await cloud(page, () => json(502, { error: "bad_gateway", message: "" }));
  await page.goto("/homes");
  await expect(page.getByText(/تعذّر الوصول إلى سحابة مكتبة/)).toBeVisible();
  await expect(page.locator("html")).toHaveAttribute("dir", "rtl");
  await expect(page.getByRole("link", { name: "افتح مباشرة" })).toBeVisible();
});

test("signing out takes the saved list away", async ({ page }) => {
  await page.addInitScript(
    (list) => {
      if (!sessionStorage.getItem("seeded")) {
        localStorage.setItem("mkt:cloud:homes:u1", JSON.stringify(list));
        sessionStorage.setItem("seeded", "1");
      }
    },
    { saved_at: new Date().toISOString(), homes: [KITCHEN] }
  );
  await cloud(page, () => json(503, { error: "unavailable", message: "" }));
  await page.goto("/homes");
  await expect(page.getByText(/Showing your homes from/)).toBeVisible();
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect
    .poll(() => page.evaluate(() => localStorage.getItem("mkt:cloud:homes:u1")))
    .toBeNull();
});
