import { describe, expect, it } from "vitest";

import { homeDbOf } from "./homeDb";

describe("homeDbOf", () => {
  it("reads a node's home_db, and shows a failure only once the machine saw it twice", () => {
    expect(homeDbOf({ home_db: { ok: true } })).toEqual({ ok: true });
    expect(homeDbOf({ home_db: { ok: false, code: "refused", fails: 1 } })).toBeNull();
    expect(homeDbOf({ home_db: { ok: false, code: "refused", fails: 2 } })?.code).toBe("refused");
    // a machine that does not count its failures: shown as it says
    expect(homeDbOf({ home_db: { ok: false, code: "auth_failed" } })?.code).toBe("auth_failed");
    expect(homeDbOf({})).toBeNull();
    expect(homeDbOf({ home_db: "x" })).toBeNull();
  });
});
