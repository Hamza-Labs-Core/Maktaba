// Story 16.6 — the flags client: 60 s cache, foreground refresh, the last good
// set kept on failure, unknown flags ignored, gated UI hidden (not disabled).
import { describe, it, expect, vi, beforeEach } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import { TestAuth, memberUser } from "../test/pageHarness";
import { Flag, FlagsClient, FlagsProvider, readBundle, useFlag, FLAGS_TTL_MS } from "./flags";

function bundle(flags: Record<string, unknown>) {
  return {
    flags,
    tier: "home",
    kid: "k1",
    issued_at: "2026-10-26T12:00:00Z",
    expires_at: "2026-10-26T12:01:00Z",
    signature: "c2ln",
  };
}

describe("readBundle", () => {
  it("keeps known boolean flags and ignores the rest", () => {
    expect(
      readBundle(
        bundle({ cloud_relay: true, federation: "yes", future_flag: true, early_access: false })
      )
    ).toEqual({ cloud_relay: true, early_access: false });
  });

  it("refuses an unsigned or malformed answer", () => {
    expect(readBundle({ ...bundle({ cloud_relay: true }), signature: "" })).toBeNull();
    expect(readBundle({ flags: { cloud_relay: true } })).toBeNull();
    expect(readBundle(null)).toBeNull();
  });
});

describe("FlagsClient", () => {
  let now = 0;
  const clock = () => now;
  beforeEach(() => {
    now = 1_000_000;
  });

  it("caches for 60 s, then fetches again", async () => {
    const load = vi.fn().mockResolvedValue(bundle({ cloud_relay: true }));
    const c = new FlagsClient(load, clock);
    await c.refresh();
    await c.refresh();
    expect(load).toHaveBeenCalledTimes(1);
    expect(c.isOn("cloud_relay")).toBe(true);
    now += FLAGS_TTL_MS;
    await c.refresh();
    expect(load).toHaveBeenCalledTimes(2);
  });

  it("a tier flip turns a premium flag off at the next refresh", async () => {
    const load = vi
      .fn()
      .mockResolvedValueOnce(bundle({ cloud_relay: true }))
      .mockResolvedValueOnce(bundle({ cloud_relay: false }));
    const c = new FlagsClient(load, clock);
    await c.refresh();
    now += FLAGS_TTL_MS;
    await c.refresh();
    expect(c.isOn("cloud_relay")).toBe(false);
  });

  it("keeps the last good set when a refresh fails or is refused", async () => {
    const load = vi
      .fn()
      .mockResolvedValueOnce(bundle({ cloud_relay: true }))
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce({ flags: { cloud_relay: false } }); // unsigned
    const c = new FlagsClient(load, clock);
    await c.refresh();
    await c.refresh(0);
    expect(c.isOn("cloud_relay")).toBe(true);
    await c.refresh(0);
    expect(c.isOn("cloud_relay")).toBe(true);
  });

  it("starts all-off and never turns a flag on after a failure", async () => {
    const c = new FlagsClient(vi.fn().mockRejectedValue(new Error("down")), clock);
    await c.refresh();
    expect(c.isOn("cloud_relay")).toBe(false);
    expect(c.isOn("flags_admin")).toBe(false);
  });

  it("clear drops the set and an answer that arrives after it", async () => {
    let resolve!: (v: unknown) => void;
    const c = new FlagsClient(() => new Promise((r) => (resolve = r)), clock);
    const pending = c.refresh();
    c.clear();
    resolve(bundle({ cloud_relay: true }));
    await pending;
    expect(c.isOn("cloud_relay")).toBe(false);
  });
});

function Gated() {
  const on = useFlag("cloud_relay");
  return (
    <>
      <span data-testid="state">{on ? "on" : "off"}</span>
      <Flag name="cloud_relay">
        <button type="button">Remote access</button>
      </Flag>
    </>
  );
}

describe("FlagsProvider", () => {
  it("hides gated UI until the server turns the flag on, and refreshes on focus", async () => {
    let now = 0;
    const load = vi
      .fn()
      .mockResolvedValueOnce(bundle({ cloud_relay: false }))
      .mockResolvedValueOnce(bundle({ cloud_relay: true }));
    const client = new FlagsClient(load, () => now);
    render(
      <TestAuth user={memberUser}>
        <FlagsProvider client={client}>
          <Gated />
        </FlagsProvider>
      </TestAuth>
    );
    await waitFor(() => expect(load).toHaveBeenCalledTimes(1));
    expect(screen.getByTestId("state")).toHaveTextContent("off");
    expect(screen.queryByRole("button", { name: "Remote access" })).toBeNull();

    now += 10_000;
    act(() => {
      window.dispatchEvent(new Event("focus"));
    });
    expect(await screen.findByRole("button", { name: "Remote access" })).toBeEnabled();
    expect(load).toHaveBeenCalledTimes(2);
  });

  it("asks nothing without a user, and a hook outside the provider is off", () => {
    const load = vi.fn();
    render(
      <TestAuth user={null}>
        <FlagsProvider client={new FlagsClient(load)}>
          <Gated />
        </FlagsProvider>
        <Gated />
      </TestAuth>
    );
    expect(load).not.toHaveBeenCalled();
    expect(screen.getAllByTestId("state").map((e) => e.textContent)).toEqual(["off", "off"]);
  });
});
