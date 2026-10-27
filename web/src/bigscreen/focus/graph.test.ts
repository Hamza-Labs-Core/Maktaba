import { describe, expect, it } from "vitest";
import { first, liesToward, next, score, type FocusNode } from "./graph";

// A row of `n` cards 100 wide with 20 between them, at height y.
function row(group: string, y: number, n: number, x0 = 0, w = 100, h = 60): FocusNode[] {
  return Array.from({ length: n }, (_, i) => ({
    id: `${group}${i}`,
    group,
    rect: { x: x0 + i * (w + 20), y, w, h },
  }));
}

// A grid of one group: `cols` per line, `lines` lines.
function grid(group: string, y0: number, cols: number, lines: number): FocusNode[] {
  const out: FocusNode[] = [];
  for (let l = 0; l < lines; l++)
    for (let c = 0; c < cols; c++)
      out.push({
        id: `${group}${l}-${c}`,
        group,
        rect: { x: c * 120, y: y0 + l * 80, w: 100, h: 60 },
      });
  return out;
}

const id = (n: FocusNode | null) => n?.id ?? null;

describe("liesToward and score", () => {
  const a = { x: 100, y: 100, w: 100, h: 50 };
  it("only what is past the centre that way", () => {
    expect(liesToward(a, { x: 220, y: 100, w: 100, h: 50 }, "right")).toBe(true);
    expect(liesToward(a, { x: 120, y: 100, w: 100, h: 50 }, "right")).toBe(false);
    expect(liesToward(a, { x: 0, y: 100, w: 90, h: 50 }, "left")).toBe(true);
    expect(liesToward(a, { x: 100, y: 200, w: 100, h: 50 }, "down")).toBe(true);
    expect(liesToward(a, { x: 100, y: 0, w: 100, h: 50 }, "up")).toBe(true);
    expect(liesToward(a, { x: 100, y: 0, w: 100, h: 50 }, "down")).toBe(false);
  });
  it("weighs the offset across twice the gap along", () => {
    const straight = { x: 100, y: 300, w: 100, h: 50 }; // 150 below
    const aside = { x: 260, y: 200, w: 100, h: 50 }; // 50 below, 160 aside
    expect(score(a, straight, "down")).toBe(150);
    expect(score(a, aside, "down")).toBe(50 + 2 * 160);
    expect(score(a, straight, "down")).toBeLessThan(score(a, aside, "down"));
  });
});

describe("next", () => {
  const tabs = row("tabs", 0, 4);
  const r1 = row("a", 100, 6);
  const r2 = row("b", 200, 6);
  const page = [...tabs, ...r1, ...r2];

  it("left and right walk a row", () => {
    expect(id(next(page, "a0", "right"))).toBe("a1");
    expect(id(next(page, "a3", "left"))).toBe("a2");
  });

  it("stays at a row's end instead of wrapping or jumping rows", () => {
    expect(next(page, "a5", "right")).toBeNull();
    expect(next(page, "a0", "left")).toBeNull();
  });

  it("a row with one card goes nowhere sideways", () => {
    const lone = [...tabs, ...row("solo", 100, 1)];
    expect(next(lone, "solo0", "right")).toBeNull();
    expect(next(lone, "solo0", "left")).toBeNull();
  });

  it("down lands straight below, and up from the first row reaches the tab row", () => {
    expect(id(next(page, "a2", "down"))).toBe("b2");
    expect(id(next(page, "a1", "up"))).toBe("tabs1");
    expect(next(page, "b2", "down")).toBeNull();
    expect(next(page, "tabs0", "up")).toBeNull();
  });

  it("returns to the card a row had focused (focus memory)", () => {
    const memory = new Map([["a", "a4"]]);
    expect(id(next(page, "tabs0", "down", memory))).toBe("a4");
    expect(id(next(page, "b0", "up", memory))).toBe("a4");
    // a remembered card no longer on the page is ignored
    expect(id(next(page, "b0", "up", new Map([["a", "gone"]])))).toBe("a0");
  });

  it("down reaches the next row even when its cards lie off to the side", () => {
    // a hero button on the right; a short row below it on the left; a long row further down
    const page2: FocusNode[] = [
      { id: "resume", group: "hero", rect: { x: 900, y: 0, w: 200, h: 60 } },
      ...row("short", 200, 2),
      ...row("long", 400, 12),
    ];
    expect(id(next(page2, "resume", "down"))).toBe("short1");
    expect(id(next(page2, "long8", "up"))).toBe("short1");
  });

  it("a shorter row below lands on its nearest card", () => {
    const short = [...row("a", 0, 6), ...row("b", 100, 2)];
    expect(id(next(short, "a5", "down"))).toBe("b1");
  });

  it("moves within a grid line by line, then on to what follows it", () => {
    const g = [...tabs, ...grid("g", 100, 4, 3), ...row("after", 400, 2)];
    expect(id(next(g, "g0-2", "down"))).toBe("g1-2");
    expect(id(next(g, "g1-2", "up"))).toBe("g0-2");
    expect(id(next(g, "g0-3", "right"))).toBeNull();
    expect(id(next(g, "g2-1", "down"))).toBe("after1");
    expect(id(next(g, "g0-1", "up"))).toBe("tabs1");
    // memory of a grid lands only on the line nearest the move
    expect(id(next(g, "after0", "up", new Map([["g", "g0-0"]])))).toBe("g2-0");
  });

  it("ungrouped buttons move freely", () => {
    const buttons: FocusNode[] = [
      { id: "play", rect: { x: 0, y: 0, w: 100, h: 50 } },
      { id: "start", rect: { x: 120, y: 0, w: 100, h: 50 } },
      { id: "chap", rect: { x: 0, y: 100, w: 100, h: 50 } },
    ];
    expect(id(next(buttons, "play", "right"))).toBe("start");
    expect(id(next(buttons, "start", "down"))).toBe("chap");
    expect(id(next(buttons, "chap", "up"))).toBe("play");
  });

  it("mirrors with the layout under RTL (rows start on the right)", () => {
    // an Arabic row: card 0 on the right, card 1 to its left
    const rtl: FocusNode[] = Array.from({ length: 3 }, (_, i) => ({
      id: `r${i}`,
      group: "r",
      rect: { x: 1000 - i * 120, y: 100, w: 100, h: 60 },
    }));
    expect(id(next(rtl, "r0", "left"))).toBe("r1");
    expect(id(next(rtl, "r1", "right"))).toBe("r0");
    expect(next(rtl, "r0", "right")).toBeNull();
    expect(id(first(rtl, true))).toBe("r0");
  });

  it("with nothing focused, any move starts at the first node", () => {
    expect(id(next(page, null, "down"))).toBe("tabs0");
    expect(id(next(page, "not-there", "left"))).toBe("tabs0");
    expect(next([], null, "down")).toBeNull();
  });
});

describe("first", () => {
  it("is the top line's start", () => {
    const nodes = [...row("b", 200, 3), ...row("a", 100, 3, 50)];
    expect(id(first(nodes))).toBe("a0");
    expect(id(first(nodes, true))).toBe("a2");
  });
});
