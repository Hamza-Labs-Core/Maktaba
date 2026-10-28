import { describe, it, expect } from "vitest";
import { cachedAnswer, forgetAnswers, keepAnswer, MAX_ANSWERS } from "./pageCache";

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

  it("forgets by key prefix, leaving the rest", () => {
    keepAnswer("u1", "playlists", 1);
    keepAnswer("u1", "playlist:p1", 2);
    keepAnswer("u1", "home", 3);
    forgetAnswers("playlist");
    expect(cachedAnswer("u1", "playlists")).toBeUndefined();
    expect(cachedAnswer("u1", "playlist:p1")).toBeUndefined();
    expect(cachedAnswer("u1", "home")).toBe(3);
  });

  it(`holds at most ${MAX_ANSWERS} answers, dropping the least recently used`, () => {
    for (let i = 0; i < MAX_ANSWERS; i++) keepAnswer("u1", `k${i}`, i);
    // a read makes k0 recent: k1 is now the oldest
    expect(cachedAnswer("u1", "k0")).toBe(0);
    keepAnswer("u1", "new", "x");
    expect(cachedAnswer("u1", "k1")).toBeUndefined();
    expect(cachedAnswer("u1", "k0")).toBe(0);
    expect(cachedAnswer("u1", "new")).toBe("x");
    expect(cachedAnswer("u1", `k${MAX_ANSWERS - 1}`)).toBe(MAX_ANSWERS - 1);
  });
});
