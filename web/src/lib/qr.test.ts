// The QR encoder (lib/qr.ts). Every symbol below was decoded by a real reader (Apple's
// Vision framework) when the encoder was written, for versions 1 to 40, ASCII and
// Arabic text; these tests keep the structure and one exact symbol from drifting.
import { describe, expect, it } from "vitest";
import { encodeQR } from "./qr";
import { qrPath } from "../components/QRCode";

// "A", version 1-M, as the encoder made it and Vision read it back.
const GOLDEN_A = [
  "111111101001001111111",
  "100000101111101000001",
  "101110100010101011101",
  "101110101101101011101",
  "101110100111001011101",
  "100000100101101000001",
  "111111101010101111111",
  "000000001101100000000",
  "101101110011101001011",
  "010110010101111001000",
  "110011101101000001101",
  "101101000001001111100",
  "100111100100100100100",
  "000000001011001001001",
  "111111101001100101000",
  "100000101100000110110",
  "101110100110111110001",
  "101110101111001111110",
  "101110101010101100000",
  "100000100010010100101",
  "111111101000010010000",
];

const rows = (m: boolean[][]) => m.map((r) => r.map((b) => (b ? "1" : "0")).join(""));

function finderAt(m: boolean[][], x: number, y: number) {
  for (let dy = 0; dy < 7; dy++)
    for (let dx = 0; dx < 7; dx++) {
      const ring = Math.max(Math.abs(dx - 3), Math.abs(dy - 3));
      expect(m[y + dy]![x + dx], `finder at ${x},${y} (${dx},${dy})`).toBe(ring !== 2);
    }
}

describe("encodeQR", () => {
  it("makes the exact symbol a reader decoded", () => {
    expect(rows(encodeQR("A")!)).toEqual(GOLDEN_A);
  });

  it("picks the smallest version that holds the text", () => {
    const size = (s: string) => encodeQR(s)!.length;
    expect(size("A")).toBe(21); // version 1
    expect(size("x".repeat(14))).toBe(21); // version 1-M holds 14 bytes
    expect(size("x".repeat(15))).toBe(25);
    expect(size("x".repeat(2331))).toBe(177); // version 40-M holds 2331 bytes
    expect(encodeQR("x".repeat(2332))).toBeNull();
  });

  it("draws the finder, timing and dark-module patterns", () => {
    for (const text of [
      "A",
      "maktaba://connect?id=x&n=Home&u=http%3A%2F%2F192.168.1.5%3A8088",
      "بيت".repeat(40),
    ]) {
      const m = encodeQR(text)!;
      const n = m.length;
      finderAt(m, 0, 0);
      finderAt(m, n - 7, 0);
      finderAt(m, 0, n - 7);
      for (let i = 8; i < n - 8; i++) {
        expect(m[6]![i]).toBe(i % 2 === 0);
        expect(m[i]![6]).toBe(i % 2 === 0);
      }
      expect(m[n - 8]![8]).toBe(true);
    }
  });

  it("is the same symbol for the same text", () => {
    const s = "http://192.168.1.5:8088/pair?code=ABCD-1234";
    expect(rows(encodeQR(s)!)).toEqual(rows(encodeQR(s)!));
  });
});

describe("qrPath", () => {
  it("draws one unit square per dark module, past the quiet zone", () => {
    expect(
      qrPath(
        [
          [true, false],
          [false, true],
        ],
        4
      )
    ).toBe("M4 4h1v1h-1zM5 5h1v1h-1z");
  });
});
