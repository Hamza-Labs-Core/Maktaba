// A small QR code encoder (ISO/IEC 18004), without a dependency: byte mode, error
// correction level M, the smallest version (1-40) that holds the text, the mask with
// the lowest penalty. Enough for the "Connect an app" link and a sign-in page address.
//
// encodeQR(text) returns the modules, rows of booleans (true = dark), without the
// quiet zone; QRCode (components/QRCode.tsx) draws them.

// Error correction codewords per block, and blocks, for level M, by version (index 0
// unused).
const ECC_PER_BLOCK_M = [
  -1, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26, 30, 22, 22, 24, 24, 28, 28, 26, 26, 26, 26, 28, 28,
  28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28,
];
const BLOCKS_M = [
  -1, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5, 5, 8, 9, 9, 10, 10, 11, 13, 14, 16, 17, 17, 18, 20, 21, 23, 25,
  26, 28, 29, 31, 33, 35, 37, 38, 40, 43, 45, 47, 49,
];
// Level M's two format bits.
const FORMAT_BITS_M = 0;

/** The modules a version's data and error correction fill. */
function rawDataModules(ver: number): number {
  let result = (16 * ver + 128) * ver + 64;
  if (ver >= 2) {
    const align = Math.floor(ver / 7) + 2;
    result -= (25 * align - 10) * align - 55;
    if (ver >= 7) result -= 36;
  }
  return result;
}

/** The data codewords a version holds at level M. */
function dataCodewords(ver: number): number {
  return Math.floor(rawDataModules(ver) / 8) - ECC_PER_BLOCK_M[ver]! * BLOCKS_M[ver]!;
}

/** The character count's bits in byte mode. */
function countBits(ver: number): number {
  return ver <= 9 ? 8 : 16;
}

// --- Reed-Solomon over GF(2^8), polynomial 0x11D ---

function gfMul(x: number, y: number): number {
  let z = 0;
  for (let i = 7; i >= 0; i--) {
    z = (z << 1) ^ ((z >>> 7) * 0x11d);
    z ^= ((y >>> i) & 1) * x;
  }
  return z & 0xff;
}

function rsDivisor(degree: number): number[] {
  const result = new Array<number>(degree).fill(0);
  result[degree - 1] = 1;
  let root = 1;
  for (let i = 0; i < degree; i++) {
    for (let j = 0; j < result.length; j++) {
      result[j] = gfMul(result[j]!, root);
      if (j + 1 < result.length) result[j]! ^= result[j + 1]!;
    }
    root = gfMul(root, 0x02);
  }
  return result;
}

function rsRemainder(data: number[], divisor: number[]): number[] {
  const result = new Array<number>(divisor.length).fill(0);
  for (const b of data) {
    const factor = b ^ result.shift()!;
    result.push(0);
    divisor.forEach((coef, i) => {
      result[i]! ^= gfMul(coef, factor);
    });
  }
  return result;
}

/** The data codewords split in blocks, each with its error correction, interleaved. */
function withErrorCorrection(data: number[], ver: number): number[] {
  const numBlocks = BLOCKS_M[ver]!;
  const eccLen = ECC_PER_BLOCK_M[ver]!;
  const raw = Math.floor(rawDataModules(ver) / 8);
  const numShort = numBlocks - (raw % numBlocks);
  const shortLen = Math.floor(raw / numBlocks);
  const divisor = rsDivisor(eccLen);
  const blocks: number[][] = [];
  for (let i = 0, k = 0; i < numBlocks; i++) {
    const dat = data.slice(k, k + shortLen - eccLen + (i < numShort ? 0 : 1));
    k += dat.length;
    const ecc = rsRemainder(dat, divisor);
    if (i < numShort) dat.push(0);
    blocks.push(dat.concat(ecc));
  }
  const result: number[] = [];
  for (let i = 0; i < blocks[0]!.length; i++) {
    blocks.forEach((block, j) => {
      if (i !== shortLen - eccLen || j >= numShort) result.push(block[i]!);
    });
  }
  return result;
}

// --- the symbol ---

class QrSymbol {
  readonly size: number;
  readonly modules: boolean[][];
  readonly isFunction: boolean[][];
  readonly version: number;

  constructor(version: number) {
    this.version = version;
    this.size = version * 4 + 17;
    this.modules = Array.from({ length: this.size }, () =>
      new Array<boolean>(this.size).fill(false)
    );
    this.isFunction = Array.from({ length: this.size }, () =>
      new Array<boolean>(this.size).fill(false)
    );
  }

