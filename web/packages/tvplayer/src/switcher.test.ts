// 34.6 TC2 — the switcher with a fake pipeline: make-before-break order,
// the surf debounce, one request in flight, EC1 (back to the channel still
// playing), telemetry.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ChannelRing } from "./ring";
import {
  ERROR_RETURN_MS,
  SURF_COMMIT_MS,
  SwitchError,
  Switcher,
  type Pipeline,
  type Stream,
  type SwitchEvent,
  type SwitcherState,
} from "./switcher";

interface Ch {
  id: string;
  number: number;
}
const chans: Ch[] = [
  { id: "a", number: 1 },
  { id: "b", number: 2 },
  { id: "c", number: 3 },
  { id: "d", number: 4 },
];

class FakePipeline implements Pipeline {
  log: string[] = [];
  private ready: (() => void) | null = null;
  private fail: ((e: unknown) => void) | null = null;
  prepare(stream: Stream, signal: AbortSignal): Promise<void> {
    this.log.push(`prepare ${stream.url}`);
    return new Promise((res, rej) => {
      this.ready = res;
      this.fail = rej;
      signal.addEventListener("abort", () => rej(new SwitchError("aborted")));
    });
  }
  canPlay() {
    this.ready?.();
  }
  breaks(e: unknown) {
    this.fail?.(e);
  }
  promote() {
    this.log.push("promote");
  }
  discard() {
    this.log.push("discard");
  }
  activeNative() {
    return false;
  }
}

const flush = async () => {
  for (let i = 0; i < 10; i++) await Promise.resolve();
};

function setup(resolveImpl?: (c: Ch) => Promise<Stream>) {
  const pipe = new FakePipeline();
  const states: SwitcherState<Ch>[] = [];
  const events: SwitchEvent[] = [];
  const log: string[] = pipe.log;
  const resolve = vi.fn(
    resolveImpl ??
      ((c: Ch) =>
        Promise.resolve<Stream>({
          url: `/${c.id}.m3u8`,
          release: () => log.push(`release ${c.id}`),
        }))
  );
  const sw = new Switcher<Ch>({
    ring: new ChannelRing(chans),
    resolve: (c) => resolve(c),
    pipeline: pipe,
    onState: (s) => states.push(s),
    telemetry: (e) => events.push(e),
    isWarm: (c) => c.id !== "d",
    retries: 1,
  });
  return { sw, pipe, states, events, resolve, log };
}

describe("Switcher", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("makes before it breaks: the old stream is released only after the new one can play", async () => {
    const { sw, pipe, log } = setup();
    sw.tune(chans[0]);
    await flush();
    pipe.canPlay();
    await flush();
    expect(sw.state.current?.id).toBe("a");
    sw.tune(chans[1]);
    await flush();
    expect(log.slice(-1)[0]).toBe("prepare /b.m3u8");
    expect(log).not.toContain("release a");
    pipe.canPlay();
    await flush();
    const i = log.indexOf("promote", log.indexOf("prepare /b.m3u8"));
    expect(i).toBeGreaterThan(0);
    expect(log.indexOf("release a")).toBeGreaterThan(i);
    expect(sw.state.current?.id).toBe("b");
    expect(sw.state.last?.id).toBe("a");
    expect(sw.state.target).toBeNull();
  });

  it("shows the target at once and commits 300 ms after the last press", async () => {
    const { sw, pipe, resolve, states } = setup();
    sw.tune(chans[0]);
    await flush();
    pipe.canPlay();
    await flush();
    resolve.mockClear();
    sw.surf(1);
    expect(states.at(-1)?.target?.id).toBe("b"); // the card, synchronously
    sw.surf(1);
    expect(states.at(-1)?.target?.id).toBe("c");
    vi.advanceTimersByTime(SURF_COMMIT_MS - 1);
    await flush();
    expect(resolve).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    await flush();
    expect(resolve).toHaveBeenCalledTimes(1);
    expect(resolve.mock.calls[0][0].id).toBe("c");
  });

  it("keeps one tune in flight: a newer one aborts the older and releases its stream", async () => {
    let n = 0;
    const releases: string[] = [];
    const { sw, pipe } = setup((c) => {
      n += 1;
      return Promise.resolve({ url: `/${c.id}.m3u8`, release: () => releases.push(c.id) });
    });
    sw.tune(chans[0]);
    await flush();
    pipe.canPlay();
    await flush();
    sw.tune(chans[1]); // loading b
    await flush();
    sw.tune(chans[2]); // aborts b
    await flush();
    expect(releases).toContain("b");
    pipe.canPlay();
    await flush();
    expect(sw.state.current?.id).toBe("c");
    expect(n).toBe(3);
  });

  it("returns to the channel still playing 3 s after a failed target (EC1)", async () => {
    const { sw, pipe } = setup((c) =>
      c.id === "b"
        ? Promise.reject(new SwitchError("remote_channels_full"))
        : Promise.resolve({ url: `/${c.id}.m3u8`, release() {} })
    );
    sw.tune(chans[0]);
    await flush();
    pipe.canPlay();
    await flush();
    sw.tune(chans[1]);
    await flush();
    expect(sw.state.phase).toBe("error");
    expect(sw.state.error?.code).toBe("remote_channels_full");
    expect(sw.state.target?.id).toBe("b");
    expect(sw.state.current?.id).toBe("a");
    vi.advanceTimersByTime(ERROR_RETURN_MS);
    expect(sw.state.phase).toBe("idle");
    expect(sw.state.target).toBeNull();
    expect(sw.state.current?.id).toBe("a");
  });

  it("frees the playing stream when the resolver asks (a per-user limit)", async () => {
    const releases: string[] = [];
    const sw = new Switcher<Ch>({
      ring: new ChannelRing(chans),
      pipeline: new FakePipeline(),
      onState: () => {},
      resolve: (c, ctx) => {
        if (c.id === "b") ctx.freeSlot();
        return Promise.resolve({ url: `/${c.id}`, release: () => releases.push(c.id) });
      },
    });
    const pipe = (sw as unknown as { o: { pipeline: FakePipeline } }).o.pipeline;
    sw.tune(chans[0]);
    await flush();
    pipe.canPlay();
    await flush();
    sw.tune(chans[1]);
    await flush();
    expect(releases).toEqual(["a"]);
    pipe.canPlay();
    await flush();
    expect(releases).toEqual(["a"]); // not released twice
  });

  it("emits switch telemetry with warm/cold", async () => {
    const { sw, pipe, events } = setup();
    sw.tune(chans[0]);
    await flush();
    pipe.canPlay();
    await flush();
    sw.tune(chans[3]);
    await flush();
    pipe.canPlay();
    await flush();
    expect(events).toHaveLength(2);
    expect(events[1]).toMatchObject({ from: "a", to: "d", path: "cold", prefetched: false });
    expect(events[0].path).toBe("warm");
    expect(events[1].ms_to_card).toBeGreaterThanOrEqual(0);
    expect(events[1].ms_to_first_frame).toBeGreaterThanOrEqual(events[1].ms_to_card);
  });

  it("goes back to the last channel", async () => {
    const { sw, pipe } = setup();
    sw.tune(chans[0]);
    await flush();
    pipe.canPlay();
    await flush();
    sw.tune(chans[2]);
    await flush();
    pipe.canPlay();
    await flush();
    sw.lastChannel();
    expect(sw.state.target?.id).toBe("a");
  });
});
