// @vitest-environment node
import { describe, expect, it } from "vitest";
import fx from "./testdata/homeauth.json";
import { HEADER_SIGNATURE, HEADER_TIME, signHome, verifyHome } from "./homeauth";

// The home signs with api/internal/tvremote.SignHome; the fixture is one of
// its posts, so the Worker accepts exactly what the home sends.
describe("home post signatures", () => {
  const body = new TextEncoder().encode(fx.body);
  const headers = new Headers({ [HEADER_TIME]: String(fx.ts), [HEADER_SIGNATURE]: fx.sig });

  it("match the home's", async () => {
    expect(await signHome(fx.secret, fx.method, fx.path, fx.ts, body)).toBe(fx.sig);
    expect(await verifyHome(fx.secret, fx.method, fx.path, headers, body, fx.ts + 5)).toEqual({
      ok: true,
      ts: fx.ts,
    });
  });

  it("refuse another body, path, secret or an old time", async () => {
    const other = new TextEncoder().encode(fx.body.replace("abc123xyz", "evil"));
    expect((await verifyHome(fx.secret, fx.method, fx.path, headers, other, fx.ts)).ok).toBe(false);
    expect((await verifyHome(fx.secret, fx.method, "/_home/other", headers, body, fx.ts)).ok).toBe(
      false
    );
    expect((await verifyHome("x" + fx.secret, fx.method, fx.path, headers, body, fx.ts)).ok).toBe(
      false
    );
    expect((await verifyHome(fx.secret, fx.method, fx.path, headers, body, fx.ts + 61)).ok).toBe(
      false
    );
  });
});