  set(x: number, y: number, dark: boolean): void {
    this.modules[y]![x] = dark;
    this.isFunction[y]![x] = true;
  }

  alignmentPositions(): number[] {
    if (this.version === 1) return [];
    const n = Math.floor(this.version / 7) + 2;
    const step = this.version === 32 ? 26 : Math.ceil((this.version * 4 + 4) / (n * 2 - 2)) * 2;
    const result = [6];
    for (let pos = this.size - 7; result.length < n; pos -= step) result.splice(1, 0, pos);
    return result;
  }

  drawFunctionPatterns(): void {
    for (let i = 0; i < this.size; i++) {
      this.set(6, i, i % 2 === 0);
      this.set(i, 6, i % 2 === 0);
    }
    this.drawFinder(3, 3);
    this.drawFinder(this.size - 4, 3);
    this.drawFinder(3, this.size - 4);
    const pos = this.alignmentPositions();
    const last = pos.length - 1;
    pos.forEach((px, i) =>
      pos.forEach((py, j) => {
        if ((i === 0 && j === 0) || (i === 0 && j === last) || (i === last && j === 0)) return;
        for (let dy = -2; dy <= 2; dy++)
          for (let dx = -2; dx <= 2; dx++)
            this.set(px + dx, py + dy, Math.max(Math.abs(dx), Math.abs(dy)) !== 1);
      })
    );
    this.drawFormat(0); // reserves the format areas; drawn for real once the mask is known
    this.drawVersion();
  }

  drawFinder(x: number, y: number): void {
    for (let dy = -4; dy <= 4; dy++) {
      for (let dx = -4; dx <= 4; dx++) {
        const dist = Math.max(Math.abs(dx), Math.abs(dy));
        const xx = x + dx;
        const yy = y + dy;
        if (xx >= 0 && xx < this.size && yy >= 0 && yy < this.size)
          this.set(xx, yy, dist !== 2 && dist !== 4);
      }
    }
  }

  drawFormat(mask: number): void {
    const data = (FORMAT_BITS_M << 3) | mask;
    let rem = data;
    for (let i = 0; i < 10; i++) rem = (rem << 1) ^ ((rem >>> 9) * 0x537);
    const bits = ((data << 10) | rem) ^ 0x5412;
    const bit = (i: number) => ((bits >>> i) & 1) !== 0;
    for (let i = 0; i <= 5; i++) this.set(8, i, bit(i));
    this.set(8, 7, bit(6));
    this.set(8, 8, bit(7));
    this.set(7, 8, bit(8));
    for (let i = 9; i < 15; i++) this.set(14 - i, 8, bit(i));
    for (let i = 0; i < 8; i++) this.set(this.size - 1 - i, 8, bit(i));
    for (let i = 8; i < 15; i++) this.set(8, this.size - 15 + i, bit(i));
    this.set(8, this.size - 8, true);
  }

  drawVersion(): void {
    if (this.version < 7) return;
    let rem = this.version;
    for (let i = 0; i < 12; i++) rem = (rem << 1) ^ ((rem >>> 11) * 0x1f25);
    const bits = (this.version << 12) | rem;
    for (let i = 0; i < 18; i++) {
      const dark = ((bits >>> i) & 1) !== 0;
      const a = this.size - 11 + (i % 3);
      const b = Math.floor(i / 3);
      this.set(a, b, dark);
      this.set(b, a, dark);
    }
  }

  drawCodewords(data: number[]): void {
    let i = 0;
    for (let right = this.size - 1; right >= 1; right -= 2) {
      if (right === 6) right = 5;
      for (let vert = 0; vert < this.size; vert++) {
        for (let j = 0; j < 2; j++) {
          const x = right - j;
          const upward = ((right + 1) & 2) === 0;
          const y = upward ? this.size - 1 - vert : vert;
          if (!this.isFunction[y]![x] && i < data.length * 8) {
            this.modules[y]![x] = ((data[i >>> 3]! >>> (7 - (i & 7))) & 1) !== 0;
            i++;
          }
        }
      }
    }
  }

