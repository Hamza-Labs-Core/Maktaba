// Story 16.5: what an error report may carry, and the coarse values sent.
import { describe, it, expect } from "vitest";
import { firstFrames, librarySizeBucket, routeFeature, sanitizeMessage } from "./sanitize";

describe("sanitize", () => {
  it("strips home folders on Unix and Windows", () => {
    expect(sanitizeMessage("ENOENT /Users/amira/Lectures/a.mp4")).toBe(
      "ENOENT <home>/Lectures/a.mp4"
    );
    expect(sanitizeMessage("open /home/sam/x")).toBe("open <home>/x");
    expect(sanitizeMessage("C:\\Users\\Amira\\Videos\\b.mkv")).toBe("<home>\\Videos\\b.mkv");
  });

  it("strips the server's address and query strings (a search lives in one)", () => {
    expect(sanitizeMessage("GET http://192.168.1.20:8088/api/search?q=fiqh failed")).toBe(
      "GET <origin>/api/search failed"
    );
  });

  it("keeps the first five frames, sanitized, without the message line", () => {
    const stack = [
      "TypeError: boom",
      ...Array.from(
        { length: 50 },
        (_, i) => `    at f${i} (http://10.0.0.2:8088/assets/app.js?v=1:1:${i})`
      ),
    ].join("\n");
    const out = firstFrames(stack).split("\n");
    expect(out).toHaveLength(5);
    expect(out[0]).toBe("at f0 (<origin>/assets/app.js)");
    expect(out.join("\n")).not.toContain("10.0.0.2");
  });

  it("maps a route to its area, never to an id", () => {
    expect(routeFeature("/videos/3f2a/watch")).toBe("player");
    expect(routeFeature("/videos/3f2a")).toBe("videos");
    expect(routeFeature("/search")).toBe("search");
    expect(routeFeature("/nope")).toBeNull();
  });

  it("reports a library's size as a range", () => {
    expect(librarySizeBucket(0)).toBe("0");
    expect(librarySizeBucket(42)).toBe("1-99");
    expect(librarySizeBucket(999)).toBe("100-999");
    expect(librarySizeBucket(5000)).toBe("1k-10k");
    expect(librarySizeBucket(10000)).toBe("10k+");
  });
});
