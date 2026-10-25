import { describe, expect, it } from "vitest";

import { accessAnswerWords, homeDbOf } from "./homeDb";

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

describe("accessAnswerWords", () => {
  const has = (key: string) => key !== "machines.homeDb.answerCode.something_new";
  it("words the home's answer by its code, an unknown one as other", () => {
    expect(accessAnswerWords({ ok: true }, has)).toEqual({
      key: "machines.homeDb.answerOk",
      reasonKey: "",
    });
    expect(accessAnswerWords({ ok: false, code: "cannot_alter_role" }, has).reasonKey).toBe(
      "machines.homeDb.answerCode.cannot_alter_role"
    );
    expect(accessAnswerWords({ ok: false, code: "something_new" }, has).reasonKey).toBe(
      "machines.homeDb.answerCode.other"
    );
    expect(accessAnswerWords({ ok: false }, has).key).toBe("machines.homeDb.answer");
  });
});
