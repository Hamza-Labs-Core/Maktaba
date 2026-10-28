import { describe, it, expect } from "vitest";
import { cachedAnswer, forgetAnswers, keepAnswer } from "./pageCache";

describe("pageCache", () => {
  it("gives a page's answer back to the user who loaded it", () => {
    keepAnswer("u1", "home", { rows: 1 });
    expect(cachedAnswer("u1", "home")).toEqual({ rows: 1 });
    expect(cachedAnswer("u1", "libraries")).toBeUndefined();
  });

  it("never to another user, and that user's turn drops it", () => {
    keepAnswer("u1", "home", { rows: 1 });
    expect(cachedAnswer("u2", "home")).toBeUndefined();
    expect(cachedAnswer("u1", "home")).toBeUndefined();
  });

  it("keeps nothing without a user, and forgets everything on sign-out", () => {
    keepAnswer(null, "home", { rows: 1 });
    expect(cachedAnswer(null, "home")).toBeUndefined();
    keepAnswer("u1", "home", { rows: 1 });
    forgetAnswers();
    expect(cachedAnswer("u1", "home")).toBeUndefined();
  });
});
