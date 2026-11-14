// The edge's clock. Tests move it (the Worker, its Durable Objects and the tests share one
// isolate under vitest-pool-workers), so "12 s later" or "11 minutes later" needs no sleep.
let offsetMs = 0;

export function now(): number {
  return Date.now() + offsetMs;
}

/** advanceClock moves the edge's clock forward (tests only). */
export function advanceClock(ms: number): void {
  offsetMs += ms;
}

/** resetClock puts the edge's clock back on real time (tests only). */
export function resetClock(): void {
  offsetMs = 0;
}