  applyMask(mask: number): void {
    for (let y = 0; y < this.size; y++) {
      for (let x = 0; x < this.size; x++) {
        if (this.isFunction[y]![x]) continue;
        let invert: boolean;
        switch (mask) {
          case 0:
            invert = (x + y) % 2 === 0;
            break;
          case 1:
            invert = y % 2 === 0;
            break;
          case 2:
            invert = x % 3 === 0;
            break;
          case 3:
            invert = (x + y) % 3 === 0;
            break;
          case 4:
            invert = (Math.floor(x / 3) + Math.floor(y / 2)) % 2 === 0;
            break;
          case 5:
            invert = ((x * y) % 2) + ((x * y) % 3) === 0;
            break;
          case 6:
            invert = (((x * y) % 2) + ((x * y) % 3)) % 2 === 0;
            break;
          default:
            invert = (((x + y) % 2) + ((x * y) % 3)) % 2 === 0;
        }
        if (invert) this.modules[y]![x] = !this.modules[y]![x];
      }
    }
  }

  /** The standard penalty (runs, 2x2 blocks, finder-like patterns, balance). */
  penalty(): number {
    const n = this.size;
    const at = (x: number, y: number, rows: boolean) =>
      rows ? this.modules[y]![x]! : this.modules[x]![y]!;
    let score = 0;
    for (const rows of [true, false]) {
      for (let y = 0; y < n; y++) {
        let run = 1;
        for (let x = 1; x <= n; x++) {
          if (x < n && at(x, y, rows) === at(x - 1, y, rows)) {
            run++;
          } else {
            if (run >= 5) score += 3 + (run - 5);
            run = 1;
          }
        }
        for (let x = 0; x + 11 <= n; x++) {
          let a = true;
          let b = true;
          for (let k = 0; k < 11; k++) {
            const dark = at(x + k, y, rows);
            if (dark !== PATTERN_A[k]) a = false;
            if (dark !== PATTERN_B[k]) b = false;
          }
          if (a) score += 40;
          if (b) score += 40;
        }
      }
    }
    let dark = 0;
    for (let y = 0; y < n; y++) {
      for (let x = 0; x < n; x++) {
        const c = this.modules[y]![x]!;
        if (c) dark++;
        if (
          x + 1 < n &&
          y + 1 < n &&
          c === this.modules[y]![x + 1] &&
          c === this.modules[y + 1]![x] &&
          c === this.modules[y + 1]![x + 1]
        )
          score += 3;
      }
    }
    const total = n * n;
    score += (Math.ceil(Math.abs(dark * 20 - total * 10) / total) - 1) * 10;
    return score;
  }
}

const PATTERN_A = [true, false, true, true, true, false, true, false, false, false, false];
const PATTERN_B = [false, false, false, false, true, false, true, true, true, false, true];

/** The text's UTF-8 bytes. */
function utf8(text: string): number[] {
  return Array.from(new TextEncoder().encode(text));
}

/** The data codewords: byte mode, the count, the bytes, the terminator and padding. */
function dataFor(bytes: number[], ver: number): number[] {
  const bits: number[] = [];
  const put = (value: number, len: number) => {
    for (let i = len - 1; i >= 0; i--) bits.push((value >>> i) & 1);
  };
  put(0b0100, 4);
  put(bytes.length, countBits(ver));
  for (const b of bytes) put(b, 8);
  const capacity = dataCodewords(ver) * 8;
  put(0, Math.min(4, capacity - bits.length));
  put(0, (8 - (bits.length % 8)) % 8);
  const out: number[] = [];
  for (let i = 0; i < bits.length; i += 8) {
    let v = 0;
    for (let k = 0; k < 8; k++) v = (v << 1) | bits[i + k]!;
    out.push(v);
  }
  for (let pad = 0xec; out.length < capacity / 8; pad ^= 0xec ^ 0x11) out.push(pad);
  return out;
}

/** The QR code of text: rows of modules, true = dark; null when it is too long. */
export function encodeQR(text: string): boolean[][] | null {
  const bytes = utf8(text);
  let ver = 1;
  for (; ver <= 40; ver++) {
    if (4 + countBits(ver) + bytes.length * 8 <= dataCodewords(ver) * 8) break;
  }
  if (ver > 40) return null;
  const codewords = withErrorCorrection(dataFor(bytes, ver), ver);
  let best: boolean[][] | null = null;
  let bestScore = Infinity;
  for (let mask = 0; mask < 8; mask++) {
    const s = new QrSymbol(ver);
    s.drawFunctionPatterns();
    s.drawCodewords(codewords);
    s.applyMask(mask);
    s.drawFormat(mask);
    const score = s.penalty();
    if (score < bestScore) {
      bestScore = score;
      best = s.modules;
    }
  }
  return best;
}
