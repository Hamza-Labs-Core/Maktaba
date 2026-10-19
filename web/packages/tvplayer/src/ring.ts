// The channel ring (Epic 34.6, design §8.1): only channels the viewer may
// watch, in number order, wrapping at both ends.

export interface RingChannel {
  id: string;
  number: number;
}

export class ChannelRing<C extends RingChannel> {
  readonly list: readonly C[];

  constructor(channels: readonly C[]) {
    this.list = [...channels].sort((a, b) => a.number - b.number);
  }

  get size(): number {
    return this.list.length;
  }

  index(id: string | null | undefined): number {
    return id ? this.list.findIndex((c) => c.id === id) : -1;
  }

  byId(id: string | null | undefined): C | null {
    const i = this.index(id);
    return i >= 0 ? this.list[i] : null;
  }

  byNumber(n: number): C | null {
    return this.list.find((c) => c.number === n) ?? null;
  }

  /** The channel `steps` away from `from` (next = +1), wrapping. */
  step(from: string | null | undefined, steps: number): C | null {
    const n = this.list.length;
    if (n === 0) return null;
    const i = this.index(from);
    const base = i === -1 ? (steps > 0 ? -1 : 0) : i;
    return this.list[(((base + steps) % n) + n) % n];
  }

  /** The previous and next channels of `id` (none for a ring of one). */
  neighbours(id: string): C[] {
    if (this.list.length < 2) return [];
    const prev = this.step(id, -1);
    const next = this.step(id, 1);
    const out: C[] = [];
    for (const c of [prev, next]) if (c && c.id !== id && !out.includes(c)) out.push(c);
    return out;
  }
}
