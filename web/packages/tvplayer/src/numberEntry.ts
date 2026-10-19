// Number entry (Epic 34.6 AC1, EC2): digits build a number shown in an
// overlay; Enter or 1.5 s without a digit commits it.

export const NUMBER_COMMIT_MS = 1500;
export const NUMBER_MAX_DIGITS = 4;

export interface Timers {
  set(fn: () => void, ms: number): unknown;
  clear(handle: unknown): void;
}

export const browserTimers: Timers = {
  set: (fn, ms) => setTimeout(fn, ms),
  clear: (h) => clearTimeout(h as ReturnType<typeof setTimeout>),
};

export class NumberEntry {
  private buf = "";
  private timer: unknown = null;

  constructor(
    private readonly onChange: (buffer: string) => void,
    private readonly onCommit: (n: number) => void,
    private readonly timers: Timers = browserTimers,
    private readonly commitMs = NUMBER_COMMIT_MS
  ) {}

  get buffer(): string {
    return this.buf;
  }

  digit(d: string): void {
    if (!/^[0-9]$/.test(d)) return;
    this.buf = (this.buf + d).slice(-NUMBER_MAX_DIGITS);
    this.onChange(this.buf);
    this.arm();
  }

  /** Enter: commits now. Reports whether there was a number to commit. */
  commit(): boolean {
    this.disarm();
    const b = this.buf;
    this.buf = "";
    this.onChange("");
    if (!b) return false;
    this.onCommit(Number(b));
    return true;
  }

  cancel(): void {
    this.disarm();
    if (this.buf) {
      this.buf = "";
      this.onChange("");
    }
  }

  private arm() {
    this.disarm();
    this.timer = this.timers.set(() => {
      this.timer = null;
      this.commit();
    }, this.commitMs);
  }

  private disarm() {
    if (this.timer !== null) this.timers.clear(this.timer);
    this.timer = null;
  }
}
