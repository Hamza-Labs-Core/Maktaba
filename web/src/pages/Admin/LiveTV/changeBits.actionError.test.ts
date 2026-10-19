// actionError: 409 reasons by their code; any other failure shows the
// server's own message (rollback is built, so no status is "not yet").
import { describe, it, expect } from "vitest";
import { actionError } from "./changeBits";
import { ApiError } from "../../../lib/api";

const t = (k: string, v?: Record<string, string | number>) =>
  v ? `${k}(${Object.values(v).join(",")})` : k;

const err = (status: number, extra: Record<string, unknown> = {}) =>
  new ApiError({ type: "about:blank", title: `T${status}`, status, ...extra } as never);

describe("actionError", () => {
  it("names the 409 reasons", () => {
    expect(actionError(t, err(409, { error: "second_admin_required" }))).toBe(
      "tvchanges.err.secondAdmin"
    );
    expect(actionError(t, err(409, { error: "change_in_flight" }))).toBe("tvchanges.err.inFlight");
    expect(actionError(t, err(409, { state: "live" }))).toBe(
      "tvchanges.err.state(tvchanges.state.live)"
    );
  });

  it("shows the server's message for any other status, 501 included", () => {
    expect(actionError(t, err(501, { detail: "nope" }))).toBe("T501: nope");
    expect(actionError(t, err(500))).toBe("T500");
    expect(actionError(t, new Error("offline"))).toBe("offline");
  });
});
